<script setup lang="ts">
/* App 双壳：门户（顶栏+页脚） / 工作台（侧栏）——视觉与骨架，页面零壳样式依赖 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useTheme } from './composables/useTheme'
import { useMeta } from './composables/useMeta'
import { sessionToken, me, loadMe, avatarUrl, logout } from './composables/useAuth'
import AqIcon from './components/AqIcon.vue'

const route = useRoute()
const { theme, set: setTheme } = useTheme()
const { meta, loadMeta } = useMeta()

onMounted(() => { loadMeta(); if (sessionToken.value) loadMe() })

/* 工作台壳路径；/admin 为独立入口：无任何壳、无任何导航引用，仅站长手动输入 URL 进入 */
const BENCH = ['/console', '/finance', '/usage']
const isBench = computed(() => BENCH.includes(route.path))
const isStandalone = computed(() => route.path === '/admin')

/* ---- 门户导航 ---- */
interface NavLeaf { label: string; to: string }
const PORTAL_NAV: { label: string; to: string; match: string[]; children?: NavLeaf[] }[] = [
  { label: '首页', to: '/home', match: ['/home'] },
  { label: '模型与价格', to: '/models', match: ['/models', '/model'] },
  { label: '接入文档', to: '/api', match: ['/api'] },
  { label: '服务状态', to: '/status', match: ['/status'] },
  { label: '体验与生态', to: '/playground', match: ['/playground', '/treehole', '/tools', '/prompts', '/arena', '/community', '/sponsor'], children: [
    { label: 'AI 对话', to: '/playground' }, { label: '树洞', to: '/treehole' },
    { label: '工具箱', to: '/tools' }, { label: '提示词工坊', to: '/prompts' },
    { label: '模型竞技场', to: '/arena' }, { label: '社区交流', to: '/community' },
    { label: '自愿赞助', to: '/sponsor' },
  ] },
]
const mobileOpen = ref(false)
watch(() => route.path, () => { mobileOpen.value = false })

function navOn(n: { match: string[] }): boolean {
  if (n.match.includes(route.path)) return true
  return n.match.some(m => route.path.startsWith(m) && m !== '/home')
}

/* ---- 工作台导航 ---- */
const BENCH_NAV = [
  {
    grp: '工作台',
    items: [
      { label: '总览 · 密钥', to: '/console', icon: 'layout', match: ['/console'] },
      { label: '账单中心', to: '/finance', icon: 'wallet', match: ['/finance'] },
      { label: '我的用量', to: '/usage', icon: 'chart', match: ['/usage'] },
    ],
  },
  {
    grp: '快捷入口',
    items: [
      { label: '模型中心', to: '/models', icon: 'box', match: [] },
      { label: 'AI 对话', to: '/playground', icon: 'chat', match: [] },
      { label: 'API 文档', to: '/api', icon: 'book', match: [] },
    ],
  },
]
function benchOn(it: { match: string[] }): boolean { return it.match.includes(route.path) }

const initial = computed(() => (me.value?.username || 'A').charAt(0).toUpperCase())
const siteName = computed(() => meta.value?.name || 'AQUA api')

async function doLogout() {
  await logout()
  if (isBench.value) location.href = '/home'
}
</script>

