import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue(), tailwindcss()],

  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },

  server: {
    // 显式绑 IPv4 回环：默认的 localhost 在本机会解析到 IPv6 ::1，
    // 从 127.0.0.1 访问会连不上。这里和仓库「只绑本地」的口径保持一致。
    host: '127.0.0.1',
    port: 5173,
    /**
     * 开发期的两个后端都在根目录那个 Go 程序里：
     *   - /api -> 菜单 3 的网页版分析（127.0.0.1:8765）
     *   - /ws  -> 菜单 4 的复盘服务（127.0.0.1:8790，HTTP 与 WebSocket 同一个端口）
     * 那边端口被占会自动换端口并在终端说明 —— 换了要同步改这里。
     *
     * /ws 必须带 ws: true，否则 Vite 会把它当成普通 HTTP 请求转发，
     * 浏览器收到的不是 101 而是 200，握手直接失败。
     */
    proxy: {
      // 录像里的音频（/api/recordings/<id>/audio/*.wav）挂在复盘服务上，
      // 必须排在 '/api' 前面 —— 否则会被下面那条转发到菜单 2 的分析服务（8765）。
      '/api/recordings': {
        target: 'http://127.0.0.1:8790',
        changeOrigin: true,
      },
      '/api': {
        target: 'http://127.0.0.1:8765',
        changeOrigin: true,
      },
      '/ws': {
        target: 'http://127.0.0.1:8790',
        ws: true,
      },
    },
  },
})
