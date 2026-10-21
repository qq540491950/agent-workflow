// Package git 封装本地 Git 能力。
// 支持的操作: status / diff / log / branch / checkout / commit。
// 调用方必须先通过 Permission Manager 校验权限,本包只负责执行与结构化输出。
package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/logx"
	"agentworkflow/workflow/model"
)

// Service 在指定工作目录上执行 git 操作。
type Service struct {
	// WorkingDir 是默认仓库目录。
	//
	// 由设置页写、**执行协程读**(git/script/agent 节点在跑时读它决定
	// cmd.Dir):Go 字符串是 (ptr, len) 二元组,非原子,无锁并发读写是
	// DATA RACE,撕裂后可产生非法指针。改由 wdMu 保护,读写一律走
	// WorkingDir()/SetWorkingDir()。
	wdMu       sync.RWMutex
	workingDir string
	// Bin 是 git 可执行文件路径,默认 "git"。
	//
	// 构造期字段:启动后不再修改。运行期如需修改请用 SetBin(带锁),
	// 直接写字段与并发执行存在数据竞争(约定 15)。
	Bin string
}

// New 创建 Git Service。
func New(workingDir string) *Service {
	return &Service{workingDir: workingDir, Bin: "git"}
}

// WorkingDir 读取默认仓库目录(并发安全)。
func (s *Service) WorkingDir() string {
	s.wdMu.RLock()
	defer s.wdMu.RUnlock()
	return s.workingDir
}

// SetWorkingDir 更新默认仓库目录(并发安全)。
func (s *Service) SetWorkingDir(dir string) {
	s.wdMu.Lock()
	defer s.wdMu.Unlock()
	s.workingDir = dir
}

// bin 读取 git 可执行文件路径(并发安全)。
func (s *Service) bin() string {
	s.wdMu.RLock()
	defer s.wdMu.RUnlock()
	if s.Bin == "" {
		return "git"
	}
	return s.Bin
}

// SetBin 更新 git 可执行文件路径(并发安全)。
func (s *Service) SetBin(bin string) {
	s.wdMu.Lock()
	defer s.wdMu.Unlock()
	s.Bin = bin
}

// Status 表示 git status 的结构化结果。
type Status struct {
	Branch    string   `json:"branch"`
	Modified  []string `json:"modified"`
	Staged    []string `json:"staged"`
	Untracked []string `json:"untracked"`
	Short     string   `json:"short"`
}

// LogEntry 是一条提交记录。
type LogEntry struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
}

// Run 执行任意受支持的 git 操作并返回文本输出。
func (s *Service) Run(ctx context.Context, operation string, args map[string]any) (string, error) {
	dir, _ := args["working_dir"].(string)
	if dir == "" {
		dir = s.WorkingDir()
	}
	var gitArgs []string
	switch operation {
	case "status":
		gitArgs = []string{"status"}
	case "diff":
		gitArgs = []string{"diff"}
		if staged, _ := args["staged"].(bool); staged {
			gitArgs = []string{"diff", "--cached"}
		}
	case "log":
		n, _ := args["max_count"].(int)
		if n == 0 {
			if nf, ok := args["max_count"].(float64); ok {
				n = int(nf)
			}
		}
		if n <= 0 {
			n = 10
		}
		gitArgs = []string{"log", "--oneline", fmt.Sprintf("-%d", n)}
	case "branch":
		gitArgs = []string{"branch"}
		if create, _ := args["name"].(string); create != "" {
			if err := checkRefName(create); err != nil {
				return "", err
			}
			gitArgs = []string{"branch", create}
		}
	case "checkout":
		branch, _ := args["branch"].(string)
		if branch == "" {
			return "", model.NewError(model.KindGitError, "GIT_MISSING_ARG", "checkout 需要 branch 参数")
		}
		if err := checkRefName(branch); err != nil {
			return "", err
		}
		gitArgs = []string{"checkout", branch}
	case "commit":
		msg, _ := args["message"].(string)
		if msg == "" {
			return "", model.NewError(model.KindGitError, "GIT_MISSING_ARG", "commit 需要 message 参数")
		}
		// 分两步:add_all 先暂存,再提交。
		// 注意:此前 add 分支把 gitArgs 整体覆盖成 ["add","-A"],导致
		// 随后的 exec 执行的是第二次 add,commit 从未发生却返回成功。
		if addAll, _ := args["add_all"].(bool); addAll {
			if _, err := s.exec(ctx, dir, []string{"add", "-A"}); err != nil {
				return "", err
			}
		}
		gitArgs = []string{"commit", "-m", msg}
	default:
		return "", model.NewError(model.KindGitError, "GIT_UNSUPPORTED_OP",
			fmt.Sprintf("不支持的 git 操作 %q", operation))
	}

	out, err := s.exec(ctx, dir, gitArgs)
	if err != nil {
		return "", err
	}
	return out, nil
}

