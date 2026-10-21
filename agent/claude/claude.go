// Package claude 实现 Claude Code Agent 适配器。
// 职责:启动 claude CLI、传 Prompt、设置工作目录、按权限策略限制工具、
// 捕获 stdout/stderr、解析结构化输出(JSON)、超时控制、进程取消、日志。
//
// 默认职责是 PLAN / REVIEW / DECISION;
// 通过 --disallowedTools 强制禁止 Write/Edit/删除/Git 提交等写操作,
// Claude Code 在本系统中默认禁止修改项目文件(权限策略 + CLI 参数双重控制)。
//
// 为什么本适配器**没有** Permission Manager 字段(迭代 13):
//   - 写操作:CLI 层已用 --disallowedTools 硬禁 Write/Edit/MultiEdit/
//     NotebookEdit/Bash(git commit:*)/Bash(git push:*) —— 适配器物理上
//     不可能写文件,"查写权限"在这里没有可观测的语义;
//   - 读操作:运行期的节点检查(workflow/runtime/executor.go)对**所有**
//     agent 节点都查 fs:read,查的是**有效策略**(全局 ∧ 工作流 permissions 段)
//     —— 比适配器能查到的全局策略更严,不会与适配器结论相反。
//
// 此前 Config 上挂着 Perms 字段但**从不读它**:调用方以为"权限已交给适配器
// 兜底",实际兜底根本不存在(与 default_working_dir 同族的死设置)。
// 留着它只会误导人 —— 删掉,让权限判定留在唯一的权威位置。
package claude

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
	// Model 默认模型(节点级 req.Model 优先)。
	Model string
	// BaseURL API 端点 → 环境变量 ANTHROPIC_BASE_URL(仅子进程)。
	BaseURL string
	// AgentEnv 调用级环境变量(仅子进程,不改本地配置)。
	AgentEnv map[string]string
	// WaitDelaySeconds 是子进程退出后等待 stdout/stderr 管道关闭的上限
	// (秒)。管道被孤儿子进程持有时,Wait 在这个上限后返回
	// exec.ErrWaitDelay —— 此时进程已成功退出、输出完整,必须判成功。
	// 0 = 默认 5 秒。
	WaitDelaySeconds int
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
	if cfg.WaitDelaySeconds <= 0 {
		cfg.WaitDelaySeconds = 5
	}
	return &Agent{cfg: cfg}
}

// resolveModel 决定本次调用模型:请求级 > 配置级。
func (a *Agent) resolveModel(reqModel string) string {
	if reqModel != "" {
		return reqModel
	}
	return a.cfg.Model
}

// agentConfig 以 agent.AgentConfig 形态暴露配置(供环境合并)。
func (a *Agent) agentConfig() coreagent.AgentConfig {
	return coreagent.AgentConfig{
		BaseURL: a.cfg.BaseURL,
		Env:     a.cfg.AgentEnv,
	}
}

// ID 实现 agent.Agent。
func (a *Agent) ID() string { return "claude-code" }

// Name 实现 agent.Agent。
func (a *Agent) Name() string { return "Claude Code" }

// claudeOutput 是 claude --output-format json 的结构。
type claudeOutput struct {
	Result  string   `json:"result"`
	IsError jsonBool `json:"is_error"`
}

// jsonBool 兼容 CLI 把 is_error 写成布尔 / 字符串 / 数字的几种形态。
//
// 形态无法识别时按"是错误"处理(fail closed):把一个看不懂的错误标志
// 当成成功,正是约定 14 说的"该失败却成功"。
// 识别失败**不能**让整个信封解析失败 —— 信封里还有 result 等关键字段,
// 整段丢弃会把"额度耗尽"当成正常输出返回。
type jsonBool bool

