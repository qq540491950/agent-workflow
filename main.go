// Agent Workflow Orchestrator — 可配置、可视化、可扩展的多 Agent 工作流编排桌面应用。
//
// 运行模式:
//   - 默认:Wails 桌面应用(GUI)
//   - --server:HTTP 服务器模式(REST + SSE,无 GUI,用于浏览器访问与测试)
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"agentworkflow/api"
	app "agentworkflow/app/application"
	"agentworkflow/event"
	"agentworkflow/logx"

	wails "github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed all:workflows/examples
var examplesFS embed.FS

func main() {
	// 退出码由 run() 返回:os.Exit 不执行 defer,
	// 直接在 main 里 os.Exit 会跳过数据库关闭(连接泄漏 + WAL 不落盘)。
	os.Exit(run())
}

// options 是解析后的启动参数。
type options struct {
	Server   bool
	Addr     string
	DataDir  string
	GitDir   string
	LogLevel string
}

// validLogLevels 是 --log 的合法取值(唯一来源:logx —— 应用真正能应用的
// 级别集合;此前这里另写了一份,两份漂移就会出现"参数接受、后端不认")。
var validLogLevels = logx.Levels()

// flagValues 保存各启动参数的取值指针。
type flagValues struct {
	server   *bool
	addr     *string
	data     *string
	gitDir   *string
	logLevel *string
}

// newFlagSet 构造启动参数解析器。输出交给调用方决定:测试里丢到
// io.Discard 保持安静,真正运行时写 stderr。
func newFlagSet(out io.Writer) (*flag.FlagSet, flagValues) {
	fs := flag.NewFlagSet("agent-workflow", flag.ContinueOnError)
	fs.SetOutput(out)
	v := flagValues{
		server:   fs.Bool("server", false, "以 HTTP 服务器模式运行(无 GUI)"),
		addr:     fs.String("addr", "127.0.0.1:8080", "HTTP 监听地址"),
		data:     fs.String("data", "", "数据目录(默认用户配置目录)"),
		gitDir:   fs.String("git-dir", "", "Git 工作目录(默认当前目录)"),
		logLevel: fs.String("log", "info", "日志级别 debug|info|warn|error"),
	}
	return fs, v
}

// parseArgs 解析并校验命令行参数。纯函数:不打印、不退出,便于单测。
func parseArgs(args []string) (options, error) {
	fs, v := newFlagSet(io.Discard)
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	opts := options{
		Server:   *v.server,
		Addr:     *v.addr,
		DataDir:  *v.data,
		GitDir:   *v.gitDir,
		LogLevel: *v.logLevel,
	}
	if err := opts.validate(); err != nil {
		return options{}, err
	}
	return opts, nil
}

// validate 校验启动参数:参数非法要**当场报**,而不是默默按默认值跑
// (约定 14:该失败却成功等同 P0)。
func (o options) validate() error {
	if !isValidLogLevel(o.LogLevel) {
		return fmt.Errorf("日志级别 %q 非法(允许: %s)", o.LogLevel, strings.Join(validLogLevels, "|"))
	}
	if o.Server {
		if o.Addr == "" {
			return errors.New("服务器模式必须指定监听地址(--addr)")
		}
		if _, _, err := net.SplitHostPort(o.Addr); err != nil {
			return fmt.Errorf("监听地址 %q 非法: %w", o.Addr, err)
		}
	}
	return nil
}

// isValidLogLevel 判断日志级别是否合法(与 logx 支持的值一致)。
func isValidLogLevel(level string) bool {
	return logx.ValidateLevel(level) == nil
}

// defaultDataDir 返回默认数据目录:用户配置目录下的 agent-workflow。
// userConfigDir 为空(取不到用户配置目录)时退化为当前目录。
func defaultDataDir(userConfigDir string) string {
	if userConfigDir == "" {
		userConfigDir = "."
	}
	return filepath.Join(userConfigDir, "agent-workflow")
}

// usage 打印命令行用法。
func usage(out io.Writer) {
	fs, _ := newFlagSet(out)
	fs.Usage()
}

