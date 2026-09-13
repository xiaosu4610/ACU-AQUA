<script setup lang="ts">
import { ref } from 'vue'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'

interface Rgb { r: number; g: number; b: number }
interface Hsl { h: number; s: number; l: number }

function rgbToHsl(r: number, g: number, b: number): Hsl {
  const rn = r / 255, gn = g / 255, bn = b / 255
  const max = Math.max(rn, gn, bn), min = Math.min(rn, gn, bn)
  let h = 0, s = 0
  const l = (max + min) / 2
  if (max !== min) {
    const d = max - min
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min)
    if (max === rn) h = (gn - bn) / d + (gn < bn ? 6 : 0)
    else if (max === gn) h = (bn - rn) / d + 2
    else h = (rn - gn) / d + 4
    h *= 60
  }
  return { h: Math.round(h), s: Math.round(s * 100), l: Math.round(l * 100) }
}
function hslToRgb(hDeg: number, sPct: number, lPct: number): Rgb {
  const h = ((hDeg % 360) + 360) % 360 / 360, s = sPct / 100, l = lPct / 100
  if (s === 0) { const v = Math.round(l * 255); return { r: v, g: v, b: v } }
  const q = l < 0.5 ? l * (1 + s) : l + s - l * s
  const p = 2 * l - q
  const f = (t: number) => {
    if (t < 0) t += 1
    if (t > 1) t -= 1
    if (t < 1 / 6) return p + (q - p) * 6 * t
    if (t < 1 / 2) return q
    if (t < 2 / 3) return p + (q - p) * (2 / 3 - t) * 6
    return p
  }
  return { r: Math.round(f(h + 1 / 3) * 255), g: Math.round(f(h) * 255), b: Math.round(f(h - 1 / 3) * 255) }
}
function pack(r: number, g: number, b: number) {
  const hex = '#' + [r, g, b].map(v => v.toString(16).padStart(2, '0')).join('')
  const rgbStr = 'rgb(' + r + ', ' + g + ', ' + b + ')'
  const hsl = rgbToHsl(r, g, b)
  const hslStr = 'hsl(' + hsl.h + ', ' + hsl.s + '%, ' + hsl.l + '%)'
  return { hex, rgbStr, hslStr }
}
/* 解析 #rgb / #rrggbb / rgb() / hsl()，统一折算到 rgb 后输出三格式 */
function parseColor(raw: string) {
  const s = raw.trim().toLowerCase()
  let m = s.match(/^#?([0-9a-f]{3})$/)
  if (m) {
    const r = parseInt(m[1][0] + m[1][0], 16), g = parseInt(m[1][1] + m[1][1], 16), b = parseInt(m[1][2] + m[1][2], 16)
    return pack(r, g, b)
  }
  m = s.match(/^#?([0-9a-f]{6})$/)
  if (m) {
    const r = parseInt(m[1].slice(0, 2), 16), g = parseInt(m[1].slice(2, 4), 16), b = parseInt(m[1].slice(4, 6), 16)
    return pack(r, g, b)
  }
  m = s.match(/^rgba?\(\s*(\d{1,3})\s*[,，]\s*(\d{1,3})\s*[,，]\s*(\d{1,3})/)
  if (m) {
    const r = +m[1], g = +m[2], b = +m[3]
    if (r > 255 || g > 255 || b > 255) return null
    return pack(r, g, b)
  }
  m = s.match(/^hsla?\(\s*(\d{1,3})\s*[,，]\s*(\d{1,3})%\s*[,，]\s*(\d{1,3})%/)
  if (m) {
    const rgb = hslToRgb(+m[1], +m[2], +m[3])
    return pack(rgb.r, rgb.g, rgb.b)
  }
  return null
}

const input = ref('')
const msg = ref('')
const result = ref<{ hex: string; rgbStr: string; hslStr: string } | null>(null)

function run() {
  msg.value = ''
  result.value = null
  const v = input.value.trim()
  if (!v) { msg.value = '请先输入颜色值。'; return }
  const parsed = parseColor(v)
  if (!parsed) { msg.value = '转换失败：无法识别的颜色格式'; return }
  result.value = parsed
}
</script>

<template>
  <p class="tool-intro">HEX / RGB / HSL 三格式互转——输入 <code>#3b82f6</code> 或 <code>rgb(59,130,246)</code> 均可。</p>
  <div class="grid2">
    <div class="card out-pane">
      <div class="field">
        <label>颜色值（HEX / RGB / HSL）</label>
        <input v-model="input" class="input mono" placeholder="如 #3b82f6 或 rgb(59,130,246)" @keydown.enter="run">
      </div>
      <button class="btn primary" @click="run"><svg class="bic" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.color"></svg>转换</button>
    </div>
    <div class="card out-pane">
      <div v-if="msg" class="out-empty"><AqIcon name="alert" :size="24" /><span>{{ msg }}</span></div>
      <template v-else-if="result">
        <div class="swatch" :style="{ background: result.hex }"></div>
        <div class="tool-kv mono">
          <div><span style="user-select: all; color: var(--txt0);">{{ result.hex }}</span><b><CopyBtn :text="result.hex" size="xs" /></b></div>
          <div><span style="user-select: all; color: var(--txt0);">{{ result.rgbStr }}</span><b><CopyBtn :text="result.rgbStr" size="xs" /></b></div>
          <div><span style="user-select: all; color: var(--txt0);">{{ result.hslStr }}</span><b><CopyBtn :text="result.hslStr" size="xs" /></b></div>
        </div>
      </template>
      <div v-else class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.color"></svg><span>结果将显示在这里</span></div>
    </div>
  </div>
</template>

<style scoped>
.swatch { width: 100%; height: 64px; border-radius: var(--r-md); border: 1px solid var(--line); }
.ticon { width: 26px; height: 26px; opacity: .55; }
.bic { width: 14px; height: 14px; }
</style>
