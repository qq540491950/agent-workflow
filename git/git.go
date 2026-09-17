// Package git 封装本地 Git 能力。
// 支持的操作: status / diff / log / branch / checkout / commit。
// 调用方必须先通过 Permission Manager 校验权限,本包只负责执行与结构化输出。
package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	coreagent "agentworkflow/agent"
	"agentworkflow/workflow/model"
)

// Service 在指定工作目录上执行 git 操作。
type Service struct {
	// WorkingDir 是默认仓库目录。
	WorkingDir string
	// Bin 是 git 可执行文件路径,默认 "git"。
	Bin string
}

// New 创建 Git Service。
func New(workingDir string) *Service {
	return &Service{WorkingDir: workingDir, Bin: "git"}
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
		dir = s.WorkingDir
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
			gitArgs = []string{"branch", create}
		}
	case "checkout":
		branch, _ := args["branch"].(string)
		if branch == "" {
			return "", model.NewError(model.KindGitError, "GIT_MISSING_ARG", "checkout 需要 branch 参数")
		}
		gitArgs = []string{"checkout", branch}
	case "commit":
		msg, _ := args["message"].(string)
		if msg == "" {
			return "", model.NewError(model.KindGitError, "GIT_MISSING_ARG", "commit 需要 message 参数")
		}
		gitArgs = []string{"commit", "-m", msg}
		if addAll, _ := args["add_all"].(bool); addAll {
			gitArgs = []string{"add", "-A"}
			// 分两步:add 先执行
			if _, err := s.exec(ctx, dir, []string{"add", "-A"}); err != nil {
				return "", err
			}
		}
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

// Status 返回结构化状态。
func (s *Service) Status(ctx context.Context) (*Status, error) {
	short, err := s.exec(ctx, s.WorkingDir, []string{"status", "--porcelain", "-b"})
	if err != nil {
		return nil, err
	}
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
	out, err := s.exec(ctx, s.WorkingDir, args)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "(no diff)", nil
	}
	return out, nil
}

// Log 返回最近提交。
func (s *Service) Log(ctx context.Context, maxCount int) ([]LogEntry, error) {
	if maxCount <= 0 {
		maxCount = 10
	}
	out, err := s.exec(ctx, s.WorkingDir, []string{
		"log", fmt.Sprintf("-%d", maxCount),
		"--pretty=format:%h%x09%an%x09%ad%x09%s", "--date=short",
	})
	if err != nil {
		return nil, err
	}
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
	if _, err := s.exec(ctx, s.WorkingDir, []string{"add", "-A"}); err != nil {
		return "", err
	}
	out, err := s.exec(ctx, s.WorkingDir, []string{"commit", "-m", message})
	if err != nil {
		return "", err
	}
	return out, nil
}

// CurrentBranch 返回当前分支名。
func (s *Service) CurrentBranch(ctx context.Context) (string, error) {
	out, err := s.exec(ctx, s.WorkingDir, []string{"rev-parse", "--abbrev-ref", "HEAD"})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// exec 运行 git 子进程,处理超时与错误。
func (s *Service) exec(ctx context.Context, dir string, args []string) (string, error) {
	bin := s.Bin
	if bin == "" {
		bin = "git"
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	// commit 触发的钩子等子进程可能挂起并持有输出管道:
	// 进程组击杀 + WaitDelay 兜底,保证调用方及时返回
	coreagent.ConfigureProcess(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		gitErr := model.NewError(model.KindGitError, "GIT_COMMAND_FAILED",
			fmt.Sprintf("git %s 失败: %s", strings.Join(args, " "), strings.TrimSpace(string(out))))
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
