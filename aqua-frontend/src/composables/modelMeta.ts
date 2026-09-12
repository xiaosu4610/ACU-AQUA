/* ============================================================
 * AQUA 模型元数据（2026-09-12 全站对齐：免费线仅 Nvidia NIM，
 * 收费线统一 aqua/ 前缀——按次 2 款 + 按量 21 款含文生图）
 * 静态平台表 + 类型规则 + 离线兜底清单 + DeepSeek 下线守卫
 * ============================================================ */

/** 离线兜底模型清单：/v1/models 拉取失败时保证首屏可用 */
export const fallbackModels: string[] = [
  // ── 官方自营收费模型（统一 aqua/ 前缀；按次/按量由密钥计费分组决定）──
  'aqua/deepseek-v4-flash', 'aqua/deepseek-v4-pro',
  'aqua/deepseek-flash', 'aqua/deepseek-v4-flash-0731', 'aqua/deepseek-v4-pro-0813',
  'aqua/glm-5.1', 'aqua/glm-5.2', 'aqua/glm-5.3', 'aqua/glm-5.3-flash',
  'aqua/kimi-k2.6', 'aqua/kimi-k2.7-code',
  'aqua/longcat-2.0', 'aqua/mimo-v2.5-pro', 'aqua/minimax-m2.7',
  'aqua/qwen3.7-flash', 'aqua/qwen3.7-max', 'aqua/qwen3.8-27b', 'aqua/qwen3.8-flash', 'aqua/qwen3.8-max',
  'aqua/seed-2.1-pro', 'aqua/seed-2.1-turbo',
  'aqua/qwen-image-2.0', 'aqua/wan2.7-image',
  // ── Nvidia NIM（免费）──
  'deepseek-v4-flash-0731', 'deepseek-v4-pro-0813',
  'diffusiongemma-26b-a4b-it', 'gemma-4-31b-it', 'gpt-oss-20b', 'ising-calibration-1.5-31b',
  'kimi-k3', 'laguna-xs-2.1',
  'llama-3.1-nemoguard-8b-content-safety', 'llama-3.1-nemoguard-8b-topic-control',
  'llama-3.1-nemotron-safety-guard-8b-v3',
  'llama-3.2-11b-vision-instruct', 'llama-3.2-90b-vision-instruct', 'llama-guard-4-12b',
  'mistral-nemotron', 'muse-glimmer-30b',
  'nemotron-3-nano-omni-30b-a3b-reasoning', 'nemotron-3-super-120b-a12b', 'nemotron-3-ultra-550b-a55b',
  'nemotron-3.5-content-safety', 'nemotron-3.5-lightning-30b-a3b',
  'riva-translate-4b-instruct-v1.1', 'riva-translate-4b-instruct-v2',
]

/** 静态平台表：表外模型兜底 nvidia */
export const PLATFORM: Record<string, string> = {
  // 历史裸 ID（福利通道前的自营按次模型）兜底为官方自营
  'deepseek-v4-flash': 'acu',
}

const TYPES: Record<string, string> = {}
for (const id of fallbackModels) {
  const l = id
  if (['bge-large-en-v1.5', 'bge-large-zh-v1.5', 'bge-m3', 'qwen3-embedding-4b'].includes(id)) TYPES[id] = 'embedding'
  else if (l.includes('reranker') || l.includes('rerank')) TYPES[id] = 'rerank'
  else if (l.includes('cogview') || l.includes('image')) TYPES[id] = 'image'
  else if (l.includes('cogvideo')) TYPES[id] = 'video'
  else if (/glm-asr|sensevoice|teleasr|qwen3-asr/.test(l)) TYPES[id] = 'asr'
  else if (l.includes('tts')) TYPES[id] = 'tts'
  else if (/v-[a-z]/.test(l)) TYPES[id] = 'vision'
  else if (/guard|moderat|nonescape|security|nsfw|reward|nemoguard/.test(l)) TYPES[id] = 'moderation'
  else TYPES[id] = 'chat'
}

/** 根据模型 ID 推断能力类型（用于不在静态清单中的动态模型） */
export function inferType(id: string): string {
  const l = id.toLowerCase()
  if (l.includes('cogview') || l.includes('image')) return 'image'
  if (l.includes('cogvideo')) return 'video'
  if (l.includes('ip-location')) return 'ip'
  if (l.includes('rerank') || /reranker|bce-rerank/.test(l)) return 'rerank'
  if (/bge-large|bge-m3|arctic-embed|embedqa|embed-|embedcode/.test(l) && !l.includes('rerank')) return 'embedding'
  if (/guard|moderat|nonescape|security|nsfw|reward|nemoguard/.test(l) && !l.includes('guardian')) return 'moderation'
  if (/asr|sensevoice|teleasr|speechasr|voice/.test(l)) return 'asr'
  if (/melotts|spark-tts|tts/.test(l)) return 'tts'
  if (/vision|kosmos|fuyu|paligemma|neva|vila|llama-3\.2-1[19]b/.test(l)) return 'vision'
  return 'chat'
}

export function classifyModel(id: string): { platform: string; type: string } {
  // 官方自营收费模型：统一 aqua/ 前缀（按次/按量由密钥分组决定）；旧 tide/ 前缀兼容
  if (id.startsWith('aqua/') || id.startsWith('tide/')) return { platform: 'acu', type: TYPES[id] || inferType(id) || 'chat' }
  return { platform: PLATFORM[id] || 'nvidia', type: TYPES[id] || inferType(id) || 'chat' }
}

export function platformLabel(p: string): string {
  return ({ nvidia: 'Nvidia', acu: '官方自营' } as Record<string, string>)[p] || p
}

export function typeLabel(t: string): string {
  return ({ chat: '对话', vision: '视觉', embedding: '向量', rerank: '重排', asr: '语音识别',
    tts: '语音合成', moderation: '风控', image: '文生图', video: '文生视频', ip: 'IP 定位' } as Record<string, string>)[t] || t
}

/** 对话模型不显示类型标签（精简） */
export function hideTag(type: string): boolean { return type === 'chat' }

/* ---- DeepSeek 官方自营通道下线守卫（与网关 CHANNEL_MAINTENANCE 同刻生效） ---- */
export const DS_SUNSET_MS = Date.parse('2026-09-03T20:10:00+08:00')
export function dsRetired(): boolean { return Date.now() >= DS_SUNSET_MS }
export function dsModel(model: string): boolean { return model.toLowerCase() === 'deepseek-v4-flash' }
export function dsMaintenance(model: string): boolean { return dsModel(model) && dsRetired() }
