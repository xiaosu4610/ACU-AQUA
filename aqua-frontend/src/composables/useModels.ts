/* 模型列表共享状态：/v1/models 拉取 + 离线兜底（首屏即有模型可选） */
import { ref, watch } from 'vue'
import { sessionToken } from './useAuth'
import { apiJson } from './useApi'
import { classifyModel } from './modelMeta'

export interface ModelRow {
  id: string
  platform: string
  type: string
  status?: string
  status_msg?: string
  /** 收费模型：价格（微元）+ 活动价标记 */
  paid?: boolean
  price_view?: string
  base_price_micro?: number
  price_micro?: number
  promo_active?: boolean
  /** 活动价截止时刻（unix 秒，后端 /v1/models 提供） */
  promo_ends_at?: number
  /** 按量计费切换时刻（unix 秒，后端 /v1/models 提供） */
  billing_switch_at?: number
  /** 按量计费：计费模式（per_call=按次 / per_token=按量三段价） */
  mode?: 'per_call' | 'per_token'
  /** 可用计费分组列表（同模型双分组并存时为 [per_call, per_token]） */
  groups?: string[]
  /** 三段价（元/百万 tokens）：输入 / 缓存命中 / 输出 */
  in_price?: number
  cache_price?: number
  out_price?: number
  /** 按张计费单价（微元/张，仅图片模型；存在即按张计费） */
  per_image?: number
  /** 按量计费：单次保底（微元） */
  floor_micro?: number
  /** 限时补贴档标记（v4-flash） */
  subsidized?: boolean
  /** 常态扣费倍率（20260923 展示价分离）：>0 时主价应显示 base_*（官方原价），并标该倍率角标 */
  charge_rate?: number
  /** 展示分组标记（'external' = 外模专线，前端单独成板块） */
  section?: string
  /** 2 号折扣钱包专用（20260924）：该模型只能用折扣钱包余额调用，主钱包会被拒绝 */
  wallet2_only?: boolean
}

/** offline fallback：仅 auto（离线不展示任何模型清单，避免运营数据进代码） */
const OFFLINE: ModelRow[] = [{ id: 'auto', ...classifyModel('auto') }]

const models = ref<ModelRow[]>([])
const loading = ref(false)
const error = ref('')
/** 最近一次成功拉取时间戳（0 = 从未成功） */
const loadedAt = ref(0)
let generation = 0
watch(sessionToken, () => {
  generation++
  models.value = []
  loadedAt.value = 0
  loading.value = false
  error.value = ''
  void useModels().load(true)
}, { flush: 'pre' })

export function useModels() {
  async function load(force = false) {
    if (loading.value) return
    if (models.value.length && !force) return
    const requestGeneration = generation
    loading.value = true
    error.value = ''
    try {
      // 带登录态：后端按用户价格组下发 VIP 拿货价（vip 价目）+ base_* 原价对照字段；匿名/未登录得 normal 视角
      const j = await apiJson<{ data: any[] }>('/models', { session: true })
      if (requestGeneration !== generation) return
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
          // 特殊计费类型（image/video…）以后端下发的 type 字段为准（配置化，前端零硬编码），仅离线兜底时走本地推断
          type: (raw as any).type || classifyModel(id).type,
          status: raw.status || undefined,
          status_msg: raw.status_msg || undefined,
          paid: raw.paid === true,
          price_micro: raw.price_micro,
          promo_active: raw.promo_active === true,
          promo_ends_at: raw.promo_ends_at,
          billing_switch_at: raw.billing_switch_at,
          mode: raw.mode === 'per_token' ? 'per_token' : 'per_call',
          groups: Array.isArray(raw.groups) ? raw.groups : undefined,
          in_price: raw.in_price,
          cache_price: raw.cache_price,
          out_price: raw.out_price,
          per_image: raw.per_image,
          floor_micro: raw.floor_micro,
          // VIP 拿货价对照：base_* 原价字段透传（模型卡片「VIP 拿货价」标签与划线原价依赖这些字段）
          price_view: raw.price_view,
          base_price_micro: raw.base_price_micro,
          base_in_price: raw.base_in_price,
          base_cache_price: raw.base_cache_price,
          base_out_price: raw.base_out_price,
          base_floor_micro: raw.base_floor_micro,
          base_per_image: raw.base_per_image,
          subsidized: raw.subsidized === true,
          // 展示价与实收价分离（20260923）：charge_rate 与 base_* 需透传到详情页，
          // 否则列表页显示官方原价、详情页显示实收价 → 同一模型两个价
          charge_rate: raw.charge_rate,
          section: raw.section,
          // 2 号折扣钱包专用标记（20260924）：卡片需提示"用折扣钱包余额调用"
          wallet2_only: raw.wallet2_only === true,
        }
      })
      loadedAt.value = Date.now()
    } catch (e: any) {
      if (requestGeneration !== generation) return
      error.value = e?.message || String(e)
      if (!models.value.length) models.value = OFFLINE
    } finally {
      if (requestGeneration === generation) loading.value = false
    }
  }
  return { models, loading, error, loadedAt, load }
}
