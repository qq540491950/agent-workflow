// @vitest-environment jsdom
// StateBadge:执行/节点状态的统一渲染(标签 + 颜色)。
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { StateBadge } from "./state-badge";

describe("StateBadge", () => {
  it.each([
    ["RUNNING", "Running"],
    ["WAITING_USER", "Waiting User"],
    ["COMPLETED", "Completed"],
    ["FAILED", "Failed"],
    ["CANCELLED", "Cancelled"],
    ["SUCCESS", "Success"],
    ["WAITING", "Waiting"],
  ])("%s 渲染标签 %s", (state, label) => {
    render(<StateBadge state={state} />);
    expect(screen.getByText(label)).toBeTruthy();
  });

  // PAUSED 已从执行状态机移除(它从来不可达:没有任何路径会迁移到它),
  // 老库里的遗留行在读出口已归一为 CANCELLED → 前端不再需要它的徽章。
  it("PAUSED 回退为原始文本(该状态已从状态机移除)", () => {
    render(<StateBadge state="PAUSED" />);
    expect(screen.getByText("PAUSED")).toBeTruthy();
    expect(screen.queryByText("Paused")).toBeNull();
  });

  it("未知状态回退为原始文本", () => {
    render(<StateBadge state="SOME_FUTURE_STATE" />);
    expect(screen.getByText("SOME_FUTURE_STATE")).toBeTruthy();
  });
});
