package api

import (
	"runtime/debug"
	"sync"
)

var (
	versionOnce   sync.Once
	cachedVersion map[string]string
)

// VersionInfo 返回应用与关键依赖的版本信息。
// ADK/Wails 版本从构建信息读取(模块升级后自动跟上,不再硬编码);
// 构建信息不可用时省略对应键,前端按存在性渲染。
func VersionInfo() map[string]string {
	versionOnce.Do(func() {
		cachedVersion = map[string]string{
			"app":     "Agent Workflow Orchestrator",
			"version": "0.1.0",
		}
		bi, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		for _, d := range bi.Deps {
			switch d.Path {
			case "google.golang.org/adk":
				cachedVersion["adk"] = d.Path + " " + d.Version
			case "github.com/wailsapp/wails/v3":
				cachedVersion["wails"] = d.Version
			}
		}
		for _, kv := range bi.Settings {
			if kv.Key == "vcs.revision" && len(kv.Value) >= 7 {
				cachedVersion["commit"] = kv.Value[:7]
			}
		}
	})
	out := make(map[string]string, len(cachedVersion))
	for k, v := range cachedVersion {
		out[k] = v
	}
	return out
}
