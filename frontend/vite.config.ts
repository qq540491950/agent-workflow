import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";
import tailwindcss from "@tailwindcss/vite";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [react(), wails("./bindings"), tailwindcss()],
  resolve: {
    alias: {
      // 显式声明 @bindings 别名:不依赖 @wailsio/runtime 插件的隐式解析
      // (beta 版插件在 Windows 上无法把 @bindings/*.js 解析到生成的 .ts 文件)
      "@bindings": import.meta.dirname + "/bindings",
      "@": import.meta.dirname + "/src",
    },
  },
});