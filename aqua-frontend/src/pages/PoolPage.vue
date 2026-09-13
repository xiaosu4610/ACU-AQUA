<script setup lang="ts">
// 众筹池账本页：acu/ 公共算力池的驾驶舱 + 充值 + 四大榜单 + 透明流水
// 铁律：本页零上游调用——只打本网关 /v1/pool/* 与 /v1/pay/* 接口
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AqIcon from '@/components/AqIcon.vue'
import { apiJson, errText } from '@/composables/useApi'
import { isLoggedIn } from '@/composables/useAuth'

const status = ref<any>(null)
const flows = ref<any[]>([])
const loading = ref(true)

async function loadStatus() {
  try { status.value = await apiJson<any>('/pool/status') } catch { /* 忽略 */ }
}
async function loadFlows() {
  try { const j = await apiJson<any>('/pool/flows?limit=80'); flows.value = j.items || [] } catch { /* 忽略 */ }
}

/* ===== 榜单 ===== */
const rankTab = ref<'charge' | 'usage' | 'honor' | 'net'>('charge')
const rankRange = ref<'week' | 'all'>('all')
const rankItems = ref<any[]>([])
const heroes = ref<any[]>([])
const elders = ref<any[]>([])
const rankLoading = ref(false)

async function loadRanks() {
  rankLoading.value = true
  try {
    if (rankTab.value === 'honor') {
      const j = await apiJson<any>('/pool/ranks?type=honor')
      heroes.value = j.heroes || []; elders.value = j.elders || []; rankItems.value = []
    } else {
      const j = await apiJson<any>(`/pool/ranks?type=${rankTab.value}&range=${rankRange.value}`)
      rankItems.value = j.items || []; heroes.value = []; elders.value = []
    }
  } catch { /* 忽略 */ }
  rankLoading.value = false
}
function setTab(t: 'charge' | 'usage' | 'honor' | 'net') {
  rankTab.value = t
  if (t === 'usage') rankRange.value = 'week' // 用量榜仅周榜
  loadRanks()
}
function setRange(r: 'week' | 'all') { rankRange.value = r; loadRanks() }

/* ===== 充值（复用站内支付：product=pool 进公共池） ===== */
const topupOpen = ref(false)
const topupAmt = ref(10)
const topupChannel = ref<'alipay' | 'wxpay'>('alipay')
const paying = ref(false)
const payMsg = ref('')
const payOk = ref(false)
const payingOrder = ref<{ out_trade_no: string } | null>(null)
let pollTimer: number | null = null
const AMTS = [5, 10, 30, 100]

const topupMicro = computed(() => Math.round((Number(topupAmt.value) || 0) * 1_000_000))
function yuan(v?: number): string {
  if (v == null) return '--'
  return (v / 1e6).toFixed(2).replace(/\.00$/, '').replace(/(\.\d)0$/, '$1')
}

async function createTopup() {
  if (!isLoggedIn()) { payMsg.value = '请先登录再充值'; return }
  if (topupMicro.value < 5_000_000) { payMsg.value = '众筹池充值单笔最低 ¥5'; return }
  paying.value = true; payMsg.value = ''
  try {
    const j = await apiJson<any>('/pay/create', {
      method: 'POST', session: true,
      body: { amount_micro: topupMicro.value, channel: topupChannel.value, product: 'pool' },
    })
    payingOrder.value = { out_trade_no: j.out_trade_no }
    window.open(j.pay_url, '_blank')
    startPolling()
  } catch (e) { payMsg.value = errText(e) }
  paying.value = false
}
function startPolling() {
  stopPolling()
  pollTimer = window.setInterval(async () => {
    if (!payingOrder.value) { stopPolling(); return }
    try {
      const j = await apiJson<any>(`/pay/status?out_trade_no=${payingOrder.value.out_trade_no}`, { session: true })
      if (j.status === 'paid') {
        payOk.value = true
        payMsg.value = `充值成功：¥${yuan(j.amount_micro)} 已注入众筹池（当前池子 ¥${yuan(j.pool_balance_micro)}），感谢扩充公共算力！`
        stopPolling(); payingOrder.value = null
        loadStatus(); loadFlows(); loadRanks()
      }
    } catch { /* 轮询失败忽略 */ }
  }, 3000)
}
function stopPolling() { if (pollTimer != null) { clearInterval(pollTimer); pollTimer = null } }
async function manualCheck() {
  if (!payingOrder.value) return
  try {
    const j = await apiJson<any>(`/pay/status?out_trade_no=${payingOrder.value.out_trade_no}`, { session: true })
    if (j.status === 'paid') {
      payOk.value = true
      payMsg.value = `充值成功：¥${yuan(j.amount_micro)} 已注入众筹池（当前池子 ¥${yuan(j.pool_balance_micro)}）`
      stopPolling(); payingOrder.value = null
      loadStatus(); loadFlows(); loadRanks()
    } else payMsg.value = '还未查询到支付结果，完成支付后稍等几秒'
  } catch (e) { payMsg.value = errText(e) }
}
onUnmounted(stopPolling)

