<script setup lang="ts">
/* 状态大屏（流量大屏）：/v1/stats（今日卡片 + 24h 趋势 + 全模型流量表 + TOP10）+ /status（1h 健康度），30s 自动刷新 */
import { onMounted, onUnmounted, ref } from 'vue'
import { apiJson } from '@/composables/useApi'
import { fmt } from '@/composables/useApi'
import AqIcon from '@/components/AqIcon.vue'

interface StatRow { model: string; cls?: string; width: number; delay?: string; val: string }
interface FlowRow { model: string; calls: string; rate: string; lat: string; tokens: string; cls: string }
interface HourBar { hour: number; label: string; calls: number; rate: number | null; height: number }

const meta = ref<{ version: string; uptime: string; window: string } | null>(null)
const cards = ref({ calls: '--', rate: '--', lat: '--', users: '--' })
const topRows = ref<StatRow[]>([])
const topMsg = ref('加载中…')
const healthRows = ref<StatRow[]>([])
const healthMsg = ref('加载中…')
const flowRows = ref<FlowRow[]>([])
const flowMsg = ref('')
const hours = ref<HourBar[]>([])
const trendMsg = ref('')
const updatedAt = ref('')

let timer: ReturnType<typeof setInterval> | null = null
let aborter: AbortController | null = null

/* /v1/stats：今日卡片 + 24h 趋势 + 全模型流量表 + TOP10 */
async function loadStats(ac: AbortController) {
  try {
    const j = await apiJson<any>('/stats', { signal: ac.signal })
    if (ac.signal.aborted) return
    cards.value = {
      calls: j.today ? fmt(j.today.total_calls) : '--',
      rate: j.today ? j.today.ok_rate + '%' : '--',
      lat: j.today ? (j.today.avg_latency_ms / 1000).toFixed(2) + 's' : '--',
      users: j.today ? fmt(j.today.active_keys) : '--',
    }
    /* 24h 趋势柱状图 */
    const hourly: any[] = j.hourly || []
    if (!hourly.length) {
      hours.value = []
      trendMsg.value = '暂无趋势数据'
    } else {
      const maxc = Math.max(...hourly.map((h) => h.calls || 0), 1)
      hours.value = hourly.map((h) => {
        const d = new Date((h.hour || 0) * 1000)
        const hh = String(d.getHours()).padStart(2, '0')
        return {
          hour: h.hour,
          label: hh + ':00',
          calls: h.calls || 0,
          rate: h.ok_rate,
          height: Math.max(2, Math.round(((h.calls || 0) * 100) / maxc)),
        }
      })
      trendMsg.value = ''
    }
    /* 全模型 24h 流量表 */
    const ms: any[] = j.model_stats || []
    if (!ms.length) {
      flowRows.value = []
      flowMsg.value = '近 24 小时还没有调用记录'
    } else {
      flowRows.value = ms.map((m) => ({
        model: m.model,
        calls: fmt(m.calls_24h),
        rate: m.success_rate + '%',
        lat: (m.avg_latency_ms / 1000).toFixed(2) + 's',
        tokens: fmt(m.total_tokens || 0),
        cls: m.success_rate >= 90 ? '' : m.success_rate >= 50 ? 'warn' : 'bad',
      }))
      flowMsg.value = ''
    }
    const top = j.top_models || []
    if (!top.length) {
      topRows.value = []
      topMsg.value = '今天还没有调用记录'
    } else {
      const maxc = top[0].calls || 1
      topRows.value = top.map((m: any) => ({
        model: m.model,
        width: Math.max(4, Math.round((m.calls * 100) / maxc)),
        val: fmt(m.calls) + ' 次',
      }))
    }
    updatedAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
  } catch (e) {
    if (ac.signal.aborted) return
    cards.value = { calls: '--', rate: '--', lat: '--', users: '--' }
  }
}

/* /status：网关信息 + 1h 健康度 */
async function loadStatus(ac: AbortController) {
  try {
    const j = await apiJson<any>('/status', { signal: ac.signal })
    if (ac.signal.aborted) return
    const up = j.uptime_sec || 0
    const d = Math.floor(up / 86400)
    const h = Math.floor((up % 86400) / 3600)
    const mnt = Math.floor((up % 3600) / 60)
    meta.value = { version: j.version || '--', uptime: d + ' 天 ' + h + ' 时 ' + mnt + ' 分', window: j.window || '1h' }
    const list = j.models || []
    if (!list.length) {
      healthRows.value = []
      healthMsg.value = '近 1 小时还没有调用数据——去 Playground 或竞技场产生第一条记录'
    } else {
      healthRows.value = list.map((m: any) => ({
        model: m.model,
        cls: m.success_rate >= 90 ? '' : m.success_rate >= 50 ? 'warn' : 'bad',
        width: Math.max(3, Math.round(m.success_rate)),
        delay: (m.avg_latency_ms / 1000).toFixed(2),
        val: m.success_rate + '% · ' + m.calls_1h + '次 · ' + (m.avg_latency_ms / 1000).toFixed(1) + 's',
      }))
    }
  } catch {
    if (ac.signal.aborted) return
    healthRows.value = []
    healthMsg.value = '健康数据加载失败，稍后自动重试'
  }
}

