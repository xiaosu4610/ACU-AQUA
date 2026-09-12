<script setup lang="ts">
/* 模型中心 · 模型列表（自旧版 page-modelhub hub-pane:models 平移）
 * 旧版对应逻辑：loadModels / applyFilter / render / smartSort / healthTag / updateModelsNotice */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import CopyBtn from '@/components/CopyBtn.vue'
import { useModels, type ModelRow } from '@/composables/useModels'
import { dsMaintenance, hideTag, platformLabel, typeLabel } from '@/composables/modelMeta'
import CapabilitiesPage from './CapabilitiesPage.vue'
import AqIcon from '@/components/AqIcon.vue'

/* 视图切换：free 免费模型 / paid 收费模型 / cap 能力总览（URL ?view=paid|cap 可直达分享） */
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
})
let refreshTimer = 0
onUnmounted(() => { if (refreshTimer) window.clearInterval(refreshTimer) })
function retry() { load(true) }

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

/* ---- 过滤 + 渲染行（免费模型页：收费模型已隔离到专属页，此处只展示免费模型） ---- */
const viewRows = computed(() => {
  const kw = q.value.toLowerCase().trim()
  return orderedModels.value
    .filter(m => {
      if (m.paid) return false
      if (curPlatform.value !== 'all' && m.platform !== curPlatform.value) return false
      if (curType.value !== 'all' && m.type !== curType.value) return false
      return kw ? m.id.toLowerCase().indexOf(kw) !== -1 : true
    })
    .map(m => ({ row: m, st: statusOf(m), exhausted: isExhausted(m), health: healthOf(m) }))
})

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

/* ---- 收费模型专区：统一 aqua/ 前缀（按次/按量分组由密钥决定，实时健康分动态渲染） ---- */
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
    isImage: m.type === 'image',
    health: healthOf(m),
    st: statusOf(m),
    link: modelLink(m.id),
  })))
