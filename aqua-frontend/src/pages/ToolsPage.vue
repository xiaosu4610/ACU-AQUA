<script setup lang="ts">
/* 工具箱首页（旧版 #/tools 工具面板平移）：按 registry 分类分组，组内保持注册顺序 */
import { computed } from 'vue'
import { TOOL_REGISTRY, TOOL_CATEGORY_LABEL, type ToolMeta } from '@/tools/registry'
import { TOOL_ICONS, TOOL_TAGS, TOOL_CARD_TAGS } from '@/tools/meta'

const groups = computed(() => {
  const order: ToolMeta['category'][] = []
  const map = {} as Record<ToolMeta['category'], ToolMeta[]>
  Object.values(TOOL_REGISTRY).forEach(t => {
    if (!map[t.category]) { map[t.category] = []; order.push(t.category) }
    map[t.category].push(t)
  })
  return order.map(c => ({ cat: c, label: TOOL_CATEGORY_LABEL[c], list: map[c] }))
})
</script>

<template>
  <section class="route-page">
    <div class="models-page-head">
      <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg></span>体验中心</h1>
      <p>免登录直接对话、玩 AI 对弈游戏、用免费实用工具——所有体验与真实 API 行为完全一致。</p>
    </div>
    <nav class="hub-subnav" aria-label="体验中心子导航">
      <router-link class="hub-tab" to="/playground" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg></span>AI 对话</router-link>
      <router-link class="hub-tab" to="/treehole" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3c-4.4 0-8 3.6-8 8 0 4.4 3.6 8 8 8h.5c.3 0 .5.2.5.5V21l3.8-2.6C19.9 17 20.5 14 20.5 11 20.5 6.6 16.4 3 12 3z"/><circle cx="8.5" cy="10.5" r=".8" fill="currentColor"/><circle cx="12" cy="10.5" r=".8" fill="currentColor"/><circle cx="15.5" cy="10.5" r=".8" fill="currentColor"/></svg></span>树洞</router-link>
      <router-link class="hub-tab" to="/tools" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg></span>工具箱</router-link>
      <router-link class="hub-tab" to="/prompts" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z"/></svg></span>提示词工坊</router-link>
    </nav>

    <div class="tools-page">
      <p class="hub-desc">基于 AQUA 网关构建的对弈游戏与实用工具——每个工具既是网页应用，也是网关 API，兼容性拉满。</p>

      <template v-for="g in groups" :key="g.cat">
        <div class="tools-sec"><span class="tools-sec-t">{{ g.label }}</span><span class="tools-sec-n">{{ g.list.length }} 个</span></div>
        <div class="tools-grid">
          <router-link v-for="t in g.list" :key="t.id" class="tool-card" :to="'/tools/' + t.id">
            <span class="tool-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS[t.id]"></svg></span>
            <b>{{ t.title }}</b>
            <span class="tool-desc">{{ t.desc }}</span>
            <span class="tool-tag">{{ TOOL_CARD_TAGS[t.id] || TOOL_TAGS[t.id] }}</span>
          </router-link>
        </div>
      </template>

      <div class="tools-sec"><span class="tools-sec-t">工具 API</span><span class="tools-sec-n">不只 AI，工具也能调</span></div>
      <div class="tools-api-note">
        <p>上述所有工具的能力同时以 REST API 形式开放在网关上——例如 <code>POST /v1/tools/text-stats</code>、<code>POST /v1/tools/hash</code>、<code>POST /v1/tools/shorten</code>、<code>POST /v1/tools/webhook</code>。鉴权方式与主 API 相同，详见 <router-link to="/api" style="color:var(--accent);">API 文档</router-link>。我们不只提供 AI API，也提供工具 API。</p>
      </div>
    </div>
  </section>
</template>
