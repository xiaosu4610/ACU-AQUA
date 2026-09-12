/* 模型列表共享状态：/v1/models 拉取 + 离线兜底（首屏即有模型可选） */
import { ref } from 'vue'
import { apiJson } from './useApi'
import { classifyModel, fallbackModels } from './modelMeta'

export interface ModelRow {
  id: string
  platform: string
  type: string
  health?: { score?: number; total?: number; ok?: number; avg_latency_ms?: number } | null
  status?: string
  status_msg?: string
  /** 收费模型：价格（微元）+ 活动价标记 */
  paid?: boolean
  price_micro?: number
  promo_active?: boolean
  /** 活动价截止时刻（unix 秒，后端 /v1/models 提供） */
  promo_ends_at?: number
  /** 按量计费切换时刻（unix 秒，后端 /v1/models 提供） */
  billing_switch_at?: number
  /** 按量计费：计费模式（per_call=按次 / per_token=按量三段价） */
  mode?: 'per_call' | 'per_token'
  /** 三段价（元/百万 tokens）：输入 / 缓存命中 / 输出 */
  in_price?: number
  cache_price?: number
  out_price?: number
  /** 按量计费：单次保底（微元） */
  floor_micro?: number
  /** 限时补贴档标记（v4-flash） */
  subsidized?: boolean
}

/** offline fallback：auto 置顶 + 静态清单 */
const OFFLINE: ModelRow[] = ['auto', ...fallbackModels].map(id => ({ id, ...classifyModel(id) }))

const models = ref<ModelRow[]>([])
const loading = ref(false)
const error = ref('')
/** 最近一次成功拉取时间戳（0 = 从未成功） */
const loadedAt = ref(0)

export function useModels() {
  async function load(force = false) {
    if (loading.value) return
    if (models.value.length && !force) return
    loading.value = true
    error.value = ''
    try {
      const j = await apiJson<{ data: any[] }>('/models')
      const all: any[] = j?.data || []
      let ids: string[] = all.map((m: any) => (typeof m === 'string' ? m : m.id)).filter(Boolean)
      ids = [...new Set(ids)]
      if (!ids.includes('auto')) ids.unshift('auto')
      const byId = new Map(all.filter((m: any) => m && m.id).map((m: any) => [m.id, m]))
      models.value = ids.map(id => {
        const raw = byId.get(id) || {}
        return {
          id,
          ...classifyModel(id),
          health: raw.health || null,
          status: raw.status || undefined,
          status_msg: raw.status_msg || undefined,
          paid: raw.paid === true,
          price_micro: raw.price_micro,
          promo_active: raw.promo_active === true,
          promo_ends_at: raw.promo_ends_at,
          billing_switch_at: raw.billing_switch_at,
          mode: raw.mode === 'per_token' ? 'per_token' : 'per_call',
          in_price: raw.in_price,
          cache_price: raw.cache_price,
          out_price: raw.out_price,
          floor_micro: raw.floor_micro,
          subsidized: raw.subsidized === true,
        }
      })
      loadedAt.value = Date.now()
    } catch (e: any) {
      error.value = e?.message || String(e)
      if (!models.value.length) models.value = OFFLINE
    } finally {
      loading.value = false
    }
  }
  return { models, loading, error, loadedAt, load }
}
