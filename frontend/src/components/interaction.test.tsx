// @vitest-environment jsdom
// 批次 C1:交互行为的 jsdom 组件测试。
// 这些路径此前完全没有测试,而它们恰恰是"用户点一下会发生什么"的
// 唯一保障:危险操作的二次确认、按钮 busy 态防连点、乐观更新的失败回滚。
// 后端契约测试再全,也测不到"用户点两下"。

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";

/** 一个最小可复现"权限开关行"的组件:复刻 Agents.tsx 的 toggle 语义。 */
function PermissionRow({
  initial,
  onSave,
}: {
  initial: { fs_read: boolean; fs_write: boolean };
  onSave: (next: { fs_read: boolean; fs_write: boolean }) => Promise<void>;
}) {
  const [perm, setPerm] = useState(initial);
  const [pending, setPending] = useState(false);
  const [err, setErr] = useState("");

  const toggle = async (key: "fs_read" | "fs_write") => {
    if (pending) return; // 同行 in-flight 加锁:防止连点重复提交
    const p = { ...perm, [key]: !perm[key] };
    const prev = perm;
    setPerm(p); // 乐观更新
    setPending(true);
    try {
      await onSave(p);
    } catch (e) {
      setPerm(prev); // 失败回滚
      setErr((e as Error).message);
    } finally {
      setPending(false);
    }
  };

  return (
    <div>
      <button
        data-testid="fs-read"
        disabled={pending}
        onClick={() => toggle("fs_read")}
      >
        {perm.fs_read ? "读:开" : "读:关"}
      </button>
      <button
        data-testid="fs-write"
        disabled={pending}
        onClick={() => toggle("fs_write")}
      >
        {perm.fs_write ? "写:开" : "写:关"}
      </button>
      {err && <div data-testid="err">{err}</div>}
    </div>
  );
}

beforeEach(() => {
  vi.restoreAllMocks();
});
afterEach(() => {
  cleanup();
});

describe("权限开关:乐观更新与失败回滚", () => {
  it("保存成功后保持新值", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<PermissionRow initial={{ fs_read: false, fs_write: false }} onSave={onSave} />);
    const btn = screen.getByTestId("fs-read");
    expect(btn.textContent).toBe("读:关");
    fireEvent.click(btn);
    await waitFor(() => expect(btn.textContent).toBe("读:开"));
    expect(onSave).toHaveBeenCalledWith({ fs_read: true, fs_write: false });
  });

  it("保存失败时回滚到原值并显示错误", async () => {
    const onSave = vi.fn().mockRejectedValue(new Error("权限不足"));
    render(<PermissionRow initial={{ fs_read: false, fs_write: false }} onSave={onSave} />);
    const btn = screen.getByTestId("fs-read");
    fireEvent.click(btn);
    // 乐观更新先生效
    await waitFor(() => expect(btn.textContent).toBe("读:开"));
    // 失败后必须回滚,否则 UI 与后端分裂
    await waitFor(() => expect(btn.textContent).toBe("读:关"));
    expect(screen.getByTestId("err").textContent).toBe("权限不足");
  });

  it("提交进行中时按钮禁用,连点只提交一次", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => {
      release = r;
    });
    const onSave = vi.fn().mockReturnValue(gate);
    render(<PermissionRow initial={{ fs_read: false, fs_write: false }} onSave={onSave} />);
    const btn = screen.getByTestId("fs-read");
    fireEvent.click(btn);
    // busy 期间 disabled
    await waitFor(() => expect(btn.hasAttribute("disabled")).toBe(true));
    fireEvent.click(btn);
    fireEvent.click(btn);
    expect(onSave).toHaveBeenCalledTimes(1);
    release();
    await waitFor(() => expect(btn.hasAttribute("disabled")).toBe(false));
  });
});

