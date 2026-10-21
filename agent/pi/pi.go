// Package pi 实现 Pi Agent 适配器。
// 职责:执行任务、修改代码、修复 Review 问题、运行测试、提交代码。
// 通过 pi-agent CLI(或可配置的本地命令)执行,权限由 Permission
// Manager 控制(可写文件、可 commit、禁止 push)。
package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/logx"
	"agentworkflow/permission"
	"agentworkflow/workflow/model"
)

// Config 是 Pi Agent 适配器配置。
type Config struct {
	// Bin 是 pi-agent 可执行文件路径,默认 "pi-agent"。
	Bin string
	// DefaultArgs 追加参数。
	DefaultArgs []string
	// DefaultTimeoutSeconds 默认超时。
	DefaultTimeoutSeconds int
	// Model 默认模型(节点级 req.Model 优先)。
	Model string
	// BaseURL API 端点 → 环境变量 PI_BASE_URL(仅子进程)。
	BaseURL string
	// AgentEnv 调用级环境变量(仅子进程,不改本地配置)。
	AgentEnv map[string]string
	// Perms 权限管理器(执行前校验)。
	Perms *permission.Manager
	// WaitDelaySeconds 是子进程退出后等待 stdout/stderr 管道关闭的上限
	// (秒)。管道被孤儿子进程持有时,Wait 在这个上限后返回
	// exec.ErrWaitDelay —— 此时进程已成功退出、输出完整,必须判成功。
	// 0 = 默认 5 秒。
	WaitDelaySeconds int
}

// Agent 是 PiAgent 实现。
type Agent struct {
	cfg Config
}

// New 创建适配器。
func New(cfg Config) *Agent {
	if cfg.Bin == "" {
		cfg.Bin = "pi-agent"
	}
	if cfg.DefaultTimeoutSeconds <= 0 {
		cfg.DefaultTimeoutSeconds = 900
	}
	if cfg.WaitDelaySeconds <= 0 {
		cfg.WaitDelaySeconds = 5
	}
	return &Agent{cfg: cfg}
}

// ID 实现 agent.Agent。
func (a *Agent) ID() string { return "pi-agent" }

// Name 实现 agent.Agent。
func (a *Agent) Name() string { return "Pi Agent" }

