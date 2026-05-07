import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// NOTE: this project lives on an exFAT USB drive (P:\). Native file-system
// watchers fire spurious change events on exFAT, which puts vite into an
// infinite "page reload" loop and the user's browser shows
// "사이트에 연결할 수 없음" with hundreds of canceled requests.
//
// Fix: use polling. Mtimes are reliable on exFAT, so polling at ~1s gives a
// snappy dev loop without ghost events.
export default defineConfig({
  plugins: [svelte()],
  server: {
    host: '0.0.0.0',
    port: 34115,
    strictPort: true,
    watch: {
      usePolling: true,
      interval: 1000,
      ignored: [
        '**/node_modules/**',
        '**/.git/**',
        '**/dist/**',
        '**/tsconfig*.json'
      ]
    }
  },
  build: { target: 'es2022', outDir: 'dist', emptyOutDir: true }
});
