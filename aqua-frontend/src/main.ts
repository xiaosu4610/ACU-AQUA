/* AQUA api 前端入口：主题先行 + 全局错误防线 + hash 链接兼容 + 路由守卫 */
import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import { setUnauthorizedHook } from './composables/useApi'
import { isLoggedIn } from './composables/useAuth'

import './styles/tokens.css'
import './styles/base.css'
import './styles/components.css'
import './styles/layout.css'

/* 旧版 hash 链接（/#/xxx）一次性改写为真实路径 */
if (location.hash.startsWith('#/')) {
  const p = location.hash.slice(1)
  history.replaceState(null, '', p + location.search)
}

/* 需要登录的页面：未登录 → 登录页（带回跳） */
const AUTH_PATHS = ['/console', '/finance', '/usage']
router.beforeEach(to => {
  if (AUTH_PATHS.includes(to.path) && !isLoggedIn()) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }
})

/* 会话 401 → 清理并跳登录（useApi 钩子） */
setUnauthorizedHook(() => {
  router.push({ path: '/login', query: { redirect: location.pathname } })
})

const app = createApp(App)

/* 全局错误防线：未捕获同步错误不上抛白屏 */
app.config.errorHandler = (err, _inst, info) => {
  console.error('[aqua] 全局错误:', err, info)
}

app.use(router)
app.mount('#app')
;(window as any).__AQUA_BOOTED = true // boot 自愈标记：挂载成功，取消强刷定时

/* 异步错误防线：unhandledrejection 兜底记录 */
window.addEventListener('unhandledrejection', e => {
  console.error('[aqua] 未处理 Promise 拒绝:', e.reason)
})
