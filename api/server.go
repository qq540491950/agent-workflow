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
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"agentworkflow/agent"
	"agentworkflow/app/application"
	"agentworkflow/event"
	"agentworkflow/logx"
	"agentworkflow/permission"
	"agentworkflow/workflow/model"
)

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
	// mode 列表:唯一来源是 workflow/model.AllModes(约定 13)。
	//
	// 前端此前自己硬编码了一份 mode 清单 —— 后端加了 mode 而前端没跟上,
	// 用户在下拉里选不到新 mode;前端留着后端已删的 mode,用户选了必然
	// 被校验期判非法。两种模式拿的是同一份数据(桌面模式走 Wails 绑定
	// 的 WorkflowService.Modes)。
	s.mux.HandleFunc("GET /api/modes", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return writeJSON(w, model.AllModes(), nil)
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
		// 原始载荷交给应用层:顶层未知键的判定必须与桌面模式同源
		// (桌面模式走 app.Workflows.SavePayload,同一段代码)。
		body, err := readLimitedBody(r)
		if err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE)
		}
		created, err := s.app.Workflows.SavePayload(body)
		if err != nil {
			return err
		}
		// 响应形状:工作流字段仍在顶层(第三方客户端直接读 body.id),
		// 校验警告作为附加字段 warnings 一并带回 —— 此前警告只进日志,
		// 直连 API 的调用方永远看不到,用户以为设置生效了。
		return writeJSON(w, created, nil)
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
		body, err := readLimitedBody(r)
		if err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE)
		}
		// 与保存端点同源:同一份载荷,保存与校验必须给出同一个判定
		// (否则"校验通过"却保存不了,用户无从下手)。
		parsed, err := application.ParseWorkflowPayload([]byte(body))
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
		body, err := readLimitedBody(r)
		if err != nil {
			return err
		}
		// 被忽略的键由应用层产出(此前本层自己又解析了一遍 DSL:
		// 同一份解析做两遍,且桌面模式根本没有这个字段)。
		res, err := s.app.Workflows.ImportYAML(body)
		if err != nil {
			return err
		}
		// 响应形状:工作流字段仍在**顶层**(前端 api.importYAML 直接把响应
		// 当 Workflow 用,契约不变),额外附加 ignored_keys / warnings 数组。
		return writeJSON(w, res, nil)
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
		limit, truncated := queryLimit(r, "limit", 0)
		execs, err := s.app.Executions.List(wfID, limit)
		markLimitTruncated(w, truncated, limit)
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
		limit, truncated := queryLimit(r, "limit", 200)
		evs, err := s.app.Executions.AllEvents(r.URL.Query().Get("type"), limit)
		markLimitTruncated(w, truncated, limit)
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
		// 局部更新:只改载荷里**显式给出**的能力位,缺省的保持原值。
		//
		// 此前按"整表覆盖"解释(policyOf:缺键 → false):客户端只发
		// {"filesystem_read":true} 会把 filesystem_write / git_read /
		// git_commit / git_push **静默置 false** —— 能力被吊销,
		// 用户却什么都没收到(约定 14:不该动的却动了)。
		//
		// 解析口径与 DSL permissions 段同源(permission.ParsePolicyPatch
		// 复用 policyMask 的"显式指定"概念,约定 13)。
		patch, err := permission.ParsePolicyPatch(p)
		if err != nil {
			// 原消息里带 Go 类型标注(permission 包:"…的值不是布尔(实际 %T)"),
			// 对用户没有意义 → 换成人话;原始错误进日志(wrap 里的 logx.Warn)。
			return badRequest("权限字段的值必须是布尔值(true/false)")
		}
		id := r.PathValue("id")
		// 基线 = 当前**生效**的策略:缺省的能力位保持原值。
		// 未知 Agent 走 SetPermission 里的 requireAgent → 404 NOT_FOUND。
		base := s.app.Perms.Get(id)
		err = s.app.AgentSvc.SetPermission(id, patch.Apply(base))
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
			// 原始错误形如 "json: cannot unmarshal string into Go struct
			// field AgentConfig.extra_args of type []string" ——
			// 泄漏 Go 结构体名与类型名,对用户没有意义。
			// 换成人话;字段名(用户自己 payload 里的 JSON 键)保留,
			// 用户才知道该改哪一处。原始错误进日志。
			var ute *json.UnmarshalTypeError
			if errors.As(err, &ute) && ute.Field != "" {
				return badRequest(fmt.Sprintf("Agent 配置的字段 %q 类型不对(应为对应类型的值)", ute.Field))
			}
			return badRequest("Agent 配置格式非法:字段类型与预期不符")
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
		limit, truncated := queryLimit(r, "limit", 10)
		logs, err := s.app.GitAPI.Log(r.Context(), limit)
		markLimitTruncated(w, truncated, limit)
		return writeJSON(w, logs, err)
	}))

	// 设置
	s.mux.HandleFunc("GET /api/settings", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return writeJSON(w, s.app.Settings.Info(), nil)
	}))
	s.mux.HandleFunc("POST /api/settings", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		var body struct {
			// 缺省(nil / 显式 null)= **不修改**,保持既有值;
			// 显式 ""= 清除(用户在设置页清空输入框的语义)。
			//
			// 此前是普通 string:客户端**省略**该字段时会被解成零值 "",
			// 而应用层把 "" 当作"清除" —— 一个只想改日志级别的请求
			// ({"log_level":"debug"})会顺手把用户的 git 工作目录清空,
			// 用户没要求改它(约定 14 的同类:不该动的却动了)。
			GitWorkingDir *string `json:"git_working_dir"`
			LogLevel      string  `json:"log_level"`
			// 缺省(nil)表示不修改,保持既有值
			AllowScriptNodes *bool `json:"allow_script_nodes"`
		}
		if err := decodeJSON(r, &body); err != nil {
			return err // 结构化错误(PAYLOAD_TOO_LARGE / INVALID_JSON)
		}
		// 省略 = 不修改:回填当前**生效值**(与设置页显示的一致)。
		// 应用层的 Update 语义是"空串 = 清除",所以这里必须显式回填,
		// 不能直接把零值传下去。
		dir := s.app.Settings.Info().GitWorkingDir
		if body.GitWorkingDir != nil {
			dir = *body.GitWorkingDir
		}
		info, err := s.app.Settings.Update(dir, body.LogLevel, body.AllowScriptNodes)
		return writeJSON(w, info, err)
	}))

	// SSE 实时事件
	s.mux.HandleFunc("GET /api/events", s.handleSSE)
	// SSE 订阅计数(只读,无敏感信息):SSE 断连后订阅是否释放此前只有
	// 单测覆盖(sse_test.go),端到端层面(真实进程 + 真实 TCP 断开)没有
	// 可观测点 —— 冒烟脚本靠它断言"断开后归零"(开放清单第 5 条)。
	// 对运维也有用:排查"连接泄漏 / 订阅常驻"时一眼看到当前连接数。
	s.mux.HandleFunc("GET /api/sse/stats", s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return writeJSON(w, map[string]int{"subscribers": s.app.EventSubscriberCount()}, nil)
	}))
}

