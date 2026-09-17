package builtin

import (
	"context"
	"testing"

	"agentworkflow/skill"
)

func TestSubmitSkillDefaultMessage(t *testing.T) {
	s := &SubmitSkill{}
	resp, err := s.Execute(context.Background(), skill.SkillRequest{NodeID: "submit-node"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Status != "SUCCESS" {
		t.Fatalf("status = %s", resp.Status)
	}
	if resp.Data["commit_message"] != "workflow submit: submit-node" {
		t.Errorf("default message = %v", resp.Data["commit_message"])
	}
	if resp.Data["submitted"] != true {
		t.Errorf("submitted flag = %v", resp.Data["submitted"])
	}
}

func TestSubmitSkillCustomMessageAndSummary(t *testing.T) {
	s := &SubmitSkill{}
	resp, err := s.Execute(context.Background(), skill.SkillRequest{
		NodeID:  "n",
		Args:    map[string]any{"message": "feat: add x"},
		Context: map[string]any{"summary": "did x"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Data["commit_message"] != "feat: add x" {
		t.Errorf("commit_message = %v", resp.Data["commit_message"])
	}
	if resp.Output != "submitted: feat: add x\nsummary: did x" {
		t.Errorf("output = %q", resp.Output)
	}
}

func TestLogSkill(t *testing.T) {
	s := &LogSkill{}
	resp, err := s.Execute(context.Background(), skill.SkillRequest{
		Args: map[string]any{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.Status != "SUCCESS" || resp.Output != "hello" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestRunTestSkill(t *testing.T) {
	s := &RunTestSkill{}
	ok, err := s.Execute(context.Background(), skill.SkillRequest{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ok.Status != "SUCCESS" || ok.Data["passed"] != true {
		t.Errorf("unexpected pass response: %+v", ok)
	}

	fail, err := s.Execute(context.Background(), skill.SkillRequest{
		Args: map[string]any{"fail": true},
	})
	if err != nil {
		t.Fatalf("execute fail: %v", err)
	}
	if fail.Status != "FAILED" || fail.Error == "" {
		t.Errorf("unexpected fail response: %+v", fail)
	}
}

func TestAllUniqueIDs(t *testing.T) {
	all := All()
	seen := map[string]bool{}
	for _, s := range all {
		if s.ID() == "" {
			t.Errorf("skill %s has empty ID", s.Name())
		}
		if seen[s.ID()] {
			t.Errorf("duplicate skill ID: %s", s.ID())
		}
		seen[s.ID()] = true
	}
	if len(all) != 3 {
		t.Errorf("All() len = %d, want 3", len(all))
	}
}
