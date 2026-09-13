<script setup lang="ts">
/* 模型中心 · 模型列表（自旧版 page-modelhub hub-pane:models 平移）
 * 旧版对应逻辑：loadModels / applyFilter / render / smartSort / healthTag / updateModelsNotice
 * v2026.09.16-rc7：收费页改版——倍率横幅（0.2× 促销 → 0.5× 恢复）+ 模型卡片内嵌实时状态
 * （时延/生成速度/成功率，20 秒轮询）+ 搜索/筛选工具栏，独立「实时状态」子页移除。 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import CopyBtn from '@/components/CopyBtn.vue'
import { useModels, type ModelRow } from '@/composables/useModels'
import { apiJson } from '@/composables/useApi'
import { dsMaintenance, hideTag, platformLabel, typeLabel } from '@/composables/modelMeta'
import CapabilitiesPage from './CapabilitiesPage.vue'
import AqIcon from '@/components/AqIcon.vue'

/* 视图切换：free 免费模型 / paid 收费模型 / cap 能力总览（URL ?view= 可直达分享） */
const route = useRoute()
const router = useRouter()
const view = ref(route.query.view === 'cap' ? 'cap' : route.query.view === 'paid' ? 'paid' : 'free')
watch(() => route.query.view, v => { view.value = v === 'cap' ? 'cap' : v === 'paid' ? 'paid' : 'free' })
function setView(v: 'free' | 'paid' | 'cap') {
  view.value = v
  router.replace({ query: v === 'free' ? {} : { view: v } })
}

const { models, loading, error, loadedAt, load } = useModels()
onMounted(() => {
  load()
  // 旧版每 60 秒自动刷新（模型列表与额度状态自动更新）
  refreshTimer = window.setInterval(() => load(true), 60000)
  // 实时状态：卡片内嵌展示，20 秒轮询持续刷新（页面级，不依赖子视图）
  liveTimer = window.setInterval(loadLive, 20000)
  loadLive()
  loadRate()
})
let refreshTimer = 0
let liveTimer = 0
let tickTimer = 0
onUnmounted(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
  if (liveTimer) window.clearInterval(liveTimer)
  if (tickTimer) window.clearInterval(tickTimer)
})
function retry() { load(true) }

/* ---- 实时状态：/v1/models/status 最近 200 次请求推断（时延/速度/成功率，剔除用户参数错），20 秒自动刷新 ---- */
type LiveRow = { model: string; samples: number; ok: number; ok_rate: number; status: string; avg_latency_ms?: number; avg_tps?: number; last_ts: number }
const liveRows = ref<LiveRow[]>([])
const liveTs = ref(0)
const liveLoading = ref(false)
async function loadLive() {
  liveLoading.value = true
  try {
    // 走统一 API 基址（api.ltzy.top）——相对路径 fetch 在前端域名下会被 nginx SPA 接管
    const j = await apiJson<{ data: LiveRow[]; generated_ts: number }>('/models/status')
    liveRows.value = (j.data || []).filter((x: LiveRow) => /^(aqua|acu)\//.test(x.model))
    liveTs.value = j.generated_ts || 0
  } catch { /* 静默：下一轮自动重试 */ }
  liveLoading.value = false
}
const liveMap = computed(() => { const m: Record<string, LiveRow> = {}; for (const r of liveRows.value) m[r.model] = r; return m })
function fmtLat(ms?: number): string {
  if (!ms) return '--'
  return ms >= 1000 ? (ms / 1000).toFixed(2) + ' s' : Math.round(ms) + ' ms'
}
function fmtRate(v: number): string { return (v * 100).toFixed(1) + '%' }
function fmtAgo(ts?: number): string {
  if (!ts) return '--'
  const d = Math.max(0, Math.floor(Date.now() / 1000 - ts))
  if (d < 60) return d + ' 秒前'
  if (d < 3600) return Math.floor(d / 60) + ' 分钟前'
  return Math.floor(d / 3600) + ' 小时前'
}
/* 卡片实时状态灯：great 发光绿 / ok 绿 / degraded 黄 / down 红 / 无数据灰（待命中） */
function liveStatusOf(id: string): { key: 'great' | 'ok' | 'degraded' | 'down' | 'idle'; text: string } {
  const live = liveMap.value[id]
  if (!live) return { key: 'idle', text: '待命中' }
  if (live.status === 'great') return { key: 'great', text: '状态极佳' }
  if (live.status === 'ok') return { key: 'ok', text: '运行正常' }
  if (live.status === 'degraded') return { key: 'degraded', text: '部分异常' }
  return { key: 'down', text: '故障' }
}

/* ---- 计费倍率横幅（/v1/meta 配置下发；促销结束 promo_ends_at 过期后横幅自动隐藏） ---- */
const ratePromo = ref('')
const rateNormal = ref('')
async function loadRate() {
  try {
    const j = await apiJson<{ rate_promo?: string; rate_normal?: string }>('/meta')
    ratePromo.value = j.rate_promo || ''
    rateNormal.value = j.rate_normal || ''
  } catch { /* 静默：无配置不展示横幅 */ }
}
const nowTick = ref(Math.floor(Date.now() / 1000))
tickTimer = window.setInterval(() => { nowTick.value = Math.floor(Date.now() / 1000) }, 1000)
const promoEndsAt = computed(() => {
  let t = 0
  for (const m of paidModels.value) { const e = (m as any).promoEndsAt || 0; if (e > t) t = e }
  return t
})
const rateBannerVisible = computed(() => !!ratePromo.value && !!rateNormal.value && promoEndsAt.value > nowTick.value)
function fmtCountdown(sec: number): string {
  if (sec <= 0) return ''
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60
  if (d > 0) return d + ' 天 ' + h + ' 小时'
  if (h > 0) return h + ' 小时 ' + m + ' 分'
  return m + ' 分 ' + (s < 10 ? '0' : '') + s + ' 秒'
}
const promoLeftStr = computed(() => fmtCountdown(promoEndsAt.value - nowTick.value))
const promoEndsStr = computed(() => new Date(promoEndsAt.value * 1000).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }))

/* ---- 筛选状态（旧 curPlatform / curType / 搜索词） ---- */
const curPlatform = ref('all')
const curType = ref('all')
const q = ref('')

