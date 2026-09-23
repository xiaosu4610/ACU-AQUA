<script setup lang="ts">
/* 模型中心 · 免费模型 / 收费模型 / 能力总览 三视图
 * 接口对接（与旧版 1:1）：
 * - /v1/models（useModels.load，session:true，60 秒轮询 + 离线兜底）
 * - /v1/models/status（apiJson('/models/status')，实时首字延迟/速度，20 秒轮询，静默失败）
 * - /v1/meta（apiJson('/meta')，rate_promo / rate_normal 计费倍率横幅，静默失败）
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
  router.replace({ query: { ...route.query, view: v === 'free' ? undefined : v } })
}

/* ================= 模型列表：/v1/models（60 秒自动刷新） ================= */
const { models, loading, error, loadedAt, load } = useModels()
let refreshTimer = 0
let liveTimer = 0
let tickTimer = 0
let poolTimer = 0
onMounted(() => {
  load()
  refreshTimer = window.setInterval(() => load(true), 60000)
  /* 实时状态：卡片内嵌展示，20 秒轮询 */
  liveTimer = window.setInterval(loadLive, 20000)
  loadLive()
  loadRate()
  /* 众筹池状态：与模型列表同频刷新（池子耗尽要立刻在 acu/ 卡片上体现） */
  loadPoolState()
  poolTimer = window.setInterval(loadPoolState, 60000)
})
onUnmounted(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
  if (liveTimer) window.clearInterval(liveTimer)
  if (tickTimer) window.clearInterval(tickTimer)
  if (poolTimer) window.clearInterval(poolTimer)
})
function retry() { load(true) }

/* ================= 实时状态：/v1/models/status 最近 200 次请求推断（20 秒自动刷新） ================= */
type LiveRow = { model: string; samples: number; avg_latency_ms?: number; avg_first_ms?: number; avg_tps?: number; last_ts: number }
const liveRows = ref<LiveRow[]>([])
const liveTs = ref(0)
const liveLoading = ref(false)
async function loadLive() {
  liveLoading.value = true
  try {
    const j = await apiJson<{ data: LiveRow[]; generated_ts: number }>('/models/status')
    /* 全量接收：收费线（aqua/ codex/ tlk/）与免费线（acu/ 官方自营 + 公益通道裸名）都要有状态。
       此前只留带前缀的 → 免费模型卡片永远没有实时数据（本次修复的正是这个）。 */
    liveRows.value = j.data || []
    liveTs.value = j.generated_ts || 0
  } catch { /* 静默：下一轮自动重试 */ }
  liveLoading.value = false
}
const liveMap = computed(() => { const m: Record<string, LiveRow> = {}; for (const r of liveRows.value) m[r.model] = r; return m })
/* 基准与排名仍只用"自营线路"样本（与本次改动前口径一致，收费卡的"比全站快/速度排名"含义不变）：
   免费公益池的延迟与吞吐量级差异极大，混入会把参照系整体带偏 */
const benchRows = computed(() => liveRows.value.filter(r => /^(aqua|acu|codex|tlk)\//.test(r.model)))
/* 展示口径：优先首字延迟（用户感知的响应速度），旧数据无首字段时回退总耗时 */
function latOf(r?: LiveRow): number {
  if (!r) return 0
  return r.avg_first_ms || r.avg_latency_ms || 0
}
function fmtLat(ms?: number): string {
  if (!ms) return '--'
  return ms >= 1000 ? (ms / 1000).toFixed(2) + ' s' : Math.round(ms) + ' ms'
}
function fmtAgo(ts?: number): string {
  if (!ts) return '--'
  const d = Math.max(0, Math.floor(Date.now() / 1000 - ts))
  if (d < 60) return d + ' 秒前'
  if (d < 3600) return Math.floor(d / 60) + ' 分钟前'
  return Math.floor(d / 3600) + ' 小时前'
}
/* ================= 全站实测基准与排名（数据源同为 /v1/models/status 真实样本，纯前端推导） ================= */
/* 可信度门槛：样本数 ≥20 才纳入基准与排名，防小样本带偏对比口径 */
const CMP_MIN_SAMPLES = 20
const siteAvg = computed(() => {
  let frtSum = 0, frtN = 0, tpsSum = 0, tpsN = 0
  for (const r of benchRows.value) {
    if (r.samples < CMP_MIN_SAMPLES) continue
    if (r.avg_first_ms && r.avg_first_ms > 0) { frtSum += r.avg_first_ms * r.samples; frtN += r.samples }
    if (r.avg_tps && r.avg_tps > 0) { tpsSum += r.avg_tps * r.samples; tpsN += r.samples }
  }
  return { frt: frtN ? frtSum / frtN : 0, tps: tpsN ? tpsSum / tpsN : 0 }
})
/* 速度排名：仅统计有 TPS 实测的模型（降序），返回 1-based 名次与总数 */
const tpsRank = computed(() => {
  const arr = benchRows.value.filter(r => r.samples >= CMP_MIN_SAMPLES && (r.avg_tps || 0) > 0)
  const sorted = arr.slice().sort((a, b) => (b.avg_tps || 0) - (a.avg_tps || 0))
  const m: Record<string, { rank: number; total: number }> = {}
  sorted.forEach((r, i) => { m[r.model] = { rank: i + 1, total: sorted.length } })
  return m
})
/* 对比文案：首字比全站基准快多少 + TPS 排名百分位；样本不足或无数据返回空串（不展示、不编造） */
function speedCmp(id: string): string {
  const r = liveMap.value[id]
  if (!r || r.samples < CMP_MIN_SAMPLES) return ''
  const parts: string[] = []
  const frt = r.avg_first_ms || 0
  if (frt > 0 && siteAvg.value.frt > 0) {
    const pct = Math.round((1 - frt / siteAvg.value.frt) * 100)
    if (pct > 0) parts.push('首字比全站快 ' + pct + '%')
  }
  const rk = tpsRank.value[id]
  if (rk && rk.total >= 3) {
    const top = Math.max(1, Math.round((rk.rank / rk.total) * 100))
    parts.push('速度排名前 ' + top + '%')
  }
  return parts.join(' · ')
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
const q = ref(typeof route.query.q === 'string' ? route.query.q : '')
watch(q, value => router.replace({ query: { ...route.query, q: value || undefined } }))
/* 筛选选项常量（模板直接消费） */
const PLATFORM_OPTS = [['all', '全部'], ['nvidia', '公益通道'], ['acu', '官方自营']] as const
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

/* ================= 状态徽标（旧 statusOf 平移，色彩映射为 .dot ok/warn/bad） ================= */
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
    .map(m => ({ row: m, st: statusOf(m), exhausted: isExhausted(m), ...capInfoOf(m.id, m.type, m.platform) }))
})

/* ================= 官方自营众筹专区（acu/ 前缀）=================
 * 20260921 起 acu/ 由"纯免费"改为**众筹池付费**：调用按次从公共池扣站点额度，
 * 个人余额分文不动；池子耗尽时调用端返回 403 crowd_pool_empty，故卡片必须如实提示。 */
function isCrowd(id: string) { return id.startsWith('acu/') }

const crowdModels = computed(() => orderedModels.value
  .filter(m => isCrowd(m.id))
  .map(m => ({
    id: m.id,
    st: statusOf(m),
    link: modelLink(m.id),
    price: m.price_micro ?? null,
    ...capInfoOf(m.id, m.type, m.platform),
  })))
const CROWD_DESC: Record<string, { t: string; d: string }> = {
  'acu/deepseek-v4-flash': { t: 'DeepSeek 极速轻旗舰', d: '秒回级响应，高频轻任务首选' },
  'acu/deepseek-v4-pro': { t: 'DeepSeek 满血旗舰', d: '深度推理 · 长文创作 · 复杂任务扛把子' },
  'acu/deepseek-v4-1-flash': { t: 'DeepSeek V4.1 满血旗舰', d: '更快更实惠的满血版本，综合能力最强' },
  'acu/glm-5.3': { t: '智谱 GLM 旗舰', d: '中文创作与代码生成双优' },
  'acu/glm-5.3-flash': { t: '智谱 GLM 极速版', d: '原生多模态，长输出场景首选' },
  'acu/kimi-k3': { t: 'Kimi K3 长文本旗舰', d: '超长上下文，长文档处理利器' },
}
function crowdDesc(id: string) { return CROWD_DESC[id] || { t: '官方自营众筹通道', d: '由公共众筹池统一付费，个人余额分文不动' } }

/* 众筹池公开状态：池子耗尽时 acu/ 全部返回 403，卡片要如实提示并引导注资 */
const poolAlive = ref(false)
const poolBalance = ref(0)
async function loadPoolState() {
  try {
    const j = await apiJson<{ balance_micro: number; alive: boolean }>('/pool/status')
    poolAlive.value = !!j.alive
    poolBalance.value = j.balance_micro || 0
  } catch { /* 静默：下一轮自动重试 */ }
}

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
    priceView: (m as any).price_view as string | undefined,
    /* 常态扣费倍率（后端 /v1/models 的 charge_rate）：卡片展示官方原价 + 倍率角标，实收 = 官方价 × 倍率 */
    chargeRate: (m as any).charge_rate as number | undefined,
    /* 展示分组标记（后端下发 section=external 表示外模专线，前端单独成板块） */
    section: (m as any).section as string | undefined,
    /* 2 号折扣钱包专用（20260924）：该模型只能用折扣钱包余额调用（主钱包会被拒），
       卡片需明确提示，否则用户会以为"余额够却调不了" */
    wallet2Only: (m as any).wallet2_only === true,
    isImage: m.type === 'image',
    st: statusOf(m),
    link: modelLink(m.id),
  })))
