package runtime

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/agent/mock"
	"agentworkflow/event"
	"agentworkflow/git"
	"agentworkflow/permission"
	"agentworkflow/persistence"
	"agentworkflow/skill"
	"agentworkflow/skill/builtin"
	"agentworkflow/workflow/dsl"
	"agentworkflow/workflow/model"
)

func newMinimalEngine(t *testing.T, extra ...skill.Skill) *Engine {
	t.Helper()
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	bus := event.NewBus()
	agents := coreagent.NewRegistry()
	if err := agents.Register(mock.New(mock.Options{ID: "mock-a"})); err != nil {
		t.Fatal(err)
	}
	skills := skill.NewRegistry()
	for _, s := range builtin.All() {
		if err := skills.Register(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range extra {
		if err := skills.Register(s); err != nil {
			t.Fatal(err)
		}
	}
	return NewEngine(agents, skills, permission.NewManager(), bus, git.New(t.TempDir()), repo)
}

// waitingSkill 是返回 WAIT_USER 的 Skill(接口文档承诺的状态);
// 首次调用 WAIT_USER,恢复重跑后 SUCCESS(模拟"用户已确认")。
type waitingSkill struct {
	calls  int32
	primed int32 // 预置已调用次数(跨"重启"恢复场景)
}

func (w *waitingSkill) ID() string          { return "wait-user" }
func (w *waitingSkill) Name() string        { return "Wait User" }
func (w *waitingSkill) Description() string { return "test skill returning WAIT_USER" }

func (w *waitingSkill) Execute(ctx context.Context, req skill.SkillRequest) (*skill.SkillResponse, error) {
	n := atomic.AddInt32(&w.calls, 1)
	if n == w.primed+1 {
		return &skill.SkillResponse{Status: "WAIT_USER", Output: "需要确认"}, nil
	}
	return &skill.SkillResponse{Status: "SUCCESS", Output: "已确认"}, nil
}

// 回归:Skill 返回 WAIT_USER 曾被静默当作 SUCCESS 继续执行;
// 接口文档承诺 SUCCESS | FAILED | WAIT_USER,应映射为 NodeWaiting 暂停。
func TestSkillWaitUserPauses(t *testing.T) {
	eng := newMinimalEngine(t, &waitingSkill{})

	yml := `
version: "1"
workflow:
  id: skill-wait
  name: Skill Wait
nodes:
  - id: confirm
    type: skill
    skill: wait-user
  - id: done
    type: skill
    skill: log
    args:
      message: done
edges:
  - from: confirm
    to: done
`
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("invalid: %+v", res.Errors)
	}
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, eng, exec.ID, model.ExecutionWaitingUser)

	// 恢复后继续完成
	if _, err := eng.Resume(context.Background(), exec.ID, map[string]any{"response": "approve"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	exec = waitForState(t, eng, exec.ID, model.ExecutionCompleted)
}

// 回归:script 节点派生的后台子进程持有输出管道时,CombinedOutput 曾
// 阻塞到子进程退出(60s);进程组击杀 + WaitDelay 后及时返回。
func TestScriptNodeOrphanPipe(t *testing.T) {
	eng := newMinimalEngine(t)

	yml := `
version: "1"
workflow:
  id: script-orphan
  name: Script Orphan
nodes:
  - id: bg
    type: script
    command: "sleep 60 & echo hi"
edges: []
`
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("invalid: %+v", res.Errors)
	}

	start := time.Now()
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, eng, exec.ID, model.ExecutionCompleted)
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("script node blocked %v on orphan child holding pipe", elapsed)
	}
	if state := exec.NodeStates["bg"]; state != string(model.NodeSuccess) {
		t.Errorf("bg state = %s, want success", state)
	}
}

