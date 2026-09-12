<script setup lang="ts">
/* 财务管理中心：余额 + 消费统计（今日/7日/累计）+ 按模型消费 Top + 消费流水 + 充值记录 */
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { errText } from '@/composables/useApi'
import { fetchFinance, isLoggedIn, loadMe, me, type FinanceData } from '@/composables/useAuth'
import AqIcon from '@/components/AqIcon.vue'

const router = useRouter()
const data = ref<FinanceData | null>(null)
const loading = ref(true)
const errMsg = ref('')

onMounted(async () => {
  if (!isLoggedIn()) {
    router.push('/login?next=/finance')
    return
  }
  if (!me.value) await loadMe().catch(() => {})
  await load()
})

async function load() {
  loading.value = true
  errMsg.value = ''
  try {
    data.value = await fetchFinance()
  } catch (e) { errMsg.value = errText(e) }
  loading.value = false
}

/** 微元 → 元字符串（去尾零） */
function yuan(micro?: number): string {
  if (micro == null) return '--'
  return '¥' + (micro / 1e6).toFixed(6).replace(/0+$/, '').replace(/\.$/, '')
}
function fmtTime(ts?: number): string {
  if (!ts) return '--'
  return new Date(ts * 1000).toLocaleString()
}
function topupStatus(s: string): string {
  if (s === 'paid') return '已到账'
  if (s === 'pending') return '待支付'
  if (s === 'expired' || s === 'closed') return '已关闭'
  return s
}
</script>

