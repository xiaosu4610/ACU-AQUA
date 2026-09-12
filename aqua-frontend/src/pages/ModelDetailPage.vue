<script setup lang="ts">
/* 模型中心 · 模型详情（自旧版 page-model 平移）
 * 旧版对应逻辑：renderModelDetail / modelProfile / PARAM_TEMPLATES / MODEL_SPECS / MODEL_NOTES / modelSpec / modelExample / healthTag */
import { computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import CopyBtn from '@/components/CopyBtn.vue'
import { useModels } from '@/composables/useModels'
import { classifyModel, dsMaintenance, platformLabel, typeLabel } from '@/composables/modelMeta'
import AqIcon from '@/components/AqIcon.vue'

/* ---- 路由参数：#/model/{encodeURIComponent(id)}（vue-router 已解码一次，这里再兜底解码） ---- */
const route = useRoute()
const id = computed(() => {
  const raw = String(route.params.id || '')
  try { return decodeURIComponent(raw) } catch { return raw }
})

/* ---- 数据源：useModels 单例（找不到时旧版 fallback：modelMeta[id] || classify(id)，照常渲染） ---- */
const { models, load } = useModels()
onMounted(() => {
  load()
  // 旧版每 60 秒自动刷新（模型状态与健康分自动更新）
  refreshTimer = window.setInterval(() => load(true), 60000)
})
let refreshTimer = 0
onUnmounted(() => { if (refreshTimer) window.clearInterval(refreshTimer) })

const row = computed(() => models.value.find(m => m.id === id.value))
const meta = computed(() => row.value ? { platform: row.value.platform, type: row.value.type } : classifyModel(id.value))
/** 按量计费模型（按价目 mode 判断；统一 aqua/ 前缀后按量线模型 ID 也是 aqua/）：无保底、先付后用、缓存价引导 */
const isTide = computed(() => row.value?.mode === 'per_token')
const isTideImage = computed(() => isTide.value && meta.value.type === 'image')
/** 微元 → 元字符串（去尾零：2000→"0.002"） */
const microYuan = (v?: number) => (v == null ? '--' : (v / 1e6).toFixed(6).replace(/0+$/, '').replace(/\.$/, ''))
/** 元/百万tokens 价显示（0.05 → "0.05"） */
const perMYuan = (v?: number) => (v == null ? '--' : v.toFixed(4).replace(/0+$/, '').replace(/\.$/, ''))

/* ---- 状态徽标（旧 renderModelDetail 的 statusTag 平移） ---- */
const statusTag = computed(() => {
  if (dsMaintenance(id.value)) return { cls: 'm-status-exhausted', text: '服务暂停 · 维护中' }
  const st = row.value?.status
  if (st === 'exhausted') return { cls: 'm-status-exhausted', text: '额度已耗尽' }
  if (st === 'unavailable') return { cls: 'm-status-unavailable', text: '暂时不可用' }
  return null
})

/* ---- 健康评分徽标（旧 healthTag 平移）：根据近100次调用自动评分（后端计算，0-100） ---- */
const health = computed(() => {
  const h = row.value?.health
  if (!h || !h.total) return null
  const score = h.score != null ? h.score | 0 : 0
  let cls = 'h-bad'
  if (score >= 90) cls = 'h-excellent'
  else if (score >= 70) cls = 'h-good'
  else if (score >= 50) cls = 'h-warn'
  const rate = Math.round(((h.ok || 0) / h.total) * 100)
  const lat = h.avg_latency_ms != null ? (h.avg_latency_ms / 1000).toFixed(1) + 's' : '-'
  return { score, cls, tip: `近 ${h.total} 次调用的健康评分：${score}/100（成功率 ${rate}%，平均延迟 ${lat}）` }
})

/* ---- 按能力类型定义「支持的请求体参数」模板（旧 PARAM_TEMPLATES 平移） ---- */
interface ParamTpl { desc: string; params: [string, string, string, string][]; unsupported: string[]; limits: string[] }
const PARAM_TEMPLATES: Record<string, ParamTpl> = {
  chat: {
    desc: "通用对话 / 补全模型。向一个纯文本模型发送一轮或多轮对话消息，返回模型补全内容，支持 SSE 流式输出。",
    params: [
      ["messages", "array", "必填", "对话消息列表，每项含 role（system / user / assistant）与 content"],
      ["stream", "boolean", "false", "开启 SSE 流式输出，逐字返回"],
      ["temperature", "number", "1.0", "采样温度，越高输出越发散、越低越稳定"],
      ["top_p", "number", "1.0", "核采样阈值，与 temperature 二选一微调"],
      ["max_tokens", "integer", "—", "本次最大输出 token 数（受模型上限约束）"],
      ["stop", "array", "null", "停止序列，命中即结束生成"],
      ["frequency_penalty", "number", "0", "对已出现 token 的重复惩罚"],
      ["presence_penalty", "number", "0", "对新话题的鼓励程度"],
      ["seed", "integer", "null", "随机种子，固定后可复现结果（部分模型支持）"]
    ],
    unsupported: ["tools", "tool_choice", "response_format", "function_call", "functions", "logprobs", "top_logprobs", "n", "logit_bias", "user", "parallel_tool_calls", "audio", "stream_options"],
    limits: ["纯文本输入，不支持图像 / 音频 / 视频", "max_tokens 受各模型上下文窗口限制", "不支持 tools / function calling（特殊模型除外）"]
  },
  vision: {
    desc: "多模态视觉对话模型。除文本外可输入图片（URL 或 base64），模型能理解图像内容并作答。",
    params: [
      ["messages", "array", "必填", "对话消息，content 数组可含 type=image_url 的图片"],
      ["stream", "boolean", "false", "SSE 流式输出"],
      ["temperature", "number", "1.0", "采样温度"],
      ["top_p", "number", "1.0", "核采样"],
      ["max_tokens", "integer", "—", "最大输出 token 数"]
    ],
    unsupported: ["tools", "tool_choice", "response_format", "function_call", "functions", "logprobs", "n", "logit_bias", "user", "parallel_tool_calls", "audio", "stream_options"],
    limits: ["仅接受图片，不支持视频输入", "图片建议使用公开 URL 或 base64 编码，单张建议 < 10MB", "不支持音频输入"]
  },
  embedding: {
    desc: "向量化模型。将文本转换为高维向量，用于检索、相似度计算、RAG 知识库等场景。",
    params: [
      ["input", "string / array", "必填", "待向量化的文本，可一次传单条或多条"],
      ["encoding_format", "string", "float", "返回格式：float（浮点数组）或 base64"]
    ],
    unsupported: ["messages", "stream", "temperature", "top_p", "max_tokens", "stop", "frequency_penalty", "presence_penalty", "seed", "tools", "tool_choice", "response_format", "function_call", "functions", "n", "logit_bias", "user"],
    limits: ["仅支持文本输入", "不支持流式、temperature 等对话参数", "向量维度由模型决定（如 bge-m3 为 1024 维）"]
  },
  rerank: {
    desc: "重排模型。给定查询与候选文档列表，为每个文档打分排序，用于 RAG 检索结果精排。",
    params: [
      ["query", "string", "必填", "查询文本"],
      ["documents", "array", "必填", "候选文档列表"],
      ["top_n", "integer", "全部", "仅返回得分最高的前 N 个文档"],
      ["return_documents", "boolean", "false", "是否在结果中回带文档内容"]
    ],
    unsupported: ["messages", "stream", "temperature", "top_p", "max_tokens", "stop", "frequency_penalty", "presence_penalty", "seed", "tools", "tool_choice", "response_format", "function_call", "functions", "n", "logit_bias", "user"],
    limits: ["专用重排接口，不支持对话参数", "文档数量受上游限制（通常 ≤ 1024 条）"]
  },
  asr: {
    desc: "语音识别（ASR）模型。接收音频文件，返回识别出的文字内容。",
    params: [
      ["file", "file", "必填", "音频文件（multipart/form-data 上传）"],
      ["model", "string", "必填", "模型 ID"],
      ["response_format", "string", "json", "返回格式：json / text / verbose_json"],
      ["language", "string", "auto", "音频语言（如 zh / en，可留空自动识别）"]
    ],
    unsupported: ["messages", "stream", "temperature", "top_p", "max_tokens", "stop", "frequency_penalty", "presence_penalty", "seed", "tools", "tool_choice", "response_format", "function_call", "functions", "n", "logit_bias", "user"],
    limits: ["仅接受音频文件，不支持文本输入", "使用 multipart/form-data 上传", "支持绝大多数主流音频编码（wav / mp3 / flac / m4a 等）"]
  },
  tts: {
    desc: "语音合成（TTS）模型。将文本合成为自然语音，支持多种音色；部分模型支持声音克隆。",
    params: [
      ["input", "string", "必填", "待合成的文本"],
      ["voice", "string", "—", "音色 / 说话人 ID（因模型而异）"],
      ["response_format", "string", "mp3", "音频格式：mp3 / wav / pcm"],
      ["speed", "number", "1.0", "语速倍率"],
      ["prompt_audio", "string", "null", "参考音频 base64（语音克隆类模型）"],
      ["prompt_audio_url", "string", "null", "参考音频 URL（语音克隆类模型）"]
    ],
    unsupported: ["messages", "stream", "temperature", "top_p", "max_tokens", "stop", "frequency_penalty", "presence_penalty", "seed", "tools", "tool_choice", "response_format", "function_call", "functions", "n", "logit_bias", "user"],
    limits: ["仅支持文本合成，不支持对话", "长文本请分段（单次有字数限制）", "克隆音色需提供参考音频"]
  },
  moderation: {
    desc: "内容安全 / 风控模型。检测文本中是否含有违规、有害内容，返回各安全类别打分。",
    params: [
      ["input", "string / array", "必填", "待检测文本"],
      ["input_type", "string", "—", "输入类型（query / response，部分模型支持）"]
    ],
    unsupported: ["messages", "stream", "temperature", "top_p", "max_tokens", "stop", "frequency_penalty", "presence_penalty", "seed", "tools", "tool_choice", "response_format", "function_call", "functions", "n", "logit_bias", "user"],
    limits: ["专用检测接口，不支持对话参数", "返回的是分类打分配置，不生成文本内容"]
  },
  image: {
    desc: "文生图模型。根据文字描述生成图片，返回图片 URL（已缓存至本站 R2，24 小时内有效）。",
    params: [
      ["prompt", "string", "必填", "图片描述（越详细效果越好）"],
      ["size", "string", "1024x1024", "图片尺寸，如 1024x1024 / 768x1344 / 1344x768"],
      ["n", "integer", "1", "一次生成张数"],
      ["negative_prompt", "string", "null", "负面提示词（避免出现的内容，部分模型支持）"],
      ["response_format", "string", "url", "返回格式：url 或 b64_json"]
    ],
    unsupported: ["messages", "stream", "temperature", "top_p", "max_tokens", "stop", "frequency_penalty", "presence_penalty", "seed", "tools", "tool_choice", "response_format(含义不同)", "function_call", "functions", "logit_bias", "user", "audio", "stream_options"],
    limits: ["仅支持文字描述生成，不作为对话模型使用", "生成结果缓存 24 小时后自动清理", "提示词可能存在内容审核"]
  },
  video: {
    desc: "文生视频模型。根据文字描述生成一段视频（耗时较长，需等待）。",
    params: [
      ["prompt", "string", "必填", "视频内容描述"],
      ["size", "string", "—", "视频尺寸（因模型而异）"],
      ["duration", "integer", "—", "生成时长（因模型而异）"],
      ["negative_prompt", "string", "null", "负面提示词（部分模型支持）"]
    ],
    unsupported: ["messages", "stream", "temperature", "top_p", "max_tokens", "stop", "frequency_penalty", "presence_penalty", "seed", "tools", "tool_choice", "response_format", "function_call", "functions", "logit_bias", "user", "audio", "stream_options"],
    limits: ["生成耗时较长（数秒至数分钟），请耐心等待", "不适合流式场景", "每月免费额度有限"]
  },
  ip: {
    desc: "IP 归属地查询。返回请求来源 IP 的地理位置信息。",
    params: [
      ["ip", "string", "可选", "要查询的 IP（缺省为请求者 IP）"]
    ],
    unsupported: ["messages", "stream", "temperature", "top_p", "max_tokens", "stop", "frequency_penalty", "presence_penalty", "seed", "tools", "tool_choice", "response_format", "function_call", "functions", "n", "logit_bias", "user", "model", "audio", "stream_options"],
    limits: ["专用查询接口", "返回地理信息，不参与对话生成"]
  }
}

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
function modelSpec(id: string): ModelSpec | null {
  for (const s of MODEL_SPECS) {
    if (s.re.test(id)) return s
  }
  return null
}

/* ---- 热门模型定制描述（按模型名关键词匹配，先匹配先生效）（旧 MODEL_NOTES 平移） ---- */
interface ModelNote { match: RegExp; name: string; desc: string }
const MODEL_NOTES: ModelNote[] = [
  // ── 官方自营专线（aqua/，官方原版满血，顺序匹配置顶优先） ──
  { match: /aqua\/deepseek-v4-flash$/, name: "DeepSeek V4 Flash", desc: "官方原版满血 DeepSeek-V4-Flash-0731。MoE 架构总参 284B / 激活 13B（MIT 开源），1M 上下文、单次最大输出 384K，支持思考 / 非思考双模式（含 Think Max 深度档）。0731 版重点强化 Agent 与编程：Terminal-Bench 2.1 达 82.7、DeepSWE 54.4，高频调用与长任务的性价比之选。" },
  { match: /aqua\/deepseek-v4-pro$/, name: "DeepSeek V4 Pro", desc: "官方原版满血 DeepSeek-V4-Pro-0813 正式版。旗舰 MoE 总参 1.6T / 激活 49B（MIT 开源），1M 上下文、单次最大输出 384K，思考三档（Non-Think / High / Max）。Agentic Coding 开源最佳：SWE-bench Verified 79.4%、Codeforces 2919、GPQA-Diamond 89.1%，复杂推理与竞赛级代码旗舰。" },
  { match: /aqua\/glm-5\.3-flash$/, name: "GLM-5.3-Flash", desc: "官方原版满血 GLM-5.3-Flash（320B-A18B MoE，MIT 开源），GLM-5 系列首个原生多模态基座（文本 / 图片 / 视频输入）。稀疏 + 线性混合注意力让注意力计算量降约 3 倍、KV 缓存降约 4.4 倍。1M 上下文、最大输出 128K，DeepSWE v1.1 63.4 全面超越上代 GLM-5.2，AA 智能指数 57 持平 Claude Opus 4.8 而成本仅约其十分之一。" },
  { match: /aqua\/glm-5\.3$/, name: "GLM-5.3", desc: "官方原版满血 GLM-5.3 旗舰（与 5.2 同 744B-A40B 基座，纯后训练 Scaling，MIT 开源）。1M 上下文、最大输出 128K，深度思考强制开启（reasoning_effort low / high / max）。Terminal-Bench 3.0 28.3 开源第一、DeepSWE v1.1 66.9，编程（Built to Code）与网络安全（CyberGym 84.5）双修。" },
  { match: /aqua\/glm-5\.2$/, name: "GLM-5.2", desc: "官方原版满血 GLM-5.2（744B-A40B MoE，MIT 开源），面向长任务时代的旗舰。Solid 1M 上下文（IndexShare 架构，1M 长度下单 token 计算量降约 2.9 倍）、最大输出 128K，思考力度可调（High / Max）。Terminal-Bench 2.1 达 81.0，长程 Agent 可稳定运行 12 小时以上，Code Arena 百万用户盲测全球第一。" },
  { match: /deepseek-v4-pro|deepseek-v4-flash-0/, name: "DeepSeek-V4", desc: "DeepSeek 最新旗舰模型，兼顾推理能力与速度，支持超大上下文，中文表现优秀。" },
  { match: /deepseek-r1/, name: "DeepSeek-R1", desc: "DeepSeek 深度推理模型，擅长数学、代码、逻辑推理，输出会展示思考过程。" },
  { match: /deepseek-coder/, name: "DeepSeek-Coder", desc: "代码专项模型，精通多种编程语言，适合代码生成、补全与解释。" },
  { match: /llama-3\.3-70b-instruct$/, name: "Llama 3.3 70B", desc: "Meta Llama 3.3 系列，70B 参数旗舰对话模型，继承 405B 能力但更经济。" },
  { match: /llama-3\.3/, name: "Llama 3.3", desc: "Meta Llama 3.3 系列，70B 参数旗舰对话模型，均衡的较强能力与效率。" },
  { match: /llama-3\.1-405b/, name: "Llama 3.1 405B", desc: "Meta 最大规模开源模型，405B 参数顶级能力，适合复杂任务。" },
  { match: /llama-3\.1-70b/, name: "Llama 3.1 70B", desc: "70B 参数的强对话模型，能力与成本的优秀平衡点。" },
  { match: /llama-3\.2-(1b|3b)/, name: "Llama 3.2 小模型", desc: "轻量级模型，低延迟、低资源占用，适合简单对话与移动端场景。" },
  { match: /nemotron/, name: "Nvidia Nemotron", desc: "Nvidia 自家对话系列，面向指令跟随与推理优化，参数规模选择丰富。" },
  { match: /gemma-3/, name: "Gemma 3", desc: "Google 开源轻量模型，多模态与多语言，边端友好。" },
  { match: /phi-3|phi-4/, name: "Phi 系列", desc: "Microsoft 小模型系列，以较小参数量达成较强常识推理能力。" },
  { match: /mistral-large/, name: "Mistral Large", desc: "Mistral 高端旗舰，多语言能力强，适合复杂推理任务。" },
  { match: /mixtral/, name: "Mixtral", desc: "Mistral 多专家稀疏模型（MoE），推理高效。" },
  { match: /qwen|qwen2\.5|qwq/, name: "通义千问 Qwen", desc: "阿里巴巴开源系列，中文能力出色的全能模型。" },
  { match: /guard|nsfw|nonescape|security.*filter/, name: "安全风控", desc: "内容安全检测模型，用于识别违规、有害、敏感内容。" }
]

/* ---- 官方自营专线（aqua/）官方参数与限制：网关对请求体原样透传，官方能力即本站能力 ---- */
interface AcuProfile { limits: string[]; extraParams?: [string, string, string, string][]; unsupported: string[] }
const ACU_UNSUPPORTED = ["function_call", "functions", "logprobs", "top_logprobs", "n", "logit_bias", "user", "audio"]
const ACU_PROFILES: Record<string, AcuProfile> = {
  'aqua/deepseek-v4-flash': {
    limits: [
      "官方原版满血 DeepSeek-V4-Flash-0731：MoE 总参 284B / 激活 13B（MIT 开源权重）",
      "上下文 1M tokens，单次最大输出 384K tokens",
      "思考模式：支持非思考与思考双模式（默认思考，最高 Think Max 深度档）",
      "官方支持 Tool Calls / JSON Output / 结构化输出 / 对话前缀续写，请求体参数原样透传",
      "AQUA 专线通道有全局并发保护，高峰期自动排队等待，请勿重复提交"
    ],
    unsupported: ACU_UNSUPPORTED
  },
  'aqua/deepseek-v4-pro': {
    limits: [
      "官方原版满血 DeepSeek-V4-Pro-0813 正式版：旗舰 MoE 总参 1.6T / 激活 49B（MIT 开源权重）",
      "上下文 1M tokens，单次最大输出 384K tokens",
      "思考三档：Non-Think（快速响应）/ Think High / Think Max（最深推理）",
      "官方支持 Tool Calls / JSON Output / 结构化输出 / 对话前缀续写，请求体参数原样透传",
      "AQUA 专线通道有全局并发保护，高峰期自动排队等待，请勿重复提交"
    ],
    unsupported: ACU_UNSUPPORTED
  },
  'aqua/glm-5.2': {
    limits: [
      "官方原版满血 GLM-5.2：MoE 总参 744B / 激活 40B（MIT 开源权重）",
      "Solid 1M 上下文（IndexShare 架构），单次最大输出 128K tokens",
      "思考力度可调：High（平衡）/ Max（深度）档位，none / minimal 可放弃思考",
      "官方支持 Tool Calls / JSON 结构化输出 / 上下文缓存 / 流式输出，请求体参数原样透传",
      "AQUA 专线通道有全局并发保护，高峰期自动排队等待，请勿重复提交"
    ],
    extraParams: [
      ["thinking", "object", "enabled", "思维链开关（GLM-5.2 及以上支持）：{\"type\": \"enabled\"}"],
      ["reasoning_effort", "string", "max", "思考力度：none / minimal / high / max（GLM-5.2 档位）"]
    ],
    unsupported: ACU_UNSUPPORTED
  },
  'aqua/glm-5.3': {
    limits: [
      "官方原版满血 GLM-5.3 旗舰：744B-A40B MoE（MIT 开源权重）",
      "上下文 1M tokens，单次最大输出 128K tokens",
      "深度思考强制开启（不可关闭），reasoning_effort 支持 low / high / max（默认 max）",
      "官方支持 Tool Calls / JSON 结构化输出 / 上下文缓存 / 流式输出，请求体参数原样透传",
      "AQUA 专线通道有全局并发保护，高峰期自动排队等待，请勿重复提交"
    ],
    extraParams: [
      ["thinking", "object", "enabled", "思维链开关：{\"type\": \"enabled\"}（GLM-5.3 强制开启）"],
      ["reasoning_effort", "string", "max", "思考力度：low / high / max（默认 max）"]
    ],
    unsupported: ACU_UNSUPPORTED
  },
  'aqua/glm-5.3-flash': {
    limits: [
      "官方原版满血 GLM-5.3-Flash：320B-A18B MoE（MIT 开源权重），稀疏 + 线性混合注意力架构",
      "上下文 1M tokens，单次最大输出 128K tokens",
      "深度思考默认开启且不可关闭，reasoning_effort 支持 low / high / max",
      "GLM-5 系列首个原生多模态基座（文本 / 图片 / 视频输入），本站专线当前以文本对话为主",
      "官方支持 Tool Calls / JSON 结构化输出 / 上下文缓存 / 流式输出，请求体参数原样透传",
      "AQUA 专线通道有全局并发保护，高峰期自动排队等待，请勿重复提交"
    ],
    extraParams: [
      ["thinking", "object", "enabled", "思维链开关：{\"type\": \"enabled\"}（GLM-5.3-Flash 强制开启）"],
      ["reasoning_effort", "string", "max", "思考力度：low / high / max（默认 max）"]
    ],
    unsupported: ACU_UNSUPPORTED
  }
}

/* ---- 档案合成（旧 modelProfile 平移）：合并专属描述 + 平台说明 ---- */
const profile = computed(() => {
  const mid = id.value
  const m = meta.value
  const t = m.type || 'chat'
  const base = PARAM_TEMPLATES[t] || PARAM_TEMPLATES.chat
  let note: ModelNote | null = null
  for (const n of MODEL_NOTES) {
    if (n.match.test(mid)) { note = n; break }
  }
  const acu = m.platform === 'acu' ? ACU_PROFILES[mid] : undefined
  const desc = (note ? note.name + "。" + note.desc + " " : "") + base.desc
  const limits = base.limits.slice()
  // 平台特定限制
  if (acu) {
    // 官方自营专线：完全按官方参数与限制展示（覆盖通用 chat 模板）
    limits.length = 0
    limits.push(...acu.limits)
  } else if (m.platform === 'nvidia') {
    limits.unshift("由 Nvidia NIM 提供，网关密钥池自动轮换（单密钥 38 次/分钟）")
  }
  return {
    typeText: typeLabel(t),
    desc,
    params: acu?.extraParams ? [...base.params, ...acu.extraParams] : base.params,
    limits,
    noteName: note ? note.name : null,
    spec: modelSpec(mid),
    unsupported: acu ? acu.unsupported : base.unsupported || []
  }
})

/* ---- 调用示例（旧 modelExample 平移） ---- */
const example = computed(() => {
  const mid = id.value
  const t = meta.value.type
  let body = ''
  if (t === 'chat' || t === 'vision') {
    body = '{"model":"' + mid + '","messages":[{"role":"user","content":"你好"}],"stream":true}'
  } else if (t === 'embedding') {
    body = '{"model":"' + mid + '","input":"你好"}'
  } else if (t === 'rerank') {
    body = '{"model":"' + mid + '","query":"你好","documents":["文档A","文档B"]}'
  } else if (t === 'asr') {
    return '# 使用 multipart 上传音频文件\ncurl https://api.ltzy.top/v1/audio/transcriptions \\\n  -F "file=@audio.wav" \\\n  -F "model=' + mid + '"'
  } else if (t === 'tts') {
    body = '{"model":"' + mid + '","input":"你好，欢迎使用 AQUA","voice":"default"}'
  } else if (t === 'moderation') {
    body = '{"model":"' + mid + '","input":"待检测文本"}'
  } else if (t === 'image') {
    body = '{"model":"' + mid + '","prompt":"一只可爱的橘猫","size":"1024x1024"}'
  } else if (t === 'video') {
    body = '{"model":"' + mid + '","prompt":"一只小狗在草地上奔跑"}'
  } else if (t === 'ip') {
    return 'curl https://api.ltzy.top/v1/ip_location\n  -H "Content-Type: application/json"\n  -d \'{"ip":""}\''
  }
  return 'curl https://api.ltzy.top/v1/chat/completions \\\n  -H "Content-Type: application/json" \\\n  -H "Authorization: Bearer sk-****" \\\n  -d \'' + body + '\''
})
</script>

<template>
  <section class="route-page">
    <div class="model-detail">
      <router-link to="/capabilities" class="back-link"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M19 12H5M12 19l-7-7 7-7"/></svg>返回模型能力</router-link>
      <div class="md-head">
        <h1><code>{{ id }}</code></h1>
        <div class="md-tags">
          <span class="mtag" :class="'m-' + meta.platform">{{ platformLabel(meta.platform) }}</span>
          <span class="mtag" :class="'m-' + meta.platform">{{ profile.typeText }}</span>
          <span v-if="row?.paid && row?.mode === 'per_token' && row?.in_price != null && row?.per_image == null" class="mtag m-paid" :title="isTide ? '按量计费：输入/缓存命中/输出分段计价，用多少付多少，详见下方计费说明' : '按量计费：输入/缓存命中/输出分段计价，单次设最低消费，详见下方计费说明'">
            收费模型 · 按量 ¥{{ perMYuan(row.in_price) }}/百万tokens 起{{ isTide ? '' : (row.subsidized ? ' · 限时补贴' : '') }}
          </span>
          <span v-else-if="row?.paid && (row?.price_micro || row?.per_image != null)" class="mtag m-paid" :title="isTideImage ? '按张计费：n 参数控制张数，详见下方计费说明' : '预充值按次计费，详见下方计费说明'">
            收费模型 · ¥{{ ((row.price_micro ?? row.per_image) / 1_000_000).toFixed(3) }}/{{ isTideImage ? '张' : '次' }} · 按{{ isTideImage ? '张' : '次' }}计费
          </span>
          <span v-else-if="meta.platform" class="mtag m-free" title="本站免费模型，不收一分钱">免费模型</span>
          <span v-if="statusTag" class="mtag" :class="statusTag.cls">{{ statusTag.text }}</span>
          <span v-if="health" class="mtag m-health" :class="health.cls" :title="health.tip">健康 {{ health.score }}</span>
        </div>
        <CopyBtn :text="id" label="复制 ID" />
      </div>
      <!-- 模型上下文知识库区块 -->
      <div v-if="profile.spec" class="md-block md-spec-grid">
        <div v-if="profile.spec.ctx" class="spec-item"><div class="spec-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg></div><div class="spec-label">上下文窗口</div><div class="spec-val">{{ profile.spec.ctx }}</div></div>
        <div v-if="profile.spec.size" class="spec-item"><div class="spec-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18"/><path d="M9 21V9"/></svg></div><div class="spec-label">参数量</div><div class="spec-val">{{ profile.spec.size }}</div></div>
        <div v-if="profile.spec.dims" class="spec-item"><div class="spec-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a16 16 0 0 1 0 18M12 3a16 16 0 0 0 0 18"/></svg></div><div class="spec-label">向量维度</div><div class="spec-val">{{ profile.spec.dims }}</div></div>
        <div v-if="profile.spec.released" class="spec-item"><div class="spec-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2"/><path d="M16 2v4M8 2v4M3 10h18"/></svg></div><div class="spec-label">发布日期</div><div class="spec-val">{{ profile.spec.released }}</div></div>
        <div class="spec-item"><div class="spec-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg></div><div class="spec-label">上游平台</div><div class="spec-val">{{ platformLabel(meta.platform) }}</div></div>
        <div class="spec-item"><div class="spec-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2 2 7l10 5 10-5-10-5z"/><path d="m2 17 10 5 10-5"/></svg></div><div class="spec-label">能力类型</div><div class="spec-val">{{ profile.typeText }}</div></div>
        <div v-if="profile.noteName" class="spec-item"><div class="spec-icon"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="M12 8v4M12 16h.01"/></svg></div><div class="spec-label">模型系列</div><div class="spec-val">{{ profile.noteName }}</div></div>
      </div>
      <div class="md-block"><h3>模型能力简介</h3><p class="md-desc">{{ profile.desc }}</p></div>
      <!-- 收费模型计费说明（仅付费模型显示） -->
      <div v-if="row?.paid && (row?.mode === 'per_token' || row?.price_micro)" class="md-block md-billing">
        <h3>计费说明</h3>
        <div class="bill-price">
          <template v-if="row?.mode === 'per_token' && row?.per_image == null">
            <b>¥{{ perMYuan(row.in_price) }}<span>/百万tokens（输入）</span></b>
            <span class="bill-promo">缓存命中 ¥{{ perMYuan(row.cache_price) }} · 输出 ¥{{ perMYuan(row.out_price) }}（元/百万tokens）{{ isTide ? ' · 无保底，用多少付多少' : ' · 单次最低消费 ¥' + microYuan(row.floor_micro) }}{{ row.subsidized ? ' · 官方限时补贴价' : '' }}</span>
          </template>
          <template v-else-if="isTideImage">
            <b>¥{{ (row.price_micro / 1_000_000).toFixed(3) }}<span>/张</span></b>
            <span class="bill-promo">按张计费 · n 参数控制张数（1~10 张）</span>
          </template>
          <template v-else>
            <b>¥{{ (row.price_micro / 1_000_000).toFixed(3) }}<span>/次</span></b>
            <span class="bill-promo">正式价 · 每次成功请求扣一次，与生成长度无关</span>
          </template>
        </div>
        <ul class="md-limits">
          <li v-if="row?.mode === 'per_token' && row?.per_image == null">按量计费：输入 / 缓存命中 / 输出按 tokens 分段计价，<b>用多少付多少</b>{{ isTide ? '；重复对话前缀命中缓存价，输入成本大幅更低' : '' }}</li>
          <li v-else-if="isTideImage">按张计费：每次成功请求按 <code>n</code>（张数）扣费，失败自动全额退回</li>
          <li v-else>按次计费（正式价）：每次<b>成功</b>请求扣一次，与生成长度无关（写一句话和写一千字同价）</li>
          <li v-if="isTide">先付后用：发起请求按预估预扣（输入 + <code>max_tokens</code> 输出上限），完成后<b>多退少补</b>；可在请求中调小 <code>max_tokens</code> 降低单次预扣</li>
          <li>预充值制：余额用完自动返回 402 停止服务，<b>绝不透支</b>；<router-link to="/console?view=topup" style="color:var(--accent);">在线充值即时到账</router-link>（支付金额 100% 全额到账）</li>
          <li>失败不扣费：上游失败 / 网络中断 / 服务异常，预扣金额<b>自动全额退回</b></li>
          <li>账目透明：每次扣费、余额、请求明细在<router-link to="/console" style="color:var(--accent);">个人控制台</router-link>实时可查，流水永久留存</li>
          <li>调用方式与免费模型完全一致：同一接口、同一密钥，<code>model</code> 填本模型 ID 即可；需注册登录并使用个人密钥</li>
        </ul>
        <p class="md-desc" style="margin-top:8px;">除本模型外的<b>免费模型注册即用、不收一分钱</b>（完整清单见模型中心），免费与收费互不影响，放心使用。</p>
      </div>
      <div class="md-block"><h3>支持的请求参数</h3>
        <div class="md-table-wrap"><table class="md-table"><thead><tr><th>参数名</th><th>类型</th><th>默认值</th><th>说明</th></tr></thead><tbody>
          <tr v-for="p in profile.params" :key="p[0]"><td class="pname"><code>{{ p[0] }}</code></td><td>{{ p[1] }}</td><td>{{ p[2] }}</td><td>{{ p[3] }}</td></tr>
        </tbody></table></div>
      </div>
      <div v-if="profile.unsupported && profile.unsupported.length" class="md-block"><h3>不支持的参数</h3>
        <p class="md-desc" style="margin-bottom:10px;">以下 OpenAI 标准参数在该模型类型下不可用或无意义：</p>
        <div class="md-unsupported-grid">
          <span v-for="p in profile.unsupported" :key="p" class="unsupp-pill"><span class="unsupp-x"><AqIcon name="cross" :size="11" /></span><code>{{ p }}</code></span>
        </div>
      </div>
      <div class="md-block"><h3>限制 / 不支持的功能</h3><ul class="md-limits"><li v-for="l in profile.limits" :key="l">{{ l }}</li></ul></div>
      <div class="md-block"><h3>调用示例</h3><pre><CopyBtn :text="example" label="复制" /><code>{{ example }}</code></pre></div>
    </div>
  </section>
</template>
