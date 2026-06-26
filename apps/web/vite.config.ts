import { defineConfig, type ProxyOptions } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

const devAPIURL = process.env.PARSAR_DEV_API_URL ?? process.env.VITE_PARSAR_API_URL ?? 'http://127.0.0.1:18080'

const devProxy: ProxyOptions = {
  target: devAPIURL,
  changeOrigin: true,
  // When the upstream Go server is down, reply 503 with a JSON body so the
  // frontend recognises it as "server unreachable" instead of vite's default
  // ECONNREFUSED 404.
  configure(proxy) {
    proxy.on('error', (err, _req, res) => {
      // Some node/vite versions surface res as a Socket; guard.
      const sockRes = res as unknown as {
        headersSent?: boolean
        writeHead?: (status: number, headers: Record<string, string>) => void
        end?: (body?: string) => void
      }
      if (sockRes.headersSent || !sockRes.writeHead) return
      sockRes.writeHead(503, { 'Content-Type': 'application/json' })
      sockRes.end?.(JSON.stringify({
        error: 'server_unreachable',
        detail: err.message,
      }))
    })
  },
}

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      '/dev': devProxy,
      '/api': devProxy,
    },
  },
})
