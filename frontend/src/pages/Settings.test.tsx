// @vitest-environment jsdom
// Settings:恢复备份的计数语义、script 开关的风险确认、日志级别的前端校验。
//
// 后端第一轮把 agent_configs_invalid 的语义扩大了(结构非法 **或** 本机
// 不存在该 Agent),并新增 agent_configs_unknown_ids;前端文案必须跟上,
// 否则用户以为凭据已恢复,实际运行时根本没带上。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { toast } from "sonner";
import Settings from "./Settings";
import { settle } from "@/test/helpers";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

const m = vi.hoisted(() => ({
  getSettings: vi.fn(),
  updateSettings: vi.fn(),
  restoreBackup: vi.fn(),
  exportBackupText: vi.fn(),
  version: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      getSettings: m.getSettings,
      updateSettings: m.updateSettings,
      restoreBackup: m.restoreBackup,
      exportBackupText: m.exportBackupText,
      version: m.version,
    },
  };
});

const info = {
  data_dir: "/tmp/data",
  git_working_dir: "",
  log_level: "info",
  allow_script_nodes: false,
};

beforeEach(() => {
  vi.useFakeTimers();
  m.getSettings.mockReset().mockResolvedValue({ ...info });
  m.updateSettings.mockReset().mockResolvedValue({ ...info });
  m.restoreBackup.mockReset().mockResolvedValue({
    workflows_restored: 1,
    workflows_skipped: 0,
    workflows_invalid: 0,
    agent_configs_restored: 0,
    agent_configs_invalid: 0,
  });
  m.exportBackupText.mockReset().mockResolvedValue("{}");
  m.version.mockReset().mockResolvedValue({});
  vi.mocked(toast.error).mockClear();
  vi.mocked(toast.success).mockClear();
  vi.mocked(toast.warning).mockClear();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function renderSettings() {
  return render(<Settings />);
}

describe("Settings 失败必须可见", () => {
  // 此前 api.version() 是 `.catch(() => {})`:版本信息加载失败时静默不显示,
  // 用户以为"没有版本信息"而不是"加载失败"。
  it("版本信息加载失败时弹出错误提示", async () => {
    m.version.mockRejectedValue(new Error("后端不可用"));
    renderSettings();
    await settle();
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain("加载版本信息");
  });
});

describe("Settings 恢复备份提示", () => {
  it("拒绝计数写明【结构非法或本机无此 Agent】", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    m.restoreBackup.mockResolvedValue({
      workflows_restored: 1,
      workflows_skipped: 2,
      workflows_invalid: 0,
      agent_configs_restored: 1,
      agent_configs_invalid: 2,
      agent_configs_unknown_ids: ["ghost-agent"],
    });
    renderSettings();
    await settle();
    const file = new File([JSON.stringify({ workflows: [] })], "backup.json", {
      type: "application/json",
    });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file] } });
    await settle();
    const msg = vi.mocked(toast.success).mock.calls[vi.mocked(toast.success).mock.calls.length - 1]?.[0] as string;
    expect(msg).toContain("拒绝 2 个(结构非法或本机无此 Agent)");
    expect(msg).toContain("本机无此 Agent:ghost-agent");
    expect(msg).toContain("跳过已存在 2 个");
  });

  it("全部恢复成功时不显示拒绝计数", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    renderSettings();
    await settle();
    const file = new File([JSON.stringify({ workflows: [] })], "backup.json", {
      type: "application/json",
    });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file] } });
    await settle();
    const msg = vi.mocked(toast.success).mock.calls[vi.mocked(toast.success).mock.calls.length - 1]?.[0] as string;
    expect(msg).not.toContain("拒绝");
    expect(vi.mocked(toast.warning)).not.toHaveBeenCalled();
  });
});

describe("Settings script 开关风险提示", () => {
  it("开启时要求二次确认,拒绝则保持关闭且不提交", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
    renderSettings();
    await settle();
    const sw = screen.getAllByRole("switch")[0] as HTMLButtonElement;
    expect(sw.getAttribute("data-state")).toBe("unchecked");
    fireEvent.click(sw);
    await settle();
    expect(confirmSpy).toHaveBeenCalled();
    expect(sw.getAttribute("data-state")).toBe("unchecked");
    // 未确认 → 不允许保存成"已放行"
    fireEvent.click(screen.getByText("保存设置"));
    await settle();
    expect(m.updateSettings).toHaveBeenCalledWith(
      expect.objectContaining({ allow_script_nodes: false }),
    );
  });

  it("确认后开关才可打开", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    renderSettings();
    await settle();
    const sw = screen.getAllByRole("switch")[0] as HTMLButtonElement;
    fireEvent.click(sw);
    await settle();
    expect(sw.getAttribute("data-state")).toBe("checked");
  });
});

describe("Settings 日志级别前端校验", () => {
  it("非法日志级别在前端就被拦下(不发给后端)", async () => {
    // 存量配置里的非法值(下拉框选不出来,但后端可能返回)
    m.getSettings.mockResolvedValue({ ...info, log_level: "trace" });
    renderSettings();
    await settle();
    fireEvent.click(screen.getByText("保存设置"));
    await settle();
    expect(m.updateSettings).not.toHaveBeenCalled();
    expect(vi.mocked(toast.error).mock.calls[vi.mocked(toast.error).mock.calls.length - 1]?.[0]).toContain("日志级别非法");
  });

  it("合法级别正常提交", async () => {
    renderSettings();
    await settle();
    fireEvent.click(screen.getByText("保存设置"));
    await settle();
    expect(m.updateSettings).toHaveBeenCalledWith(
      expect.objectContaining({ log_level: "info" }),
    );
  });
});