// UnmarshalJSON 实现 json.Unmarshaler。
func (b *jsonBool) UnmarshalJSON(data []byte) error {
	s := strings.ToLower(strings.Trim(strings.TrimSpace(string(data)), `"`))
	switch s {
	case "true", "1":
		*b = true
	case "false", "0", "null", "":
		*b = false
	default:
		*b = true // 形态未知 → 保守当作错误
	}
	return nil
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
	// 模型:节点级(req.Model)> Agent 配置;仅通过 --model 参数作用于本次调用
	if model := a.resolveModel(req.Model); model != "" {
		args = append(args, "--model", model)
	}
	// 权限策略:默认禁写(PLAN/REVIEW/DECISION 不修改项目文件)。
	// 即使权限策略被误配置,CLI 层也强制禁止写工具。
	args = append(args,
		"--disallowedTools", "Write,Edit,MultiEdit,NotebookEdit,Bash(git commit:*),Bash(git push:*)",
	)
	if mode := req.Mode; mode == "plan" || mode == "review" {
		args = append(args, "--allowedTools", "Read,Grep,Glob,Bash(git diff:*),Bash(git status:*),Bash(git log:*)")
	}
	args = append(args, a.cfg.ExtraArgs...)

	// argv 是本机可见的凭据出口:extra_args 里若含凭据形态的值,启动前
	// 告警(只告警,绝不改写 argv —— 掩码后的值不是真凭据,下发过去
	// CLI 鉴权必失败)。见 agent.WarnCredentialArgs。
	coreagent.WarnCredentialArgs(a.ID(), a.cfg.ExtraArgs)

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
	env = append(env, a.agentConfig().MergeEnv(req.Environment)...)
	cmd.Env = env

	// stdout/stderr 走带上限的缓冲区,不再无界读进内存
	// (见 coreagent.BoundedBuffer 与 MaxOutputBytes)
	stderr := coreagent.NewBoundedBuffer(0)
	cmd.Stderr = stderr
	stdout := coreagent.NewBoundedBuffer(0)
	cmd.Stdout = stdout
	runErr := cmd.Run()
	if stdout.Truncated() {
		logx.Warn("claude 输出超出上限,已截断", "limit", coreagent.MaxOutputBytes)
	}
	// 凭据脱敏:适配器是唯一知道注入值原貌的地方,CLI 常把环境变量
	// 原样打进 stderr,错误消息会把 stderr 拼进去 —— 凭据不能跟着走
	// (约定 8)。父进程环境里的凭据同样算(子进程继承了它们)。
	redactor := coreagent.NewRedactor(a.cfg.AgentEnv, req.Environment, coreagent.ParentEnv())
	if errors.Is(runErr, exec.ErrWaitDelay) {
		// 进程已成功退出,仅孤儿子进程延迟关管道;输出已完整捕获
		return a.responseFor(stdout, redactor)
	}
	if runErr != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return nil, model.NewError(model.KindTimeoutError, "CLAUDE_TIMEOUT",
				fmt.Sprintf("claude 执行超时(%ds)", timeout))
		}
		if ctx.Err() == context.Canceled {
			return nil, model.NewError(model.KindCancelledError, "CLAUDE_CANCELLED", "执行被取消")
		}
		return nil, model.NewError(model.KindProcessError, "CLAUDE_PROCESS_ERROR",
			fmt.Sprintf("claude 启动/执行失败: %v; stderr: %s", runErr,
				redactor.Text(truncate(stderr.String(), 2000))))
	}

	return a.responseFor(stdout, redactor)
}

// responseFor 把子进程输出转成 Agent 响应。
//
// 输出被截断时结构化结果不可信(尾部 JSON 必然不完整),必须**显式失败**
// 并说明原因 —— 返回 SUCCESS + 空决策会让路由静默走默认分支,
// 正是约定 14 说的"该失败却成功"。
func (a *Agent) responseFor(stdout *coreagent.BoundedBuffer, redactor *coreagent.Redactor) (*coreagent.AgentResponse, error) {
	if stdout.Truncated() {
		return &coreagent.AgentResponse{
			Status: coreagent.StatusFailed,
			Error: fmt.Sprintf("claude 输出超出 %d 字节上限被截断,结构化结果不可信",
				coreagent.MaxOutputBytes),
			Output: stdout.String(), // 带显式截断标记,便于定位
		}, nil
	}
	resp, err := a.parseOutput(stdout.Bytes())
	return redactor.RedactResponse(resp), err
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
		msg := co.Result
		if msg == "" {
			msg = "claude 返回 is_error=true 但没有错误详情"
		}
		return &coreagent.AgentResponse{
			Status: coreagent.StatusFailed,
			Error:  msg,
			Output: co.Result,
		}, nil
	}
	resp := &coreagent.AgentResponse{
		Status: coreagent.StatusSuccess,
		Output: co.Result,
	}
	// 结构化决策:要求模型按约定返回 JSON(见 buildPrompt 的指令)。
	// 解析失败不猜测,decision 留空由路由按 output 处理。
	var structured struct {
		Decision string `json:"decision"`
		Summary  string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(extractJSON(co.Result)), &structured); err == nil {
		if structured.Summary != "" {
			resp.Summary = structured.Summary
		}
		// 决策取值必须合法才进路由(非法取值留痕但不路由)。
		coreagent.ApplyDecision(resp, structured.Decision)
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

// extractJSON 从文本中提取结构化 JSON 对象。
// 优先取**尾部**那个(提示词要求模型把 JSON 放在回复末尾),
// 尾部没有才回退到第一个。
//
// 为什么不能只取第一个:模型正文里常先出现一个 JSON 片段 —— 最典型的
// 是把提示词里的示例原样抄回来。取第一个会把示例当成决策,
// 路由按示例走而不是按模型的实际结论走。
func extractJSON(s string) string {
	if js := coreagent.StructuredJSON(s); js != "" {
		return js
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
