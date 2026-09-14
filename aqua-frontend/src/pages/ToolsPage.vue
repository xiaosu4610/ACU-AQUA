<script setup lang="ts">
/* 工具箱：分类分区（.grp 标题 + .grid3 卡片）+ 顶部搜索过滤
 * 卡片数据来自 src/tools/registry.ts 内置清单（旧版条目照抄） */
import { computed, ref } from 'vue'
import { TOOL_REGISTRY, TOOL_CATEGORY_LABEL, toolCount, type ToolMeta } from '@/tools/registry'
import { TOOL_ICONS, TOOL_TAGS, TOOL_CARD_TAGS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'

const q = ref('')

const CAT_ICON: Record<ToolMeta['category'], string> = {
  text: 'book', dev: 'box', net: 'grid', ai: 'spark', game: 'gamepad',
}

const groups = computed(() => {
  const kw = q.value.trim().toLowerCase()
  const order: ToolMeta['category'][] = []
  const map = {} as Record<ToolMeta['category'], ToolMeta[]>
  Object.values(TOOL_REGISTRY).forEach(t => {
    if (kw) {
      const hay = (t.title + ' ' + t.desc + ' ' + t.id + ' ' + (TOOL_CARD_TAGS[t.id] || TOOL_TAGS[t.id] || '')).toLowerCase()
      if (!hay.includes(kw)) return
    }
    if (!map[t.category]) { map[t.category] = []; order.push(t.category) }
    map[t.category].push(t)
  })
  return order.map(c => ({ cat: c, label: TOOL_CATEGORY_LABEL[c], list: map[c] }))
})
</script>

<template>
  <div class="wrap">
    <div class="page-head fade-up">
      <div>
        <h1><AqIcon name="box" />工具箱</h1>
        <div class="sub">基于 AQUA api 网关构建的对弈游戏与实用工具——每个工具既是网页应用，也是网关 API，兼容性拉满。共 {{ toolCount() }} 个。</div>
      </div>
      <div class="ops">
        <input v-model="q" class="input" style="width: 250px;" placeholder="搜索工具名称 / 功能 / 接口路径…">
      </div>
    </div>

    <div class="fade-up">
      <template v-for="g in groups" :key="g.cat">
        <div class="grp-head">
          <AqIcon :name="CAT_ICON[g.cat]" :size="15" />
          {{ g.label }}
          <span class="grp-n">{{ g.list.length }} 个</span>
        </div>
        <div class="grid3 mb16">
          <router-link v-for="t in g.list" :key="t.id" class="card hoverable tool-card" :to="'/tools/' + t.id">
            <span class="tool-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS[t.id]"></svg></span>
            <b>{{ t.title }}</b>
            <span class="dim tool-desc">{{ t.desc }}</span>
            <span class="tag mono">{{ TOOL_CARD_TAGS[t.id] || TOOL_TAGS[t.id] }}</span>
          </router-link>
        </div>
      </template>

      <div v-if="!groups.length" class="empty">
        <div class="big"><AqIcon name="puzzle" :size="34" /></div>
        <b>没有匹配「{{ q }}」的工具</b>
        <div class="dim">换个关键词试试，比如「翻译」「棋」「短链」</div>
      </div>

      <div class="card mt8">
        <div class="card-h"><AqIcon name="bolt" :size="15" />工具 API</div>
        <p class="dim mt8">
          上述所有工具的能力同时以 REST API 形式开放在网关上——例如
          <code>POST /v1/tools/text-stats</code>、<code>POST /v1/tools/hash</code>、<code>POST /v1/tools/shorten</code>、<code>POST /v1/tools/webhook</code>。
          鉴权方式与主 API 相同，详见 <router-link to="/api">API 文档</router-link>。我们不只提供 AI API，也提供工具 API。
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.grp-head { display: flex; align-items: center; gap: 8px; margin: 22px 0 12px; font-size: 15px; font-weight: 700; color: var(--txt0); }
.grp-head .grp-n { font-size: 12px; font-weight: 600; color: var(--txt3); }
.tool-card { display: flex; flex-direction: column; align-items: flex-start; gap: 8px; color: var(--txt1); }
.tool-ic { width: 38px; height: 38px; border-radius: 10px; display: flex; align-items: center; justify-content: center; background: var(--acc-soft); color: var(--acc); flex: none; }
.tool-ic svg { width: 20px; height: 20px; }
.tool-desc { line-height: 1.55; }
.tool-card .tag { margin-top: auto; }
</style>