/** 按次分组 / 按量分组（同一模型两组都有时两栏都展示——用哪组计费取决于密钥分组） */
const paidCallLine = computed(() => paidModels.value.filter(m => m.groups.includes('per_call')))
const paidTokenLine = computed(() => paidModels.value.filter(m => m.groups.includes('per_token')))
/** 微元 → 元字符串（去尾零：2000→"0.002"，3800→"0.0038"） */
function microYuan(v?: number): string {
  if (v == null) return '--'
  return (v / 1e6).toFixed(6).replace(/0+$/, '').replace(/\.$/, '')
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
      </template>
      <template v-else-if="view === 'paid'">
      <p class="hub-desc">官方自营<b>收费模型统一使用 <code>aqua/</code> 前缀</b>：计费方式由你<b>密钥的计费分组</b>决定——「免费+按次」密钥按次扣费，「免费+按量」密钥按 tokens 三段扣费——先付后用、失败全额退回、绝不透支。<router-link to="/console?view=topup">在线充值即充即用</router-link>，免费模型不受余额影响。</p>
      <!-- 收费模型专区：按分组双栏（数据实时来自 /v1/models 的 groups 字段） -->
      <div v-if="paidModels.length" class="paid-models-sec">
        <div class="pms-head">
          <div class="pms-title">
            <span class="pms-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2l2.9 6.26 6.86.63-5.2 4.55 1.56 6.71L12 16.9 5.88 20.15l1.56-6.71-5.2-4.55 6.86-.63z"/></svg></span>
            <b>收费模型专区 · 官方自营</b>
            <span class="pms-promo" title="统一 aqua/ 前缀；按次还是按量由密钥分组决定，两个分组都有的模型用对应密钥即可调用">统一 aqua/ 前缀 · 计费方式由密钥分组决定</span>
          </div>
          <span class="pms-sub">失败自动全额退回 · 先付后用、绝不透支 · <router-link to="/console?view=topup">在线充值即充即用</router-link></span>
        </div>
        <!-- 按次计费分组（密钥选「免费+按次」时可用） -->
        <div v-if="paidCallLine.length" class="pms-line-title">按次计费分组<small>（密钥选「免费 + 按次计费」时可用：每次成功请求扣一次，与生成长度无关）</small></div>
        <div class="pms-grid">
          <div v-for="m in paidCallLine" :key="m.id + ':call'" class="pms-card" :class="{ paused: !!m.st }">
            <div class="pms-top">
              <code class="pms-id" :title="m.id">{{ m.id.replace(/^aqua\//, '') }}</code>
              <span v-if="m.st" class="pms-st" :title="m.st.title">{{ m.st.text }}</span>
              <span v-else-if="m.health" class="pms-health" :class="m.health.cls" :title="m.health.tip">健康 {{ m.health.score }}</span>
            </div>
            <!-- 按次计费：单次正式价（默认，后端当前模式）；VIP 用户展示拿货价 + 底部原价 -->
            <div v-if="m.mode === 'per_call'" class="pms-price">
              <b>¥{{ microYuan(m.price) }}</b><i>/次</i>
              <span class="pms-price-tag" :class="{ promo: m.subsidized || m.basePrice != null }">{{ m.basePrice != null ? 'VIP 拿货价' : (m.subsidized ? '限时补贴' : '正常价') }}</span>
            </div>
            <div v-if="m.mode === 'per_call' && m.basePrice != null" class="pms-base-price">原价 ¥{{ microYuan(m.basePrice) }}/次 · VIP 专享拿货价已生效</div>
            <!-- 按量计费：三段价 + 单次保底 + 补贴徽标 -->
            <div v-else class="pms-price pms-price-token">
              <div class="tp-row"><i>输入</i><b>¥{{ perMYuan(m.inPrice) }}</b><i class="tp-unit">/百万tokens</i></div>
              <div class="tp-row"><i>缓存命中</i><b>¥{{ perMYuan(m.cachePrice) }}</b><i class="tp-unit">/百万tokens</i></div>
              <div class="tp-row"><i>输出</i><b>¥{{ perMYuan(m.outPrice) }}</b><i class="tp-unit">/百万tokens</i></div>
              <div class="tp-floor">单次最低 ¥{{ microYuan(m.floor) }}<span v-if="m.subsidized" class="tp-subsidy" title="补贴额度有限，随时恢复原价">限时补贴</span></div>
            </div>
            <div class="pms-actions">
              <CopyBtn :text="m.id" />
              <router-link class="pms-detail" :to="m.link">能力详情<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg></router-link>
            </div>
          </div>
        </div>
        <!-- 按量计费分组（密钥选「免费+按量」时可用；三段价、无保底、先付后用） -->
        <div v-if="paidTokenLine.length" class="pms-line-title">按量计费分组<small>（密钥选「免费 + 按量计费」时可用：输入 / 缓存命中 / 输出分段计价，缓存命中大幅更省，无保底）</small></div>
        <div class="pms-grid">
          <div v-for="m in paidTokenLine" :key="m.id" class="pms-card" :class="{ paused: !!m.st }">
            <div class="pms-top">
              <code class="pms-id" :title="m.id">{{ m.id.replace(/^aqua\//, '') }}</code>
              <span v-if="m.st" class="pms-st" :title="m.st.title">{{ m.st.text }}</span>
              <span v-else-if="m.health" class="pms-health" :class="m.health.cls" :title="m.health.tip">健康 {{ m.health.score }}</span>
            </div>
            <!-- 图片模型：按张计费（VIP 展示拿货价 + 底部原价） -->
            <div v-if="m.isImage && m.perImage" class="pms-price">
              <b>¥{{ microYuan(m.perImage) }}</b><i>/张</i>
              <span class="pms-price-tag" :class="{ promo: m.basePerImage != null }">{{ m.basePerImage != null ? 'VIP 拿货价' : '按张计费' }}</span>
            </div>
            <div v-if="m.isImage && m.perImage && m.basePerImage != null" class="pms-base-price">原价 ¥{{ microYuan(m.basePerImage) }}/张 · VIP 专享拿货价已生效</div>
            <!-- 按量计费：三段价（无保底；缓存命中更省） -->
            <div v-else class="pms-price pms-price-token">
              <div class="tp-row"><i>输入</i><b>¥{{ perMYuan(m.inPrice) }}</b><i class="tp-unit">/百万tokens</i></div>
              <div class="tp-row"><i>缓存命中</i><b>¥{{ perMYuan(m.cachePrice) }}</b><i class="tp-unit">/百万tokens</i></div>
              <div class="tp-row"><i>输出</i><b>¥{{ perMYuan(m.outPrice) }}</b><i class="tp-unit">/百万tokens</i></div>
              <div class="tp-floor">先付后用 · 用多少付多少 · <span class="tp-cache-tip" title="重复前缀会命中缓存价，显著降低输入成本">缓存命中更省</span></div>
              <div v-if="m.baseInPrice != null" class="tp-base">VIP 拿货价已生效 · 原价：输入 ¥{{ perMYuan(m.baseInPrice) }} / 缓存 ¥{{ perMYuan(m.baseCachePrice) }} / 输出 ¥{{ perMYuan(m.baseOutPrice) }} 每百万tokens（保底 ¥{{ microYuan(m.baseFloor) }}）</div>
            </div>
            <div class="pms-actions">
              <CopyBtn :text="m.id" />
              <router-link class="pms-detail" :to="m.link">能力详情<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg></router-link>
            </div>
          </div>
        </div>
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
              <p>收费模型统一使用 <code>aqua/</code> 前缀，<b>计费方式由你密钥的计费分组决定</b>：在个人控制台创建密钥时选择「免费 + 按次计费」或「免费 + 按量计费」。两个分组的可用模型不同（上方双栏即两组各自可用列表；部分模型两个分组通用）。按次分组为高频调用与极致速度单独采购的专属算力；按量分组提供全系列大杯旗舰与文生图模型（含 <code>aqua/qwen-image-2.0</code>、<code>aqua/wan2.7-image</code> 图片模型，按张计费）。调用方式与免费模型完全一致（同一个接口，只是 <code>model</code> 换成它们），支持流式输出，对客户端完全透明。</p>
              <div class="pp-rules">
                <span>计费方式：<b>按次分组密钥</b>——每次<b>成功</b>请求按所用模型单价扣一次，<b>与生成长度无关</b>（各模型实时单价见上方专区）</span>
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
/* 子导航 Tab 由 router-link 改为按钮：去掉按钮默认边框/底色，其余外观沿用 legacy.css 的 .hub-tab */
button.hub-tab { border: 0; background: none; font-family: inherit; cursor: pointer; }

/* ---- 收费模型专区（pms-*）：价格 + 倒计时 + 实时健康分 ---- */
.paid-models-sec {
  border: 1px solid rgba(56, 189, 248, .3);
  background: linear-gradient(160deg, rgba(56,189,248,.07), rgba(129,140,248,.04) 55%, transparent);
  border-radius: 14px;
  padding: 18px 20px;
  margin: 14px 0 18px;
}
.pms-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap; margin-bottom: 14px; }
.pms-title { display: flex; align-items: center; gap: 10px; }
.pms-ic { width: 34px; height: 34px; border-radius: 9px; display: flex; align-items: center; justify-content: center; background: rgba(56,189,248,.14); color: var(--accent); flex: none; }
.pms-ic svg { width: 18px; height: 18px; }
.pms-title b { font-size: 16px; }
.pms-promo { font-size: 12px; color: #b45309; background: rgba(245,158,11,.14); border: 1px solid rgba(245,158,11,.32); border-radius: 999px; padding: 2px 10px; font-weight: 600; }
[data-theme="light"] .pms-promo { color: #92400e; }
.pms-sub { font-size: 12.5px; color: var(--muted); }
.pms-sub a { color: var(--accent); }
/* 分线标题（aqua/ 按次线 + tide/ 按量线） */
.pms-line-title { font-size: 13.5px; font-weight: 700; color: var(--text); margin: 12px 0 8px; display: flex; align-items: baseline; gap: 6px; flex-wrap: wrap; }
.pms-line-title:first-of-type { margin-top: 0; }
.pms-line-title code { font-family: var(--mono, monospace); font-size: 12px; background: rgba(56,189,248,.12); color: var(--accent); border-radius: 6px; padding: 1px 6px; }
.pms-line-title small { font-size: 11.5px; font-weight: 400; color: var(--muted); }
.tp-cache-tip { font-size: 10px; font-weight: 700; color: #16a34a; background: rgba(34,197,94,.14); border: 1px solid rgba(34,197,94,.32); border-radius: 999px; padding: 1px 7px; }
.pms-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); gap: 10px; }
.pms-card {
  border: 1px solid var(--border); border-radius: 12px; padding: 13px 14px;
  background: var(--card); display: flex; flex-direction: column; gap: 8px;
  transition: transform .18s ease, border-color .18s ease, box-shadow .18s ease;
}
.pms-card:hover { transform: translateY(-2px); border-color: var(--border-hover); box-shadow: 0 6px 18px rgba(0,0,0,.18); }
.pms-card.paused { opacity: .62; }
.pms-top { display: flex; align-items: center; justify-content: space-between; gap: 8px; min-width: 0; }
.pms-id { font-family: var(--mono, monospace); font-size: 12.5px; color: var(--text); font-weight: 700; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pms-health { flex: none; font-size: 11px; font-weight: 700; border-radius: 999px; padding: 2px 8px; }
.pms-health.h-excellent { background: rgba(34,197,94,.15); color: #22c55e; }
.pms-health.h-good { background: rgba(56,189,248,.15); color: #38bdf8; }
.pms-health.h-warn { background: rgba(245,158,11,.16); color: #f59e0b; }
.pms-health.h-bad { background: rgba(239,68,68,.16); color: #ef4444; }
.pms-st { flex: none; font-size: 11px; color: #ef4444; background: rgba(239,68,68,.12); border: 1px solid rgba(239,68,68,.3); border-radius: 999px; padding: 2px 8px; }
.pms-price { display: flex; align-items: baseline; gap: 5px; }
.pms-price b { font-size: 21px; font-weight: 800; color: #f59e0b; font-variant-numeric: tabular-nums; }
.pms-price i { font-style: normal; font-size: 12px; color: var(--muted); }
/* 按量计费三段价 */
.pms-price-token { flex-direction: column; align-items: stretch; gap: 3px; }
.pms-price-token .tp-row { display: flex; align-items: baseline; gap: 6px; }
.pms-price-token .tp-row i { font-style: normal; font-size: 11.5px; color: var(--muted); min-width: 48px; }
.pms-price-token .tp-row b { font-size: 16.5px; font-weight: 800; color: #f59e0b; font-variant-numeric: tabular-nums; }
.pms-price-token .tp-unit { font-size: 10.5px; min-width: 0; }
.pms-price-token .tp-floor { display: flex; align-items: center; gap: 6px; font-size: 11.5px; color: var(--muted); margin-top: 2px; padding-top: 5px; border-top: 1px dashed rgba(148,163,184,.28); }
.tp-subsidy { font-size: 10px; font-weight: 700; color: #16a34a; background: rgba(34,197,94,.14); border: 1px solid rgba(34,197,94,.32); border-radius: 999px; padding: 1px 7px; }
.pms-price-tag { margin-left: auto; font-size: 10.5px; font-weight: 700; border-radius: 999px; padding: 2px 8px; background: rgba(148,163,184,.15); color: var(--muted); }
.pms-price-tag.promo { background: linear-gradient(120deg, rgba(245,158,11,.2), rgba(251,191,36,.12)); color: #f59e0b; border: 1px solid rgba(245,158,11,.35); }
.pms-base-price { font-size: 11px; color: var(--muted); margin-top: 4px; padding-top: 4px; border-top: 1px dashed rgba(148,163,184,.22); }
.pms-base-price::before { content: "VIP "; font-weight: 700; color: #f59e0b; }
.tp-base { font-size: 11px; color: var(--muted); margin-top: 3px; }
.tp-base::before { content: "VIP "; font-weight: 700; color: #f59e0b; }
.pms-actions { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.pms-detail { display: inline-flex; align-items: center; gap: 3px; font-size: 12.5px; color: var(--accent); text-decoration: none; }
.pms-detail:hover { text-decoration: underline; }
.pms-detail svg { width: 13px; height: 13px; }
@media (max-width: 480px) {
  .pms-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .pms-card { padding: 11px 12px; }
  .pms-price b { font-size: 18px; }
}

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
</style>
