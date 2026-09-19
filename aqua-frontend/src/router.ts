import { createRouter, createWebHistory } from 'vue-router'
import { TOOL_REGISTRY } from '@/tools/registry'

// 19 个页面路由（history 模式，可直达/可分享；旧 hash 链接由 main.ts 入口改写）
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
      desc: 'AQUA api 模型广场：GPT、Claude、Gemini、DeepSeek、GLM、Kimi 等主流 AI 模型实时在线状态与按次价格，免费与低价档任选。' } },
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
    { path: '/pool', component: () => import('@/pages/PoolPage.vue'), meta: {
      title: '公共众筹池 — 半公益 AI 算力',
      desc: 'AQUA api 公共池贡献与历史流水，与个人余额独立。acu/ 官方自营纯免费，不扣公共池额度。' } },
    { path: '/login', component: () => import('@/pages/LoginPage.vue'), meta: {
      title: '登录 / 注册', noindex: true } },
    // /register 兼容重定向：注册是 /login 页内的 tab，老邀请链接与直链透传 code / mode
    { path: '/register', redirect: (to) => ({ path: '/login', query: { ...to.query, mode: 'register' } }) },
    { path: '/console', component: () => import('@/pages/ConsolePage.vue'), meta: {
      title: '控制台', noindex: true } },
    { path: '/admin', component: () => import('@/pages/AdminPage.vue'), meta: {
      title: '管理控制台', noindex: true } },
    { path: '/:pathMatch(.*)*', redirect: '/home' },
  ],
})

/* SEO：SPA 单壳 HTML 的逐页搜索引擎适配——每次路由切换同步
 * title / description / canonical / og:* / robots(noindex)。 */
const SITE = 'https://aqua.ltzy.top'
const SEO_TITLE = 'AQUA api — 免费 AI API 网关 · ACU 工程系列'
const SEO_DESC = 'AQUA api — ACU 工程系列开源旗舰项目。免费 AI API 网关，OpenAI 兼容，多模型聚合，注册即用。'

function upsertMeta(name: string, content: string, attr: 'name' | 'property' = 'name') {
  let el = document.head.querySelector<HTMLMetaElement>(`meta[${attr}="${name}"]`)
  if (!el) {
    el = document.createElement('meta')
    el.setAttribute(attr, name)
    document.head.appendChild(el)
  }
  el.content = content
}

router.afterEach((to) => {
  const m = to.meta as { title?: string; desc?: string; noindex?: boolean }
  // 工具子页标题取自注册表（/tools/translate → "AI 翻译 — 免费在线工具 · AQUA api"）
  const tool = to.path.startsWith('/tools/') ? TOOL_REGISTRY[to.params.name as string] : null
  const title = m.title ?? (tool ? `${tool.title} — 免费在线工具` : SEO_TITLE)
  const desc = m.desc ?? tool?.desc ?? SEO_DESC
  document.title = to.path === '/home' ? title : `${title} · AQUA api`
  upsertMeta('description', desc)
  upsertMeta('og:title', title, 'property')
  upsertMeta('og:description', desc, 'property')
  upsertMeta('og:url', to.path === '/home' ? `${SITE}/` : `${SITE}${to.path}`, 'property')
  // canonical：/home 归一到站点根（对外分享的都是 aqua.ltzy.top/）
  let link = document.head.querySelector<HTMLLinkElement>('link[rel="canonical"]')
  if (!link) {
    link = document.createElement('link')
    link.rel = 'canonical'
    document.head.appendChild(link)
  }
  link.href = to.path === '/home' ? `${SITE}/` : `${SITE}${to.path}`
  // robots：私密页 noindex，公开页移除标记（还原默认 index,follow）
  let robots = document.head.querySelector<HTMLMetaElement>('meta[name="robots"]')
  if (m.noindex) {
    if (!robots) {
      robots = document.createElement('meta')
      robots.name = 'robots'
      document.head.appendChild(robots)
    }
    robots.content = 'noindex, nofollow'
  } else {
    robots?.remove()
  }
})

export default router
