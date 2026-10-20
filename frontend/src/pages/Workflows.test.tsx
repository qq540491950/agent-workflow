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
  importYAML: vi.fn(),
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
      importYAML: m.importYAML,
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
  m.importYAML.mockReset().mockResolvedValue({ ...wf, id: "wf-2", name: "导入的流程" });
  vi.mocked(toast.error).mockClear();
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.warning).mockClear();
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

// 导入响应里的 ignored_keys:DSL 解析器把闭合结构里的未知键**丢弃**
// (那些设置不会生效)。后端把清单放在响应里,前端必须把它显示出来 ——
// "导入成功"不等于"每个设置都生效"(约定 #14:静默丢弃同族问题)。
describe("Workflows 导入 YAML:被忽略的键必须可见", () => {
  async function doImport() {
    renderWorkflows();
    await settle();
    fireEvent.click(screen.getByText("导入 YAML"));
    await settle();
    fireEvent.change(screen.getByPlaceholderText(/version/), {
      target: { value: 'version: "1"\nworkflow:\n  id: wf-2\n' },
    });
    fireEvent.click(screen.getByText("导入"));
    await settle();
  }

  it("带 ignored_keys 时提示 N 个键被忽略并列出键名", async () => {
    m.importYAML.mockResolvedValue({
      ...wf,
      id: "wf-2",
      name: "导入的流程",
      ignored_keys: ["workflow.settings", "settings.max_iteration"],
    });
    await doImport();
    expect(toast.success).toHaveBeenCalled();
    const warned = vi.mocked(toast.warning).mock.calls[0]?.[0] ?? "";
    expect(warned).toContain("2 个键被忽略");
    expect(warned).toContain("workflow.settings");
    expect(warned).toContain("settings.max_iteration");
  });

  it("桌面模式没有该字段时不得崩(?? [] 兜底)", async () => {
    // 桌面模式走 Wails 绑定,响应里没有 ignored_keys。
    m.importYAML.mockResolvedValue({ ...wf, id: "wf-2", name: "导入的流程" });
    await doImport();
    expect(toast.success).toHaveBeenCalled();
    expect(toast.warning).not.toHaveBeenCalled();
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("导入失败时提示错误", async () => {
    m.importYAML.mockRejectedValue(new Error("DSL 含未知顶层键"));
    await doImport();
    expect(toast.error).toHaveBeenCalledWith("DSL 含未知顶层键");
  });
});
