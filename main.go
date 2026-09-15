// Agent Workflow Orchestrator — 可配置、可视化、可扩展的多 Agent 工作流编排桌面应用。
//
// 运行模式:
//   - 默认:Wails 桌面应用(GUI)
//   - --server:HTTP 服务器模式(REST + SSE,无 GUI,用于浏览器访问与测试)
package main

import (
	"embed"
	"flag"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

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

	if *serverMode {
		runServer(app, *addr)
		return
	}
	runDesktop(app)
}

// runServer HTTP 服务器模式:REST API + SSE + 静态前端资源。
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
	mux.Handle("/", static)

	logx.Info("HTTP 服务启动", "addr", addr, "mode", "server")
	if err := http.ListenAndServe(addr, mux); err != nil {
		logx.Error("HTTP 服务退出", "error", err)
		os.Exit(1)
	}
}

// runDesktop Wails 桌面模式。
func runDesktop(app *app.App) {
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
