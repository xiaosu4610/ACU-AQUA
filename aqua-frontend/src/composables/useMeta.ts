/* 站点元信息：/v1/meta 全站共享单例（DB 外置配置，改完即时生效） */
import { ref } from 'vue'
import { apiJson } from './useApi'

export interface SiteMeta {
  name: string
  domain: string
  docs_url: string
  qq_group: string
  qq_group_url: string
  qq_group2: string
  qq_group_url2: string
  rate_promo: string
  rate_normal: string
  rate_promo_vip: string
  announcement: string
  announcement_enabled: boolean
  ad_image: string
  ad_link: string
  ad_label: string
}

const meta = ref<SiteMeta | null>(null)
const loadedAt = ref(0)

export function useMeta() {
  async function load(force = false) {
    if (meta.value && !force) return
    if (loadedAt.value && Date.now() - loadedAt.value < 60_000 && !force) return
    try {
      meta.value = await apiJson<SiteMeta>('/meta')
      loadedAt.value = Date.now()
    } catch { /* meta 拉取失败静默：全部字段有兜底展示 */ }
  }
  return { meta, loadMeta: load }
}
