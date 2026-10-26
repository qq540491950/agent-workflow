package application

import (
	"strings"
	"testing"

	"agentworkflow/logx"
)

// 本文件锁住"设置行读不出来"时的语义(迭代 19 / P3-2)。
//
// 背景:`SettingsService.Info()` 此前是
//
//	_ = s.app.Repo.GetSetting("git_working_dir", &wd)   // 错误被吞
//	if wd == "" { wd = s.app.GitSvc.WorkingDir() }        // 静默回落到内存态
//
// 设置行损坏(存的不是字符串)或 DB 读失败时,读错误被完全吞掉,界面显示的
// 是 **Git 服务的内存态目录** —— 不是库里那个。用户以为看到的是库里的值,
// 一保存就把损坏行覆盖成内存值,损坏事实就此消失(约定 14:该失败却成功)。
//
// 现在:读失败必须**留痕**(ERROR 日志),并且在返回结构里**明确标注**
// "这不是库里的值"(SettingsInfo.GitWorkingDirReadFailed)。
//
// 读失败与"从未设置过"必须区分开:后者是正常初始状态(库里没有这一行),
// 不是失败,不该报错、更不该被标注成失败。

// captureLogx 捕获 logx 的输出(测试用)。
//
// logx.SetOutput 只换输出目的地(不改变生效级别);本包没有 t.Parallel
// 的用例(见 randid_test.go 的说明),替换是安全的。
func captureLogx(t *testing.T) (*strings.Builder, func()) {
	t.Helper()
	var buf strings.Builder
	restore := logx.SetOutput(&buf)
	return &buf, func() { restore() }
}

// corruptGitWorkingDirRow 把 git_working_dir 这一行写成"读不出来"的样子。
//
// 走公开 API(SaveSetting)写入一个数字:存下来的是 JSON 数字,而
// GetSetting 把它解进 string 变量 → 解码失败 → 读失败。
// 这正是"设置行损坏"的真实形态(不是 DB 故障,而是行内容坏了)。
func corruptGitWorkingDirRow(t *testing.T, a *App) {
	t.Helper()
	if err := a.Repo.SaveSetting("git_working_dir", 12345); err != nil {
		t.Fatalf("写入损坏的设置行: %v", err)
	}
}

// TestSettingsInfoLogsErrorWhenGitWorkingDirUnreadable 读失败必须留 ERROR 日志。
//
// 此前读错误被 `_ =` 吞掉,一行日志都没有 —— "界面显示的不是库里那个"
// 这件事完全不可见,排查时无从下手。
func TestSettingsInfoLogsErrorWhenGitWorkingDirUnreadable(t *testing.T) {
	a := newTestApp(t)
	corruptGitWorkingDirRow(t, a)

	buf, restore := captureLogx(t)
	defer restore()

	_ = a.Settings.Info()

	out := buf.String()
	if !strings.Contains(out, "git_working_dir") {
		t.Errorf("读失败必须留下提到 git_working_dir 的日志,实际日志: %q", out)
	}
	if !strings.Contains(out, "ERROR") {
		t.Errorf("读失败必须记 ERROR(不是 WARN/INFO),实际日志: %q", out)
	}
}

// TestSettingsInfoMarksGitWorkingDirReadFailure 读失败必须在返回结构里标注。
//
// 这是"不要拿 Git 服务目录冒充库值"的落点:消费方拿到这个标记,就知道
// 上面的 git_working_dir 是内存态、不是库里的值 —— 可以据此提示用户,
// 而不是把损坏行当成"用户设置的值"回存(回存即覆盖,损坏事实消失)。
func TestSettingsInfoMarksGitWorkingDirReadFailure(t *testing.T) {
	a := newTestApp(t)
	corruptGitWorkingDirRow(t, a)

	info := a.Settings.Info()
	if !info.GitWorkingDirReadFailed {
		t.Error("库值读不出来时必须在返回结构里标注读失败")
	}
}

// TestSettingsInfoReadFailureMarkerFalseWhenReadable 行读得出来时不得标注失败。
//
// 标记必须只在"真的读不出来"时为 true:否则界面会对一个正常的设置
// 报"读失败",用户白忙一场(误报同样是 bug)。
func TestSettingsInfoReadFailureMarkerFalseWhenReadable(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	if err := a.Repo.SaveSetting("git_working_dir", dir); err != nil {
		t.Fatalf("写入设置行: %v", err)
	}

	info := a.Settings.Info()
	if info.GitWorkingDirReadFailed {
		t.Error("行读得出来时不得标注读失败")
	}
	if info.GitWorkingDir != dir {
		t.Errorf("库值可读时应显示库里的目录: %q, want %q", info.GitWorkingDir, dir)
	}
}

// TestSettingsInfoNoRowIsNotAReadFailure "从未设置过"不是读失败。
//
// 库里没有这一行 = 正常初始状态(全新安装),不该报错、更不该被标注成失败。
func TestSettingsInfoNoRowIsNotAReadFailure(t *testing.T) {
	a := newTestApp(t)

	buf, restore := captureLogx(t)
	defer restore()

	_ = a.Settings.Info()

	if out := buf.String(); strings.Contains(out, "ERROR") {
		t.Errorf("设置项不存在属于正常初始状态,不应记 ERROR,实际日志: %q", out)
	}
}

