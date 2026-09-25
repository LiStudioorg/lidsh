import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  base: './',
  // outDir 指向 internal/webui/dist，作为 Go //go:embed 的嵌入源（embed 仅限包目录内）。
  build: { outDir: '../internal/webui/dist', emptyOutDir: true },
  server: { port: 5173, proxy: { '/api': 'http://127.0.0.1:3000' } },
})
