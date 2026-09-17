package runtime

import (
	"context"
	"path/filepath"
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
	calls int32
}

func (w *waitingSkill) ID() string          { return "wait-user" }
func (w *waitingSkill) Name() string        { return "Wait User" }
func (w *waitingSkill) Description() string { return "test skill returning WAIT_USER" }

func (w *waitingSkill) Execute(ctx context.Context, req skill.SkillRequest) (*skill.SkillResponse, error) {
	if atomic.AddInt32(&w.calls, 1) == 1 {
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