// Execute 实现 agent.Agent。
func (a *Agent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	// 权限前置检查:执行/修复/提交类模式需要写权限。
	//
	// 这里查的是**全局**策略;工作流级 permissions 段(运行期作用域,
	// 只能收紧)由运行期节点检查按**有效策略**(全局 ∧ 覆盖段)裁决 ——
	// 有效策略恒为全局的子集,两处结论不会相反(单一来源,见
	// permission 包的说明)。
	//
	// 本检查不是冗余:运行期节点检查覆盖的是**有效策略**(全局 ∧ 工作流
	// permissions 段),这里查的是全局策略 —— 有效策略恒为全局的子集,
	// 两处结论不会相反(单一来源,见 permission 包的说明)。
	//
	// mode 集合与运行期节点检查共用 agent.ModeRequiresWrite(唯一来源):
	// 两边各写一份必然漂移 —— 漂移方向是"某模式在一边要写权限、
	// 另一边不要",于是工作流级 permissions 段对那个模式失效。
	if coreagent.ModeRequiresWrite(req.Mode) {
		if a.cfg.Perms != nil {
			if err := a.cfg.Perms.Check(a.ID(), permission.FSWrite); err != nil {
				return nil, err
			}
		}
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = a.cfg.DefaultTimeoutSeconds
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	args := append([]string{}, a.cfg.DefaultArgs...)
	args = append(args,
		"--mode", req.Mode,
		"--task", req.Task,
	)
	// 模型:节点级(req.Model)> Agent 配置;仅作用于本次调用
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	} else if a.cfg.Model != "" {
		args = append(args, "--model", a.cfg.Model)
	}
	if req.Instructions != "" {
		args = append(args, "--instructions", req.Instructions)
	}
	if req.WorkingDir != "" {
		args = append(args, "--cwd", req.WorkingDir)
	}

	// argv 是本机可见的凭据出口:DefaultArgs(= Agent 配置的 extra_args)
	// 里若含凭据形态的值,启动前告警(只告警,绝不改写 argv —— 掩码后的值
	// 不是真凭据,下发过去 CLI 鉴权必失败)。见 agent.WarnCredentialArgs。
	coreagent.WarnCredentialArgs(a.ID(), a.cfg.DefaultArgs)

	cmd := exec.CommandContext(cctx, a.cfg.Bin, args...)
	// 取消/超时击杀整个进程组;WaitDelay 兜底关闭遗留管道,
	// 保证孤儿进程不会让调用方阻塞在 Output() 上。
	coreagent.ConfigureProcess(cmd)
	cmd.WaitDelay = time.Duration(a.cfg.WaitDelaySeconds) * time.Second
	if req.WorkingDir != "" {
		cmd.Dir = req.WorkingDir
	}
	// 环境:父环境 + Agent 配置(含 BaseURL)+ 请求级覆盖;
	// 全部为子进程级,不落盘、不改任何本地配置。
	env := cmd.Environ()
	env = append(env, coreagent.AgentConfig{
		BaseURL: a.cfg.BaseURL,
		Env:     a.cfg.AgentEnv,
	}.MergeEnv(req.Environment)...)
	cmd.Env = env
	stderr := coreagent.NewBoundedBuffer(0)
	cmd.Stderr = stderr

	// stdout 走带上限的缓冲区,不再无界读进内存(见 coreagent.BoundedBuffer)
	stdout := coreagent.NewBoundedBuffer(0)
	cmd.Stdout = stdout
	runErr := cmd.Run()
	if stdout.Truncated() {
		logx.Warn("pi-agent 输出超出上限,已截断", "limit", coreagent.MaxOutputBytes)
	}
	// 凭据脱敏:适配器是唯一知道注入值原貌的地方(约定 8)。
	// 父进程环境里的凭据同样算(子进程继承了它们)。
	redactor := coreagent.NewRedactor(a.cfg.AgentEnv, req.Environment, coreagent.ParentEnv())
	if errors.Is(runErr, exec.ErrWaitDelay) {
		// 进程已成功退出,仅孤儿子进程延迟关管道;输出已完整捕获。
		// 但那些孤儿必须**收掉**:它们还在后台跑并持有管道
		// (进程泄漏 —— 约定 3 的进程组击杀在成功路径上没有兑现)。
		coreagent.KillProcessGroup(cmd)
		return responseFor(stdout, stderr, redactor), nil
	}
	if runErr != nil {
		// 失败路径同样要收掉进程组:直接子进程已经死了,组里剩下的
		// 都是它留下的孤儿(可能还持有管道)。
		coreagent.KillProcessGroup(cmd)
		switch {
		case cctx.Err() == context.DeadlineExceeded:
			return nil, model.NewError(model.KindTimeoutError, "PI_TIMEOUT",
				fmt.Sprintf("pi-agent 执行超时(%ds)", timeout))
		case ctx.Err() == context.Canceled:
			return nil, model.NewError(model.KindCancelledError, "PI_CANCELLED", "执行被取消")
		}
		return nil, model.NewError(model.KindProcessError, "PI_PROCESS_ERROR",
			fmt.Sprintf("pi-agent 执行失败: %v; stderr: %s", runErr,
				redactor.Text(truncate(stderr.String(), 2000))))
	}
	return responseFor(stdout, stderr, redactor), nil
}

