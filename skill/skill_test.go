package skill

import (
	"context"
	"testing"
)

type fakeSkill struct {
	id   string
	fail bool
}

func (f *fakeSkill) ID() string          { return f.id }
func (f *fakeSkill) Name() string        { return "Fake " + f.id }
func (f *fakeSkill) Description() string { return "fake for tests" }

func (f *fakeSkill) Execute(ctx context.Context, req SkillRequest) (*SkillResponse, error) {
	if f.fail {
		return &SkillResponse{Status: "FAILED", Error: "injected"}, nil
	}
	return &SkillResponse{Status: "SUCCESS", Output: f.id}, nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	s := &fakeSkill{id: "submit"}
	if err := r.Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, err := r.Get("submit")
	if err != nil || got != s {
		t.Fatalf("Get(submit) = %v, %v", got, err)
	}
}

func TestRegistryRegisterNil(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("expected error registering nil skill")
	}
	if err := r.Register(&fakeSkill{id: ""}); err == nil {
		t.Fatal("expected error registering empty-id skill")
	}
}

func TestRegistryDuplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeSkill{id: "log"}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register(&fakeSkill{id: "log"}); err == nil {
		t.Fatal("expected duplicate register error")
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("nope"); err == nil {
		t.Fatal("expected error for unregistered skill")
	}
}

func TestRegistryListSorted(t *testing.T) {
	r := NewRegistry()
	for _, id := range []string{"run-test", "submit", "log"} {
		if err := r.Register(&fakeSkill{id: id}); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("List len = %d, want 3", len(list))
	}
	ids := []string{list[0].ID(), list[1].ID(), list[2].ID()}
	want := []string{"log", "run-test", "submit"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("List order = %v, want %v", ids, want)
		}
	}
}
