import { createRouter, createWebHistory } from 'vue-router'
import { TOOL_REGISTRY } from '@/tools/registry'

// 18 个页面路由（history 模式，可直达/可分享；旧 hash 链接由 main.ts 入口改写）
// meta.title = 页面主标题（渲染时自动补 " · AQUA api" 后缀；/home 为完整标题不补）
// meta.desc  = 每页专属 description；meta.noindex = 私密页禁止收录
const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: (to, _from, saved) => saved || (to.hash ? { el: to.hash, top: 80 } : { top: 0 }),
  routes: [
    { path: '/', redirect: '/home' },
    { path: '/home', component: () => import('@/pages/HomePage.vue'), meta: {
      title: 'AQUA api — 免费 AI API 网关 · ACU 工程系列',
      desc: 'AQUA api — ACU 工程系列开源旗舰项目。免费 AI API 网关，OpenAI 兼容，多模型聚合，注册即用。' } },
    { path: '/models', component: () => import('@/pages/ModelsPage.vue'), meta: {
      title: '模型广场 — 在线 AI 模型列表与实时价格',
      desc: 'AQUA api 模型广场：DeepSeek、GLM、Kimi、GPT、豆包 Seed 等主流大模型的实时在线状态、首字延迟与按次/按量价格，免费与低价档任选。' } },
    { path: '/model/:id', component: () => import('@/pages/ModelDetailPage.vue'), meta: {
      title: '模型详情' } },
    { path: '/capabilities', redirect: { path: '/models', query: { view: 'cap' } } },
    { path: '/api', component: () => import('@/pages/ApiPage.vue'), meta: {
      title: 'API 文档 — OpenAI 兼容接口接入指南',
      desc: 'AQUA api API 文档：OpenAI 兼容的 /v1/chat/completions 等接口，任何 OpenAI SDK 改个 base_url 即可接入，含密钥创建与计费说明。' } },
    { path: '/playground', component: () => import('@/pages/PlaygroundPage.vue'), meta: {
      title: '在线 Playground — 免费试玩大模型',
      desc: 'AQUA api Playground：浏览器里直接与各大 AI 模型对话试玩，无需本地环境，注册即可用。' } },
    { path: '/tools', component: () => import('@/pages/ToolsPage.vue'), meta: {
      title: '工具箱 — 免费在线 AI 与开发者工具',
      desc: 'AQUA api 工具箱：IP 归属地、AI 翻译、AI 摘要、正则解释、哈希计算、周报生成、五子棋等免费在线工具，即开即用。' } },
    { path: '/tools/:name', component: () => import('@/pages/ToolPage.vue') },
    { path: '/treehole', component: () => import('@/pages/TreeholePage.vue'), meta: {
      title: '树洞 — AI 倾诉与陪伴',
      desc: 'AQUA api 树洞：向 AI 说出心事，获得陪伴与回应，不替代专业心理支持。' } },
    { path: '/prompts', component: () => import('@/pages/PromptsPage.vue'), meta: {
      title: '提示词库 — 精选 Prompt 模板',
      desc: 'AQUA api 提示词库：写作、编程、学习、办公等场景的精选 Prompt 模板，一键复制到 Playground 试用。' } },
    { path: '/arena', component: () => import('@/pages/ArenaPage.vue'), meta: {
      title: '竞技场 — 模型盲测对战',
      desc: 'AQUA api 竞技场：匿名盲测两个 AI 模型的回答并投票，看看谁更强。' } },
    { path: '/status', component: () => import('@/pages/StatusPage.vue'), meta: {
      title: '服务状态 — 实时可用性监控',
      desc: 'AQUA api 服务状态：网关与各上游模型线路的实时可用性监控。' } },
    { path: '/usage', component: () => import('@/pages/UsagePage.vue'), meta: {
      title: '用量查询', noindex: true } },
    { path: '/finance', component: () => import('@/pages/FinancePage.vue'), meta: {
      title: '财务中心', noindex: true } },
    { path: '/community', component: () => import('@/pages/CommunityPage.vue'), meta: {
      title: '社区 — 交流与反馈',
      desc: 'AQUA api 社区：QQ 群交流、问题反馈与最新动态。' } },
    { path: '/sponsor', component: () => import('@/pages/SponsorPage.vue'), meta: {
      title: '赞助支持 — 请 AQUA api 喝杯咖啡',
      desc: 'AQUA api 是半公益项目，赞助帮助我们覆盖上游算力成本，让免费额度持续下去。' } },
    { path: '/login', component: () => import('@/pages/LoginPage.vue'), meta: {
      title: '登录 / 注册', noindex: true } },
    // /register 兼容重定向：注册是 /login 页内的 tab，老邀请链接与直链透传 code / mode
    { path: '/register', redirect: (to) => ({ path: '/login', query: { ...to.query, mode: 'register' } }) },
    // /pay/return 支付完成回跳（EPay return_url 指向此处）：回到控制台并保留平台带回的参数。
    // 控制台启动时用 localStorage 里的待支付订单恢复上下文（停到正确页签 + 轮询 /v1/pay/status
    // 确认到账），故这里不再指定 tab（20260922：支付改为跳转平台收银台，回跳即靠此路径）
    { path: '/pay/return', redirect: (to) => ({ path: '/console', query: to.query }) },
    { path: '/console', component: () => import('@/pages/ConsolePage.vue'), meta: {
      title: '控制台', noindex: true } },
    { path: '/admin', component: () => import('@/pages/AdminPage.vue'), meta: {
      title: '管理控制台', noindex: true } },
    { path: '/:pathMatch(.*)*', redirect: '/home' },
  ],
})