const paidCallLine = computed(() => paidModels.value.filter(m => m.groups.includes('per_call')))
/* codex/ 前缀（GPT · Codex 专线）走专属板块，不混入按量计费分组 */
const paidTokenLine = computed(() => paidModels.value.filter(m => m.groups.includes('per_token') && !m.id.startsWith('codex/') && m.section !== 'external'))
/* 外模专线（20260923 站长指令）：外模（Kiro）模型单独成板块。
   展示分组与密钥分组解耦——用户密钥分组不变（仍是「免费 + 按量计费」），仅列表呈现分区。 */
const externalLineRows = computed(() => sortTokenLine(paidModels.value.filter(m => m.section === 'external')))

/* 收费页工具栏：搜索 + 类型筛选 + 排序 */
const paidQ = ref(typeof route.query.q === 'string' ? route.query.q : '')
watch(paidQ, value => router.replace({ query: { ...route.query, q: value || undefined } }))
const paidType = ref<'all' | 'chat' | 'image'>('all')
const paidSort = ref<'smart' | 'price'>('smart')
/* ================= 收费页排版：card 卡片（默认，多列总览）/ bar 长条（单列详览），localStorage 记忆 ================= */
function initialPaidLayout(): 'card' | 'bar' {
  try { return localStorage.getItem('aqua_paid_layout') === 'bar' ? 'bar' : 'card' } catch { return 'card' }
}
const paidLayout = ref<'card' | 'bar'>(initialPaidLayout())
watch(paidLayout, v => { try { localStorage.setItem('aqua_paid_layout', v) } catch { /* 隐私模式等场景忽略 */ } })
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
/* 模型精确规格（按 ID 关键词匹配，官方公开资料口径）：ctx=上下文上限 maxOut=官方最大输出
 * size=参数量 dims=向量维度 released=发布日期；vision/tools 为官方能力事实（覆盖按类型推断） */