// run 是真正的入口逻辑,返回进程退出码。
// 拆出来是为了让 defer(关闭 SQLite)在退出路径上真的执行。
func run() int {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(os.Stderr)
			return 0
		}
		fmt.Fprintf(os.Stderr, "启动参数错误: %v\n\n", err)
		usage(os.Stderr)
		return 2
	}

	// 应用日志级别:用 logx.SetLevelChecked(非法值保持当前级别并返回 error),
	// 错误直接回报给调用方 —— 退出码 2(与参数错误同一档)。
	// 此前用 logx.SetLevel:非法值只打一条 WARN 就继续启动,用户以为
	// 日志级别已生效、实际跑的是另一个级别(静默失败)。
	if err := logx.SetLevelChecked(opts.LogLevel); err != nil {
		fmt.Fprintf(os.Stderr, "日志级别设置失败: %v\n", err)
		return 2
	}

	dir := opts.DataDir
	if dir == "" {
		base, cfgErr := os.UserConfigDir()
		if cfgErr != nil {
			logx.Warn("获取用户配置目录失败,改用当前目录", "error", cfgErr)
		}
		dir = defaultDataDir(base)
	}

	application, err := app.NewApp(dir, examplesFS)
	if err != nil {
		logx.Error("应用初始化失败", "error", err)
		return 1
	}
	// 退出前关闭 SQLite:释放连接并让 WAL 落盘。
	defer func() {
		if cerr := application.Repo.Close(); cerr != nil {
			logx.Warn("关闭数据库失败", "error", cerr)
		}
	}()

	// 启动参数优先:Git 工作目录(默认进程当前目录,亦可在设置页修改)。
	//
	// 语义:**持久化设置**。--git-dir 与设置页写的是同一个键
	// (git_working_dir),所以设置页回显的就是实际在用的目录 ——
	// 显示与实际永远一致,不会出现"设置页显示 A、实际在 B 里干活"。
	//
	// 落库失败时:明确告警 + 回退到默认(保持已持久化的目录)。
	// 此前 `_ = application.GitAPI.SetWorkingDir(...)` 把错误整个丢掉:
	// 启动参数没生效、日志里也没有任何痕迹,用户以为参数被接受了
	// (约定 14:静默失败)。
	if opts.GitDir != "" {
		if err := applyGitDirArg(application, opts.GitDir); err != nil {
			logx.Warn("启动参数 --git-dir 未能生效,已回退到默认工作目录",
				"dir", opts.GitDir, "fallback", application.GitSvc.WorkingDir(), "error", err)
		}
	}

	if opts.Server {
		if err := runServer(application, opts.Addr); err != nil {
			logx.Error("HTTP 服务退出", "error", err)
			return 1
		}
		return 0
	}
	if err := runDesktop(application); err != nil {
		logx.Error("应用退出异常", "error", err)
		return 1
	}
	return 0
}

// applyGitDirArg 应用启动参数 --git-dir(持久化语义)。
//
// 抽成独立函数是为了可测:run() 里直接内联时,错误被丢弃且无法断言
// "落库失败时回退到默认目录"。
//
// 落库失败**不改内存态**(GitService.SetWorkingDir 先落库再改内存),
// 因此调用方看到错误即可知道实际用的还是默认目录 —— 显示与实际一致。
func applyGitDirArg(application *app.App, dir string) error {
	if dir == "" {
		return nil
	}
	if err := application.GitAPI.SetWorkingDir(dir); err != nil {
		return err
	}
	logx.Info("已按启动参数设置 Git 工作目录", "dir", dir)
	return nil
}

// runServer HTTP 服务器模式:REST API + SSE + 静态前端资源。
// 非 API 且无扩展名的路径回退到 index.html(SPA 前端路由)。
//
// 返回错误而不是自行 os.Exit:退出码由 run() 统一决定,
// 这样 defer(关闭 SQLite)一定会执行。
func runServer(app *app.App, addr string) error {
	apiHandler := api.NewServer(app)

	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return fmt.Errorf("嵌入资源加载失败: %w", err)
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
		defer signal.Stop(sig)
		<-sig
		logx.Info("收到退出信号,正在关闭…")
		// 关闭也要有上限:挂死的请求不能把关机无限拖住
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		// 给运行中的执行最多 5 秒收尾(状态落库为 CANCELLED)
		if left := app.Engine.WaitIdle(5 * time.Second); left > 0 {
			logx.Warn("仍有执行未收尾", "count", left)
		}
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP 服务退出: %w", err)
	}
	// 等待执行协程收尾后再退出
	_ = app.Engine.WaitIdle(5 * time.Second)
	return nil
}

