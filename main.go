// Agent Workflow Orchestrator — 可配置、可视化、可扩展的多 Agent 工作流编排桌面应用。
//
// 运行模式:
//   - 默认:Wails 桌面应用(GUI)
//   - --server:HTTP 服务器模式(REST + SSE,无 GUI,用于浏览器访问与测试)
package main

import (
	"context"
	"embed"
	"flag"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"agentworkflow/api"
	app "agentworkflow/app/application"
	"agentworkflow/event"
	"agentworkflow/logx"

	wails "github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	serverMode := flag.Bool("server", false, "以 HTTP 服务器模式运行(无 GUI)")
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP 监听地址")
	dataDir := flag.String("data", "", "数据目录(默认用户配置目录)")
	gitDir := flag.String("git-dir", "", "Git 工作目录(默认当前目录)")
	logLevel := flag.String("log", "info", "日志级别 debug|info|warn|error")
	flag.Parse()

	logx.SetLevel(*logLevel)

	dir := *dataDir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			base = "."
		}
		dir = filepath.Join(base, "agent-workflow")
	}

	app, err := app.NewApp(dir)
	if err != nil {
		logx.Error("应用初始化失败", "error", err)
		os.Exit(1)
	}
	// 启动参数优先:Git 工作目录(默认进程当前目录,亦可在设置页修改)
	if *gitDir != "" {
		_ = app.GitAPI.SetWorkingDir(*gitDir)
	}

	if *serverMode {
		runServer(app, *addr)
		return
	}
	runDesktop(app)
}

// runServer HTTP 服务器模式:REST API + SSE + 静态前端资源。
// 非 API 且无扩展名的路径回退到 index.html(SPA 前端路由)。
func runServer(app *app.App, addr string) {
	apiHandler := api.NewServer(app)

	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		logx.Error("嵌入资源加载失败", "error", err)
		os.Exit(1)
	}
	static := http.FileServer(http.FS(dist))

	mux := http.NewServeMux()
	mux.Handle("/api/", apiHandler.Handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// SPA fallback:仅对前端路由(无扩展名、非 API/Wails 路径)返回 index.html
		p := r.URL.Path
		if p != "/" && filepath.Ext(p) == "" &&
			!strings.HasPrefix(p, "/api/") && !strings.HasPrefix(p, "/wails/") {
			if indexHTML, readErr := fs.ReadFile(dist, "index.html"); readErr == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(indexHTML)
				return
			}
		}
		static.ServeHTTP(w, r)
	})

	logx.Info("HTTP 服务启动", "addr", addr, "mode", "server")

	srv := &http.Server{Addr: addr, Handler: mux}
	// 优雅关闭:SIGINT/SIGTERM 时先停接收新请求,给运行中执行一次收尾机会
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		logx.Info("收到退出信号,正在关闭…")
		_ = srv.Shutdown(context.Background())
		// 给运行中的执行最多 5 秒收尾(状态落库为 CANCELLED)
		if left := app.Engine.WaitIdle(5 * time.Second); left > 0 {
			logx.Warn("仍有执行未收尾", "count", left)
		}
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		if err != http.ErrServerClosed {
			logx.Error("HTTP 服务退出", "error", err)
			os.Exit(1)
		}
		// 等待执行协程收尾后再退出
		_ = app.Engine.WaitIdle(5 * time.Second)
	}
}

// runDesktop Wails 桌面模式。
func runDesktop(app *app.App) {
	// REST API 处理器:桌面模式下经 AssetHandler 挂载到 AssetServer 的 /api
	// 前缀,导出/备份等下载链接与服务器模式同源可用
	apiHandler := api.NewServer(app)

	wailsApp := wails.New(wails.Options{
		Name:        "Agent Workflow Orchestrator",
		Description: "可配置、可视化、可扩展的多 Agent 工作流编排",
		Services: []wails.Service{
			wails.NewService(app.Workflows),
			wails.NewService(app.Executions),
			wails.NewService(app.AgentSvc),
			wails.NewService(app.SkillSvc),
			wails.NewService(app.GitAPI),
			wails.NewService(app.Settings),
			wails.NewServiceWithOptions(&api.AssetHandler{Srv: apiHandler},
				wails.ServiceOptions{Route: "/api"}),
		},
		// 退出前给运行中的执行一次收尾机会(状态落库),与服务器模式对齐
		OnShutdown: func() {
			if left := app.Engine.WaitIdle(5 * time.Second); left > 0 {
				logx.Warn("仍有执行未收尾,已强制退出", "count", left)
			}
		},
		Assets: wails.AssetOptions{
			Handler: wails.AssetFileServerFS(assets),
		},
		Mac: wails.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// 实时事件桥接:总线 → Wails 事件(前端统一监听 ui:event)
	app.ConnectEvents()
	_ = app.Subscribe(func(ev event.UIEvent) {
		wailsApp.Event.Emit("ui:event", ev)
	})

	wailsApp.Window.NewWithOptions(wails.WebviewWindowOptions{
		Title:  "Agent Workflow Orchestrator",
		Width:  1440,
		Height: 900,
		Mac: wails.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                wails.MacBackdropTranslucent,
			TitleBar:                wails.MacTitleBarHiddenInset,
		},
		BackgroundColour: wails.NewRGB(13, 17, 23),
		URL:              "/",
	})

	if err := wailsApp.Run(); err != nil {
		logx.Error("应用退出异常", "error", err)
		os.Exit(1)
	}
}
