import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { compression } from 'vite-plugin-compression2'
import { fileURLToPath, URL } from 'node:url'

// AQUA 前端构建配置
// - base '/'：history 路由真实路径部署（站点在域名根），资源绝对路径在任意深度的 URL 下都正确
// - /v1 代理：dev 模式直连线上网关，本地边改边用真实 API
// - 预压缩：构建期生成 .gz / .br，服务器零压缩 CPU 开销（弱主机友好）
export default defineConfig({
  base: '/',
  plugins: [
    vue(),
    compression({ algorithm: 'gzip', include: /\.(js|mjs|css|html|svg|json)$/ }),
    compression({ algorithm: 'brotliCompress', include: /\.(js|mjs|css|html|svg|json)$/ }),
  ],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    proxy: {
      '/v1': { target: 'https://api.ltzy.top', changeOrigin: true },
    },
  },
  build: {
    chunkSizeWarningLimit: 700,
    assetsInlineLimit: 8192,
    rollupOptions: {
      output: {
        // vue 运行时独立分包：业务页更新不失效框架缓存
        manualChunks: {
          vue: ['vue', 'vue-router'],
        },
      },
    },
  },
})
