// @vitest-environment jsdom
// "上次任务文本"的存储:不再明文留在 localStorage 里永不清理。
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  LAST_TASK_KEY_PREFIX,
  LAST_TASK_MAX_ENTRIES,
  LAST_TASK_MAX_LEN,
  loadLastTask,
  purgeLegacyLastTasks,
  saveLastTask,
} from "./task-storage";

beforeEach(() => {
  window.sessionStorage.clear();
  window.localStorage.clear();
  vi.useFakeTimers();
});

describe("任务文本存储", () => {
  it("保存后能读回来", () => {
    saveLastTask("wf-1", "部署到生产环境");
    expect(loadLastTask("wf-1")).toBe("部署到生产环境");
  });

  it("不往 localStorage 写(明文任务不落在磁盘上)", () => {
    saveLastTask("wf-1", "token=secret");
    expect(window.localStorage.getItem(`${LAST_TASK_KEY_PREFIX}wf-1`)).toBeNull();
    expect(window.sessionStorage.getItem(`${LAST_TASK_KEY_PREFIX}wf-1`)).not.toBeNull();
  });

  it("超长文本被截断(存储有上限)", () => {
    saveLastTask("wf-1", "x".repeat(LAST_TASK_MAX_LEN + 500));
    expect(loadLastTask("wf-1").length).toBe(LAST_TASK_MAX_LEN);
  });

  it("条数超限时淘汰最旧的一条(存储不会无限增长)", () => {
    for (let i = 0; i < LAST_TASK_MAX_ENTRIES + 3; i++) {
      vi.setSystemTime(1_000_000 + i * 1000);
      saveLastTask(`wf-${i}`, `任务 ${i}`);
    }
    const keys = Object.keys(window.sessionStorage).filter((k) =>
      k.startsWith(LAST_TASK_KEY_PREFIX),
    );
    expect(keys.length).toBe(LAST_TASK_MAX_ENTRIES);
    // 最旧的三个已被淘汰
    expect(loadLastTask("wf-0")).toBe("");
    expect(loadLastTask("wf-1")).toBe("");
    expect(loadLastTask("wf-2")).toBe("");
    // 最新的还在
    expect(loadLastTask(`wf-${LAST_TASK_MAX_ENTRIES + 2}`)).toBe(
      `任务 ${LAST_TASK_MAX_ENTRIES + 2}`,
    );
  });

  it("没有记录时返回空串", () => {
    expect(loadLastTask("wf-404")).toBe("");
  });

  it("升级后清掉旧版本留在 localStorage 里的明文", () => {
    window.localStorage.setItem(`${LAST_TASK_KEY_PREFIX}wf-1`, "token=secret");
    window.localStorage.setItem("unrelated", "keep-me");
    purgeLegacyLastTasks();
    expect(window.localStorage.getItem(`${LAST_TASK_KEY_PREFIX}wf-1`)).toBeNull();
    expect(window.localStorage.getItem("unrelated")).toBe("keep-me");
  });
});
