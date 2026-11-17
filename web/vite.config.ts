import path from 'node:path';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, 'src') } },
  server: {
    port: 6416,
    host: true,
    // `go run ./cmd/hometwin` in server/ serves the API on :6417
    proxy: { '/api': { target: 'http://127.0.0.1:6417', ws: true } },
  },
  // relative asset paths, so the build also works under a prefix (Home Assistant ingress)
  base: './',
  build: { chunkSizeWarningLimit: 1800 },
});