/* ---- 智能排序：国产/大参数/高智能模型排前（旧 smartSort 平移） ---- */
function modelPriority(id: string): number {
  const l = id.toLowerCase()
  // 官方自营收费专线最前（统一 aqua/ 前缀；旧 acu/ 兼容）
  if (/^(aqua|acu)\//.test(l)) return -1
  // 国产模型最前（含 Kimi/豆包/月之暗面 等）
  if (/deepseek|qwen|qwq|chatglm|thudm|baichuan|01-ai|yi-large|zhipu|glm|bigmodel|kimi|moonshot|doubao|moonshotai/.test(l)) return 0
  // Nvidia 旗舰 Nemotron
  if (/nemotron|nvidia.*llama-3\.[13]/.test(l)) return 1
  // Meta Llama 旗舰
  if (/meta\/llama-3\.[13]|meta\/llama-4/.test(l)) return 2
  // Mistral 旗舰
  if (/mistral-large|mistral-medium|mixtral/.test(l)) return 3
  // Google Gemma
  if (/gemma/.test(l)) return 4
  // Microsoft Phi
  if (/phi-4|phi-3\.5/.test(l)) return 5
  if (/phi-3/.test(l)) return 6
  // 其他
  return 8
}
/* 旗舰/大体量版本加分：同组内把热门旗舰版本往前排 */
function hotFlag(id: string): number {
  const l = id.toLowerCase()
  if (/v4|k3|k2\.6|k2|r1|v3|max|ultra|plus|large|turbo|pro|premium|72b|70b|405b|671b|32b|27b|236b/.test(l)) return 1
  return 0
}
function smartSort(a: string, b: string): number {
  const pa = modelPriority(a), pb = modelPriority(b)
  if (pa !== pb) return pa - pb
  // 同组内：旗舰版本优先
  const ha = hotFlag(a), hb = hotFlag(b)
  if (ha !== hb) return hb - ha
  // 同组内按参数量降序（粗略提取数字）
  const na = Number(a.match(/(\d+)b/i)?.[1] || 0)
  const nb = Number(b.match(/(\d+)b/i)?.[1] || 0)
  if (na !== nb) return nb - na
  return a.localeCompare(b)
}

/* 排序：auto（智能路由）置顶，其余按 smartSort（与共享 store 的 auto 置顶约定一致） */
const orderedModels = computed(() => {
  const arr = models.value.slice()
  arr.sort((a, b) => {
    const aa = a.id.toLowerCase() === 'auto' ? 0 : 1
    const bb = b.id.toLowerCase() === 'auto' ? 0 : 1
    if (aa !== bb) return aa - bb
    return smartSort(a.id, b.id)
  })
  return arr
})

/* ---- 状态/健康徽标（旧 render + healthTag 平移） ---- */
function statusOf(m: ModelRow) {
  if (m.id.toLowerCase() === 'auto') {
    return { cls: 'm-acu', text: '智能路由 · 快与稳优先', title: '每次请求实时选择当前最快最稳的模型，不保证命中同一个' }
  }
  if (dsMaintenance(m.id)) {
    return { cls: 'm-status-exhausted', text: '服务暂停 · 维护中', title: '官方自营通道维护中，已暂停服务' }
  }
  if (m.status === 'exhausted') {
    return { cls: 'm-status-exhausted', text: '额度已耗尽', title: m.status_msg || '今日免费额度已用尽' }
  }
  if (m.status === 'unavailable' || m.status === 'maintenance') {
    return {
      cls: 'm-status-unavailable',
      text: m.status === 'maintenance' ? '服务暂停 · 维护中' : '暂时不可用',
      title: m.status_msg || (m.status === 'maintenance' ? '官方自营通道维护中，已暂停服务' : '该模型暂时不可用'),
    }
  }
  return null
}
function isExhausted(m: ModelRow): boolean {
  if (m.id.toLowerCase() === 'auto') return false
  return dsMaintenance(m.id) || m.status === 'exhausted' || m.status === 'unavailable' || m.status === 'maintenance'
}
/* 健康评分徽标：根据近100次调用自动评分（后端计算，0-100） */
function healthOf(m: ModelRow) {
  const h = m.health
  if (!h || !h.total) return null
  const score = h.score != null ? h.score | 0 : 0
  let cls = 'h-bad'
  if (score >= 90) cls = 'h-excellent'
  else if (score >= 70) cls = 'h-good'
  else if (score >= 50) cls = 'h-warn'
  const rate = Math.round(((h.ok || 0) / h.total) * 100)
  const lat = h.avg_latency_ms != null ? (h.avg_latency_ms / 1000).toFixed(1) + 's' : '-'
  return { score, cls, tip: `近 ${h.total} 次调用的健康评分：${score}/100（成功率 ${rate}%，平均延迟 ${lat}）` }
}

/* ---- 过滤 + 渲染行（免费模型页：收费模型已隔离到专属页，此处只展示免费模型；acu/ 众筹模型提升到专区大卡片，不进普通小卡片流） ---- */
const viewRows = computed(() => {
  const kw = q.value.toLowerCase().trim()
  return orderedModels.value
    .filter(m => {
      if (m.paid) return false
      if (isCrowd(m.id)) return false
      if (curPlatform.value !== 'all' && m.platform !== curPlatform.value) return false
      if (curType.value !== 'all' && m.type !== curType.value) return false
      return kw ? m.id.toLowerCase().indexOf(kw) !== -1 : true
    })
    .map(m => ({ row: m, st: statusOf(m), exhausted: isExhausted(m), health: healthOf(m) }))
})

/** 众筹专区：acu/ 前缀大卡片（官方自营通道，官方原价扣站点额度，充值翻倍） */
const crowdModels = computed(() => orderedModels.value
  .filter(m => isCrowd(m.id))
  .map(m => ({
    id: m.id,
    inPrice: m.in_price ?? 0,
    cachePrice: m.cache_price ?? 0,
    outPrice: m.out_price ?? 0,
    floor: m.floor_micro ?? 0,
    health: healthOf(m),
    st: statusOf(m),
    link: modelLink(m.id),
  })))

/* ---- 加载/同步状态提示条（旧 updateModelsNotice 平移） ---- */
function timeStr(t: number): string {
  const d = new Date(t), p = (x: number) => (x < 10 ? '0' : '') + x
  return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds())
}
const noticeVisible = computed(() => !!error.value || !!loadedAt.value)
const noticeMsg = computed(() => {
  if (error.value) {
    return loadedAt.value
      ? `模型列表同步失败（上次成功更新 ${timeStr(loadedAt.value)}），当前展示离线内置列表。`
      : '模型列表加载失败，当前展示离线内置列表。'
  }
  return `模型列表已同步，更新于 ${timeStr(loadedAt.value)}（每 60 秒自动刷新）`
})

/* ---- 收费模型专区：统一 aqua/ 前缀（按次/按量分组由密钥决定，实时状态内嵌卡片） ---- */
const paidModels = computed(() => orderedModels.value
  .filter(m => m.paid)
  .map(m => ({
    id: m.id,
    mode: m.mode || 'per_call',
    groups: (Array.isArray((m as any).groups) && (m as any).groups.length)
      ? (m as any).groups as string[]
      : [m.mode === 'per_token' ? 'per_token' : 'per_call'],
    price: m.price_micro ?? 0,
    inPrice: m.in_price ?? 0,
    cachePrice: m.cache_price ?? 0,
    outPrice: m.out_price ?? 0,
    floor: m.floor_micro ?? 0,
    // VIP 专享：后端对 VIP 用户附原价（base_*）；普通用户无这些字段
    basePrice: (m as any).base_price_micro,
    baseInPrice: (m as any).base_in_price,
    baseCachePrice: (m as any).base_cache_price,
    baseOutPrice: (m as any).base_out_price,
    baseFloor: (m as any).base_floor_micro,
    perImage: (m as any).per_image,
    basePerImage: (m as any).base_per_image,
    subsidized: m.subsidized === true,
    promoEndsAt: m.promo_ends_at ?? 0,
    isImage: m.type === 'image',
    health: healthOf(m),
    st: statusOf(m),
    link: modelLink(m.id),
  })))
/** 按次分组 / 按量分组（同一模型两组都有时两栏都展示——用哪组计费取决于密钥分组） */
const paidCallLine = computed(() => paidModels.value.filter(m => m.groups.includes('per_call')))
const paidTokenLine = computed(() => paidModels.value.filter(m => m.groups.includes('per_token')))

/* ---- 收费页工具栏：搜索 + 类型筛选 + 排序 ---- */
const paidQ = ref('')
const paidType = ref<'all' | 'chat' | 'image'>('all')
const paidSort = ref<'smart' | 'price'>('smart')
function sortTokenLine(arr: typeof paidModels.value) {
  const kw = paidQ.value.toLowerCase().trim()
  let out = arr.filter(m => (paidType.value === 'all' || (paidType.value === 'image' ? m.isImage : !m.isImage))
    && (kw ? m.id.toLowerCase().indexOf(kw) !== -1 : true))
  if (paidSort.value === 'price') {
    out = out.slice().sort((a, b) => (a.perImage ?? a.outPrice ?? a.price) - (b.perImage ?? b.outPrice ?? b.price))
  }
  return out
}
const tokenLineRows = computed(() => sortTokenLine(paidTokenLine.value))
const callLineRows = computed(() => sortTokenLine(paidCallLine.value))
/** 官方中转专线（tlk/ 前缀，official 分组密钥专用：官方原价 6 折） */
const officialLineRows = computed(() => sortTokenLine(paidModels.value.filter(m => m.groups.includes('official'))))

/* ---- 众筹池（acu/ 前缀）：免费列表内展示池子供血状态，条目本身来自 /v1/models（paid=false 自动进免费视图） ---- */
import { apiJson as poolApiJson } from '@/composables/useApi'
const poolStatus = ref<any>(null)
async function loadPool() {
  try { poolStatus.value = await poolApiJson<any>('/pool/status') } catch { /* 忽略 */ }
}
const poolAlive = computed(() => !!poolStatus.value && poolStatus.value.balance_micro > 0)
function isCrowd(id: string) { return id.startsWith('acu/') }
loadPool()

