<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useTheme } from '@/composables/useTheme'
import { avatarUrl, isLoggedIn, me } from '@/composables/useAuth'
import AqIcon from '@/components/AqIcon.vue'

const route = useRoute()
const { theme, set } = useTheme()

/** 打开的下拉菜单（同一时间最多一个；点击外部/切路由自动收起） */
const openMenu = ref('')
function toggleMenu(name: string) {
  openMenu.value = openMenu.value === name ? '' : name
}
document.addEventListener('click', e => {
  if (!(e.target as HTMLElement).closest('.nav-drop')) openMenu.value = ''
})
watch(() => route.fullPath, () => { openMenu.value = ''; sheet.value = false })

function isActive(tab: string): boolean {
  const p = route.path.split('?')[0]
  if (tab === 'home') return p === '/' || p === '/home'
  if (tab === 'models') return p === '/models' || p.startsWith('/model/')
  return p.startsWith('/' + tab)
}

/* ===== 移动端底部标签栏 ===== */
const sheet = ref(false)
/** 抽屉里除四个底部 Tab 外的全部入口 */
const sheetLinks = [
  { to: '/api', label: 'API 文档', icon: '<path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/>' },
  { to: '/treehole', label: '树洞', icon: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>' },
  { to: '/prompts', label: '提示词', icon: '<path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.12 2.12 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>' },
  { to: '/arena', label: '竞技场', icon: '<path d="M6 2 3 6v14a2 2 0 0 0 2 2h4a2 2 0 0 0 2-2V6L8 2z"/><path d="M4 6h16"/><path d="M18 2l3 4v14a2 2 0 0 1-2 2h-4a2 2 0 0 1-2-2V6l3-4z"/>' },
  { to: '/status', label: '状态大屏', icon: '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>' },
  { to: '/usage', label: '我的用量', icon: '<path d="M21.21 15.89A10 10 0 1 1 8 2.83"/><path d="M22 12A10 10 0 0 0 12 2v10z"/>' },
  { to: '/sponsor', label: '赞助支持', icon: '<path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/>' },
]
const openSheet = () => { openMenu.value = ''; sheet.value = true }
</script>

<template>
  <!-- ===== 顶部切换栏 ===== -->
  <nav class="topnav">
    <div class="wrap nav-inner">
      <router-link class="nav-brand" to="/home">
        <img src="/favicon.ico" alt="AQUA" width="34" height="34" style="border-radius:8px; display:block;" class="nav-logo">
        <span>AQUA</span>
      </router-link>
      <div class="nav-right">
        <div class="nav-tabs">
          <router-link to="/home">
            <span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 10.5 12 3l9 7.5"/><path d="M5 9.5V21h14V9.5"/><path d="M9 21v-6h6v6"/></svg></span>
            <span class="nt nt-long">首页</span><span class="nt nt-short">首页</span>
          </router-link>

          <div class="nav-drop" :class="{ open: openMenu === 'exp', active: isActive('playground') || isActive('tools') || isActive('treehole') || isActive('prompts') }">
            <button type="button" class="drop-toggle" @click.stop="toggleMenu('exp')">
              <span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg></span>
              <span class="dt-label">体验中心</span>
              <svg class="caret" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>
            </button>
            <div class="drop-panel" v-show="openMenu === 'exp'">
              <router-link to="/playground">
                <span class="drow"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg></span>AI 对话</span>
                <span class="ddesc">在线对话 · 流式体验 · 模型即点即用</span>
              </router-link>
              <router-link to="/tools">
                <span class="drow"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg></span>工具箱</span>
                <span class="ddesc">25+ 款实用小工具 · 翻译 / 哈希 / 短链等</span>
              </router-link>
              <router-link to="/treehole">
                <span class="drow"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg></span>树洞</span>
                <span class="ddesc">匿名倾诉 · 推理模型深度陪伴</span>
              </router-link>
              <router-link to="/prompts">
                <span class="drow"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.12 2.12 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg></span>提示词工坊</span>
                <span class="ddesc">精选提示词模板 · 一键填入对话</span>
              </router-link>
            </div>
          </div>

          <router-link to="/models">
            <span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2 2 7l10 5 10-5-10-5z"/><path d="m2 17 10 5 10-5"/><path d="m2 12 10 5 10-5"/></svg></span>
            <span class="nt nt-long">模型中心</span><span class="nt nt-short">模型</span>
          </router-link>
          <router-link to="/api">
            <span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/></svg></span>
            <span class="nt nt-long">API 文档</span><span class="nt nt-short">API</span>
          </router-link>
          <router-link to="/sponsor" class="nav-sponsor" title="赞助 AQUA · 请作者喝杯咖啡">
            <span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/></svg></span>
            <span class="nt nt-long">赞助</span><span class="nt nt-short">赞助</span>
          </router-link>

          <div class="nav-drop" :class="{ open: openMenu === 'data', active: isActive('arena') || isActive('status') || isActive('usage') }">
            <button type="button" class="drop-toggle" @click.stop="toggleMenu('data')">
              <span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg></span>
              <span class="dt-label">数据中心</span>
              <svg class="caret" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>
            </button>
            <div class="drop-panel" v-show="openMenu === 'data'">
              <router-link to="/arena">
                <span class="drow"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 2 3 6v14a2 2 0 0 0 2 2h4a2 2 0 0 0 2-2V6L8 2z"/><path d="M4 6h16"/><path d="M18 2l3 4v14a2 2 0 0 1-2 2h-4a2 2 0 0 1-2-2V6l3-4z"/></svg></span>模型竞技场</span>
                <span class="ddesc">双模型盲测对比 · 投票看胜率榜</span>
              </router-link>
              <router-link to="/status">
                <span class="drow"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg></span>状态大屏</span>
                <span class="ddesc">全站调用量 · 成功率 · 实时监控</span>
              </router-link>
              <router-link to="/usage">
                <span class="drow"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.21 15.89A10 10 0 1 1 8 2.83"/><path d="M22 12A10 10 0 0 0 12 2v10z"/></svg></span>我的用量</span>
                <span class="ddesc">按密钥查统计 · 匿名指纹不存明文</span>
              </router-link>
            </div>
          </div>
        </div>
        <div class="theme-switch">
          <button class="theme-btn" :class="{ active: theme === 'light' }" data-theme="light" title="白天亮色" @click="set('light')">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/></svg>
          </button>
          <button class="theme-btn" :class="{ active: theme === 'dark' }" data-theme="dark" title="深蓝模式" @click="set('dark')">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2 2 7l10 5 10-5-10-5z"/><path d="m2 17 10 5 10-5"/><path d="m2 12 10 5 10-5"/></svg>
          </button>
        </div>
        <!-- 用户区：登录 → 头像入口；未登录 → 登录按钮 -->
        <router-link v-if="isLoggedIn()" to="/console" class="nav-avatar" :title="me?.username || '个人控制台'">
          <img v-if="me?.avatar_ext" :src="avatarUrl() + '?t=' + me?.created_ts" alt="" />
          <span v-else class="nav-avatar-txt">{{ (me?.username || '我').slice(0, 1) }}</span>
        </router-link>
        <router-link v-else to="/login" class="nav-login-btn">登录 / 注册</router-link>
      </div>
    </div>
  </nav>

  <div class="wrap">
    <router-view />
  </div>

  <footer>
    <p>AQUA · <b>ACU 工程系列</b>开源旗舰项目 —— 更多生态链项目持续开发中 · 数据由 Nvidia NIM 与官方自营提供 · 仅用于技术学习与交流</p>
    <p style="margin-top:8px;">
      <a href="https://gitee.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener" style="color:var(--accent);">Gitee <AqIcon name="star" :size="13" /></a> ·
      <a href="https://github.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener" style="color:var(--accent);">GitHub <AqIcon name="star" :size="13" /></a> ·
      <a href="https://qm.qq.com/q/qoe6XbsVge" target="_blank" rel="noopener" style="color:var(--accent);">QQ 一群（1103667832）</a> ·
      <a href="https://qm.qq.com/q/o8QDbza2Ge" target="_blank" rel="noopener" style="color:var(--accent);">QQ 二群（1006740220）</a> ·
      <a href="https://pd.qq.com/s/e4ktxw1b8" target="_blank" rel="noopener" style="color:var(--accent);">QQ 频道（pd57362562）</a> ·
      <router-link to="/sponsor" style="color:var(--accent);">赞助支持</router-link>
      · <span style="color:var(--muted);">v5.2.0 沧溟</span>
    </p>
  </footer>

  <!-- ===== 移动端底部标签栏（≤860px，样式见 theme.css）===== -->
  <nav class="tabbar">
    <router-link to="/home" :class="{ active: isActive('home') }">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 10.5 12 3l9 7.5"/><path d="M5 9.5V21h14V9.5"/><path d="M9 21v-6h6v6"/></svg>
      <span>首页</span>
    </router-link>
    <router-link to="/models" :class="{ active: isActive('models') }">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2 2 7l10 5 10-5-10-5z"/><path d="m2 17 10 5 10-5"/><path d="m2 12 10 5 10-5"/></svg>
      <span>模型</span>
    </router-link>
    <router-link to="/playground" :class="{ active: isActive('playground') }">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
      <span>对话</span>
    </router-link>
    <router-link to="/tools" :class="{ active: isActive('tools') }">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg>
      <span>工具</span>
    </router-link>
    <button type="button" :class="{ active: sheet }" @click="openSheet">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="1"/><circle cx="12" cy="5" r="1"/><circle cx="12" cy="19" r="1"/><circle cx="5" cy="12" r="1"/><circle cx="19" cy="12" r="1"/></svg>
      <span>更多</span>
    </button>
  </nav>

  <!-- 「更多」抽屉：底部 Tab 未收录的全部入口 -->
  <div class="sheet-mask" v-if="sheet" @click="sheet = false">
    <div class="sheet" @click.stop>
      <div class="sheet-grid">
        <router-link v-for="link in sheetLinks" :key="link.to" :to="link.to" @click="sheet = false">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="link.icon"></svg>
          <span>{{ link.label }}</span>
        </router-link>
      </div>
      <button class="sheet-close" @click="sheet = false">关闭</button>
    </div>
  </div>
</template>

<style>
/* 路由切换入场动画（对应旧版 .page.active 的 fadein） */
.route-page { animation: fadein .25s ease; }

/* ===== 导航栏用户区 ===== */
.nav-avatar {
  width: 34px; height: 34px; border-radius: 50%; overflow: hidden; flex-shrink: 0;
  background: var(--accent); display: flex; align-items: center; justify-content: center;
  border: 2px solid rgba(128,140,160,.35); transition: all .2s ease;
}
.nav-avatar:hover {
  border-color: var(--aqua);
  box-shadow: 0 0 0 3px rgba(56, 189, 248, .25);
}
.nav-avatar img { width: 100%; height: 100%; object-fit: cover; }
.nav-avatar-txt { color: #fff; font-size: 15px; font-weight: 800; }
.nav-login-btn {
  font-size: 13px; font-weight: 650; padding: 8px 14px; border-radius: 10px;
  background: var(--btn-grad); color: #fff; white-space: nowrap;
  text-shadow: 0 1px 1px rgba(2, 32, 71, .25);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, .28),
    inset 0 -1px 0 rgba(2, 32, 71, .22),
    0 2px 10px rgba(8, 145, 178, .32);
  transition: all .16s ease;
}
.nav-login-btn:hover {
  background: var(--btn-grad-hover, var(--btn-grad));
  transform: translateY(-1px);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, .32),
    inset 0 -1px 0 rgba(2, 32, 71, .22),
    0 4px 14px rgba(8, 145, 178, .42);
}
.nav-login-btn:active {
  transform: translateY(0);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, .18), 0 1px 4px rgba(8, 145, 178, .28);
}
@media (max-width: 860px) {
  .nav-login-btn { padding: 6px 10px; font-size: 12px; }
}
</style>
