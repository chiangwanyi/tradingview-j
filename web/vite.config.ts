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
     * 开发期把 /api 转发给抓取工具（Go）自带的网页版分析服务。
     * 它默认监听 127.0.0.1:8765，端口被占时会自动换端口并在终端说明
     * —— 那边换了端口，这里要同步改。
     */
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8765',
        changeOrigin: true,
      },
    },
  },
})