// runDesktop Wails 桌面模式。
func runDesktop(app *app.App) error {
	// REST API 处理器:桌面模式下经 AssetHandler 挂载到 AssetServer 的 /api
	// 前缀,导出/备份等下载链接与服务器模式同源可用
	apiHandler := api.NewServer(app)

	// 先声明后赋值:SingleInstance 回调需要引用 wailsApp 聚焦窗口
	var wailsApp *wails.App
	notifSvc := notifications.New()
	wailsApp = wails.New(wails.Options{
		Name:        "Agent Workflow Orchestrator",
		Description: "可配置、可视化、可扩展的多 Agent 工作流编排",
		// Wails 内部日志走 logx(脱敏 + 级别跟随设置页)
		Logger: logx.Logger(),
		Services: []wails.Service{
			wails.NewService(app.Workflows),
			wails.NewService(app.Executions),
			wails.NewService(app.AgentSvc),
			wails.NewService(app.SkillSvc),
			wails.NewService(app.GitAPI),
			wails.NewService(app.Settings),
			wails.NewServiceWithOptions(&api.AssetHandler{Srv: apiHandler},
				wails.ServiceOptions{Route: "/api"}),
			wails.NewService(notifSvc),
		},
		// 退出前给运行中的执行一次收尾机会(状态落库),与服务器模式对齐
		OnShutdown: func() {
			if left := app.Engine.WaitIdle(5 * time.Second); left > 0 {
				logx.Warn("仍有执行未收尾,已强制退出", "count", left)
			}
		},
		// 单实例:数据目录只有一个 SQLite,双开会并发写库;
		// 二次启动改为聚焦已有窗口
		SingleInstance: &wails.SingleInstanceOptions{
			UniqueID: "com.agent-workflow.orchestrator",
			OnSecondInstanceLaunch: func(data wails.SecondInstanceData) {
				if win, ok := wailsApp.Window.GetByName("main"); ok {
					win.Show()
					win.Focus()
				}
			},
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
	bridgeNotifications(app, notifSvc, wailsApp)

	wailsApp.Window.NewWithOptions(wails.WebviewWindowOptions{
		Name:   "main",
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
	// 记录窗口创建:桌面模式无法在浏览器里观察(没有 HTTP 端口),
	// 视觉验证与"窗口为什么没出来"这类排障只能靠日志。
	if win, ok := wailsApp.Window.GetByName("main"); ok && win != nil {
		logx.Info("主窗口已创建", "name", "main", "title", "Agent Workflow Orchestrator")
	} else {
		logx.Warn("主窗口创建失败")
	}

	if err := wailsApp.Run(); err != nil {
		return fmt.Errorf("Wails 应用退出异常: %w", err)
	}
	return nil
}

// bridgeNotifications 将执行生命周期事件桥接为系统原生通知。
// WKWebView 对 Web Notification 的权限支持不可靠,桌面通知由 Go 侧
// 发出(前端在桌面模式跳过 Web 通知,避免双重提醒)。
func bridgeNotifications(app *app.App, ns *notifications.NotificationService, wailsApp *wails.App) {
	var state int32 // 原子:0 未请求授权,1 已授权,-1 不可用
	_ = app.Subscribe(func(ev event.UIEvent) {
		var title, body string
		switch ev.Type {
		case event.WorkflowCompleted:
			title = "工作流已完成"
		case event.WorkflowFailed:
			title = "工作流失败"
			body = fmt.Sprint(ev.Data["error"])
		case event.HumanInputRequired:
			title = "工作流等待你的输入"
			body = fmt.Sprint(ev.Data["prompt"])
		default:
			return
		}
		if r := []rune(body); len(r) > 120 {
			body = string(r[:120]) + "…"
		}

		switch atomic.LoadInt32(&state) {
		case 0:
			granted, err := ns.RequestNotificationAuthorization()
			if err != nil || !granted {
				atomic.StoreInt32(&state, -1)
				logx.Debug("通知授权不可用,本次会话不再尝试", "granted", granted, "error", fmt.Sprint(err))
				return
			}
			atomic.StoreInt32(&state, 1)
		case -1:
			return
		}
		err := ns.SendNotification(notifications.NotificationOptions{
			ID:       ev.ExecutionID + ":" + ev.Type,
			Title:    title,
			Body:     body,
			ThreadID: ev.ExecutionID,
			Data:     map[string]interface{}{"execution_id": ev.ExecutionID},
		})
		if err != nil {
			logx.Debug("发送通知失败", "error", err.Error())
		}
	})

	// 点击通知:聚焦窗口并让前端跳转到对应执行
	ns.OnNotificationResponse(func(result notifications.NotificationResult) {
		if result.Error != nil {
			return
		}
		if win, ok := wailsApp.Window.GetByName("main"); ok {
			win.Show()
			win.Focus()
		}
		if id, ok := result.Response.UserInfo["execution_id"].(string); ok && id != "" {
			wailsApp.Event.Emit("ui:open-execution", id)
		}
	})
}
