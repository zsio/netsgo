import path from "path"
import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, __dirname, '')
  const backendTarget = (env.VITE_DEV_PROXY_TARGET || 'http://127.0.0.1:9527').replace(/\/$/, '')
  const wsTarget = backendTarget.replace(/^http/, 'ws')

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
    server: {
      host: '0.0.0.0',
      allowedHosts: [
        "xxx.com"
      ],
      proxy: {
        '/api': {
          target: backendTarget,
          // 管理面 Host 判定依赖浏览器原始 Host（例如 localhost:5173）。
          // dev proxy 不能改写成 backendTarget，否则后端会把管理 API 当成非管理 Host。
        },
        '/ws': {
          target: wsTarget,
          ws: true,
          // 与 /api 保持一致，保留原始 Host 供后端分发判断使用。
        },
      },
    },
  }
})