/* ===== 展示辅助 ===== */
const alive = computed(() => !!status.value && status.value.balance_micro > 0)
const tokensOf = (micro: number) => Math.floor(micro / 1_000_000 * 10_000_000) // ¥1 ≈ 1000 万输入 tokens（v4f 五折口径）
function fmtTs(t: number): string {
  const d = new Date((t || 0) * 1000)
  const p = (x: number) => (x < 10 ? '0' : '') + x
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
const typeLabel: Record<string, string> = { charge: '充值', seed: '官方注入', consume: '扣费', adjust: '调整' }

onMounted(async () => {
  await Promise.all([loadStatus(), loadFlows(), loadRanks()])
  loading.value = false
})
</script>

<template>
  <section class="route-page">
    <div class="pool-head">
      <h1><span class="ic"><AqIcon name="coin" :size="22" /></span>众筹公共算力池</h1>
      <p>acu/ 前缀模型（deepseek-v4-flash / glm-5.3-flash）按官方原价 <b>五折</b> 从公共池扣费——人人可调、无需充值、个人余额分文不动。池子余额由大家共同充值扩充，见底即暂停，充值即复活。每一笔充值与扣费全部公开可查。</p>
    </div>

    <!-- 池子驾驶舱 -->
    <div class="dash-card pool-main" :class="{ empty: status && !alive }">
      <div class="pool-state">
        <span class="dot" :class="alive ? 'on' : 'off'"></span>
        {{ status ? (alive ? '供血中' : '已熔断 · 等待充值复活') : '加载中…' }}
      </div>
      <div class="pool-balance"><small>¥</small>{{ status ? yuan(status.balance_micro) : '--' }}</div>
      <div class="pool-sub">当前池子余额 ≈ 可供 {{ status ? tokensOf(status.balance_micro).toLocaleString() : '--' }} 万输入 tokens（v4f 五折口径）</div>
      <div class="pool-stats">
        <div><b>{{ status ? yuan(status.charged_micro) : '--' }}</b><span>累计充值</span></div>
        <div><b>{{ status ? yuan(status.used_micro) : '--' }}</b><span>累计消耗</span></div>
        <div><b>{{ status ? yuan(status.today_used_micro) : '--' }}</b><span>今日消耗</span></div>
        <div><b>{{ status ? status.consumers : '--' }}</b><span>共同使用者</span></div>
      </div>
    </div>

    <!-- 充值 -->
    <div class="dash-card pool-topup">
      <h2><AqIcon name="plus" :size="15" /> 扩充池子</h2>
      <p class="topup-note">充值即注入公共池，由所有人共用消耗，<b>不可退、不可转个人余额</b>；单笔最低 ¥5。救场者（池子归零后第一笔充值）将登上荣誉墙。</p>
      <div class="amt-row">
        <button v-for="a in AMTS" :key="a" type="button" class="amt" :class="{ on: topupAmt === a }" @click="topupAmt = a">¥{{ a }}</button>
        <input v-model.number="topupAmt" type="number" min="5" max="1000" placeholder="自定义" />
      </div>
      <div class="ch-row">
        <button type="button" class="ch" :class="{ on: topupChannel === 'alipay' }" @click="topupChannel = 'alipay'">支付宝</button>
        <button type="button" class="ch" :class="{ on: topupChannel === 'wxpay' }" @click="topupChannel = 'wxpay'">微信</button>
        <button class="btn topup-go" :disabled="paying || !isLoggedIn()" @click="createTopup">
          {{ paying ? '下单中…' : isLoggedIn() ? `注入 ¥${topupAmt || 0}` : '请先登录' }}
        </button>
      </div>
      <p v-if="payingOrder" class="pay-wait">
        已打开支付页，完成支付后本页自动确认 <button class="mini-btn" @click="manualCheck">我已支付，立即检查</button>
      </p>
      <p v-if="payMsg" class="pay-msg" :class="{ ok: payOk }">{{ payMsg }}</p>
    </div>

    <!-- 四大榜单 -->
    <div class="dash-card pool-ranks">
      <h2><AqIcon name="trophy" :size="15" /> 榜单与荣誉</h2>
      <div class="rank-tabs">
        <button v-for="t in [['charge','充值榜'],['usage','用量榜'],['honor','荣誉墙'],['net','净贡献榜']]" :key="t[0]"
          type="button" class="rtab" :class="{ on: rankTab === t[0] }" @click="setTab(t[0] as any)">{{ t[1] }}</button>
        <span class="rspacer"></span>
        <button v-if="rankTab !== 'usage'" type="button" class="rtab sm" :class="{ on: rankRange === 'week' }" @click="setRange('week')">本周</button>
        <button v-if="rankTab !== 'usage'" type="button" class="rtab sm" :class="{ on: rankRange === 'all' }" @click="setRange('all')">总榜</button>
      </div>
      <div v-if="rankLoading" class="rank-empty">加载中…</div>
      <template v-else-if="rankTab === 'honor'">
        <div class="honor-sec">
          <h3>救场英雄（池子归零后第一笔复活充值）</h3>
          <div v-if="!heroes.length" class="rank-empty">暂无救场记录——池子还没熔断过，大家都在续命</div>
          <div v-for="(h, i) in heroes" :key="i" class="rank-row hero">
            <span class="rk">{{ i + 1 }}</span>
            <span class="nm">{{ h.user }}</span>
            <span class="badge-coin">第 {{ h.saves }} 次救场</span>
            <span class="amt-c">¥{{ yuan(h.amount_micro) }}</span>
            <span class="tm">{{ fmtTs(h.ts) }}</span>
          </div>
        </div>
        <div class="honor-sec">
          <h3>开服元老（最早 10 位众筹充值用户）</h3>
          <div v-if="!elders.length" class="rank-empty">虚位以待——第一位充值者将永久留名</div>
          <div v-for="e in elders" :key="e.rank" class="rank-row">
            <span class="rk">{{ e.rank }}</span>
            <span class="nm">{{ e.user }}</span>
            <span class="badge-coin elder">元老</span>
            <span class="amt-c">¥{{ yuan(e.amount_micro) }}</span>
          </div>
        </div>
      </template>
      <template v-else>
        <div v-if="!rankItems.length" class="rank-empty">虚位以待——{{ rankTab === 'charge' ? '第一笔充值将载入史册' : rankTab === 'usage' ? '本周还没有众筹模型调用' : '充值大于消耗即可上榜' }}</div>
        <div v-for="e in rankItems" :key="e.rank" class="rank-row">
          <span class="rk" :class="{ top: e.rank <= 3 }">{{ e.rank }}</span>
          <span class="nm">{{ e.user }}</span>
          <span v-if="e.note" class="badge-coin">{{ e.note }}</span>
          <span v-if="rankTab === 'charge' && e.extra" class="pct">{{ e.extra.toFixed(1) }}%</span>
          <span v-if="rankTab === 'usage'" class="pct">{{ e.calls }} 次</span>
          <span class="amt-c">¥{{ yuan(e.amount_micro) }}</span>
        </div>
      </template>
    </div>

    <!-- 透明账本 -->
    <div class="dash-card pool-ledger">
      <h2><AqIcon name="list" :size="15" /> 透明账本（最近 80 笔）</h2>
      <div v-if="!flows.length && !loading" class="rank-empty">暂无流水——池子从第一笔充值开始书写历史</div>
      <div v-for="f in flows" :key="f.id" class="flow-row" :class="f.type">
        <span class="ft">{{ typeLabel[f.type] || f.type }}</span>
        <span v-if="f.revival" class="badge-coin hero-mini">救场</span>
        <span class="fnm">{{ f.user }}</span>
        <span class="famt" :class="{ neg: f.amount_micro < 0 }">{{ f.amount_micro > 0 ? '+' : '' }}{{ yuan(f.amount_micro) }}</span>
        <span class="fbal">池余 ¥{{ yuan(f.balance_after_micro) }}</span>
        <span class="tm">{{ fmtTs(f.ts) }}</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.pool-head h1 { display: flex; align-items: center; gap: 10px; font-size: 24px; margin: 0 0 8px; }
.pool-head .ic { width: 38px; height: 38px; border-radius: 11px; display: flex; align-items: center; justify-content: center; background: rgba(56,189,248,.12); color: var(--aqua,#38bdf8); border: 1px solid rgba(56,189,248,.3); flex: none; }
.pool-head p { color: var(--muted,#8a94a6); font-size: 13.5px; line-height: 1.7; margin: 0; max-width: 860px; }
.pool-head p b { color: var(--aqua,#38bdf8); }
.dash-card { background: var(--card,#121a26); border: 1px solid var(--border,rgba(128,140,160,.25)); border-radius: 16px; padding: 18px 20px; margin-top: 16px; }
.pool-main { position: relative; overflow: hidden; background: linear-gradient(135deg, rgba(56,189,248,.10), rgba(129,140,248,.08) 60%, transparent), var(--card,#121a26); }
.pool-main.empty { background: linear-gradient(135deg, rgba(248,113,113,.10), transparent 60%), var(--card,#121a26); }
.pool-state { display: inline-flex; align-items: center; gap: 7px; font-size: 12.5px; color: var(--muted,#8a94a6); border: 1px solid var(--border,rgba(128,140,160,.25)); border-radius: 999px; padding: 3px 11px; }
.pool-state .dot { width: 8px; height: 8px; border-radius: 50%; }
.pool-state .dot.on { background: #34d399; box-shadow: 0 0 8px rgba(52,211,153,.8); }
.pool-state .dot.off { background: #f87171; box-shadow: 0 0 8px rgba(248,113,113,.8); }
.pool-balance { font-size: 52px; font-weight: 800; margin: 10px 0 2px; background: linear-gradient(135deg,#67e8f9,#38bdf8,#818cf8); -webkit-background-clip: text; background-clip: text; color: transparent; font-variant-numeric: tabular-nums; }
.pool-balance small { font-size: 26px; margin-right: 2px; }
.pool-sub { font-size: 12.5px; color: var(--muted,#8a94a6); margin-bottom: 14px; }
.pool-stats { display: flex; gap: 26px; flex-wrap: wrap; border-top: 1px dashed var(--border,rgba(128,140,160,.25)); padding-top: 12px; }
.pool-stats b { display: block; font-size: 17px; font-variant-numeric: tabular-nums; }
.pool-stats span { font-size: 11.5px; color: var(--muted,#8a94a6); }
.pool-topup h2, .pool-ranks h2, .pool-ledger h2 { display: flex; align-items: center; gap: 7px; font-size: 15.5px; margin: 0 0 10px; }
.topup-note { font-size: 12.5px; color: var(--muted,#8a94a6); margin: 0 0 12px; line-height: 1.6; }
.topup-note b { color: #fbbf24; }
.amt-row { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 10px; }
.amt { padding: 9px 18px; border-radius: 10px; border: 1px solid var(--border,rgba(128,140,160,.3)); background: transparent; color: inherit; cursor: pointer; font-weight: 700; font-size: 14px; transition: all .15s; }
.amt.on { border-color: var(--aqua,#38bdf8); background: rgba(56,189,248,.12); color: var(--aqua,#38bdf8); box-shadow: 0 0 0 1px rgba(56,189,248,.3); }
.amt-row input { width: 110px; padding: 9px 12px; border-radius: 10px; border: 1px solid var(--border,rgba(128,140,160,.3)); background: transparent; color: inherit; }
.ch-row { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.ch { padding: 9px 16px; border-radius: 10px; border: 1px solid var(--border,rgba(128,140,160,.3)); background: transparent; color: inherit; cursor: pointer; font-size: 13px; }
.ch.on { border-color: var(--aqua,#38bdf8); background: rgba(56,189,248,.10); }
.topup-go { margin-left: auto; }
.btn.topup-go { background: linear-gradient(135deg,#0ea5e9,#6366f1); border: none; color: #fff; padding: 10px 22px; border-radius: 10px; font-weight: 700; cursor: pointer; }
.btn.topup-go:disabled { opacity: .5; cursor: not-allowed; }
.pay-wait, .pay-msg { font-size: 12.5px; margin: 10px 0 0; color: #fbbf24; }
.pay-msg.ok { color: #34d399; }
.rank-tabs { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 12px; align-items: center; }
.rtab { padding: 7px 14px; border-radius: 999px; border: 1px solid var(--border,rgba(128,140,160,.3)); background: transparent; color: var(--muted,#8a94a6); cursor: pointer; font-size: 12.5px; }
.rtab.on { border-color: var(--aqua,#38bdf8); color: var(--aqua,#38bdf8); background: rgba(56,189,248,.10); }
.rtab.sm { padding: 5px 11px; font-size: 11.5px; }
.rspacer { flex: 1; }
.rank-row { display: flex; align-items: center; gap: 10px; padding: 9px 12px; border-radius: 10px; font-size: 13px; border: 1px solid transparent; }
.rank-row:hover { background: rgba(56,189,248,.05); border-color: var(--border,rgba(128,140,160,.2)); }
.rank-row.hero { background: rgba(251,191,36,.06); border-color: rgba(251,191,36,.25); }
.rk { width: 26px; height: 26px; border-radius: 8px; display: flex; align-items: center; justify-content: center; font-size: 12px; font-weight: 800; background: rgba(128,140,160,.12); color: var(--muted,#8a94a6); flex: none; }
.rk.top:nth-child(1) { background: linear-gradient(135deg,#fbbf24,#f59e0b); color: #fff; }
.nm { font-weight: 600; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.badge-coin { font-size: 10.5px; padding: 2px 8px; border-radius: 999px; background: linear-gradient(135deg,rgba(251,191,36,.2),rgba(245,158,11,.15)); color: #fbbf24; border: 1px solid rgba(251,191,36,.35); white-space: nowrap; }
.badge-coin.elder { background: rgba(129,140,248,.14); color: #a5b4fc; border-color: rgba(129,140,248,.35); }
.badge-coin.hero-mini { font-size: 9.5px; padding: 1px 6px; }
.pct { font-size: 11.5px; color: var(--muted,#8a94a6); }
.amt-c { margin-left: auto; font-weight: 700; font-variant-numeric: tabular-nums; color: var(--aqua,#38bdf8); }
.tm { font-size: 11px; color: var(--muted,#8a94a6); white-space: nowrap; }
.honor-sec h3 { font-size: 13px; margin: 12px 0 6px; color: var(--muted,#8a94a6); }
.honor-sec h3:first-child { margin-top: 0; }
.rank-empty { padding: 18px; text-align: center; color: var(--muted,#8a94a6); font-size: 12.5px; border: 1px dashed var(--border,rgba(128,140,160,.25)); border-radius: 10px; margin: 4px 0; }
.flow-row { display: flex; align-items: center; gap: 10px; padding: 7px 12px; border-radius: 8px; font-size: 12.5px; }
.flow-row:hover { background: rgba(56,189,248,.05); }
.ft { width: 62px; flex: none; font-size: 11px; padding: 2px 0; text-align: center; border-radius: 6px; background: rgba(56,189,248,.10); color: var(--aqua,#38bdf8); }
.flow-row.consume .ft { background: rgba(128,140,160,.12); color: var(--muted,#8a94a6); }
.fnm { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.famt { margin-left: auto; font-weight: 700; font-variant-numeric: tabular-nums; color: #34d399; }
.famt.neg { color: var(--muted,#8a94a6); }
.fbal { font-size: 11px; color: var(--muted,#8a94a6); font-variant-numeric: tabular-nums; }
@media (max-width: 640px) {
  .pool-balance { font-size: 40px; }
  .pool-stats { gap: 16px; }
  .flow-row .fbal, .rank-row .pct { display: none; }
}
</style>