// checkRefName 拒绝以 "-" 开头的引用名(分支名)。
// git 会把这种参数当作选项解析(如 git branch --delete),DSL 的 args
// 来自工作流定义,不能让节点参数注入 git 选项;checkout 也不能简单加
// "--" 分隔(那会被解释为 pathspec 而非分支)。
func checkRefName(name string) error {
	if strings.HasPrefix(name, "-") {
		return model.NewError(model.KindGitError, "GIT_INVALID_REF",
			fmt.Sprintf("分支名 %q 非法:不能以 - 开头", name))
	}
	return nil
}

// maskOutput 对"要返回给调用方的 git 命令输出"脱敏。
//
// ---- 为什么 git 包的出口必须脱敏(迭代 30,P0-1) ----
//
// Git 工作目录是**用户自己的代码库**:里面常有 .env、带密钥的配置文件、
// 凭据写死在配置里的脚本。此前 `GET /api/git/diff` **原样**返回 diff
// 文本 —— 凭据明文直接进 API 响应体,再由前端 GitPanel 渲染到屏幕
// (违反 AGENTS.md 约定 8:"API 返回掩码",备份导出含明文是唯一例外)。
//
// 口径:**与节点 output 出口同源**(logx.Mask = 键名 ∪ 值形态,
// 见 logx/credential.go 的分工说明)。为什么不用更松的"只按值形态":
//
//   - .env 里的密码多半是**低熵**的(DB_PASSWORD=hunter2)—— 值形态规则
//     判它"不是凭据",只靠值形态规则,秘密照样明文出站;
//   - 键名规则把这类也抓住(值 → ***),代价是 diff 里"键名像凭据"的
//     代码行也被遮(取舍见 docs/api.md 的 git 出口一节)。
//
// **哪些出口脱敏、哪些不脱敏**:
//
//   - 本文件里 Status / Diff / Log / Commit / CurrentBranch ——
//     **GitPanel 面**(经 app/application.GitService → REST / 桌面 IPC),
//     直接呈现给用户,必须脱敏;
//   - Run(工作流 git 节点用)—— **不**在这里脱敏:它的输出由应用层
//     统一处理(节点 output → logx.Mask;制品 artifact → 值形态规则,
//     见 workflow/runtime/executor.go),两处口径都是既有的、已锁定的
//     契约(制品要保住 `const token = getToken();` 的可读性 ——
//     workflow/runtime/masking_test.go 钉着这一条)。在这里再叠一层
//     键名规则会把制品的可读性毁掉,反而与制品出口的既定口径打架。
func maskOutput(s string) string { return logx.Mask(s) }

