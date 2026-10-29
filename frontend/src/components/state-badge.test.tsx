// @vitest-environment jsdom
// StateBadge:执行/节点状态的统一渲染(标签 + 颜色)。
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { StateBadge } from "./state-badge";

afterEach(cleanup);

describe("StateBadge", () => {
  // 文案统一走 lib/labels.ts 的 stateLabel:已知状态中文,未知回退原文。
  it.each([
    ["RUNNING", "运行中"],
    ["WAITING_USER", "等待人工"],
    ["COMPLETED", "已完成"],
    ["FAILED", "失败"],
    ["CANCELLED", "已取消"],
    ["CREATED", "已创建"],
    ["SUCCESS", "成功"],
    ["PENDING", "待执行"],
    ["SKIPPED", "已跳过"],
    ["WAITING", "等待中"],
  ])("%s 渲染标签 %s", (state, label) => {
    render(<StateBadge state={state} />);
    expect(screen.getByText(label)).toBeTruthy();
  });

  // PAUSED 已从执行状态机移除(它从来不可达:没有任何路径会迁移到它),
  // 老库里的遗留行在读出口已归一为 CANCELLED → 前端不再需要它的徽章。
  it("PAUSED 回退为原始文本(该状态已从状态机移除)", () => {
    render(<StateBadge state="PAUSED" />);
    expect(screen.getByText("PAUSED")).toBeTruthy();
    expect(screen.queryByText("等待中")).toBeNull();
  });

  it("未知状态回退为原始文本", () => {
    render(<StateBadge state="SOME_FUTURE_STATE" />);
    expect(screen.getByText("SOME_FUTURE_STATE")).toBeTruthy();
  });
});
