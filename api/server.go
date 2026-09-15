// Package api 提供 HTTP Server 模式(REST API + SSE 实时事件 + 静态资源)。
// 与 Wails 绑定共享同一 Application 服务层,保证 UI 与后端解耦:
// 桌面模式走 Wails IPC,浏览器模式走 HTTP/SSE。
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"agentworkflow/app/application"
	"agentworkflow/event"
	"agentworkflow/logx"
	"agentworkflow/agent"
	"agentworkflow/permission"
	"agentworkflow/workflow/model"
)

func policyOf(m map[string]any) permission.Policy {
	get := func(k string) bool {
		b, _ := m[k].(bool)
		return b
	}
	return permission.Policy{
		FilesystemRead:  get("filesystem_read"),
		FilesystemWrite: get("filesystem_write"),
		GitRead:         get("git_read"),
		GitCommit:       get("git_commit"),
		GitPush:         get("git_push"),
	}
}

// Server 是 HTTP API 服务器。
type Server struct {
	app *application.App
	mux *http.ServeMux
}

// NewServer 创建服务器并注册路由。
func NewServer(app *application.App) *Server {
	s := &Server{app: app, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler 返回 http.Handler。
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/workflows", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		wfs, err := s.app.Workflows.List()
		return writeJSON(w, wfs, err)
	}))
	s.mux.HandleFunc("GET /api/workflows/{id}", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		wf, err := s.app.Workflows.Get(r.PathValue("id"))
		return writeJSON(w, wf, err)
	}))
	s.mux.HandleFunc("POST /api/workflows", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var wf map[string]any
		if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
			return badRequest(err.Error())
		}
		created, err := s.app.Workflows.Save(parseWorkflow(wf))
		return writeJSON(w, created, err)
	}))
	s.mux.HandleFunc("DELETE /api/workflows/{id}", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return s.app.Workflows.Delete(r.PathValue("id"))
	}))
	s.mux.HandleFunc("POST /api/workflows/{id}/duplicate", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		wf, err := s.app.Workflows.Duplicate(r.PathValue("id"))
		return writeJSON(w, wf, err)
	}))
	s.mux.HandleFunc("POST /api/workflows/{id}/enable", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		enabled := r.URL.Query().Get("value") != "false"
		wf, err := s.app.Workflows.SetEnabled(r.PathValue("id"), enabled)
		return writeJSON(w, wf, err)
	}))
	s.mux.HandleFunc("POST /api/workflows/validate", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var wf map[string]any
		if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
			return badRequest(err.Error())
		}
		return writeJSON(w, s.app.Workflows.Validate(parseWorkflow(wf)), nil)
	}))
	s.mux.HandleFunc("POST /api/workflows/import", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return badRequest(err.Error())
		}
		wf, err := s.app.Workflows.ImportYAML(string(body))
		return writeJSON(w, wf, err)
	}))
	s.mux.HandleFunc("GET /api/workflows/{id}/export", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		out, err := s.app.Workflows.ExportYAML(r.PathValue("id"))
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(out))
		return nil
	}))

	// 执行
	s.mux.HandleFunc("GET /api/executions", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		wfID := r.URL.Query().Get("workflow_id")
		limit := queryInt(r, "limit", 0)
		execs, err := s.app.Executions.List(wfID, limit)
		return writeJSON(w, execs, err)
	}))
	s.mux.HandleFunc("GET /api/executions/{id}", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		e, err := s.app.Executions.Get(r.PathValue("id"))
		return writeJSON(w, e, err)
	}))
	s.mux.HandleFunc("GET /api/executions/{id}/nodes", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		nodes, err := s.app.Executions.Nodes(r.PathValue("id"))
		return writeJSON(w, nodes, err)
	}))
	s.mux.HandleFunc("GET /api/executions/{id}/events", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		events, err := s.app.Executions.Events(r.PathValue("id"))
		return writeJSON(w, events, err)
	}))
	s.mux.HandleFunc("GET /api/executions/{id}/artifacts", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		arts, err := s.app.Executions.Artifacts(r.PathValue("id"))
		return writeJSON(w, arts, err)
	}))
	s.mux.HandleFunc("POST /api/workflows/{id}/run", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Task      string         `json:"task"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return badRequest(err.Error())
		}
		exec, err := s.app.Executions.Run(r.Context(), r.PathValue("id"), body.Task, body.Variables)
		return writeJSON(w, exec, err)
	}))
	s.mux.HandleFunc("POST /api/executions/{id}/input", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var response map[string]any
		if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
			return badRequest(err.Error())
		}
		exec, err := s.app.Executions.ProvideInput(r.Context(), r.PathValue("id"), response)
		return writeJSON(w, exec, err)
	}))
	s.mux.HandleFunc("GET /api/events/all", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		evs, err := s.app.Executions.AllEvents(r.URL.Query().Get("type"), queryInt(r, "limit", 200))
		return writeJSON(w, evs, err)
	}))
	s.mux.HandleFunc("POST /api/executions/{id}/retry", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Skip bool `json:"skip"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		exec, err := s.app.Executions.RetryNode(r.PathValue("id"), body.Skip)
		return writeJSON(w, exec, err)
	}))
	s.mux.HandleFunc("POST /api/executions/{id}/cancel", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		if err := s.app.Executions.Cancel(r.Context(), r.PathValue("id")); err != nil {
			return err
		}
		return writeJSON(w, map[string]any{"ok": true}, nil)
	}))

	// Agents / Skills / Git
	s.mux.HandleFunc("GET /api/agents", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return writeJSON(w, s.app.AgentSvc.List(), nil)
	}))
	s.mux.HandleFunc("POST /api/agents/{id}/permission", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			return badRequest(err.Error())
		}
		err := s.app.AgentSvc.SetPermission(r.PathValue("id"), policyOf(p))
		return writeJSON(w, map[string]any{"ok": err == nil}, err)
	}))
	s.mux.HandleFunc("GET /api/agents/{id}/config", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		cfg, err := s.app.AgentSvc.GetConfig(r.PathValue("id"))
		return writeJSON(w, cfg, err)
	}))
	s.mux.HandleFunc("PUT /api/agents/{id}/config", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			return badRequest(err.Error())
		}
		rawJSON, _ := json.Marshal(raw)
		var cfg agent.AgentConfig
		if err := json.Unmarshal(rawJSON, &cfg); err != nil {
			return badRequest(err.Error())
		}
		if err := s.app.AgentSvc.UpdateConfig(r.PathValue("id"), cfg); err != nil {
			return err
		}
		masked, err := s.app.AgentSvc.GetConfig(r.PathValue("id"))
		return writeJSON(w, masked, err)
	}))
	s.mux.HandleFunc("POST /api/agents/{id}/test", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		out, err := s.app.AgentSvc.Test(r.Context(), r.PathValue("id"))
		return writeJSON(w, map[string]any{"output": out}, err)
	}))
	s.mux.HandleFunc("GET /api/skills", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return writeJSON(w, s.app.SkillSvc.List(), nil)
	}))
	s.mux.HandleFunc("POST /api/skills/{id}/test", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		out, err := s.app.SkillSvc.Test(r.Context(), r.PathValue("id"))
		return writeJSON(w, map[string]any{"output": out}, err)
	}))
	s.mux.HandleFunc("GET /api/git/status", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		st, err := s.app.GitAPI.Status(r.Context())
		return writeJSON(w, st, err)
	}))
	s.mux.HandleFunc("GET /api/git/diff", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		out, err := s.app.GitAPI.Diff(r.Context(), r.URL.Query().Get("staged") == "true")
		return writeJSON(w, map[string]any{"diff": out}, err)
	}))
	s.mux.HandleFunc("POST /api/git/commit", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return badRequest(err.Error())
		}
		if strings.TrimSpace(body.Message) == "" {
			return badRequest("commit message 不能为空")
		}
		out, err := s.app.GitAPI.Commit(r.Context(), body.Message)
		return writeJSON(w, map[string]any{"output": out}, err)
	}))
	s.mux.HandleFunc("GET /api/git/log", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		logs, err := s.app.GitAPI.Log(r.Context(), queryInt(r, "limit", 10))
		return writeJSON(w, logs, err)
	}))

	// 设置
	s.mux.HandleFunc("GET /api/settings", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return writeJSON(w, s.app.Settings.Info(), nil)
	}))
	s.mux.HandleFunc("POST /api/settings", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			GitWorkingDir string `json:"git_working_dir"`
			LogLevel      string `json:"log_level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return badRequest(err.Error())
		}
		info, err := s.app.Settings.Update(body.GitWorkingDir, body.LogLevel)
		return writeJSON(w, info, err)
	}))

	// SSE 实时事件
	s.mux.HandleFunc("GET /api/events", s.handleSSE)
}

// wrap 统一错误处理与日志。
func (s *Server) wrap(h func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := h(w, r); err != nil {
			logx.Warn("http error", "path", r.URL.Path, "error", err)
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		}
	}
}

// handleSSE 推送实时 UIEvent。
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	unsub := s.app.Subscribe(func(ev event.UIEvent) {
		raw, err := json.Marshal(ev)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", raw)
		flusher.Flush()
	})
	defer unsub()

	// 客户端断开或超时退出
	select {
	case <-r.Context().Done():
	case <-s.shutdown(r):
	}
}

func (s *Server) shutdown(r *http.Request) <-chan struct{} {
	return r.Context().Done()
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, data any, err error) error {
	if err != nil {
		return err
	}
	if data == nil {
		data = map[string]any{}
	}
	return json.NewEncoder(w).Encode(data)
}

func badRequest(msg string) error { return fmt.Errorf("%s", msg) }

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}


func parseWorkflow(m map[string]any) *model.Workflow {
	raw, _ := json.Marshal(m)
	var wf model.Workflow
	_ = json.Unmarshal(raw, &wf)
	return &wf
}



