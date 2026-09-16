<script setup lang="ts">
/* 模型中心 · 免费模型 / 收费模型 / 能力总览 三视图
 * 接口对接（与旧版 1:1）：
 * - /v1/models（useModels.load，session:true，60 秒轮询 + 离线兜底）
 * - /v1/models/status（apiJson('/models/status')，实时时延/速度/成功率，20 秒轮询，静默失败）
 * - /v1/meta（apiJson('/meta')，rate_promo / rate_normal 计费倍率横幅，静默失败）
 * - /v1/pool/status（apiJson('/pool/status')，众筹站点额度状态，静默失败）
 * - 前端秒级 tick（促销倒计时） */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'
import { apiJson } from '@/composables/useApi'
import { useModels, type ModelRow } from '@/composables/useModels'
import { dsMaintenance, hideTag, platformLabel, typeLabel } from '@/composables/modelMeta'

/* ================= 视图切换：free 免费模型 / paid 收费模型 / cap 能力总览（URL ?view= 可直达分享） ================= */
const route = useRoute()
const router = useRouter()
const view = ref<'free' | 'paid' | 'cap'>(route.query.view === 'cap' ? 'cap' : route.query.view === 'paid' ? 'paid' : 'free')
watch(() => route.query.view, v => { view.value = v === 'cap' ? 'cap' : v === 'paid' ? 'paid' : 'free' })
function setView(v: 'free' | 'paid' | 'cap') {
  view.value = v
  router.replace({ query: v === 'free' ? {} : { view: v } })
}

/* ================= 模型列表：/v1/models（60 秒自动刷新） ================= */
const { models, loading, error, loadedAt, load } = useModels()
let refreshTimer = 0
let liveTimer = 0
let tickTimer = 0
onMounted(() => {
  load()
  refreshTimer = window.setInterval(() => load(true), 60000)
  /* 实时状态：卡片内嵌展示，20 秒轮询 */
  liveTimer = window.setInterval(loadLive, 20000)
  loadLive()
  loadRate()
})
onUnmounted(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
  if (liveTimer) window.clearInterval(liveTimer)
  if (tickTimer) window.clearInterval(tickTimer)
})
function retry() { load(true) }

