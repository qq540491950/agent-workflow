// Package api 提供 HTTP Server 模式(REST API + SSE 实时事件 + 静态资源)。
// 与 Wails 绑定共享同一 Application 服务层,保证 UI 与后端解耦:
// 桌面模式走 Wails IPC,浏览器模式走 HTTP/SSE。
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"agentworkflow/agent"
	"agentworkflow/app/application"
	"agentworkflow/event"
	"agentworkflow/logx"
	"agentworkflow/permission"
	"agentworkflow/workflow/dsl"
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
// 顺接通 Bus → UI 处理器的桥接(SSE 依赖;幂等,桌面模式重复调用无害)。
func NewServer(app *application.App) *Server {
	app.ConnectEvents()
	s := &Server{app: app, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler 返回 http.Handler。
// 额外挂一层错误体拦截:net/http ServeMux 对"方法不匹配"(405)与
// "路由不存在"(404)直接写纯文本,与本 API 的 JSON 错误体契约不一致
// (前端 http() 解析不出 error 字段,只能回退成 "HTTP 405")。
func (s *Server) Handler() http.Handler { return jsonErrorHandler(s.mux) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		info := VersionInfo()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "version": info["version"], "mode": "server",
			"adk": info["adk"], "wails": info["wails"],
		})
	})
	s.mux.HandleFunc("GET /api/version", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return writeJSON(w, VersionInfo(), nil)
	}))
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
		if err := decodeJSON(r, &wf); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
		}
		parsed, err := parseWorkflow(wf)
		if err != nil {
			return err
		}
		created, err := s.app.Workflows.Save(parsed)
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
		// 显式布尔,fail closed:此前 `value != "false"` 让缺失参数、?value=0、
		// ?value=no、?value=False 全部判为"启用" —— 一个禁用开关缺参数
		// 反而打开工作流。只接受显式 true/false,其余一律 400。
		raw := r.URL.Query().Get("value")
		var enabled bool
		switch raw {
		case "true":
			enabled = true
		case "false":
			enabled = false
		default:
			return badRequest("需要 value=true|false 参数")
		}
		wf, err := s.app.Workflows.SetEnabled(r.PathValue("id"), enabled)
		return writeJSON(w, wf, err)
	}))
	s.mux.HandleFunc("POST /api/workflows/validate", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var wf map[string]any
		if err := decodeJSON(r, &wf); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
		}
		parsed, err := parseWorkflow(wf)
		if err != nil {
			return err
		}
		return writeJSON(w, s.app.Workflows.Validate(parsed), nil)
	}))
	s.mux.HandleFunc("GET /api/workflows/{id}/versions", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		vers, err := s.app.Workflows.Versions(r.PathValue("id"))
		return writeJSON(w, vers, err)
	}))
	s.mux.HandleFunc("GET /api/workflows/{id}/versions/{version}", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		v, err := parseVersion(r.PathValue("version"))
		if err != nil {
			return badRequest(err.Error())
		}
		out, err := s.app.Workflows.VersionDSL(r.PathValue("id"), v)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(out))
		return nil
	}))
	s.mux.HandleFunc("POST /api/workflows/import", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		// 上限与 JSON 端点一致(1MB);多读 1 字节用于识别超限,
		// 否则超长内容会被静默截断,报出与真实原因无关的 YAML 解析错误。
		body, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBody+1))
		if err != nil {
			return badRequest(err.Error())
		}
		if len(body) > maxJSONBody {
			return model.NewError(model.KindValidationError, "PAYLOAD_TOO_LARGE",
				fmt.Sprintf("导入内容超过 %d MB 上限", maxJSONBody>>20))
		}
		wf, err := s.app.Workflows.ImportYAML(string(body))
		if err != nil {
			return writeJSON(w, wf, err)
		}
		// 附上"被忽略的键"清单:DSL 解析器把闭合结构里的未知键丢掉了
		// (记入 Document.IgnoredKeys + WARN 日志),但 IgnoredKeys 不参与
		// 序列化,用户此前**看不到** —— 导入成功 ≠ 每个设置都生效了。
		//
		// 响应形状:工作流字段仍在**顶层**(前端 api.importYAML 直接把响应
		// 当 Workflow 用,契约不变),额外附加一个 ignored_keys 数组。
		ignored := dslIgnoredKeys(body)
		if len(ignored) == 0 {
			return writeJSON(w, wf, nil)
		}
		out, err := workflowJSONMap(wf)
		if err != nil {
			return err
		}
		out["ignored_keys"] = ignored
		return writeJSON(w, out, nil)
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
	s.mux.HandleFunc("GET /api/executions/{id}/export", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		data, err := s.app.Executions.Export(r.PathValue("id"))
		if err != nil {
			return err
		}
		// 导出内容含执行明细:同样禁止缓存(与备份导出一致)。
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="execution-`+r.PathValue("id")+`.json"`)
		return writeJSON(w, data, nil)
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
		// 空 body 合法(等价于空任务 + 无变量),非空则必须能解析
		if err := decodeOptionalJSON(r, &body); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
		}
		exec, err := s.app.Executions.Run(r.Context(), r.PathValue("id"), body.Task, body.Variables)
		return writeJSON(w, exec, err)
	}))
	s.mux.HandleFunc("POST /api/executions/{id}/input", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var response map[string]any
		if err := decodeJSON(r, &response); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
		}
		exec, err := s.app.Executions.ProvideInput(r.Context(), r.PathValue("id"), response)
		return writeJSON(w, exec, err)
	}))
	s.mux.HandleFunc("GET /api/stats", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		stats, err := s.app.Executions.Stats()
		return writeJSON(w, stats, err)
	}))
	s.mux.HandleFunc("GET /api/backup", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		data, err := s.app.Settings.ExportBackup()
		if err != nil {
			return err
		}
		// 备份是约定 8 唯一允许含明文凭据(Agent 环境变量)的出口:
		// 显式禁止缓存,避免明文 API Key 留在浏览器/代理的磁盘缓存里。
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="agent-workflow-backup.json"`)
		return writeJSON(w, data, nil)
	}))
	s.mux.HandleFunc("POST /api/backup/restore", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var backup map[string]any
		if err := decodeJSON(r, &backup); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
		}
		result, err := s.app.Settings.RestoreBackup(backup)
		return writeJSON(w, result, err)
	}))
	s.mux.HandleFunc("GET /api/events/all", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		evs, err := s.app.Executions.AllEvents(r.URL.Query().Get("type"), queryInt(r, "limit", 200))
		return writeJSON(w, evs, err)
	}))
	s.mux.HandleFunc("DELETE /api/executions/{id}", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return s.app.Executions.Delete(r.PathValue("id"))
	}))
	s.mux.HandleFunc("POST /api/executions/{id}/retry", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		// 空 body 合法(等价 skip=false 重试),但 body 存在且解析失败必须
		// 上抛:此前错误被吞,skip 静默变 false —— 用户点「跳过失败节点」
		// 却重跑了那个失败的 Agent(可能重复写文件/重复发 PR),还返回 200。
		var body struct {
			Skip *bool `json:"skip"`
		}
		if err := decodeOptionalJSON(r, &body); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
		}
		skip := body.Skip != nil && *body.Skip
		exec, err := s.app.Executions.RetryNode(r.PathValue("id"), skip)
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
		if err := decodeJSON(r, &p); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
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
		if err := decodeJSON(r, &raw); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
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
	s.mux.HandleFunc("POST /api/agents/{id}/reset", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		if err := s.app.AgentSvc.ResetConfig(r.PathValue("id")); err != nil {
			return err
		}
		cfg, err := s.app.AgentSvc.GetConfig(r.PathValue("id"))
		return writeJSON(w, cfg, err)
	}))
	s.mux.HandleFunc("POST /api/agents/{id}/test", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		out, err := s.app.AgentSvc.Test(r.Context(), r.PathValue("id"))
		return writeJSON(w, map[string]any{"output": out}, err)
	}))
	s.mux.HandleFunc("GET /api/skills", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return writeJSON(w, s.app.SkillSvc.List(), nil)
	}))
	s.mux.HandleFunc("POST /api/skills/{id}/enable", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decodeJSON(r, &body); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
		}
		if body.Enabled == nil {
			return badRequest("需要 enabled 字段")
		}
		if err := s.app.SkillSvc.SetEnabled(r.PathValue("id"), *body.Enabled); err != nil {
			return err
		}
		return writeJSON(w, map[string]any{"ok": true}, nil)
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
		if err := decodeJSON(r, &body); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
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
			// 缺省(nil)表示不修改,保持既有值
			AllowScriptNodes *bool `json:"allow_script_nodes"`
		}
		if err := decodeJSON(r, &body); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
		}
		info, err := s.app.Settings.Update(body.GitWorkingDir, body.LogLevel, body.AllowScriptNodes)
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
			// 结构化错误映射为语义化 HTTP 状态码(资源缺失 404、权限 403,
			// 其余含校验错误 400);body 保留 error 文本并附带 code/kind。
			//
			// 普通错误(参数校验失败等)此前只有 error 字段,同一套 API
			// 出现两种错误体形状,前端 ApiError.code 时而 undefined ——
			// 统一补上 code/kind,消费方才能稳定按 code 分支。
			status := http.StatusBadRequest
			body := map[string]any{
				"error": err.Error(),
				"code":  "BAD_REQUEST",
				"kind":  string(model.KindValidationError),
			}
			var se *model.Error
			if errors.As(err, &se) {
				body["code"] = se.Code
				body["kind"] = string(se.Kind)
				switch {
				case se.Code == "NOT_FOUND":
					status = http.StatusNotFound
				case se.Code == "PAYLOAD_TOO_LARGE":
					status = http.StatusRequestEntityTooLarge
				case se.Kind == model.KindPermissionError:
					status = http.StatusForbidden
				}
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(body)
		}
	}
}

