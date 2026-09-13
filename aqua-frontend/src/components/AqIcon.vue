<script setup lang="ts">
// AqIcon —— 站点统一手绘 SVG 图标（feather 风格，24x24 stroke）
// 全站 emoji 图标的替代品；颜色随 currentColor，尺寸由 size 控制
import { computed } from 'vue'

const props = withDefaults(defineProps<{ name: string; size?: number }>(), { size: 18 })

// 每个图标均为手绘 path 集合（viewBox 0 0 24 24，stroke=currentColor）
const ICONS: Record<string, string> = {
  // —— 控制台 ——
  key: '<circle cx="7.5" cy="15.5" r="4.5"/><path d="M11 12 20 3"/><path d="M16 7l3 3"/><path d="M13 10l2.5 2.5"/>',
  coin: '<circle cx="12" cy="12" r="9"/><path d="M12 7v10M9.5 9.5c0-1 1.1-1.7 2.5-1.7s2.5.7 2.5 1.7c0 2.6-5 1.4-5 4 0 1 1.1 1.7 2.5 1.7s2.5-.7 2.5-1.7"/>',
  alert: '<path d="M12 3 2.5 20h19L12 3z"/><path d="M12 10v5"/><circle cx="12" cy="18" r="0.6" fill="currentColor" stroke="none"/>',
  chart: '<path d="M4 4v16h16"/><path d="M8 16v-5"/><path d="M13 16V8"/><path d="M18 16v-3"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3.5 2"/>',
  lock: '<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/><circle cx="12" cy="15.5" r="1.2" fill="currentColor" stroke="none"/>',
  puzzle: '<path d="M9 4h3v2.2a1.8 1.8 0 1 0 3.6 0V4H19v5h-2.2a1.8 1.8 0 1 0 0 3.6H19V19h-5v-2.2a1.8 1.8 0 1 0-3.6 0V19H6.5A2.5 2.5 0 0 1 4 16.5V13h2.2a1.8 1.8 0 1 0 0-3.6H4V6.5A2.5 2.5 0 0 1 6.5 4H9z"/>',
  // —— 状态 / 用量 ——
  activity: '<path d="M3 12h4l3-8 4 16 3-8h4"/>',
  trend: '<path d="M3 17l6-6 4 4 8-8"/><path d="M15 7h6v6"/>',
  // —— 竞技场 ——
  'hand-left': '<path d="M9 12V5.5a1.5 1.5 0 0 1 3 0V11"/><path d="M12 11V9.5a1.5 1.5 0 0 1 3 0V12"/><path d="M15 12v-1a1.5 1.5 0 0 1 3 0v4.5A5.5 5.5 0 0 1 12.5 21h-1A5.5 5.5 0 0 1 6 15.5V13a1.5 1.5 0 0 1 3 0"/>',
  'hand-right': '<path d="M15 12V5.5a1.5 1.5 0 0 0-3 0V11"/><path d="M12 11V9.5a1.5 1.5 0 0 0-3 0V12"/><path d="M9 12v-1a1.5 1.5 0 0 0-3 0v4.5A5.5 5.5 0 0 0 11.5 21h1A5.5 5.5 0 0 0 18 15.5V13a1.5 1.5 0 0 0-3 0"/>',
  handshake: '<path d="M3 7l4-2 5 2 5-2 4 2v7l-4 2-5-2-5 2-4-2V7z"/><path d="M12 7v3"/><path d="M7 12l3 3"/><path d="M17 12l-3 3"/>',
  vote: '<path d="M5 3h14l2 6H3l2-6z"/><path d="M4 9h16v11a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V9z"/><path d="M9 13.5l2 2 4-4"/>',
  trophy: '<path d="M7 4h10v5a5 5 0 0 1-10 0V4z"/><path d="M7 5H4a3 3 0 0 0 3 4"/><path d="M17 5h3a3 3 0 0 1-3 4"/><path d="M12 14v3"/><path d="M8 21h8"/><path d="M10 17h4"/>',
  // —— 通用 ——
  bulb: '<path d="M9 18h6"/><path d="M10 21h4"/><path d="M12 3a6 6 0 0 1 3.6 10.8c-.9.7-1.6 1.3-1.6 2.2h-4c0-.9-.7-1.5-1.6-2.2A6 6 0 0 1 12 3z"/>',
  star: '<path d="M12 3l2.7 5.6 6.1.8-4.5 4.2 1.1 6-5.4-3-5.4 3 1.1-6L3.2 9.4l6.1-.8L12 3z"/>',
  bolt: '<path d="M13 2 4.5 13.5H11L9.5 22 19 10h-6.5L13 2z"/>',
  check: '<path d="M4 12.5l5 5L20 6.5"/>',
  cross: '<path d="M6 6l12 12"/><path d="M18 6 6 18"/>',
  wave: '<path d="M3 16c2.5 0 2.5-2 5-2s2.5 2 5 2 2.5-2 5-2 2.5 2 3 2"/><path d="M3 20c2.5 0 2.5-2 5-2s2.5 2 5 2 2.5-2 5-2 2.5 2 3 2"/><path d="M12 3c3 2.5 5 4.5 5 7.5a5 5 0 0 1-10 0C7 7.5 9 5.5 12 3z"/>',
  spark: '<path d="M12 3v4"/><path d="M12 17v4"/><path d="M3 12h4"/><path d="M17 12h4"/><path d="M5.6 5.6l2.8 2.8"/><path d="M15.6 15.6l2.8 2.8"/><path d="M18.4 5.6l-2.8 2.8"/><path d="M8.4 15.6l-2.8 2.8"/>',
  'arrow-right': '<path d="M4 12h15"/><path d="M13 6l6 6-6 6"/>',
  heart: '<path d="M12 20.5S4 15 4 9.5A4.5 4.5 0 0 1 12 6a4.5 4.5 0 0 1 8 3.5c0 5.5-8 11-8 11z"/>',
  copy: '<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  shield: '<path d="M12 3l8 3v5c0 5-3.5 8.5-8 10-4.5-1.5-8-5-8-10V6l8-3z"/><path d="M9 12l2 2 4-4"/>',
  user: '<circle cx="12" cy="8" r="4"/><path d="M4 21c0-4 3.5-6.5 8-6.5S20 17 20 21"/>',
  mail: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 7l9 6 9-6"/>',
  eye: '<path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12z"/><circle cx="12" cy="12" r="2.5"/>',
  trash: '<path d="M4 7h16"/><path d="M9 7V4h6v3"/><path d="M6 7l1 13a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1l1-13"/><path d="M10 11v6"/><path d="M14 11v6"/>',
  refresh: '<path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 5v6h-6"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5"/><circle cx="12" cy="8" r="0.6" fill="currentColor" stroke="none"/>',
  // —— 控制台侧边栏 ——
  menu: '<path d="M4 6h16"/><path d="M4 12h16"/><path d="M4 18h16"/>',
  layout: '<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18"/><path d="M9 21V9"/>',
  list: '<path d="M8 6h13"/><path d="M8 12h13"/><path d="M8 18h13"/><path d="M3 6h.01"/><path d="M3 12h.01"/><path d="M3 18h.01"/>',
  settings: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.01a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h.01a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.01a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>',
  // —— 账号检查 ——
  stethoscope: '<path d="M6 4v6a5 5 0 0 0 10 0V4"/><path d="M6 4h2"/><path d="M14 4h2"/><path d="M11 15v1.5A4.5 4.5 0 0 0 15.5 21h0A4.5 4.5 0 0 0 20 16.5V15"/><circle cx="20" cy="13.5" r="1.8"/>',
  gauge: '<path d="M4 18a9 9 0 1 1 16 0"/><path d="M12 13l4-4"/><circle cx="12" cy="14" r="1.3" fill="currentColor" stroke="none"/>',
  // —— 游戏棋子 ——
  'x-mark': '<path d="M5 5l14 14"/><path d="M19 5 5 19"/>',
  'circle-mark': '<circle cx="12" cy="12" r="8"/>',
}

const frag = computed(() => ICONS[props.name] ?? ICONS.info)
</script>

<template>
  <svg
    :width="size" :height="size" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"
    class="aq-icon" v-html="frag"
  />
</template>

<style scoped>
.aq-icon { flex: none; vertical-align: -0.15em; }
</style>