// wrap 统一错误处理与日志。
func (s *Server) wrap(h func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// tw 记录"响应是否已经写过":panic 兜底时若响应已经提交,
		// 就不能再写一个 500 错误体(会把半截成功响应污染成坏 JSON)。
		tw := &trackedWriter{ResponseWriter: w}
		err := s.callHandler(h, tw, r)
		if err == nil {
			return
		}
		logx.Warn("http error", "path", r.URL.Path, "error", err)
		status, body := errorResponse(err)
		if tw.wrote {
			// 响应已经提交(半截正文已写出)→ 只能记日志,不能改状态码。
			logx.Error("http error 发生在响应提交之后,无法改写状态码",
				"path", r.URL.Path, "status", status)
			return
		}
		tw.WriteHeader(status)
		_ = json.NewEncoder(tw).Encode(body)
	}
}

// callHandler 调用 handler 并兜住 panic。
//
// **为什么必须兜**(迭代 30,P2-5):此前 wrap 没有任何 recover,handler
// 里的 panic 会一路冒到 net/http —— 后果是**两种模式两种错误形态**:
//
//   - 服务器模式:连接被服务端直接断开 → 客户端看到 EOF → 前端报
//     "Failed to fetch" → 用户以为是"后端没启动/地址不可达";
//   - 桌面模式:Wails 把 panic 原文交给前端 → 界面弹出
//     "agentworkflow/app/application.WorkflowService.Get: panic: …"
//     —— 泄漏 Go 包路径,用户看不懂,开发者也定位不到。
//
// 兜住之后统一回 500 + 标准错误体{error,code,kind}(code=INTERNAL),
// 两种模式的形态一致,且**不外泄** panic 原文(原文只进日志)。
//
// http.ErrAbortHandler 例外:那是 net/http 的"主动中止连接"信号,
// 官方要求**不要** recover 它(recover 会破坏连接关闭语义)。
func (s *Server) callHandler(h func(http.ResponseWriter, *http.Request) error, w http.ResponseWriter, r *http.Request) (err error) {
	defer func() {
		rec := recover()
		if rec == nil {
			return
		}
		if rec == http.ErrAbortHandler {
			panic(rec) // 放行:见函数注释
		}
		// panic 原文可能含路径/参数 → 只进日志(mask 后),不进响应体。
		logx.Error("http handler panic", "path", r.URL.Path, "panic", fmt.Sprint(rec))
		err = &panicError{value: rec}
	}()
	return h(w, r)
}

