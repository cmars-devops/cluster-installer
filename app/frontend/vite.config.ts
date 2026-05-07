import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  server: { port: 34115, strictPort: true },
  build: { target: 'es2022', outDir: 'dist', emptyOutDir: true }
});
