// @vitest-environment jsdom
// GitPanel:三个查询各自独立成败 —— 一个失败不能把整个面板打成"加载中…"。
//
// 此前用 Promise.all:工作目录不是 git 仓库时 git status 报错,整个
// refresh 一起 reject,状态面板永远停在"加载中…",用户只看到一句 toast,
// 不知道面板为什么是空的。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { toast } from "sonner";
import GitPanel from "./GitPanel";
import { settle } from "@/test/helpers";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

const m = vi.hoisted(() => ({
  gitStatus: vi.fn(),
  gitDiff: vi.fn(),
  gitLog: vi.fn(),
  gitCommit: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      gitStatus: m.gitStatus,
      gitDiff: m.gitDiff,
      gitLog: m.gitLog,
      gitCommit: m.gitCommit,
    },
  };
});

beforeEach(() => {
  vi.useFakeTimers();
  m.gitStatus.mockReset().mockResolvedValue({
    branch: "main",
    staged: ["a.txt"],
    modified: [],
    untracked: [],
  });
  m.gitDiff.mockReset().mockResolvedValue("diff --git a/a.txt");
  m.gitLog.mockReset().mockResolvedValue([]);
  m.gitCommit.mockReset().mockResolvedValue("[main 123] msg");
  vi.mocked(toast.error).mockClear();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("GitPanel 部分失败", () => {
  it("status 查询失败时显示原因,且不影响 Diff 展示", async () => {
    m.gitStatus.mockRejectedValue(new Error("git status 失败: not a git repository"));
    render(<GitPanel />);
    await settle();
    expect(screen.getByText(/状态查询失败/)).toBeTruthy();
    expect(screen.queryByText("加载中…")).toBeNull();
    // diff 成功 → 仍然展示
    expect(screen.getByText("diff --git a/a.txt")).toBeTruthy();
    expect(vi.mocked(toast.error).mock.calls[vi.mocked(toast.error).mock.calls.length - 1]?.[0]).toContain("Git 状态");
  });

  it("Log 查询失败不影响状态面板", async () => {
    m.gitLog.mockRejectedValue(new Error("git log 失败"));
    render(<GitPanel />);
    await settle();
    expect(screen.getByText("main")).toBeTruthy();
    expect(vi.mocked(toast.error).mock.calls[vi.mocked(toast.error).mock.calls.length - 1]?.[0]).toContain("Log");
  });

  it("全部成功时不报错", async () => {
    render(<GitPanel />);
    await settle();
    expect(screen.getByText("a.txt")).toBeTruthy();
    expect(toast.error).not.toHaveBeenCalled();
  });
});
