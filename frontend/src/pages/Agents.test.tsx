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
  });
  m.updateAgentConfig.mockReset().mockResolvedValue({
    model: "",
    base_url: "",
    timeout_seconds: 0,
    extra_args: [],
    env: { TOKEN: "***" },
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

// ---- 迭代 13:A —— 删掉 Agent 级"默认工作目录"死设置 ----
//
// default_working_dir 是**死设置**:运行期从不读取(claude.Config / pi.Config
// 都没有该字段),后端会明确拒绝任何新值。此前 UI 却有一个输入框:用户填了、
// 界面提示"已保存",Agent 仍在全局 Git 目录下读写文件(约定 14)。
//
// 现在:输入框删除,并留一行说明指向真正生效的地方(节点级 config.working_dir);
// 前端也不再下发这个字段。
describe("Agents 死设置:默认工作目录", () => {
  it("配置对话框里不再有默认工作目录输入框", async () => {
    await openConfigDialog();
    expect(screen.queryByText("默认工作目录(可选)")).toBeNull();
  });

  it("留一行说明,指向节点级 config.working_dir", async () => {
    await openConfigDialog();
    expect(screen.getByText(/config\.working_dir/)).toBeTruthy();
  });

  it("保存时不下发 default_working_dir(后端会拒绝新值)", async () => {
    await openConfigDialog();
    fireEvent.click(screen.getByText("保存"));
    await settle();
    const payload = m.updateAgentConfig.mock.calls[m.updateAgentConfig.mock.calls.length - 1]?.[1] as
      | Record<string, unknown>
      | undefined;
    expect(payload).toBeTruthy();
    expect("default_working_dir" in (payload as Record<string, unknown>)).toBe(false);
  });

  it("存量 default_working_dir 原样回传(不得被前端悄悄清掉)", async () => {
    // 后端对存量值的规则:原样回传放行、清空也放行、新值一律拒绝。
    // 前端删掉输入框后**不得**顺手把存量值抹掉(那是静默改用户配置)。
    m.getAgentConfig.mockResolvedValue({
      model: "",
      base_url: "",
      timeout_seconds: 0,
      extra_args: [],
      env: {},
      default_working_dir: "/legacy/dir",
    });
    await openConfigDialog();
    fireEvent.click(screen.getByText("保存"));
    await settle();
    const payload = m.updateAgentConfig.mock.calls[m.updateAgentConfig.mock.calls.length - 1]?.[1] as
      | Record<string, unknown>
      | undefined;
    expect((payload as Record<string, unknown>).default_working_dir).toBe("/legacy/dir");
  });
});
