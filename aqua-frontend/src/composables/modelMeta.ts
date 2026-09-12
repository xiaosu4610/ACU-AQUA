/* ============================================================
 * AQUA 模型元数据（通用版：零运营数据硬编码）
 * 平台前缀规则 + 类型推断（仅按模型命名规律兜底）+ DeepSeek 下线守卫
 * 特殊计费模型类型（image/video…）与在售清单一律由后端 /v1/models
 * 的 type 字段与实时数据下发，本文件不保存任何站点专属模型清单/价格
 * ============================================================ */

/** 根据模型 ID 命名规律推断能力类型（离线兜底用，不做任何站点运营假设） */
export function inferType(id: string): string {
  const l = id.toLowerCase()
  if (l.includes('cogview') || l.includes('image')) return 'image'
  if (l.includes('cogvideo') || l.includes('video')) return 'video'
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
  if (id.startsWith('aqua/') || id.startsWith('tide/')) return { platform: 'acu', type: inferType(id) || 'chat' }
  return { platform: 'nvidia', type: inferType(id) || 'chat' }
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

/* ---- DeepSeek 免费线下线守卫（与网关 CHANNEL_MAINTENANCE 同刻生效） ---- */
export const DS_SUNSET_MS = Date.parse('2026-09-03T20:10:00+08:00')
export function dsRetired(): boolean { return Date.now() >= DS_SUNSET_MS }
export function dsModel(model: string): boolean { return model.toLowerCase() === 'deepseek-v4-flash' }
export function dsMaintenance(model: string): boolean { return dsModel(model) && dsRetired() }