// panicError 是 panic 被兜住后的内部错误。
//
// 只用于**标记**"这次错误来自 panic" —— 响应体里不会出现 panic 原文
// (Error() 返回人话),原文只进日志。
type panicError struct{ value any }

func (e *panicError) Error() string { return "服务内部错误" }

// trackedWriter 记录"响应是否已经写过"。
//
// 用途只有一个:panic 兜底时判断还能不能改写状态码。已经写出正文的
// 响应再补一个 500 错误体,客户端拿到的是"半截 JSON + 半截错误体"。
type trackedWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *trackedWriter) WriteHeader(code int) {
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *trackedWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

// Unwrap 让 http.NewResponseController 能拿到底层 writer。
func (w *trackedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// errorResponse 把错误映射成 (HTTP 状态码, 错误体)。
//
// 错误体形状统一为 {error, code, kind}(前端 ApiError 靠 code 分支;
// 约定 8:错误也要结构化)。
//
// **error 文本过 logx.Mask**:错误消息常夹带命令输出/SQL 文本,里面
// 可能带凭据(如 "Authorization: Bearer …")→ 出口必须脱敏
// (与节点 output 出口同源)。
func errorResponse(err error) (int, map[string]any) {
	var pe *panicError
	if errors.As(err, &pe) {
		// panic:不外泄原文,统一 INTERNAL。
		return http.StatusInternalServerError, map[string]any{
			"error": "服务内部错误",
			"code":  "INTERNAL",
			"kind":  string(model.KindWorkflowError),
		}
	}
	status := http.StatusBadRequest
	body := map[string]any{
		"error": logx.Mask(err.Error()),
		"code":  "BAD_REQUEST",
		"kind":  string(model.KindValidationError),
	}
	var se *model.Error
	if errors.As(err, &se) {
		body["code"] = se.Code
		body["kind"] = string(se.Kind)
		status = statusForError(se)
	}
	return status, body
}

// statusForError 把结构化错误映射为 HTTP 状态码(迭代 30,P1-2)。
//
// ---- 为什么要有这张表 ----
//
// 此前**所有**错误都报 400(除 NOT_FOUND/PAYLOAD_TOO_LARGE/权限):
// 数据库锁死、Agent 崩溃、超时 —— 全部 400。用户与监控无法区分
// "用户填错了"与"后端炸了";监控侧 4xx 告警会被这类噪声淹没。
//
// ---- 口径(硬要求:既有 4xx 映射一律不变) ----
//
//	NOT_FOUND          → 404   (不变)
//	PAYLOAD_TOO_LARGE  → 413   (不变)
//	KindPermissionError→ 403   (不变)
//	其余校验类/状态类  → 400   (不变)
//
// 新增的 5xx 只覆盖**内部类**错误:
//
//	KindPersistenceError → 500  (库读不出来/写不进去)
//	DB_ERROR / INTERNAL → 500
//	KindAgentError      → 502  (外部 Agent 调用失败)
//	KindProcessError    → 502  (外部进程异常退出)
//	KindTimeoutError    → 504  (外部调用超时)
//
// **StateError 的 INVALID_RESUME / INVALID_TRANSITION 仍是 400**
// (不是 409):这两个码是**既有对外契约**——docs/api.md 的"取消执行"
// 一节写明"已终态 → 400 INVALID_TRANSITION",api/cancel_semantics_test.go
// 也钉着 400。改成 409 会破坏既有客户端(前端不按状态码分支,但第三方
// 客户端与文档契约都在),而"语义更准"的收益不足以抵掉破坏兼容性。
// 登记在 docs/api.md 的状态码映射表里,留待后续轮次统一裁决。
func statusForError(se *model.Error) int {
	// 先看 code:少数 code 的语义与 kind 无关 ——
	// DB_ERROR 同时出现在 KindPersistenceError 与 KindStateError 下,
	// 都是"库读不出来" → 500,不能因为 kind 落在状态类就报 400。
	switch se.Code {
	case "NOT_FOUND":
		return http.StatusNotFound
	case "PAYLOAD_TOO_LARGE":
		return http.StatusRequestEntityTooLarge
	case "DB_ERROR", "INTERNAL":
		return http.StatusInternalServerError
	}
	switch se.Kind {
	case model.KindPermissionError:
		return http.StatusForbidden
	case model.KindPersistenceError:
		return http.StatusInternalServerError
	case model.KindAgentError:
		return http.StatusBadGateway
	case model.KindProcessError:
		return http.StatusBadGateway
	case model.KindTimeoutError:
		return http.StatusGatewayTimeout
	}
	return http.StatusBadRequest
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
			// 面向用户但可定位(迭代 30,P3-6):此前的"接口不存在"是写给
			// 开发者看的 —— 普通用户看到它不知道该做什么(换个地址?刷新?
			// 还是等后端起来?)。现在说清"资源没了"并给出两个常见原因。
			"error": "请求的资源不存在(链接可能已过期,或地址有误)",
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
				// 序列化失败的事件**发不出去**。此前是 `continue`:
				// 这一条事件不送达且**不留任何痕迹** —— 事件流里
				// "少了一条",没人知道是哪条、为什么(约定 14 同族:
				// 该送达却没送达 = 静默丢数据)。
				//
				// 现在至少记一条 WARN(可追溯:类型/执行 ID/节点/原因),
				// 并且**继续**推流:一条坏事件不该掐断整条流。
				logx.Warn("SSE 事件序列化失败,已跳过该事件",
					"event_type", ev.Type,
					"execution_id", ev.ExecutionID,
					"node_id", ev.NodeID,
					"error", err.Error())
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

// readLimitedBody 读取受上限保护的请求体**原文**(不解析)。
//
// 上限(maxJSONBody)与既有 JSON 端点一致:服务器模式监听 127.0.0.1 且
// 无鉴权,本机任意进程都能打这些端点,一个大 body 即可把应用 OOM 掉。
//
// 工作流保存/校验/导入三个端点改用它:载荷的**解析与判定**下沉到应用层
// (application.ParseWorkflowPayload / SavePayload),服务器模式与桌面模式
// 走同一段代码 —— 顶层未知键的判定不能只在 api 层有(约定 10)。
func readLimitedBody(r *http.Request) (string, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBody)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return "", model.NewError(model.KindValidationError, "PAYLOAD_TOO_LARGE",
				fmt.Sprintf("请求体超过 %d MB 上限", maxJSONBody>>20))
		}
		return "", badRequest("读取请求体失败")
	}
	return string(b), nil
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

// badRequest 造一个"用户填错了"的错误。
//
// **必须是结构化错误**(迭代 30,P2-4):此前它返回裸 fmt.Errorf,
// 错误体靠 wrap 的默认值兜底(code=BAD_REQUEST/kind=ValidationError),
// 形状是碰巧对的而不是**声明**出来的。改成显式的 model.Error 之后,
// code/kind 由错误自己携带,任何调用方(errors.As)都能判别。
//
// **消息必须是人话**:调用方只传用户能看懂的话 ——
// Go 的类型名/结构体名(如 "json: cannot unmarshal string into Go
// struct field AgentConfig.extra_args of type []string")对用户没有
// 任何意义,却泄漏了内部实现。原始错误**只进日志**(wrap 里的
// logx.Warn),响应体只给人话。
func badRequest(msg string) error {
	return model.NewError(model.KindValidationError, "BAD_REQUEST", msg)
}

// maxQueryLimit 是显式 limit 参数的上限。
// 此前用 int 累加解析,超长数字会整数溢出成负数,而仓储层把 limit<=0
// 解释为"不限制" —— 一个 limit=99999999999999999999 的请求会变成
// "返回全表"(内存与响应体都不可控)。
const maxQueryLimit = 1000

func queryLimit(r *http.Request, key string, def int) (applied int, truncated bool) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def, false
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return def, false
		}
		n = n*10 + int(c-'0')
		if n > maxQueryLimit {
			return maxQueryLimit, true // 收敛到上限,杜绝溢出成负数
		}
	}
	return n, false
}

// markLimitTruncated 在显式 limit 被上限收敛时打响应头。
// 此前截断是静默的:消费方拿到 1000 条会把"就返回了 1000 条"误读成
// "总共只有 1000 条"。标记走响应头而不是响应体字段 —— 列表端点的
// 响应体是裸数组,塞字段等于改所有列表接口的响应形状(前端契约要同步,
// 属跨层改动);头是消费方可选读取的附加语义,不读不受影响。
func markLimitTruncated(w http.ResponseWriter, truncated bool, applied int) {
	if !truncated {
		return
	}
	w.Header().Set("X-Limit-Truncated", "true")
	w.Header().Set("X-Limit-Applied", strconv.Itoa(applied))
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