interface ModelSpec { re: RegExp; ctx?: string; maxOut?: string; size?: string; dims?: number; released?: string; vision?: boolean; tools?: boolean }
const MODEL_SPECS: ModelSpec[] = [
  /* DeepSeek 官方 API 文档 / 模型卡：V4 系列 1M 上下文、最大输出 384K；
     V4-Flash 285B(13B active)、V4-Pro 1.6T(49B active)；V4-Pro 不支持视觉（官方明确 Not supported） */
  { re: /^deepseek-v4-flash$/, ctx: "1M", maxOut: "384K", size: "285B (13B active)", released: "2026-04" },
  { re: /^deepseek-v4-flash-0731$/, ctx: "1M", maxOut: "384K", size: "285B (13B active)", released: "2026-07" },
  { re: /^deepseek-v4-pro$/, ctx: "1M", maxOut: "384K", size: "1.6T (49B active)", released: "2026-04", vision: false },
  { re: /^deepseek-v4-1-flash$|^deepseek-v4\.1-flash$/, ctx: "1M", maxOut: "384K", size: "285B (13B active)", released: "2026-09", vision: true },
  /* 智谱官方文档：GLM-5.2 发布 2026-06-16、GLM-5.3 发布 2026-08-19、GLM-5.3-Flash 发布 2026-08-26
     （原生多模态，总参 320B/激活 18B）；三者均为 1M 上下文、最大输出 128K */
  { re: /^glm-5\.2$/, ctx: "1M", maxOut: "128K", size: "744B (40B active)", released: "2026-06" },
  { re: /^glm-5\.3$/, ctx: "1M", maxOut: "128K", size: "744B (40B active)", released: "2026-08", tools: true },
  { re: /^glm-5\.3-flash$/, ctx: "1M", maxOut: "128K", size: "320B (18B active)", released: "2026-08", vision: true, tools: true },
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
  { re: /deepseek-v4-pro/, ctx: "1M", maxOut: "384K", size: "1.6T (49B active)", released: "2026-04" },
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
/* 线路前缀（acu/ aqua/ codex/ tlk/ tide/）剥掉后再匹配规格：
 * 同一模型在免费线（acu/）、收费线（aqua/）、公益通道（裸名）下共用同一份官方规格，
 * 此前正则按 aqua/ 锚定 → 免费线模型一条都匹配不上，能力数据整片空白。 */
function stripLinePrefix(id: string): string {
  const i = id.indexOf('/')
  return i > 0 ? id.slice(i + 1) : id
}
function modelSpec(id: string): Omit<ModelSpec, 're'> {
  const bare = stripLinePrefix(id)
  for (const s of MODEL_SPECS) if (s.re.test(bare)) return s
  return {}
}
/* 能力数据（官方规格 + 能力位）：免费/收费/能力总览三处卡片共用同一口径 */
function capInfoOf(id: string, type: string, platform: string) {
  const spec = modelSpec(id)
  const c = capForType(type)
  /* 官方自营专线（acu/）官方支持 function calling，请求体原样透传 */
  if (platform === 'acu') c.tools = 1
  /* 官方能力事实优先于按类型推断（如 GLM-5.3-Flash 原生多模态、DeepSeek V4-Pro 官方明确不支持视觉） */
  if (spec.vision === true) c.vision = 1
  if (spec.vision === false) c.vision = 0
  if (spec.tools === true) c.tools = 1
  const metrics: { label: string; value: string }[] = []
  if (spec.ctx) metrics.push({ label: '上下文', value: spec.ctx })
  if (spec.maxOut) metrics.push({ label: '最大输出', value: spec.maxOut })
  if (spec.size) metrics.push({ label: '参数', value: spec.size })
  if (spec.dims) metrics.push({ label: '向量', value: spec.dims + 'D' })
  if (spec.released) metrics.push({ label: '发布', value: spec.released })
  const icons = CAPS.map(cp => ({ label: cp.label, tip: cp.label + ': ' + cp.tip, mini: capMini(c[cp.id]) }))
  return { metrics, icons }
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
      let stTag: CapFlag | null = null
      if (m.id.toLowerCase() === 'auto') stTag = { cls: 'acc', text: '智能路由' }
      else if (dsMaintenance(m.id)) stTag = { cls: 'bad', text: '维护中' }
      else if (m.status === 'exhausted') stTag = { cls: 'bad', text: '耗尽' }
      else if (m.status === 'unavailable') stTag = { cls: 'warn', text: '不可用' }
      const acu = m.platform === 'acu' ? { cls: 'acc', text: '官方自营' } : null
      const { metrics, icons } = capInfoOf(m.id, m.type, m.platform)
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
        <div class="sub">全线模型一览与能力总览：<b>公益免费优先</b>，专线按需 · 列表每 60 秒 / 实时状态每 20 秒自动刷新</div>
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

        <!-- 官方自营众筹专区：acu/ 前缀（公共众筹池统一付费，个人余额分文不动） -->
        <div v-if="crowdModels.length" class="card mt16 crowd">
          <div class="row wrap between">
            <b><AqIcon name="coin" :size="17" />官方自营 · 众筹专线（acu/）</b>
            <div class="row wrap" style="gap: 8px; align-items: center;">
              <span class="tag" :class="poolAlive ? 'acc' : 'bad'">
                {{ poolAlive ? `众筹池存活 · 余额 ¥${microYuan(poolBalance)}` : '众筹池已耗尽' }}
              </span>
              <!-- 注资入口：池子空了就高亮引导，池子存活时也给常驻入口方便大家续命 -->
              <router-link to="/console?view=crowd" class="btn sm" :class="poolAlive ? 'ghost' : 'primary'">
                <AqIcon name="spark" :size="13" /> 充值到众筹池
              </router-link>
            </div>
          </div>
          <p class="dim mt8" style="font-size: 12.5px;">
            acu/ 官方自营线路由<b>公共众筹池</b>统一付费：调用时按次从池子扣站点额度，<b>你的个人余额分文不动</b>。
            <template v-if="poolAlive">池子当前余额 ¥{{ microYuan(poolBalance) }}，越充足大家用得越久。</template>
            <template v-else><b style="color: var(--bad);">池子已被用完</b>，acu/ 模型暂不可用——到控制台「众筹算力池」注资任意金额即可立刻点亮。</template>
            追求满速与旗舰模型体验，可选用 <b>aqua/ 按量收费模型</b>（官方原版直连 · 机制性价比 · 按量计费分组）。
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
              <!-- 能力数据（官方规格：上下文 / 最大输出 / 参数 / 发布） -->
              <div v-if="m.metrics.length" class="row wrap mt8" style="gap: 5px;">
                <span v-for="mt in m.metrics" :key="mt.label" class="tag">{{ mt.label }} {{ mt.value }}</span>
              </div>
              <div class="caps mt12">
                <span v-for="ic in m.icons" :key="ic.label" class="cap-mini" :class="ic.mini.cls" :title="ic.tip">
                  {{ ic.label }}
                  <AqIcon v-if="ic.mini.sym === 'check'" name="check" :size="11" />
                  <AqIcon v-else-if="ic.mini.sym === 'cross'" name="cross" :size="11" />
                  <template v-else>~</template>
                </span>
              </div>
              <!-- 实时状态（/v1/models/status 真实样本，与收费卡同口径） -->
              <div class="pmetrics mt8 has-cmp">
                <span :class="{ dim: !latOf(liveMap[m.id]) }"><i>首字</i><b>{{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</b></span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }"><i>速度</i><b>{{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</b></span>
                <span v-if="speedCmp(m.id)" class="speed-cmp"><AqIcon name="bolt" :size="11" />{{ speedCmp(m.id) }}</span>
              </div>
              <div class="dim mt8" style="font-size: 11px;">
                {{ liveMap[m.id] ? `实测样本 ${liveMap[m.id].samples} 次 · 最近活动 ${fmtAgo(liveMap[m.id].last_ts)}` : '暂无近期实测数据' }}
              </div>
              <div class="row wrap mt8" style="gap: 6px;">
                <span class="tag" :class="poolAlive ? 'acc' : 'bad'" :title="poolAlive ? `公共众筹池当前余额 ¥${microYuan(poolBalance)}，调用不扣个人余额` : '公共众筹池已耗尽，acu/ 模型暂不可用'">
                  {{ poolAlive ? '众筹池支付' : '众筹池已耗尽' }}
                </span>
                <span v-if="m.price" class="tag" :title="`每次成功请求从公共池扣 ${microYuan(m.price)} 元，个人余额分文不动`">池子扣 {{ microYuan(m.price) }} 元/次</span>
                <!-- 池子空时在每张卡上给出注资入口（用户看到的第一处就是卡片） -->
                <router-link v-if="!poolAlive" to="/console?view=crowd" class="tag acc" style="text-decoration: none;">去注资点亮 →</router-link>
                <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
              </div>
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
              <button type="button" class="btn ghost xs" :aria-label="`展开 ${r.row.id}`" :aria-expanded="expandedId === r.row.id" @click.stop="toggle(r.row.id)"><AqIcon name="chevron-down" :size="15" class="caret" :class="{ open: expandedId === r.row.id }" /></button>
            </div>
            <div class="row wrap mt8" style="gap: 6px;">
              <span v-if="r.row.id.toLowerCase() === 'auto'" class="tag acc">智能路由</span>
              <span v-if="!hideTag(r.row.type)" class="tag">{{ platformLabel(r.row.platform) }} · {{ typeLabel(r.row.type) }}</span>
              <span v-if="r.st" class="tag" :class="r.st.cls" :title="r.st.title">{{ r.st.text }}</span>
            </div>
            <!-- 能力数据（官方规格：上下文 / 最大输出 / 参数 / 发布） -->
            <div v-if="r.metrics.length" class="row wrap mt8" style="gap: 5px;">
              <span v-for="mt in r.metrics" :key="mt.label" class="tag">{{ mt.label }} {{ mt.value }}</span>
            </div>
            <div class="caps mt12">
              <span v-for="ic in r.icons" :key="ic.label" class="cap-mini" :class="ic.mini.cls" :title="ic.tip">
                {{ ic.label }}
                <AqIcon v-if="ic.mini.sym === 'check'" name="check" :size="11" />
                <AqIcon v-else-if="ic.mini.sym === 'cross'" name="cross" :size="11" />
                <template v-else>~</template>
              </span>
            </div>
            <!-- 实时状态（/v1/models/status 真实样本，与收费卡同口径） -->
            <div class="pmetrics mt8 has-cmp">
              <span :class="{ dim: !latOf(liveMap[r.row.id]) }"><i>首字</i><b>{{ latOf(liveMap[r.row.id]) ? fmtLat(latOf(liveMap[r.row.id])) : '--' }}</b></span>
              <span :class="{ dim: !liveMap[r.row.id]?.avg_tps }"><i>速度</i><b>{{ liveMap[r.row.id]?.avg_tps ? liveMap[r.row.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</b></span>
              <span v-if="speedCmp(r.row.id)" class="speed-cmp"><AqIcon name="bolt" :size="11" />{{ speedCmp(r.row.id) }}</span>
            </div>
            <div class="dim mt8" style="font-size: 11px;">
              {{ liveMap[r.row.id] ? `实测样本 ${liveMap[r.row.id].samples} 次 · 最近活动 ${fmtAgo(liveMap[r.row.id].last_ts)}` : '暂无近期实测数据' }}
            </div>
            <div v-if="expandedId === r.row.id" class="mt12 expand" @click.stop>
              <div class="dim" style="font-size: 12.5px;">{{ r.st?.title || r.row.status_msg || '该模型运行正常 · 复制 ID 即可在任意 OpenAI 客户端调用' }}</div>
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
        <p class="dim mt12" style="font-size: 12.5px;">免费模型由官方免费通道提供——注册即可使用；acu/ 官方自营线路完全免费，收费模型（aqua/）为官方原版直连专线；点击模型 ID 查看详细能力说明与支持参数。卡片上的<b>能力数据</b>（上下文 / 最大输出 / 参数 / 发布）为官方公开规格，<b>首字与速度</b>为站内真实实测样本（近 6 小时、每模型最近 200 次请求），样本不足时显示 <span class="mono">--</span>，不做任何估算填充。</p>
      </template>

      <!-- ================================================== 收费模型视图 ================================================== -->
      <template v-else-if="view === 'paid'">
        <!-- 倍率横幅（/v1/meta 下发；促销到期自动隐藏） -->
        <div v-if="rateBannerVisible" class="card accent rate-card mt16">
          <div class="row wrap between" style="gap: 14px;">
            <div class="row" style="gap: 12px;">
              <span class="tag grad rate-big num">{{ ratePromo }}×</span>
              <div>
                <b>官方原版直连专线 · 限时 {{ ratePromo }} 倍率</b>
                <div class="dim" style="font-size: 12.5px;">按量计费分组现按 {{ ratePromo }} 倍率计费，活动结束后恢复 {{ rateNormal }} 倍率——恢复前价格不变，越早用越划算。</div>
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
          <div class="row paid-tools" style="gap: 8px;">
            <div class="chips">
              <button class="chip" :class="{ on: paidSort === 'smart' }" title="旗舰与国产模型优先" @click="paidSort = 'smart'">智能排序</button>
              <button class="chip" :class="{ on: paidSort === 'price' }" title="按输出单价从低到高" @click="paidSort = 'price'">价格优先</button>
            </div>
            <div class="chips" role="group" aria-label="列表排版切换">
              <button class="chip" :class="{ on: paidLayout === 'card' }" title="卡片：多列总览，一屏看清全部模型" :aria-pressed="paidLayout === 'card'" @click="paidLayout = 'card'"><AqIcon name="layout" :size="13" />卡片</button>
              <button class="chip" :class="{ on: paidLayout === 'bar' }" title="长条：单列详览，逐行对比价格" :aria-pressed="paidLayout === 'bar'" @click="paidLayout = 'bar'"><AqIcon name="list" :size="13" />长条</button>
            </div>
          </div>
        </div>

        <div v-if="!paidModels.length" class="card mt16">
          <div v-if="loading" class="grid3">
            <div v-for="i in 3" :key="i"><div class="skeleton" style="min-height: 120px;"></div></div>
          </div>
          <div v-else class="empty"><div class="big"><AqIcon name="coin" :size="40" /></div><b>按次模型加载中或暂未在售</b><div class="dim">acu/ 官方自营众筹模型见免费专区 · 在售清单以 /v1/models 实时下发为准 · 每分钟自动刷新</div></div>
        </div>

        <div v-if="!loading && !tokenLineRows.length && !externalLineRows.length && !callLineRows.length && !codexLineRows.length && !officialLineRows.length" class="empty"><b>没有匹配当前条件的收费模型</b><button class="btn mt12" @click="paidQ = ''; paidType = 'all'">清除搜索与筛选</button></div>
        <!-- 按量计费分组：官方原版中转超高速专线（核心卖点板块） -->
        <!-- 外模专线（20260923 站长指令）：外模（Kiro）模型单独成板块展示。
             展示分组与密钥分组解耦——用户密钥分组仍是「免费 + 按量计费」，调用路由完全不变。 -->
        <section v-if="externalLineRows.length" class="mt16">
          <div class="line-title">外模专线<small>Claude 旗舰系列（Anthropic 官方模型）：按量计费，输入 / 缓存命中 / 输出分段计价，用多少付多少</small></div>
          <div v-if="paidLayout === 'card'" class="grid3">
            <div v-for="m in externalLineRows" :key="m.id + ':ext'" class="card hoverable pcard" :class="{ paused: !!m.st }">
              <div class="row between">
                <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                <span v-if="m.chargeRate" class="tag warn num" title="卡片展示官方原价，实际按此倍率扣费">{{ m.chargeRate }}×</span>
                <span v-else-if="m.subsidized && rateBadgeOf(m)" class="tag warn num" title="官方原价 × 补贴倍率 = 现价">{{ rateBadgeOf(m) }}</span>
                <span v-else-if="m.baseInPrice != null || m.basePerImage != null" class="tag warn" title="VIP 专享拿货价已生效">VIP</span>
              </div>
              <div class="row wrap mt8" style="gap: 6px;">
                <span class="dim" style="font-size: 11px;">{{ liveMap[m.id] ? '最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '暂无近期数据' }}</span>
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
                  <div><i>输入</i><b class="num">¥{{ perMYuan(m.chargeRate ? m.baseInPrice : m.inPrice) }}</b><s v-if="!m.chargeRate && m.baseInPrice != null" title="官方原价">¥{{ perMYuan(m.baseInPrice) }}</s><em>/M</em></div>
                  <div><i>缓存命中</i><b class="num">¥{{ perMYuan(m.chargeRate ? m.baseCachePrice : m.cachePrice) }}</b><s v-if="!m.chargeRate && m.baseCachePrice != null" title="官方原价">¥{{ perMYuan(m.baseCachePrice) }}</s><em>/M</em></div>
                  <div><i>输出</i><span class="tag grad num">¥{{ perMYuan(m.chargeRate ? m.baseOutPrice : m.outPrice) }}</span><s v-if="!m.chargeRate && m.baseOutPrice != null" title="官方原价">¥{{ perMYuan(m.baseOutPrice) }}</s><em>/M</em></div>
                </div>
                <div class="dim mt8" style="font-size: 11.5px;">先付后用 · 用多少付多少 · 缓存命中更省</div>
              </template>
              <div class="pmetrics mt8 has-cmp">
                <span :class="{ dim: !latOf(liveMap[m.id]) }"><i>首字</i><b>{{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</b></span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }"><i>速度</i><b>{{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</b></span>
                <span v-if="speedCmp(m.id)" class="speed-cmp"><AqIcon name="bolt" :size="11" />{{ speedCmp(m.id) }}</span>
              </div>
              <div class="row between mt12">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">能力详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
          <div v-else class="plist">
            <div v-for="m in externalLineRows" :key="m.id + ':extbar'" class="card hoverable pbar" :class="{ paused: !!m.st }">
              <div class="pb-main">
                <div class="row between" style="gap: 8px;">
                  <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                  <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
                </div>
                <div class="row wrap mt8" style="gap: 6px;">
                  <span class="dim" style="font-size: 11px;">{{ liveMap[m.id] ? '最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '暂无近期数据' }}</span>
                  <span v-if="m.chargeRate" class="tag warn num" title="卡片展示官方原价，实际按此倍率扣费">{{ m.chargeRate }}×</span>
                  <span v-else-if="m.subsidized && rateBadgeOf(m)" class="tag warn num" title="官方原价 × 补贴倍率 = 现价">{{ rateBadgeOf(m) }}</span>
                  <span v-else-if="m.baseInPrice != null" class="tag warn" title="VIP 专享拿货价已生效">VIP</span>
                </div>
              </div>
              <div class="pb-price">
                <template v-if="m.perImage != null">
                  <span class="tag grad num">¥{{ microYuan(m.perImage) }}<em>/张</em></span>
                  <s v-if="m.basePerImage != null" class="num" title="官方原价">¥{{ microYuan(m.basePerImage) }}</s>
                </template>
                <template v-else>
                  <span class="pp"><i>输入</i><b class="num">¥{{ perMYuan(m.chargeRate ? m.baseInPrice : m.inPrice) }}</b><s v-if="!m.chargeRate && m.baseInPrice != null" title="官方原价">¥{{ perMYuan(m.baseInPrice) }}</s><em>/M</em></span>
                  <span class="pp"><i>缓存</i><b class="num">¥{{ perMYuan(m.chargeRate ? m.baseCachePrice : m.cachePrice) }}</b><s v-if="!m.chargeRate && m.baseCachePrice != null" title="官方原价">¥{{ perMYuan(m.baseCachePrice) }}</s><em>/M</em></span>
                  <span class="pp"><i>输出</i><b class="num acc">¥{{ perMYuan(m.chargeRate ? m.baseOutPrice : m.outPrice) }}</b><s v-if="!m.chargeRate && m.baseOutPrice != null" title="官方原价">¥{{ perMYuan(m.baseOutPrice) }}</s><em>/M</em></span>
                </template>
              </div>
              <div class="pb-meta has-cmp">
                <span :class="{ dim: !latOf(liveMap[m.id]) }">首字 {{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }">速度 {{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</span>
                <span v-if="speedCmp(m.id)" class="speed-cmp"><AqIcon name="bolt" :size="11" />{{ speedCmp(m.id) }}</span>
              </div>
              <div class="pb-ops">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
        </section>

        <section v-if="tokenLineRows.length" class="mt16">
          <div class="line-title">⚡ 超高速官方中转专线<small>密钥选「免费 + 按量计费」时可用：<b>官方原版接口直连</b>，零中间层、延迟不叠加——输入 / 缓存命中 / 输出分段计价，用多少付多少</small></div>
          <div class="card accent speed-hero">
            <div class="row wrap between" style="gap: 12px;">
              <div style="max-width: 620px;">
                <b style="font-size: 15px;">有人在意价格，有人只在意时间。</b>
                <p class="dim mt8" style="font-size: 12.5px;">
                  这条线是<b>官方原版接口直连</b>——不是蒸馏版、不是量化版、不经过任何转卖中间层。
                  同一颗大脑，更短的神经：<b>原版模型 + 超低首字延迟 + 旗舰模型全覆盖</b>。
                  按量计费，用多少付多少，账单逐笔可核对。
                </p>
              </div>
              <div class="row" style="gap: 6px;">
                <span class="tag acc">超高速</span>
                <span class="tag">按量计费</span>
              </div>
            </div>
          </div>
          <div v-if="paidLayout === 'card'" class="grid3">
            <div v-for="m in tokenLineRows" :key="m.id" class="card hoverable pcard" :class="{ paused: !!m.st }">
              <div class="row between">
                <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                <span v-if="m.chargeRate" class="tag warn num" title="卡片展示官方原价，实际按此倍率扣费">{{ m.chargeRate }}×</span>
                <span v-else-if="m.subsidized && rateBadgeOf(m)" class="tag warn num" title="官方原价 × 补贴倍率 = 现价">{{ rateBadgeOf(m) }}</span>
                <span v-else-if="m.baseInPrice != null || m.basePerImage != null" class="tag warn" title="VIP 专享拿货价已生效">VIP</span>
              </div>
              <div class="row wrap mt8" style="gap: 6px;">
                <span class="dim" style="font-size: 11px;">{{ liveMap[m.id] ? '最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '暂无近期数据' }}</span>
              </div>
              <!-- 2 号折扣钱包专用提示（20260924）：必须显式告知，否则用户"余额够却调不了"会以为故障 -->
              <div v-if="m.wallet2Only" class="row wrap mt8" style="gap: 6px;">
                <span class="tag acc" title="该模型只能用 2 号折扣钱包余额调用；两钱包资金独立、不支持互转">折扣钱包专用</span>
                <span class="dim" style="font-size: 11px;">需在控制台为「折扣钱包」独立充值后调用</span>
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
                  <div><i>输入</i><b class="num">¥{{ perMYuan(m.chargeRate ? m.baseInPrice : m.inPrice) }}</b><s v-if="!m.chargeRate && m.baseInPrice != null" title="官方原价">¥{{ perMYuan(m.baseInPrice) }}</s><em>/M</em></div>
                  <div><i>缓存命中</i><b class="num">¥{{ perMYuan(m.chargeRate ? m.baseCachePrice : m.cachePrice) }}</b><s v-if="!m.chargeRate && m.baseCachePrice != null" title="官方原价">¥{{ perMYuan(m.baseCachePrice) }}</s><em>/M</em></div>
                  <div><i>输出</i><span class="tag grad num">¥{{ perMYuan(m.chargeRate ? m.baseOutPrice : m.outPrice) }}</span><s v-if="!m.chargeRate && m.baseOutPrice != null" title="官方原价">¥{{ perMYuan(m.baseOutPrice) }}</s><em>/M</em></div>
                </div>
                <div class="dim mt8" style="font-size: 11.5px;">先付后用 · 用多少付多少 · 缓存命中更省</div>
              </template>
              <div class="pmetrics mt8 has-cmp">
                <span :class="{ dim: !latOf(liveMap[m.id]) }"><i>首字</i><b>{{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</b></span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }"><i>速度</i><b>{{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</b></span>
                <span v-if="speedCmp(m.id)" class="speed-cmp"><AqIcon name="bolt" :size="11" />{{ speedCmp(m.id) }}</span>
              </div>
              <div class="row between mt12">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">能力详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
          <div v-else class="plist">
            <div v-for="m in tokenLineRows" :key="m.id" class="card hoverable pbar" :class="{ paused: !!m.st }">
              <div class="pb-main">
                <div class="row between" style="gap: 8px;">
                  <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                  <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
                </div>
                <div class="row wrap mt8" style="gap: 6px;">
                  <span class="dim" style="font-size: 11px;">{{ liveMap[m.id] ? '最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '暂无近期数据' }}</span>
                  <span v-if="m.chargeRate" class="tag warn num" title="卡片展示官方原价，实际按此倍率扣费">{{ m.chargeRate }}×</span>
                  <span v-else-if="m.subsidized && rateBadgeOf(m)" class="tag warn num" title="官方原价 × 补贴倍率 = 现价">{{ rateBadgeOf(m) }}</span>
                  <span v-else-if="m.baseInPrice != null" class="tag warn" title="VIP 专享拿货价已生效">VIP</span>
                  <!-- 2 号折扣钱包专用（20260924）：条状布局空间紧，只留角标 + hover 说明 -->
                  <span v-if="m.wallet2Only" class="tag acc" title="该模型只能用 2 号折扣钱包余额调用（需在控制台为「折扣钱包」独立充值）；两钱包资金独立、不支持互转">折扣钱包专用</span>
                </div>
              </div>
              <div class="pb-price">
                <template v-if="m.perImage != null">
                  <span class="tag grad num">¥{{ microYuan(m.perImage) }}<em>/张</em></span>
                  <s v-if="m.basePerImage != null" class="num" title="官方原价">¥{{ microYuan(m.basePerImage) }}</s>
                </template>
                <template v-else>
                  <span class="pp"><i>输入</i><b class="num">¥{{ perMYuan(m.chargeRate ? m.baseInPrice : m.inPrice) }}</b><s v-if="!m.chargeRate && m.baseInPrice != null" title="官方原价">¥{{ perMYuan(m.baseInPrice) }}</s><em>/M</em></span>
                  <span class="pp"><i>缓存</i><b class="num">¥{{ perMYuan(m.chargeRate ? m.baseCachePrice : m.cachePrice) }}</b><s v-if="!m.chargeRate && m.baseCachePrice != null" title="官方原价">¥{{ perMYuan(m.baseCachePrice) }}</s><em>/M</em></span>
                  <span class="pp"><i>输出</i><b class="num acc">¥{{ perMYuan(m.chargeRate ? m.baseOutPrice : m.outPrice) }}</b><s v-if="!m.chargeRate && m.baseOutPrice != null" title="官方原价">¥{{ perMYuan(m.baseOutPrice) }}</s><em>/M</em></span>
                </template>
              </div>
              <div class="pb-meta has-cmp">
                <span :class="{ dim: !latOf(liveMap[m.id]) }">首字 {{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }">速度 {{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</span>
                <span v-if="speedCmp(m.id)" class="speed-cmp"><AqIcon name="bolt" :size="11" />{{ speedCmp(m.id) }}</span>
              </div>
              <div class="pb-ops">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
          <div v-if="!tokenLineRows.length" class="empty mt12"><b>未找到匹配的收费模型</b></div>
        </section>

        <!-- 按次计费分组 -->
        <section v-if="callLineRows.length" class="mt16">
          <div class="line-title">按次计费模型<small>密钥选「免费 + 按次计费」时可用：每次成功请求按模型单价结算一次，与生成长度无关——短输出场景更划算</small></div>
          <div v-if="paidLayout === 'card'" class="grid3">
            <div v-for="m in callLineRows" :key="m.id + ':call'" class="card hoverable pcard" :class="{ paused: !!m.st }">
              <div class="row between">
                <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
              </div>
              <div class="row mt8" style="gap: 7px; font-size: 12px;">
                <span class="dim" style="font-size: 11px;">{{ liveMap[m.id] ? '最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '暂无近期数据' }}</span>
              </div>
              <div class="row wrap mt8" style="gap: 6px;">
                <span class="tag grad num">¥{{ microYuan(m.price) }}<em>/次</em></span>
                <span v-if="m.priceView === 'agent'" class="tag" style="background: linear-gradient(135deg, #f59e0b, #d97706); color: #fff;" title="代理结算价由服务端确认，价差不代表保证收益">代理拿货价</span>
                <span v-else class="tag">{{ m.basePrice != null ? 'VIP 拿货价' : '正常价' }}</span>
              </div>
              <div v-if="m.priceView === 'agent' && m.basePrice != null" class="dim mt8"><b>当前结算身份：代理</b><dl class="price-compare"><dt>代理结算价</dt><dd>¥{{ microYuan(m.price) }}/次</dd><dt>官网零售价</dt><dd>¥{{ microYuan(m.basePrice) }}/次</dd><dt>每次价差</dt><dd>¥{{ microYuan(m.basePrice - m.price) }}</dd></dl><p>价差未扣除获客、支付及其他经营成本，不代表保证收益。</p></div>
              <div v-else-if="m.basePrice != null" class="dim mt8" style="font-size: 11.5px;">原价 ¥{{ microYuan(m.basePrice) }}/次 · VIP 专享拿货价已生效</div>
              <div class="pmetrics mt8 has-cmp">
                <span :class="{ dim: !latOf(liveMap[m.id]) }"><i>首字</i><b>{{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</b></span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }"><i>速度</i><b>{{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</b></span>
                <span v-if="speedCmp(m.id)" class="speed-cmp"><AqIcon name="bolt" :size="11" />{{ speedCmp(m.id) }}</span>
              </div>
              <div class="row between mt12">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">能力详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
          <div v-else class="plist">
            <div v-for="m in callLineRows" :key="m.id + ':callbar'" class="card hoverable pbar" :class="{ paused: !!m.st }">
              <div class="pb-main">
                <div class="row between" style="gap: 8px;">
                  <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                  <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
                </div>
                <div class="row wrap mt8" style="gap: 6px;">
                  <span class="dim" style="font-size: 11px;">{{ liveMap[m.id] ? '最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '暂无近期数据' }}</span>
                  <span v-if="m.priceView === 'agent'" class="tag" style="background: linear-gradient(135deg, #f59e0b, #d97706); color: #fff;" title="代理结算价由服务端确认，价差不代表保证收益">代理拿货价</span>
                  <span v-else class="tag">{{ m.basePrice != null ? 'VIP 拿货价' : '正常价' }}</span>
                </div>
              </div>
              <div class="pb-price">
                <span class="tag grad num pbar-price">¥{{ microYuan(m.price) }}<em>/次</em></span>
                <s v-if="m.basePrice != null" class="num" title="零售原价">¥{{ microYuan(m.basePrice) }}</s>
                <span v-if="m.priceView === 'agent' && m.basePrice != null" class="pp"><i>价差</i><b class="num acc">¥{{ microYuan(m.basePrice - m.price) }}</b></span>
              </div>
              <div class="pb-meta has-cmp">
                <span :class="{ dim: !latOf(liveMap[m.id]) }">首字 {{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }">速度 {{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</span>
                <span v-if="speedCmp(m.id)" class="speed-cmp"><AqIcon name="bolt" :size="11" />{{ speedCmp(m.id) }}</span>
              </div>
              <div class="pb-ops">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
        </section>

        <!-- GPT · Codex 专线 -->
        <section v-if="codexLineRows.length" class="mt16">
          <div class="line-title">GPT · Codex 专线<small>codex/ 前缀独占模型，ChatGPT 账号池直连：输入 / 缓存命中 / 输出分段计价，按量密钥即可调用</small></div>
          <div v-if="paidLayout === 'card'" class="grid3">
            <div v-for="m in codexLineRows" :key="m.id" class="card hoverable pcard" :class="{ paused: !!m.st }">
              <div class="row between">
                <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
              </div>
              <div class="row mt8" style="gap: 7px; font-size: 12px;">
                <span class="dim" style="font-size: 11px;">{{ liveMap[m.id] ? '最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '暂无近期数据' }}</span>
              </div>
              <div class="tp mt8">
                <div><i>输入</i><b class="num">¥{{ perMYuan(m.inPrice) }}</b><em>/M</em></div>
                <div><i>缓存命中</i><b class="num">¥{{ perMYuan(m.cachePrice) }}</b><em>/M</em></div>
                <div><i>输出</i><span class="tag grad num">¥{{ perMYuan(m.outPrice) }}</span><em>/M</em></div>
              </div>
              <div class="dim mt8" style="font-size: 11.5px;">Codex 账号池专线 · 缓存命中更省 · 先付后用</div>
              <div class="pmetrics mt8 has-cmp">
                <span :class="{ dim: !latOf(liveMap[m.id]) }"><i>首字</i><b>{{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</b></span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }"><i>速度</i><b>{{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</b></span>
                <span v-if="speedCmp(m.id)" class="speed-cmp"><AqIcon name="bolt" :size="11" />{{ speedCmp(m.id) }}</span>
              </div>
              <div class="row between mt12">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">能力详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
          <div v-else class="plist">
            <div v-for="m in codexLineRows" :key="m.id + ':bar'" class="card hoverable pbar" :class="{ paused: !!m.st }">
              <div class="pb-main">
                <div class="row between" style="gap: 8px;">
                  <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                  <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
                </div>
                <div class="row wrap mt8" style="gap: 6px;">
                  <span class="dim" style="font-size: 11px;">{{ liveMap[m.id] ? '最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '暂无近期数据' }}</span>
                </div>
              </div>
              <div class="pb-price">
                <span class="pp"><i>输入</i><b class="num">¥{{ perMYuan(m.inPrice) }}</b><em>/M</em></span>
                <span class="pp"><i>缓存</i><b class="num">¥{{ perMYuan(m.cachePrice) }}</b><em>/M</em></span>
                <span class="pp"><i>输出</i><b class="num acc">¥{{ perMYuan(m.outPrice) }}</b><em>/M</em></span>
              </div>
              <div class="pb-meta">
                <span :class="{ dim: !latOf(liveMap[m.id]) }">首字 {{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }">速度 {{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</span>
              </div>
              <div class="pb-ops">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
        </section>


        <!-- 官方中转 · 高速专线 -->
        <section v-if="officialLineRows.length" class="mt16">
          <div class="line-title">官方中转 · 高速专线<small>密钥选「官方中转」分组时可用：tlk/ 前缀独占模型，按官方原价 6 折分段计价</small></div>
          <div v-if="paidLayout === 'card'" class="grid3">
            <div v-for="m in officialLineRows" :key="m.id" class="card hoverable pcard" :class="{ paused: !!m.st }">
              <div class="row between">
                <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
              </div>
              <div class="row mt8" style="gap: 7px; font-size: 12px;">
                <span class="dim" style="font-size: 11px;">{{ liveMap[m.id] ? '最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '暂无近期数据' }}</span>
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
              </div>
              <div class="row between mt12">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">能力详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
          <div v-else class="plist">
            <div v-for="m in officialLineRows" :key="m.id + ':bar'" class="card hoverable pbar" :class="{ paused: !!m.st }">
              <div class="pb-main">
                <div class="row between" style="gap: 8px;">
                  <span class="mono pid" :title="'完整模型 ID：' + m.id"><i>{{ m.id.split('/')[0] }}/</i>{{ m.id.split('/').slice(1).join('/') }}</span>
                  <span v-if="m.st" class="tag bad" :title="m.st.title">{{ m.st.text }}</span>
                </div>
                <div class="row wrap mt8" style="gap: 6px;">
                  <span class="dim" style="font-size: 11px;">{{ liveMap[m.id] ? '最近活动 ' + fmtAgo(liveMap[m.id].last_ts) : '暂无近期数据' }}</span>
                </div>
              </div>
              <div class="pb-price">
                <span class="pp"><i>输入</i><b class="num">¥{{ perMYuan(m.inPrice) }}</b><em>/M</em></span>
                <span class="pp"><i>缓存</i><b class="num">¥{{ perMYuan(m.cachePrice) }}</b><em>/M</em></span>
                <span class="pp"><i>输出</i><b class="num acc">¥{{ perMYuan(m.outPrice) }}</b><em>/M</em></span>
              </div>
              <div class="pb-meta">
                <span :class="{ dim: !latOf(liveMap[m.id]) }">首字 {{ latOf(liveMap[m.id]) ? fmtLat(latOf(liveMap[m.id])) : '--' }}</span>
                <span :class="{ dim: !liveMap[m.id]?.avg_tps }">速度 {{ liveMap[m.id]?.avg_tps ? liveMap[m.id].avg_tps.toFixed(1) + ' tok/s' : '--' }}</span>
              </div>
              <div class="pb-ops">
                <CopyBtn :text="m.id" />
                <router-link :to="m.link" class="btn ghost sm">详情 <AqIcon name="arrow-right" :size="13" /></router-link>
              </div>
            </div>
          </div>
        </section>

        <!-- 免费与收费 · 必读说明 -->
        <div class="card mt24" style="max-width: 980px;">
          <b><AqIcon name="info" :size="16" />免费与收费 · 必读说明</b>
          <div class="mt12" style="display: grid; gap: 12px;">
            <div>
              <b style="font-size: 13.5px;">1 · 公益免费模型优先</b>
              <p class="dim" style="font-size: 13px;">当前标注为免费的模型注册即可使用；具体可用模型与实时状态以本页实时目录和实际响应为准。</p>
            </div>
            <div>
              <b style="font-size: 13.5px;">2 · acu/ 官方自营众筹专线</b>
              <p class="dim">acu/ 由<b>公共众筹池</b>统一付费：按次从池子扣站点额度，<b>不扣个人余额</b>；池子耗尽时 acu/ 暂不可用，到控制台「众筹算力池」注资即可点亮。aqua/ 按量计费为官方原版直连专线（超高速）；aqua/ 按次计费与 codex/ 按量计费按各自线路单价结算；完整模型前缀决定线路，请勿混用。</p>
            </div>
            <div>
              <b style="font-size: 13.5px;">3 · 个人余额与赞助分开</b>
              <p class="dim">付费调用使用个人余额；自愿赞助不等于个人充值，也不改变 acu/ 纯免费规则。支付前请核对资金用途。</p>
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
.tool-search { flex: 1 1 260px; min-width: 0; max-width: 420px; }

/* ---- 模型卡（免费视图 + 展开行） ---- */
.mcard { cursor: pointer; }
.mcard .mid { color: var(--txt0); font-weight: 700; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mcard.exhausted { opacity: .72; }
.caret { color: var(--txt3); transition: transform var(--t-fast); }
.caret.open { transform: rotate(180deg); }
.expand { border-top: 1px dashed var(--line); padding-top: 10px; }

/* ---- 官方自营免费专区 ---- */
.crowd { border-color: color-mix(in srgb, var(--acc) 34%, transparent); }
.crowd.off { border-color: color-mix(in srgb, var(--bad) 34%, transparent); }
.crowd-item { border: 1px solid var(--line); border-radius: var(--r-md); padding: 13px 15px; background: var(--bg1); }
.crowd-item .ci-id { color: var(--acc); font-weight: 700; font-size: 12.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.crowd-item .ci-t { font-size: 13px; color: var(--txt0); }
.crowd-item.cta { border-style: dashed; border-color: color-mix(in srgb, var(--warn) 45%, transparent); display: flex; flex-direction: column; gap: 8px; }
.crowd-item.cta b { color: var(--warn); }

/* ---- 收费卡 ---- */
/* 长条排版（bar 模式）：主信息 / 价格 / 指标 三区一行，操作靠右；<860px 退化为两列 */
.plist { display: flex; flex-direction: column; border-top: 1px solid var(--line-strong); }
.pbar {
  display: grid;
  grid-template-columns: minmax(0, 1.25fr) minmax(0, 1.25fr) auto;
  grid-template-areas: 'main price ops' 'meta price ops';
  align-items: center;
  gap: 10px 20px;
  border: 0;
  border-bottom: 1px solid var(--line);
  border-radius: 0;
  box-shadow: none;
  padding: 14px 16px;
}
.pbar:hover { transform: none; background: var(--bg3); }
.pbar > .pb-main { grid-area: main; min-width: 0; }
.pbar > .pb-price { grid-area: price; min-width: 0; }
.pbar > .pb-meta { grid-area: meta; }
.pbar > .pb-ops { grid-area: ops; display: flex; align-items: center; gap: 8px; justify-self: end; }
.pbar .pid { font-size: 13.5px; overflow-wrap: anywhere; word-break: break-word; white-space: normal; }
.pbar .pb-price { display: flex; align-items: baseline; flex-wrap: wrap; gap: 4px 12px; }
.pbar .pp { display: inline-flex; align-items: baseline; gap: 4px; white-space: nowrap; }
.pbar .pp i { font-style: normal; font-size: 11px; color: var(--txt2); }
.pbar .pp b { font-size: 13.5px; font-weight: 700; color: var(--txt0); }
.pbar .pp b.acc { color: var(--acc); }
.pbar .pp s { opacity: .65; font-size: 10.5px; }
.pbar .pp em { font-style: normal; font-size: 10.5px; color: var(--txt3); }
.pbar .pb-price > s { opacity: .65; font-size: 11.5px; }
.pbar .pbar-price { font-size: 14px; padding: 4px 11px; }
.pbar .pb-meta { display: flex; flex-wrap: wrap; gap: 4px 14px; font-size: 11.5px; color: var(--txt1); }
.pbar .pb-meta .speed-cmp { display: inline-flex; align-items: center; gap: 4px; font-weight: 600; color: var(--acc); }
.pbar.paused { opacity: .62; }
@media(max-width: 860px) {
  .toolbar { align-items: stretch; }
  .toolbar .chips, .toolbar .tool-search, .toolbar > .tag { width: 100%; max-width: none; }
  .paid-tools { width: 100%; flex-wrap: wrap; justify-content: flex-start; }

  .pbar { grid-template-columns: minmax(0, 1fr) auto; grid-template-areas: 'main ops' 'price ops' 'meta ops'; gap: 8px 12px; padding: 14px; }
  .pbar > .pb-ops { flex-direction: column; align-items: stretch; }
}

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
.pmetrics { display: grid; grid-template-columns: 1fr 1fr; gap: 4px; border-top: 1px dashed var(--line); padding-top: 8px; }
.pmetrics.has-cmp { grid-template-columns: 1fr 1fr; }
.pmetrics .speed-cmp { grid-column: 1 / -1; flex-direction: row; align-items: center; gap: 4px; font-size: 11px; font-weight: 600; color: var(--acc); }
.pmetrics span { display: flex; flex-direction: column; gap: 1px; }
.pmetrics i { font-style: normal; font-size: 10px; color: var(--txt2); }
.pmetrics b { font-size: 12px; font-weight: 700; color: var(--txt0); white-space: nowrap; }
.line-title { display: flex; align-items: baseline; gap: 8px; flex-wrap: wrap; font-size: 14.5px; font-weight: 700; color: var(--txt0); margin-bottom: 10px; }
.line-title small { font-weight: 400; font-size: 11.5px; color: var(--txt2); }
/* 按量专线卖点板块（官方原版直连 · 超高速） */
.speed-hero { margin-bottom: 12px; }
.speed-hero b { color: var(--txt0); }
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
