// @vitest-environment jsdom
// Agents:"+ 添加环境变量" 插入的是空键名行 —— 保存前必须过滤,否则后端
// 存下一条空名环境变量(第二个空名行还会覆盖第一个)。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { toast } from "sonner";
import Agents from "./Agents";
import { settle } from "@/test/helpers";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

const m = vi.hoisted(() => ({
  listAgents: vi.fn(),
  getAgentConfig: vi.fn(),
  updateAgentConfig: vi.fn(),
  resetAgentConfig: vi.fn(),
  testAgent: vi.fn(),
  setAgentPermission: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      listAgents: m.listAgents,
      getAgentConfig: m.getAgentConfig,
      updateAgentConfig: m.updateAgentConfig,
      resetAgentConfig: m.resetAgentConfig,
      testAgent: m.testAgent,
      setAgentPermission: m.setAgentPermission,
    },
  };
});

const agent = {
  id: "mock-claude",
  name: "Mock Claude",
  model: "",
  permissions: {
    filesystem_read: true,
    filesystem_write: false,
    git_read: true,
    git_commit: false,
    git_push: false,
  },
};

beforeEach(() => {
  vi.useFakeTimers();
  m.listAgents.mockReset().mockResolvedValue([agent]);
  m.getAgentConfig.mockReset().mockResolvedValue({
    model: "",
    base_url: "",
    timeout_seconds: 0,
    extra_args: [],
    env: { TOKEN: "***" },
    default_working_dir: "",
  });
  m.updateAgentConfig.mockReset().mockResolvedValue({
    model: "",
    base_url: "",
    timeout_seconds: 0,
    extra_args: [],
    env: { TOKEN: "***" },
    default_working_dir: "",
  });
  vi.mocked(toast.error).mockClear();
  vi.mocked(toast.warning).mockClear();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

async function openConfigDialog() {
  render(<Agents />);
  await settle();
  fireEvent.click(screen.getByText("配置"));
  await settle();
}

describe("Agents 环境变量空键", () => {
  it("新增行未填名称时不再允许再插一行(同名空键会互相覆盖)", async () => {
    await openConfigDialog();
    const addBtn = screen.getByText("+ 添加环境变量") as HTMLButtonElement;
    expect(addBtn.disabled).toBe(false);
    fireEvent.click(addBtn);
    await settle();
    expect((screen.getByText("+ 添加环境变量") as HTMLButtonElement).disabled).toBe(true);
  });

  it("保存时过滤掉未命名行并如实告知", async () => {
    await openConfigDialog();
    fireEvent.click(screen.getByText("+ 添加环境变量"));
    await settle();
    fireEvent.click(screen.getByText("保存"));
    await settle();
    const payload = m.updateAgentConfig.mock.calls[m.updateAgentConfig.mock.calls.length - 1]?.[1] as {
      env: Record<string, string>;
    };
    expect(Object.keys(payload.env)).toEqual(["TOKEN"]);
    expect(vi.mocked(toast.warning).mock.calls[vi.mocked(toast.warning).mock.calls.length - 1]?.[0]).toContain(
      "已忽略 1 个未填写名称的环境变量",
    );
  });

  it("没有空键时原样提交(掩码值 *** 不被丢掉)", async () => {
    await openConfigDialog();
    fireEvent.click(screen.getByText("保存"));
    await settle();
    const payload = m.updateAgentConfig.mock.calls[m.updateAgentConfig.mock.calls.length - 1]?.[1] as {
      env: Record<string, string>;
    };
    expect(payload.env).toEqual({ TOKEN: "***" });
    expect(vi.mocked(toast.warning)).not.toHaveBeenCalled();
  });
});
