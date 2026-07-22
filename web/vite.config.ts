import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  build: {
    outDir: '../internal/webembed/dist',
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    proxy: { '/api': 'http://127.0.0.1:2096', '/sub': 'http://127.0.0.1:2096' },
  },
})
