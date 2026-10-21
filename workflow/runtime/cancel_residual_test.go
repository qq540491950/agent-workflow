package runtime

// 本文件钉住"取消的残余窗口"的**当前语义**(迭代 17)。
//
// 背景:迭代 15(#264)把 `Engine.Cancel` 的"运行中"快路径改成**先读库**
// (约定 4 的唯一裁决来源),关掉了"终态写库 → 句柄摘除"那个窗口(几百 µs,
// 紧密轮询 40/40 命中)。
//
// 但**没有完全关闭**:读库是一次 DB 往返(实测 ≈0.3~0.7ms),而执行协程
// 的收尾(最后一次取消检查 → 终态 CAS → 终态事件同步落库)也在同一时间
// 尺度上。两者撞车时:
//
//	读库(看到 RUNNING)→ 执行提交 COMPLETED → 句柄查得到 → cancel()
//	→ return nil(200),而执行最终是 COMPLETED。
//
// **实测宽度**:随机到达时刻采样 220 次,Δ(到达时刻 − 协程最后一次取消
// 检查的时刻)在 [-300µs, +250µs] 是混合带,其中约一半落到 200 假成功
// (9/220 ≈ 4%);带外要么正常取消(CANCELLED),要么被拒
// (INVALID_TRANSITION)。窗口 ≈ **0.5ms**,宽度主要由读库耗时决定。
//
// **判断**:可接受(有意保留的边界)—— 状态与事件流仍然正确(库里是
// COMPLETED、只有一条 workflow.completed、没有 workflow.cancelled),
// 用户的"让它停下来"的意图其实已经满足(执行已经跑完),前端刷新后看到
// 的就是真实状态。彻底关闭需要让取消方也走 CAS(把 CANCELLED 的写入
// 从执行协程挪到 API 线程),那会改动取消流程的四条对外契约(见
// engine.go 里 Cancel 的说明),本轮不改。
//
// 本文件的作用:把"现在返回什么、状态最终是什么"**确定性地**钉死 ——
// 复刻时序不靠运气(探针 + 阻塞事件总线),将来若要按上述方案改,这条
// 用例就是验收标准。

import (
	"context"
	"testing"
	"time"

	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/workflow/model"
)

// cancelResidualYAML 是最小可跑工作流(单个 Agent 节点)。
const cancelResidualYAML = `
version: "1"
workflow:
  id: cancel-residual
  name: Cancel Residual
nodes:
  - id: work
    type: agent
    agent: mock-a
    mode: plan
edges: []
`

