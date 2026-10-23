import { defineConfig } from 'vite'

export default defineConfig({
  // host 暴露到局域网：方便手机开页面测试麦克风与延迟手感
  server: {
    host: true,
    // /ws 代理到本地 golang（浏览器主通道：音频 + 游戏动作）
    proxy: {
      '/ws': { target: 'http://127.0.0.1:8080', ws: true },
    },
  },
})