/** 微元 → 元字符串（去尾零：2000→"0.002"，3800→"0.0038"） */
function microYuan(v?: number): string {
  if (v == null) return '--'
  return (v / 1e6).toFixed(6).replace(/0+$/, '').replace(/\.$/, '')
}
/** 官方原价 → 现价 的实际倍率角标（数据驱动：VIP 0.13× / 普通 0.2×；非促销无对照不显示） */
function rateBadgeOf(m: { baseInPrice?: number | null; basePerImage?: number | null; inPrice: number; perImage?: number | null }): string {
  const base = m.baseInPrice ?? m.basePerImage
  const cur = m.inPrice ?? m.perImage
  if (!base || !cur) return ''
  const v = Math.round((cur / base) * 100) / 100
  if (v <= 0 || v >= 1) return ''
  return (v < 0.095 ? v.toFixed(2) : v.toFixed(1)) + '×'
}
/** 元/百万tokens 价显示（0.05 → "0.05"，0.005 → "0.005"） */
function perMYuan(v?: number): string {
  if (v == null) return '--'
  return v.toFixed(4).replace(/0+$/, '').replace(/\.$/, '')
}

function modelLink(id: string) { return '/model/' + encodeURIComponent(id) }
</script>

<template>
  <section class="route-page">
    <div class="models-page-head">
      <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/></svg></span>模型中心</h1>
      <p>全线模型一览与能力总览的统一入口：由 Nvidia NIM 与官方自营专线实时提供。</p>
    </div>
    <nav class="hub-subnav" aria-label="模型中心子导航">
      <button type="button" class="hub-tab" :class="{ active: view === 'free' }" @click="setView('free')"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg></span>免费模型</button>
      <button type="button" class="hub-tab" :class="{ active: view === 'paid' }" @click="setView('paid')"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v10M9.5 9.5c0-1 1.1-1.7 2.5-1.7s2.5.7 2.5 1.7c0 2.6-5 1.4-5 4 0 1 1.1 1.7 2.5 1.7s2.5-.7 2.5-1.7"/></svg></span>收费模型</button>
      <button type="button" class="hub-tab" :class="{ active: view === 'cap' }" @click="setView('cap')"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2 20 7v10l-8 5-8-5V7l8-5z"/><path d="M12 12v8"/><path d="m4 7 8 5 8-5"/><path d="M12 2v8"/></svg></span>模型能力</button>
    </nav>
    <div class="hub-pane">
      <template v-if="view === 'free'">
      <p class="hub-desc">以下为<b>全站免费模型</b>，由 Nvidia NIM 提供——<b>注册即可使用、永久免费</b>。官方自营收费模型（统一 <code>aqua/</code> 前缀）请切换到「收费模型」页查看。点击「复制」即可获取模型 ID，填入客户端使用。</p>
      <div class="repo-banner">
        <svg viewBox="0 0 24 24" fill="#f59e0b" stroke="#f59e0b" stroke-width="1" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2l2.9 6.26L21.5 9.27l-4.75 4.63 1.12 6.53L12 17.77l-5.87 3.09 1.12-6.53L2.5 9.27l6.6-1.01L12 2z"/></svg>
        <span><b>AQUA · ACU 工程系列开源项目</b>（网关 + 前台全量源码）—— 喜欢就给作者点个 Star 吧</span>
        <a class="repo-link" href="https://gitee.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener">Gitee 仓库 <AqIcon name="star" :size="14" /></a>
        <a class="repo-link" href="https://github.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener">GitHub 仓库 <AqIcon name="star" :size="14" /></a>
      </div>
      <!-- 众筹模型专区：官方自营通道（acu/ 前缀，官方原价扣站点额度，充值翻倍） -->
      <div v-if="crowdModels.length" class="crowd-sec" :class="{ off: poolStatus && !poolAlive }">
        <div class="cs-head">
          <div class="cs-title">
            <span class="cs-ic"><AqIcon name="coin" :size="18" /></span>
            <b>众筹公共模型 · 官方自营通道</b>
            <span class="cs-live">
              <span class="pb-dot" :class="poolAlive ? 'on' : 'off'"></span>
              {{ poolStatus ? (poolAlive ? '站点额度可用' : '站点额度已用完 · 等待充值复活') : '状态同步中…' }}
              <template v-if="poolStatus"> · 站点额度 ¥{{ (poolStatus.balance_micro / 1e6).toFixed(2).replace(/\.00$/, '') }}</template>
            </span>
          </div>
          <router-link to="/pool" class="cs-go">账本与榜单 →</router-link>
        </div>
        <p class="cs-desc">acu/ 前缀模型按<b>官方原价</b>从<b>众筹站点额度</b>扣费——无需充值即可调用，任何分组密钥可用，<b>个人余额分文不动</b>；充值翻倍：充 1 元 = 2 元站点额度，额度见底即暂停，充值立刻复活，救场者登上荣誉墙。</p>
        <div class="cs-cards">
          <div v-for="m in crowdModels" :key="m.id" class="cs-card">
            <div class="cs-model">
              <router-link :to="m.link" class="cs-id">{{ m.id }}</router-link>
              <CopyBtn :text="m.id" />
            </div>
            <div class="cs-official">
              <b>按官方原价扣费</b>
              <span>充 1 元 = 2 元站点额度 · 个人余额分文不动</span>
            </div>
            <div class="cs-foot">
              <span class="cs-tag">官方原价 · 站点额度</span>
              <span v-if="m.health" class="cs-h">健康 {{ m.health.score }}</span>
              <router-link :to="m.link" class="cs-detail">详情 →</router-link>
            </div>
          </div>
          <div class="cs-card cs-cta">
            <b>充值翻倍</b>
            <p>充 1 元 = 2 元站点额度——充 10 到账 20，给所有人的公共算力扩容，无最低限制，额度归零后第一笔充值自动登上荣誉墙</p>
            <router-link to="/pool" class="cs-btn">去充值翻倍</router-link>
          </div>
        </div>
      </div>
      </template>
      <template v-else-if="view === 'paid'">
      <!-- 倍率横幅：明示当前促销倍率与恢复倍率（meta 配置下发，促销到期自动隐藏） -->
      <div v-if="rateBannerVisible" class="rate-banner">
        <div class="rb-left">
          <span class="rb-rate"><b>{{ ratePromo }}</b><i>×</i></span>
          <span class="rb-rate-label">当前计费倍率<br><em>限时补贴价</em></span>
        </div>
        <div class="rb-mid">
          <b class="rb-title">全场按量计费模型 · 官方补贴进行中</b>
          <p class="rb-desc">现在按 <b>{{ ratePromo }} 倍率</b>（上游成本的 {{ Math.round(Number(ratePromo) * 10) }} 折）计费，活动结束后恢复 <b>{{ rateNormal }} 倍率</b>——越早用越便宜，恢复前价格不变。</p>
        </div>
        <div class="rb-right">
          <span class="rb-cd-label">距恢复 {{ rateNormal }}×</span>
          <span class="rb-cd">{{ promoLeftStr }}</span>
          <span class="rb-cd-sub">{{ promoEndsStr }} 自动恢复</span>
        </div>
      </div>
      <p class="hub-desc">官方自营<b>收费模型统一使用 <code>aqua/</code> 前缀</b>：计费方式由你<b>密钥的计费分组</b>决定——先付后用、失败全额退回、绝不透支。<router-link to="/console?view=topup">在线充值即充即用</router-link>，免费模型不受余额影响。</p>
      <!-- 收费模型专区：按分组双栏（数据实时来自 /v1/models 的 groups 字段） -->
      <div v-if="paidModels.length" class="paid-models-sec">
        <div class="pms-head">
          <div class="pms-title">
            <span class="pms-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2l2.9 6.26 6.86.63-5.2 4.55 1.56 6.71L12 16.9 5.88 20.15l1.56-6.71-5.2-4.55 6.86-.63z"/></svg></span>
            <b>收费模型专区 · 官方自营</b>
            <span class="pms-promo">实时状态内嵌卡片 · 每 20 秒自动刷新</span>
          </div>
          <span class="pms-live-meta">
            <span class="live-pulse" :class="{ loading: liveLoading }"></span>
            数据时间 {{ liveTs ? new Date(liveTs * 1000).toLocaleTimeString() : '--' }}
            <button class="mini-btn" :disabled="liveLoading" @click="loadLive">{{ liveLoading ? '刷新中…' : '立即刷新' }}</button>
          </span>
        </div>
        <!-- 工具栏：搜索 + 类型筛选 + 排序 -->
        <div class="pms-toolbar">
          <input v-model="paidQ" type="text" class="pms-search" placeholder="搜索模型，如 deepseek / glm / qwen …">
          <div class="pms-pills">
            <button class="pill" :class="{ active: paidType === 'all' }" @click="paidType = 'all'">全部</button>
            <button class="pill" :class="{ active: paidType === 'chat' }" @click="paidType = 'chat'">对话</button>
            <button class="pill" :class="{ active: paidType === 'image' }" @click="paidType = 'image'">图片</button>
          </div>
          <div class="pms-pills">
            <button class="pill" :class="{ active: paidSort === 'smart' }" @click="paidSort = 'smart'" title="旗舰与国产模型优先">智能排序</button>
            <button class="pill" :class="{ active: paidSort === 'price' }" @click="paidSort = 'price'" title="按输出单价从低到高">价格优先</button>
          </div>
        </div>
        <!-- 按次计费分组（密钥选「免费+按次」时可用；按次线临时下架时整栏自动消失） -->
        <template v-if="callLineRows.length">
          <div class="pms-line-title">按次计费分组<small>（密钥选「免费 + 按次计费」时可用：每次成功请求扣一次，与生成长度无关）</small></div>
          <div class="pms-grid">
            <div v-for="(m, i) in callLineRows" :key="m.id + ':call'" class="pms-card" :class="{ paused: !!m.st }" :style="{ '--i': i }">
              <div class="pms-top">
                <code class="pms-id" :title="'完整模型 ID：' + m.id"><span class="pms-ns">{{ m.id.split('/')[0] }}/</span>{{ m.id.split('/').slice(1).join('/') }}</code>
                <span v-if="m.st" class="pms-st" :title="m.st.title">{{ m.st.text }}</span>
              </div>
              <div v-if="m.mode === 'per_call'" class="pms-price">
                <b>¥{{ microYuan(m.price) }}</b><i>/次</i>
                <span class="pms-price-tag" :class="{ promo: m.basePrice != null }">{{ m.basePrice != null ? 'VIP 拿货价' : '正常价' }}</span>
              </div>
              <div v-if="m.mode === 'per_call' && m.basePrice != null" class="pms-base-price">原价 ¥{{ microYuan(m.basePrice) }}/次 · VIP 专享拿货价已生效</div>
              <div class="pms-actions">
                <CopyBtn :text="m.id" />
                <router-link class="pms-detail" :to="m.link">能力详情<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg></router-link>
              </div>
            </div>
          </div>
        </template>
        <!-- 按量计费分组（密钥选「免费+按量」时可用；三段价、无保底、先付后用） -->
        <template v-if="tokenLineRows.length">
          <div class="pms-line-title">按量计费分组<small>（密钥选「免费 + 按量计费」时可用：输入 / 缓存命中 / 输出分段计价，缓存命中大幅更省，无保底）</small></div>
          <div class="pms-grid">
            <div v-for="(m, i) in tokenLineRows" :key="m.id" class="pms-card" :class="{ paused: !!m.st }" :style="{ '--i': i }">
              <div class="pms-top">
                <code class="pms-id" :title="'完整模型 ID：' + m.id"><span class="pms-ns">{{ m.id.split('/')[0] }}/</span>{{ m.id.split('/').slice(1).join('/') }}</code>
                <span v-if="m.subsidized && rateBadgeOf(m)" class="pms-rate-badge" title="官方原价 × 补贴倍率 = 现价，活动结束后恢复原价">{{ rateBadgeOf(m) }}</span>
                <span v-else-if="m.baseInPrice != null || m.basePerImage != null" class="pms-vip-badge" title="VIP 专享拿货价已生效">VIP</span>
              </div>
              <!-- 实时状态灯行：近 6 小时内最近 200 次请求推断（20 秒轮询） -->
              <div class="pms-live" :class="'lv-' + liveStatusOf(m.id).key"
                :title="'状态由近 6 小时内最近 200 次真实请求推断（剔除调用方参数错误与限流）· 每 20 秒自动刷新' + (liveMap[m.id] ? ' · 最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '')">
                <span class="lv-dot"></span><span class="lv-text">{{ liveStatusOf(m.id).text }}</span>
                <span v-if="liveMap[m.id]" class="lv-last">{{ fmtAgo(liveMap[m.id].last_ts) }}</span>
              </div>
              <!-- 图片模型：按张计费（VIP 展示拿货价 + 底部原价） -->
              <div v-if="m.perImage != null" class="pms-price">
                <b>¥{{ microYuan(m.perImage) }}</b><i>/张</i>
                <span class="pms-price-tag" :class="{ promo: m.basePerImage != null || m.subsidized }">{{ m.basePerImage != null ? 'VIP 拿货价' : (m.subsidized ? '限时补贴' : '按张计费') }}</span>
              </div>
              <div v-if="m.perImage != null && m.basePerImage != null" class="pms-base-price">原价 ¥{{ microYuan(m.basePerImage) }}/张 · VIP 专享拿货价已生效</div>
              <!-- 按量计费：三段价 + 官方原价划线对照（促销生效时后端下发 base_*） -->
              <div v-if="m.perImage == null" class="pms-price pms-price-token">
                <div class="tp-row"><i>输入</i><b>¥{{ perMYuan(m.inPrice) }}</b><s v-if="m.baseInPrice != null" title="官方原价">¥{{ perMYuan(m.baseInPrice) }}</s><i class="tp-unit">/百万tokens</i></div>
                <div class="tp-row"><i>缓存命中</i><b>¥{{ perMYuan(m.cachePrice) }}</b><s v-if="m.baseCachePrice != null" title="官方原价">¥{{ perMYuan(m.baseCachePrice) }}</s><i class="tp-unit">/百万tokens</i></div>
                <div class="tp-row"><i>输出</i><b>¥{{ perMYuan(m.outPrice) }}</b><s v-if="m.baseOutPrice != null" title="官方原价">¥{{ perMYuan(m.baseOutPrice) }}</s><i class="tp-unit">/百万tokens</i></div>
                <div class="tp-floor">先付后用 · 用多少付多少 · <span class="tp-cache-tip" title="重复前缀会命中缓存价，显著降低输入成本">缓存命中更省</span></div>
              </div>
              <!-- 实时指标：平均时延 / 生成速度 / 成功率（最近 200 次请求聚合） -->
              <div class="pms-live-metrics" :title="'近 6 小时 · 最近 200 次真实请求聚合 · 数据时间 ' + (liveTs ? new Date(liveTs * 1000).toLocaleTimeString() : '--')">
                <span class="lm-item" :class="{ dim: !liveMap[m.id]?.avg_latency_ms }"><i>时延</i><b>{{ liveMap[m.id]?.avg_latency_ms ? fmtLat(liveMap[m.id].avg_latency_ms) : '--' }}</b></span>
                <span class="lm-item" :class="{ dim: !liveMap[m.id]?.avg_tps }"><i>速度</i><b>{{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) : '--' }}<u>tok/s</u></b></span>
                <span class="lm-item" :class="{ dim: !liveMap[m.id] }"><i>成功率</i><b>{{ liveMap[m.id] ? fmtRate(liveMap[m.id].ok_rate) : '--' }}</b></span>
              </div>
              <div class="pms-actions">
                <CopyBtn :text="m.id" />
                <router-link class="pms-detail" :to="m.link">能力详情<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg></router-link>
              </div>
            </div>
          </div>
          <div v-if="!tokenLineRows.length" class="model-empty">未找到匹配的收费模型</div>
        </template>
        <!-- 官方中转 · 高速专线（tlk/ 前缀，official 分组密钥专用） -->
        <template v-if="officialLineRows.length">
          <div class="pms-line-title">官方中转 · 高速专线<small>（密钥选「官方中转」分组时可用：tlk/ 前缀独占模型，按官方原价 6 折分段计价）</small></div>
          <div class="pms-grid">
            <div v-for="(m, i) in officialLineRows" :key="m.id" class="pms-card" :class="{ paused: !!m.st }" :style="{ '--i': i }">
              <div class="pms-top">
                <code class="pms-id" :title="'完整模型 ID：' + m.id"><span class="pms-ns">{{ m.id.split('/')[0] }}/</span>{{ m.id.split('/').slice(1).join('/') }}</code>
              </div>
              <div class="pms-live" :class="'lv-' + liveStatusOf(m.id).key"
                :title="'状态由近 6 小时内最近 200 次真实请求推断（剔除调用方参数错误与限流）· 每 20 秒自动刷新' + (liveMap[m.id] ? ' · 最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '')">
                <span class="lv-dot"></span><span class="lv-text">{{ liveStatusOf(m.id).text }}</span>
                <span v-if="liveMap[m.id]" class="lv-last">{{ fmtAgo(liveMap[m.id].last_ts) }}</span>
              </div>
              <div class="pms-price pms-price-token">
                <div class="tp-row"><i>输入</i><b>¥{{ perMYuan(m.inPrice) }}</b><i class="tp-unit">/百万tokens</i></div>
                <div class="tp-row"><i>缓存命中</i><b>¥{{ perMYuan(m.cachePrice) }}</b><i class="tp-unit">/百万tokens</i></div>
                <div class="tp-row"><i>输出</i><b>¥{{ perMYuan(m.outPrice) }}</b><i class="tp-unit">/百万tokens</i></div>
                <div class="tp-floor">官方中转专线 · 官方原价 6 折 · 先付后用</div>
              </div>
              <div class="pms-live-metrics" :title="'近 6 小时 · 最近 200 次真实请求聚合 · 数据时间 ' + (liveTs ? new Date(liveTs * 1000).toLocaleTimeString() : '--')">
                <span class="lm-item" :class="{ dim: !liveMap[m.id]?.avg_latency_ms }"><i>时延</i><b>{{ liveMap[m.id]?.avg_latency_ms ? fmtLat(liveMap[m.id].avg_latency_ms) : '--' }}</b></span>
                <span class="lm-item" :class="{ dim: !liveMap[m.id]?.avg_tps }"><i>速度</i><b>{{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) : '--' }}<u>tok/s</u></b></span>
                <span class="lm-item" :class="{ dim: !liveMap[m.id] }"><i>成功率</i><b>{{ liveMap[m.id] ? fmtRate(liveMap[m.id].ok_rate) : '--' }}</b></span>
              </div>
              <div class="pms-actions">
                <CopyBtn :text="m.id" />
                <router-link class="pms-detail" :to="m.link">能力详情<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg></router-link>
              </div>
            </div>
          </div>
        </template>
      </div>
      <!-- 收费模型与免费政策说明（用户必读） -->
      <div class="paid-policy-card">
        <div class="pp-head">
          <div class="pp-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="5" width="20" height="14" rx="2"/><circle cx="12" cy="12" r="3"/><path d="M6 9v0M18 15v0"/></svg></div>
          <b>免费与收费 · 必读说明</b>
          <span class="pp-tag">请花一分钟读完</span>
        </div>
        <div class="pp-body">
          <div class="pp-item">
            <span class="pp-no pp-no-free">1</span>
            <div>
              <b>全站绝大多数模型完全免费，而且会一直免费</b>
              <p>Nvidia NIM 免费通道的全部模型——对话、视觉、语音、向量、重排统统<b>不收一分钱</b>，注册即可使用，用量照常统计但仅用于展示，<b>今后也不会对这些模型收费</b>。</p>
            </div>
          </div>
          <div class="pp-item">
            <span class="pp-no pp-no-paid">2</span>
            <div>
              <b>收费模型：官方自营系列（统一 <code>aqua/</code> 前缀）</b>
              <p v-if="paidCallLine.length">收费模型统一使用 <code>aqua/</code> 前缀，<b>计费方式由你密钥的计费分组决定</b>：在个人控制台创建密钥时选择「免费 + 按次计费」或「免费 + 按量计费」。两个分组的可用模型不同（上方双栏即两组各自可用列表；部分模型两个分组通用）。按次分组为高频调用与极致速度单独采购的专属算力；按量分组提供全系列大杯旗舰与文生图等特殊计费模型（特殊模型按张/按量计费以卡片实时标注为准）。调用方式与免费模型完全一致（同一个接口，只是 <code>model</code> 换成它们），支持流式输出，对客户端完全透明。</p>
              <p v-else>收费模型统一使用 <code>aqua/</code> 前缀，<b>密钥选择「免费 + 按量计费」分组即可调用</b>：输入 / 缓存命中 / 输出分段精算，用多少付多少。文生图等特殊计费模型以卡片实时标注为准。调用方式与免费模型完全一致（同一个接口，只是 <code>model</code> 换成它们），支持流式输出，对客户端完全透明。</p>
              <div class="pp-rules">
                <span v-if="paidCallLine.length">计费方式：<b>按次分组密钥</b>——每次<b>成功</b>请求按所用模型单价扣一次，<b>与生成长度无关</b>（各模型实时单价见上方专区）</span>
                <span>计费方式：<b>按量分组密钥</b>——输入 / 缓存命中 / 输出分段计价，<b>用多少付多少、无保底</b>；重复前缀命中缓存价，输入成本大幅更低</span>
                <span>先付后用：发起请求即按预估预扣（可在请求中调小 <code>max_tokens</code> 降低单次预扣），完成后<b>多退少补</b>；余额用完自动停止，<b>绝不透支、绝无欠费</b></span>
                <span>请求失败自动全额退回，<b>错误请求不扣费</b></span>
                <span>每次扣费与余额在<b>个人控制台</b>实时可查，流水永久留存；余额不足前可在控制台开启<b>邮件提醒</b></span>
              </div>
            </div>
          </div>
          <div class="pp-item">
            <span class="pp-no pp-no-free">3</span>
            <div>
              <b>如何获得余额</b>
              <p><b>在线充值即时到账</b>：登录后进入<router-link to="/console?view=topup" style="color:var(--accent);">个人控制台 · 余额充值</router-link>，支付宝 / 微信任一渠道支付，<b>支付金额 100% 全额到账</b>（渠道手续费由本站承担），到账立即可用。免费模型不受余额影响——没有余额照样随便用。</p>
            </div>
          </div>
        </div>
      </div>
      </template>
      <template v-if="view === 'free'">
      <div class="filter-row">
        <span class="flabel">平台</span>
        <div class="filters" id="platform-filter">
          <button class="pill" :class="{ active: curPlatform === 'all' }" data-platform="all" @click="curPlatform = 'all'">全部</button>
          <button class="pill" :class="{ active: curPlatform === 'nvidia' }" data-platform="nvidia" @click="curPlatform = 'nvidia'">Nvidia NIM</button>
          <button class="pill" :class="{ active: curPlatform === 'acu' }" data-platform="acu" @click="curPlatform = 'acu'">官方自营</button>
        </div>
      </div>
      <div class="filter-row">
        <span class="flabel">数组</span>
        <div class="filters" id="type-filter">
          <button class="pill" :class="{ active: curType === 'all' }" data-type="all" @click="curType = 'all'">全部</button>
          <button class="pill" :class="{ active: curType === 'chat' }" data-type="chat" @click="curType = 'chat'">对话</button>
          <button class="pill" :class="{ active: curType === 'embedding' }" data-type="embedding" @click="curType = 'embedding'">向量</button>
          <button class="pill" :class="{ active: curType === 'rerank' }" data-type="rerank" @click="curType = 'rerank'">重排</button>
          <button class="pill" :class="{ active: curType === 'asr' }" data-type="asr" @click="curType = 'asr'">语音识别</button>
          <button class="pill" :class="{ active: curType === 'tts' }" data-type="tts" @click="curType = 'tts'">语音合成</button>
          <button class="pill" :class="{ active: curType === 'moderation' }" data-type="moderation" @click="curType = 'moderation'">风控</button>
          <button class="pill" :class="{ active: curType === 'vision' }" data-type="vision" @click="curType = 'vision'">视觉</button>
        </div>
      </div>
      <div class="models-bar">
        <input class="search" id="model-search" v-model="q" type="text" placeholder="搜索模型名称，如 llama / deepseek / gemma ...">
        <span class="count" id="model-count">共 {{ viewRows.length }} 个模型</span>
      </div>
      <div v-if="noticeVisible" class="model-notice" id="model-notice" :class="{ err: !!error }">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/></svg>
        <span class="nt-msg">{{ noticeMsg }}</span>
        <button v-if="error" class="btn nt-btn" @click="retry">重试</button>
      </div>
      <div class="model-grid" id="model-grid">
        <div v-if="loading && !models.length" class="model-empty">模型加载中…</div>
        <div v-else-if="!viewRows.length" class="model-empty">未找到匹配的模型</div>
        <template v-else>
          <div v-for="r in viewRows" :key="r.row.id" class="model-item" :class="{ exhausted: r.exhausted }">
            <span v-if="isCrowd(r.row.id)" class="mtag m-crowd" title="众筹公共算力池：按官方原价从站点额度扣费，充 1 元 = 2 元额度，个人余额不动">众筹</span>
            <span v-if="!hideTag(r.row.type)" class="mtag" :class="'m-' + r.row.platform" :title="r.row.type">{{ platformLabel(r.row.platform) }} · {{ typeLabel(r.row.type) }}</span>
            <span v-if="r.st" class="mtag" :class="r.st.cls" :title="r.st.title">{{ r.st.text }}</span>
            <span v-if="r.health" class="mtag m-health" :class="r.health.cls" :title="r.health.tip">健康 {{ r.health.score }}</span>
            <router-link class="model-link" :to="modelLink(r.row.id)" :title="'查看 ' + r.row.id + ' 详情'">{{ r.row.id }}</router-link>
            <CopyBtn :text="r.row.id" />
          </div>
        </template>
      </div>
      <p class="hint" style="margin-top:10px;">点击任意模型 ID 可查看该模型的详细能力说明与支持参数。</p>
      </template>
      <CapabilitiesPage v-if="view === 'cap'" embedded />
    </div>
  </section>
