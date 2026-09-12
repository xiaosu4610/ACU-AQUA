<script setup lang="ts">
/* 工具详情壳（旧版 toolShell 平移）：返回工具箱 + 工具头（图标/标题/端点徽标）+ 动态加载工具组件 */
import { computed, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import { TOOL_REGISTRY } from '@/tools/registry'
import { TOOL_ICONS, TOOL_TAGS } from '@/tools/meta'
import AuthBanner from '@/components/AuthBanner.vue'

const route = useRoute()
const meta = computed(() => TOOL_REGISTRY[String(route.params.name || '')])

const comp = shallowRef<any>(null)
const state = shallowRef<'loading' | 'ok' | 'fail'>('loading')

watch(() => route.params.name, async () => {
  comp.value = null
  if (!meta.value) return // 未注册的工具 → 空态文案
  state.value = 'loading'
  try {
    const m = await meta.value.load()
    comp.value = m && m.default ? m.default : m // 动态 import 返回 module，取 .default 组件
    state.value = 'ok'
  } catch {
    state.value = 'fail'
  }
}, { immediate: true })
</script>

<template>
  <section class="route-page">
    <div class="tools-page">
      <template v-if="meta">
        <router-link class="back-link" to="/tools"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M19 12H5M12 19l-7-7 7-7"/></svg>返回工具箱</router-link>
        <AuthBanner />
        <div class="tool-page-card">
          <div class="tool-head">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="width:22px;height:22px;vertical-align:-4px;color:var(--accent);" v-html="TOOL_ICONS[meta.id]"></svg>
            <h2>{{ meta.title }}</h2>
            <span class="tool-tag">{{ TOOL_TAGS[meta.id] }}</span>
          </div>
          <div v-if="state === 'loading'" class="tool-loading">加载中…</div>
          <div v-else-if="state === 'fail'" class="tool-empty">工具加载失败，请刷新重试</div>
          <component :is="comp" v-else-if="comp" />
        </div>
      </template>
      <div v-else class="model-empty">该工具正在建设中，敬请期待</div>
    </div>
  </section>
</template>