// jsonErrorHandler 把 ServeMux 自己产生的 404/405 纯文本响应换成与
// wrap 一致的 JSON 错误体。已经由 wrap 写出的 JSON 错误体
// (Content-Type: application/json)原样放行,不会被覆盖。
func jsonErrorHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&jsonErrorWriter{ResponseWriter: w}, r)
	})
}

// jsonErrorWriter 只拦截 404/405 的响应体,其余行为完全透传。
type jsonErrorWriter struct {
	http.ResponseWriter
	replaced bool
}

func (w *jsonErrorWriter) WriteHeader(code int) {
	if w.replaced {
		return
	}
	if (code == http.StatusNotFound || code == http.StatusMethodNotAllowed) &&
		!strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		w.replaced = true
		h := w.Header()
		h.Del("Content-Length")
		h.Set("Content-Type", "application/json")
		w.ResponseWriter.WriteHeader(code)
		body := map[string]any{
			"error": "接口不存在",
			"code":  "NOT_FOUND",
			"kind":  string(model.KindValidationError),
		}
		if code == http.StatusMethodNotAllowed {
			body["error"] = "HTTP 方法不被允许"
			body["code"] = "METHOD_NOT_ALLOWED"
		}
		_ = json.NewEncoder(w.ResponseWriter).Encode(body)
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write 丢弃被替换掉的 ServeMux 纯文本正文(状态码与正文已在
// WriteHeader 里写过,再写一次会污染响应)。
func (w *jsonErrorWriter) Write(b []byte) (int, error) {
	if w.replaced {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

// Flush 透传:JSON 错误拦截不能破坏 SSE(依赖 http.Flusher)。
func (w *jsonErrorWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 让 http.NewResponseController 能拿到底层 writer(写超时设置)。
func (w *jsonErrorWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// handleSSE 推送实时 UIEvent。
// 连接建立立即下发响应头 + SSE 注释行:否则 EventSource 在首个事件前
// 一直处于 CONNECTING 状态;每 15s 发 keepalive 注释防止代理断开空闲连接。
//
// 扇出非阻塞:订阅回调由总线在"产生事件的工作流协程"里同步调用,
// 直接写 ResponseWriter 会让慢客户端的 TCP 背压反压到执行引擎
// (单个挂死连接即可卡住工作流)。故每连接一个带缓冲 channel +
// 独立写协程,回调只做非阻塞投递;缓冲满则丢弃并计数(事件已实时落库,
// 回放走 /api/executions/{id}/events,实时流按 best-effort 处理)。
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		// 建连即失败:对端不可达,不必再订阅(否则白留一个 handler)。
		return
	}
	flusher.Flush()

	const queueSize = 256
	queue := make(chan event.UIEvent, queueSize)
	var dropped atomic.Int64

	// 写超时:客户端停止读取时,写操作最多阻塞到该期限,避免写协程永久挂起
	rc := http.NewResponseController(w)
	// write 返回 false 表示写入失败(对端已断开):调用方必须结束流,
	// 否则连接不释放(订阅不注销、goroutine 常驻、keepalive 继续写)。
	write := func(payload string) bool {
		if err := rc.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			logx.Debug("SSE 设置写超时失败(连接可能已关闭)", "error", err.Error())
		}
		if _, err := io.WriteString(w, payload); err != nil {
			logx.Debug("SSE 写入失败(客户端已断开)", "error", err.Error())
			return false
		}
		flusher.Flush()
		return true
	}

	unsub := s.app.Subscribe(func(ev event.UIEvent) {
		select {
		case queue <- ev:
		default:
			// 慢客户端:丢弃而不是阻塞执行引擎,每 100 条记一次日志
			if n := dropped.Add(1); n%100 == 1 {
				logx.Warn("SSE 客户端消费过慢,已丢弃事件", "dropped", n, "queue", queueSize)
			}
		}
	})
	defer unsub()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-queue:
			raw, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if !write("data: " + string(raw) + "\n\n") {
				return // 写失败:对端已不可达,关闭流并释放订阅
			}
		case <-keepalive.C:
			if !write(": keepalive\n\n") {
				return
			}
		}
	}
}

// AssetHandler 返回可挂载到 Wails AssetServer 指定 Route 前缀的处理器。
// 桌面模式下注册 `wails.NewServiceWithOptions(h, ServiceOptions{Route: "/api"})`
// 后,导出/备份等 /api 下载链接走同源 HTTP,不再落入 SPA fallback。
// 独立类型只暴露 ServeHTTP(Wails 绑定引擎按名跳过),避免 Server 的
// 其他导出方法被注册为 IPC 绑定。
type AssetHandler struct{ Srv *Server }

func (h *AssetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Wails 命中 Route 时已剥掉前缀,补回以匹配内部 /api/* 路由
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		r.URL.Path = "/api" + r.URL.Path
	}
	h.Srv.Handler().ServeHTTP(w, r)
}

// ---- helpers ----

// maxJSONBody 是所有 JSON 请求体的上限。服务器模式监听 127.0.0.1 且
// 无鉴权,本机任意进程都能打这些端点;此前只有 import 端点有 1MB 限制,
// /api/backup/restore、/api/workflows、/input、config 等直接 json.Decode
// 全量读进内存,一个大 body 即可把应用 OOM 掉。
const maxJSONBody = 1 << 20 // 1MB

// decodeJSON 解析受上限保护的 JSON 请求体。
// 解析失败一律返回**结构化**错误(错误体形状统一,见 wrap):
// 超限 → 413(PAYLOAD_TOO_LARGE),非法 JSON → 400(INVALID_JSON)。
func decodeJSON(r *http.Request, out any) error {
	err := decodeJSONRaw(r, out)
	if err == nil {
		return nil
	}
	return wrapDecodeError(err)
}

// decodeJSONRaw 只做受限读取与解析,错误原样返回(供可选 body 判空)。
func decodeJSONRaw(r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBody)
	return json.NewDecoder(r.Body).Decode(out)
}

// wrapDecodeError 把解码错误映射为结构化错误。
func wrapDecodeError(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return model.NewError(model.KindValidationError, "PAYLOAD_TOO_LARGE",
			fmt.Sprintf("请求体超过 %d MB 上限", maxJSONBody>>20))
	}
	return model.NewError(model.KindValidationError, "INVALID_JSON",
		"请求体不是合法 JSON: "+err.Error())
}

// decodeOptionalJSON 允许空 body(等价于零值),但 body 非空且非法时报错。
func decodeOptionalJSON(r *http.Request, out any) error {
	err := decodeJSONRaw(r, out)
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) {
		return nil // 空 body:用零值
	}
	return wrapDecodeError(err)
}

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

// maxQueryLimit 是显式 limit 参数的上限。
// 此前用 int 累加解析,超长数字会整数溢出成负数,而仓储层把 limit<=0
// 解释为"不限制" —— 一个 limit=99999999999999999999 的请求会变成
// "返回全表"(内存与响应体都不可控)。
const maxQueryLimit = 1000

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
		if n > maxQueryLimit {
			return maxQueryLimit // 收敛到上限,杜绝溢出成负数
		}
	}
	return n
}

// parseVersion 解析版本号路径参数:只接受十进制数字,且必须落在合理
// 范围内。此前 int 累加会整数溢出成负数/垃圾值,再去查版本快照。
func parseVersion(s string) (int, error) {
	if s == "" {
		return 0, errors.New("版本号非法")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("版本号非法")
		}
		n = n*10 + int(c-'0')
		if n > maxQueryLimit*1000 {
			return 0, errors.New("版本号非法")
		}
	}
	return n, nil
}

// parseWorkflow 把请求体(map)转为 Workflow。
// 结构非法(字段类型不符等)必须报错:此前丢弃 Unmarshal 错误,
// 错类型字段会静默变成零值,产出空白/残缺工作流。
//
// **未知顶层键也必须报错**(UNKNOWN_FIELD):json.Unmarshal 默认宽松,多余
// 的键被**静默丢弃** —— 用户提交了一份含多余键的 JSON,接口返回 200,
// 用户以为那份设置生效了,实际根本没进模型(约定 14:静默丢弃 = 用户
// 以为设置还在)。
//
// 与 DSL 顶层键策略同源:workflow/dsl 对文档顶层的未知键报
// DSL_UNKNOWN_KEY(迭代 15),JSON 保存路径此前却没有对应行为 —— 同一份
// 定义换个格式导入行为就不同。
//
// 前端发出的 payload 只含模型里的字段(设计器 toWorkflowModel / 新建
// 工作流),因此严格化**不会**打破前端保存路径(见
// TestFrontendSavePayloadsAreAccepted)。
func parseWorkflow(m map[string]any) (*model.Workflow, error) {
	if unknown := unknownTopLevelKeys(m); len(unknown) > 0 {
		return nil, model.NewError(model.KindValidationError, "UNKNOWN_FIELD",
			fmt.Sprintf("未知的顶层字段 %s(允许: %s)",
				strings.Join(unknown, ", "), strings.Join(allowedTopLevelKeys(), ", ")))
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, badRequest("工作流结构非法: " + err.Error())
	}
	var wf model.Workflow
	if err := json.Unmarshal(raw, &wf); err != nil {
		return nil, badRequest("工作流结构非法: " + err.Error())
	}
	return &wf, nil
}

// workflowTopLevelKeys 是工作流 JSON 允许的顶层键集合。
//
// 唯一来源:model.Workflow 的 json tag(反射派生 —— 模型加字段这里自动
// 生效,不会与模型漂移),外加两个特例:
//
//   - "permissions":DSL 根级段的正式写法(需求.md 第十八章),模型读它时
//     映射到 Permission 字段(见 model.Workflow.UnmarshalJSON);
//   - "ignored_keys":导入接口的**响应专用**字段(见 import 处理器)。
//     保存时忽略它 —— 拒掉它会让"导入 → 原样回存"这种往返失败。
var workflowTopLevelKeys = buildWorkflowTopLevelKeys()

func buildWorkflowTopLevelKeys() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(model.Workflow{})
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	out["permissions"] = true
	out["ignored_keys"] = true
	return out
}

// allowedTopLevelKeys 返回允许的顶层键(排序后),用于错误信息。
func allowedTopLevelKeys() []string {
	out := make([]string, 0, len(workflowTopLevelKeys))
	for k := range workflowTopLevelKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// unknownTopLevelKeys 返回请求体里不被识别的顶层键(排序)。
func unknownTopLevelKeys(m map[string]any) []string {
	var out []string
	for k := range m {
		if !workflowTopLevelKeys[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// dslIgnoredKeys 解析 DSL 文本,返回"出现在文档里但被解析器丢弃"的键。
//
// 解析失败时返回 nil:真正的错误由 app 层的 ImportYAML 报出(这里不重复
// 报错,避免两处错误语义不一致)。
func dslIgnoredKeys(content []byte) []string {
	doc, err := dsl.Parse(content)
	if err != nil {
		return nil
	}
	return doc.IgnoredKeys
}

// workflowJSONMap 把工作流展开成 map(便于在响应里附加额外字段)。
func workflowJSONMap(wf *model.Workflow) (map[string]any, error) {
	raw, err := json.Marshal(wf)
	if err != nil {
		return nil, badRequest("工作流序列化失败: " + err.Error())
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, badRequest("工作流序列化失败: " + err.Error())
	}
	return m, nil
}
