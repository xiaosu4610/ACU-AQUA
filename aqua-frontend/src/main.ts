import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import './styles/legacy.css'
import './styles/theme.css'
import './styles/aurora.css'
import { setUnauthorizedHook } from './composables/useApi'
import { isLoggedIn, loadMe } from './composables/useAuth'

/* ===== 全局错误防线（三层）：Vue errorHandler / unhandledrejection / window error =====
   原则：只记录 + 温和提示，绝不中断渲染。同条消息 5 秒内去重，防轰炸。 */
let lastToastMsg = ''
let lastToastTs = 0
let toastEl: HTMLElement | null = null
function showErrToast(msg: string) {
  const now = Date.now()
  if (msg === lastToastMsg && now - lastToastTs < 5000) return
  lastToastMsg = msg; lastToastTs = now
  if (!toastEl) {
    toastEl = document.createElement('div')
    toastEl.id = 'aqua-err-toast'
    toastEl.style.cssText = 'position:fixed;left:50%;bottom:72px;transform:translateX(-50%);z-index:99999;' +
      'background:rgba(20,32,52,.92);color:#e6edf5;padding:10px 18px;border-radius:12px;font-size:13px;' +
      'border:1px solid rgba(56,189,248,.35);box-shadow:0 12px 40px rgba(0,0,0,.45);' +
      'backdrop-filter:blur(10px);transition:opacity .3s;pointer-events:none;max-width:88vw;'
    document.body.appendChild(toastEl)
  }
  toastEl.textContent = msg
  toastEl.style.opacity = '1'
  window.setTimeout(() => { if (toastEl) toastEl.style.opacity = '0' }, 3000)
}
;(window as any).__AQUA_ERR_TOAST = showErrToast

const VITE_PROD = import.meta.env.PROD
window.addEventListener('unhandledrejection', (e) => {
  console.error('[AQUA] 未处理的 Promise 异常:', e.reason)
  if (VITE_PROD) showErrToast('网络请求出了点小问题，请稍后重试')
})
window.addEventListener('error', (e) => {
  // 资源加载错误（img/script/css）不打扰用户，仅记录
  console.error('[AQUA] 全局错误:', e.message || e.target)
})

// 旧版 hash 链接兼容：/#/xxx → /xxx（入口处一次性 replaceState 改写，路由按真实路径解析）。
// 网关错误信息与老分享链接里的 #/api、#/models 等因此继续可用
if (location.hash.startsWith('#/')) history.replaceState(null, '', location.hash.slice(1))

// 会话失效 401 → 统一跳登录页（带回跳地址）
setUnauthorizedHook(() => {
  if (!isLoggedIn()) router.replace({ path: '/login', query: { redirect: location.pathname + location.search } })
})

// 启动时恢复会话（有令牌则拉一次 /auth/me，同步全局状态）
void loadMe()

const app = createApp(App)
app.config.errorHandler = (err, _inst, info) => {
  console.error('[AQUA] Vue 渲染错误:', err, '·', info)
  if (VITE_PROD) showErrToast('页面出了点小问题，已自动记录')
}
app.use(router).mount('#app')
// boot 自愈标志：挂载成功，index.html 的 8 秒强刷守卫可安全取消
;(window as any).__AQUA_BOOTED = true

// 闲置预取：首屏渲染完把其余页面与工具 chunk 提前拉好（全站 gzip 约 200KB，远低于体积预算），
// 之后所有页内跳转零等待。省流模式（saveData）或 2g 弱网不预取
const conn = (navigator as any).connection
if (!conn?.saveData && conn?.effectiveType !== '2g') {
  const idle = (window as any).requestIdleCallback || ((fn: () => void) => setTimeout(fn, 1800))
  idle(() => {
    const pages = [
      import('@/pages/ModelsPage.vue'), import('@/pages/ModelDetailPage.vue'),
      import('@/pages/CapabilitiesPage.vue'), import('@/pages/ApiPage.vue'),
      import('@/pages/PlaygroundPage.vue'), import('@/pages/ToolsPage.vue'),
      import('@/pages/ToolPage.vue'), import('@/pages/TreeholePage.vue'),
      import('@/pages/PromptsPage.vue'), import('@/pages/ArenaPage.vue'),
      import('@/pages/StatusPage.vue'), import('@/pages/UsagePage.vue'),
      import('@/pages/SponsorPage.vue'), import('@/pages/LoginPage.vue'),
      import('@/pages/ConsolePage.vue'),
    ]
    Promise.all(pages)
      .then(() => import('@/tools/registry'))
      .then(r => Object.values(r.TOOL_REGISTRY).forEach(t => t.load()))
      .catch(() => { /* 预取失败不影响正常使用，点击时仍会按需加载 */ })
  })
}
