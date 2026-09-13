import { createRouter, createWebHistory } from 'vue-router'

// 19 个页面路由（history 模式，可直达/可分享；旧 hash 链接由 main.ts 入口改写）
const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: () => ({ top: 0 }),
  routes: [
    { path: '/', redirect: '/home' },
    { path: '/home', component: () => import('@/pages/HomePage.vue') },
    { path: '/models', component: () => import('@/pages/ModelsPage.vue') },
    { path: '/model/:id', component: () => import('@/pages/ModelDetailPage.vue') },
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
    { path: '/admin', component: () => import('@/pages/AdminPage.vue'), meta: { noindex: true } },
    { path: '/:pathMatch(.*)*', redirect: '/home' },
  ],
})

export default router
