// labels:执行/节点状态等枚举值的中文映射(中文化专项的唯一来源)。
// 未知状态回退原文是硬性行为:新状态上线而前端未跟上时,显示原文
// 好过显示错误的中文(StateBadge 此前对未知值同样回退原文)。
import { describe, expect, it } from "vitest";
import { enabledLabel, modeLabel, stateLabel, STATE_LABELS } from "./labels";

describe("stateLabel", () => {
  it.each([
    ["RUNNING", "运行中"],
    ["WAITING_USER", "等待人工"],
    ["COMPLETED", "已完成"],
    ["FAILED", "失败"],
    ["CANCELLED", "已取消"],
    ["CREATED", "已创建"],
    ["SUCCESS", "成功"],
    ["PENDING", "待执行"],
    ["WAITING", "等待中"],
    ["SKIPPED", "已跳过"],
  ])("%s → %s", (state, label) => {
    expect(stateLabel(state)).toBe(label);
  });

  it("未知状态回退原文(不猜)", () => {
    expect(stateLabel("SOME_FUTURE_STATE")).toBe("SOME_FUTURE_STATE");
    expect(stateLabel("PAUSED")).toBe("PAUSED");
  });
});

describe("modeLabel", () => {
  it("desktop/server → 桌面/服务器", () => {
    expect(modeLabel("desktop")).toBe("桌面");
    expect(modeLabel("server")).toBe("服务器");
  });

  it("未知模式回退原文", () => {
    expect(modeLabel("…")).toBe("…");
  });
});

describe("enabledLabel", () => {
  it("启用/禁用两态", () => {
    expect(enabledLabel(true)).toBe("已启用");
    expect(enabledLabel(false)).toBe("已禁用");
  });
});

describe("STATE_LABELS 完整性", () => {
  it("执行与节点状态各归其位(防止映射被误删)", () => {
    for (const s of [
      "CREATED",
      "RUNNING",
      "WAITING_USER",
      "COMPLETED",
      "FAILED",
      "CANCELLED",
      "PENDING",
      "SUCCESS",
      "WAITING",
      "SKIPPED",
    ]) {
      expect(STATE_LABELS[s]).toBeTruthy();
    }
  });
});
