package persistence

import (
	"encoding/json"
	"strings"
	"testing"

	coreagent "agentworkflow/agent"
)

func TestAgentConfigPersistence(t *testing.T) {
	db := openTestDB(t)
	cfg := coreagent.AgentConfig{
		Model:     "claude-sonnet-4-5",
		BaseURL:   "https://relay.local",
		Env:       map[string]string{"ANTHROPIC_AUTH_TOKEN": "sk-xyz"},
		ExtraArgs: []string{"--verbose"},
	}
	raw, err := jsonMarshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAgentConfig("claude-code", raw); err != nil {
		t.Fatalf("save: %v", err)
	}
	// 覆盖保存
	cfg.Model = "claude-opus-4-6"
	raw, _ = jsonMarshal(cfg)
	if err := db.SaveAgentConfig("claude-code", raw); err != nil {
		t.Fatalf("resave: %v", err)
	}

	got, err := db.GetAgentConfig("claude-code")
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(string(got), "claude-opus-4-6") {
		t.Fatalf("upsert failed: %s", got)
	}

	all, err := db.ListAgentConfigs()
	if err != nil || len(all) != 1 {
		t.Fatalf("list: %v %v", all, err)
	}

	missing, err := db.GetAgentConfig("nope")
	if err != nil || missing != nil {
		t.Fatalf("missing should be nil,nil: %v %v", missing, err)
	}
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
