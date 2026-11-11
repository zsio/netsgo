import path from "path"
import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

function parseDevAllowedHosts(raw: string | undefined): true | string[] | undefined {
  const trimmed = raw?.trim()
  if (!trimmed) {
    return undefined
  }
  if (trimmed === 'true') {
    return true
  }
  const hosts = trimmed
    .split(',')
    .map((host) => host.trim())
    .filter(Boolean)
  return hosts.length > 0 ? hosts : undefined
}

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, __dirname, '')
  const backendTarget = (env.VITE_DEV_PROXY_TARGET || 'http://127.0.0.1:9527').replace(/\/$/, '')
  const wsTarget = backendTarget.replace(/^http/, 'ws')
  const proxyChangeOrigin = env.VITE_DEV_PROXY_CHANGE_ORIGIN !== 'false'
  const allowedHosts = parseDevAllowedHosts(env.VITE_DEV_ALLOWED_HOSTS)

  return {
    plugins: [
      react({
        babel: {
          plugins: [['babel-plugin-react-compiler']],
        },
      }),
      tailwindcss(),
    ],
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    build: {
      rollupOptions: {
        output: {
          manualChunks(id) {
            if (id.includes('commonjsHelpers')) {
              return 'vendor-react'
            }
            if (!id.includes('node_modules')) {
              return undefined
            }
            if (
              /\/node_modules\/(react|react-dom|scheduler|clsx|tailwind-merge|class-variance-authority)\//.test(id)
            ) {
              return 'vendor-react'
            }
            if (
              id.includes('/@codemirror/') ||
              id.includes('/codemirror/') ||
              id.includes('/@lezer/')
            ) {
              return 'vendor-codemirror'
            }
            if (id.includes('/recharts/') || id.includes('/victory-vendor/')) {
              return 'vendor-charts'
            }
            if (/\/node_modules\/d3-[^/]+\//.test(id)) {
              return 'vendor-d3'
            }
            if (
              id.includes('/@tanstack/') ||
              id.includes('/zustand/')
            ) {
              return 'vendor-router-query'
            }
            if (
              id.includes('/i18next/') ||
              id.includes('/react-i18next/') ||
              id.includes('/i18next-browser-languagedetector/')
            ) {
              return 'vendor-i18n'
            }
            return undefined
          },
        },
      },
    },
    server: {
      host: '0.0.0.0',
      ...(allowedHosts !== undefined ? { allowedHosts } : {}),
      proxy: {
        '/api': {
          target: backendTarget,
          changeOrigin: proxyChangeOrigin,
        },
        '/ws': {
          target: wsTarget,
          ws: true,
          changeOrigin: proxyChangeOrigin,
        },
      },
    },
  }
})
