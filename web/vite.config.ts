import { defineConfig } from 'vite'

export default defineConfig({
  // host 暴露到局域网：方便手机开页面测试麦克风与延迟手感
  server: { host: true },
})