<template>
  <section class="route-page">
    <div class="fin-head">
      <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="6" width="20" height="14" rx="2"/><path d="M2 10h20M6 15h4"/></svg></span>财务管理中心</h1>
      <p>余额、消费统计、按模型账单与充值记录的统一入口。先付后用、失败全额退回。</p>
    </div>

    <div v-if="loading" class="fin-empty">加载中…</div>
    <div v-else-if="errMsg" class="fin-empty">
      {{ errMsg }}
      <button class="mini-btn" style="margin-left:10px" @click="load">重试</button>
    </div>

    <template v-else-if="data">
      <!-- 余额 + 汇总卡 -->
      <div class="fin-cards">
        <div class="fin-card balance">
          <span class="fc-label">当前余额</span>
          <b class="fc-balance">{{ yuan(data.balance_micro) }}</b>
          <router-link class="btn tool-run fc-topup" to="/console?view=topup"><AqIcon name="spark" :size="14" /> 立即充值</router-link>
        </div>
        <div class="fin-card"><span class="fc-label">今日消费</span><b class="fc-num">{{ yuan(data.spend_today) }}</b><span class="fc-sub">按服务器自然日</span></div>
        <div class="fin-card"><span class="fc-label">近 7 日消费</span><b class="fc-num">{{ yuan(data.spend_week) }}</b><span class="fc-sub">成功扣费合计</span></div>
        <div class="fin-card"><span class="fc-label">累计消费</span><b class="fc-num">{{ yuan(data.spend_total) }}</b><span class="fc-sub">全部历史合计</span></div>
      </div>

      <div class="fin-grid">
        <!-- 按模型消费 Top -->
        <div class="fin-sec">
          <h3>近 30 天按模型消费 <i>Top 10</i></h3>
          <div v-if="!data.by_model.length" class="fin-empty small">暂无消费记录</div>
          <table v-else class="fin-table">
            <thead><tr><th>模型</th><th>消费金额</th><th>调用次数</th></tr></thead>
            <tbody>
              <tr v-for="m in data.by_model" :key="m.model">
                <td><code>{{ m.model }}</code></td>
                <td class="num">{{ yuan(m.amount_micro) }}</td>
                <td class="num">{{ m.calls }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- 最近消费流水 -->
        <div class="fin-sec">
          <h3>最近消费流水 <i>20 条</i></h3>
          <div v-if="!data.recent.length" class="fin-empty small">暂无消费记录</div>
          <table v-else class="fin-table">
            <thead><tr><th>模型</th><th>金额</th><th>结果</th><th>时间</th></tr></thead>
            <tbody>
              <tr v-for="(f, i) in data.recent" :key="i">
                <td><code>{{ f.model }}</code></td>
                <td class="num">{{ yuan(f.amount_micro) }}</td>
                <td><span class="st-mini" :class="f.ok ? 'ok' : 'bad'">{{ f.ok ? '成功' : '已退回' }}</span></td>
                <td class="dim">{{ fmtTime(f.ts) }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- 最近充值记录 -->
        <div class="fin-sec wide">
          <h3>最近充值记录 <i>10 条</i></h3>
          <div v-if="!data.topups.length" class="fin-empty small">暂无充值记录，<router-link to="/console?view=topup">去充值</router-link></div>
          <table v-else class="fin-table">
            <thead><tr><th>金额</th><th>状态</th><th>渠道</th><th>创建时间</th><th>到账时间</th></tr></thead>
            <tbody>
              <tr v-for="(t, i) in data.topups" :key="i">
                <td class="num strong">{{ yuan(t.amount_micro) }}</td>
                <td><span class="st-mini" :class="t.status === 'paid' ? 'ok' : 'idle'">{{ topupStatus(t.status) }}</span></td>
                <td>{{ t.channel || '--' }}</td>
                <td class="dim">{{ fmtTime(t.created_ts) }}</td>
                <td class="dim">{{ t.paid_ts ? fmtTime(t.paid_ts) : '--' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.fin-head { margin-bottom: 18px; }
.fin-head h1 { display: flex; align-items: center; gap: 10px; font-size: 24px; margin: 0 0 6px; }
.fin-head .ic svg { width: 26px; height: 26px; color: var(--aqua, #38bdf8); }
.fin-head p { color: var(--muted, #8a94a6); font-size: 13.5px; margin: 0; }
.fin-cards { display: grid; grid-template-columns: 1.4fr 1fr 1fr 1fr; gap: 12px; margin-bottom: 16px; }
.fin-card { background: linear-gradient(160deg, var(--card), var(--card2, var(--card))); border: 1px solid var(--border); border-radius: 14px; padding: 16px 18px; display: flex; flex-direction: column; gap: 4px; }
.fin-card.balance { border-color: rgba(56,189,248,.4); }
.fc-label { font-size: 12px; color: var(--muted, #8a94a6); font-weight: 600; }
.fc-balance { font-size: 30px; font-weight: 800; color: var(--aqua, #38bdf8); font-variant-numeric: tabular-nums; }
.fc-num { font-size: 21px; font-weight: 800; font-variant-numeric: tabular-nums; }
.fc-sub { font-size: 11px; color: var(--muted, #8a94a6); }
.fc-topup { margin-top: 8px; align-self: flex-start; text-decoration: none; display: inline-flex; align-items: center; gap: 6px; font-size: 13px; padding: 7px 14px; }
.fin-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.fin-sec { background: var(--card); border: 1px solid var(--border); border-radius: 14px; padding: 16px 18px; min-width: 0; }
.fin-sec.wide { grid-column: 1 / -1; }
.fin-sec h3 { margin: 0 0 12px; font-size: 15px; }
.fin-sec h3 i { font-style: normal; font-size: 12px; color: var(--muted, #8a94a6); font-weight: 400; margin-left: 6px; }
.fin-sec code { font-size: 12px; color: var(--accent, #38bdf8); word-break: break-all; }
.fin-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.fin-table th, .fin-table td { padding: 8px 10px; text-align: left; border-bottom: 1px solid rgba(148,163,184,.14); }
.fin-table th { font-size: 11.5px; color: var(--muted, #8a94a6); font-weight: 700; }
.fin-table tbody tr:last-child td { border-bottom: 0; }
.fin-table .num { font-variant-numeric: tabular-nums; }
.fin-table .strong { font-weight: 700; }
.fin-table .dim { color: var(--muted, #8a94a6); font-size: 12px; }
.st-mini { font-size: 11px; font-weight: 700; border-radius: 999px; padding: 2px 8px; }
.st-mini.ok { color: #16a34a; background: rgba(34,197,94,.13); }
.st-mini.bad { color: #dc2626; background: rgba(239,68,68,.12); }
.st-mini.idle { color: var(--muted, #8a94a6); background: rgba(148,163,184,.14); }
.fin-empty { text-align: center; color: var(--muted, #8a94a6); padding: 40px 0; font-size: 14px; }
.fin-empty.small { padding: 18px 0; font-size: 13px; }
@media (max-width: 900px) {
  .fin-cards { grid-template-columns: 1fr 1fr; }
  .fin-grid { grid-template-columns: 1fr; }
}
</style>
