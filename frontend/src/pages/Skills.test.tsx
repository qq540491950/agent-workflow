// @vitest-environment jsdom
// Skills:启用开关必须 in-flight 加锁(连点只发一次)+ 失败回滚。
//
// 第一轮记为"存疑未改":同类的 Agents 页权限开关已经加了锁(乐观更新 +
// 失败回滚 + 同行 in-flight 锁),而 Skill 开关没有 —— 连点会并发打多次
// /api/skills/{id}/enable,最终生效值取决于请求到达顺序,界面显示的却是
// 刷新回来的值,两者可能对不上。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { toast } from "sonner";
import Skills from "./Skills";
import { settle } from "@/test/helpers";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

const m = vi.hoisted(() => ({
  listSkills: vi.fn(),
  setSkillEnabled: vi.fn(),
  testSkill: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      listSkills: m.listSkills,
      setSkillEnabled: m.setSkillEnabled,
      testSkill: m.testSkill,
    },
  };
});

const skill = {
  id: "skill-1",
  name: "审查",
  description: "描述",
  enabled: true,
};

beforeEach(() => {
  vi.useFakeTimers();
  m.listSkills.mockReset().mockResolvedValue([{ ...skill }]);
  m.setSkillEnabled.mockReset().mockResolvedValue(undefined);
  m.testSkill.mockReset().mockResolvedValue("out");
  vi.mocked(toast.error).mockClear();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function renderSkills() {
  return render(<Skills />);
}

describe("Skills 启用开关", () => {
  it("请求未完成时开关锁定,连点只发一次请求", async () => {
    let release: () => void = () => {};
    m.setSkillEnabled.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          release = resolve;
        }),
    );
    renderSkills();
    await settle();
    const sw = screen.getAllByRole("switch")[0] as HTMLButtonElement;
    fireEvent.click(sw);
    await settle();
    expect(m.setSkillEnabled).toHaveBeenCalledTimes(1);
    expect(m.setSkillEnabled).toHaveBeenCalledWith("skill-1", false);
    // in-flight 期间开关禁用:连点不会再打第二次请求
    expect((sw as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(sw);
    await settle();
    expect(m.setSkillEnabled).toHaveBeenCalledTimes(1);
    release();
    await settle();
  });

  it("点击后立即乐观更新(不等刷新才动)", async () => {
    let release: () => void = () => {};
    m.setSkillEnabled.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          release = resolve;
        }),
    );
    renderSkills();
    await settle();
    const sw = screen.getAllByRole("switch")[0] as HTMLButtonElement;
    fireEvent.click(sw);
    await settle();
    expect(sw.getAttribute("data-state")).toBe("unchecked");
    release();
    await settle();
  });

  it("失败时回滚开关并提示错误", async () => {
    m.setSkillEnabled.mockRejectedValue(new Error("启用失败"));
    renderSkills();
    await settle();
    const sw = screen.getAllByRole("switch")[0] as HTMLButtonElement;
    fireEvent.click(sw);
    await settle();
    expect(toast.error).toHaveBeenCalledWith("启用失败");
    // 回滚到服务端状态(仍为启用)
    expect(sw.getAttribute("data-state")).toBe("checked");
  });
});
