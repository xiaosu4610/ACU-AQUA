<script setup lang="ts">
/* 未登录提示条：需要密钥的页面在未登录时显示引导（可复用组件） */
import { isLoggedIn } from '@/composables/useAuth'
</script>

<template>
  <div v-if="!isLoggedIn()" class="auth-banner">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 9v4M12 17h.01"/><circle cx="12" cy="12" r="10" opacity=".35"/></svg>
    <span>本功能需要登录后使用——注册免费，控制台一键创建 API 密钥。</span>
    <router-link to="/login" class="auth-banner-btn">登录 / 注册</router-link>
  </div>
</template>

<style scoped>
.auth-banner {
  display: flex; align-items: center; gap: 10px; flex-wrap: wrap;
  padding: 12px 16px; margin-bottom: 16px;
  border: 1px solid rgba(56,189,248,.35); border-radius: 12px;
  background: rgba(56,189,248,.08); font-size: 13.5px;
}
.auth-banner svg { width: 18px; height: 18px; color: var(--aqua, #38bdf8); flex-shrink: 0; }
.auth-banner-btn {
  margin-left: auto; padding: 6px 14px; border-radius: 8px;
  background: var(--btn-grad); color: #fff; font-size: 12.5px; font-weight: 700; white-space: nowrap;
  text-shadow: 0 1px 1px rgba(2, 32, 71, .25);
}
.auth-banner-btn:hover { opacity: .88; }
</style>
