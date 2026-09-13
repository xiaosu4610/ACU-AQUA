<script setup lang="ts">
/* 状态大屏：顶部 KPI 行 + 线路健康表（/v1/models/status）+ 模型健康热区（/v1/models health.score）
 * 网关信息与近 1h 聚合来自 /v1/status；30 秒轮询（interval 在 onUnmounted 清理，请求可中断） */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { apiJson, fmt } from '@/composables/useApi'
import { useModels } from '@/composables/useModels'
import AqIcon from '@/components/AqIcon.vue'

interface StatusModel { model: string; success_rate: number; calls_1h: number; avg_latency_ms: number }
interface LiveRow { model: string; samples: number; ok: number; ok_rate: number; status: string; avg_latency_ms?: number; last_ts: number }

const { models, loading: modelsLoading, load: loadModels } = useModels()

const meta = ref<{ version: string; uptime: string; window: string } | null>(null)
const kpi = ref({ calls: '--', rate: '--', lat: '--' })
const firstLoading = ref(true)
const lineRows = ref<LiveRow[]>([])
const lineMsg = ref('加载中…')
const updatedAt = ref('')

/* 在线模型数：站点全部模型（/v1/models 全量，仅排除 auto 聚合项） */
const onlineCount = computed(() => models.value.filter(m => m.id !== 'auto').length)
const onlineShow = computed(() => (modelsLoading.value && !models.value.length ? '--' : String(onlineCount.value)))

/* 模型健康热区：health.score 着色（后端近 100 次调用评分，0-100） */
const heat = computed(() => models.value
  .filter(m => m.id !== 'auto' && m.health && m.health.total)
  .map(m => {
    const h = m.health!
    const score = h.score != null ? h.score | 0 : 0
    const rate = Math.round(((h.ok || 0) / (h.total || 1)) * 100)
    const lat = h.avg_latency_ms != null ? (h.avg_latency_ms / 1000).toFixed(1) + 's' : '-'
    return { id: m.id, score, rate, lat, color: scoreColor(score), tip: `近 ${h.total} 次调用的健康评分：${score}/100（成功率 ${rate}%，平均延迟 ${lat}）` }
  })
  .sort((a, b) => b.score - a.score))
function scoreColor(s: number): string {
  if (s >= 90) return 'var(--ok)'
  if (s >= 70) return 'var(--acc)'
  if (s >= 50) return 'var(--warn)'
  return 'var(--bad)'
}

/* 线路状态 → 状态点/文案（great 极佳 / ok 正常 / degraded 部分异常 / down 故障） */
function lineStatus(s: string): { dot: string; text: string } {
  if (s === 'great' || s === 'ok') return { dot: 'ok', text: s === 'great' ? '状态极佳' : '运行正常' }
  if (s === 'degraded') return { dot: 'warn', text: '部分异常' }
  return { dot: 'bad', text: '故障' }
}
const lineRowsView = computed(() => lineRows.value.slice().sort((a, b) => b.samples - a.samples))

/* /v1/status：网关信息 + 近 1h 健康度 → 聚合出 KPI（总调用/加权成功率/加权平均延迟） */
async function loadStatus(ac: AbortController) {
  try {
    const j = await apiJson<any>('/status', { signal: ac.signal })
    if (ac.signal.aborted) return
    const up = j.uptime_sec || 0
    const d = Math.floor(up / 86400)
    const h = Math.floor((up % 86400) / 3600)
    const mnt = Math.floor((up % 3600) / 60)
    meta.value = { version: j.version || '--', uptime: d + ' 天 ' + h + ' 时 ' + mnt + ' 分', window: j.window || '1h' }
    const list: StatusModel[] = j.models || []
    if (!list.length) {
      kpi.value = { calls: '--', rate: '--', lat: '--' }
    } else {
      let tc = 0, wRate = 0, wLat = 0
      list.forEach(m => {
        const c = m.calls_1h || 0
        tc += c
        wRate += (m.success_rate || 0) * c
        wLat += (m.avg_latency_ms || 0) * c
      })
      kpi.value = {
        calls: tc ? fmt(tc) : '0',
        rate: tc ? (wRate / tc).toFixed(1) + '%' : '--',
        lat: tc ? (wLat / tc / 1000).toFixed(2) + 's' : '--',
      }
    }
  } catch {
    if (ac.signal.aborted) return
    meta.value = null
    kpi.value = { calls: '--', rate: '--', lat: '--' }
  }
}

/* /v1/models/status：最近 200 次请求推断的线路级实时状态（20-30s 自动刷新） */
async function loadLive(ac: AbortController) {
  try {
    const j = await apiJson<{ data: LiveRow[]; generated_ts: number }>('/models/status', { signal: ac.signal })
    if (ac.signal.aborted) return
    lineRows.value = j.data || []
    lineMsg.value = lineRows.value.length ? '' : '暂无实时采样数据——去 Playground 或竞技场产生第一条记录'
  } catch {
    if (ac.signal.aborted) return
    lineRows.value = []
    lineMsg.value = '实时数据加载失败，稍后自动重试'
  }
}