<template>
  <!-- ============ 独立页（/admin）：无壳、无导航、与站点页面零关联 ============ -->
  <router-view v-if="isStandalone" v-slot="{ Component }">
    <transition name="page" mode="out-in">
      <component :is="Component" :key="route.path" />
    </transition>
  </router-view>

  <!-- ============ 工作台壳 ============ -->
  <div v-else-if="isBench" class="bench">
    <aside class="bside">
      <router-link to="/home" class="brand" style="padding: 2px 11px 10px;">
        <img src="/logo.png" alt="{{ siteName }}" />
        <em>{{ siteName }}</em>
      </router-link>
      <template v-for="g in BENCH_NAV" :key="g.grp">
        <div class="grp">{{ g.grp }}</div>
        <router-link
          v-for="it in g.items" :key="it.to" :to="it.to"
          class="blink" :class="{ on: benchOn(it) }"
        >
          <AqIcon :name="it.icon" :size="16" />{{ it.label }}
        </router-link>
      </template>
      <div class="spacer" />
      <div v-if="me" class="buser">
        <div class="avatar"><img v-if="avatarUrl()" :src="avatarUrl()" alt="" /><template v-else>{{ initial }}</template></div>
        <div>
          <div class="nm">{{ me.username }}</div>
          <div class="st">UID #{{ me.id }}</div>
        </div>
      </div>
      <button v-if="me" class="btn ghost sm bhide-m" @click="doLogout"><AqIcon name="arrow-right" :size="14" />退出登录</button>
      <router-link v-if="me" to="/home" class="blink bhide-m"><AqIcon name="home" :size="16" />返回门户</router-link>
      <router-link v-else to="/login" class="blink"><AqIcon name="key" :size="16" />登录 / 注册</router-link>
    </aside>

    <div class="bmain">
      <header class="btop">
        <div class="crumb">
          <router-link to="/home" style="color: var(--txt2);">{{ siteName }}</router-link>
          <span style="margin: 0 6px; opacity: .5;">/</span>
          <b>{{ $route.meta.title || '工作台' }}</b>
        </div>
        <div class="sp" />
        <button class="theme-btn" :title="theme === 'dark' ? '切换亮色' : '切换暗色'" @click="setTheme(theme === 'dark' ? 'light' : 'dark')">
          <AqIcon :name="theme === 'dark' ? 'sun' : 'moon'" :size="16" />
        </button>
        <router-link v-if="me" to="/console" class="avatar-btn" :title="me.username">
          <img v-if="avatarUrl()" :src="avatarUrl()" alt="" /><template v-else>{{ initial }}</template>
        </router-link>
      </header>
      <main class="bcontent">
        <router-view v-slot="{ Component }">
          <transition name="page" mode="out-in">
            <component :is="Component" :key="route.path" />
          </transition>
        </router-view>
      </main>
    </div>
  </div>

  <!-- ============ 门户壳 ============ -->
  <template v-else>
    <header class="ptop">
      <div class="ptop-in">
        <router-link to="/home" class="brand">
          <img src="/logo.png" alt="{{ siteName }}" />
          <em>{{ siteName }}</em>
        </router-link>

        <nav class="pnav" aria-label="主导航">
          <div v-for="n in PORTAL_NAV" :key="n.label" class="item">
            <router-link :to="n.to" class="nl" :class="{ on: navOn(n) }">
              {{ n.label }}<AqIcon v-if="n.children" name="chevron-down" :size="13" />
            </router-link>
            <div v-if="n.children" class="drop">
              <div class="dt">{{ n.label }}</div>
              <router-link v-for="c in n.children" :key="c.to" :to="c.to">
                <AqIcon name="arrow-right" :size="13" />{{ c.label }}
              </router-link>
            </div>
          </div>
        </nav>

        <div class="ptop-sp" />
        <div class="ptop-acts">
          <button class="theme-btn" :title="theme === 'dark' ? '切换亮色' : '切换暗色'" @click="setTheme(theme === 'dark' ? 'light' : 'dark')">
            <AqIcon :name="theme === 'dark' ? 'sun' : 'moon'" :size="16" />
          </button>
          <router-link v-if="me" to="/console" class="avatar-btn" :title="me.username">
            <img v-if="avatarUrl()" :src="avatarUrl()" alt="" /><template v-else>{{ initial }}</template>
          </router-link>
          <router-link v-else-if="route.path !== '/login'" to="/login" class="btn primary sm">登录 / 注册</router-link>
          <button class="burger" aria-label="菜单" :aria-expanded="mobileOpen" aria-controls="mobile-navigation" @keydown.esc="mobileOpen = false" @click="mobileOpen = !mobileOpen">
            <AqIcon :name="mobileOpen ? 'cross' : 'menu'" :size="17" />
          </button>
        </div>
      </div>

      <!-- 移动端菜单 -->
      <nav v-if="mobileOpen" id="mobile-navigation" class="mnav" aria-label="移动导航" @keydown.esc="mobileOpen = false">
        <template v-for="n in PORTAL_NAV" :key="n.label">
          <div class="dt">{{ n.label }}</div>
          <router-link v-if="!n.children" :to="n.to" :class="{ on: navOn(n) }">{{ n.label }}</router-link>
          <router-link v-for="c in n.children" :key="c.to" :to="c.to">{{ c.label }}</router-link>
        </template>
      </nav>
    </header>

    <router-view v-slot="{ Component }">
      <transition name="page" mode="out-in">
        <component :is="Component" :key="route.path" />
      </transition>
    </router-view>

    <footer class="foot">
      <div class="foot-in">
        <div>{{ siteName }} · OpenAI 兼容模型接口 · acu/ 纯免费 · aqua/ 按次与按量 · codex/ 按量</div>
        <div class="row" style="justify-content: center; flex-wrap: wrap;">
          <a href="https://gitee.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener">Gitee</a> ·
          <a href="https://github.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener">GitHub</a> ·
          <router-link to="/finance">财务管理中心</router-link> ·
          <router-link to="/sponsor">赞助支持</router-link>
          <template v-if="meta?.qq_group_url2"> ·
            <a :href="meta.qq_group_url2" target="_blank" rel="noopener">Q 群 {{ meta.qq_group2 }}</a>
          </template>
          <template v-if="meta?.qq_group_url"> ·
            <a :href="meta.qq_group_url" target="_blank" rel="noopener">Q 群 {{ meta.qq_group }}</a>
          </template>
        </div>
      </div>
    </footer>

    <a v-if="meta?.qq_group_url2 || meta?.qq_group_url" class="qq-fab" :href="meta?.qq_group_url2 || meta?.qq_group_url" target="_blank" rel="noopener">
      <AqIcon name="message" :size="15" />Q 群二群
    </a>
  </template>
</template>