async function loadDashboard() {
  if (aborter) aborter.abort()
  const ac = new AbortController()
  aborter = ac
  await Promise.all([loadStats(ac), loadStatus(ac)])
}

onMounted(() => {
  loadDashboard()
  timer = setInterval(loadDashboard, 30000)
})

onUnmounted(() => {
  if (timer) { clearInterval(timer); timer = null }
  if (aborter) { aborter.abort(); aborter = null }
})
</script>

<template>
  <section class="route-page">
    <div class="dash-wrap">
      <div class="dash-head">
        <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg></span>状态大屏</h1>
        <p>全站运行数据实时透明化——所有数字直接来自网关数据库，不修饰、不筛选。数据每 30 秒自动刷新。</p>
      </div>
      <div class="dash-sub" id="st-meta">
        <template v-if="meta">网关版本 <b>{{ meta.version }}</b> · 已连续运行 <b>{{ meta.uptime }}</b> · 健康窗口 {{ meta.window }}</template>
        <template v-else>网关版本 <b>--</b> · 已运行 <b>--</b></template>
        <template v-if="updatedAt"> · 更新于 <b>{{ updatedAt }}</b></template>
      </div>
      <div class="dash-cards">
        <div class="dash-card"><b id="st2-calls">{{ cards.calls }}</b><span>今日全站调用</span></div>
        <div class="dash-card"><b id="st2-rate">{{ cards.rate }}</b><span>今日成功率</span></div>
        <div class="dash-card"><b id="st2-lat">{{ cards.lat }}</b><span>今日平均延迟</span></div>
        <div class="dash-card"><b id="st2-users">{{ cards.users }}</b><span>今日活跃密钥</span></div>
      </div>

      <div class="dash-sec">
        <b><AqIcon name="trend" :size="16" /> 近 24 小时调用趋势 <span style="font-size:11.5px;color:var(--muted);font-weight:400;">（每小时调用量 · 悬停看成功率）</span></b>
        <div v-if="trendMsg" class="dash-empty">{{ trendMsg }}</div>
        <div v-else id="st2-trend" class="flow-chart" role="img" aria-label="近 24 小时每小时调用量柱状图">
          <div v-for="h in hours" :key="h.hour" class="flow-col" :title="h.label + ' · ' + h.calls + ' 次' + (h.rate != null ? ' · 成功率 ' + h.rate + '%' : '')">
            <span class="flow-num" v-if="h.calls > 0">{{ h.calls >= 10000 ? (h.calls / 1000).toFixed(1) + 'k' : h.calls }}</span>
            <span v-else class="flow-num">&nbsp;</span>
            <span class="flow-barbox"><span class="flow-bar" :style="{ height: h.height + '%' }"></span></span>
            <span class="flow-hour">{{ h.label }}</span>
          </div>
        </div>
      </div>

      <div class="dash-sec">
        <b><AqIcon name="activity" :size="16" /> 全模型流量表 <span style="font-size:11.5px;color:var(--muted);font-weight:400;">（近 24 小时 · 按调用量排序）</span></b>
        <div id="st2-flow" class="tbl-scroll">
          <div v-if="!flowRows.length" class="dash-empty">{{ flowMsg }}</div>
          <table v-else class="flow-table">
            <thead>
              <tr><th>模型</th><th class="num">调用量</th><th class="num">成功率</th><th class="num">平均延迟</th><th class="num">Tokens</th></tr>
            </thead>
            <tbody>
              <tr v-for="(m, i) in flowRows" :key="i">
                <td class="mono" :title="m.model">{{ m.model }}</td>
                <td class="num">{{ m.calls }}</td>
                <td class="num" :class="m.cls">{{ m.rate }}</td>
                <td class="num">{{ m.lat }}</td>
                <td class="num">{{ m.tokens }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div class="dash-sec">
        <b><AqIcon name="trend" :size="16" /> 今日热门模型 TOP 10 <span style="font-size:11.5px;color:var(--muted);font-weight:400;">（按调用量）</span></b>
        <div id="st2-top">
          <div v-if="!topRows.length" class="dash-empty">{{ topMsg }}</div>
          <div v-for="(m, i) in topRows" :key="i" class="stat-row">
            <span class="nm" :title="m.model">{{ m.model }}</span>
            <span class="trk"><span class="fil" :style="{ width: m.width + '%' }"></span></span>
            <span class="val">{{ m.val }}</span>
          </div>
        </div>
      </div>

      <div class="dash-sec">
        <b><AqIcon name="activity" :size="16" /> 全模型近 1 小时健康度 <span style="font-size:11.5px;color:var(--muted);font-weight:400;">（成功率条 · 悬停看延迟）</span></b>
        <div id="st2-health">
          <div v-if="!healthRows.length" class="dash-empty">{{ healthMsg }}</div>
          <div v-for="(m, i) in healthRows" :key="i" class="stat-row">
            <span class="nm" :title="m.model">{{ m.model }}</span>
            <span class="trk"><span class="fil" :class="m.cls" :style="{ width: m.width + '%' }"></span></span>
            <span class="val" :title="'平均延迟 ' + m.delay + 's'">{{ m.val }}</span>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