// TestSettingsInfoClearedRowIsNotAReadFailure 空串(用户主动清除)不是读失败。
//
// 空串 = 清除(回到启动默认目录),是有意义的库值,读成功。
func TestSettingsInfoClearedRowIsNotAReadFailure(t *testing.T) {
	a := newTestApp(t)
	if err := a.Repo.SaveSetting("git_working_dir", ""); err != nil {
		t.Fatalf("写入空串: %v", err)
	}

	buf, restore := captureLogx(t)
	defer restore()

	_ = a.Settings.Info()

	if out := buf.String(); strings.Contains(out, "ERROR") {
		t.Errorf("空串(清除)是正常库值,不应记 ERROR,实际日志: %q", out)
	}
}

// TestNewAppLogsErrorWhenGitWorkingDirUnreadable 启动时读失败也不该静默。
//
// 启动路径此前是 `if err := repo.GetSetting(...); err == nil && wd != ""`:
// 读失败 → 静默用默认目录 → 进程内一切正常,但库里那一行已经坏了,
// 而用户看到的目录不是他要的那个。全程零日志。
func TestNewAppLogsErrorWhenGitWorkingDirUnreadable(t *testing.T) {
	dir := t.TempDir()

	// 先造一个"行已损坏"的库:起一次应用、写入损坏行、关库。
	seed, err := NewApp(dir, nil)
	if err != nil {
		t.Fatalf("准备种子库: %v", err)
	}
	corruptGitWorkingDirRow(t, seed)
	if err := seed.Repo.Close(); err != nil {
		t.Fatalf("关闭种子库: %v", err)
	}

	buf, restore := captureLogx(t)
	defer restore()

	// 用同一个数据目录重新启动(走的就是应用启动时的读路径)。
	app, err := NewApp(dir, nil)
	if err != nil {
		t.Fatalf("重启应用: %v", err)
	}
	t.Cleanup(func() { _ = app.Repo.Close() })

	out := buf.String()
	if !strings.Contains(out, "git_working_dir") {
		t.Errorf("启动读失败必须留下提到 git_working_dir 的日志,实际日志: %q", out)
	}
	if !strings.Contains(out, "ERROR") {
		t.Errorf("启动读失败必须记 ERROR,实际日志: %q", out)
	}
}

// ---- log_level / allow_script_nodes 的库值体检(#287)----
//
// 与 git_working_dir 同一族:Info() 响应里的 log_level / allow_script_nodes
// 是"当前生效值",但生效值可能来自"启动时读失败回落默认" —— 库里那行
// 坏了这件事此前在响应里完全不可见。现在读失败必须在响应里标注。

// TestSettingsInfoMarksLogLevelReadFailure log_level 行损坏必须标注。
func TestSettingsInfoMarksLogLevelReadFailure(t *testing.T) {
	a := newTestApp(t)
	// 走公开 API 写入数字:GetSetting 把它解进 string → 解码失败 → 读失败。
	if err := a.Repo.SaveSetting("log_level", 12345); err != nil {
		t.Fatalf("写入损坏的 log_level 行: %v", err)
	}

	buf, restore := captureLogx(t)
	defer restore()

	info := a.Settings.Info()
	if !info.LogLevelReadFailed {
		t.Error("log_level 行读不出来时必须在返回结构里标注读失败")
	}
	if out := buf.String(); !strings.Contains(out, "log_level") || !strings.Contains(out, "ERROR") {
		t.Errorf("log_level 读失败必须留 ERROR 日志,实际日志: %q", out)
	}
}

// TestSettingsInfoMarksAllowScriptNodesReadFailure allow_script_nodes 行损坏必须标注。
func TestSettingsInfoMarksAllowScriptNodesReadFailure(t *testing.T) {
	a := newTestApp(t)
	// 写入字符串:GetSetting 把它解进 bool → 解码失败 → 读失败。
	if err := a.Repo.SaveSetting("allow_script_nodes", "yes"); err != nil {
		t.Fatalf("写入损坏的 allow_script_nodes 行: %v", err)
	}

	buf, restore := captureLogx(t)
	defer restore()

	info := a.Settings.Info()
	if !info.AllowScriptNodesReadFailed {
		t.Error("allow_script_nodes 行读不出来时必须在返回结构里标注读失败")
	}
	if out := buf.String(); !strings.Contains(out, "allow_script_nodes") || !strings.Contains(out, "ERROR") {
		t.Errorf("allow_script_nodes 读失败必须留 ERROR 日志,实际日志: %q", out)
	}
}

// TestSettingsInfoHealthyRowsCarryNoMarkers 行正常时三个读失败标记都不得出现
// (误报同样是 bug;omitempty 正常情况下保持响应形状不变)。
func TestSettingsInfoHealthyRowsCarryNoMarkers(t *testing.T) {
	a := newTestApp(t)
	// 走公开 Set(库值 + 内存态一起更新,与真实保存路径同源;直接写库
	// 不会更新内存态,而 Info() 显示的就是内存态生效值)。
	if err := a.Settings.Set("log_level", "debug"); err != nil {
		t.Fatalf("设置 log_level: %v", err)
	}
	if err := a.Settings.Set("allow_script_nodes", true); err != nil {
		t.Fatalf("设置 allow_script_nodes: %v", err)
	}

	info := a.Settings.Info()
	if info.GitWorkingDirReadFailed || info.LogLevelReadFailed || info.AllowScriptNodesReadFailed {
		t.Errorf("行全部可读时不得标注读失败: %+v", info)
	}
	if info.LogLevel != "debug" {
		t.Errorf("生效值应与库值一致: %q, want debug", info.LogLevel)
	}
	if !info.AllowScriptNodes {
		t.Errorf("生效值应与库值一致: %v, want true", info.AllowScriptNodes)
	}
}
