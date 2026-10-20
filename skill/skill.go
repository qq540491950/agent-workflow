// Package skill 定义 Skill 抽象与注册表。
// Skill 是独立扩展点(如 submit、run-test、git-commit),Workflow Runtime
// 不知道 Skill 的内部实现,只通过接口调用。
package skill

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"agentworkflow/workflow/model"
)

// SkillRequest 是一次 Skill 调用请求。
type SkillRequest struct {
	NodeID   string
	Args     map[string]any
	Context  map[string]any
	Executor string // 例如由哪个 agent 执行(pi-agent)
}

// Skill 状态取值。接口只有这三个,取值必须来自这里 ——
// 散落的字面量("SUCCESS"/"FAILED"/"WAIT_USER")迟早会大小写不一致,
// 而判定漏掉一种形态就意味着"该失败/该等待却成功"(约定 14)。
const (
	StatusSuccess  = "SUCCESS"
	StatusFailed   = "FAILED"
	StatusWaitUser = "WAIT_USER"
)

// NormalizeStatus 归一化 Skill 返回的状态:去首尾空白 + 转大写。
// 未知取值原样返回(大写化),由调用方决定如何处理。
func NormalizeStatus(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// SkillResponse 是 Skill 的结构化结果。
type SkillResponse struct {
	Status   string         `json:"status"` // SUCCESS | FAILED | WAIT_USER
	Output   string         `json:"output,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
	Error    string         `json:"error,omitempty"`
	Decision string         `json:"decision,omitempty"`
}

// Skill 是可注册的能力单元。
type Skill interface {
	ID() string
	Name() string
	Description() string
	Execute(ctx context.Context, req SkillRequest) (*SkillResponse, error)
}

// Registry 管理 Skill 的注册与查找。
type Registry interface {
	Register(s Skill) error
	Get(id string) (Skill, error)
	List() []Skill
}

type registry struct {
	mu     sync.RWMutex
	skills map[string]Skill
}

// NewRegistry 创建空的 Skill 注册表。
func NewRegistry() Registry {
	return &registry{skills: map[string]Skill{}}
}

func (r *registry) Register(s Skill) error {
	if s == nil || s.ID() == "" {
		return fmt.Errorf("skill: register requires non-nil skill with id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.skills[s.ID()]; exists {
		return fmt.Errorf("skill: %q already registered", s.ID())
	}
	r.skills[s.ID()] = s
	return nil
}

func (r *registry) Get(id string) (Skill, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[id]
	if !ok {
		// 结构化错误:与 agent 注册表同源,让 REST 层能映射 404 +
		// code=NOT_FOUND,而不是裸 fmt.Errorf 只能映射成 400 且无 code。
		return nil, model.NewError(model.KindValidationError, "NOT_FOUND",
			fmt.Sprintf("Skill %q 不存在", id))
	}
	return s, nil
}

func (r *registry) List() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// DTO 是给前端的 Skill 描述。
type DTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}