// TestCancelResidualWindowSemantics 残余窗口的当前语义。
//
// 复刻步骤(全部确定性,不靠 sleep 撞运气):
//
//  1. 在**终态事件**上阻塞:终态 CAS 已经落库(库里是 COMPLETED),
//     但执行协程还卡在 Emit 里 → `e.cancels` 里的句柄**还在**
//     (这正是窗口现场:"终态写库 → 句柄摘除"之间)。
//  2. 取消请求在**读到库里状态(RUNNING)之后**被探针挂住。
//  3. 先放开取消(协程仍卡在 Emit 里 → 句柄查得到)→ 取消走"运行中"
//     快路径 → 返回 nil(200)。
//  4. 再放开事件总线 → 协程收尾、摘掉句柄。
//
// 断言(当前语义):
//   - 取消返回 nil(即 API 回 200)—— 这就是残余窗口的"假成功";
//   - 执行最终是 COMPLETED(不是 CANCELLED);
//   - 事件流里**只有一条** workflow.completed,**没有** workflow.cancelled,
//     **没有** runtime.error(事件流与 DB 一致,约定 14);
//   - 状态提交失败计数为 0(取消没有把状态机搅乱)。
func TestCancelResidualWindowSemantics(t *testing.T) {
	agt := mock.New(mock.Options{
		ID: "mock-a",
		Scripts: map[string]*mock.Script{
			// 留出时间窗:取消请求一定在执行收尾之前发出。
			"plan": {DelayMS: 100, SummaryTemplate: "ok"},
		},
	})
	eng, repo, bus := newEngineWithAgent(t, agt)

	// ① 终态事件上阻塞(终态已落库,协程未返回 → 句柄还在)。
	completed := make(chan struct{})
	releaseBus := make(chan struct{})
	bus.Subscribe(func(ev event.UIEvent) {
		if ev.Type != event.WorkflowCompleted {
			return
		}
		close(completed)
		<-releaseBus
	})

	// ② 取消请求在"读到库里状态"之后被探针挂住。
	probed := make(chan struct{})
	releaseCancel := make(chan struct{})
	eng.cancelProbe = func() {
		close(probed)
		<-releaseCancel
	}

	wf := parseYAML(t, eng, cancelResidualYAML)
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, eng, exec.ID, model.ExecutionRunning)

	cancelErr := make(chan error, 1)
	go func() { cancelErr <- eng.Cancel(context.Background(), exec.ID) }()

	// ③ 等取消读到状态(RUNNING)并被挂住。
	select {
	case <-probed:
	case <-time.After(10 * time.Second):
		t.Fatal("取消请求没有走到探针(读库之后应被挂住)")
	}
	// 等执行提交终态:库里已经是 COMPLETED,协程卡在 Emit 里。
	select {
	case <-completed:
	case <-time.After(10 * time.Second):
		t.Fatal("执行未在期限内提交终态")
	}

	// ④ 放开取消:此刻协程仍卡在 Emit 里 → 句柄查得到 → 走"运行中"快路径。
	close(releaseCancel)
	var cancelErrVal error
	select {
	case cancelErrVal = <-cancelErr:
	case <-time.After(10 * time.Second):
		t.Fatal("取消请求没有返回")
	}
	// ⑤ 放开事件总线:协程收尾、摘掉句柄。
	close(releaseBus)

	// ---- 当前语义 ----
	if cancelErrVal != nil {
		t.Errorf("残余窗口内取消返回了错误 %v(当前语义:返回 nil,即 200 假成功)", cancelErrVal)
	}
	after := waitForState(t, eng, exec.ID,
		model.ExecutionCompleted, model.ExecutionFailed, model.ExecutionCancelled)
	if after.State != model.ExecutionCompleted {
		t.Errorf("最终状态 = %s, want COMPLETED(执行真的跑完了)", after.State)
	}
	stored, err := repo.GetExecution(exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if stored.State != model.ExecutionCompleted {
		t.Errorf("库里状态 = %s, want COMPLETED", stored.State)
	}
	// 事件流与 DB 必须一致(约定 14):只有一条 workflow.completed,
	// 没有 workflow.cancelled(状态没落成 CANCELLED 就不许宣布"已取消")。
	if n := countEvent(bus, event.WorkflowCompleted); n != 1 {
		t.Errorf("workflow.completed 事件数 = %d, want 1(事件: %v)", n, eventTypes(bus))
	}
	if n := countEvent(bus, event.WorkflowCancelled); n != 0 {
		t.Errorf("workflow.cancelled 事件数 = %d, want 0(状态没落成 CANCELLED 就不许发:", n)
		t.Errorf("  %v)", eventTypes(bus))
	}
	if hasEvent(bus, "runtime.error") {
		t.Errorf("残余窗口不该发 runtime.error(事件: %v)", eventTypes(bus))
	}
	if n := eng.StateCommitFailures(); n != 0 {
		t.Errorf("状态提交失败计数 = %d, want 0(取消没有把状态机搅乱)", n)
	}
}

// countEvent 统计事件流里某类事件的数量。
func countEvent(bus *event.Bus, typ string) int {
	n := 0
	for _, ev := range bus.History() {
		if ev.Type == typ {
			n++
		}
	}
	return n
}
