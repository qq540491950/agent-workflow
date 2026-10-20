package runtime

import (
	"context"
	"strings"
	"testing"

	"agentworkflow/event"
	"agentworkflow/permission"
	"agentworkflow/skill"
	"agentworkflow/workflow/compiler"
	"agentworkflow/workflow/model"
)

// 本文件锁住"节点 args 的类型守卫"。
//
// 此前运行期写法是:
//
//	args, _ := node.Config["args"].(map[string]any)
//
// 类型断言失败时 args 变成 nil,而**没有任何提示** —— 用户在节点上写的
// 参数全部静默丢失,Skill 拿到空参数照常执行,工作流照常成功。
// 正确语义:args 存在但不是对象 → 节点失败(fail closed)+ 可读原因;

// argsSkill 记录收到的 args。
type argsSkill struct {
	id   string
	args map[string]any
}

func (s *argsSkill) ID() string          { return s.id }
func (s *argsSkill) Name() string        { return "args skill" }
func (s *argsSkill) Description() string { return "records received args" }

func (s *argsSkill) Execute(ctx context.Context, req skill.SkillRequest) (*skill.SkillResponse, error) {
	s.args = req.Args
	return &skill.SkillResponse{Status: skill.StatusSuccess, Output: "ok"}, nil
}

// runSkillNodeWithArgs 跑一个 skill 节点(node 上只配 args)。
func runSkillNodeWithArgs(t *testing.T, args any, hasArgs bool) (*Engine, *argsSkill, compiler.NodeOutcome) {
	t.Helper()
	sk := &argsSkill{id: "args-skill"}
	eng := newMinimalEngine(t, sk)
	cfg := map[string]any{"skill": "args-skill"}
	if hasArgs {
		cfg["args"] = args
	}
	node := &model.Node{ID: "sn", Type: model.NodeTypeSkill, Config: cfg}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-args"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	return eng, sk, out
}

// TestSkillArgsWrongTypeFails args 不是对象 → 节点失败并说明原因。
func TestSkillArgsWrongTypeFails(t *testing.T) {
	cases := []struct {
		name string
		args any
	}{
		{"字符串", "message=hello"},
		{"数字", 42},
		{"列表", []any{"a", "b"}},
		{"布尔", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, sk, out := runSkillNodeWithArgs(t, tc.args, true)
			if out.State != model.NodeFailed {
				t.Fatalf("args 为 %s → 节点状态 %s,want %s", tc.name, out.State, model.NodeFailed)
			}
			if !strings.Contains(out.Error, "args") {
				t.Errorf("失败原因应点名 args,实际 %q", out.Error)
			}
			if sk.args != nil {
				t.Errorf("参数类型非法时不应把参数传给 Skill,实际 %v", sk.args)
			}
		})
	}
}

// TestSkillArgsMapReachesSkill 正常对象参数必须原样传给 Skill。
func TestSkillArgsMapReachesSkill(t *testing.T) {
	_, sk, out := runSkillNodeWithArgs(t, map[string]any{"message": "hi", "n": 3}, true)
	if out.State != model.NodeSuccess {
		t.Fatalf("合法 args → 节点状态 %s,want %s (%s)", out.State, model.NodeSuccess, out.Error)
	}
	if sk.args == nil {
		t.Fatal("Skill 未收到 args")
	}
	if got := sk.args["message"]; got != "hi" {
		t.Errorf("args.message = %v,want \"hi\"", got)
	}
	if got := sk.args["n"]; got != 3 {
		t.Errorf("args.n = %v,want 3", got)
	}
}

// TestSkillArgsAbsentOK 没配 args 是合法的(不是类型错误)。
func TestSkillArgsAbsentOK(t *testing.T) {
	_, sk, out := runSkillNodeWithArgs(t, nil, false)
	if out.State != model.NodeSuccess {
		t.Fatalf("未配 args → 节点状态 %s,want %s (%s)", out.State, model.NodeSuccess, out.Error)
	}
	if len(sk.args) != 0 {
		t.Errorf("未配 args 时 Skill 应收到空参数,实际 %v", sk.args)
	}
}

// TestSkillArgsNilOK args 显式写成空值(YAML "args:")等价于没配。
func TestSkillArgsNilOK(t *testing.T) {
	_, sk, out := runSkillNodeWithArgs(t, nil, true)
	if out.State != model.NodeSuccess {
		t.Fatalf("args 为空值 → 节点状态 %s,want %s (%s)", out.State, model.NodeSuccess, out.Error)
	}
	if len(sk.args) != 0 {
		t.Errorf("args 为空值时 Skill 应收到空参数,实际 %v", sk.args)
	}
}

// TestGitArgsWrongTypeFails git 节点同理:args 类型非法时不能静默丢参数
// (git 服务靠 args 里的 working_dir / staged / max_count / name 生效)。
func TestGitArgsWrongTypeFails(t *testing.T) {
	eng := newMinimalEngine(t)
	eng.Perms.Set("workflow", permission.Policy{GitRead: true})
	node := &model.Node{ID: "gn", Type: model.NodeTypeGit, Config: map[string]any{
		"operation": "status",
		"args":      "not-a-map",
	}}
	env := &compiler.RunEnv{
		Exec: &model.Execution{ID: "exec-git"}, WF: &model.Workflow{},
		Runner: eng, Bus: event.NewBus(), Vars: map[string]any{},
	}
	out := eng.ExecuteNode(context.Background(), env, node, &fakeState{m: map[string]any{}})
	if out.State != model.NodeFailed {
		t.Fatalf("git args 类型非法 → 节点状态 %s,want %s", out.State, model.NodeFailed)
	}
	if !strings.Contains(out.Error, "args") {
		t.Errorf("失败原因应点名 args,实际 %q", out.Error)
	}
}
