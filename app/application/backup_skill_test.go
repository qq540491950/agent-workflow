package application

import (
	"errors"
	"testing"

	"agentworkflow/workflow/model"
)

// 备份的两项口径修正:
//
//  1. `version` 此前**只写不读**:导出时写了 version:1,恢复时从头到尾
//     没看过它 —— 拿一份格式完全不同的 JSON 来恢复,也会被当成"备份"
//     照单全收(只跳过解析不了的部分),用户看到"恢复成功"却什么也没恢复。
//     现在恢复必须**校验** version:缺失/不认识 → 显式报错。
//
//  2. 备份缺 `skill_configs` 段:技能的**启用/禁用状态**在备份里彻底
//     消失 —— 用户换了机器、恢复了备份,此前禁用的技能又全部可用了
//     (禁用的技能在运行期会被拒绝调用,这是一条安全相关的状态)。
//     现在导出带上、恢复写回(只对本机确实存在的技能)。
//
// **向后兼容**(硬要求):
//   - 现有备份的 version 是 1 → 识别为 v1,正常路径;
//   - 老备份没有 skill_configs 段 → 视为"不恢复技能状态",不报错。

// errorCode 取结构化错误的 code(没有结构化错误时返回空串)。
func errorCode(err error) string {
	var se *model.Error
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

// TestRestoreBackupRejectsMissingVersion 缺少 version 的备份必须被拒。
//
// 修前(红):直接当备份处理,返回"恢复成功"。
func TestRestoreBackupRejectsMissingVersion(t *testing.T) {
	a := newTestApp(t)
	_, err := a.Settings.RestoreBackup(map[string]any{
		"workflows": []any{},
	})
	if err == nil {
		t.Fatal("缺少 version 的备份必须报错(此前只写不读,任何 JSON 都被当备份)")
	}
	if code := errorCode(err); code != "BAD_REQUEST" {
		t.Errorf("错误码应为 BAD_REQUEST,实际 %q(err=%v)", code, err)
	}
}

// TestRestoreBackupRejectsUnknownVersion 不认识的版本号必须被拒(fail closed)。
//
// 未来的备份格式(比如 version:2 带新字段)不能被当前版本按老格式硬读 ——
// 那会静默丢掉新格式里的数据。
func TestRestoreBackupRejectsUnknownVersion(t *testing.T) {
	a := newTestApp(t)
	_, err := a.Settings.RestoreBackup(map[string]any{
		"version":   99,
		"workflows": []any{},
	})
	if err == nil {
		t.Fatal("不认识的备份版本必须报错")
	}
	if code := errorCode(err); code != "BAD_REQUEST" {
		t.Errorf("错误码应为 BAD_REQUEST,实际 %q(err=%v)", code, err)
	}
}

// TestRestoreBackupAcceptsVersionOne version:1 是现有备份的版本号 →
// 正常路径(向后兼容:老备份必须仍能恢复)。
func TestRestoreBackupAcceptsVersionOne(t *testing.T) {
	a := newTestApp(t)
	res, err := a.Settings.RestoreBackup(map[string]any{
		"version":   1,
		"workflows": []any{},
	})
	if err != nil {
		t.Fatalf("version=1 的备份必须能恢复: %v", err)
	}
	if _, ok := res["workflows_restored"]; !ok {
		t.Errorf("恢复结果缺少 workflows_restored: %v", res)
	}
}

// TestExportBackupIncludesSkillConfigs 导出必须带上技能启用状态。
//
// 修前(红):导出里根本没有 skill_configs 段。
func TestExportBackupIncludesSkillConfigs(t *testing.T) {
	a := newTestApp(t)
	if err := a.SkillSvc.SetEnabled("log", false); err != nil {
		t.Fatalf("禁用技能: %v", err)
	}

	backup, err := a.Settings.ExportBackup()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, ok := backup["skill_configs"]
	if !ok {
		t.Fatalf("导出缺少 skill_configs 段(技能禁用状态会静默丢失): %v", backup)
	}
	cfgs, ok := raw.(map[string]bool)
	if !ok {
		t.Fatalf("skill_configs 应为 map[string]bool,实际 %T", raw)
	}
	enabled, ok := cfgs["log"]
	if !ok {
		t.Errorf("skill_configs 里没有 log: %v", cfgs)
	} else if enabled {
		t.Errorf("log 已被禁用,导出里应是 enabled=false: %v", cfgs)
	}
	if enabled, ok := cfgs["submit"]; !ok || !enabled {
		t.Errorf("未禁用的技能应导出为 enabled=true: %v", cfgs)
	}
}

// TestRestoreBackupRestoresSkillState 恢复必须把技能启用状态写回。
//
// 修前(红):备份里没有这段,恢复后技能状态保持目标机器的现状 ——
// 此前禁用的技能又能在工作流里被调用了。
func TestRestoreBackupRestoresSkillState(t *testing.T) {
	a1 := newTestApp(t)
	if err := a1.SkillSvc.SetEnabled("log", false); err != nil {
		t.Fatalf("禁用技能: %v", err)
	}
	backup, err := a1.Settings.ExportBackup()
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	a2 := newTestApp(t)
	if _, err := a2.Settings.RestoreBackup(backup); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !a2.SkillDisabled("log") {
		t.Error("备份里的技能禁用状态未恢复(log 又是启用状态)")
	}
	if a2.SkillDisabled("submit") {
		t.Error("未禁用的技能被误禁(submit)")
	}
	// 落库也要对上:重启后仍应是禁用的。
	disabled, err := a2.Repo.ListDisabledSkills()
	if err != nil {
		t.Fatalf("读取禁用技能: %v", err)
	}
	if !disabled["log"] {
		t.Errorf("禁用状态未落库: %v", disabled)
	}
}

// TestRestoreBackupSkipsUnknownSkill 备份里有本机不存在的技能时,
// 跳过并如实计数(与未知 Agent 同一口径:不落无效数据、不算已恢复)。
func TestRestoreBackupSkipsUnknownSkill(t *testing.T) {
	a := newTestApp(t)
	res, err := a.Settings.RestoreBackup(map[string]any{
		"version":   1,
		"workflows": []any{},
		"skill_configs": map[string]any{
			"log":            false,
			"ghost-skill-xy": false,
		},
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n, _ := res["skill_configs_restored"].(int); n != 1 {
		t.Errorf("skill_configs_restored = %v,期望 1", res["skill_configs_restored"])
	}
	ids, _ := res["skill_configs_unknown_ids"].([]string)
	if len(ids) != 1 || ids[0] != "ghost-skill-xy" {
		t.Errorf("应列出本机不存在的技能 ID,实际: %v", res["skill_configs_unknown_ids"])
	}
}

// TestRestoreBackupWithoutSkillConfigs 老备份(没有 skill_configs 段)
// 必须仍能恢复,且不改动技能状态(向后兼容)。
func TestRestoreBackupWithoutSkillConfigsIsOK(t *testing.T) {
	a := newTestApp(t)
	res, err := a.Settings.RestoreBackup(map[string]any{
		"version":   1,
		"workflows": []any{},
	})
	if err != nil {
		t.Fatalf("老备份(没有 skill_configs 段)必须仍能恢复: %v", err)
	}
	if n, _ := res["skill_configs_restored"].(int); n != 0 {
		t.Errorf("老备份没有技能段,不该有已恢复的技能,实际 %v", res["skill_configs_restored"])
	}
	if a.SkillDisabled("log") {
		t.Error("老备份不得改动本机的技能状态")
	}
}
