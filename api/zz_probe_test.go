package api

import (
	"fmt"
	"net/http"
	"testing"
)

func TestZZProbe(t *testing.T) {
	s := newTestServer(t)
	probes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/health", ""},
		{http.MethodGet, "/api/version", ""},
		{http.MethodGet, "/api/workflows/ghost/versions", ""},
		{http.MethodGet, "/api/workflows/ghost/export", ""},
		{http.MethodGet, "/api/executions/ghost/nodes", ""},
		{http.MethodGet, "/api/executions/ghost/events", ""},
		{http.MethodGet, "/api/executions/ghost/artifacts", ""},
		{http.MethodGet, "/api/executions/ghost/export", ""},
		{http.MethodPost, "/api/executions/ghost/cancel", ""},
		{http.MethodDelete, "/api/executions/ghost", ""},
		{http.MethodGet, "/api/agents/ghost/config", ""},
		{http.MethodPut, "/api/agents/ghost/config", `{"model":"m"}`},
		{http.MethodPost, "/api/agents/ghost/reset", ""},
		{http.MethodPost, "/api/agents/ghost/permission", `{"git_read":true}`},
		{http.MethodPost, "/api/agents/ghost/test", ""},
		{http.MethodGet, "/api/agents/claude-code/config", ""},
		{http.MethodPost, "/api/agents/claude-code/reset", ""},
		{http.MethodPost, "/api/agents/claude-code/test", ""},
		{http.MethodPost, "/api/skills/ghost/enable", `{"enabled":true}`},
		{http.MethodPost, "/api/skills/log/enable", `{}`},
		{http.MethodPost, "/api/skills/log/enable", `bad`},
		{http.MethodGet, "/api/git/status", ""},
		{http.MethodGet, "/api/git/diff", ""},
		{http.MethodGet, "/api/git/log", ""},
		{http.MethodPost, "/api/git/commit", `{"message":"  "}`},
		{http.MethodGet, "/api/settings", ""},
		{http.MethodPost, "/api/settings", `{"log_level":"debug"}`},
		{http.MethodGet, "/api/backup", ""},
		{http.MethodPost, "/api/backup/restore", `{"workflows":[],"agent_configs":{}}`},
		{http.MethodPost, "/api/backup/restore", `{`},
		{http.MethodGet, "/api/events/all", ""},
		{http.MethodGet, "/api/stats", ""},
		{http.MethodPost, "/api/workflows/validate", `{"id":"v1","name":"V","nodes":[{"id":"a","type":"agent","config":{"agent":"mock-claude","mode":"plan"}}],"edges":[]}`},
		{http.MethodPost, "/api/workflows/validate", `{"id":"v2","name":"V","nodes":[{"id":"a","type":"agent","config":{"agent":"nope","mode":"plan"}}],"edges":[]}`},
		{http.MethodPost, "/api/workflows/validate", `{`},
		{http.MethodPost, "/api/workflows/import", "not: [yaml"},
		{http.MethodPost, "/api/workflows/ghost/enable?value=true", ""},
		{http.MethodGet, "/api/workflows/wf-x/versions/abc", ""},
	}
	for _, p := range probes {
		code, m, raw := doReq(t, s, p.method, p.path, p.body)
		fmt.Printf("PROBE %s %s → %d %v | %.120s\n", p.method, p.path, code, m, raw)
	}
}
