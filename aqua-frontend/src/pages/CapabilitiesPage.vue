<script setup lang="ts">
/* 模型中心 · 模型能力总览（自旧版 page-modelhub hub-pane:capabilities 平移）
 * 旧版对应逻辑：renderCapabilities / renderCapStats / CAPS / capForType / capMini / curCap 筛选 / cap-search */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useModels } from '@/composables/useModels'
import { dsMaintenance, platformLabel, typeLabel } from '@/composables/modelMeta'
import AqIcon from '@/components/AqIcon.vue'

const { models, loading, load } = useModels()
/* embedded：作为 ModelsPage 的能力视图内嵌渲染（隐藏页头/子导航，数据刷新由宿主的共享 store 负责） */
const props = defineProps<{ embedded?: boolean }>()
onMounted(() => {
  load()
  // 独立路由访问时保持旧版每 60 秒自动刷新；内嵌模式复用宿主列表页的定时刷新，避免双倍轮询
  if (!props.embedded) {
    refreshTimer = window.setInterval(() => load(true), 60000)
  }
})
let refreshTimer = 0
onUnmounted(() => { if (refreshTimer) window.clearInterval(refreshTimer) })

/* ---- 模型精确规格（按 ID 关键词匹配）（旧 MODEL_SPECS 平移） ---- */
/* ctx = 上下文 token 上限；size = 参数量；dims = 向量维度；released = 发布日期 */
interface ModelSpec { re: RegExp; ctx?: string; size?: string; dims?: number; released?: string }
const MODEL_SPECS: ModelSpec[] = [
  // ── 官方自营专线（aqua/，官方原版满血，按 ID 精确匹配置顶优先） ──
  { re: /aqua\/deepseek-v4-flash$/, ctx: "1M", size: "284B (13B active)", released: "2026-07" },
  { re: /aqua\/deepseek-v4-pro$/, ctx: "1M", size: "1.6T (49B active)", released: "2026-08" },
  { re: /aqua\/glm-5\.3-flash$/, ctx: "1M", size: "320B (18B active)", released: "2026-08" },
  { re: /aqua\/glm-5\.3$/, ctx: "1M", size: "744B (40B active)", released: "2026-08" },
  { re: /aqua\/glm-5\.2$/, ctx: "1M", size: "744B (40B active)", released: "2026-06" },
  // ── Nvidia / Meta Llama ──
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
  // ── Nvidia Nemotron ──
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
  // ── Nvidia 嵌入 / 检索 ──
  { re: /nv-embed-v1|nv-embedcode/, ctx: "0.5K", size: "7B", dims: 2048, released: "2024" },
  { re: /embed-qa-4/, ctx: "0.5K", size: "4B", dims: 1024, released: "2024" },
  { re: /nv-embedqa-mistral-7b/, ctx: "0.5K", size: "7B", dims: 1024, released: "2024" },
  { re: /nv-embedqa-1b-v1/, ctx: "0.5K", size: "1B", dims: 1024, released: "2024" },
  { re: /nemoretriever-1b/, ctx: "0.5K", size: "1B", dims: 1024, released: "2024-10" },
  { re: /nemotron-embed-vl-1b/, ctx: "0.5K", size: "1B", dims: 1024, released: "2024-11" },
  { re: /arctic-embed-l/, ctx: "0.5K", size: "335M", dims: 1024, released: "2024-04" },
  // ── Nvidia 视觉 ──
  { re: /neva-22b/, ctx: "4K", size: "22B", released: "2023-11" },
  { re: /vila/, ctx: "4K", size: "8B", released: "2024-02" },
  { re: /nvclip/, ctx: "0.5K", size: "1.3B", released: "2024-06" },
  // ── Nvidia 翻译 ──
  { re: /riva-translate/, ctx: "0.5K", size: "4B", released: "2024-03" },
  // ── DeepSeek ──
  { re: /deepseek-r1\/|deepseek-r1-distill-qwen-32b/, ctx: "128K", size: "671B (MoE)", released: "2025-01" },
  { re: /deepseek-r1-distill-qwen-14b/, ctx: "128K", size: "14B", released: "2025-01" },
  { re: /deepseek-r1-distill-qwen-7b/, ctx: "128K", size: "7B", released: "2025-01" },
  { re: /deepseek-r1-distill-llama-8b/, ctx: "128K", size: "8B", released: "2025-01" },
  { re: /deepseek-coder/, ctx: "16K", size: "6.7B", released: "2023-11" },
  { re: /deepseek-ai\/deepseek-v4-flash/, ctx: "1M", size: "284B (13B active)", released: "2026-04" },
  { re: /deepseek-v4-pro/, ctx: "1M", size: "1.6T (49B active)", released: "2026-04" },
  // ── Qwen ──
  { re: /qwq-32b/, ctx: "32K", size: "32B", released: "2024-11" },
  { re: /qwen2\.5-coder-32b/, ctx: "32K", size: "32B", released: "2024-11" },
  { re: /qwen2\.5-coder-7b/, ctx: "128K", size: "7B", released: "2024-11" },
  { re: /qwen2\.5-7b/, ctx: "32K", size: "7B", released: "2024-09" },
  { re: /qwen2-7b/, ctx: "32K", size: "7B", released: "2024-09" },
  // ── Google Gemma ──
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
  // ── Microsoft Phi ──
  { re: /phi-4-mini/, ctx: "16K", size: "3.8B", released: "2024-12" },
  { re: /phi-3\.5-mini/, ctx: "128K", size: "3.8B", released: "2024-08" },
  { re: /phi-3\.5-moe/, ctx: "128K", size: "42B (MoE)", released: "2024-08" },
  { re: /phi-3-medium/, ctx: "128K", size: "14B", released: "2024-06" },
  { re: /phi-3-small/, ctx: "8K", size: "7B", released: "2024-05" },
  { re: /phi-3-mini/, ctx: "128K", size: "3.8B", released: "2024-04" },
  { re: /phi-3-vision/, ctx: "128K", size: "4.2B", released: "2024-05" },
  { re: /phi-4-multimodal/, ctx: "16K", size: "5.6B", released: "2024-12" },
  { re: /kosmos-2/, ctx: "4K", size: "1.6B", released: "2023-06" },
  // ── Mistral ──
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
  // ── IBM Granite ──
  { re: /granite-34b-code/, ctx: "8K", size: "34B", released: "2024-05" },
  { re: /granite-3\.0-8b/, ctx: "8K", size: "8B", released: "2024-10" },
  { re: /granite-3\.0-3b/, ctx: "4K", size: "3B", released: "2024-10" },
  { re: /granite-8b-code/, ctx: "8K", size: "8B", released: "2024-05" },
  { re: /granite-guardian/, ctx: "8K", size: "8B", released: "2024-10" },
  // ── 其他国际模型 ──
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
  // ── 开源大模型（新接入） ──
  { re: /gpt-oss-120b/, ctx: "128K", size: "120B", released: "2025-08" },
  { re: /gpt-oss-20b/, ctx: "128K", size: "20B", released: "2025-08" },
  { re: /kimi-k2\.6/, ctx: "128K", size: "~200B (MoE)", released: "2025-07" },
  { re: /kimi-k3/, ctx: "128K", size: "~260B (MoE)", released: "2025-08" },
  { re: /minimax-m3/, ctx: "1M", size: "428B (~22B active)", released: "2025-08" },
  { re: /laguna-xs/, ctx: "8K", size: "—", released: "2025-06" },
  { re: /step-3\.7-flash/, ctx: "128K", size: "~30B", released: "2025-07" },
  // ── BGE 检索模型 ──
  { re: /bge-m3|baai\/bge-m3/, ctx: "8K", size: "568M", dims: 1024, released: "2024-01" },
  { re: /bge-large-zh/, ctx: "0.5K", size: "326M", dims: 1024, released: "2023-06" },
  { re: /bge-large-en/, ctx: "0.5K", size: "326M", dims: 1024, released: "2023-06" },
  { re: /bge-reranker-v2-m3/, ctx: "8K", size: "568M", dims: 0, released: "2024-01" },
  { re: /bce-reranker/, ctx: "0.5K", size: "278M", dims: 0, released: "2023-09" },
]
function modelSpec(id: string): ModelSpec {
  for (const s of MODEL_SPECS) {
    if (s.re.test(id)) return s
  }
  return {}
}

