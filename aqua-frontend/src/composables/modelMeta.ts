/* ============================================================
 * AQUA 模型元数据（自旧版 index.html 平移，2026-08-31 新 ID 体系）
 * 静态平台表 + 类型规则 + 离线兜底清单 + DeepSeek 下线守卫
 * ============================================================ */

/** 离线兜底模型清单：/v1/models 拉取失败时保证首屏可用 */
export const fallbackModels: string[] = [
  // ── 官方自营收费模型（统一 aqua/ 前缀；计费方式由密钥分组决定）──
  'aqua/deepseek-v4-flash', 'aqua/glm-5.3-flash', 'aqua/deepseek-v4-pro', 'aqua/glm-5.2', 'aqua/glm-5.3',
  'aqua/deepseek-v4.1-flash', 'aqua/deepseek-v4-flash-0731', 'aqua/deepseek-v4-pro-0813',
  'aqua/glm-5', 'aqua/glm-5.1',
  'aqua/kimi-k2.5', 'aqua/kimi-k2.6', 'aqua/kimi-k2.7-code',
  'aqua/longcat-2.0', 'aqua/mimo-v2.5-pro',
  'aqua/minimax-m2.5', 'aqua/minimax-m2.7',
  'aqua/qwen3.7-flash', 'aqua/qwen3.7-max', 'aqua/qwen3.8-27b', 'aqua/qwen3.8-flash', 'aqua/qwen3.8-max',
  'aqua/seed-2.1-pro', 'aqua/seed-2.1-turbo',
  'aqua/qwen-image-2.0', 'aqua/wan2.7-image',
  // ── DeepSeek 官方（NVIDIA NIM） ──
  'deepseek-v4-flash-0731', 'deepseek-v4-pro-0813',
  // ── Gitee AI（模力方舟） ──
  'deepseek-prover-v2-7b', 'healthgpt-l14', 'huatuogpt-o1-7b', 'lingshu-32b',
  'glm-4-9b-0414', 'glm-4-9b-chat', 'qwen2-7b-instruct', 'internlm3-8b-instruct',
  'deepseek-r1-distill-qwen-1.5b', 'deepseek-r1-distill-qwen-7b', 'deepseek-r1-distill-qwen-14b',
  'qwen3-0.6b', 'qwen3-4b', 'qwen3-8b',
  'qwen3-embedding-4b', 'qwen3-reranker-0.6b', 'qwen3-reranker-4b', 'qwen3guard-gen-0.6b',
  'glm-asr', 'sensevoice-small', 'spark-tts-0.5b',
  'bge-reranker-v2-m3', 'bce-reranker-base_v1',
  'nonescape-v0', 'security-semantic-filtering', 'nsfw-classifier', 'ip-location',
  // ── Google / Meta / MiniMax / Mistral / Moonshot / Nvidia / OpenAI / Poolside（NVIDIA NIM） ──
  'diffusiongemma-26b-a4b-it', 'gemma-4-31b-it',
  'llama-3.2-11b-vision-instruct', 'llama-3.2-90b-vision-instruct',
  'llama-guard-4-12b', 'muse-glimmer-30b',
  'minimax-m3', 'mistral-nemotron', 'kimi-k3',
  'ising-calibration-1.5-31b',
  'llama-3.1-nemoguard-8b-content-safety', 'llama-3.1-nemoguard-8b-topic-control',
  'llama-3.1-nemotron-safety-guard-8b-v3',
  'nemotron-3-nano-30b-a3b', 'nemotron-3-nano-omni-30b-a3b-reasoning',
  'nemotron-3-super-120b-a12b', 'nemotron-3-ultra-550b-a55b',
  'nemotron-3.5-content-safety', 'nemotron-3.5-lightning-30b-a3b',
  'riva-translate-4b-instruct-v1.1', 'riva-translate-4b-instruct-v2',
  'gpt-oss-120b', 'gpt-oss-20b',
  'laguna-xs-2.1',
  // ── SiliconFlow（硅基流动） ──
  'bge-large-en-v1.5', 'bge-large-zh-v1.5', 'bge-m3',
  'paddleocr-vl-1.5', 'glm-z1-9b-0414', 'hunyuan-mt-7b',
  // ── 讯飞星火（Spark，免费） ──
  'spark-lite',
  // ── 智谱 GLM（免费，含 CogView 绘图 / CogVideo 视频） ──
  'glm-4.7-flash', 'glm-4-flash-250414', 'glm-z1-flash', 'glm-4-flash',
  'glm-4.6v-flash', 'glm-4v-flash', 'glm-4.1v-thinking-flash',
  'cogview-3-flash', 'cogvideox-flash',
]

/** 静态平台表：表外模型兜底 nvidia */
export const PLATFORM: Record<string, string> = {
  'deepseek-v4-flash': 'acu',
  'aqua/deepseek-v4-flash': 'acu',
  'aqua/glm-5.3-flash': 'acu',
  'aqua/deepseek-v4-pro': 'acu',
  'aqua/glm-5.2': 'acu',
  'aqua/glm-5.3': 'acu',
  // Gitee AI
  'deepseek-prover-v2-7b': 'gitee', 'healthgpt-l14': 'gitee', 'huatuogpt-o1-7b': 'gitee', 'lingshu-32b': 'gitee',
  'glm-4-9b-0414': 'gitee', 'glm-4-9b-chat': 'gitee', 'qwen2-7b-instruct': 'gitee', 'internlm3-8b-instruct': 'gitee',
  'deepseek-r1-distill-qwen-1.5b': 'gitee', 'deepseek-r1-distill-qwen-7b': 'gitee', 'deepseek-r1-distill-qwen-14b': 'gitee',
  'qwen3-0.6b': 'gitee', 'qwen3-4b': 'gitee', 'qwen3-8b': 'gitee',
  'qwen3-embedding-4b': 'gitee', 'qwen3-reranker-0.6b': 'gitee', 'qwen3-reranker-4b': 'gitee', 'qwen3guard-gen-0.6b': 'gitee',
  'glm-asr': 'gitee', 'sensevoice-small': 'gitee', 'spark-tts-0.5b': 'gitee',
  'bge-reranker-v2-m3': 'gitee', 'bce-reranker-base_v1': 'gitee',
  'nonescape-v0': 'gitee', 'security-semantic-filtering': 'gitee', 'nsfw-classifier': 'gitee', 'ip-location': 'gitee',
  // SiliconFlow
  'bge-large-en-v1.5': 'siliconflow', 'bge-large-zh-v1.5': 'siliconflow', 'bge-m3': 'siliconflow',
  'paddleocr-vl-1.5': 'siliconflow', 'glm-z1-9b-0414': 'siliconflow', 'hunyuan-mt-7b': 'siliconflow',
  // 讯飞
  'spark-lite': 'spark',
  // 智谱
  'glm-4-flash': 'zhipu', 'glm-4-flash-250414': 'zhipu', 'glm-4.1v-thinking-flash': 'zhipu', 'glm-4.6v-flash': 'zhipu',
  'glm-4.7-flash': 'zhipu', 'glm-4v-flash': 'zhipu', 'glm-z1-flash': 'zhipu',
  'cogview-3-flash': 'zhipu', 'cogvideox-flash': 'zhipu',
  // 其余（DeepSeek/Google/Meta/MiniMax/Mistral/Moonshot/Nvidia/OpenAI/Poolside 系）→ 兜底 nvidia
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
  return ({ nvidia: 'Nvidia', gitee: 'Gitee', siliconflow: 'SiliconFlow', zhipu: '智谱', spark: '讯飞星火', acu: '官方自营' } as Record<string, string>)[p] || p
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
