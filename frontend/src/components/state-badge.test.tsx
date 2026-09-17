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

  it("未知状态回退为原始文本", () => {
    render(<StateBadge state="SOME_FUTURE_STATE" />);
    expect(screen.getByText("SOME_FUTURE_STATE")).toBeTruthy();
  });
});