/* ---- 能力列定义（旧 CAPS / capForType / capMini 平移） ---- */
const CAPS = [
  { id: "chat",   label: "对话", tip: "支持 chat/completions 多轮对话" },
  { id: "stream", label: "流式", tip: "支持流式逐字返回" },
  { id: "vision", label: "视觉", tip: "可输入图片理解内容" },
  { id: "audio",  label: "音频", tip: "支持音频输入或输出" },
  { id: "emb",    label: "向量", tip: "支持 embeddings 接口" },
  { id: "rerank", label: "重排", tip: "支持 rerank 检索精排" },
  { id: "gen",    label: "生成", tip: "图像/视频/语音内容生成" },
  { id: "tools",  label: "工具", tip: "支持 function calling" }
]
function capForType(t: string): Record<string, number> {
  const c: Record<string, number> = {}
  c.chat = (t === "chat" || t === "vision") ? 1 : 0
  c.stream = (t === "chat" || t === "vision") ? 1 : 0
  c.vision = t === "vision" ? 1 : 0
  c.audio = (t === "asr" || t === "tts") ? 1 : 0
  c.emb = t === "embedding" ? 1 : 0
  c.rerank = t === "rerank" ? 1 : 0
  c.gen = (t === "image" || t === "video" || t === "tts") ? 1 : 0
  c.tools = (t === "chat") ? 0.5 : 0
  return c
}
function capMini(v: number) {
  if (v === 1) return { cls: 'yes', title: '支持', sym: 'check' }
  if (v === 0.5) return { cls: 'part', title: '有限支持', sym: '' }
  return { cls: 'no', title: '不支持', sym: 'cross' }
}

