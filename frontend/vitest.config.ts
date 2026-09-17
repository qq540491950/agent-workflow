import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
  },
  resolve: {
    alias: {
      // 与 vite.config.ts 保持一致:测试中被 mock 的 @bindings 无需真实文件,
      // 但显式别名让未 mock 的模块也能解析。
      "@bindings": new URL("./bindings", import.meta.url).pathname,
      "@": new URL("./src", import.meta.url).pathname,
    },
  },
});
