// @vitest-environment jsdom
// 顶层错误边界:渲染异常必须有降级界面,不能整窗白屏(桌面应用里
// 白屏 = 应用坏了且无任何线索)。
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ErrorBoundary } from "./error-boundary";

function Boom(): never {
  throw new Error("夹具爆炸");
}

afterEach(() => { cleanup(); });

describe("ErrorBoundary", () => {
  it("子组件渲染异常时显示降级界面与错误信息(不白屏)", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    render(
      <ErrorBoundary>
        <Boom />
      </ErrorBoundary>,
    );
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.getByText("页面出错了")).toBeTruthy();
    expect(screen.getByText(/夹具爆炸/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "重试" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "刷新应用" })).toBeTruthy();
    spy.mockRestore();
  });

  it("没有异常时正常渲染子组件(兜底不是恒真路障)", () => {
    render(
      <ErrorBoundary>
        <div>正常内容</div>
      </ErrorBoundary>,
    );
    expect(screen.getByText("正常内容")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("重试按钮清掉错误状态重新渲染(第二次不再抛)", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    let throwNow = true;
    function Sometimes(): string {
      if (throwNow) throw new Error("第一次抛");
      return "恢复后的内容";
    }
    render(
      <ErrorBoundary>
        <Sometimes />
      </ErrorBoundary>,
    );
    expect(screen.getByText("页面出错了")).toBeTruthy();
    throwNow = false;
    // fireEvent 会包 act:裸 .click() 的 setState 不保证同步提交
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(screen.getByText("恢复后的内容")).toBeTruthy();
    spy.mockRestore();
  });
});
