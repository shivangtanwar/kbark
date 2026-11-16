import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// The console is compiled into internal/server/dist and embedded in the
// kbark binary. `npm run dev` proxies the API to a running `kbark serve`.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: '../internal/server/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5173,
    proxy: { '/api': { target: 'http://127.0.0.1:8765', changeOrigin: false } },
  },
});