/* ---- 筛选状态（旧 curCap / cap-search） ---- */
const curCap = ref('all')
const capFilters = [
  { id: 'all', label: '全部' },
  { id: 'chat', label: '对话' },
  { id: 'vision', label: '视觉' },
  { id: 'embedding', label: '向量' },
  { id: 'rerank', label: '重排' },
  { id: 'asr', label: '语音识别' },
  { id: 'tts', label: '语音合成' },
  { id: 'moderation', label: '风控' },
  { id: 'image', label: '绘图' },
  { id: 'video', label: '视频' },
  { id: 'ip', label: 'IP' },
]
const kw = ref('')

/* ---- 过滤 + 卡片视图（旧 renderCapabilities 主体平移） ---- */
interface CapFlag { cls: string; text: string }
interface CapCard {
  id: string
  platform: string
  type: string
  stTag: CapFlag | null
  acu: CapFlag | null
  metrics: { label: string; value: string }[]
  typeText: string
  platformText: string
  icons: { label: string; tip: string; mini: { cls: string; title: string; sym: string } }[]

}
const viewCards = computed<CapCard[]>(() => {
  const q = kw.value.toLowerCase().trim()
  return models.value
    .filter(m => {
      if (curCap.value !== 'all' && m.type !== curCap.value) return false
      return q ? m.id.toLowerCase().indexOf(q) !== -1 : true
    })
    .map(m => {
      const spec = modelSpec(m.id)
      const c = capForType(m.type)
      // 官方自营专线（aqua/）官方支持 function calling，请求体原样透传
      if (m.platform === 'acu') c.tools = 1
      let stTag: CapFlag | null = null
      if (m.id.toLowerCase() === 'auto') stTag = { cls: 'flag-acu', text: '智能路由' }
      else if (dsMaintenance(m.id)) stTag = { cls: 'flag-ex', text: '维护中' }
      else if (m.status === 'exhausted') stTag = { cls: 'flag-ex', text: '耗尽' }
      else if (m.status === 'unavailable') stTag = { cls: 'flag-un', text: '不可用' }
      // 官方自营模型使用专属蓝色卡片（m-acu 由 CSS 定义专属主题）
      const acu = m.platform === 'acu' ? { cls: 'flag-acu', text: '官方自营' } : null
      const metrics: { label: string; value: string }[] = []
      if (spec.ctx) metrics.push({ label: '上下文', value: spec.ctx })
      if (spec.size) metrics.push({ label: '参数', value: spec.size })
      if (spec.dims) metrics.push({ label: '向量', value: spec.dims + 'D' })
      if (spec.released) metrics.push({ label: '发布', value: spec.released })
      const icons = CAPS.map(cp => ({ label: cp.label, tip: cp.label + ': ' + cp.tip, mini: capMini(c[cp.id]) }))
      return { id: m.id, platform: m.platform, type: m.type, stTag, acu, metrics, typeText: typeLabel(m.type), platformText: platformLabel(m.platform), icons }
    })
})
function modelLink(id: string) { return '/model/' + encodeURIComponent(id) }

