// @vitest-environment jsdom
// Workflows:危险/异步操作的错误必须被捕获并提示。
//
// 此前启用开关、复制、下载 YAML、复制 YAML、删除这几处都是裸 await,
// 失败时是 unhandled rejection:界面毫无反馈,用户以为按钮坏了。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { toast } from "sonner";
import Workflows from "./Workflows";
import { settle } from "@/test/helpers";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

const m = vi.hoisted(() => ({
  listWorkflows: vi.fn(),
  stats: vi.fn(),
  setEnabled: vi.fn(),
  deleteWorkflow: vi.fn(),
  duplicateWorkflow: vi.fn(),
  exportYAML: vi.fn(),
  copyYAML: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      listWorkflows: m.listWorkflows,
      stats: m.stats,
      setEnabled: m.setEnabled,
      deleteWorkflow: m.deleteWorkflow,
      duplicateWorkflow: m.duplicateWorkflow,
      exportYAML: m.exportYAML,
      copyYAML: m.copyYAML,
    },
  };
});

const wf = {
  id: "wf-1",
  name: "评审流程",
  description: "描述",
  version: 1,
  enabled: true,
  nodes: [],
  edges: [],
  settings: {},
};

beforeEach(() => {
  vi.useFakeTimers();
  m.listWorkflows.mockReset().mockResolvedValue([wf]);
  m.stats.mockReset().mockResolvedValue([]);
  m.setEnabled.mockReset().mockResolvedValue(wf);
  m.deleteWorkflow.mockReset().mockResolvedValue(undefined);
  m.duplicateWorkflow.mockReset().mockResolvedValue(wf);
  m.exportYAML.mockReset().mockResolvedValue("workflow:\n  id: wf-1\n");
  m.copyYAML.mockReset().mockResolvedValue("workflow:\n  id: wf-1\n");
  vi.mocked(toast.error).mockClear();
  vi.mocked(toast.success).mockClear();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function renderWorkflows() {
  return render(
    <MemoryRouter>
      <Workflows />
    </MemoryRouter>,
  );
}

describe("Workflows 启用开关", () => {
  it("失败时提示错误(不得静默吞掉)", async () => {
    m.setEnabled.mockRejectedValue(new Error("权限不足"));
    renderWorkflows();
    await settle();
    fireEvent.click(screen.getAllByRole("switch")[0]);
    await settle();
    expect(m.setEnabled).toHaveBeenCalledWith("wf-1", false);
    expect(toast.error).toHaveBeenCalledWith("权限不足");
  });

  it("成功时不报错", async () => {
    renderWorkflows();
    await settle();
    fireEvent.click(screen.getAllByRole("switch")[0]);
    await settle();
    expect(m.setEnabled).toHaveBeenCalledWith("wf-1", false);
    expect(toast.error).not.toHaveBeenCalled();
  });
});

describe("Workflows 下拉菜单操作", () => {
  it("复制失败时提示错误", async () => {
    m.duplicateWorkflow.mockRejectedValue(new Error("复制失败"));
    renderWorkflows();
    await settle();
    openMenu();
    fireEvent.click(screen.getByText("复制"));
    await settle();
    expect(m.duplicateWorkflow).toHaveBeenCalledWith("wf-1");
    expect(toast.error).toHaveBeenCalledWith("复制失败");
  });

  it("删除失败时提示错误", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    m.deleteWorkflow.mockRejectedValue(new Error("删除失败"));
    renderWorkflows();
    await settle();
    openMenu();
    fireEvent.click(screen.getByText("删除"));
    await settle();
    expect(m.deleteWorkflow).toHaveBeenCalledWith("wf-1");
    expect(toast.error).toHaveBeenCalledWith("删除失败");
  });
});

/** 打开行操作菜单(Radix DropdownMenu 监听 pointerdown) */
function openMenu() {
  const trigger = document.querySelector('button[aria-haspopup="menu"]');
  if (!trigger) throw new Error("未找到行操作菜单触发器");
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false });
}
