import { createRouter, createWebHistory } from 'vue-router'

// 路由表与旧版 12 路由一一对应（history 模式：每个页面独立真实 URL，可直达/可分享；
// 旧版 hash 链接 #/xxx 由 main.ts 入口处一次性改写为真实路径，网关 SPA 回退兜底深链接）
const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: () => ({ top: 0 }),
  routes: [
    { path: '/', redirect: '/home' },
    { path: '/home', component: () => import('@/pages/HomePage.vue') },
    { path: '/models', component: () => import('@/pages/ModelsPage.vue') },
    { path: '/model/:id', component: () => import('@/pages/ModelDetailPage.vue') },
    // 能力矩阵已合并进模型中心（?view=cap 视图），旧地址重定向保持老链接可用
    { path: '/capabilities', redirect: { path: '/models', query: { view: 'cap' } } },
    { path: '/api', component: () => import('@/pages/ApiPage.vue') },
    { path: '/playground', component: () => import('@/pages/PlaygroundPage.vue') },
    { path: '/tools', component: () => import('@/pages/ToolsPage.vue') },
    { path: '/tools/:name', component: () => import('@/pages/ToolPage.vue') },
    { path: '/treehole', component: () => import('@/pages/TreeholePage.vue') },
    { path: '/prompts', component: () => import('@/pages/PromptsPage.vue') },
    { path: '/arena', component: () => import('@/pages/ArenaPage.vue') },
    { path: '/status', component: () => import('@/pages/StatusPage.vue') },
    { path: '/usage', component: () => import('@/pages/UsagePage.vue') },
    { path: '/finance', component: () => import('@/pages/FinancePage.vue') },
    { path: '/community', component: () => import('@/pages/CommunityPage.vue') },
    { path: '/sponsor', component: () => import('@/pages/SponsorPage.vue') },
    { path: '/pool', component: () => import('@/pages/PoolPage.vue') },
    { path: '/login', component: () => import('@/pages/LoginPage.vue') },
    { path: '/console', component: () => import('@/pages/ConsolePage.vue') },
    // 站长管理控制台（单密码，站内导航无入口；meta.noindex 由页面动态设置 robots）
    { path: '/admin', component: () => import('@/pages/AdminPage.vue'), meta: { noindex: true } },
    { path: '/:pathMatch(.*)*', redirect: '/home' },
  ],
})

export default router