async function refresh() {
  if (aborter) aborter.abort()
  const ac = new AbortController()
  aborter = ac
  updatedAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
  await Promise.all([loadStatus(ac), loadLive(ac)])
  firstLoading.value = false
}

let timer: ReturnType<typeof setInterval> | null = null
let aborter: AbortController | null = null

onMounted(() => {
  loadModels()
  refresh()
  timer = setInterval(() => { loadModels(true); refresh() }, 30000)
})

onUnmounted(() => {
  if (timer) { clearInterval(timer); timer = null }
  if (aborter) { aborter.abort(); aborter = null }
})
</script>

<template>
  <div class="wrap">
    <div class="page-head fade-up">
      <div>
        <h1><AqIcon name="activity" />状态大屏</h1>
        <div class="sub">全站运行数据实时透明化——所有数字直接来自网关数据库，不修饰、不筛选。数据每 30 秒自动刷新。</div>
      </div>
      <div class="ops">
        <button class="btn sm" :disabled="firstLoading" @click="refresh"><AqIcon name="refresh" :size="14" />立即刷新</button>
      </div>
    </div>

    <!-- 网关信息条 -->
    <div class="dim mb12" style="font-size: 13px;">
      <template v-if="meta">网关版本 <b>{{ meta.version }}</b> · 已连续运行 <b>{{ meta.uptime }}</b> · 健康窗口 {{ meta.window }}</template>
      <template v-else>网关版本 <b>--</b> · 已运行 <b>--</b></template>
      <template v-if="updatedAt"> · 更新于 <b>{{ updatedAt }}</b></template>
    </div>

    <!-- 顶部 KPI 行 -->
    <div v-if="firstLoading" class="kpis kpis-xl">
      <div v-for="i in 4" :key="i" class="kpi">
        <span class="skeleton" style="width: 72px; display: inline-block;"></span>
        <div class="skeleton" style="width: 110px; height: 28px; margin-top: 8px;"></div>
      </div>
    </div>
    <div v-else class="kpis kpis-xl">
      <div class="kpi"><span>近 1h 总调用</span><b>{{ kpi.calls }}</b></div>
      <div class="kpi"><span>近 1h 成功率</span><b>{{ kpi.rate }}</b></div>
      <div class="kpi"><span>近 1h 平均延迟</span><b>{{ kpi.lat }}</b></div>
      <div class="kpi"><span>在线模型数</span><b>{{ onlineShow }}</b></div>
    </div>

    <!-- 线路健康表 -->
    <div class="grp-head mt24">
      <AqIcon name="server" :size="15" />线路健康
      <span class="grp-n">最近 200 次请求采样 · 实时推断</span>
    </div>
    <div v-if="lineRowsView.length" class="tbl-wrap">
      <table class="table">
        <thead>
          <tr><th>线路</th><th>状态</th><th class="num">调用数</th><th class="num">近 1h 成功率</th></tr>
        </thead>
        <tbody>
          <tr v-for="r in lineRowsView" :key="r.model">
            <td class="mono" :title="r.model">{{ r.model }}</td>
            <td>
              <span class="row" style="gap: 7px; white-space: nowrap;">
                <span class="dot" :class="lineStatus(r.status).dot"></span>{{ lineStatus(r.status).text }}
              </span>
            </td>
            <td class="num">{{ fmt(r.samples) }}</td>
            <td class="num" :style="{ color: scoreColor(Math.round(r.ok_rate * 100)) }">{{ (r.ok_rate * 100).toFixed(1) }}%</td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="empty">
      <div class="big"><AqIcon name="activity" :size="32" /></div>
      <b>{{ lineMsg }}</b>
    </div>

    <!-- 模型健康热区 -->
    <div class="grp-head mt24">
      <AqIcon name="gauge" :size="15" />模型健康热区
      <span class="grp-n">后端近 100 次调用评分（0-100）</span>
    </div>
    <div v-if="heat.length" class="grid3">
      <div v-for="h in heat" :key="h.id" class="card heat-card" :title="h.tip">
        <div class="row between">
          <span class="mono model-id" :title="h.id">{{ h.id }}</span>
          <b class="score" :style="{ color: h.color }">{{ h.score }}</b>
        </div>
        <div class="dim mt8" style="font-size: 12px;">成功率 {{ h.rate }}% · 延迟 {{ h.lat }}</div>
      </div>
    </div>
    <div v-else class="empty">
      <div class="big"><AqIcon name="gauge" :size="32" /></div>
      <b>{{ modelsLoading ? '模型健康数据加载中…' : '暂无模型健康评分' }}</b>
    </div>
  </div>
</template>

<style scoped>
.kpis-xl .kpi b { font-size: 32px; }
.grp-head { display: flex; align-items: center; gap: 8px; font-size: 15px; font-weight: 700; color: var(--txt0); }
.grp-head .grp-n { font-size: 12px; font-weight: 600; color: var(--txt3); }
.heat-card { padding: 14px 16px; }
.heat-card .score { font-size: 26px; font-variant-numeric: tabular-nums; }
.model-id { max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
