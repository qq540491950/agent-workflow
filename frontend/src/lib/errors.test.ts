// 统一错误提示出口(lib/errors)的单元测试:文案口径 + 去重窗口。
//
// 这个模块是"请求失败不再被静默吞掉"的落点:此前各页面用
// `.catch(() => {})` 把失败吞成空值,用户以为"没数据"实为"加载失败"。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import {
  ERROR_DEDUP_WINDOW_MS,
  describeError,
  reportError,
  resetErrorDedup,
} from "./errors";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), message: vi.fn() },
}));

beforeEach(() => {
  vi.useFakeTimers();
  resetErrorDedup();
  vi.mocked(toast.error).mockClear();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("describeError", () => {
  it("普通 Error 取 message", () => {
    expect(describeError(new Error("权限不足"))).toBe("权限不足");
  });

  it("字符串错误原样返回", () => {
    expect(describeError("后端不可用")).toBe("后端不可用");
  });

  it("带错误码时补上码(与 describeWarnings 同口径)", () => {
    // ApiError 的形状(message/code/status),不依赖真的实例化 ApiError ——
    // errors.ts 刻意不 import @/lib/api(避免与 api.ts 形成循环依赖)。
    expect(describeError({ message: "权限不足", code: "PERMISSION_DENIED", status: 403 })).toBe(
      "权限不足(PERMISSION_DENIED)",
    );
  });

  it("没有文案时退回状态码", () => {
    expect(describeError({ message: "", status: 500 })).toBe("HTTP 500");
  });

  it("完全认不出来的错误也给出人话(提示路径本身不能崩)", () => {
    expect(describeError(undefined)).toBe("未知错误");
    expect(describeError(null)).toBe("未知错误");
    expect(describeError({})).toBe("未知错误");
  });

  it("fetch 的英文网络错误翻成人话", () => {
    expect(describeError(new TypeError("Failed to fetch"))).toBe(
      "无法连接后端服务(后端未启动或地址不可达)",
    );
  });

  it("后端的具体错误不被泛化提示盖掉(带 timeout 的后端文案保持原样)", () => {
    // 子串匹配会把"Claude 执行超时"这种有信息量的话替换成"请求超时"——
    // 泛化提示只该用于"根本连不上"这一种情况。
    expect(describeError(new Error("Claude 执行超时"))).toBe("Claude 执行超时");
    expect(describeError(new Error("request timeout"))).toBe("请求超时");
  });

  // 迭代 30(P1-3):这两条此前会**原样**弹给用户 ——
  // "AbortError: The operation was aborted" / "timeout of 5000ms exceeded"
  // 是英文,用户看不懂,也不知道该不该重试。
  it("请求被中断(AbortError)翻成人话", () => {
    expect(describeError(new DOMException("The operation was aborted", "AbortError"))).toBe(
      "请求被中断(可能已取消或页面切换)",
    );
    expect(describeError(new Error("AbortError: The operation was aborted"))).toBe(
      "请求被中断(可能已取消或页面切换)",
    );
  });

  it("超时文案(timeout of Nms exceeded)翻成人话", () => {
    expect(describeError(new Error("timeout of 5000ms exceeded"))).toBe("请求超时");
    expect(describeError(new Error("request timeout of 30000ms exceeded"))).toBe("请求超时");
  });
});

describe("reportError", () => {
  it("带 scope 时拼成「哪里出的错:什么错」", () => {
    reportError(new Error("权限不足"), "加载工作流列表");
    expect(toast.error).toHaveBeenCalledWith("加载工作流列表:权限不足");
  });

  it("不带 scope 时只弹错误本身", () => {
    reportError(new Error("权限不足"));
    expect(toast.error).toHaveBeenCalledWith("权限不足");
  });

  it("同一个错误在去重窗口内只提示一次(防提示风暴)", () => {
    reportError(new Error("后端不可用"), "加载工作流列表");
    reportError(new Error("后端不可用"), "加载执行记录");
    reportError(new Error("后端不可用"), "加载统计");
    expect(toast.error).toHaveBeenCalledTimes(1);
  });

  it("超过去重窗口后同一个错误可以再次提示", () => {
    reportError(new Error("后端不可用"), "加载工作流列表");
    vi.advanceTimersByTime(ERROR_DEDUP_WINDOW_MS + 1);
    reportError(new Error("后端不可用"), "加载工作流列表");
    expect(toast.error).toHaveBeenCalledTimes(2);
  });

  it("不同的错误各提示一次", () => {
    reportError(new Error("后端不可用"), "加载工作流列表");
    reportError(new Error("权限不足"), "加载执行记录");
    expect(toast.error).toHaveBeenCalledTimes(2);
  });
});

// ---- 迭代 35:WORKFLOW_INVALID 的 JSON 数组 message 必须拍平成人话 -------
//
// 应用层把 validator.Result.Errors 整个 marshal 进错误 message;直接显示
// 是一坨机器可读 JSON(走查实测:导入缺 name 的 YAML,toast 弹出
// "导入工作流:[{"code":"MISSING_NAME",...}](WORKFLOW_INVALID)")。
describe("flattenValidationErrors / describeError:WORKFLOW_INVALID", () => {
  it("错误数组拍平为分号连接的人话,失败节点带定位", () => {
    const err = {
      code: "WORKFLOW_INVALID",
      message: JSON.stringify([
        { code: "MISSING_NAME", node: "", message: "workflow.name 不能为空" },
        { code: "EMPTY_WORKFLOW", node: "", message: "Workflow 至少需要一个节点" },
      ]),
    };
    expect(describeError(err)).toBe(
      "workflow.name 不能为空; Workflow 至少需要一个节点(WORKFLOW_INVALID)",
    );
  });

  it("带 node 的错误给出节点定位", () => {
    const err = {
      code: "WORKFLOW_INVALID",
      message: JSON.stringify([{ code: "X", node: "review", message: "表达式非法" }]),
    };
    expect(describeError(err)).toBe("[review] 表达式非法(WORKFLOW_INVALID)");
  });

  it("message 不是 JSON 数组时按原文走通用路径(不硬造)", () => {
    const err = { code: "WORKFLOW_INVALID", message: "普通文本" };
    expect(describeError(err)).toBe("普通文本(WORKFLOW_INVALID)");
  });

  it("数组里混进非对象元素 → 返回原文(拍平失败不吞错误内容)", () => {
    const raw = JSON.stringify([{ message: "ok" }, 42]);
    const err = { code: "WORKFLOW_INVALID", message: raw };
    expect(describeError(err)).toBe(`${raw}(WORKFLOW_INVALID)`);
  });

  it("空数组视为拍平失败(显示原文而不是空白 toast)", () => {
    const err = { code: "WORKFLOW_INVALID", message: "[]" };
    expect(describeError(err)).toBe("[](WORKFLOW_INVALID)");
  });
});
