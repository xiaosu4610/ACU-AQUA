<script setup lang="ts">
/* 模型详情 · 左信息栏 + 右调用示例 双栏
 * 接口对接（与旧版 1:1）：
 * - route params id（vue-router 解码一次，再兜底解码）
 * - /v1/models（useModels.load，session:true，60 秒轮询 + classify 离线兜底渲染）
 * - /v1/models/status（apiJson('/models/status')，20 秒轮询：实时状态 + 会话内健康趋势采样，静默失败）
 * 档案数据（PARAM_TEMPLATES / MODEL_SPECS / MODEL_NOTES / ACU_PROFILES）与旧版逐条平移 */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'
import { apiJson } from '@/composables/useApi'
import { useModels } from '@/composables/useModels'
import { classifyModel, dsMaintenance, platformLabel, typeLabel } from '@/composables/modelMeta'

/* ---- 路由参数：/model/{encodeURIComponent(id)} ---- */
const route = useRoute()
const id = computed(() => {
  const raw = String(route.params.id || '')
  try { return decodeURIComponent(raw) } catch { return raw }
})

/* ---- 数据源：useModels 单例（找不到时 fallback classify，照常渲染） ---- */
const { models, loading, load } = useModels()
let refreshTimer = 0
onMounted(() => {
  load()
  refreshTimer = window.setInterval(() => load(true), 60000)
  liveTimer = window.setInterval(loadLive, 20000)
  loadLive()
})
onUnmounted(() => { if (refreshTimer) window.clearInterval(refreshTimer) })

const row = computed(() => models.value.find(m => m.id === id.value))
const meta = computed(() => row.value ? { platform: row.value.platform, type: row.value.type } : classifyModel(id.value))
/* 按量计费模型（mode=per_token）：无保底、先付后用、缓存价引导 */
const isTide = computed(() => row.value?.mode === 'per_token')
const isTideImage = computed(() => isTide.value && meta.value.type === 'image')
/** 微元 → 元字符串（去尾零：2000→"0.002"） */
const microYuan = (v?: number) => (v == null ? '--' : (v / 1e6).toFixed(6).replace(/0+$/, '').replace(/\.$/, ''))
/** 元/百万tokens 价显示（0.05 → "0.05"） */
const perMYuan = (v?: number) => (v == null ? '--' : v.toFixed(4).replace(/0+$/, '').replace(/\.$/, ''))

/* ---- 状态徽标（旧 statusTag 平移） ---- */
const statusTag = computed(() => {
  if (dsMaintenance(id.value)) return { cls: 'bad', text: '服务暂停 · 维护中' }
  const st = row.value?.status
  if (st === 'exhausted') return { cls: 'bad', text: '额度已耗尽' }
  if (st === 'unavailable') return { cls: 'warn', text: '暂时不可用' }
  return null
})