// Status 返回结构化状态。
func (s *Service) Status(ctx context.Context) (*Status, error) {
	short, err := s.exec(ctx, s.WorkingDir(), []string{"status", "--porcelain", "-b"})
	if err != nil {
		return nil, err
	}
	// 脱敏在**解析之前**:文件路径同样可能带凭据
	// (例如 "API_KEY=sk-….txt"),解析出来的路径也必须已脱敏。
	short = maskOutput(short)
	st := &Status{Modified: []string{}, Staged: []string{}, Untracked: []string{}}
	lines := strings.Split(strings.TrimRight(short, "\n"), "\n")
	for i, line := range lines {
		if i == 0 && strings.HasPrefix(line, "##") {
			st.Branch = strings.TrimSpace(strings.TrimPrefix(line, "##"))
			continue
		}
		if len(line) < 4 {
			continue
		}
		code, path := line[:2], strings.TrimSpace(line[3:])
		switch {
		case strings.HasPrefix(code, "??"):
			st.Untracked = append(st.Untracked, path)
		case strings.TrimSpace(code) != "" && code[0] != ' ':
			st.Staged = append(st.Staged, path)
		default:
			st.Modified = append(st.Modified, path)
		}
	}
	st.Short = short
	return st, nil
}

// Diff 返回当前未提交的改动;empty 时返回说明文本。
func (s *Service) Diff(ctx context.Context, staged bool) (string, error) {
	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	out, err := s.exec(ctx, s.WorkingDir(), args)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "(no diff)", nil
	}
	return maskOutput(out), nil
}

// Log 返回最近提交。
func (s *Service) Log(ctx context.Context, maxCount int) ([]LogEntry, error) {
	if maxCount <= 0 {
		maxCount = 10
	}
	out, err := s.exec(ctx, s.WorkingDir(), []string{
		"log", fmt.Sprintf("-%d", maxCount),
		"--pretty=format:%h%x09%an%x09%ad%x09%s", "--date=short",
	})
	if err != nil {
		return nil, err
	}
	// 提交信息(commit message)同样可能含凭据
	// ("chore: rotate ANTHROPIC_API_KEY=sk-…")→ 脱敏在解析之前。
	out = maskOutput(out)
	var entries []LogEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) != 4 {
			continue
		}
		entries = append(entries, LogEntry{Hash: parts[0], Author: parts[1], Date: parts[2], Subject: parts[3]})
	}
	return entries, nil
}

// Commit 执行 git add -A + commit,返回提交输出。
func (s *Service) Commit(ctx context.Context, message string) (string, error) {
	if _, err := s.exec(ctx, s.WorkingDir(), []string{"add", "-A"}); err != nil {
		return "", err
	}
	out, err := s.exec(ctx, s.WorkingDir(), []string{"commit", "-m", message})
	if err != nil {
		return "", err
	}
	return maskOutput(out), nil
}

// CurrentBranch 返回当前分支名。
func (s *Service) CurrentBranch(ctx context.Context) (string, error) {
	out, err := s.exec(ctx, s.WorkingDir(), []string{"rev-parse", "--abbrev-ref", "HEAD"})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(maskOutput(out)), nil
}

// exec 运行 git 子进程,处理超时与错误。
func (s *Service) exec(ctx context.Context, dir string, args []string) (string, error) {
	bin := s.bin()
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	// commit 触发的钩子等子进程可能挂起并持有输出管道:
	// 进程组击杀 + WaitDelay 兜底,保证调用方及时返回
	coreagent.ConfigureProcess(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	// 进程已成功退出、仅孤儿子进程延迟关管道时,输出已完整捕获,不算失败
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	if err != nil {
		// git 没有输出时(可执行文件不存在、参数被拒等)原因不能为空:
		// API 只回传 Message(err.Error() 不含 Detail),空原因会让用户
		// 只看到"git status 失败:"却不知道发生了什么。
		reason := strings.TrimSpace(string(out))
		if reason == "" {
			reason = err.Error()
		}
		// 失败原因也是"命令输出",同样可能带凭据
		// (例如 remote URL 里的 token)→ 出口脱敏。
		reason = maskOutput(reason)
		gitErr := model.NewError(model.KindGitError, "GIT_COMMAND_FAILED",
			fmt.Sprintf("git %s 失败: %s", strings.Join(args, " "), reason))
		gitErr.Detail = err.Error()
		if cctx.Err() == context.DeadlineExceeded {
			gitErr.Code = "GIT_TIMEOUT"
		} else if ctx.Err() == context.Canceled {
			gitErr.Code = "GIT_CANCELLED"
		}
		return "", gitErr
	}
	return string(out), nil
}
