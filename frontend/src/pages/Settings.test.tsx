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
