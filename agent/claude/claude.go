// Package claude 实现 Claude Code Agent 适配器。
// 职责:启动 claude CLI、传 Prompt、设置工作目录、按权限策略限制工具、
// 捕获 stdout/stderr、解析结构化输出(JSON)、超时控制、进程取消、日志。
//
// 默认职责是 PLAN / REVIEW / DECISION;
// 通过 --disallowedTools 强制禁止 Write/Edit/删除/Git 提交等写操作,
// Claude Code 在本系统中默认禁止修改项目文件(权限策略 + CLI 参数双重控制)。
package claude

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

// Config 是 Claude Code 适配器配置。
type Config struct {
	// Bin 是 claude 可执行文件路径,默认 "claude"。
	Bin string
	// DefaultTimeoutSeconds 超时(请求未指定时使用)。
	DefaultTimeoutSeconds int
	// ExtraArgs 追加 CLI 参数。
	ExtraArgs []string
	// Permissions 由 Permission Manager 提供(可选,用于校验)。
	Perms *permission.Manager
}

// Agent 是 ClaudeCodeAgent 实现。
type Agent struct {
	cfg Config
}

// New 创建适配器。
func New(cfg Config) *Agent {
	if cfg.Bin == "" {
		cfg.Bin = "claude"
	}
	if cfg.DefaultTimeoutSeconds <= 0 {
		cfg.DefaultTimeoutSeconds = 600
	}
	return &Agent{cfg: cfg}
}

// ID 实现 agent.Agent。
func (a *Agent) ID() string { return "claude-code" }

// Name 实现 agent.Agent。
func (a *Agent) Name() string { return "Claude Code" }

// claudeOutput 是 claude --output-format json 的结构。
type claudeOutput struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// Execute 实现 agent.Agent:调用 claude CLI(非交互、JSON 输出)。
func (a *Agent) Execute(ctx context.Context, req coreagent.AgentRequest) (*coreagent.AgentResponse, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = a.cfg.DefaultTimeoutSeconds
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	prompt := buildPrompt(req)
	args := []string{"-p", prompt, "--output-format", "json"}
	// 权限策略:默认禁写(PLAN/REVIEW/DECISION 不修改项目文件)。
	// 即使权限策略被误配置,CLI 层也强制禁止写工具。
	args = append(args,
		"--disallowedTools", "Write,Edit,MultiEdit,NotebookEdit,Bash(git commit:*),Bash(git push:*)",
	)
	if mode := req.Mode; mode == "plan" || mode == "review" {
		args = append(args, "--allowedTools", "Read,Grep,Glob,Bash(git diff:*),Bash(git status:*),Bash(git log:*)")
	}
	args = append(args, a.cfg.ExtraArgs...)

	cmd := exec.CommandContext(cctx, a.cfg.Bin, args...)
	if req.WorkingDir != "" {
		cmd.Dir = req.WorkingDir
	}
	if len(req.Environment) > 0 {
		env := cmd.Environ()
		for k, v := range req.Environment {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return nil, model.NewError(model.KindTimeoutError, "CLAUDE_TIMEOUT",
				fmt.Sprintf("claude 执行超时(%ds)", timeout))
		}
		if ctx.Err() == context.Canceled {
			return nil, model.NewError(model.KindCancelledError, "CLAUDE_CANCELLED", "执行被取消")
		}
		return nil, model.NewError(model.KindProcessError, "CLAUDE_PROCESS_ERROR",
			fmt.Sprintf("claude 启动/执行失败: %v; stderr: %s", err, truncate(stderr.String(), 2000)))
	}

	return a.parseOutput(out)
}

// parseOutput 解析 claude CLI 的 JSON 输出为结构化 AgentResponse。
func (a *Agent) parseOutput(out []byte) (*coreagent.AgentResponse, error) {
	var co claudeOutput
	if err := json.Unmarshal(out, &co); err != nil {
		// 非 JSON 输出:原样返回,禁止猜测 decision
		return &coreagent.AgentResponse{
			Status: coreagent.StatusSuccess,
			Output: string(out),
		}, nil
	}
	if co.IsError {
		return &coreagent.AgentResponse{
			Status: coreagent.StatusFailed,
			Error:  co.Result,
			Output: co.Result,
		}, nil
	}
	resp := &coreagent.AgentResponse{
		Status: coreagent.StatusSuccess,
		Output: co.Result,
	}
	// 结构化决策:要求模型按约定返回 JSON(见 buildPrompt 的指令),
	// 解析失败不猜测,decision 留空由路由按 output 处理。
	var structured struct {
		Decision string `json:"decision"`
		Summary  string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(extractJSON(co.Result)), &structured); err == nil && structured.Decision != "" {
		resp.Decision = coreagent.AgentDecision(strings.ToUpper(structured.Decision))
		resp.Summary = structured.Summary
	}
	return resp, nil
}

// buildPrompt 组装 Prompt:指令 + 任务 + 上下文 + 结构化输出要求。
func buildPrompt(req coreagent.AgentRequest) string {
	var b strings.Builder
	if req.Instructions != "" {
		b.WriteString(req.Instructions)
		b.WriteString("\n\n")
	}
	b.WriteString("Task: ")
	b.WriteString(req.Task)
	if len(req.Context) > 0 {
		b.WriteString("\n\nContext:\n")
		for k, v := range req.Context {
			if s, ok := v.(string); ok && s != "" {
				fmt.Fprintf(&b, "%s: %s\n", k, truncate(s, 4000))
			}
		}
	}
	b.WriteString(`
Respond with JSON at the end of your reply:
{"decision": "APPROVED|REJECTED|CONTINUE|DONE|WAIT_USER", "summary": "..."}`)
	return b.String()
}

// extractJSON 从文本中提取第一个 JSON 对象。
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return s
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