/* ---- 健康评分（旧 healthTag 平移）：后端按近 100 次调用计算，0-100 ---- */
const health = computed(() => {
  const h = row.value?.health
  if (!h || !h.total) return null
  const score = h.score != null ? h.score | 0 : 0
  let dot: 'ok' | 'warn' | 'bad' = 'bad'
  if (score >= 90) dot = 'ok'
  else if (score >= 70) dot = 'ok'
  else if (score >= 50) dot = 'warn'
  const rate = Math.round(((h.ok || 0) / h.total) * 100)
  const lat = h.avg_latency_ms != null ? (h.avg_latency_ms / 1000).toFixed(1) + 's' : '-'
  return { score, dot, tip: `近 ${h.total} 次调用的健康评分：${score}/100（成功率 ${rate}%，平均延迟 ${lat}）` }
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
function modelSpec(id: string): ModelSpec | null {
  for (const s of MODEL_SPECS) if (s.re.test(id)) return s
  return null
}

/* ---- 热门模型定制描述（旧 MODEL_NOTES 平移） ---- */
interface ModelNote { match: RegExp; name: string; desc: string }
const MODEL_NOTES: ModelNote[] = [
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

/* ---- 官方自营专线（aqua/）官方参数与限制：请求体原样透传（旧 ACU_PROFILES 平移） ---- */
interface AcuProfile { limits: string[]; extraParams?: [string, string, string, string][]; unsupported: string[] }
const ACU_UNSUPPORTED = ["function_call", "functions", "logprobs", "top_logprobs", "n", "logit_bias", "user", "audio"]
const ACU_PROFILES: Record<string, AcuProfile> = {
  'aqua/deepseek-v4-flash': {
    limits: [
      "官方原版满血 DeepSeek-V4-Flash-0731：MoE 总参 284B / 激活 13B（MIT 开源权重）",
      "上下文 1M tokens，单次最大输出 384K tokens",
      "思考模式：支持非思考与思考双模式（默认思考，最高 Think Max 深度档）",
      "官方支持 Tool Calls / JSON Output / 结构化输出 / 对话前缀续写，请求体参数原样透传",
      "AQUA api 专线通道有全局并发保护，高峰期自动排队等待，请勿重复提交"
    ],
    unsupported: ACU_UNSUPPORTED
  },
  'aqua/deepseek-v4-pro': {
    limits: [
      "官方原版满血 DeepSeek-V4-Pro-0813 正式版：旗舰 MoE 总参 1.6T / 激活 49B（MIT 开源权重）",
      "上下文 1M tokens，单次最大输出 384K tokens",
      "思考三档：Non-Think（快速响应）/ Think High / Think Max（最深推理）",
      "官方支持 Tool Calls / JSON Output / 结构化输出 / 对话前缀续写，请求体参数原样透传",
      "AQUA api 专线通道有全局并发保护，高峰期自动排队等待，请勿重复提交"
    ],
    unsupported: ACU_UNSUPPORTED
  },
  'aqua/glm-5.2': {
    limits: [
      "官方原版满血 GLM-5.2：MoE 总参 744B / 激活 40B（MIT 开源权重）",
      "Solid 1M 上下文（IndexShare 架构），单次最大输出 128K tokens",
      "思考力度可调：High（平衡）/ Max（深度）档位，none / minimal 可放弃思考",
      "官方支持 Tool Calls / JSON 结构化输出 / 上下文缓存 / 流式输出，请求体参数原样透传",
      "AQUA api 专线通道有全局并发保护，高峰期自动排队等待，请勿重复提交"
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
      "AQUA api 专线通道有全局并发保护，高峰期自动排队等待，请勿重复提交"
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
      "AQUA api 专线通道有全局并发保护，高峰期自动排队等待，请勿重复提交"
    ],
    extraParams: [
      ["thinking", "object", "enabled", "思维链开关：{\"type\": \"enabled\"}（GLM-5.3-Flash 强制开启）"],
      ["reasoning_effort", "string", "max", "思考力度：low / high / max（默认 max）"]
    ],
    unsupported: ACU_UNSUPPORTED
  }
}

/* ---- 档案合成（旧 modelProfile 平移） ---- */
const profile = computed(() => {
  const mid = id.value
  const m = meta.value
  const t = m.type || 'chat'
  const base = PARAM_TEMPLATES[t] || PARAM_TEMPLATES.chat
  let note: ModelNote | null = null
  for (const n of MODEL_NOTES) if (n.match.test(mid)) { note = n; break }
  const acu = m.platform === 'acu' ? ACU_PROFILES[mid] : undefined
  const desc = (note ? note.name + "。" + note.desc + " " : "") + base.desc
  const limits = base.limits.slice()
  if (acu) {
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

/* ---- 调用示例（旧 modelExample 平移为 cURL；另生成 Python / JS 版本） ---- */
const EX_BASE = 'https://api.ltzy.top/v1'
function exBody(): string {
  const mid = id.value
  const t = meta.value.type
  if (t === 'embedding') return '{"model":"' + mid + '","input":"你好"}'
  if (t === 'rerank') return '{"model":"' + mid + '","query":"你好","documents":["文档A","文档B"]}'
  if (t === 'tts') return '{"model":"' + mid + '","input":"你好，欢迎使用 AQUA api","voice":"default"}'
  if (t === 'moderation') return '{"model":"' + mid + '","input":"待检测文本"}'
  if (t === 'image') return '{"model":"' + mid + '","prompt":"一只可爱的橘猫","size":"1024x1024"}'
  if (t === 'video') return '{"model":"' + mid + '","prompt":"一只小狗在草地上奔跑"}'
  return '{"model":"' + mid + '","messages":[{"role":"user","content":"你好"}],"stream":true}'
}
const exCurl = computed(() => {
  const mid = id.value
  const t = meta.value.type
  if (t === 'asr') {
    return '# 使用 multipart 上传音频文件\ncurl https://api.ltzy.top/v1/audio/transcriptions \\\n  -F "file=@audio.wav" \\\n  -F "model=' + mid + '"'
  }
  if (t === 'ip') {
    return 'curl https://api.ltzy.top/v1/ip_location\n  -H "Content-Type: application/json"\n  -d \'{"ip":""}\''
  }
  return 'curl https://api.ltzy.top/v1/chat/completions \\\n  -H "Content-Type: application/json" \\\n  -H "Authorization: Bearer sk-****" \\\n  -d \'' + exBody() + '\''
})
const exPy = computed(() => {
  const mid = id.value
  const t = meta.value.type
  const head = 'from openai import OpenAI\n\nclient = OpenAI(\n    api_key="sk-你的密钥",\n    base_url="' + EX_BASE + '",\n)\n'
  if (t === 'embedding') {
    return head + 'r = client.embeddings.create(\n    model="' + mid + '",\n    input="你好",\n)\nprint(r.data[0].embedding[:8], "...")'
  }
  if (t === 'image') {
    return head + 'r = client.images.generate(\n    model="' + mid + '",\n    prompt="一只可爱的橘猫",\n    size="1024x1024",\n)\nprint(r.data[0].url)'
  }
  if (t === 'asr') {
    return head + 'r = client.audio.transcriptions.create(\n    model="' + mid + '",\n    file=open("audio.wav", "rb"),\n)\nprint(r.text)'
  }
  if (t === 'tts') {
    return head + 'r = client.audio.speech.create(\n    model="' + mid + '",\n    voice="default",\n    input="你好，欢迎使用 AQUA api",\n)\nr.stream_to_file("speech.mp3")'
  }
  if (t === 'moderation') {
    return head + 'r = client.moderations.create(\n    model="' + mid + '",\n    input="待检测文本",\n)\nprint(r.results[0])'
  }
  if (t === 'vision') {
    return head + 'r = client.chat.completions.create(\n    model="' + mid + '",\n    messages=[{"role": "user", "content": [\n        {"type": "text", "text": "描述这张图"},\n        {"type": "image_url", "image_url": {"url": "https://example.com/cat.jpg"}},\n    ]}],\n)\nprint(r.choices[0].message.content)'
  }
  if (t === 'rerank' || t === 'ip' || t === 'video') {
    const path = t === 'ip' ? '/ip_location' : '/chat/completions'
    const dict = t === 'ip'
      ? '{"ip": ""}'
      : t === 'video'
        ? '{"model": "' + mid + '", "prompt": "一只小狗在草地上奔跑"}'
        : '{"model": "' + mid + '", "query": "你好", "documents": ["文档A", "文档B"]}'
    return '# 非 OpenAI 标准端点：通用 HTTP 调用\nimport requests\n\nr = requests.post(\n    "' + EX_BASE + path + '",\n    headers={"Authorization": "Bearer sk-你的密钥"},\n    json=' + dict + ',\n)\nprint(r.json())'
  }
  return head + 'r = client.chat.completions.create(\n    model="' + mid + '",\n    messages=[{"role": "user", "content": "你好"}],\n)\nprint(r.choices[0].message.content)'
})
const exJs = computed(() => {
  const mid = id.value
  const t = meta.value.type
  const head = 'import OpenAI from "openai";\n\nconst client = new OpenAI({\n  apiKey: "sk-你的密钥",\n  baseURL: "' + EX_BASE + '",\n});\n'
  if (t === 'embedding') {
    return head + 'const r = await client.embeddings.create({\n  model: "' + mid + '",\n  input: "你好",\n});\nconsole.log(r.data[0].embedding.slice(0, 8));'
  }
  if (t === 'image') {
    return head + 'const r = await client.images.generate({\n  model: "' + mid + '",\n  prompt: "一只可爱的橘猫",\n  size: "1024x1024",\n});\nconsole.log(r.data[0].url);'
  }
  if (t === 'asr') {
    return head + 'const r = await client.audio.transcriptions.create({\n  model: "' + mid + '",\n  file: fs.createReadStream("audio.wav"),\n});\nconsole.log(r.text);'
  }
  if (t === 'tts') {
    return head + 'const r = await client.audio.speech.create({\n  model: "' + mid + '",\n  voice: "default",\n  input: "你好，欢迎使用 AQUA api",\n});\nawait r.writeFile("speech.mp3");'
  }
  if (t === 'moderation') {
    return head + 'const r = await client.moderations.create({\n  model: "' + mid + '",\n  input: "待检测文本",\n});\nconsole.log(r.results[0]);'
  }
  if (t === 'vision') {
    return head + 'const r = await client.chat.completions.create({\n  model: "' + mid + '",\n  messages: [{ role: "user", content: [\n    { type: "text", text: "描述这张图" },\n    { type: "image_url", image_url: { url: "https://example.com/cat.jpg" } },\n  ] }],\n});\nconsole.log(r.choices[0].message.content);'
  }
  if (t === 'rerank' || t === 'ip' || t === 'video') {
    const path = t === 'ip' ? '/ip_location' : '/chat/completions'
    return '// 非 OpenAI 标准端点：通用 HTTP 调用\nconst r = await fetch("' + EX_BASE + path + '", {\n  method: "POST",\n  headers: {\n    "Content-Type": "application/json",\n    Authorization: "Bearer sk-你的密钥",\n  },\n  body: JSON.stringify(' + exBody() + '),\n});\nconsole.log(await r.json());'
  }
  return head + 'const r = await client.chat.completions.create({\n  model: "' + mid + '",\n  messages: [{ role: "user", content: "你好" }],\n});\nconsole.log(r.choices[0].message.content);'
})
const exLang = ref<'curl' | 'python' | 'js'>('curl')
const exCode = computed(() => (exLang.value === 'python' ? exPy.value : exLang.value === 'js' ? exJs.value : exCurl.value))

/* ---- 实时状态 + 会话内健康趋势（/v1/models/status，20 秒采样，静默失败） ---- */
type LiveRow = { model: string; samples: number; ok: number; ok_rate: number; status: string; avg_latency_ms?: number; avg_tps?: number; last_ts: number }
const live = ref<LiveRow | null>(null)
const trendPts = ref<{ t: number; rate: number; lat: number }[]>([])
let liveTimer = 0
async function loadLive() {
  try {
    const j = await apiJson<{ data: LiveRow[]; generated_ts: number }>('/models/status')
    const r = (j.data || []).find((x: LiveRow) => x.model === id.value)
    if (r) {
      live.value = r
      const last = trendPts.value[trendPts.value.length - 1]
      if (!last || last.t !== (r.last_ts || j.generated_ts)) {
        trendPts.value.push({ t: r.last_ts || j.generated_ts, rate: Math.round(r.ok_rate * 100), lat: r.avg_latency_ms || 0 })
        if (trendPts.value.length > 12) trendPts.value.shift()
      }
    }
  } catch { /* 静默：下一轮自动重试 */ }
}
function fmtLat(ms?: number): string {
  if (!ms) return '--'
  return ms >= 1000 ? (ms / 1000).toFixed(2) + ' s' : Math.round(ms) + ' ms'
}
function liveDot(): 'ok' | 'warn' | 'bad' | '' {
  if (!live.value) return ''
  if (live.value.status === 'great' || live.value.status === 'ok') return 'ok'
  if (live.value.status === 'degraded') return 'warn'
  return 'bad'
}
function liveText(): string {
  if (!live.value) return '待命中'
  if (live.value.status === 'great') return '状态极佳'
  if (live.value.status === 'ok') return '运行正常'
  if (live.value.status === 'degraded') return '部分异常'
  return '故障'
}

/* ---- 头部价格标签（付费模型三态文案，与旧版 1:1） ---- */
const priceTag = computed(() => {
  const r = row.value
  if (!r?.paid) return null
  if (r.mode === 'per_token' && r.in_price != null && r.per_image == null) {
    return {
      text: '收费 · 按量 ¥' + perMYuan(r.in_price) + '/百万tokens 起' + (isTide.value ? '' : (r.subsidized ? ' · 限时补贴' : '')),
      title: isTide.value
        ? '按量计费：输入/缓存命中/输出分段计价，用多少付多少，详见下方计费说明'
        : '按量计费：输入/缓存命中/输出分段计价，单次设最低消费，详见下方计费说明',
    }
  }
  if (r.price_micro || r.per_image != null) {
    const v = ((r.price_micro ?? r.per_image) / 1_000_000).toFixed(3)
    return {
      text: '收费 · ¥' + v + '/' + (isTideImage.value ? '张' : '次') + ' · 按' + (isTideImage.value ? '张' : '次') + '计费',
      title: isTideImage.value ? '按张计费：n 参数控制张数，详见下方计费说明' : '预充值按次计费，详见下方计费说明',
    }
  }
  return null
})

/* ---- 价格表行（付费模型） ---- */
const priceRows = computed<[string, string][] | null>(() => {
  const r = row.value
  if (!r?.paid) return null
  if (r.mode === 'per_token' && r.in_price != null && r.per_image == null) {
    return [
      ['计费方式', isTide.value ? '按量三段价 · 无保底，用多少付多少' : '按量三段价 · 单次最低消费 ¥' + microYuan(r.floor_micro)],
      ['输入', '¥' + perMYuan(r.in_price) + ' / 百万 tokens'],
      ['缓存命中', '¥' + perMYuan(r.cache_price) + ' / 百万 tokens'],
      ['输出', '¥' + perMYuan(r.out_price) + ' / 百万 tokens'],
    ]
  }
  if (isTideImage.value) {
    return [['计费方式', '按张计费 · n 参数控制张数（1~10 张）'], ['单价', '¥' + microYuan(r.price_micro ?? r.per_image) + ' / 张']]
  }
  return [['计费方式', '按次计费 · 每次成功请求扣一次，与生成长度无关'], ['单价', '¥' + microYuan(r.price_micro ?? r.per_image) + ' / 次']]
})
</script>

<template>
  <div class="wrap">
    <!-- 加载骨架 -->
    <template v-if="loading && !models.length">
      <div class="skeleton" style="min-height: 30px; width: 40%; margin-top: 26px;"></div>
      <div class="detail-grid mt16">
        <div class="card"><div class="skeleton" style="min-height: 16px; width: 55%;"></div><div class="skeleton mt12" style="min-height: 12px; width: 92%;"></div><div class="skeleton mt8" style="min-height: 12px; width: 80%;"></div><div class="skeleton mt8" style="min-height: 12px; width: 86%;"></div></div>
        <div class="card"><div class="skeleton" style="min-height: 200px;"></div></div>
      </div>
    </template>

    <template v-else>
      <div class="page-head">
        <div style="min-width: 0;">
          <router-link to="/models" class="dim back-link"><AqIcon name="arrow-right" :size="13" style="transform: rotate(180deg);" />返回模型中心</router-link>
          <h1 class="mid-clip"><code class="mid-id">{{ id }}</code></h1>
          <div class="sub row wrap" style="gap: 6px; margin-top: 8px;">
            <span class="tag acc">{{ platformLabel(meta.platform) }}</span>
            <span class="tag">{{ profile.typeText }}</span>
            <span v-if="priceTag" class="tag grad" :title="priceTag.title">{{ priceTag.text }}</span>
            <span v-else-if="meta.platform" class="tag ok">免费模型</span>
            <span v-if="statusTag" class="tag" :class="statusTag.cls">{{ statusTag.text }}</span>
            <span v-if="health" class="tag" :title="health.tip"><span class="dot" :class="health.dot"></span>健康 {{ health.score }}</span>
            <span v-if="live" class="tag"><span class="dot" :class="liveDot()"></span>{{ liveText() }}</span>
          </div>
        </div>
        <div class="ops">
          <CopyBtn :text="id" label="复制 ID" />
          <router-link to="/models?view=cap" class="btn sm">能力总览</router-link>
        </div>
      </div>

      <div class="detail-grid fade-up">
        <!-- ================= 左：信息栏 ================= -->
        <div class="col-main">
          <div class="card">
            <b><AqIcon name="bulb" :size="16" />模型能力简介<template v-if="profile.noteName">&nbsp;· {{ profile.noteName }}</template></b>
            <p class="mt12">{{ profile.desc }}</p>
          </div>

          <!-- 规格网格 -->
          <div class="card mt16" v-if="profile.spec">
            <b><AqIcon name="gauge" :size="16" />规格参数</b>
            <div class="spec-grid mt12">
              <div v-if="profile.spec.ctx" class="spec"><span>上下文窗口</span><b>{{ profile.spec.ctx }}</b></div>
              <div v-if="profile.spec.size" class="spec"><span>参数量</span><b>{{ profile.spec.size }}</b></div>
              <div v-if="profile.spec.dims" class="spec"><span>向量维度</span><b>{{ profile.spec.dims }}</b></div>
              <div v-if="profile.spec.released" class="spec"><span>发布日期</span><b>{{ profile.spec.released }}</b></div>
              <div class="spec"><span>上游平台</span><b>{{ platformLabel(meta.platform) }}</b></div>
              <div class="spec"><span>能力类型</span><b>{{ profile.typeText }}</b></div>
            </div>
          </div>

          <!-- 价格表（付费模型） -->
          <div class="card mt16" v-if="priceRows">
            <b><AqIcon name="coin" :size="16" />价格表</b>
            <div class="tbl-wrap mt12">
              <table class="table">
                <tbody>
                  <tr v-for="(p, i) in priceRows" :key="p[0]">
                    <td style="width: 130px; color: var(--txt2);">{{ p[0] }}</td>
                    <td class="num" v-if="i > 0"><b>{{ p[1] }}</b></td>
                    <td v-else>{{ p[1] }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <ul class="mt12 bill-notes">
              <li v-if="row?.mode === 'per_token' && row?.per_image == null">按量计费：输入 / 缓存命中 / 输出按 tokens 分段计价，<b>用多少付多少</b>{{ isTide ? '；重复对话前缀命中缓存价，输入成本大幅更低' : '' }}</li>
              <li v-else-if="isTideImage">按张计费：每次成功请求按 <code>n</code>（张数）扣费，失败自动全额退回</li>
              <li v-else>按次计费：每次<b>成功</b>请求扣一次，与生成长度无关（写一句话和写一千字同价）</li>
              <li v-if="isTide">先付后用：发起请求按预估预扣（输入 + <code>max_tokens</code> 输出上限），完成后<b>多退少补</b>；调小 <code>max_tokens</code> 可降低单次预扣</li>
              <li>预充值制：余额用完自动返回 402 停止服务，<b>绝不透支</b>；<router-link to="/console?view=topup">在线充值即时到账</router-link></li>
              <li>失败不扣费：上游失败 / 网络中断 / 服务异常，预扣金额<b>自动全额退回</b></li>
              <li>调用方式与免费模型完全一致：同一接口、同一密钥，<code>model</code> 填本模型 ID 即可；需注册登录并使用个人密钥</li>
            </ul>
          </div>

          <!-- 支持的请求参数 -->
          <div class="card mt16">
            <b><AqIcon name="list" :size="16" />支持的请求参数</b>
            <div class="tbl-wrap mt12">
              <table class="table">
                <thead><tr><th>参数名</th><th>类型</th><th>默认值</th><th>说明</th></tr></thead>
                <tbody>
                  <tr v-for="p in profile.params" :key="p[0]">
                    <td><code>{{ p[0] }}</code></td>
                    <td>{{ p[1] }}</td>
                    <td>{{ p[2] }}</td>
                    <td>{{ p[3] }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>

          <!-- 不支持的参数 -->
          <div class="card mt16" v-if="profile.unsupported.length">
            <b><AqIcon name="cross" :size="16" />不支持的参数</b>
            <p class="dim mt8" style="font-size: 12.5px;">以下 OpenAI 标准参数在该模型类型下不可用或无意义：</p>
            <div class="row wrap mt8" style="gap: 6px;">
              <span v-for="p in profile.unsupported" :key="p" class="tag bad"><AqIcon name="cross" :size="11" /><code>{{ p }}</code></span>
            </div>
          </div>

          <!-- 限制 / 不支持的功能 -->
          <div class="card mt16">
            <b><AqIcon name="alert" :size="16" />限制 / 注意事项</b>
            <ul class="mt12 bill-notes">
              <li v-for="l in profile.limits" :key="l">{{ l }}</li>
            </ul>
          </div>
        </div>

        <!-- ================= 右：调用示例 + 健康趋势 ================= -->
        <div class="col-side">
          <div class="card">
            <div class="row between">
              <b><AqIcon name="send" :size="16" />调用示例</b>
              <CopyBtn :text="exCode" />
            </div>
            <div class="chips mt12">
              <button class="chip" :class="{ on: exLang === 'curl' }" @click="exLang = 'curl'">cURL</button>
              <button class="chip" :class="{ on: exLang === 'python' }" @click="exLang = 'python'">Python</button>
              <button class="chip" :class="{ on: exLang === 'js' }" @click="exLang = 'js'">JavaScript</button>
            </div>
            <pre class="code mt8 ex-code">{{ exCode }}</pre>
          </div>

          <div class="card mt16">
            <b><AqIcon name="activity" :size="16" />健康状态</b>
            <div class="row mt12" style="gap: 14px;">
              <div class="health-score">
                <b class="num" v-if="health">{{ health.score }}</b>
                <b v-else>--</b>
                <span class="dim">健康分</span>
              </div>
              <div style="min-width: 0; flex: 1;">
                <div class="row" style="gap: 7px; font-size: 12.5px;">
                  <span class="dot" :class="liveDot() || 'warn'"></span>{{ liveText() }}
                  <span v-if="live" class="dim" style="margin-left: auto; font-size: 11px;">近 {{ live.samples }} 次 · 最近活动 {{ live.last_ts ? new Date(live.last_ts * 1000).toLocaleTimeString() : '--' }}</span>
                </div>
                <div class="row wrap mt8" style="gap: 6px;">
                  <span class="tag">时延 {{ live?.avg_latency_ms ? fmtLat(live.avg_latency_ms) : '--' }}</span>
                  <span class="tag">速度 {{ live?.avg_tps ? live.avg_tps.toFixed(1) + ' tok/s' : '--' }}</span>
                  <span class="tag">成功率 {{ live ? (live.ok_rate * 100).toFixed(1) + '%' : '--' }}</span>
                </div>
              </div>
            </div>
            <!-- 会话内趋势采样（每 20 秒一点，成功率%） -->
            <div class="mt12" style="border-top: 1px dashed var(--line); padding-top: 10px;">
              <div class="row between" style="font-size: 11.5px; color: var(--txt2);">
                <span>健康趋势（本页停留期间 · 每 20 秒采样成功率）</span><span class="num">0–100%</span>
              </div>
              <div v-if="trendPts.length" class="trend-bars">
                <span v-for="p in trendPts" :key="p.t" :title="new Date(p.t * 1000).toLocaleTimeString() + ' · ' + p.rate + '%'">
                  <i :style="{ height: Math.max(8, p.rate) + '%' }" :class="p.rate >= 90 ? 'ok' : p.rate >= 50 ? 'warn' : 'bad'"></i>
                </span>
              </div>
              <div v-else class="empty" style="padding: 14px 0;"><b>等待采样</b><div class="dim" style="font-size: 12px;">每 20 秒自动拉取一次该模型实时状态</div></div>
            </div>
            <div v-if="health" class="dim mt8" style="font-size: 11.5px;">{{ health.tip }}</div>
          </div>

          <div class="card">
            <b><AqIcon name="book" :size="16" />相关页面</b>
            <div class="mt12" style="display: grid; gap: 8px;">
              <router-link to="/models" class="btn sm block">模型中心 · 全部模型</router-link>
              <router-link to="/playground" class="btn sm block">在线体验 · 流式对话</router-link>
              <router-link to="/api" class="btn sm block">API 文档 · 端点与错误码</router-link>
            </div>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
/* ---- 双栏布局：桌面 左信息 + 右示例；窄屏单列 ---- */
.detail-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 380px;
  gap: 16px;
  align-items: start;
}
.col-side {
  position: sticky;
  top: 68px;
  display: grid;
  gap: 0;
}
@media (max-width: 1020px) {
  .detail-grid { grid-template-columns: 1fr; }
  .col-side { position: static; }
}
.back-link { display: inline-flex; align-items: center; gap: 5px; font-size: 12.5px; }
.mid-id { font-size: 22px; font-weight: 700; color: var(--txt0); word-break: break-all; }
.page-head { align-items: flex-start; }
.page-head .sub { max-width: none; }

/* ---- 规格网格 ---- */
.spec-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 10px; }
.spec { border: 1px solid var(--line); border-radius: var(--r-md); padding: 10px 13px; background: var(--bg1); }
.spec span { display: block; font-size: 11px; color: var(--txt2); }
.spec b { font-size: 14.5px; color: var(--txt0); word-break: break-all; }

/* ---- 列表 ---- */
.bill-notes { padding-left: 18px; display: grid; gap: 5px; font-size: 13px; color: var(--txt2); }
.bill-notes b { color: var(--txt0); }

/* ---- 调用示例 / 健康趋势 ---- */
.ex-code { min-height: 250px; max-height: 380px; white-space: pre; margin-top: 0; }
.health-score { text-align: center; flex: none; }
.health-score b { display: block; font-size: 34px; font-weight: 800; color: var(--txt0); line-height: 1.1; }
.trend-bars { display: flex; align-items: flex-end; gap: 4px; height: 52px; margin-top: 8px; }
.trend-bars span { flex: 1; height: 100%; display: flex; align-items: flex-end; }
.trend-bars i { display: block; width: 100%; border-radius: 3px 3px 0 0; background: var(--acc-grad); }
.trend-bars i.warn { background: var(--warn); }
.trend-bars i.bad { background: var(--bad); }
</style>
