import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  base: process.env.VITE_BASE_PATH || '/',
  plugins: [vue()],
  build: {
    // 兼容轻量浏览器（如 Via）：关闭代码分割，避免动态 import 的 ES Module chunk 在部分 WebView 里无法执行
    target: 'es2015',
    rollupOptions: {
      output: {
        // 只打一个 JS 文件，没有懒加载 chunk，Via 等浏览器也能跑
        manualChunks: undefined,
        inlineDynamicImports: true
      }
    }
  },
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8910',
        changeOrigin: true
      },
      '/uploads': {
        target: 'http://localhost:8910',
        changeOrigin: true
      }
    }
  }
})