/* ================= 实时状态：/v1/models/status 最近 200 次请求推断（20 秒自动刷新） ================= */
type LiveRow = { model: string; samples: number; ok: number; ok_rate: number; status: string; avg_latency_ms?: number; avg_first_ms?: number; avg_tps?: number; last_ts: number }
const liveRows = ref<LiveRow[]>([])
const liveTs = ref(0)
const liveLoading = ref(false)
async function loadLive() {
  liveLoading.value = true
  try {
    const j = await apiJson<{ data: LiveRow[]; generated_ts: number }>('/models/status')
    liveRows.value = (j.data || []).filter((x: LiveRow) => /^(aqua|acu|codex|tlk)\//.test(x.model))
    liveTs.value = j.generated_ts || 0
  } catch { /* 静默：下一轮自动重试 */ }
  liveLoading.value = false
}
const liveMap = computed(() => { const m: Record<string, LiveRow> = {}; for (const r of liveRows.value) m[r.model] = r; return m })
/* 展示口径：优先首字延迟（用户感知的响应速度），旧数据无首字段时回退总耗时 */
function latOf(r?: LiveRow): number {
  if (!r) return 0
  return r.avg_first_ms || r.avg_latency_ms || 0
}
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
/* 实时状态灯：great/ok 绿 / degraded 黄 / down 红 / 无数据待命中 */
function liveStatusOf(id: string): { key: 'great' | 'ok' | 'degraded' | 'down' | 'idle'; text: string; dot: 'ok' | 'warn' | 'bad' | '' } {
  const live = liveMap.value[id]
  if (!live) return { key: 'idle', text: '待命中', dot: '' }
  if (live.status === 'great') return { key: 'great', text: '状态极佳', dot: 'ok' }
  if (live.status === 'ok') return { key: 'ok', text: '运行正常', dot: 'ok' }
  if (live.status === 'degraded') return { key: 'degraded', text: '部分异常', dot: 'warn' }
  return { key: 'down', text: '故障', dot: 'bad' }
}

/* ================= 计费倍率横幅（/v1/meta 配置下发；促销到期自动隐藏） ================= */
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
  for (const m of paidModels.value) { const e = m.promoEndsAt || 0; if (e > t) t = e }
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

/* ================= 筛选状态 ================= */
const curPlatform = ref('all')
const curType = ref('all')
const q = ref('')
/* 筛选选项常量（模板直接消费） */
const PLATFORM_OPTS = [['all', '全部'], ['nvidia', 'Nvidia NIM'], ['acu', '官方自营']] as const
const TYPE_OPTS = [['all', '全部'], ['chat', '对话'], ['embedding', '向量'], ['rerank', '重排'], ['asr', '语音识别'], ['tts', '语音合成'], ['moderation', '风控'], ['vision', '视觉']] as const
const PAID_TYPE_OPTS = [['all', '全部'], ['chat', '对话'], ['image', '图片']] as const

/* ================= 智能排序：国产/大参数/高智能模型排前（旧 smartSort 平移） ================= */
function modelPriority(id: string): number {
  const l = id.toLowerCase()
  if (/^(aqua|acu)\//.test(l)) return -1
  if (/deepseek|qwen|qwq|chatglm|thudm|baichuan|01-ai|yi-large|zhipu|glm|bigmodel|kimi|moonshot|doubao|moonshotai/.test(l)) return 0
  if (/nemotron|nvidia.*llama-3\.[13]/.test(l)) return 1
  if (/meta\/llama-3\.[13]|meta\/llama-4/.test(l)) return 2
  if (/mistral-large|mistral-medium|mixtral/.test(l)) return 3
  if (/gemma/.test(l)) return 4
  if (/phi-4|phi-3\.5/.test(l)) return 5
  if (/phi-3/.test(l)) return 6
  return 8
}
function hotFlag(id: string): number {
  const l = id.toLowerCase()
  if (/v4|k3|k2\.6|k2|r1|v3|max|ultra|plus|large|turbo|pro|premium|72b|70b|405b|671b|32b|27b|236b/.test(l)) return 1
  return 0
}
function smartSort(a: string, b: string): number {
  const pa = modelPriority(a), pb = modelPriority(b)
  if (pa !== pb) return pa - pb
  const ha = hotFlag(a), hb = hotFlag(b)
  if (ha !== hb) return hb - ha
  const na = Number(a.match(/(\d+)b/i)?.[1] || 0)
  const nb = Number(b.match(/(\d+)b/i)?.[1] || 0)
  if (na !== nb) return nb - na
  return a.localeCompare(b)
}
/* auto（智能路由）置顶 */
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

/* ================= 状态 / 健康徽标（旧 statusOf / healthOf 平移，色彩映射为 .dot ok/warn/bad） ================= */
function statusOf(m: ModelRow) {
  if (m.id.toLowerCase() === 'auto') {
    return { cls: 'acc', text: '智能路由 · 快与稳优先', title: '每次请求实时选择当前最快最稳的模型，不保证命中同一个' }
  }
  if (dsMaintenance(m.id)) {
    return { cls: 'bad', text: '服务暂停 · 维护中', title: '官方自营通道维护中，已暂停服务' }
  }
  if (m.status === 'exhausted') {
    return { cls: 'bad', text: '额度已耗尽', title: m.status_msg || '今日免费额度已用尽' }
  }
  if (m.status === 'unavailable' || m.status === 'maintenance') {
    return {
      cls: 'warn',
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
function healthOf(m: ModelRow) {
  const h = m.health
  if (!h || !h.total) return null
  const score = h.score != null ? h.score | 0 : 0
  let dot: 'ok' | 'warn' | 'bad' = 'bad'
  if (score >= 90) dot = 'ok'
  else if (score >= 70) dot = 'ok'
  else if (score >= 50) dot = 'warn'
  const rate = Math.round(((h.ok || 0) / h.total) * 100)
  const lat = h.avg_latency_ms != null ? (h.avg_latency_ms / 1000).toFixed(1) + 's' : '-'
  return { score, dot, tip: `近 ${h.total} 次调用的健康评分：${score}/100（成功率 ${rate}%，平均延迟 ${lat}）` }
}

/* ================= 免费模型视图（收费模型隔离到收费页；acu/ 众筹模型进专区） ================= */
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

/* ================= 众筹专区（acu/ 前缀）+ /v1/pool/status ================= */
const poolStatus = ref<any>(null)
async function loadPool() {
  try { poolStatus.value = await apiJson<any>('/pool/status') } catch { /* 忽略 */ }
}
const poolAlive = computed(() => !!poolStatus.value && poolStatus.value.balance_micro > 0)
function isCrowd(id: string) { return id.startsWith('acu/') }
loadPool()

const crowdModels = computed(() => orderedModels.value
  .filter(m => isCrowd(m.id))
  .map(m => ({
    id: m.id,
    health: healthOf(m),
    st: statusOf(m),
    link: modelLink(m.id),
  })))
const CROWD_DESC: Record<string, { t: string; d: string }> = {
  'acu/deepseek-v4-flash': { t: 'DeepSeek 极速轻旗舰', d: '秒回级响应，高频轻任务首选' },
  'acu/deepseek-v4-pro': { t: 'DeepSeek 满血旗舰', d: '深度推理 · 长文创作 · 复杂任务扛把子' },
  'acu/doubao-seed-2.0-lite': { t: '豆包轻量版', d: '日常问答的性价比之王' },
  'acu/doubao-seed-2.1-turbo': { t: '豆包加速版', d: '速度与质量兼顾的全能选手' },
  'acu/doubao-seed-evolving': { t: '豆包自进化', d: '持续在线迭代，能力常用常新' },
  'acu/glm-5.3': { t: '智谱 GLM 旗舰', d: '中文创作与代码生成双优' },
  'acu/glm-5.3-flash': { t: 'GLM 极速版', d: '毫秒级响应，高并发场景利器' },
}
function crowdDesc(id: string) { return CROWD_DESC[id] || { t: '官方自营通道', d: '按次从站点额度扣费' } }

/* ================= 加载/同步状态提示条（旧 updateModelsNotice 平移） ================= */
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

/* ================= 收费模型（aqua/ 前缀；分组由密钥决定） ================= */
const paidModels = computed(() => orderedModels.value
  .filter(m => m.paid)
  .map(m => ({
    id: m.id,
    mode: m.mode || 'per_call',
    groups: (Array.isArray(m.groups) && m.groups.length)
      ? m.groups
      : [m.mode === 'per_token' ? 'per_token' : 'per_call'],
    price: m.price_micro ?? 0,
    inPrice: m.in_price ?? 0,
    cachePrice: m.cache_price ?? 0,
    outPrice: m.out_price ?? 0,
    floor: m.floor_micro ?? 0,
    /* VIP 专享：后端对 VIP 用户附原价（base_*）；普通用户无这些字段 */
    basePrice: (m as any).base_price_micro as number | undefined,
    baseInPrice: (m as any).base_in_price as number | undefined,
    baseCachePrice: (m as any).base_cache_price as number | undefined,
    baseOutPrice: (m as any).base_out_price as number | undefined,
    baseFloor: (m as any).base_floor_micro as number | undefined,
    perImage: (m as any).per_image as number | undefined,
    basePerImage: (m as any).base_per_image as number | undefined,
    subsidized: m.subsidized === true,
    promoEndsAt: m.promo_ends_at ?? 0,
    isImage: m.type === 'image',
    health: healthOf(m),
    st: statusOf(m),
    link: modelLink(m.id),
  })))
const paidCallLine = computed(() => paidModels.value.filter(m => m.groups.includes('per_call')))
/* codex/ 前缀（GPT · Codex 专线）走专属板块，不混入按量计费分组 */
const paidTokenLine = computed(() => paidModels.value.filter(m => m.groups.includes('per_token') && !m.id.startsWith('codex/')))

/* 收费页工具栏：搜索 + 类型筛选 + 排序 */
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
/* 官方中转专线（tlk/ 前缀，official 分组密钥专用：官方原价 6 折） */
const officialLineRows = computed(() => sortTokenLine(paidModels.value.filter(m => m.groups.includes('official'))))
/* GPT · Codex 专线（codex/ 前缀，ChatGPT 账号池：按量分段计价含缓存命中） */
const codexLineRows = computed(() => sortTokenLine(paidModels.value.filter(m => m.id.startsWith('codex/'))))

/* ================= 微元 → 元 / 元每百万 tokens 显示 ================= */
function microYuan(v?: number): string {
  if (v == null) return '--'
  return (v / 1e6).toFixed(6).replace(/0+$/, '').replace(/\.$/, '')
}
function perMYuan(v?: number): string {
  if (v == null) return '--'
  return v.toFixed(4).replace(/0+$/, '').replace(/\.$/, '')
}
/* 官方原价 → 现价 的实际倍率角标（数据驱动；非促销无对照不显示） */
function rateBadgeOf(m: { baseInPrice?: number | null; basePerImage?: number | null; inPrice: number; perImage?: number | null }): string {
  const base = m.baseInPrice ?? m.basePerImage
  const cur = m.inPrice ?? m.perImage
  if (!base || !cur) return ''
  const v = Math.round((cur / base) * 100) / 100
  if (v <= 0 || v >= 1) return ''
  return (v < 0.095 ? v.toFixed(2) : v.toFixed(1)) + '×'
}

/* ================= 工具条共享搜索（三个视图各持关键词，切换不丢） ================= */
const search = computed({
  get: () => (view.value === 'paid' ? paidQ.value : view.value === 'cap' ? capKw.value : q.value),
  set: (v: string) => {
    if (view.value === 'paid') paidQ.value = v
    else if (view.value === 'cap') capKw.value = v
    else q.value = v
  },
})
const resultCount = computed(() => view.value === 'paid' ? paidModels.value.length : view.value === 'cap' ? viewCards.value.length : viewRows.value.length)

/* ================= 展开行（免费卡详情） ================= */
const expandedId = ref('')
function toggle(id: string) { expandedId.value = expandedId.value === id ? '' : id }

/* ================= 能力总览视图（/models?view=cap，逻辑与旧 CapabilitiesPage 1:1） ================= */
/* 模型精确规格（按 ID 关键词匹配）：ctx=上下文上限 size=参数量 dims=向量维度 released=发布日期 */
interface ModelSpec { re: RegExp; ctx?: string; size?: string; dims?: number; released?: string }
const MODEL_SPECS: ModelSpec[] = [
  { re: /aqua\/deepseek-v4-flash$/, ctx: "1M", size: "284B (13B active)", released: "2026-07" },
  { re: /aqua\/deepseek-v4-pro$/, ctx: "1M", size: "1.6T (49B active)", released: "2026-08" },
  { re: /aqua\/glm-5\.3-flash$/, ctx: "1M", size: "320B (18B active)", released: "2026-08" },
  { re: /aqua\/glm-5\.3$/, ctx: "1M", size: "744B (40B active)", released: "2026-08" },
  { re: /aqua\/glm-5\.2$/, ctx: "1M", size: "744B (40B active)", released: "2026-06" },
  { re: /llama-3\.1-405b/, ctx: "128K", size: "405B", released: "2024-12" },
  { re: /llama-3\.1-70b-instruct/, ctx: "128K", size: "70B", released: "2024-07" },
  { re: /llama-3\.1-8b-instruct/, ctx: "128K", size: "8B", released: "2024-07" },
  { re: /llama-3\.3-70b/, ctx: "128K", size: "70B", released: "2024-12" },
  { re: /llama-3\.2-1b/, ctx: "128K", size: "1B", released: "2024-09" },
  { re: /llama-3\.2-3b/, ctx: "128K", size: "3B", released: "2024-09" },
  { re: /llama-3\.2-90b-vision/, ctx: "128K", size: "90B", released: "2024-09" },
  { re: /llama-3\.2-11b-vision/, ctx: "128K", size: "11B", released: "2024-09" },
  { re: /llama3-70b/, ctx: "8K", size: "70B", released: "2024-03" },
  { re: /llama3-8b/, ctx: "8K", size: "8B", released: "2024-03" },
  { re: /llama2-70b/, ctx: "4K", size: "70B", released: "2023-07" },
  { re: /codellama-70b/, ctx: "16K", size: "70B", released: "2023-08" },
  { re: /llama-guard-4/, ctx: "8K", size: "12B", released: "2024-12" },
  { re: /muse-glimmer/, ctx: "256K", size: "30B", released: "2024-06" },
  { re: /nemotron-ultra-253b/, ctx: "128K", size: "253B", released: "2025-01" },
  { re: /nemotron-4-340b-instruct/, ctx: "4K", size: "340B", released: "2024-05" },
  { re: /nemotron-3-super-120b/, ctx: "1M", size: "120B (12B active)", released: "2026-06" },
  { re: /nemotron-3-ultra-550b/, ctx: "1M", size: "550B (55B active)", released: "2026-06" },
  { re: /nemotron-3-nano-omni/, ctx: "256K", size: "30B (3.6B active)", released: "2026-05" },
  { re: /nemotron-3-nano-30b/, ctx: "1M", size: "30B (3.6B active)", released: "2026-03" },
  { re: /nemotron-nano-3-30b/, ctx: "1M", size: "30B (3.6B active)", released: "2026-03" },
  { re: /nemotron-3\.5-lightning/, ctx: "1M", size: "30B (3B active)", released: "2026-08" },
  { re: /nemotron-3\.5-content-safety/, ctx: "1M", size: "8B", released: "2026-08" },
  { re: /nemotron-4-340b-reward/, ctx: "4K", size: "340B", released: "2024-05" },
  { re: /nemotron-3-embed-1b/, ctx: "0.5K", size: "1B", dims: 2048, released: "2025-06" },
  { re: /nemotron-parse/, ctx: "0.5K", size: "—", released: "2024-10" },
  { re: /llama-3\.3-nemotron-super-49b/, ctx: "128K", size: "49B", released: "2024-12" },
  { re: /llama-3\.1-nemotron-70b/, ctx: "128K", size: "70B", released: "2024-10" },
  { re: /llama-3\.1-nemotron-51b/, ctx: "128K", size: "51B", released: "2024-09" },
  { re: /llama-3\.1-nemotron-safety-guard/, ctx: "128K", size: "8B", released: "2024-10" },
  { re: /nemoguard-8b/, ctx: "128K", size: "8B", released: "2024-10" },
  { re: /llama3-chatqa-1\.5-70b/, ctx: "32K", size: "70B", released: "2024-06" },
  { re: /llama3-chatqa-1\.5-8b/, ctx: "32K", size: "8B", released: "2024-06" },
  { re: /mistral-nemo-minitron-8b-8k/, ctx: "8K", size: "8B", released: "2024-08" },
  { re: /ising-calibration/, ctx: "4K", size: "31B", released: "2024-11" },
  { re: /ai-synthetic-video-detector/, ctx: "—", size: "—", released: "2024-08" },
  { re: /cosmos-reason2-8b/, ctx: "16K", size: "8B", released: "2025-01" },
  { re: /nv-embed-v1|nv-embedcode/, ctx: "0.5K", size: "7B", dims: 2048, released: "2024" },
  { re: /embed-qa-4/, ctx: "0.5K", size: "4B", dims: 1024, released: "2024" },
  { re: /nv-embedqa-mistral-7b/, ctx: "0.5K", size: "7B", dims: 1024, released: "2024" },
  { re: /nv-embedqa-1b-v1/, ctx: "0.5K", size: "1B", dims: 1024, released: "2024" },
  { re: /nemoretriever-1b/, ctx: "0.5K", size: "1B", dims: 1024, released: "2024-10" },
  { re: /nemotron-embed-vl-1b/, ctx: "0.5K", size: "1B", dims: 1024, released: "2024-11" },
  { re: /arctic-embed-l/, ctx: "0.5K", size: "335M", dims: 1024, released: "2024-04" },
  { re: /neva-22b/, ctx: "4K", size: "22B", released: "2023-11" },
  { re: /vila/, ctx: "4K", size: "8B", released: "2024-02" },
  { re: /nvclip/, ctx: "0.5K", size: "1.3B", released: "2024-06" },
  { re: /riva-translate/, ctx: "0.5K", size: "4B", released: "2024-03" },
  { re: /deepseek-r1\/|deepseek-r1-distill-qwen-32b/, ctx: "128K", size: "671B (MoE)", released: "2025-01" },
  { re: /deepseek-r1-distill-qwen-14b/, ctx: "128K", size: "14B", released: "2025-01" },
  { re: /deepseek-r1-distill-qwen-7b/, ctx: "128K", size: "7B", released: "2025-01" },
  { re: /deepseek-r1-distill-llama-8b/, ctx: "128K", size: "8B", released: "2025-01" },
  { re: /deepseek-coder/, ctx: "16K", size: "6.7B", released: "2023-11" },
  { re: /deepseek-ai\/deepseek-v4-flash/, ctx: "1M", size: "284B (13B active)", released: "2026-04" },
  { re: /deepseek-v4-pro/, ctx: "1M", size: "1.6T (49B active)", released: "2026-04" },
  { re: /qwq-32b/, ctx: "32K", size: "32B", released: "2024-11" },
  { re: /qwen2\.5-coder-32b/, ctx: "32K", size: "32B", released: "2024-11" },
  { re: /qwen2\.5-coder-7b/, ctx: "128K", size: "7B", released: "2024-11" },
  { re: /qwen2\.5-7b/, ctx: "32K", size: "7B", released: "2024-09" },
  { re: /qwen2-7b/, ctx: "32K", size: "7B", released: "2024-09" },
  { re: /gemma-4-31b/, ctx: "128K", size: "31B", released: "2025-06" },
  { re: /gemma-3-27b/, ctx: "128K", size: "27B", released: "2025-02" },
  { re: /gemma-3-12b/, ctx: "128K", size: "12B", released: "2025-02" },
  { re: /gemma-3-4b/, ctx: "128K", size: "4B", released: "2025-02" },
  { re: /gemma-3-1b/, ctx: "128K", size: "1B", released: "2025-02" },
  { re: /gemma-2-27b/, ctx: "8K", size: "27B", released: "2024-06" },
  { re: /gemma-2-9b/, ctx: "8K", size: "9B", released: "2024-06" },
  { re: /gemma-2-2b/, ctx: "8K", size: "2B", released: "2024-06" },
  { re: /gemma-2b$/, ctx: "8K", size: "2B", released: "2024-02" },
  { re: /codegemma/, ctx: "14K", size: "7B", released: "2024-04" },
  { re: /deplot/, ctx: "2K", size: "1.3B", released: "2023-10" },
  { re: /diffusiongemma/, ctx: "—", size: "26B", released: "2025-05" },
  { re: /recurrentgemma/, ctx: "8K", size: "2B", released: "2024-02" },
  { re: /phi-4-mini/, ctx: "16K", size: "3.8B", released: "2024-12" },
  { re: /phi-3\.5-mini/, ctx: "128K", size: "3.8B", released: "2024-08" },
  { re: /phi-3\.5-moe/, ctx: "128K", size: "42B (MoE)", released: "2024-08" },
  { re: /phi-3-medium/, ctx: "128K", size: "14B", released: "2024-06" },
  { re: /phi-3-small/, ctx: "8K", size: "7B", released: "2024-05" },
  { re: /phi-3-mini/, ctx: "128K", size: "3.8B", released: "2024-04" },
  { re: /phi-3-vision/, ctx: "128K", size: "4.2B", released: "2024-05" },
  { re: /phi-4-multimodal/, ctx: "16K", size: "5.6B", released: "2024-12" },
  { re: /kosmos-2/, ctx: "4K", size: "1.6B", released: "2023-06" },
  { re: /mistral-large/, ctx: "128K", size: "123B", released: "2024-07" },
  { re: /mistral-small-3\.1/, ctx: "96K", size: "24B", released: "2025-03" },
  { re: /mistral-small-24b/, ctx: "96K", size: "24B", released: "2025-03" },
  { re: /mixtral-8x22b/, ctx: "64K", size: "141B (MoE)", released: "2024-04" },
  { re: /mistral-7b/, ctx: "32K", size: "7B", released: "2023-09" },
  { re: /codestral-22b/, ctx: "32K", size: "22B", released: "2024-02" },
  { re: /mistral-nemotron/, ctx: "128K", size: "70B", released: "2024-10" },
  { re: /mamba-codestral/, ctx: "256K", size: "7B", released: "2024-07" },
  { re: /mathstral/, ctx: "32K", size: "7B", released: "2024-07" },
  { re: /mistral-nemo-12b/, ctx: "128K", size: "12B", released: "2024-07" },
  { re: /granite-34b-code/, ctx: "8K", size: "34B", released: "2024-05" },
  { re: /granite-3\.0-8b/, ctx: "8K", size: "8B", released: "2024-10" },
  { re: /granite-3\.0-3b/, ctx: "4K", size: "3B", released: "2024-10" },
  { re: /granite-8b-code/, ctx: "8K", size: "8B", released: "2024-05" },
  { re: /granite-guardian/, ctx: "8K", size: "8B", released: "2024-10" },
  { re: /yi-large/, ctx: "32K", size: "34B", released: "2024-05" },
  { re: /fuyu-8b/, ctx: "16K", size: "8B", released: "2023-10" },
  { re: /jamba-1\.5-large/, ctx: "256K", size: "398B (MoE)", released: "2024-08" },
  { re: /jamba-1\.5-mini/, ctx: "256K", size: "52B (MoE)", released: "2024-08" },
  { re: /sea-lion-7b/, ctx: "3K", size: "7B", released: "2024-02" },
  { re: /starcoder2-15b/, ctx: "16K", size: "15B", released: "2024-04" },
  { re: /starcoder2-7b/, ctx: "16K", size: "7B", released: "2024-04" },
  { re: /dbrx-instruct/, ctx: "32K", size: "132B (MoE)", released: "2024-03" },
  { re: /colosseum_355b/, ctx: "16K", size: "355B", released: "2024-06" },
  { re: /italia_10b/, ctx: "16K", size: "10B", released: "2024-06" },
  { re: /palmyra-creative-122b/, ctx: "16K", size: "122B", released: "2024-05" },
  { re: /palmyra-fin-70b-32k/, ctx: "32K", size: "70B", released: "2024-03" },
  { re: /palmyra-med-70b-32k/, ctx: "32K", size: "70B", released: "2024-03" },
  { re: /palmyra-med-70b$/, ctx: "4K", size: "70B", released: "2024-03" },
  { re: /dracarys-llama/, ctx: "128K", size: "70B", released: "2024-08" },
  { re: /swallow-70b/, ctx: "8K", size: "70B", released: "2024-03" },
  { re: /swallow-8b/, ctx: "8K", size: "8B", released: "2024-03" },
  { re: /llama-3-swallow/, ctx: "8K", size: "70B", released: "2024-03" },
  { re: /breeze-7b/, ctx: "4K", size: "7B", released: "2024-04" },
  { re: /solar-10\.7b/, ctx: "4K", size: "11B", released: "2023-12" },
  { re: /rakutenai/, ctx: "4K", size: "7B", released: "2024-01" },
  { re: /falcon3-7b/, ctx: "32K", size: "7B", released: "2024-12" },
  { re: /llama-3-taiwan/, ctx: "8K", size: "70B", released: "2024-04" },
  { re: /zamba2-7b/, ctx: "4K", size: "7B", released: "2024-10" },
  { re: /usdcode/, ctx: "128K", size: "70B", released: "2024-08" },
  { re: /gpt-oss-120b/, ctx: "128K", size: "120B", released: "2025-08" },
  { re: /gpt-oss-20b/, ctx: "128K", size: "20B", released: "2025-08" },
  { re: /kimi-k2\.6/, ctx: "128K", size: "~200B (MoE)", released: "2025-07" },
  { re: /kimi-k3/, ctx: "128K", size: "~260B (MoE)", released: "2025-08" },
  { re: /minimax-m3/, ctx: "1M", size: "428B (~22B active)", released: "2025-08" },
  { re: /laguna-xs/, ctx: "8K", size: "—", released: "2025-06" },
  { re: /step-3\.7-flash/, ctx: "128K", size: "~30B", released: "2025-07" },
  { re: /bge-m3|baai\/bge-m3/, ctx: "8K", size: "568M", dims: 1024, released: "2024-01" },
  { re: /bge-large-zh/, ctx: "0.5K", size: "326M", dims: 1024, released: "2023-06" },
  { re: /bge-large-en/, ctx: "0.5K", size: "326M", dims: 1024, released: "2023-06" },
  { re: /bge-reranker-v2-m3/, ctx: "8K", size: "568M", dims: 0, released: "2024-01" },
  { re: /bce-reranker/, ctx: "0.5K", size: "278M", dims: 0, released: "2023-09" },
]
function modelSpec(id: string): ModelSpec {
  for (const s of MODEL_SPECS) if (s.re.test(id)) return s
  return {}
}
/* 能力列定义 + 类型→能力推导（旧 CAPS / capForType / capMini 平移） */
const CAPS = [
  { id: 'chat', label: '对话', tip: '支持 chat/completions 多轮对话' },
  { id: 'stream', label: '流式', tip: '支持流式逐字返回' },
  { id: 'vision', label: '视觉', tip: '可输入图片理解内容' },
  { id: 'audio', label: '音频', tip: '支持音频输入或输出' },
  { id: 'emb', label: '向量', tip: '支持 embeddings 接口' },
  { id: 'rerank', label: '重排', tip: '支持 rerank 检索精排' },
  { id: 'gen', label: '生成', tip: '图像/视频/语音内容生成' },
  { id: 'tools', label: '工具', tip: '支持 function calling' },
]
function capForType(t: string): Record<string, number> {
  const c: Record<string, number> = {}
  c.chat = (t === 'chat' || t === 'vision') ? 1 : 0
  c.stream = (t === 'chat' || t === 'vision') ? 1 : 0
  c.vision = t === 'vision' ? 1 : 0
  c.audio = (t === 'asr' || t === 'tts') ? 1 : 0
  c.emb = t === 'embedding' ? 1 : 0
  c.rerank = t === 'rerank' ? 1 : 0
  c.gen = (t === 'image' || t === 'video' || t === 'tts') ? 1 : 0
  c.tools = (t === 'chat') ? 0.5 : 0
  return c
}
function capMini(v: number) {
  if (v === 1) return { cls: 'yes', title: '支持', sym: 'check' }
  if (v === 0.5) return { cls: 'part', title: '有限支持', sym: '' }
  return { cls: 'no', title: '不支持', sym: 'cross' }
}
/* 筛选 + 卡片视图 */
const curCap = ref('all')
const capFilters = [
  { id: 'all', label: '全部' }, { id: 'chat', label: '对话' }, { id: 'vision', label: '视觉' },
  { id: 'embedding', label: '向量' }, { id: 'rerank', label: '重排' }, { id: 'asr', label: '语音识别' },
  { id: 'tts', label: '语音合成' }, { id: 'moderation', label: '风控' }, { id: 'image', label: '绘图' },
  { id: 'video', label: '视频' }, { id: 'ip', label: 'IP' },
]
const capKw = ref('')
interface CapFlag { cls: string; text: string }
interface CapCard {
  id: string; platform: string; type: string
  stTag: CapFlag | null; acu: CapFlag | null
  metrics: { label: string; value: string }[]
  typeText: string; platformText: string
  icons: { label: string; tip: string; mini: { cls: string; title: string; sym: string } }[]
}
const viewCards = computed<CapCard[]>(() => {
  const k = capKw.value.toLowerCase().trim()
  return models.value
    .filter(m => {
      if (curCap.value !== 'all' && m.type !== curCap.value) return false
      return k ? m.id.toLowerCase().indexOf(k) !== -1 : true
    })
    .map(m => {
      const spec = modelSpec(m.id)
      const c = capForType(m.type)
      /* 官方自营专线（aqua/）官方支持 function calling，请求体原样透传 */
      if (m.platform === 'acu') c.tools = 1
      let stTag: CapFlag | null = null
      if (m.id.toLowerCase() === 'auto') stTag = { cls: 'acc', text: '智能路由' }
      else if (dsMaintenance(m.id)) stTag = { cls: 'bad', text: '维护中' }
      else if (m.status === 'exhausted') stTag = { cls: 'bad', text: '耗尽' }
      else if (m.status === 'unavailable') stTag = { cls: 'warn', text: '不可用' }
      const acu = m.platform === 'acu' ? { cls: 'acc', text: '官方自营' } : null
      const metrics: { label: string; value: string }[] = []
      if (spec.ctx) metrics.push({ label: '上下文', value: spec.ctx })
      if (spec.size) metrics.push({ label: '参数', value: spec.size })
      if (spec.dims) metrics.push({ label: '向量', value: spec.dims + 'D' })
      if (spec.released) metrics.push({ label: '发布', value: spec.released })
      const icons = CAPS.map(cp => ({ label: cp.label, tip: cp.label + ': ' + cp.tip, mini: capMini(c[cp.id]) }))
      return { id: m.id, platform: m.platform, type: m.type, stTag, acu, metrics, typeText: typeLabel(m.type), platformText: platformLabel(m.platform), icons }
    })
})
/* 统计区（随当前筛选联动） */
const capStats = computed(() => {
  const rows = viewCards.value
  const byType: Record<string, number> = {}
  const capCounts: Record<string, number> = {}
  CAPS.forEach(c => { capCounts[c.id] = 0 })
  rows.forEach(r => {
    byType[r.type] = (byType[r.type] || 0) + 1
    const c = capForType(r.type)
    CAPS.forEach(cp => { if (c[cp.id] === 1) capCounts[cp.id] += 1 })
  })
  const total = rows.length
  const typePills = Object.keys(byType).map(t => ({ count: byType[t], label: typeLabel(t) }))
  const bars = CAPS.filter(c => capCounts[c.id] > 0).map(c => ({ label: c.label, pct: total ? Math.round(capCounts[c.id] / total * 100) : 0 }))
  return { total, typeCount: total ? Object.keys(byType).length : 0, typePills, bars }
})

function modelLink(id: string) { return '/model/' + encodeURIComponent(id) }
</script>

<template>
  <div class="wrap">
    <div class="page-head">
      <div>
        <h1><AqIcon name="box" :size="22" />模型中心</h1>
        <div class="sub">全线模型一览与能力总览：由 Nvidia NIM 与官方自营专线实时提供 · 列表每 60 秒 / 实时状态每 20 秒自动刷新</div>
      </div>
      <div class="ops">
        <span class="tag"><span class="dot" :class="liveLoading ? 'warn' : 'ok'"></span>{{ liveTs ? new Date(liveTs * 1000).toLocaleTimeString() : '--' }}</span>
        <button class="btn sm" :disabled="liveLoading" @click="loadLive()"><AqIcon name="refresh" :size="13" />刷新状态</button>
      </div>
    </div>

    <!-- ============ sticky 工具条：视图分组 + 搜索 ============ -->
    <div class="toolbar">
      <div class="chips">
        <button class="chip" :class="{ on: view === 'free' }" @click="setView('free')">免费模型</button>
        <button class="chip" :class="{ on: view === 'paid' }" @click="setView('paid')">收费模型</button>
        <button class="chip" :class="{ on: view === 'cap' }" @click="setView('cap')">能力总览</button>
      </div>
      <input v-model="search" type="text" class="input tool-search" :placeholder="view === 'paid' ? '搜索收费模型，如 deepseek / glm / qwen …' : view === 'cap' ? '在能力总览中搜索模型…' : '搜索模型名称，如 llama / deepseek / gemma …'">
      <span class="tag">共 {{ resultCount }} 个模型</span>
    </div>

    <div class="fade-up">
      <!-- ================================================== 免费模型视图 ================================================== -->
      <template v-if="view === 'free'">
        <div class="row wrap mt16" style="gap: 8px 14px;">
          <div class="row" style="gap: 6px;">
            <span class="dim" style="font-size: 12.5px;">平台</span>
            <button v-for="p in PLATFORM_OPTS" :key="p[0]" class="chip" :class="{ on: curPlatform === p[0] }" @click="curPlatform = p[0]">{{ p[1] }}</button>
          </div>
          <div class="row wrap" style="gap: 6px;">
            <span class="dim" style="font-size: 12.5px;">类型</span>
            <button v-for="t in TYPE_OPTS" :key="t[0]" class="chip" :class="{ on: curType === t[0] }" @click="curType = t[0]">{{ t[1] }}</button>
          </div>
        </div>

        <!-- 众筹专区：/v1/pool/status 站点额度状态 -->
        <div v-if="crowdModels.length" class="card mt16 crowd" :class="{ off: poolStatus && !poolAlive }">
          <div class="row wrap between">
            <b><AqIcon name="coin" :size="17" />众筹公共模型 · 官方自营通道</b>
            <div class="row wrap" style="gap: 8px;">
              <span class="dot" :class="poolAlive ? 'ok' : 'bad'"></span>
              <span class="dim">{{ poolStatus ? (poolAlive ? '站点额度可用' : '站点额度已用完 · 等待充值复活') : '状态同步中…' }}</span>
              <span v-if="poolStatus" class="tag">站点额度 ¥{{ (poolStatus.balance_micro / 1e6).toFixed(2).replace(/\.00$/, '') }}</span>
              <router-link to="/pool" class="btn ghost sm">账本与榜单 <AqIcon name="arrow-right" :size="13" /></router-link>
            </div>
          </div>
          <p class="dim mt8" style="font-size: 12.5px;">
            acu/ 前缀模型<b>按次</b>从<b>众筹站点额度</b>扣费（与生成长度无关）——无需充值即可调用，任何分组密钥可用，个人余额分文不动；
            站点额度充值 1:1 到账，见底即暂停，充值立刻复活。
          </p>
          <div class="grid3 mt12">
            <div v-for="m in crowdModels" :key="m.id" class="crowd-item">
              <div class="row between">
                <router-link :to="m.link" class="mono ci-id" :title="m.id">{{ m.id }}</router-link>
                <CopyBtn :text="m.id" />
              </div>
              <div class="mt8">
                <b class="ci-t">{{ crowdDesc(m.id).t }}</b>
                <div class="dim" style="font-size: 11.5px;">{{ crowdDesc(m.id).d }}</div>
              </div>
              <div class="row wrap mt8" style="gap: 6px;">
                <span class="tag grad">按次计费</span>
                <span v-if="m.health" class="tag"><span class="dot" :class="m.health.dot"></span>健康 {{ m.health.score }}</span>
                <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
              </div>
            </div>
            <div class="crowd-item cta">
              <b>充值扩容</b>
              <p class="dim" style="font-size: 12px;">充值 1:1 注入站点额度——给所有人的公共算力扩容，无最低限制；额度归零后第一笔充值自动登上荣誉墙。</p>
              <router-link to="/pool" class="btn primary sm">去充值</router-link>
            </div>
          </div>
        </div>

        <!-- 同步状态提示条 -->
        <div v-if="noticeVisible" class="banner mt16" :class="{ bad: !!error }">
          <AqIcon :name="error ? 'alert' : 'check'" :size="15" />
          <span>{{ noticeMsg }}</span>
          <button v-if="error" class="btn xs" style="margin-left: auto;" @click="retry">重试</button>
        </div>

        <!-- 三态：骨架 / 空态 / 模型卡（点击展开详情行） -->
        <div v-if="loading && !models.length" class="grid3 mt16">
          <div v-for="i in 6" :key="i" class="card">
            <div class="skeleton" style="min-height: 15px; width: 62%;"></div>
            <div class="skeleton mt12" style="min-height: 12px; width: 88%;"></div>
            <div class="skeleton mt8" style="min-height: 12px; width: 40%;"></div>
          </div>
        </div>
        <div v-else-if="!viewRows.length" class="card mt16">
          <div class="empty"><div class="big"><AqIcon name="box" :size="40" /></div><b>未找到匹配的模型</b><div class="dim">换个关键词或清除筛选试试</div></div>
        </div>
        <div v-else class="grid3 mt16">
          <div v-for="r in viewRows" :key="r.row.id" class="card hoverable mcard" :class="{ exhausted: r.exhausted }" @click="toggle(r.row.id)">
            <div class="row between">
              <router-link :to="modelLink(r.row.id)" class="mono mid" :title="'查看 ' + r.row.id + ' 详情'" @click.stop>{{ r.row.id }}</router-link>
              <AqIcon name="chevron-down" :size="15" class="caret" :class="{ open: expandedId === r.row.id }" />
            </div>
            <div class="row wrap mt8" style="gap: 6px;">
              <span v-if="r.row.id.toLowerCase() === 'auto'" class="tag acc">智能路由</span>
              <span v-if="!hideTag(r.row.type)" class="tag">{{ platformLabel(r.row.platform) }} · {{ typeLabel(r.row.type) }}</span>
              <span v-if="r.st" class="tag" :class="r.st.cls" :title="r.st.title">{{ r.st.text }}</span>
              <span v-if="r.health" class="tag" :title="r.health.tip"><span class="dot" :class="r.health.dot"></span>健康 {{ r.health.score }}</span>
            </div>
            <div v-if="expandedId === r.row.id" class="mt12 expand" @click.stop>
              <div class="dim" style="font-size: 12.5px;">{{ r.st?.title || r.row.status_msg || r.health?.tip || '该模型运行正常 · 复制 ID 即可在任意 OpenAI 客户端调用' }}</div>
              <div class="row wrap mt12">
                <CopyBtn :text="r.row.id" />
                <router-link :to="modelLink(r.row.id)" class="btn sm">能力详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
            <div v-else class="row between mt12">
              <CopyBtn :text="r.row.id" />
              <span class="dim" style="font-size: 11.5px;">点击卡片展开</span>
            </div>
          </div>
        </div>
        <p class="dim mt12" style="font-size: 12.5px;">免费模型由 Nvidia NIM 提供——注册即可使用、永久免费；点击模型 ID 查看详细能力说明与支持参数。</p>
      </template>

      <!-- ================================================== 收费模型视图 ================================================== -->
      <template v-else-if="view === 'paid'">
        <!-- 倍率横幅（/v1/meta 下发；促销到期自动隐藏） -->
        <div v-if="rateBannerVisible" class="card accent rate-card mt16">
          <div class="row wrap between" style="gap: 14px;">
            <div class="row" style="gap: 12px;">
              <span class="tag grad rate-big num">{{ ratePromo }}×</span>
              <div>
                <b>官方补贴进行中 · 全场按量计费 {{ ratePromo }} 倍率</b>
                <div class="dim" style="font-size: 12.5px;">现在按 {{ ratePromo }} 倍率计费，活动结束后恢复 {{ rateNormal }} 倍率——恢复前价格不变，越早用越便宜。</div>
              </div>
            </div>
            <div style="text-align: right;">
              <div class="dim" style="font-size: 11.5px;">距恢复 {{ rateNormal }}×</div>
              <b class="num" style="font-size: 20px;">{{ promoLeftStr }}</b>
              <div class="dim" style="font-size: 11.5px;">{{ promoEndsStr }} 自动恢复</div>
            </div>
          </div>
        </div>

        <div class="row wrap between mt16" style="gap: 8px;">
          <div class="chips">
            <button v-for="t in PAID_TYPE_OPTS" :key="t[0]" class="chip" :class="{ on: paidType === t[0] }" @click="paidType = t[0]">{{ t[1] }}</button>
          </div>
          <div class="chips">
            <button class="chip" :class="{ on: paidSort === 'smart' }" title="旗舰与国产模型优先" @click="paidSort = 'smart'">智能排序</button>
            <button class="chip" :class="{ on: paidSort === 'price' }" title="按输出单价从低到高" @click="paidSort = 'price'">价格优先</button>
          </div>
        </div>

        <div v-if="!paidModels.length" class="card mt16">
          <div v-if="loading" class="grid3">
            <div v-for="i in 3" :key="i"><div class="skeleton" style="min-height: 120px;"></div></div>
          </div>
          <div v-else class="empty"><div class="big"><AqIcon name="coin" :size="40" /></div><b>收费模型加载中或暂未在售</b><div class="dim">在售清单以 /v1/models 实时下发为准 · 每分钟自动刷新</div></div>
        </div>

        <!-- 按量计费分组 -->
        <section v-if="tokenLineRows.length" class="mt16">
          <div class="line-title">按量计费分组<small>密钥选「免费 + 按量计费」时可用：输入 / 缓存命中 / 输出分段计价，无保底，先付后用</small></div>
          <div class="grid3">
            <div v-for="m in tokenLineRows" :key="m.id" class="card hoverable pcard" :class="{ paused: !!m.st }">
              <div class="row between">
                <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                <span v-if="m.id === 'tlinks/deepseek-flash'" class="tag grad" title="DeepSeek V4.1 Flash 同源架构 · 极速轻量实验通道">DeepSeek V4.1 Flash 同源</span>
                <span v-else-if="m.subsidized && rateBadgeOf(m)" class="tag warn num" title="官方原价 × 补贴倍率 = 现价">{{ rateBadgeOf(m) }}</span>
                <span v-else-if="m.baseInPrice != null || m.basePerImage != null" class="tag warn" title="VIP 专享拿货价已生效">VIP</span>
              </div>
              <div class="row mt8" style="gap: 7px; font-size: 12px;">
                <span class="dot" :class="liveStatusOf(m.id).dot"></span>
                <span>{{ liveStatusOf(m.id).text }}</span>
                <span class="dim" style="margin-left: auto; font-size: 11px;">{{ liveMap[m.id] ? fmtAgo(liveMap[m.id].last_ts) : '' }}</span>
              </div>
              <template v-if="m.perImage != null">
                <div class="row wrap mt8" style="gap: 6px;">
                  <span class="tag grad num">¥{{ microYuan(m.perImage) }}<em>/张</em></span>
                  <span class="tag">{{ m.basePerImage != null ? 'VIP 拿货价' : (m.subsidized ? '限时补贴' : '按张计费') }}</span>
                </div>
                <div v-if="m.basePerImage != null" class="dim mt8" style="font-size: 11.5px;">原价 ¥{{ microYuan(m.basePerImage) }}/张 · VIP 专享拿货价已生效</div>
              </template>
              <template v-else>
                <div class="tp mt8">
                  <div><i>输入</i><b class="num">¥{{ perMYuan(m.inPrice) }}</b><s v-if="m.baseInPrice != null" title="官方原价">¥{{ perMYuan(m.baseInPrice) }}</s><em>/M</em></div>
                  <div><i>缓存命中</i><b class="num">¥{{ perMYuan(m.cachePrice) }}</b><s v-if="m.baseCachePrice != null" title="官方原价">¥{{ perMYuan(m.baseCachePrice) }}</s><em>/M</em></div>
                  <div><i>输出</i><span class="tag grad num">¥{{ perMYuan(m.outPrice) }}</span><s v-if="m.baseOutPrice != null" title="官方原价">¥{{ perMYuan(m.baseOutPrice) }}</s><em>/M</em></div>
                </div>
                <div class="dim mt8" style="font-size: 11.5px;">先付后用 · 用多少付多少 · 缓存命中更省</div>
              </template>
              <div class="pmetrics mt8">
                <span :class="{ dim: !latOf(liveMap[m.id]) }"><i>首字</i><b>{{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</b></span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }"><i>速度</i><b>{{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</b></span>
                <span :class="{ dim: !liveMap[m.id] }"><i>成功率</i><b>{{ liveMap[m.id] ? fmtRate(liveMap[m.id].ok_rate) : '--' }}</b></span>
              </div>
              <div class="row between mt12">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">能力详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
          <div v-if="!tokenLineRows.length" class="empty mt12"><b>未找到匹配的收费模型</b></div>
        </section>

        <!-- 按次计费分组 -->
        <section v-if="callLineRows.length" class="mt16">
          <div class="line-title">收费模型（按次计费）<small>密钥选「免费 + 收费」时可用：每次成功请求扣一次，与生成长度无关</small></div>
          <div class="grid3">
            <div v-for="m in callLineRows" :key="m.id + ':call'" class="card hoverable pcard" :class="{ paused: !!m.st }">
              <div class="row between">
                <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
              </div>
              <div class="row wrap mt8" style="gap: 6px;">
                <span class="tag grad num">¥{{ microYuan(m.price) }}<em>/次</em></span>
                <span class="tag">{{ m.basePrice != null ? 'VIP 拿货价' : '正常价' }}</span>
              </div>
              <div v-if="m.basePrice != null" class="dim mt8" style="font-size: 11.5px;">原价 ¥{{ microYuan(m.basePrice) }}/次 · VIP 专享拿货价已生效</div>
              <div class="row between mt12">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">能力详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
        </section>

        <!-- GPT · Codex 专线 -->
        <section v-if="codexLineRows.length" class="mt16">
          <div class="line-title">GPT · Codex 专线<small>codex/ 前缀独占模型，ChatGPT 账号池直连：输入 / 缓存命中 / 输出分段计价，按量密钥即可调用</small></div>
          <div class="grid3">
            <div v-for="m in codexLineRows" :key="m.id" class="card hoverable pcard" :class="{ paused: !!m.st }">
              <div class="row between">
                <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
              </div>
              <div class="row mt8" style="gap: 7px; font-size: 12px;">
                <span class="dot" :class="liveStatusOf(m.id).dot"></span>
                <span :title="'最近活动 ' + (liveMap[m.id] ? fmtAgo(liveMap[m.id].last_ts) : '--')">{{ liveStatusOf(m.id).text }}</span>
                <span class="dim" style="margin-left: auto; font-size: 11px;">{{ liveMap[m.id] ? fmtAgo(liveMap[m.id].last_ts) : '' }}</span>
              </div>
              <div class="tp mt8">
                <div><i>输入</i><b class="num">¥{{ perMYuan(m.inPrice) }}</b><em>/M</em></div>
                <div><i>缓存命中</i><b class="num">¥{{ perMYuan(m.cachePrice) }}</b><em>/M</em></div>
                <div><i>输出</i><span class="tag grad num">¥{{ perMYuan(m.outPrice) }}</span><em>/M</em></div>
              </div>
              <div class="dim mt8" style="font-size: 11.5px;">Codex 账号池专线 · 缓存命中更省 · 先付后用</div>
              <div class="pmetrics mt8">
                <span :class="{ dim: !latOf(liveMap[m.id]) }"><i>首字</i><b>{{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</b></span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }"><i>速度</i><b>{{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</b></span>
                <span :class="{ dim: !liveMap[m.id] }"><i>成功率</i><b>{{ liveMap[m.id] ? fmtRate(liveMap[m.id].ok_rate) : '--' }}</b></span>
              </div>
              <div class="row between mt12">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">能力详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
        </section>


        <!-- 官方中转 · 高速专线 -->
        <section v-if="officialLineRows.length" class="mt16">
          <div class="line-title">官方中转 · 高速专线<small>密钥选「官方中转」分组时可用：tlk/ 前缀独占模型，按官方原价 6 折分段计价</small></div>
          <div class="grid3">
            <div v-for="m in officialLineRows" :key="m.id" class="card hoverable pcard" :class="{ paused: !!m.st }">
              <div class="row between">
                <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
              </div>
              <div class="row mt8" style="gap: 7px; font-size: 12px;">
                <span class="dot" :class="liveStatusOf(m.id).dot"></span>
                <span>{{ liveStatusOf(m.id).text }}</span>
                <span class="dim" style="margin-left: auto; font-size: 11px;">{{ liveMap[m.id] ? fmtAgo(liveMap[m.id].last_ts) : '' }}</span>
              </div>
              <div class="tp mt8">
                <div><i>输入</i><b class="num">¥{{ perMYuan(m.inPrice) }}</b><em>/M</em></div>
                <div><i>缓存命中</i><b class="num">¥{{ perMYuan(m.cachePrice) }}</b><em>/M</em></div>
                <div><i>输出</i><span class="tag grad num">¥{{ perMYuan(m.outPrice) }}</span><em>/M</em></div>
              </div>
              <div class="dim mt8" style="font-size: 11.5px;">官方中转专线 · 官方原价 6 折 · 先付后用</div>
              <div class="pmetrics mt8">
                <span :class="{ dim: !latOf(liveMap[m.id]) }"><i>首字</i><b>{{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</b></span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }"><i>速度</i><b>{{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</b></span>
                <span :class="{ dim: !liveMap[m.id] }"><i>成功率</i><b>{{ liveMap[m.id] ? fmtRate(liveMap[m.id].ok_rate) : '--' }}</b></span>
              </div>
              <div class="row between mt12">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">能力详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
        </section>

        <!-- 免费与收费 · 必读说明 -->
        <div class="card mt24" style="max-width: 980px;">
          <b><AqIcon name="info" :size="16" />免费与收费 · 必读说明</b>
          <div class="mt12" style="display: grid; gap: 12px;">
            <div>
              <b style="font-size: 13.5px;">1 · 全站绝大多数模型完全免费，而且会一直免费</b>
              <p class="dim" style="font-size: 13px;">Nvidia NIM 免费通道的全部模型——对话、视觉、语音、向量、重排统统不收一分钱，注册即可使用；用量统计仅用于展示，今后也不会收费。</p>
            </div>
            <div>
              <b style="font-size: 13.5px;">2 · 收费模型：官方自营系列（统一 aqua/ 前缀）</b>
              <p class="dim" style="font-size: 13px;">
                计费方式由你密钥的计费分组决定：「免费 + 收费」免费模型随便调，收费模型每次成功请求扣一次、与生成长度无关、失败全额退回；「纯免费」密钥仅可调用免费模型。acu/ 前缀的众筹公共模型任何密钥都能调，按次从站点公共额度扣费、不动个人余额。
                调用方式与免费模型完全一致——同一个接口，只是 model 换成它们，支持流式输出。
              </p>
            </div>
            <div>
              <b style="font-size: 13.5px;">3 · 如何获得余额</b>
              <p class="dim" style="font-size: 13px;">
                在线充值即时到账：登录后进入 <router-link to="/console?view=topup">个人控制台 · 余额充值</router-link>，
                支付宝 / 微信任一渠道支付，支付金额 100% 全额到账。免费模型不受余额影响——没有余额照样随便用。
              </p>
            </div>
          </div>
        </div>
      </template>

      <!-- ================================================== 能力总览视图（?view=cap） ================================================== -->
      <template v-else>
        <div class="kpis mt16">
          <div class="kpi"><span>模型总数</span><b class="num">{{ capStats.total }}</b><span class="trend">当前筛选实时统计</span></div>
          <div class="kpi"><span>能力类型</span><b class="num">{{ capStats.typeCount }}</b><span class="trend">对话 / 向量 / 语音 / 生成…</span></div>
        </div>
        <div class="card mt16">
          <div class="row wrap" style="gap: 6px;">
            <span v-for="p in capStats.typePills" :key="p.label" class="tag"><b class="num">{{ p.count }}</b>&nbsp;{{ p.label }}</span>
          </div>
          <div class="mt12 cap-bars">
            <div v-for="b in capStats.bars" :key="b.label" class="cap-bar">
              <span class="cap-bar-label">{{ b.label }}</span>
              <span class="cap-bar-track"><span class="cap-bar-fill" :style="{ width: b.pct + '%' }"></span></span>
              <span class="cap-bar-pct num">{{ b.pct }}%</span>
            </div>
          </div>
        </div>
        <div class="row wrap mt16" style="gap: 6px;">
          <button v-for="f in capFilters" :key="f.id" class="chip" :class="{ on: curCap === f.id }" @click="curCap = f.id">{{ f.label }}</button>
        </div>

        <div v-if="loading && !models.length" class="grid3 mt16">
          <div v-for="i in 6" :key="i" class="card">
            <div class="skeleton" style="min-height: 15px; width: 58%;"></div>
            <div class="skeleton mt12" style="min-height: 12px; width: 90%;"></div>
            <div class="skeleton mt8" style="min-height: 12px; width: 70%;"></div>
          </div>
        </div>
        <div v-else-if="!viewCards.length" class="card mt16">
          <div class="empty"><div class="big"><AqIcon name="grid" :size="40" /></div><b>未找到匹配的模型</b><div class="dim">换个关键词或筛选条件试试</div></div>
        </div>
        <div v-else class="grid3 mt16">
          <router-link v-for="c in viewCards" :key="c.id" class="card hoverable ccard" :to="modelLink(c.id)">
            <div class="row between">
              <span class="mono mid" :title="c.id">{{ c.id }}</span>
              <span v-if="c.stTag" class="tag" :class="c.stTag.cls">{{ c.stTag.text }}</span>
            </div>
            <div class="row wrap mt8" style="gap: 5px;">
              <span v-if="c.acu" class="tag" :class="c.acu.cls">{{ c.acu.text }}</span>
              <span class="tag">{{ c.typeText }} · {{ c.platformText }}</span>
              <span v-for="mt in c.metrics" :key="mt.label" class="tag">{{ mt.label }} {{ mt.value }}</span>
            </div>
            <div class="caps mt12">
              <span v-for="ic in c.icons" :key="ic.label" class="cap-mini" :class="ic.mini.cls" :title="ic.tip">
                {{ ic.label }}
                <AqIcon v-if="ic.mini.sym === 'check'" name="check" :size="11" />
                <AqIcon v-else-if="ic.mini.sym === 'cross'" name="cross" :size="11" />
                <template v-else>~</template>
              </span>
            </div>
          </router-link>
        </div>
        <p class="dim mt12" style="font-size: 12.5px;">浏览所有模型的「能力卡片」——上下文 / 参数量 / 平台 / 类型一目了然，点击卡片进入模型详情页。</p>
      </template>
    </div>
  </div>
</template>

<style scoped>
/* ---- sticky 工具条 ---- */
.toolbar {
  position: sticky;
  top: 68px;
  z-index: 20;
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 10px 14px;
  border: 1px solid var(--line);
  border-radius: var(--r-md);
  background: var(--bg2-solid);
  box-shadow: var(--shadow-1);
}
.tool-search { flex: 1; min-width: 200px; max-width: 420px; }

/* ---- 模型卡（免费视图 + 展开行） ---- */
.mcard { cursor: pointer; }
.mcard .mid { color: var(--txt0); font-weight: 700; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mcard.exhausted { opacity: .72; }
.caret { color: var(--txt3); transition: transform var(--t-fast); }
.caret.open { transform: rotate(180deg); }
.expand { border-top: 1px dashed var(--line); padding-top: 10px; }

/* ---- 众筹专区 ---- */
.crowd { border-color: color-mix(in srgb, var(--acc) 34%, transparent); }
.crowd.off { border-color: color-mix(in srgb, var(--bad) 34%, transparent); }
.crowd-item { border: 1px solid var(--line); border-radius: var(--r-md); padding: 13px 15px; background: var(--bg1); }
.crowd-item .ci-id { color: var(--acc); font-weight: 700; font-size: 12.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.crowd-item .ci-t { font-size: 13px; color: var(--txt0); }
.crowd-item.cta { border-style: dashed; border-color: color-mix(in srgb, var(--warn) 45%, transparent); display: flex; flex-direction: column; gap: 8px; }
.crowd-item.cta b { color: var(--warn); }

/* ---- 收费卡 ---- */
.pcard .pid { font-size: 12.5px; font-weight: 700; color: var(--txt0); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pcard .pid i { font-style: normal; color: var(--txt3); font-weight: 600; }
.pcard.paused { opacity: .62; }
.tag em, .tag s { font-style: normal; text-decoration: none; }
.tag s { opacity: .7; font-size: 10.5px; margin-left: 3px; }
.tag em { opacity: .75; font-size: 10.5px; }
.tp { display: grid; gap: 3px; }
.tp > div { display: flex; align-items: baseline; gap: 6px; }
.tp i { font-style: normal; font-size: 11px; color: var(--txt2); min-width: 52px; }
.tp b { font-size: 14px; font-weight: 700; color: var(--txt0); }
.tp em { font-style: normal; font-size: 10.5px; color: var(--txt3); }
.pmetrics { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 4px; border-top: 1px dashed var(--line); padding-top: 8px; }
.pmetrics span { display: flex; flex-direction: column; gap: 1px; }
.pmetrics i { font-style: normal; font-size: 10px; color: var(--txt2); }
.pmetrics b { font-size: 12px; font-weight: 700; color: var(--txt0); white-space: nowrap; }
.line-title { display: flex; align-items: baseline; gap: 8px; flex-wrap: wrap; font-size: 14.5px; font-weight: 700; color: var(--txt0); margin-bottom: 10px; }
.line-title small { font-weight: 400; font-size: 11.5px; color: var(--txt2); }
.rate-big { font-size: 17px; padding: 5px 13px; }

/* ---- 能力总览 ---- */
.cap-bars { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: 7px 20px; }
.cap-bar { display: flex; align-items: center; gap: 8px; font-size: 12px; }
.cap-bar-label { width: 30px; color: var(--txt2); flex: none; }
.cap-bar-track { flex: 1; height: 6px; border-radius: 99px; background: var(--bg3); overflow: hidden; }
.cap-bar-fill { display: block; height: 100%; border-radius: 99px; background: var(--acc-grad); }
.cap-bar-pct { width: 38px; text-align: right; color: var(--txt2); flex: none; }
.ccard { display: block; color: inherit; }
.ccard .mid { color: var(--txt0); font-weight: 700; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.caps { display: grid; grid-template-columns: repeat(4, 1fr); gap: 5px; border-top: 1px dashed var(--line); padding-top: 10px; }
.cap-mini {
  display: inline-flex; align-items: center; justify-content: center; gap: 3px;
  font-size: 10.5px; font-weight: 600;
  border: 1px solid var(--line); border-radius: 7px; padding: 2px 0;
  color: var(--txt3); background: var(--bg3);
}
.cap-mini.yes { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 38%, transparent); background: var(--ok-soft); }
.cap-mini.part { color: var(--warn); border-color: color-mix(in srgb, var(--warn) 38%, transparent); background: var(--warn-soft); }
</style>
