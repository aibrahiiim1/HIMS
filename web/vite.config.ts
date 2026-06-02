import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Dev server proxies /api → the hims-api Go server on :8090.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5180,
    proxy: {
      '/api': { target: 'http://localhost:8090', changeOrigin: true },
    },
  },
})
