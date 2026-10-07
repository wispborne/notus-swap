import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig, loadEnv } from 'vite'

// The Go binary embeds dist/ and serves it under /notus/.
// In development, `npm run dev` proxies the API to a local notus-swap
// (go run ./cmd/notus-swap -listen 127.0.0.1:18080 ...). Set NOTUS_DEV_API to
// use another notus-swap instead, such as https://llm.example.com.
// It can also go in web/.env.local, which git ignores.
export default defineConfig(({ mode }) => {
  const env = { ...loadEnv(mode, import.meta.dirname, 'NOTUS_'), ...process.env }
  const target = { target: env.NOTUS_DEV_API ?? 'http://127.0.0.1:18080', changeOrigin: true }
  return {
    base: '/notus/',
    plugins: [svelte(), tailwindcss()],
    server: {
      // /logs is llama-swap's log stream, which notus-swap passes through.
      proxy: { '/notus/api': target, '/logs': target },
    },
  }
})
