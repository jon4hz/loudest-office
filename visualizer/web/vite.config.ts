import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// base './' keeps every asset URL relative, so the UI works behind a path
// prefix (HA ingress) as well as at /.
export default defineConfig({
  plugins: [svelte()],
  base: './',
  build: { outDir: 'dist', emptyOutDir: true },
  server: { proxy: { '/api': { target: 'http://localhost:8099', ws: true } } },
})