/* SEO：SPA 单壳 HTML 的逐页搜索引擎适配——每次路由切换同步
 * title / description / canonical / og:* / robots(noindex)。
 * 20260920：站点对外主域统一为 acu.ltzy.top（aqua.ltzy.top / aqua.zhuafs.com / api.ltzy.top
 * 为防呆或接口域，页面侧由 nginx 301 归一到主域，/v1 接口保持原样）。 */
const SITE = 'https://acu.ltzy.top'
const SEO_TITLE = 'AQUA api — 免费 AI API 网关 · ACU 工程系列'
const SEO_DESC = 'AQUA api 是 ACU 工程系列开源旗舰项目：OpenAI 兼容的多模型聚合 API 网关。公益免费线路注册即用、不扣个人余额，覆盖 DeepSeek、GLM、Kimi、GPT 等主流大模型；需要生产级稳定接口时可切低价高速专线，按次或按量计费，改一个 base_url 即可接入。'

function upsertMeta(name: string, content: string, attr: 'name' | 'property' = 'name') {
  let el = document.head.querySelector<HTMLMetaElement>(`meta[${attr}="${name}"]`)
  if (!el) {
    el = document.createElement('meta')
    el.setAttribute(attr, name)
    document.head.appendChild(el)
  }
  el.content = content
}

/** robots 标记：私密页 noindex（首屏也必须执行，否则 /console 等会被收录） */
function applyRobots(noindex?: boolean) {
  let robots = document.head.querySelector<HTMLMetaElement>('meta[name="robots"]')
  if (noindex) {
    if (!robots) {
      robots = document.createElement('meta')
      robots.name = 'robots'
      document.head.appendChild(robots)
    }
    robots.content = 'noindex, nofollow'
  } else {
    robots?.remove()
  }
}

/* SEO：SPA 单壳 HTML 的逐页搜索引擎适配——路由切换时同步
 * title / description / canonical / og:* / robots(noindex)。
 *
 * 首屏不覆盖 title/description/canonical/og：构建期 prerender 已在 HTML 里写好精确文案
 * （含每个模型页的唯一标题），覆盖只会把它冲成通用文案。爬虫按 URL 逐个抓取、走的都是首屏，
 * 所以这里跳过首屏既保住了静态文案，也不影响站内跳转时的标签页标题。
 * robots 例外——必须每次执行，否则私密页直连时不会带 noindex。 */
let seoFirstPaint = true
router.afterEach((to) => {
  const m = to.meta as { title?: string; desc?: string; noindex?: boolean }
  applyRobots(m.noindex)
  if (seoFirstPaint) {
    seoFirstPaint = false
    return
  }
  // 工具子页标题取自注册表（/tools/translate → "AI 翻译 — 免费在线工具 · AQUA api"）
  const tool = to.path.startsWith('/tools/') ? TOOL_REGISTRY[to.params.name as string] : null
  const title = m.title ?? (tool ? `${tool.title} — 免费在线工具` : SEO_TITLE)
  const desc = m.desc ?? tool?.desc ?? SEO_DESC
  document.title = to.path === '/home' ? title : `${title} · AQUA api`
  upsertMeta('description', desc)
  upsertMeta('og:title', title, 'property')
  upsertMeta('og:description', desc, 'property')
  upsertMeta('og:url', to.path === '/home' ? `${SITE}/` : `${SITE}${to.path}`, 'property')
  // canonical：/home 归一到站点根（对外分享统一走 acu.ltzy.top）
  let link = document.head.querySelector<HTMLLinkElement>('link[rel="canonical"]')
  if (!link) {
    link = document.createElement('link')
    link.rel = 'canonical'
    document.head.appendChild(link)
  }
  link.href = to.path === '/home' ? `${SITE}/` : `${SITE}${to.path}`
})

/* 动态 chunk 加载失败自愈（20260919 紧急加固）：
 * 老用户浏览器缓存了旧 index.html，其引用的旧版 chunk 若已不存在，
 * 点击导航（如首页头像 → /console）时动态 import 会失败，表现为"点了没反应"。
 * 这里兜底：检测到 chunk 加载失败 → 强刷一次拉取最新 HTML 与资源（带会话内防抖，避免死循环）。
 * 注意：仅对"资源加载类"错误生效，业务错误不触发刷新。 */
const CHUNK_RELOAD_KEY = 'aqua_chunk_reload'
router.onError((err) => {
  const msg = String((err as Error)?.message || err || '')
  const isChunkErr = /Failed to fetch dynamically imported module|Importing a module script failed|error loading dynamically imported module|Loading chunk .* failed/i.test(msg)
  if (!isChunkErr) return
  try {
    if (sessionStorage.getItem(CHUNK_RELOAD_KEY)) return // 已刷过一次仍失败：不再循环
    sessionStorage.setItem(CHUNK_RELOAD_KEY, '1')
  } catch { return }
  console.warn('[aqua] 资源版本过期，正在自动刷新获取最新版本…')
  location.reload()
})

export default router
