// Package pi 实现 Pi Agent 适配器。
// 职责:执行任务、修改代码、修复 Review 问题、运行测试、提交代码。
// 通过 pi-agent CLI(或可配置的本地命令)执行,权限由 Permission
// Manager 控制(可写文件、可 commit、禁止 push)。
package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	coreagent "agentworkflow/agent"
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
	cmd.WaitDelay = 5 * time.Second
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
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		switch {
		case cctx.Err() == context.DeadlineExceeded:
			return nil, model.NewError(model.KindTimeoutError, "PI_TIMEOUT",
				fmt.Sprintf("pi-agent 执行超时(%ds)", timeout))
		case ctx.Err() == context.Canceled:
			return nil, model.NewError(model.KindCancelledError, "PI_CANCELLED", "执行被取消")
		}
		return nil, model.NewError(model.KindProcessError, "PI_PROCESS_ERROR",
			fmt.Sprintf("pi-agent 执行失败: %v; stderr: %s", err, truncate(stderr.String(), 2000)))
	}
	return parseOutput(out), nil
}

// parseOutput 解析 pi-agent 输出。
// 约定:若输出末尾含 JSON 对象则解析结构化结果,否则原样返回。
func parseOutput(out []byte) *coreagent.AgentResponse {
	text := string(out)
	resp := &coreagent.AgentResponse{Status: coreagent.StatusSuccess, Output: text}
	if idx := strings.LastIndex(text, "{"); idx >= 0 {
		var structured struct {
			Status   string `json:"status"`
			Decision string `json:"decision"`
			Summary  string `json:"summary"`
			Error    string `json:"error"`
		}
		if err := json.Unmarshal([]byte(text[idx:]), &structured); err == nil {
			if structured.Summary != "" {
				resp.Summary = structured.Summary
			}
			if structured.Decision != "" {
				resp.Decision = coreagent.AgentDecision(strings.ToUpper(structured.Decision))
			}
			if structured.Status == "failed" || structured.Error != "" {
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
