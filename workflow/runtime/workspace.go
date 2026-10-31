package runtime

// 执行级工作区租约。
//
// 背景:所有执行共享同一个 Git 工作目录。两个编码工作流同时运行时,
// 会同时对同一棵工作树执行 checkout、改文件、git add/commit —— A 的提交
// 可能带上 B 的修改。租约把"同一目录上的副作用执行"串行化。
//
// 设计取舍:
//   - 只对**可能产生副作用**的工作流取租约(script / git 节点,或
//     execute/fix/submit 的 agent 节点)。只读的 plan/review 工作流仍可并发。
//   - 租约是进程内资源,不落库:进程重启后执行已被恢复逻辑接管,旧租约
//     自然消失。
//   - 多个目录按字典序获取,避免两个执行以不同顺序加锁造成死锁。

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"agentworkflow/workflow/model"
)

// WorkspaceLease 是一次执行持有的工作区租约。
type WorkspaceLease interface {
	Release()
}

// WorkspaceLeaseManager 负责获取/释放工作区租约。
type WorkspaceLeaseManager interface {
	Acquire(ctx context.Context, executionID string, dirs []string) (WorkspaceLease, error)
}

// NewWorkspaceLeaseManager 创建进程内实现。
func NewWorkspaceLeaseManager() WorkspaceLeaseManager {
	return &workspaceLeases{held: map[string]string{}}
}

type workspaceLeases struct {
	mu   sync.Mutex
	held map[string]string // dir -> executionID
}

type workspaceLease struct {
	m        *workspaceLeases
	execID   string
	dirs     []string
	released sync.Once
}

func (m *workspaceLeases) Acquire(_ context.Context, executionID string, dirs []string) (WorkspaceLease, error) {
	normalized := normalizeDirs(dirs)
	if len(normalized) == 0 {
		return noopWorkspaceLease{}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, dir := range normalized {
		if owner, ok := m.held[dir]; ok && owner != executionID {
			return nil, model.NewError(model.KindStateError, "WORKSPACE_BUSY",
				fmt.Sprintf("工作目录 %q 正被执行 %s 使用,请等待它结束或排队", dir, owner))
		}
	}
	for _, dir := range normalized {
		m.held[dir] = executionID
	}
	return &workspaceLease{m: m, execID: executionID, dirs: normalized}, nil
}

func (l *workspaceLease) Release() {
	if l == nil {
		return
	}
	l.released.Do(func() {
		l.m.mu.Lock()
		defer l.m.mu.Unlock()
		for _, dir := range l.dirs {
			if l.m.held[dir] == l.execID {
				delete(l.m.held, dir)
			}
		}
	})
}

type noopWorkspaceLease struct{}

func (noopWorkspaceLease) Release() {}

// normalizeDirs 规范化目录列表:绝对路径、Clean、去重、排序。
func normalizeDirs(dirs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			abs = filepath.Clean(d)
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	sort.Strings(out)
	return out
}

// workspaceDirsForWorkflow 返回一次执行需要租约的工作目录。
//
// 只有可能写文件的节点才触发租约:script、git、以及 mode 隐含写操作的
// agent。只读工作流不取租约,保持并发能力。
func workspaceDirsForWorkflow(wf *model.Workflow, defaultDir string) []string {
	if wf == nil {
		return nil
	}
	var dirs []string
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		needs := false
		switch n.Type {
		case model.NodeTypeScript, model.NodeTypeGit:
			needs = true
		case model.NodeTypeAgent:
			mode, _ := n.Config["mode"].(string)
			needs = model.ModeRequiresWrite(mode)
		}
		if !needs {
			continue
		}
		dir, _ := n.Config["working_dir"].(string)
		if strings.TrimSpace(dir) == "" {
			dir = defaultDir
		}
		dirs = append(dirs, dir)
	}
	return normalizeDirs(dirs)
}
