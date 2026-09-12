/* 流式对话（SSE）：统一解析 OpenAI 兼容流，content 与 reasoning_content 双通道 */
import { GATEWAY } from './useApi'

export interface ChatMessage { role: 'system' | 'user' | 'assistant'; content: string }

export interface StreamOpts {
  model: string
  messages: ChatMessage[]
  key?: string
  signal?: AbortSignal
  temperature?: number
  /** 每收到一段增量文本回调 */
  onDelta: (text: string) => void
}

/** 发起流式对话，返回完整文本 */
export async function streamChat(o: StreamOpts): Promise<string> {
  const body: Record<string, any> = { model: o.model, messages: o.messages, stream: true }
  if (o.temperature != null) body.temperature = o.temperature
  // 密钥优先级：显式传入 > 用户默认密钥（控制台保存）；都没有则不带鉴权头（由后端 401 提示登录）
  let k = o.key || ''
  if (!k) { try { k = localStorage.getItem('aqua_default_key') || '' } catch { /* 忽略 */ } }
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (k) headers['Authorization'] = 'Bearer ' + k
  const res = await fetch(GATEWAY + '/chat/completions', {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
    signal: o.signal,
  })
  if (!res.ok || !res.body) {
    let msg = `请求失败（HTTP ${res.status}）`
    try { const j = await res.json(); if (j?.error?.message) msg = String(j.error.message) } catch { /* 忽略 */ }
    throw new Error(msg)
  }
  const reader = res.body.getReader()
  const dec = new TextDecoder()
  let buf = ''
  let full = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += dec.decode(value, { stream: true })
    const lines = buf.split('\n')
    buf = lines.pop() || ''
    for (const line of lines) {
      const s = line.trim()
      if (!s.startsWith('data:')) continue
      const data = s.slice(5).trim()
      if (data === '[DONE]') return full
      try {
        const j = JSON.parse(data)
        const d = j?.choices?.[0]?.delta
        // content 为空时回退 reasoning_content（部分推理模型把输出全放在思考字段）
        const piece = d && (d.content || d.reasoning_content || '')
        if (piece) { full += piece; o.onDelta(piece) }
      } catch { /* 半包/心跳，跳过 */ }
    }
  }
  return full
}

/** 树洞用：剥离推理模型思维链（未闭合时不显示） */
export function stripThink(s: string): string {
  const i = s.lastIndexOf('</think>')
  if (i === -1) return s.startsWith('<think>') ? '' : s
  return s.slice(i + 8).replace(/^\s+/, '')
}
