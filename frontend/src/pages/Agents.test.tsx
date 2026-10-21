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

// ---- 迭代 20:P1-2 —— 配置读取失败不得导致"静默清空全部环境变量" ----
//
// 此前 openConfig 的 catch 分支把配置置成一份**空对象**
// (env: {})并照常打开弹窗 —— "保存"按钮可点,用户一点,
// 后端收到 env:{} 就按"整表替换"把库里的凭据全删了,
// 而用户只看到"配置已保存"(约定 14)。
//
// 现在:读取失败 → 不给出可保存的配置(按钮禁用)+ 弹窗里写明原因。
describe("Agents 配置读取失败", () => {
  it("读取失败时保存被禁用,不下发任何配置", async () => {
    m.getAgentConfig.mockRejectedValue(new Error("后端不可用"));
    await openConfigDialog();
    const saveBtn = screen.getByText("保存") as HTMLButtonElement;
    expect(saveBtn.disabled).toBe(true);
    fireEvent.click(saveBtn);
    await settle();
    expect(m.updateAgentConfig).not.toHaveBeenCalled();
  });

  it("读取失败时弹窗里可见原因(不是只弹一个 toast)", async () => {
    m.getAgentConfig.mockRejectedValue(new Error("后端不可用"));
    await openConfigDialog();
    expect(screen.getByText(/读取配置失败/)).toBeTruthy();
    expect(screen.getByText(/后端不可用/)).toBeTruthy();
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain("后端不可用");
  });

  it("读取失败时不得回传空 env(那等于清空全部凭据)", async () => {
    m.getAgentConfig.mockRejectedValue(new Error("后端不可用"));
    await openConfigDialog();
    // 即使绕过禁用状态直接调用保存路径,也没有配置可提交
    fireEvent.click(screen.getByText("保存"));
    await settle();
    expect(m.updateAgentConfig).not.toHaveBeenCalled();
  });
});

// ---- 迭代 20:P2-1 —— 附加 CLI 参数按 shell 式分词 ----
//
// 此前按空格硬切:`--header "Authorization: Bearer ghp_x"` 被切成 3 段,
// CLI 收到的参数与用户写的不是一回事。
describe("Agents 附加 CLI 参数分词", () => {
  it("带引号的值回显时带引号,保存后仍是**一个**参数", async () => {
    m.getAgentConfig.mockResolvedValue({
      model: "",
      base_url: "",
      timeout_seconds: 0,
      extra_args: ["--header", "Authorization: Bearer ghp_x"],
      env: {},
    });
    await openConfigDialog();
    const input = screen.getByPlaceholderText(/--header/) as HTMLInputElement;
    // 回显必须带引号:否则用户改一个字再保存,参数就被切碎了
    expect(input.value).toBe('--header "Authorization: Bearer ghp_x"');
    fireEvent.change(input, { target: { value: '--header "Authorization: Bearer NEW"' } });
    await settle();
    fireEvent.click(screen.getByText("保存"));
    await settle();
    const payload = m.updateAgentConfig.mock.calls[m.updateAgentConfig.mock.calls.length - 1]?.[1] as {
      extra_args: string[];
    };
    expect(payload.extra_args).toEqual(["--header", "Authorization: Bearer NEW"]);
  });

  it("不带空格的参数不受影响", async () => {
    m.getAgentConfig.mockResolvedValue({
      model: "",
      base_url: "",
      timeout_seconds: 0,
      extra_args: ["--verbose"],
      env: {},
    });
    await openConfigDialog();
    const input = screen.getByPlaceholderText(/--header/) as HTMLInputElement;
    expect(input.value).toBe("--verbose");
    fireEvent.change(input, { target: { value: "--verbose --api-key sk-x" } });
    await settle();
    fireEvent.click(screen.getByText("保存"));
    await settle();
    const payload = m.updateAgentConfig.mock.calls[m.updateAgentConfig.mock.calls.length - 1]?.[1] as {
      extra_args: string[];
    };
    expect(payload.extra_args).toEqual(["--verbose", "--api-key", "sk-x"]);
  });
});
