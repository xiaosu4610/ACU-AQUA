<script setup lang="ts">
/* 工具详情壳（/tools/:name）：返回工具箱 + 工具头（图标/标题/端点徽标）+ 动态加载工具组件
 * 共享工具布局 DSL（.tool-intro / .tool-bar / .tool-kv / .out-pane…）经 :deep() 透传给子组件 */
import { computed, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import { TOOL_REGISTRY } from '@/tools/registry'
import { TOOL_ICONS, TOOL_TAGS } from '@/tools/meta'
import { isLoggedIn } from '@/composables/useAuth'
import AqIcon from '@/components/AqIcon.vue'

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
  <div class="wrap">
    <template v-if="meta">
      <div class="page-head fade-up">
        <div>
          <router-link class="btn ghost sm mb8" to="/tools" style="margin-left: -12px;">
            <AqIcon name="arrow-right" :size="14" style="transform: rotate(180deg);" />返回工具箱
          </router-link>
          <h1>
            <svg class="tool-hero" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS[meta.id]"></svg>
            {{ meta.title }}
          </h1>
          <div class="sub">{{ meta.desc }}</div>
        </div>
        <div class="ops"><span class="tag mono">{{ TOOL_TAGS[meta.id] }}</span></div>
      </div>

      <div v-if="!isLoggedIn()" class="banner warn mb12 fade-up">
        <AqIcon name="info" :size="15" />
        <span>本功能需要登录后使用——注册免费，控制台一键创建 API 密钥。</span>
        <router-link to="/login" class="btn sm primary" style="margin-left: auto;">登录 / 注册</router-link>
      </div>

      <div class="tool-body fade-up">
        <template v-if="state === 'loading'">
          <div class="skeleton" style="width: 62%; min-height: 14px;"></div>
          <div class="skeleton mt12" style="width: 100%; min-height: 120px;"></div>
        </template>
        <div v-else-if="state === 'fail'" class="empty">
          <div class="big"><AqIcon name="alert" :size="34" /></div>
          <b>工具加载失败</b>
          <div class="dim">请刷新重试</div>
        </div>
        <component :is="comp" v-else-if="comp" />
      </div>
    </template>

    <div v-else class="empty fade-up" style="margin-top: 70px;">
      <div class="big"><AqIcon name="puzzle" :size="40" /></div>
      <b>该工具正在建设中</b>
      <div class="dim">敬请期待</div>
      <router-link class="btn primary mt12" to="/tools">返回工具箱</router-link>
    </div>
  </div>
</template>

<style scoped>
.tool-hero { width: 24px; height: 24px; color: var(--acc); flex: none; }

/* ---- 工具组件共享布局 DSL（:deep 透传给动态加载的 src/tools 子组件） ---- */
.tool-body :deep(.tool-intro) { color: var(--txt2); font-size: 13.5px; margin: 0 0 14px; max-width: 78ch; }
.tool-body :deep(.tool-intro code) { color: var(--acc); }
.tool-body :deep(.tool-bar) { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin: 0 0 14px; }
.tool-body :deep(.tool-bar > span) { color: var(--txt2); font-size: 12.5px; font-weight: 600; flex: none; }
.tool-body :deep(.tool-bar .select) { width: auto; min-width: 150px; flex: none; }
.tool-body :deep(.tool-flex1) { flex: 1 1 200px; min-width: 0; }
.tool-body :deep(.tool-status) { color: var(--acc); font-size: 12.5px; font-weight: 600; }
.tool-body :deep(.tool-prose) { white-space: pre-wrap; word-break: break-word; font-size: 13.5px; line-height: 1.75; color: var(--txt1); }
.tool-body :deep(.out-pane) { display: flex; flex-direction: column; gap: 10px; min-width: 0; }
.tool-body :deep(.out-empty) { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; min-height: 180px; padding: 24px 12px; color: var(--txt3); text-align: center; font-size: 13px; }
.tool-body :deep(.tool-kv) { border: 1px solid var(--line); border-radius: var(--r-md); overflow: hidden; background: var(--bg1); }
.tool-body :deep(.tool-kv > div) { display: flex; justify-content: space-between; align-items: baseline; gap: 14px; padding: 8px 13px; border-bottom: 1px solid var(--line); font-size: 13px; }
.tool-body :deep(.tool-kv > div:last-child) { border-bottom: 0; }
.tool-body :deep(.tool-kv > div > span) { color: var(--txt2); flex: none; }
.tool-body :deep(.tool-kv > div > b) { color: var(--txt0); font-weight: 600; text-align: right; word-break: break-all; min-width: 0; }
</style>
