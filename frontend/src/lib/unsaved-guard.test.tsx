// @vitest-environment jsdom
// 未保存守卫:dirty 时拦应用内跳转(confirm 确认后放行 / 取消留在原地),
// 不 dirty 时绝不拦(守卫不是路障)。
//
// useBlocker 只在 data router 下可用,测试用 createMemoryRouter(与生产
// createBrowserRouter 同一形态族)。
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { createMemoryRouter, Link, RouterProvider } from "react-router-dom";
import { useUnsavedGuard } from "./unsaved-guard";

function Page({ dirty }: { dirty: boolean }) {
  useUnsavedGuard(dirty, "有未保存的更改,确定离开?");
  return (
    <div>
      <Link to="/other">去别处</Link>
      <span>当前页</span>
    </div>
  );
}

function Other() {
  return <span>别的页</span>;
}

function makeRouter(dirty: boolean) {
  return createMemoryRouter([
    { path: "/", element: <Page dirty={dirty} /> },
    { path: "/other", element: <Other /> },
  ]);
}

afterEach(() => cleanup());

describe("useUnsavedGuard", () => {
  it("dirty 时跳转被拦:confirm 取消则留在原地", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
    const router = makeRouter(true);
    render(<RouterProvider router={router} />);
    expect(screen.getByText("当前页")).toBeTruthy();
    fireEvent.click(screen.getByText("去别处"));
    expect(confirmSpy).toHaveBeenCalledTimes(1);
    // confirm 返回 false → 留在原地
    expect(screen.getByText("当前页")).toBeTruthy();
    expect(screen.queryByText("别的页")).toBeNull();
    confirmSpy.mockRestore();
  });

  it("dirty 时 confirm 确认则放行跳转", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(true);
    const router = makeRouter(true);
    render(<RouterProvider router={router} />);
    fireEvent.click(screen.getByText("去别处"));
    expect(screen.getByText("别的页")).toBeTruthy();
    confirmSpy.mockRestore();
  });

  it("不 dirty 时直接放行(守卫不是恒拦的路障)", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
    const router = makeRouter(false);
    render(<RouterProvider router={router} />);
    fireEvent.click(screen.getByText("去别处"));
    expect(screen.getByText("别的页")).toBeTruthy();
    expect(confirmSpy).not.toHaveBeenCalled();
    confirmSpy.mockRestore();
  });
});
