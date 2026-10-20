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
	// 权限前置检查:执行/修复类模式需要写权限
	if req.Mode == "execute" || req.Mode == "fix" || req.Mode == "submit" {
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
		// 进程已成功退出,仅孤儿子进程延迟关管道;输出已完整捕获
		return responseFor(stdout, redactor), nil
	}
	if runErr != nil {
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
	return responseFor(stdout, redactor), nil
}

// responseFor 把子进程输出转成 Agent 响应。
//
// 输出被截断时结构化结果不可信(尾部 JSON 必然不完整),必须**显式失败**
// 并说明原因 —— 返回 SUCCESS + 空决策会让路由静默走默认分支,
// 正是约定 14 说的"该失败却成功"。
func responseFor(stdout *coreagent.BoundedBuffer, redactor *coreagent.Redactor) *coreagent.AgentResponse {
	if stdout.Truncated() {
		return &coreagent.AgentResponse{
			Status: coreagent.StatusFailed,
			Error: fmt.Sprintf("pi-agent 输出超出 %d 字节上限被截断,结构化结果不可信",
				coreagent.MaxOutputBytes),
			Output: stdout.String(), // 带显式截断标记,便于定位
		}
	}
	return redactor.RedactResponse(parseOutput(stdout.Bytes()))
}

// parseOutput 解析 pi-agent 输出。
// 约定:若输出末尾含 JSON 对象则解析结构化结果,否则原样返回。
func parseOutput(out []byte) *coreagent.AgentResponse {
	text := string(out)
	resp := &coreagent.AgentResponse{Status: coreagent.StatusSuccess, Output: text}
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
	if resp.Summary == "" {
		resp.Summary = truncate(firstLines(text, 5), 500)
	}
	return resp
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
