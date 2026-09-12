/* 工具/游戏共用对话能力：对话模型池 + 非流式 chat 入口
 * 自旧版 apiChat / toolChatModel 平移（gomoku/tictactoe 内联版本与之等价） */
import { computed, onMounted, ref, watch } from 'vue'
import { apiJson } from './useApi'
import { useModels } from './useModels'
import { dsMaintenance } from './modelMeta'

const { models, load } = useModels()

/** 工具页用：可选对话模型下拉 + 默认选中（挂载时自动拉取模型列表） */
export function useChatModels() {
  onMounted(() => { load() })
  const chatOpts = computed(() =>
    models.value.filter(m => !dsMaintenance(m.id) && (!m.type || m.type === 'chat')).map(m => m.id))
  const llmModel = ref('')
  watch(chatOpts, v => { if (!llmModel.value && v.length) llmModel.value = v[0] }, { immediate: true })
  return { chatOpts, llmModel }
}

function chatPool(): string[] {
  return models.value.filter(m => !dsMaintenance(m.id) && (!m.type || m.type === 'chat')).map(m => m.id)
}

const APICHAT_FALLBACKS = ['deepseek-v4-flash', 'qwen3-8b', 'qwen3-4b', 'gpt-oss-120b', 'glm-4-9b-0414', 'gpt-oss-20b']

/* 站内工具通道：2026-09-05 起全面强制登录，网关侧已校验会话/用户密钥。
 * 此处带用户默认密钥（登录用户在控制台创建），无登录时后端返回 401。 */
export async function apiChat(
  messages: { role: string; content: string }[],
  opts: { model?: string; temperature?: number; max_tokens?: number; noFallback?: boolean } = {},
): Promise<string> {
  function userKey(): string {
    try { return localStorage.getItem('aqua_default_key') || '' } catch { return '' }
  }
  async function attempt(model: string, isRetry: boolean): Promise<string> {
    try {
      const j: any = await apiJson('/chat/completions', {
        method: 'POST',
        key: userKey(),
        body: {
          model,
          messages,
          stream: false,
          temperature: opts.temperature != null ? opts.temperature : 0.3,
          max_tokens: opts.max_tokens || 1024,
        },
      })
      const c = (j && j.choices && j.choices[0] && j.choices[0].message) || {}
      // content 为空时回退 reasoning_content（部分推理模型把输出全放在思考字段）
      const out = c.content || c.reasoning_content || ''
      if (!out) throw new Error('模型未返回内容，请换一个模型重试')
      return out
    } catch (err: any) {
      if (err && err.status === 401) throw err
      if (isRetry || opts.noFallback) throw err
      let fb: string | null = null
      for (const f of APICHAT_FALLBACKS) if (f !== model && chatPool().includes(f)) { fb = f; break }
      if (!fb) throw err
      return attempt(fb, true)
    }
  }

  /* 站内工具通道：登录用户的 DeepSeek 官方额度通道，失败静默回退用户密钥公开池 */
  try {
    const j: any = await apiJson('/tools/chat', {
      method: 'POST',
      session: true,
      body: {
        model: 'deepseek-chat',
        messages,
        stream: false,
        temperature: opts.temperature != null ? opts.temperature : 0.3,
        max_tokens: opts.max_tokens || 1024,
      },
    })
    const c = (j && j.choices && j.choices[0] && j.choices[0].message) || {}
    const out = c.content || c.reasoning_content || ''
    if (out) return out
  } catch { /* 未登录/通道关闭/超预算/限流 → 回退公开池 */ }

  const pool = chatPool()
  const model = opts.model || (pool.includes('deepseek-v4-flash') ? 'deepseek-v4-flash' : (pool[0] || 'deepseek-v4-flash'))
  return attempt(model, false)
}