/* ---- 统计区（旧 renderCapStats 平移：随当前筛选联动） ---- */
const stats = computed(() => {
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
</script>

<template>
  <section class="route-page">
    <div v-if="!props.embedded" class="models-page-head">
      <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/></svg></span>模型中心</h1>
      <p>全线模型一览与能力总览的统一入口：由 Nvidia NIM 与官方自营专线实时提供。</p>
    </div>
    <nav v-if="!props.embedded" class="hub-subnav" aria-label="模型中心子导航">
      <router-link class="hub-tab" to="/models" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/></svg></span>模型列表</router-link>
      <router-link class="hub-tab" to="/capabilities" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2 20 7v10l-8 5-8-5V7l8-5z"/><path d="M12 12v8"/><path d="m4 7 8 5 8-5"/><path d="M12 2v8"/></svg></span>模型能力</router-link>
    </nav>
    <div :class="{ 'hub-pane': !props.embedded }">
      <div class="cap-page">
        <p class="hub-desc">浏览所有模型的「能力卡片」——上下文 / 参数量 / 平台 / 类型一目了然。点击任意卡片可进入该模型专属详情页（含上下文知识库、支持与不支持参数、使用示例等）。</p>
        <div class="cap-stats">
          <div class="cap-stat-card"><div class="cap-stat-num">{{ stats.total }}</div><div class="cap-stat-label">模型总数</div></div>
          <div class="cap-stat-card"><div class="cap-stat-num">{{ stats.typeCount }}</div><div class="cap-stat-label">能力类型</div></div>
          <div class="cap-stat-card wide"><div class="cap-stat-types">
            <span v-for="p in stats.typePills" :key="p.label" class="stat-pill"><b>{{ p.count }}</b> {{ p.label }}</span>
          </div></div>
          <div class="cap-bar-card">
            <div v-for="b in stats.bars" :key="b.label" class="cap-bar">
              <span class="cap-bar-label">{{ b.label }}</span>
              <span class="cap-bar-track"><span class="cap-bar-fill" :style="{ width: b.pct + '%' }"></span></span>
              <span class="cap-bar-pct">{{ b.pct }}%</span>
            </div>
          </div>
        </div>
        <div class="filter-row">
          <span class="flabel">筛选</span>
          <div class="filters" id="cap-filter">
            <button v-for="f in capFilters" :key="f.id" class="pill" :class="{ active: curCap === f.id }" :data-cap="f.id" @click="curCap = f.id">{{ f.label }}</button>
          </div>
        </div>
        <div class="models-bar">
          <input class="search" id="cap-search" v-model="kw" type="text" placeholder="在能力总览中搜索模型…">
          <span class="count" id="cap-count">共 {{ viewCards.length }} 个模型</span>
        </div>
        <div class="cap-cards" id="cap-cards">
          <div v-if="loading && !models.length" class="model-empty">模型加载中…</div>
          <div v-else-if="!viewCards.length" class="model-empty">未找到匹配的模型</div>
          <template v-else>
            <router-link v-for="c in viewCards" :key="c.id" class="cap-card" :class="'m-' + c.platform" :to="modelLink(c.id)">
              <div class="cap-card-top">
                <span class="cap-dot" :class="'m-' + c.platform"></span>
                <span class="cap-card-name" :title="c.id">{{ c.id }}</span>
                <span v-if="c.stTag" class="cap-card-flag" :class="c.stTag.cls">{{ c.stTag.text }}</span>
              </div>
              <span v-if="c.acu" class="cap-card-flag" :class="c.acu.cls">{{ c.acu.text }}</span>
              <div class="cap-card-metrics">
                <span v-for="mt in c.metrics" :key="mt.label" class="metric"><b>{{ mt.label }}</b> {{ mt.value }}</span>
              </div>
              <div class="cap-card-platform">
                <span class="cap-type-badge" :class="'t-' + c.type">{{ c.typeText }}</span>
                <span class="cap-platform-label">{{ c.platformText }}</span>
              </div>
              <div class="cap-card-icons">
                <span v-for="ic in c.icons" :key="ic.label" class="cap-mini-wrap" :title="ic.tip">
                  <span class="cap-mini-label">{{ ic.label }}</span>
                  <span class="cap-mini" :class="ic.mini.cls" :title="ic.mini.title"><AqIcon v-if="ic.mini.sym" :name="ic.mini.sym" :size="12" /><template v-else>~</template></span>
                </span>
              </div>
            </router-link>
          </template>
        </div>
      </div>
    </div>
  </section>
</template>