// responseFor 把子进程输出转成 Agent 响应。
//
// 输出被截断时结构化结果不可信(尾部 JSON 必然不完整),必须**显式失败**
// 并说明原因 —— 返回 SUCCESS + 空决策会让路由静默走默认分支,
// 正是约定 14 说的"该失败却成功"。
func responseFor(stdout *coreagent.BoundedBuffer, stderr *coreagent.BoundedBuffer, redactor *coreagent.Redactor) *coreagent.AgentResponse {
	if stdout.Truncated() {
		return &coreagent.AgentResponse{
			Status: coreagent.StatusFailed,
			Error: fmt.Sprintf("pi-agent 输出超出 %d 字节上限被截断,结构化结果不可信",
				coreagent.MaxOutputBytes),
			Output: stdout.String(), // 带显式截断标记,便于定位
		}
	}
	// stderr 先过脱敏再拼进错误消息(约定 8:CLI 常把凭据原样打进 stderr)。
	return redactor.RedactResponse(parseOutput(stdout.Bytes(), redactor.Text(stderr.String())))
}

// parseOutput 解析 pi-agent 输出。
// 约定:若输出末尾含 JSON 对象则解析结构化结果,否则原样返回。
//
// 两处与"禁止猜测"配套的显式口径:
//
//   - stdout 为空 / 只有空白 → **显式失败**。退出码 0 却什么都没产出
//     时,节点绿灯 + 决策为空 + 路由静默走默认分支 = 约定 14 说的
//     "该失败却成功"。
//   - 输出里没有**结构化决策** → 仍是成功(pi 的形态本就允许自由
//     文本,仓库示例工作流的 plan/execute 节点正是靠它产出上下文),
//     但必须**留痕**(decision_missing):静默成功比崩溃更难查。
func parseOutput(out []byte, stderr string) *coreagent.AgentResponse {
	text := string(out)
	resp := &coreagent.AgentResponse{Status: coreagent.StatusSuccess, Output: text}
	if strings.TrimSpace(text) == "" {
		// stderr 是这里**唯一**的诊断线索("额度耗尽"之类)—— 必须带进
		// 错误消息,否则用户只能自己去命令行重跑才知道出了什么事。
		resp.Status = coreagent.StatusFailed
		resp.Error = "pi-agent 未产生任何输出(进程退出码 0 但 stdout 为空)" + coreagent.StderrHint(stderr)
		return resp
	}
	if js := trailingJSONObject(text); js != "" {
		var structured struct {
			Status   string `json:"status"`
			Decision string `json:"decision"`
			Summary  string `json:"summary"`
			Error    string `json:"error"`
		}
		if err := json.Unmarshal([]byte(js), &structured); err == nil {
			if structured.Summary != "" {
				resp.Summary = structured.Summary
			}
			// 决策取值必须合法才进路由(非法取值留痕但不路由)。
			coreagent.ApplyDecision(resp, structured.Decision)
			// status 大小写不敏感:只精确匹配 "failed" 会让 "FAILED"
			// 形态的失败被当成成功(约定 14)。
			if strings.EqualFold(structured.Status, "failed") || structured.Error != "" {
				resp.Status = coreagent.StatusFailed
				resp.Error = structured.Error
			}
		}
	}
	if resp.Status == coreagent.StatusSuccess && resp.Decision == "" {
		markDecisionMissing(resp)
	}
	if resp.Summary == "" {
		resp.Summary = truncate(firstLines(text, 5), 500)
	}
	return resp
}

// markDecisionMissing 留下"没有结构化决策"的痕迹。
//
// 为什么要留痕:pi 的自由文本输出是合法形态(不是失败),但"没有决策"
// 意味着路由拿不到依据 —— 用户只看到一句"成功",无从判断模型到底
// 说了什么。痕迹让这件事**可判定**(约定 14 的"降级必须可判定")。
func markDecisionMissing(resp *coreagent.AgentResponse) {
	if resp.Data == nil {
		resp.Data = map[string]any{}
	}
	resp.Data["decision_missing"] = true
	const note = "(未解析出结构化决策)"
	if resp.Summary == "" {
		resp.Summary = note
		return
	}
	resp.Summary += note
}

// trailingJSONObject 从文本末尾取配平的 JSON 对象(见 coreagent 实现)。
func trailingJSONObject(text string) string { return coreagent.TrailingJSONObject(text) }

func firstLines(s string, n int) string {
	lines := strings.SplitN(strings.TrimSpace(s), "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