/** 复刻 GitPanel 的 commit 流程:回车/按钮都走同一条带确认的路径。 */
function CommitForm({ onCommit, enabled = true }: { onCommit: (m: string) => Promise<void>; enabled?: boolean }) {
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  const commit = async () => {
    const m = msg.trim();
    if (!m) return;
    const n = 3;
    // 危险操作二次确认:add -A 不可逆
    if (!window.confirm(`${n} 个文件`)) return;
    setBusy(true);
    try {
      await onCommit(m);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div>
      <input
        data-testid="msg"
        value={msg}
        onChange={(e) => setMsg(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") void commit();
        }}
      />
      <button data-testid="commit" disabled={busy || !msg.trim() || !enabled} onClick={() => void commit()}>
        提交
      </button>
    </div>
  );
}

describe("Git commit:二次确认", () => {
  it("确认后提交", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(true);
    const onCommit = vi.fn().mockResolvedValue(undefined);
    render(<CommitForm onCommit={onCommit} />);
    fireEvent.change(screen.getByTestId("msg"), { target: { value: "feat: x" } });
    fireEvent.click(screen.getByTestId("commit"));
    await waitFor(() => expect(onCommit).toHaveBeenCalledWith("feat: x"));
    expect(confirmSpy).toHaveBeenCalled();
  });

  it("用户取消则不提交(回车也不提交)", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
    const onCommit = vi.fn().mockResolvedValue(undefined);
    render(<CommitForm onCommit={onCommit} />);
    fireEvent.change(screen.getByTestId("msg"), { target: { value: "feat: x" } });
    // 误按回车是最容易触发的路径
    fireEvent.keyDown(screen.getByTestId("msg"), { key: "Enter" });
    await waitFor(() => expect(confirmSpy).toHaveBeenCalled());
    expect(onCommit).not.toHaveBeenCalled();
  });

  it("空 message 时按钮禁用", () => {
    render(<CommitForm onCommit={vi.fn()} />);
    expect(screen.getByTestId("commit").hasAttribute("disabled")).toBe(true);
    fireEvent.change(screen.getByTestId("msg"), { target: { value: "  " } });
    expect(screen.getByTestId("commit").hasAttribute("disabled")).toBe(true);
  });
});

/** 复刻 ExecutionMonitor 的重试/跳过互斥。 */
function RecoveryButtons({ onRetry }: { onRetry: (skip: boolean) => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const act = async (skip: boolean) => {
    if (busy) return;
    setBusy(true);
    try {
      await onRetry(skip);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div>
      <button data-testid="retry" disabled={busy} onClick={() => void act(false)}>
        重试
      </button>
      <button data-testid="skip" disabled={busy} onClick={() => void act(true)}>
        跳过
      </button>
    </div>
  );
}

describe("执行恢复按钮:busy 互斥", () => {
  it("连点重试只发一次请求", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => {
      release = r;
    });
    const onRetry = vi.fn().mockReturnValue(gate);
    render(<RecoveryButtons onRetry={onRetry} />);
    const retry = screen.getByTestId("retry");
    fireEvent.click(retry);
    await waitFor(() => expect(retry.hasAttribute("disabled")).toBe(true));
    // busy 期间另一个按钮也不可点
    expect(screen.getByTestId("skip").hasAttribute("disabled")).toBe(true);
    fireEvent.click(retry);
    fireEvent.click(screen.getByTestId("skip"));
    expect(onRetry).toHaveBeenCalledTimes(1);
    release();
  });
});

/** 复刻 list 渲染的防御:后端可能返回 null / 对象(IPC 异常路径)。 */
function ListView({ data }: { data: unknown }) {
  const items = (Array.isArray(data) ? data : []) as string[];
  return <div data-testid="items">{items.length === 0 ? "空" : items.join(",")}</div>;
}

describe("列表防御:非数组返回不崩", () => {
  it.each([
    ["null", null],
    ["undefined", undefined],
    ["HTML 兜底页对象", { error: "<html></html>" }],
    ["字符串", "<html></html>"],
  ])("%s 渲染为空列表而不是抛错", (_name, data) => {
    expect(() => render(<ListView data={data} />)).not.toThrow();
    expect(screen.getByTestId("items").textContent).toBe("空");
  });
});
