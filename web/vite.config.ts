import { defineConfig } from 'vite'

export default defineConfig({
  // host 暴露到局域网：方便手机开页面测试麦克风与延迟手感
  server: {
    host: true,
    // 固定 5197（README 口径与联调端口约定），不用每次手传 --port
    port: 5197,
    // /ws 与 /api 代理到本地 golang（音频主通道 + 排行榜/结算）
    proxy: {
      '/ws': { target: 'http://127.0.0.1:8080', ws: true },
      '/api': { target: 'http://127.0.0.1:8080' },
    },
  },
})
