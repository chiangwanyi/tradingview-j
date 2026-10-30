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
  },
})