// 回归:并发 Resume(双击批准)曾产生两份 goroutine 同时重放同一执行;
// 数据库级 CAS 保证恰好一个调用方成功。
func TestConcurrentResumeSingleWinner(t *testing.T) {
	eng := newMinimalEngine(t, &waitingSkill{})
	doc, err := dsl.Parse([]byte(`
version: "1"
workflow:
  id: race-resume
  name: Race Resume
nodes:
  - id: confirm
    type: skill
    skill: wait-user
  - id: done
    type: skill
    skill: log
    args:
      message: done
edges:
  - from: confirm
    to: done
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wf := doc.ToModel()
	if res := eng.Validate(wf); !res.Valid {
		t.Fatalf("invalid: %+v", res.Errors)
	}
	exec, err := eng.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	exec = waitForState(t, eng, exec.ID, model.ExecutionWaitingUser)

	// 并发发起两路 Resume
	var mu sync.Mutex
	wins := 0
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := eng.Resume(context.Background(), exec.ID, map[string]any{"response": "approve"}); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("并发 Resume 成功数 = %d, want 1", wins)
	}
	waitForState(t, eng, exec.ID, model.ExecutionCompleted)
}

// 回归:跨"进程"崩溃恢复。旧引擎实例"死亡"(不再推进)后,同一 SQLite 上
// 新建的引擎 RecoverPending 应把 RUNNING 执行标为 FAILED(可重试),
// 把 WAITING_USER 保持可恢复;随后 RetryNode/Resume 都能走完。
func TestCrashRecoveryAcrossEngines(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	mkEngine := func() *Engine {
		agents := coreagent.NewRegistry()
		agt := mock.New(mock.Options{ID: "mock-a", Scripts: map[string]*mock.Script{
			"plan": {SummaryTemplate: "ok"},
		}})
		if err := agents.Register(agt); err != nil {
			t.Fatal(err)
		}
		skills := skill.NewRegistry()
		for _, s := range builtin.All() {
			if err := skills.Register(s); err != nil {
				t.Fatal(err)
			}
		}
		eng := NewEngine(agents, skills, permission.NewManager(), event.NewBus(), git.New(t.TempDir()), repo)
		eng.Perms.Set("mock-a", permission.Policy{FilesystemRead: true})
		return eng
	}
	yml := `
version: "1"
workflow:
  id: crash-wf
  name: Crash WF
nodes:
  - id: a
    type: agent
    agent: mock-a
    mode: plan
  - id: b
    type: skill
    skill: log
    args:
      message: done
edges:
  - from: a
    to: b
`
	doc, err := dsl.Parse([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	wf := doc.ToModel()

	// 第一段:引擎 A 启动执行并在 a 节点完成后"崩溃"(直接弃用实例)
	engA := mkEngine()
	exec, err := engA.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		e, _ := repo.GetExecution(exec.ID)
		if e != nil && e.State == model.ExecutionCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 构造一个 RUNNING 假象:直接改库模拟"执行中途崩溃"
	raw, _ := repo.GetExecution(exec.ID)
	_ = raw
	crashed, _ := repo.GetExecution(exec.ID)
	crashed.State = model.ExecutionRunning
	_ = repo.SaveExecution(crashed)

	// 第二段:新引擎实例(模拟应用重启)
	engB := mkEngine()
	if err := engB.RecoverPending(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	after, _ := repo.GetExecution(exec.ID)
	if after.State != model.ExecutionFailed {
		t.Fatalf("恢复后状态 = %s, want FAILED(可重试)", after.State)
	}
	// 重试走通
	if _, err := engB.RetryNode(context.Background(), exec.ID, false); err != nil {
		t.Fatalf("retry: %v", err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		e, _ := repo.GetExecution(exec.ID)
		if e != nil && e.State == model.ExecutionCompleted {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("重试后未完成")
}

// WAITING_USER 执行在"重启"后保持可恢复(两个引擎共享同一 SQLite)。
// 新引擎的 waitingSkill 预置 primed=1:恢复重放时该节点重新执行,
// 第一次调用即返回 SUCCESS(模拟"用户已在崩溃前确认")。
func TestCrashRecoveryKeepsWaitingUser(t *testing.T) {
	repo, err := persistence.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	mkEngine := func(ws *waitingSkill) *Engine {
		agents := coreagent.NewRegistry()
		if err := agents.Register(mock.New(mock.Options{ID: "mock-a"})); err != nil {
			t.Fatal(err)
		}
		skills := skill.NewRegistry()
		for _, sk := range builtin.All() {
			if err := skills.Register(sk); err != nil {
				t.Fatal(err)
			}
		}
		if err := skills.Register(ws); err != nil {
			t.Fatal(err)
		}
		return NewEngine(agents, skills, permission.NewManager(), event.NewBus(), git.New(t.TempDir()), repo)
	}
	doc, err := dsl.Parse([]byte(`
version: "1"
workflow:
  id: wait-crash
  name: Wait Crash
nodes:
  - id: confirm
    type: skill
    skill: wait-user
edges: []
`))
	if err != nil {
		t.Fatal(err)
	}
	wf := doc.ToModel()

	eng1 := mkEngine(&waitingSkill{})
	exec, err := eng1.Start(context.Background(), wf, "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, eng1, exec.ID, model.ExecutionWaitingUser)

	// "重启":新引擎(共享 repo)+ RecoverPending
	eng2 := mkEngine(&waitingSkill{primed: 1})
	if err := eng2.RecoverPending(); err != nil {
		t.Fatal(err)
	}
	if _, err := eng2.Resume(context.Background(), exec.ID, map[string]any{"response": "approve"}); err != nil {
		t.Fatalf("resume after restart: %v", err)
	}
	waitForState(t, eng2, exec.ID, model.ExecutionCompleted)
}
