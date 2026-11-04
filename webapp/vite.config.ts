import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],

  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },

  server: {
    // 和 web/ 一样显式绑 IPv4 回环：localhost 在本机会解析到 ::1，从 127.0.0.1 连不上。
    // 端口错开 web/ 的 5173。
    host: '127.0.0.1',
    port: 5174,
    // 后端 Spring Boot 在 8791。同样写 127.0.0.1 而不是 localhost。
    proxy: {
      '/api': { target: 'http://127.0.0.1:8791', changeOrigin: true },
      // 交易页的 WebSocket（后端 `ws/WebSocketConfig` 挂在 /ws）。
      // `ws: true` 必须写：不写的话代理只转普通 HTTP，握手会 404，现象是「一进交易页就断线重连」。
      '/ws': { target: 'ws://127.0.0.1:8791', ws: true, changeOrigin: true },
    },
  },
})