// ---- 迭代 20:P1-1 —— 恢复备份"已落库但运行时未生效"必须上抛 ----
//
// 后端 RestoreBackup 已经产出 agent_configs_apply_failed_ids:
// 配置**已落库**,但 applyAgentConfig 失败 → 适配器实例仍是旧的
// (旧端点/旧凭据/旧参数)。
//
// 前端此前把这个字段整个丢掉:只报"恢复 Agent 配置 N 个",
// 用户据此认为凭据已恢复,实际运行时仍用旧凭据。
describe("Settings 恢复备份:运行时未生效的上抛", () => {
  it("apply_failed 的 Agent 逐个上抛(不得只报'已恢复 N 个')", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    m.restoreBackup.mockResolvedValue({
      workflows_restored: 0,
      workflows_skipped: 0,
      workflows_invalid: 0,
      agent_configs_restored: 1,
      agent_configs_invalid: 0,
      agent_configs_apply_failed_ids: ["claude-code"],
    });
    renderSettings();
    await settle();
    const file = new File([JSON.stringify({ workflows: [] })], "backup.json", {
      type: "application/json",
    });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file] } });
    await settle();
    const warn = vi
      .mocked(toast.warning)
      .mock.calls.map((c) => String(c[0]))
      .join(" | ");
    expect(warn).toContain("已落库但未生效");
    expect(warn).toContain("claude-code");
  });

  it("全部生效时不报'未生效'(不制造提示噪音)", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    renderSettings();
    await settle();
    const file = new File([JSON.stringify({ workflows: [] })], "backup.json", {
      type: "application/json",
    });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file] } });
    await settle();
    const warn = vi
      .mocked(toast.warning)
      .mock.calls.map((c) => String(c[0]))
      .join(" | ");
    expect(warn).not.toContain("已落库但未生效");
  });
});

// ---- 迭代 20:P2-3 —— 备份明文范围的口径必须与实际导出一致 ----
//
// 实际导出:agent_configs **整份原样**(env / behavior / extra_args)
// + 工作流定义本体(含工作流 variables)。UI 此前只写"环境变量以明文导出",
// 比实际窄 —— 用户按窄口径保管(约定 8:备份导出含明文是唯一例外)。
describe("Settings 备份明文范围文案", () => {
  it("写明三类明文来源(与 docs/api.md 一致)", async () => {
    renderSettings();
    await settle();
    const text = document.body.textContent ?? "";
    expect(text).toContain("明文范围有三类");
    expect(text).toContain("extra_args");
    expect(text).toContain("behavior");
    expect(text).toContain("variables");
  });

  it("不再使用旧的窄口径文案", async () => {
    renderSettings();
    await settle();
    expect(document.body.textContent ?? "").not.toContain("环境变量以明文导出");
  });
});

// 备份含三段(工作流 / Agent 配置 / 技能启用状态),恢复时按版本号校验
// 格式。文案必须与实际导出一致 —— 否则用户按旧口径理解:
//   - 以为"备份里没有技能状态"→ 换机器后技能开关全变回可用,毫无察觉;
//   - 以为"随便一个 JSON 都能恢复"→ 格式不认识的文件被拒时一头雾水。
describe("Settings 备份内容与版本校验文案", () => {
  it("写明备份含技能启用状态", async () => {
    renderSettings();
    await settle();
    const text = document.body.textContent ?? "";
    expect(text).toContain("技能启用状态");
    expect(text).toContain("skill_configs");
  });

  it("写明恢复时按版本号校验格式(不认识的会被拒)", async () => {
    renderSettings();
    await settle();
    const text = document.body.textContent ?? "";
    expect(text).toContain("版本号");
    expect(text).toContain("拒绝");
  });

  it("写明老备份仍能恢复(只是没有技能状态)", async () => {
    renderSettings();
    await settle();
    const text = document.body.textContent ?? "";
    expect(text).toContain("仍能恢复");
  });
});

// 设置行读失败(*_read_failed)必须**上屏**:输入框里显示的是内存态
// 生效值,不是库里的值 —— 用户不知道这一点就会直接保存,用当前值覆盖
// 损坏行,损坏事实消失(#287,与后端 git_working_dir_read_failed 同族)。
describe("Settings 设置行读失败警示", () => {
  it("log_level 行读失败时显示警示条,写明显示的不是库值", async () => {
    m.getSettings.mockResolvedValue({ ...info, log_level_read_failed: true });
    renderSettings();
    await settle();
    const alert = screen.getAllByTestId("settings-read-failed");
    expect(alert.length).toBe(1);
    expect(alert[0].textContent).toContain("日志级别");
    expect(alert[0].textContent).toContain("不是数据库里保存的值");
  });

  it("多个设置行同时读失败时逐行提示", async () => {
    m.getSettings.mockResolvedValue({
      ...info,
      log_level_read_failed: true,
      allow_script_nodes_read_failed: true,
      git_working_dir_read_failed: true,
    });
    renderSettings();
    await settle();
    const text = document.body.textContent ?? "";
    expect(text).toContain("日志级别");
    expect(text).toContain("script 节点放行开关");
    expect(text).toContain("Git 工作目录");
    expect(screen.getAllByTestId("settings-read-failed").length).toBe(3);
  });

  it("行正常时不显示警示条(误报同样是 bug)", async () => {
    renderSettings();
    await settle();
    expect(screen.queryByTestId("settings-read-failed")).toBeNull();
  });
});
