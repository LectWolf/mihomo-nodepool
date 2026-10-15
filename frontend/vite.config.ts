import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

const backendOrigin = 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [vue()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        target: backendOrigin,
        changeOrigin: true,
      },
      '/healthz': {
        target: backendOrigin,
        changeOrigin: true,
      },
    },
  },
})