</template>

<style scoped>
/* 子导航 Tab 外观统一由 legacy.css 的 .hub-tab 提供（按钮化视觉），此处仅继承字体 */
button.hub-tab { font-family: inherit; }

/* ---- 众筹模型专区（免费列表内超级大卡片区）：官方自营通道 ---- */
.crowd-sec { margin: 14px 0; padding: 16px 18px; border-radius: 16px; background: linear-gradient(135deg, rgba(56,189,248,.10), rgba(129,140,248,.07) 55%, transparent), var(--card2, rgba(255,255,255,.03)); border: 1px solid rgba(56,189,248,.35); box-shadow: 0 6px 24px rgba(56,189,248,.08); }
.crowd-sec.off { background: linear-gradient(135deg, rgba(248,113,113,.10), transparent 55%), var(--card2, rgba(255,255,255,.03)); border-color: rgba(248,113,113,.35); box-shadow: 0 6px 24px rgba(248,113,113,.08); }
.cs-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.cs-title { display: flex; align-items: center; gap: 9px; flex-wrap: wrap; }
.cs-title > b { font-size: 15.5px; }
.cs-ic { width: 32px; height: 32px; border-radius: 9px; display: flex; align-items: center; justify-content: center; background: linear-gradient(135deg, rgba(56,189,248,.18), rgba(129,140,248,.14)); color: var(--aqua,#38bdf8); border: 1px solid rgba(56,189,248,.35); flex: none; }
.cs-live { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--muted,#8a94a6); border: 1px solid var(--border,rgba(128,140,160,.25)); border-radius: 999px; padding: 3px 10px; }
.pb-dot { width: 8px; height: 8px; border-radius: 50%; flex: none; }
.pb-dot.on { background: #34d399; box-shadow: 0 0 10px rgba(52,211,153,.9); animation: pbPulse 2s ease-in-out infinite; }
.pb-dot.off { background: #f87171; box-shadow: 0 0 10px rgba(248,113,113,.9); }
@keyframes pbPulse { 50% { opacity: .5; } }
.cs-go { margin-left: auto; font-size: 12.5px; color: var(--aqua,#38bdf8); white-space: nowrap; text-decoration: none; }
.cs-desc { font-size: 12.5px; color: var(--muted,#8a94a6); line-height: 1.7; margin: 10px 0 12px; }
.cs-desc b { color: var(--aqua,#38bdf8); }
.cs-cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 12px; }
.cs-card { background: var(--card,#121a26); border: 1px solid var(--border,rgba(128,140,160,.25)); border-radius: 13px; padding: 14px 16px; display: flex; flex-direction: column; gap: 10px; transition: transform .16s ease, border-color .16s ease, box-shadow .16s ease; }
.cs-card:hover { transform: translateY(-2px); border-color: rgba(56,189,248,.45); box-shadow: 0 8px 24px rgba(56,189,248,.12); }
.cs-model { display: flex; align-items: center; gap: 8px; }
.cs-id { font-weight: 800; font-size: 14px; color: var(--aqua,#38bdf8); text-decoration: none; word-break: break-all; }
.cs-official { display: flex; flex-direction: column; gap: 3px; background: rgba(56,189,248,.06); border: 1px solid rgba(56,189,248,.15); border-radius: 9px; padding: 8px 10px; }
.cs-official b { font-size: 13px; color: var(--aqua,#38bdf8); }
.cs-official span { font-size: 10.5px; color: var(--muted,#8a94a6); line-height: 1.5; }
.cs-foot { display: flex; align-items: center; gap: 8px; }
.cs-tag { font-size: 10.5px; padding: 2px 8px; border-radius: 999px; background: rgba(129,140,248,.14); color: #a5b4fc; border: 1px solid rgba(129,140,248,.35); }
.cs-h { font-size: 11px; color: var(--muted,#8a94a6); }
.cs-detail { margin-left: auto; font-size: 12px; color: var(--aqua,#38bdf8); text-decoration: none; }
.cs-card.cs-cta { background: linear-gradient(135deg, rgba(251,191,36,.08), transparent 60%), var(--card,#121a26); border: 1px dashed rgba(251,191,36,.4); }
.cs-card.cs-cta b { font-size: 15px; color: #fbbf24; }
.cs-card.cs-cta p { font-size: 12px; color: var(--muted,#8a94a6); line-height: 1.6; margin: 0; flex: 1; }
.cs-btn { display: inline-block; text-align: center; padding: 9px 14px; border-radius: 10px; font-weight: 700; font-size: 13px; text-decoration: none; color: #fff !important; background: linear-gradient(135deg,#0ea5e9,#6366f1); }
.mtag.m-crowd { background: linear-gradient(135deg, rgba(99,102,241,.2), rgba(129,140,248,.14)); color: #a5b4fc; border: 1px solid rgba(129,140,248,.4); }

/* ---- 倍率横幅（rate-banner）：明示 0.2× 促销 → 0.5× 恢复 ---- */
.rate-banner {
  display: flex; align-items: center; gap: 22px; flex-wrap: wrap;
  margin: 0 0 16px; padding: 18px 24px; border-radius: 16px;
  background:
    radial-gradient(ellipse 60% 120% at 8% 0%, rgba(251,191,36,.28), transparent 60%),
    radial-gradient(ellipse 50% 100% at 92% 100%, rgba(244,63,94,.22), transparent 65%),
    linear-gradient(120deg, rgba(120,53,15,.55), rgba(69,26,3,.65) 55%, rgba(76,5,25,.55));
  border: 1px solid rgba(251,191,36,.4);
  box-shadow: 0 10px 32px -12px rgba(245,158,11,.35), inset 0 1px 0 rgba(255,255,255,.08);
  position: relative; overflow: hidden;
}
.rate-banner::before {
  content: ""; position: absolute; inset: 0; pointer-events: none;
  background: linear-gradient(105deg, transparent 40%, rgba(255,255,255,.09) 50%, transparent 60%);
  animation: rbshine 5.5s ease-in-out infinite;
}
@keyframes rbshine { 0%, 55%, 100% { transform: translateX(-60%); } 30% { transform: translateX(60%); } }
.rb-left { display: flex; align-items: center; gap: 12px; flex: none; }
.rb-rate { display: flex; align-items: baseline; color: #fbbf24; text-shadow: 0 0 26px rgba(251,191,36,.55); }
.rb-rate b { font-size: 52px; font-weight: 900; line-height: 1; font-variant-numeric: tabular-nums; letter-spacing: -2px; }
.rb-rate i { font-style: normal; font-size: 24px; font-weight: 800; margin-left: 2px; }
.rb-rate-label { font-size: 12px; font-weight: 700; color: rgba(255,237,213,.92); line-height: 1.5; }
.rb-rate-label em { font-style: normal; color: #fbbf24; }
.rb-mid { flex: 1; min-width: 220px; }
.rb-title { font-size: 16.5px; color: #fff; }
.rb-desc { margin: 5px 0 0; font-size: 13px; line-height: 1.7; color: rgba(255,237,213,.85); }
.rb-desc b { color: #fbbf24; }
.rb-right { flex: none; display: flex; flex-direction: column; align-items: flex-end; gap: 2px; }
.rb-cd-label { font-size: 11.5px; font-weight: 700; color: rgba(255,237,213,.7); letter-spacing: .04em; }
.rb-cd { font-size: 24px; font-weight: 900; color: #fff; font-variant-numeric: tabular-nums; text-shadow: 0 0 18px rgba(251,191,36,.4); }
.rb-cd-sub { font-size: 11px; color: rgba(255,237,213,.6); }
@media (max-width: 760px) {
  .rate-banner { gap: 14px; padding: 16px; }
  .rb-rate b { font-size: 40px; }
  .rb-right { align-items: flex-start; }
}

/* ---- 收费模型专区（pms-*）：价格 + 实时状态内嵌 ---- */
.paid-models-sec {
  border: 1px solid rgba(56, 189, 248, .3);
  background: linear-gradient(160deg, rgba(56,189,248,.07), rgba(129,140,248,.04) 55%, transparent);
  border-radius: 16px;
  padding: 18px 20px;
  margin: 14px 0 18px;
}
.pms-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap; margin-bottom: 12px; }
.pms-title { display: flex; align-items: center; gap: 10px; }
.pms-ic { width: 34px; height: 34px; border-radius: 9px; display: flex; align-items: center; justify-content: center; background: rgba(56,189,248,.14); color: var(--accent); flex: none; }
.pms-ic svg { width: 18px; height: 18px; }
.pms-title b { font-size: 16px; }
.pms-promo { font-size: 12px; color: var(--accent); background: rgba(56,189,248,.12); border: 1px solid rgba(56,189,248,.3); border-radius: 999px; padding: 2px 10px; font-weight: 600; }
.pms-live-meta { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--muted); }
/* 工具栏：搜索 + 筛选 + 排序 */
.pms-toolbar { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin: 0 0 14px; }
.pms-search {
  flex: 1; min-width: 200px; max-width: 340px; height: 34px; padding: 0 14px;
  border-radius: 10px; border: 1px solid var(--border); background: var(--card);
  color: var(--text); font-size: 13px; outline: none; transition: border-color .15s, box-shadow .15s;
}
.pms-search:focus { border-color: var(--accent); box-shadow: 0 0 0 3px rgba(56,189,248,.15); }
.pms-pills { display: flex; gap: 6px; flex-wrap: wrap; }
.pms-pills .pill { font-size: 12px; padding: 5px 13px; border-radius: 999px; border: 1px solid var(--border); background: transparent; color: var(--muted); cursor: pointer; font-weight: 600; transition: all .15s; }
.pms-pills .pill:hover { border-color: var(--border-hover); color: var(--text); }
.pms-pills .pill.active { background: rgba(56,189,248,.15); border-color: rgba(56,189,248,.5); color: var(--accent); }
/* 分线标题（按次 + 按量） */
.pms-line-title { font-size: 13.5px; font-weight: 700; color: var(--text); margin: 12px 0 8px; display: flex; align-items: baseline; gap: 6px; flex-wrap: wrap; }
.pms-line-title:first-of-type { margin-top: 0; }
.pms-line-title small { font-size: 11.5px; font-weight: 400; color: var(--muted); }
.tp-cache-tip { font-size: 10px; font-weight: 700; color: #16a34a; background: rgba(34,197,94,.14); border: 1px solid rgba(34,197,94,.32); border-radius: 999px; padding: 1px 7px; }
.pms-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(225px, 1fr)); gap: 12px; }
.pms-card {
  position: relative;
  border: 1px solid var(--border); border-radius: 14px; padding: 14px 15px;
  background: var(--card); display: flex; flex-direction: column; gap: 8px;
  transition: transform .18s ease, border-color .18s ease, box-shadow .18s ease;
}
.pms-card::before {
  content: ""; position: absolute; inset: -1px; border-radius: inherit; padding: 1px; pointer-events: none;
  background: linear-gradient(135deg, rgba(56,189,248,.55), rgba(129,140,248,.35) 45%, transparent 70%);
  -webkit-mask: linear-gradient(#fff 0 0) content-box, linear-gradient(#fff 0 0);
  -webkit-mask-composite: xor; mask-composite: exclude;
  opacity: 0; transition: opacity .2s ease;
}
.pms-card:hover { transform: translateY(-3px); box-shadow: 0 10px 26px -8px rgba(0,0,0,.35); }
.pms-card:hover::before { opacity: 1; }
.pms-card.paused { opacity: .62; }
.pms-top { display: flex; align-items: center; justify-content: space-between; gap: 8px; min-width: 0; }
.pms-id { font-family: var(--mono, monospace); font-size: 12px; color: var(--text); font-weight: 700; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
/* 完整模型 ID：前缀弱色显示（全文可见可复制，避免用户复制到不完整 ID） */
.pms-ns { color: var(--muted); font-weight: 600; }
/* 倍率/VIP 角标 */
.pms-rate-badge {
  flex: none; font-size: 11.5px; font-weight: 800; color: #fbbf24;
  background: linear-gradient(120deg, rgba(245,158,11,.25), rgba(251,191,36,.15));
  border: 1px solid rgba(251,191,36,.5); border-radius: 999px; padding: 2px 9px;
  text-shadow: 0 0 12px rgba(251,191,36,.4);
}
.pms-vip-badge { flex: none; font-size: 10.5px; font-weight: 800; color: #f59e0b; background: rgba(245,158,11,.16); border: 1px solid rgba(245,158,11,.4); border-radius: 999px; padding: 2px 8px; }
/* 实时状态灯行 */
.pms-live { display: flex; align-items: center; gap: 6px; font-size: 11.5px; font-weight: 600; }
.lv-dot { flex: none; width: 7px; height: 7px; border-radius: 50%; position: relative; }
.lv-ok .lv-dot { background: #22c55e; animation: dotpulse 2s infinite; }
.lv-degraded .lv-dot { background: #f59e0b; animation: dotpulse 2s infinite; }
.lv-down .lv-dot { background: #ef4444; animation: dotpulse 1.2s infinite; }
.lv-idle .lv-dot { background: rgba(148,163,184,.55); }
@keyframes dotpulse { 0% { box-shadow: 0 0 0 0 rgba(34,197,94,.45); } 70% { box-shadow: 0 0 0 6px rgba(34,197,94,0); } 100% { box-shadow: 0 0 0 0 rgba(34,197,94,0); } }
.pms-live .lv-text { color: var(--muted); }
.lv-ok .lv-text { color: #22c55e; }
.lv-degraded .lv-text { color: #f59e0b; }
.lv-down .lv-text { color: #ef4444; }
.lv-last { margin-left: auto; font-size: 10.5px; color: var(--muted); opacity: .8; }
/* 实时指标行：时延 / 速度 / 成功率 */
.pms-live-metrics {
  display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 4px;
  border-top: 1px dashed rgba(148,163,184,.25); padding-top: 8px; margin-top: auto;
}
.lm-item { display: flex; flex-direction: column; gap: 1px; min-width: 0; }
.lm-item i { font-style: normal; font-size: 10px; color: var(--muted); opacity: .85; }
.lm-item b { font-size: 12.5px; font-weight: 700; color: var(--text); font-variant-numeric: tabular-nums; white-space: nowrap; }
.lm-item b u { text-decoration: none; font-size: 9.5px; font-weight: 600; color: var(--muted); margin-left: 2px; }
.lm-item.dim b { color: var(--muted); opacity: .55; }
.pms-st { flex: none; font-size: 11px; color: #ef4444; background: rgba(239,68,68,.12); border: 1px solid rgba(239,68,68,.3); border-radius: 999px; padding: 2px 8px; }
.pms-price { display: flex; align-items: baseline; gap: 5px; }
.pms-price b { font-size: 21px; font-weight: 800; color: #f59e0b; font-variant-numeric: tabular-nums; }
.pms-price i { font-style: normal; font-size: 12px; color: var(--muted); }
/* 按量计费三段价 */
.pms-price-token { flex-direction: column; align-items: stretch; gap: 3px; }
.pms-price-token .tp-row { display: flex; align-items: baseline; gap: 6px; }
.pms-price-token .tp-row i { font-style: normal; font-size: 11.5px; color: var(--muted); min-width: 48px; }
.pms-price-token .tp-row b { font-size: 16.5px; font-weight: 800; color: #f59e0b; font-variant-numeric: tabular-nums; }
/* 官方原价划线对照 */
.pms-price-token .tp-row s { font-size: 11px; font-weight: 600; color: var(--muted); opacity: .75; }
.pms-price-token .tp-unit { font-size: 10.5px; min-width: 0; }
.pms-price-token .tp-floor { display: flex; align-items: center; gap: 6px; font-size: 11.5px; color: var(--muted); margin-top: 2px; padding-top: 5px; border-top: 1px dashed rgba(148,163,184,.28); }
.tp-subsidy { font-size: 10px; font-weight: 700; color: #16a34a; background: rgba(34,197,94,.14); border: 1px solid rgba(34,197,94,.32); border-radius: 999px; padding: 1px 7px; }
.pms-price-tag { margin-left: auto; font-size: 10.5px; font-weight: 700; border-radius: 999px; padding: 2px 8px; background: rgba(148,163,184,.15); color: var(--muted); }
.pms-price-tag.promo { background: linear-gradient(120deg, rgba(245,158,11,.2), rgba(251,191,36,.12)); color: #f59e0b; border: 1px solid rgba(245,158,11,.35); }
.pms-base-price { font-size: 11px; color: var(--muted); margin-top: 4px; padding-top: 4px; border-top: 1px dashed rgba(148,163,184,.22); }
.pms-base-price::before { content: "VIP "; font-weight: 700; color: #f59e0b; }
.pms-actions { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.pms-detail { display: inline-flex; align-items: center; gap: 3px; font-size: 12.5px; color: var(--accent); text-decoration: none; }
.pms-detail:hover { text-decoration: underline; }
.pms-detail svg { width: 13px; height: 13px; }
@media (max-width: 560px) {
  .pms-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
  .pms-card { padding: 11px 12px; }
  .pms-price b { font-size: 18px; }
  .pms-toolbar { gap: 8px; }
}

/* 实时脉冲点（专区头部） */
.live-pulse { width: 9px; height: 9px; border-radius: 50%; background: #22c55e; box-shadow: 0 0 0 0 rgba(34,197,94,.5); animation: livepulse 2s infinite; flex: none; }
.live-pulse.loading { background: #f59e0b; }
@keyframes livepulse { 0% { box-shadow: 0 0 0 0 rgba(34,197,94,.45); } 70% { box-shadow: 0 0 0 8px rgba(34,197,94,0); } 100% { box-shadow: 0 0 0 0 rgba(34,197,94,0); } }
.live-pulse.loading { animation: none; }

/* 免费与收费政策说明卡（用户必读） */
.paid-policy-card {
  border: 1px solid rgba(245, 158, 11, .38);
  background: linear-gradient(160deg, rgba(245,158,11,.07), rgba(251,191,36,.03) 55%, transparent);
  border-radius: 14px;
  padding: 18px 20px;
  margin: 14px 0 18px;
}
.paid-policy-card .pp-head { display: flex; align-items: center; gap: 10px; margin-bottom: 14px; }
.paid-policy-card .pp-ic { width: 34px; height: 34px; border-radius: 9px; display: flex; align-items: center; justify-content: center; background: rgba(245,158,11,.14); color: #f59e0b; flex: none; }
.paid-policy-card .pp-ic svg { width: 18px; height: 18px; }
.paid-policy-card .pp-head b { font-size: 16px; }
.paid-policy-card .pp-tag { font-size: 12px; color: #b45309; background: rgba(245,158,11,.16); border: 1px solid rgba(245,158,11,.3); border-radius: 999px; padding: 2px 10px; }
.paid-policy-card .pp-item { display: flex; gap: 12px; padding: 10px 0; }
.paid-policy-card .pp-item + .pp-item { border-top: 1px dashed rgba(128,140,160,.22); }
.paid-policy-card .pp-no { flex: none; width: 24px; height: 24px; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-size: 13px; font-weight: 700; margin-top: 2px; }
.paid-policy-card .pp-no-free { background: rgba(34,197,94,.15); color: #16a34a; }
.paid-policy-card .pp-no-paid { background: rgba(245,158,11,.18); color: #f59e0b; }
.paid-policy-card .pp-item > div { min-width: 0; }
.paid-policy-card .pp-item b { font-size: 14.5px; }
.paid-policy-card .pp-item p { margin: 6px 0 0; font-size: 13.5px; line-height: 1.75; color: var(--text-2, #64748b); }
.paid-policy-card .pp-rules { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 6px 16px; margin-top: 10px; }
.paid-policy-card .pp-rules span { font-size: 13px; color: var(--text-2, #64748b); padding-left: 18px; position: relative; }
.paid-policy-card .pp-rules span::before { content: "✓"; position: absolute; left: 0; color: #16a34a; font-weight: 700; }
.paid-policy-card .pp-rules span b { font-size: 13px; color: #d97706; }
@media (max-width: 640px) { .paid-policy-card { padding: 14px; } .paid-policy-card .pp-rules { grid-template-columns: 1fr; } }

/* ---- rc20 动效改版：交错入场编排 / 悬停微交互 / 状态极佳发光 / 横幅流光 ----
   视图由 v-if 切换重挂载，入场动画随每次切换自动重放——免费/收费/能力三视图自带过渡 */
@keyframes cardIn { from { opacity: 0; transform: translateY(14px) scale(.97); } to { opacity: 1; transform: none; } }
@keyframes secIn { from { opacity: 0; transform: translateY(10px); } to { opacity: 1; transform: none; } }
/* 卡片按序交错浮入（backwards 填充：动画结束后不锁定 transform，悬停上浮照常生效） */
.pms-card { animation: cardIn .45s cubic-bezier(.22, 1, .36, 1) backwards; animation-delay: calc(min(var(--i, 0), 14) * 45ms); }
.crowd-sec .cs-card { animation: cardIn .45s cubic-bezier(.22, 1, .36, 1) backwards; }
.crowd-sec .cs-card:nth-child(1) { animation-delay: 0ms; }
.crowd-sec .cs-card:nth-child(2) { animation-delay: 60ms; }
.crowd-sec .cs-card:nth-child(3) { animation-delay: 120ms; }
.crowd-sec .cs-card:nth-child(n + 4) { animation-delay: 180ms; }
/* 区块标题 / 横幅 / 政策卡入场 */
.pms-line-title, .rate-banner, .paid-policy-card, .crowd-sec { animation: secIn .5s cubic-bezier(.22, 1, .36, 1) backwards; }
.pms-line-title { animation-delay: 40ms; }
.paid-policy-card { animation-delay: .15s; }
/* 状态极佳：翡翠绿发光脉冲（区别于普通"运行正常"） */
.lv-great .lv-dot { background: #10b981; animation: greatpulse 1.8s infinite; }
.lv-great .lv-text { color: #10b981; }
@keyframes greatpulse {
  0% { box-shadow: 0 0 0 0 rgba(16, 185, 129, .5), 0 0 8px rgba(16, 185, 129, .85); }
  70% { box-shadow: 0 0 0 7px rgba(16, 185, 129, 0), 0 0 8px rgba(16, 185, 129, .85); }
  100% { box-shadow: 0 0 0 0 rgba(16, 185, 129, 0), 0 0 8px rgba(16, 185, 129, .85); }
}
/* 悬停微交互增强：更深上浮 + 青蓝描边光 + 价格行高亮 + 箭头滑动 */
.pms-card:hover { transform: translateY(-4px); box-shadow: 0 14px 32px -10px rgba(0, 0, 0, .45), 0 0 0 1px rgba(56, 189, 248, .18); }
.pms-price-token .tp-row { border-radius: 8px; padding: 1px 6px; margin: 0 -6px; transition: background .16s ease; }
.pms-card:hover .pms-price-token .tp-row:hover { background: rgba(56, 189, 248, .08); }
.pms-detail svg { transition: transform .18s ease; }
.pms-detail:hover svg { transform: translateX(3px); }
/* 倍率补贴角标呼吸辉光 */
.pms-rate-badge { animation: badgeGlow 2.4s ease-in-out infinite; }
@keyframes badgeGlow { 0%, 100% { box-shadow: 0 0 0 0 rgba(251, 191, 36, 0); } 50% { box-shadow: 0 0 14px 2px rgba(251, 191, 36, .3); } }
/* 倍率横幅背景缓移（叠于既有流光扫过之上） */
.rate-banner { background-size: 170% 170%; animation: secIn .5s cubic-bezier(.22, 1, .36, 1) backwards, rbdrift 9s ease-in-out infinite alternate; }
@keyframes rbdrift { from { background-position: 0% 0%; } to { background-position: 100% 100%; } }
/* 无障碍：偏好减弱动效时全部关闭 */
@media (prefers-reduced-motion: reduce) {
  .pms-card, .crowd-sec .cs-card, .pms-line-title, .rate-banner, .paid-policy-card, .crowd-sec,
  .pms-rate-badge, .pms-live .lv-dot { animation: none !important; }
}
</style>
