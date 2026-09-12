<script setup lang="ts">
/* 站长管理控制台：单密码登录（独立 adm_ 会话，与用户体系隔离）
 * 视图：仪表盘 / 客户管理（搜索+批余额）/ 上游额度（充值换算）/ 审计日志（哈希链）/ 对账
 * 资金红线：高危操作二次密码；金额全程微元整数。 */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { apiJson, errText, fmt } from '@/composables/useApi'
import AqIcon from '@/components/AqIcon.vue'

type View = 'dashboard' | 'users' | 'quota' | 'supervision' | 'audit' | 'reconcile' | 'update'
const NAV: { id: View; label: string; icon: string }[] = [
  { id: 'dashboard', label: '仪表盘', icon: 'chart' },
  { id: 'users', label: '客户管理', icon: 'user' },
  { id: 'quota', label: '上游额度', icon: 'bolt' },
  { id: 'supervision', label: '额度监管', icon: 'gauge' },
  { id: 'audit', label: '审计日志', icon: 'list' },
  { id: 'reconcile', label: '对账', icon: 'shield' },
  { id: 'update', label: '系统更新', icon: 'refresh' },
]

const TOKEN_KEY = 'aqua_admin_token'
const token = ref('')
const view = ref<View>('dashboard')

/* 登录态 */
const logging = ref(false)
const loginPw = ref('')
const loginMsg = ref('')
const lastLogin = ref<{ ts: number; ip: string } | null>(null)

/* noindex：进入页面设 robots，离开还原 */
let metaEl: HTMLMetaElement | null = null
onMounted(() => {
  try { token.value = localStorage.getItem(TOKEN_KEY) || '' } catch { /* 忽略 */ }
  metaEl = document.createElement('meta')
  metaEl.name = 'robots'
  metaEl.content = 'noindex, nofollow'
  document.head.appendChild(metaEl)
  document.title = '管理控制台 · AQUA'
  if (token.value) { loadStats(); loadQuota(); loadSupervision() }
})
onUnmounted(() => { if (metaEl) metaEl.remove() })

async function doLogin() {
  if (!loginPw.value) return
  logging.value = true; loginMsg.value = ''
  try {
    const j = await apiJson<any>('/admin/login', { method: 'POST', body: { password: loginPw.value } })
    token.value = j.token
    try { localStorage.setItem(TOKEN_KEY, j.token) } catch { /* 忽略 */ }
    lastLogin.value = j.last_login || null
    loginPw.value = ''
    loadStats(); loadQuota(); loadSupervision()
  } catch (e) { loginMsg.value = errText(e) }
  logging.value = false
}

async function doLogout() {
  try { await apiJson('/admin/logout', { method: 'POST', key: token.value }) } catch { /* 忽略 */ }
  token.value = ''
  try { localStorage.removeItem(TOKEN_KEY) } catch { /* 忽略 */ }
}

function go(v: View) {
  view.value = v
  if (v === 'dashboard') loadStats()
  if (v === 'users') loadUsers()
  if (v === 'quota') loadQuota()
  if (v === 'supervision') loadSupervision()
  if (v === 'audit') loadAudit()
  if (v === 'reconcile') loadReconcile()
  if (v === 'update') loadUpdate()
}

/* 金额换算：微元 → 元（3 位小数） */
function yuan(micro: number | undefined | null): string {
  if (micro == null) return '—'
  return (micro / 1_000_000).toFixed(3)
}
function toMicro(y: string): number { return Math.round((Number(y) || 0) * 1_000_000) }

function fmtTime(ts: number): string { return ts ? new Date(ts * 1000).toLocaleString() : '—' }

/* ===== 仪表盘 ===== */
const stats = ref<any>(null)
const statsMsg = ref('')
const loadingStats = ref(false)
async function loadStats() {
  if (!token.value) return
  loadingStats.value = true; statsMsg.value = ''
  try { stats.value = await apiJson<any>('/admin/stats', { key: token.value }) }
  catch (e) {
    statsMsg.value = errText(e)
    if ((e as any)?.status === 401) token.value = ''
  }
  loadingStats.value = false
}
const quotaPct = computed(() => {
  if (!stats.value?.upstream) return 0
  const { total_micro, used_micro } = stats.value.upstream
  return total_micro > 0 ? Math.min(100, Math.round((used_micro / total_micro) * 100)) : 0
})
/** 最低输入三段价（元/百万tokens，当前计费档主卡显示） */
const minInPrice = computed<number | null>(() => {
  const arr = (stats.value?.prices || []).filter((p: any) => p?.mode === 'per_token' && p.in_price != null)
  return arr.length ? Math.min(...arr.map((p: any) => p.in_price)) : null
})
/** 保底安全自检（后端 floor_safety：每款生效价目保底 vs 含手续费保本线） */
const floorSafetyRows = computed<any[]>(() => stats.value?.floor_safety || [])
const floorSafeCount = computed(() => floorSafetyRows.value.filter(r => r.safe).length)
const floorUnsafe = computed(() => floorSafetyRows.value.length > 0 && floorSafeCount.value < floorSafetyRows.value.length)
const floorMargins = computed(() => floorSafetyRows.value.map(r => Number(r.margin_pct) || 0).filter(v => Number.isFinite(v)))
const floorMinMargin = computed(() => floorMargins.value.length ? Math.min(...floorMargins.value).toFixed(1) : '0')
const floorMaxMargin = computed(() => floorMargins.value.length ? Math.max(...floorMargins.value).toFixed(1) : '0')
/** tide 专线面值池消耗百分比（管理仪表盘进度条） */
const tideFacePct = computed(() => {
  const t = stats.value?.tide
  if (!t || !t.face_cap_micro) return 0
  return Math.min(100, Math.round((t.face_total_micro / t.face_cap_micro) * 100))
})
/** tide 专线已耗尽摘除的密钥数 */
const tideDeadKeys = computed(() => (stats.value?.tide?.keys || []).filter((k: any) => k.dead).length)
const rate = (c: number, ok: number) => (c > 0 ? Math.round((ok / c) * 1000) / 10 : 100)

/* ===== 客户管理 ===== */
const usersQ = ref('')
const usersPage = ref(1)
const usersTotal = ref(0)
const usersItems = ref<any[]>([])
const usersMsg = ref('')
const loadingUsers = ref(false)
async function loadUsers() {
  if (!token.value) return
  loadingUsers.value = true; usersMsg.value = ''
  try {
    const q = usersQ.value.trim() ? `&q=${encodeURIComponent(usersQ.value.trim())}` : ''
    const j = await apiJson<any>(`/admin/users?page=${usersPage.value}${q}`, { key: token.value })
    usersItems.value = j.items || []; usersTotal.value = j.total || 0
  } catch (e) { usersMsg.value = errText(e) }
  loadingUsers.value = false
}
function searchUsers() { usersPage.value = 1; loadUsers() }
const usersPages = computed(() => Math.max(1, Math.ceil(usersTotal.value / 20)))

/* 批余额弹窗 */
const adjust = ref<{ open: boolean; uid: number; name: string; sign: 1 | -1; amount: string; note: string; pw: string }>({
  open: false, uid: 0, name: '', sign: 1, amount: '', note: '', pw: '',
})
const adjustMsg = ref('')
const adjusting = ref(false)
function openAdjust(uid: number, name: string, sign: 1 | -1) {
  adjust.value = { open: true, uid, name, sign, amount: '', note: '', pw: '' }
  adjustMsg.value = ''
}
async function doAdjust() {
  const a = adjust.value
  const micro = toMicro(a.amount) * a.sign
  if (!micro) { adjustMsg.value = '请输入金额'; return }
  if (!a.note.trim()) { adjustMsg.value = '必须填写备注'; return }
  adjusting.value = true; adjustMsg.value = ''
  try {
    await apiJson(`/admin/users/${a.uid}/balance`, {
      method: 'POST', key: token.value,
      body: { amount_micro: micro, note: a.note.trim(), confirm_password: a.pw },
    })
    a.open = false
    loadUsers(); loadStats()
  } catch (e) { adjustMsg.value = errText(e) }
  adjusting.value = false
}

/* 用户详情（流水） */
const detail = ref<{ open: boolean; uid: number; name: string; user: any; summary: any; flows: any[]; page: number; total: number } | null>(null)
const detailMsg = ref('')
async function openDetail(uid: number) {
  detailMsg.value = ''
  try {
    const j = await apiJson<any>(`/admin/users/${uid}`, { key: token.value })
    detail.value = { open: true, uid, name: j.user.username, user: j.user, summary: j.summary, flows: j.flows.items, page: j.flows.page, total: j.flows.total }
  } catch (e) { detailMsg.value = errText(e) }
}
async function detailPage(p: number) {
  if (!detail.value) return
  try {
    const j = await apiJson<any>(`/admin/users/${detail.value.uid}?page=${p}`, { key: token.value })
    detail.value!.flows = j.flows.items; detail.value!.page = j.flows.page
  } catch (e) { detailMsg.value = errText(e) }
}

/* ===== 上游额度（共享资金池：acu/acu2 共用一个上游钱包，金额口径） ===== */
const quota = ref<any>(null)
const quotaMsg = ref('')
const topup = ref({ amount: '', note: '', pw: '' })
const topupMsg = ref('')
const topping = ref(false)
async function loadQuota() {
  if (!token.value) return
  try { quota.value = await apiJson<any>('/admin/quota', { key: token.value }) }
  catch (e) { quotaMsg.value = errText(e) }
}
const topupPreview = computed(() => {
  const micro = toMicro(topup.value.amount)
  // 换算预览按 acu2 通道成本 0.0027 元/次估算（v4-flash 通道成本更低，实际可跑更多次）
  return micro > 0 ? `≈ ${fmt(Math.floor(micro / 2700))} 次（按 acu2 通道成本 0.0027 元/次折算，v4-flash 通道更低）` : ''
})
async function doTopup() {
  const micro = toMicro(topup.value.amount)
  if (micro <= 0) { topupMsg.value = '请输入充值金额'; return }
  topping.value = true; topupMsg.value = ''
  try {
    const j = await apiJson<any>('/admin/quota/topup', {
      method: 'POST', key: token.value,
      body: { amount_micro: micro, note: topup.value.note.trim(), confirm_password: topup.value.pw },
    })
    topupMsg.value = `充值成功：池总额度 ¥${yuan(j.total_before)} → ¥${yuan(j.total_after)}`
    topup.value = { amount: '', note: '', pw: '' }
    loadQuota(); loadStats(); loadSupervision()
  } catch (e) { topupMsg.value = errText(e) }
  topping.value = false
}
async function resetCircuit() {
  if (!confirm('确认手动解除熔断？需确保上游额度已补充。')) return
  try {
    await apiJson('/admin/quota/circuit', { method: 'POST', key: token.value })
    loadQuota()
  } catch (e) { quotaMsg.value = errText(e) }
}

/* 同步上游真实剩余（绝对值对齐，非增量） */
const sync = ref({ amount: '', note: '', pw: '' })
const syncMsg = ref('')
const syncing = ref(false)
async function doSync() {
  const micro = toMicro(sync.value.amount)
  if (micro < 0) { syncMsg.value = '金额不能为负'; return }
  syncing.value = true; syncMsg.value = ''
  try {
    const j = await apiJson<any>('/admin/quota/sync', {
      method: 'POST', key: token.value,
      body: { remain_micro: micro, note: sync.value.note.trim(), confirm_password: sync.value.pw },
    })
    syncMsg.value = `同步成功：上游剩余 ¥${yuan(j.remain_micro)}（池总额度 ¥${yuan(j.total_before)} → ¥${yuan(j.total_after)}）`
    sync.value = { amount: '', note: '', pw: '' }
    loadQuota(); loadStats(); loadSupervision()
  } catch (e) { syncMsg.value = errText(e) }
  syncing.value = false
}

/* 汇率速查：用户充值 X 元 → 需向上游充值 Y 元才不超发
 * 售价 price_micro/次（当前价），成本 cost/次；渠道手续费本站承担（微信 7%/支付宝 6%），
 * 实收 = X × (1 − 费率)；上游需充 = 实收 × (成本/售价)，留利 = 实收 − 上游需充。 */
const rateCheck = ref('')
const RATE_ROWS = [1, 5, 6, 10, 50, 100]
/** 最不利渠道费率（微信 7%）：留利按保守口径 */
const FEE_RATE = 0.07
function rateNet(userYuan: number): number {
  return userYuan * (1 - FEE_RATE)
}
function rateNeed(userYuan: number): number {
  const price = sup.value?.core?.price_now_micro || 2000
  const cost = sup.value?.core?.cost_per_call_micro || 2700
  return Math.ceil((rateNet(userYuan) * cost) / price * 1000) / 1000 // 分向上取整到厘
}
const rateHint = computed(() => {
  const price = sup.value?.core?.price_now_micro || 2000
  const cost = sup.value?.core?.cost_per_call_micro || 2700
  const ratio = price > 0 ? (cost / price * 100).toFixed(1) : '—'
  return `用户每充 ¥1（实收 ¥${(1 - FEE_RATE).toFixed(2)}，微信 7% 手续费本站承担；最坏可撑 ${fmt(Math.floor(1_000_000 / price))} 次保底请求），需向上游充值 ¥${(Math.ceil(rateNet(1) * cost / price * 1000) / 1000).toFixed(3)} 才不留缺口 · 成本占比 ${ratio}%`
})

/* ===== 审计 ===== */
const auditItems = ref<any[]>([])
const auditPage = ref(1)
const auditTotal = ref(0)
const auditMsg = ref('')
async function loadAudit() {
  if (!token.value) return
  try {
    const j = await apiJson<any>(`/admin/audit?page=${auditPage.value}`, { key: token.value })
    auditItems.value = j.items || []; auditTotal.value = j.total || 0
  } catch (e) { auditMsg.value = errText(e) }
}
const auditPages = computed(() => Math.max(1, Math.ceil(auditTotal.value / 30)))

/* ===== 对账 ===== */
const reconcile = ref<any>(null)
const reconcileMsg = ref('')
const reconciling = ref(false)
async function loadReconcile() {
  if (!token.value) return
  reconciling.value = true; reconcileMsg.value = ''
  try { reconcile.value = await apiJson<any>('/admin/reconcile', { key: token.value }) }
  catch (e) { reconcileMsg.value = errText(e) }
  reconciling.value = false
}

/* ===== 额度监管 =====
 * A=上游剩余次数；B=用户总余额按当前价折算次数（售价只升不降，当前价=最坏口径）
 * 超发率=B/A；C=缺口。横幅阈值存 localStorage（仅前端提示，后端固定 60/80/100）。 */
const SUP_TH_KEY = 'aqua_sup_thresholds'
const supTh = ref({ warn: 60, danger: 80, critical: 100 })
try {
  const saved = JSON.parse(localStorage.getItem(SUP_TH_KEY) || '')
  if (saved && typeof saved.warn === 'number') supTh.value = saved
} catch { /* 忽略 */ }
function saveSupTh() {
  const t = supTh.value
  if (!(t.warn > 0 && t.danger > t.warn && t.critical >= t.danger)) return
  try { localStorage.setItem(SUP_TH_KEY, JSON.stringify(t)) } catch { /* 忽略 */ }
}

const sup = ref<any>(null)
const supMsg = ref('')
const supQ = ref('')
const supPage = ref(1)
const loadingSup = ref(false)
async function loadSupervision() {
  if (!token.value) return
  loadingSup.value = true; supMsg.value = ''
  try {
    const q = supQ.value.trim() ? `&q=${encodeURIComponent(supQ.value.trim())}` : ''
    sup.value = await apiJson<any>(`/admin/supervision?page=${supPage.value}${q}`, { key: token.value })
  } catch (e) { supMsg.value = errText(e) }
  loadingSup.value = false
}
function searchSup() { supPage.value = 1; loadSupervision() }
const supPages = computed(() => Math.max(1, Math.ceil(((sup.value?.ledger?.total) || 0) / 20)))

/* 按本机阈值重算级别（横幅 / 视图着色用） */
const supLevel = computed<'normal' | 'warn' | 'danger' | 'critical'>(() => {
  const pct = sup.value?.core?.overissue_pct ?? 0
  if (pct >= supTh.value.critical) return 'critical'
  if (pct >= supTh.value.danger) return 'danger'
  if (pct >= supTh.value.warn) return 'warn'
  return 'normal'
})
const supLevelLabel: Record<string, string> = { normal: '正常', warn: '预警', danger: '危险', critical: '已超发' }
/* 全控制台常驻告警横幅（级别≥预警才显示） */
const supBanner = computed(() => {
  if (!sup.value?.core || supLevel.value === 'normal') return null
  const c = sup.value.core
  const cost = c.cost_per_call_micro || 2700
  return {
    level: supLevel.value, label: supLevelLabel[supLevel.value], pct: c.overissue_pct,
    bYuan: (c.b_now_calls || 0) * cost, aYuan: (c.a_remain_calls || 0) * cost,
    gap: c.gap_calls, gapYuan: c.gap_topup_micro,
  }
})
/* 余额持有占比 */
function supShare(micro: number): string {
  const liab = sup.value?.core?.liability_micro || 0
  return liab > 0 ? ((micro / liab) * 100).toFixed(1) + '%' : '—'
}
/* 发放弹窗预检：发放后折算负债与超发率（只提示不阻断） */
const adjustPreview = computed(() => {
  const c = sup.value?.core
  if (!c || !adjust.value.open || adjust.value.sign <= 0) return ''
  const micro = toMicro(adjust.value.amount)
  if (micro <= 0) return ''
  const newLiab = (c.liability_micro || 0) + micro
  const newB = Math.floor(newLiab / (c.price_now_micro || 2000))
  const pct = c.a_remain_calls > 0 ? (newB / c.a_remain_calls) * 100 : 999
  return `发放后折算负债 ${fmt(newB)} 次最坏保底 / 上游剩余 ${fmt(c.a_remain_calls)} 次 · 超发率 ${pct.toFixed(1)}%${pct >= 100 ? '（将超发）' : pct >= supTh.value.warn ? '（进入预警）' : ''}`
})

/* ===== 系统更新（gitee 发行版源） ===== */
const update = ref<any>(null)
const updateMsg = ref('')
const updateMsgOk = ref(false)
const loadingUpdate = ref(false)
const applying = ref(false)
const applyTag = ref('')          // 待更新目标版本
const applyPw = ref('')           // 二次密码
const applyMsg = ref('')
async function loadUpdate() {
  if (!token.value) return
  loadingUpdate.value = true; updateMsg.value = ''; updateMsgOk.value = false
  try {
    update.value = await apiJson<any>('/admin/update/check', { key: token.value })
  } catch (e) {
    updateMsg.value = errText(e)
    if ((e as any)?.status === 401) token.value = ''
  }
  loadingUpdate.value = false
}
function askUpdate(tag: string) {
  applyTag.value = tag; applyPw.value = ''; applyMsg.value = ''
}
async function doUpdate() {
  if (!applyTag.value || !applyPw.value) return
  applying.value = true; applyMsg.value = ''
  try {
    const j = await apiJson<any>('/admin/update/apply', {
      method: 'POST', key: token.value,
      body: { tag: applyTag.value, confirm_password: applyPw.value },
    })
    applyTag.value = ''
    updateMsg.value = j.message || '更新完成，服务重启中，几秒后请刷新页面'
    updateMsgOk.value = true
    // 服务重启中：延迟重新检查版本（失败静默，让用户手动刷新）
    setTimeout(() => { loadUpdate().catch(() => {}) }, 6000)
  } catch (e) { applyMsg.value = errText(e) }
  applying.value = false
}
function fmtNotes(n: string): string[] {
  return (n || '').split(/\r?\n/).map((s) => s.trim()).filter(Boolean)
}

const flowTypeLabel: Record<string, string> = {
  prehold: '预扣', billed: '计费确认', refunded: '退回', topup: '站长发放', deduct: '站长扣减',
}
const streamModeLabel: Record<string, string> = {
  sim_stream: '模拟流式', passthrough: '流式透传', nonstream: '非流式',
}
</script>

<template>
  <section class="route-page admin-page">
    <!-- ===== 未登录：密码门 ===== -->
    <div v-if="!token" class="adm-gate">
      <div class="adm-gate-card">
        <div class="adm-gate-logo"><AqIcon name="shield" :size="26" /></div>
        <h1>管理控制台</h1>
        <p class="adm-gate-sub">仅站长访问 · 所有操作全程审计</p>
        <input
          v-model="loginPw" type="password" placeholder="管理密码" autocomplete="current-password"
          :disabled="logging" @keydown.enter="doLogin"
        />
        <button class="btn tool-run" :disabled="logging || !loginPw" @click="doLogin">
          {{ logging ? '验证中…' : '进入' }}
        </button>
        <p v-if="loginMsg" class="adm-msg bad">{{ loginMsg }}</p>
      </div>
    </div>

    <!-- ===== 已登录 ===== -->
    <template v-else>
      <!-- 上次登录自查横幅 -->
      <div v-if="lastLogin && lastLogin.ts" class="adm-lastlogin">
        <AqIcon name="info" :size="14" />
        上次登录：{{ fmtTime(lastLogin.ts) }} · IP {{ lastLogin.ip || '—' }}（非本人操作请立即改密）
      </div>

      <div class="adm-head">
        <h1><span class="ic"><AqIcon name="shield" :size="22" /></span>管理控制台</h1>
        <nav class="adm-tabs">
          <button v-for="n in NAV" :key="n.id" class="adm-tab" :class="{ on: view === n.id }" @click="go(n.id)">
            <AqIcon :name="n.icon" :size="15" /> {{ n.label }}
          </button>
        </nav>
        <button class="mini-btn danger" @click="doLogout">退出</button>
      </div>

      <!-- 额度监管告警横幅（全视图常驻，级别≥预警才显示） -->
      <div v-if="supBanner" class="adm-supban" :class="supBanner.level">
        <AqIcon name="alert" :size="15" />
        <b>额度{{ supBanner.label }}</b>
        <span>发放折算 ¥{{ yuan(supBanner.bYuan) }} / 上游剩余 ¥{{ yuan(supBanner.aYuan) }} · 超发率 {{ supBanner.pct }}%</span>
        <span v-if="supBanner.gap > 0">缺口 ¥{{ yuan(supBanner.gapYuan) }}（{{ fmt(supBanner.gap) }} 次），请及时向上游充值</span>
        <button class="mini-btn" @click="go('supervision')">查看监管</button>
      </div>

      <!-- ▼ 仪表盘 ▼ -->
      <div v-if="view === 'dashboard'" class="adm-view">
        <p v-if="statsMsg" class="adm-msg bad">{{ statsMsg }}</p>
        <div v-if="stats" class="adm-cards">
          <div class="adm-card wide">
            <b>上游共享资金池（acu / acu2 共用上游钱包）</b>
            <div class="adm-quota-bar"><span :style="{ width: quotaPct + '%' }"></span></div>
            <div class="adm-quota-nums">
              <span>总额度 ¥{{ yuan(stats.upstream.total_micro) }}</span>
              <span>已消耗 ¥{{ yuan(stats.upstream.used_micro) }}</span>
              <span class="hl">剩 ¥{{ yuan(stats.upstream.remain_micro) }}</span>
            </div>
            <div class="adm-card-foot">
              <span>累计充值 ¥{{ yuan(stats.upstream.topup_total_micro) }} · 成本（池已消耗）¥{{ yuan(stats.upstream.cost_micro) }}</span>
              <span v-if="stats.upstream.days_left >= 0">预计耗尽约 {{ stats.upstream.days_left }} 天</span>
              <span v-if="stats.upstream.circuit_open" class="adm-tag bad">已熔断</span>
            </div>
            <button class="mini-btn ok" @click="go('quota')"><AqIcon name="bolt" :size="12" /> 充值</button>
          </div>
          <div class="adm-card">
            <b>累计收入</b>
            <strong class="num hl">¥{{ yuan(stats.income.all_micro) }}</strong>
            <span>今日 ¥{{ yuan(stats.income.today_micro) }} · 7 日 ¥{{ yuan(stats.income.week_micro) }} · 30 日 ¥{{ yuan(stats.income.month_micro) }}</span>
            <span class="adm-card-foot">通道手续费（本站承担）¥{{ yuan(stats.upstream.fee_micro || 0) }} · 实付费率 {{ ((stats.upstream.fee_rate ?? 0) * 100).toFixed(2) }}%</span>
          </div>
          <div class="adm-card">
            <b>净利（收入 − 手续费 − 已履约上游成本）</b>
            <strong class="num">¥{{ yuan(stats.upstream.profit_net_micro ?? stats.upstream.profit_micro) }}</strong>
            <span>收入 ¥{{ yuan(stats.income.all_micro) }} − 通道手续费 ¥{{ yuan(stats.upstream.fee_est_micro ?? 0) }} − 已履约成本 ¥{{ yuan(stats.upstream.cost_billed_micro ?? stats.upstream.cost_micro) }}</span>
            <span class="adm-card-foot">毛利（未扣手续费）¥{{ yuan(stats.upstream.profit_micro) }} · 待履约负债 ¥{{ yuan(stats.users.liability_micro) }} / {{ stats.users.with_balance }} 人</span>
          </div>
          <div class="adm-card">
            <b>当前计费档</b>
            <strong class="num hl">
              <template v-if="minInPrice != null">按量 ¥{{ minInPrice }}<i class="unit">/百万tokens 起</i></template>
              <template v-else>¥{{ yuan(stats.price_micro) }}<i class="unit">/次 · 按次正式价</i></template>
            </strong>
            <span>{{ minInPrice != null ? '三段价：输入 / 缓存命中 / 输出分段计价' : '按次计费：每次成功请求扣一次，与生成长度无关' }}</span>
          </div>
          <div class="adm-card" :class="{ bad: floorUnsafe }">
            <b>保底安全校验（逐单不亏）</b>
            <strong class="num" :class="floorUnsafe ? 'bad-txt' : 'ok-txt'">{{ floorSafeCount }}/{{ floorSafetyRows.length }} 通过</strong>
            <span>每款单次收费 ≥ 含 7% 手续费的保本线（通道成本 ÷ 0.93）——任何单笔最低收费必然覆盖成本+手续费</span>
            <span class="adm-card-foot" v-if="floorUnsafe">存在亏损价目：网关已拒绝启用该行（自动回退安全价目），请立即修正 pricing 表</span>
            <span class="adm-card-foot" v-else>全部价目安全，单次收费安全边际 +{{ floorMinMargin }}% ~ +{{ floorMaxMargin }}%</span>
          </div>
        </div>

        <!-- ▼ 按量计费专线（tide/）· 独立池，数学级对账 ▼ -->
        <div v-if="stats?.tide" class="adm-cards">
          <div class="adm-card wide">
            <b>按量计费专线（tide/）· 密钥面值池</b>
            <div class="adm-quota-bar"><span :style="{ width: tideFacePct + '%' }"></span></div>
            <div class="adm-quota-nums">
              <span>面值总额 ¥{{ yuan(stats.tide.face_cap_micro) }}</span>
              <span>面值已耗 ¥{{ yuan(stats.tide.face_total_micro) }}</span>
              <span class="hl">剩 ¥{{ yuan(stats.tide.face_remain_micro) }}</span>
            </div>
            <div class="adm-card-foot">
              <span>今日面值消耗 ¥{{ yuan(stats.tide.face_today_micro) }} · 7 日 ¥{{ yuan(stats.tide.face_7d_micro) }}</span>
              <span>按量线毛利（收入 − 面值成本）¥{{ yuan(stats.tide.profit_micro) }}</span>
            </div>
          </div>
          <div class="adm-card">
            <b>按量线收入（累计）</b>
            <strong class="num hl">¥{{ yuan(stats.tide.income_micro) }}</strong>
            <span>今日 ¥{{ yuan(stats.tide.income_today_micro) }} · 已计费调用 {{ fmt(stats.tide.calls) }} 次</span>
            <span class="adm-card-foot">实测 tokens：输入 {{ fmt(stats.tide.tokens_in) }} / 缓存命中 {{ fmt(stats.tide.tokens_cached) }} / 输出 {{ fmt(stats.tide.tokens_out) }}</span>
          </div>
          <div class="adm-card" :class="{ bad: tideDeadKeys > 0 }">
            <b>专线密钥池</b>
            <strong class="num">{{ stats.tide.keys?.length || 0 }}<i class="unit"> 把</i></strong>
            <span>已耗尽摘除 {{ tideDeadKeys }} 把</span>
            <span class="adm-card-foot" v-if="stats.tide.keys?.length">
              <span v-for="k in stats.tide.keys" :key="k.idx" :class="{ 'bad-txt': k.dead }">#{{ k.idx }} 剩 ¥{{ yuan(k.remain_micro) }}{{ k.dead ? '（已摘除）' : '' }}　</span>
            </span>
          </div>
        </div>

        <div v-if="stats" class="adm-grid2">
          <div class="dash-sec">
            <b><AqIcon name="activity" :size="15" /> 收费模型调用</b>
            <table class="adm-table">
              <thead><tr><th>范围</th><th class="num">调用</th><th class="num">成功率</th></tr></thead>
              <tbody>
                <tr><td>今日</td><td class="num">{{ fmt(stats.calls.today) }}</td><td class="num">{{ rate(stats.calls.today, stats.calls.today_ok) }}%</td></tr>
                <tr><td>近 7 天</td><td class="num">{{ fmt(stats.calls.week) }}</td><td class="num">{{ rate(stats.calls.week, stats.calls.week_ok) }}%</td></tr>
                <tr><td>近 30 天</td><td class="num">{{ fmt(stats.calls.month) }}</td><td class="num">{{ rate(stats.calls.month, stats.calls.month_ok) }}%</td></tr>
                <tr><td>累计</td><td class="num">{{ fmt(stats.calls.all) }}</td><td class="num">{{ rate(stats.calls.all, stats.calls.all_ok) }}%</td></tr>
              </tbody>
            </table>
            <div v-if="stats.calls.fail_dist?.length" class="adm-fails">
              <span v-for="f in stats.calls.fail_dist" :key="f.reason" class="adm-tag">{{ f.reason }} × {{ f.count }}</span>
            </div>
          </div>
          <div class="dash-sec">
            <b><AqIcon name="trend" :size="15" /> 近 14 天趋势</b>
            <table class="adm-table">
              <thead><tr><th>日期</th><th class="num">调用</th><th class="num">收入</th></tr></thead>
              <tbody>
                <tr v-if="!stats.trend?.length"><td colspan="3" class="adm-empty">暂无数据</td></tr>
                <tr v-for="t in stats.trend" :key="t.date">
                  <td>{{ fmtTime(Number(t.date)).slice(0, 10) }}</td>
                  <td class="num">{{ fmt(t.calls) }}</td>
                  <td class="num">¥{{ yuan(t.income_micro) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <div v-if="stats?.top?.length" class="dash-sec">
          <b><AqIcon name="trophy" :size="15" /> 消费 TOP10</b>
          <table class="adm-table">
            <thead><tr><th>用户</th><th class="num">累计消费</th><th class="num">调用次数</th></tr></thead>
            <tbody>
              <tr v-for="(t, i) in stats.top" :key="t.user_id">
                <td>#{{ t.user_id }} {{ t.username || '—' }}</td>
                <td class="num">¥{{ yuan(t.cost_micro) }}</td>
                <td class="num">{{ fmt(t.calls) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <button v-if="loadingStats" class="adm-loading">加载中…</button>
      </div>

      <!-- ▼ 客户管理 ▼ -->
      <div v-if="view === 'users'" class="adm-view">
        <div class="adm-search">
          <input
            v-model="usersQ" placeholder="搜索用户名 / 邮箱 / UID"
            @keydown.enter="searchUsers"
          />
          <button class="btn tool-run" @click="searchUsers">搜索</button>
        </div>
        <p v-if="usersMsg" class="adm-msg bad">{{ usersMsg }}</p>
        <div class="dash-sec">
          <table class="adm-table">
            <thead>
              <tr>
                <th>用户</th><th>邮箱</th><th class="num">余额</th><th class="num">累计发放</th>
                <th class="num">累计消费</th><th class="num">调用</th><th>最后活跃</th><th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="!usersItems.length">
                <td colspan="8" class="adm-empty">{{ loadingUsers ? '加载中…' : '没有匹配的用户' }}</td>
              </tr>
              <tr v-for="u in usersItems" :key="u.id">
                <td class="nm">#{{ u.id }} {{ u.username }}</td>
                <td class="em">{{ u.email }}</td>
                <td class="num hl">¥{{ yuan(u.balance_micro) }}</td>
                <td class="num">¥{{ yuan(u.topup_micro) }}</td>
                <td class="num">¥{{ yuan(u.cost_micro) }}</td>
                <td class="num">{{ fmt(u.calls) }}</td>
                <td class="tm">{{ fmtTime(u.last_login_ts) }}</td>
                <td class="ops">
                  <button class="mini-btn ok" @click="openAdjust(u.id, u.username, 1)">加余额</button>
                  <button class="mini-btn danger" @click="openAdjust(u.id, u.username, -1)">减余额</button>
                  <button class="mini-btn" @click="openDetail(u.id)">详情</button>
                </td>
              </tr>
            </tbody>
          </table>
          <div class="adm-pager" v-if="usersPages > 1">
            <button class="mini-btn" :disabled="usersPage <= 1" @click="usersPage--; loadUsers()">上一页</button>
            <span>{{ usersPage }} / {{ usersPages }} 页 · 共 {{ fmt(usersTotal) }} 人</span>
            <button class="mini-btn" :disabled="usersPage >= usersPages" @click="usersPage++; loadUsers()">下一页</button>
          </div>
        </div>
      </div>

      <!-- ▼ 上游额度 ▼ -->
      <div v-if="view === 'quota'" class="adm-view">
        <p v-if="quotaMsg" class="adm-msg bad">{{ quotaMsg }}</p>
        <div v-if="quota" class="adm-grid2">
          <div class="dash-sec">
            <b><AqIcon name="bolt" :size="15" /> 共享资金池（acu / acu2 共用上游钱包）</b>
            <div class="adm-kv">
              <span>总额度</span><b>¥{{ yuan(quota.pool?.total_micro) }}</b>
              <span>已消耗</span><b>¥{{ yuan(quota.pool?.used_micro) }}</b>
              <span class="hl">剩余</span><b class="hl">¥{{ yuan(quota.pool?.remain_micro) }}</b>
              <span>熔断状态</span>
              <b>
                <span v-if="quota.pool?.circuit_open" class="adm-tag bad">已熔断</span>
                <span v-else class="adm-tag ok">正常</span>
                <button v-if="quota.pool?.circuit_open" class="mini-btn" style="margin-left:8px" @click="resetCircuit">解除熔断</button>
              </b>
            </div>
            <p class="adm-hint">池剩余 ≤ ¥0.54 自动熔断（防超额欠费）；充值/同步后自动解除。两把上游密钥（acu = v4-flash 专线，acu2 = 新模型通道）共用同一个上游钱包，扣费统一从池里按各自上游成本计。</p>
            <table class="adm-table" style="margin-top:10px">
              <thead><tr><th>通道</th><th class="num">累计额度(次)</th><th class="num">已用(次)</th><th class="num">上游成本</th></tr></thead>
              <tbody>
                <tr v-for="c in quota.channels || []" :key="c.provider">
                  <td>{{ c.provider === 'acu' ? 'acu（v4-flash 专线）' : 'acu2（新模型通道）' }}</td>
                  <td class="num">{{ fmt(c.total_calls) }}</td>
                  <td class="num">{{ fmt(c.used_calls) }}</td>
                  <td class="num">¥{{ yuan(c.cost_per_call_micro) }}/次</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="dash-sec">
            <b><AqIcon name="spark" :size="15" /> 给上游充值（进共享池）</b>
            <div class="adm-form">
              <label>充值金额（元）<input v-model="topup.amount" type="number" min="0" step="0.001" placeholder="如 10" /></label>
              <label>备注（可选）<input v-model="topup.note" maxlength="100" placeholder="如：微信充值" /></label>
              <label>管理密码确认<input v-model="topup.pw" type="password" autocomplete="current-password" /></label>
              <p v-if="topupPreview" class="adm-hint ok">换算预览：¥{{ topup.amount }} {{ topupPreview }}</p>
              <button class="btn tool-run" :disabled="topping || !topup.amount || !topup.pw" @click="doTopup">
                {{ topping ? '充值中…' : '确认充值' }}
              </button>
              <p v-if="topupMsg" class="adm-msg" :class="{ bad: topupMsg.includes('失败') || topupMsg.includes('错误') }">{{ topupMsg }}</p>
            </div>
          </div>
        </div>
        <div class="adm-grid2">
          <div class="dash-sec">
            <b><AqIcon name="refresh" :size="15" /> 同步上游真实剩余</b>
            <div class="adm-form">
              <label>上游当前剩余（元）<input v-model="sync.amount" type="number" min="0" step="0.001" placeholder="如：上游后台看到的余额" /></label>
              <label>备注（可选）<input v-model="sync.note" maxlength="100" placeholder="如：月度对齐" /></label>
              <label>管理密码确认<input v-model="sync.pw" type="password" autocomplete="current-password" /></label>
              <p class="adm-hint">填上游后台显示的剩余金额：系统按「池已消耗 + 剩余金额」直接对齐总额度（绝对值，非增量，自动消除漂移）。与「给上游充值」区别：充值是加钱，同步是对准真实值。</p>
              <button class="btn tool-run" :disabled="syncing || !sync.amount || !sync.pw" @click="doSync">
                {{ syncing ? '同步中…' : '同步真实余额' }}
              </button>
              <p v-if="syncMsg" class="adm-msg" :class="{ bad: syncMsg.includes('失败') || syncMsg.includes('错误') }">{{ syncMsg }}</p>
            </div>
          </div>
          <div class="dash-sec">
            <b><AqIcon name="gauge" :size="15" /> 充值换算速查</b>
            <p class="adm-hint ok">{{ rateHint }}</p>
            <table class="adm-table">
              <thead><tr><th>用户充值</th><th class="num">保底请求(最坏)</th><th class="num">需向上游充</th><th class="num">留利(扣费后)</th></tr></thead>
              <tbody>
                <tr v-for="r in RATE_ROWS" :key="r">
                  <td>¥{{ r }}</td>
                  <td class="num">{{ fmt(Math.floor((r * 1_000_000) / (sup?.core?.price_now_micro || 2000))) }}</td>
                  <td class="num">¥{{ rateNeed(r) }}</td>
                  <td class="num ok">¥{{ (rateNet(r) - rateNeed(r)).toFixed(3) }}</td>
                </tr>
                <tr v-if="rateCheck">
                  <td>¥{{ rateCheck }}</td>
                  <td class="num">{{ fmt(Math.floor((Number(rateCheck) * 1_000_000 || 0) / (sup?.core?.price_now_micro || 2000))) }}</td>
                  <td class="num hl">¥{{ rateNeed(Number(rateCheck) || 0) }}</td>
                  <td class="num ok">¥{{ (rateNet(Number(rateCheck) || 0) - rateNeed(Number(rateCheck) || 0)).toFixed(3) }}</td>
                </tr>
              </tbody>
            </table>
            <div class="adm-search" style="margin-top:10px">
              <input v-model="rateCheck" type="number" min="0" step="0.01" placeholder="任意金额试算：如用户充 6 元，要向上游充多少？" />
            </div>
          </div>
        </div>
        <div v-if="quota?.topups?.length" class="dash-sec">
          <b><AqIcon name="list" :size="15" /> 充值/同步历史（资金口径）</b>
          <table class="adm-table">
            <thead><tr><th>时间</th><th>对象</th><th class="num">金额</th><th class="num">池总额度变化</th><th>备注</th></tr></thead>
            <tbody>
              <tr v-for="(t, i) in quota.topups" :key="t.ts + '-' + i">
                <td class="tm">{{ fmtTime(t.ts) }}</td>
                <td>{{ t.provider === 'pool' ? '共享池' : t.provider }}</td>
                <td class="num">¥{{ yuan(t.amount_micro) }}</td>
                <td class="num">
                  <template v-if="t.provider === 'pool'">¥{{ yuan(t.total_before) }} → ¥{{ yuan(t.total_after) }}</template>
                  <template v-else>{{ fmt(t.total_before) }} → {{ fmt(t.total_after) }} 次</template>
                </td>
                <td>{{ t.note || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- ▼ 额度监管 ▼ -->
      <div v-if="view === 'supervision'" class="adm-view">
        <p v-if="supMsg" class="adm-msg bad">{{ supMsg }}</p>
        <div v-if="sup?.core" class="adm-cards">
          <div class="adm-card">
            <b>A · 上游资产（剩余）</b>
            <strong class="num hl">¥{{ yuan(sup.core.a_remain_calls * sup.core.cost_per_call_micro) }}<i class="unit">（{{ fmt(sup.core.a_remain_calls) }} 次）</i></strong>
            <span>池总额 ¥{{ yuan(sup.core.pool_total_micro) }} · 已消耗 ¥{{ yuan(sup.core.pool_used_micro) }} · 成本 ¥{{ yuan(sup.core.cost_per_call_micro) }}/次</span>
            <span class="adm-card-foot">
              <span v-if="sup.core.days_left >= 0">预计耗尽约 {{ sup.core.days_left }} 天</span>
              <span>近 7 天消耗 ¥{{ yuan(sup.core.used_7d_money) }}</span>
            </span>
          </div>
          <div class="adm-card">
            <b>B · 发放折算（履约成本）</b>
            <strong class="num">¥{{ yuan(sup.core.b_now_calls * sup.core.cost_per_call_micro) }}<i class="unit">（{{ fmt(sup.core.b_now_calls) }} 次）</i></strong>
            <span>用户余额合计 ¥{{ yuan(sup.core.liability_micro) }} · {{ sup.core.holders }} 人持有</span>
            <span class="adm-card-foot">按最低单价 ¥{{ yuan(sup.core.price_now_micro) }}/次折算（最坏口径，告警依据）</span>
          </div>
          <div class="adm-card" :class="{ bad: supLevel !== 'normal' }">
            <b>超发率 B ÷ A</b>
            <strong class="num">{{ sup.core.overissue_pct }}<i class="unit">%</i></strong>
            <span>状态：{{ supLevelLabel[supLevel] }}（阈值 {{ supTh.warn }}/{{ supTh.danger }}/{{ supTh.critical }}%）</span>
            <span class="adm-card-foot" v-if="sup.core.gap_calls > 0">缺口 ¥{{ yuan(sup.core.gap_topup_micro) }}（{{ fmt(sup.core.gap_calls) }} 次）· 需向上游充值补齐</span>
            <span class="adm-card-foot" v-else>富余 ¥{{ yuan(-sup.core.gap_calls * sup.core.cost_per_call_micro) }}（{{ fmt(-sup.core.gap_calls) }} 次），完全可覆盖</span>
          </div>
        </div>

        <!-- 结论行 -->
        <div v-if="sup?.core" class="adm-supverdict" :class="supLevel">
          <template v-if="sup.core.gap_calls <= 0">
            <b class="ok-txt">✓ 可覆盖</b>
            <span>用户把余额全部消费完，履约成本 ¥{{ yuan(sup.core.b_now_calls * sup.core.cost_per_call_micro) }} ≤ 上游剩余 ¥{{ yuan(sup.core.a_remain_calls * sup.core.cost_per_call_micro) }}；富余 <b class="ok-txt">¥{{ yuan(-sup.core.gap_calls * sup.core.cost_per_call_micro) }}</b>（{{ fmt(-sup.core.gap_calls) }} 次）。</span>
          </template>
          <template v-else>
            <b class="bad-txt">✗ 有缺口</b>
            <span>用户全部消费完履约成本 ¥{{ yuan(sup.core.b_now_calls * sup.core.cost_per_call_micro) }}，超出上游剩余 ¥{{ yuan(sup.core.a_remain_calls * sup.core.cost_per_call_micro) }}，缺口 <b class="bad-txt">¥{{ yuan(sup.core.gap_topup_micro) }}</b>（{{ fmt(sup.core.gap_calls) }} 次）；向上游充值等额即可补齐。</span>
          </template>
        </div>

        <!-- 阈值（仅前端横幅，本机保存） -->
        <div class="adm-supth">
          横幅告警阈值（%）：预警
          <input v-model.number="supTh.warn" type="number" min="1" @change="saveSupTh" />
          危险
          <input v-model.number="supTh.danger" type="number" min="1" @change="saveSupTh" />
          超发
          <input v-model.number="supTh.critical" type="number" min="1" @change="saveSupTh" />
          <span>仅影响本机横幅提示（后端状态固定 60/80/100）</span>
        </div>

        <!-- 发放台账 -->
        <div class="dash-sec">
          <b><AqIcon name="list" :size="15" /> 发放台账（站长发放 / 扣减 / 在线充值）
            <span class="sup-sum">站长发放 ¥{{ yuan(sup?.summary?.admin_topup_micro) }} · 在线充值 ¥{{ yuan(sup?.summary?.epay_topup_micro) }} · 累计扣减 ¥{{ yuan(Math.abs(sup?.summary?.cum_deduct_micro || 0)) }} · 净发放 ¥{{ yuan(sup?.summary?.net_issued_micro) }}</span>
          </b>
          <div class="adm-search">
            <input
              v-model="supQ" placeholder="搜索发放对象：用户名 / 邮箱 / UID"
              @keydown.enter="searchSup"
            />
            <button class="btn tool-run" @click="searchSup">搜索</button>
          </div>
          <table class="adm-table">
            <thead><tr><th>时间</th><th>用户</th><th>类型</th><th class="num">金额</th><th>备注</th></tr></thead>
            <tbody>
              <tr v-if="!(sup?.ledger?.items?.length)">
                <td colspan="5" class="adm-empty">{{ loadingSup ? '加载中…' : '暂无发放记录' }}</td>
              </tr>
              <tr v-for="l in sup?.ledger?.items || []" :key="l.id">
                <td class="tm">{{ fmtTime(l.ts) }}</td>
                <td class="nm">#{{ l.user_id }} {{ l.username }}</td>
                <td><span class="adm-tag" :class="l.type === 'topup' ? 'ok' : 'bad'">{{ l.operator === 'epay' ? '在线充值' : (l.type === 'topup' ? '站长发放' : '站长扣减') }}</span></td>
                <td class="num" :class="l.amount_micro >= 0 ? 'ok' : 'bad'">{{ l.amount_micro >= 0 ? '+' : '−' }}¥{{ yuan(Math.abs(l.amount_micro)) }}</td>
                <td>{{ l.note || '—' }}</td>
              </tr>
            </tbody>
          </table>
          <div class="adm-pager" v-if="supPages > 1">
            <button class="mini-btn" :disabled="supPage <= 1" @click="supPage--; loadSupervision()">上一页</button>
            <span>{{ supPage }} / {{ supPages }} 页 · 共 {{ fmt(sup?.ledger?.total || 0) }} 笔</span>
            <button class="mini-btn" :disabled="supPage >= supPages" @click="supPage++; loadSupervision()">下一页</button>
          </div>
        </div>

        <!-- 余额持有 TOP 榜 -->
        <div class="dash-sec">
          <b><AqIcon name="trophy" :size="15" /> 余额持有 TOP（当前有余额用户）</b>
          <table class="adm-table">
            <thead><tr><th>用户</th><th>邮箱</th><th class="num">当前余额</th><th class="num">占负债比</th><th class="num">累计发放</th><th class="num">累计消费</th><th class="num">计费调用</th></tr></thead>
            <tbody>
              <tr v-if="!(sup?.top?.length)"><td colspan="7" class="adm-empty">当前没有持有余额的用户</td></tr>
              <tr v-for="t in sup?.top || []" :key="t.id">
                <td class="nm">#{{ t.id }} {{ t.username }}</td>
                <td class="em">{{ t.email }}</td>
                <td class="num hl">¥{{ yuan(t.balance_micro) }}</td>
                <td class="num">{{ supShare(t.balance_micro) }}</td>
                <td class="num">¥{{ yuan(t.topup_micro) }}</td>
                <td class="num">¥{{ yuan(t.spent_micro) }}</td>
                <td class="num">{{ fmt(t.billed_calls) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <button class="adm-loading" style="margin-top:0" v-if="loadingSup">加载中…</button>
      </div>

      <!-- ▼ 审计日志 ▼ -->
      <div v-if="view === 'audit'" class="adm-view">
        <p v-if="auditMsg" class="adm-msg bad">{{ auditMsg }}</p>
        <div class="dash-sec">
          <table class="adm-table">
            <thead><tr><th>时间</th><th>操作</th><th>对象</th><th>详情</th><th>IP</th><th>哈希链</th></tr></thead>
            <tbody>
              <tr v-if="!auditItems.length"><td colspan="6" class="adm-empty">暂无记录</td></tr>
              <tr v-for="a in auditItems" :key="a.ts + '-' + a.action">
                <td class="tm">{{ fmtTime(a.ts) }}</td>
                <td><span class="adm-tag" :class="{ ok: a.action.includes('ok') || a.action.includes('topup'), bad: a.action.includes('fail') }">{{ a.action }}</span></td>
                <td class="num">{{ a.target_user ? '#' + a.target_user : '—' }}</td>
                <td class="dt">{{ a.detail || '—' }}</td>
                <td>{{ a.ip || '—' }}</td>
                <td class="hs" :title="a.self_hash">{{ a.self_hash?.slice(0, 8) }}…</td>
              </tr>
            </tbody>
          </table>
          <div class="adm-pager" v-if="auditPages > 1">
            <button class="mini-btn" :disabled="auditPage <= 1" @click="auditPage--; loadAudit()">上一页</button>
            <span>{{ auditPage }} / {{ auditPages }} 页</span>
            <button class="mini-btn" :disabled="auditPage >= auditPages" @click="auditPage++; loadAudit()">下一页</button>
          </div>
        </div>
      </div>

      <!-- ▼ 对账 ▼ -->
      <div v-if="view === 'reconcile'" class="adm-view">
        <button class="btn tool-run" :disabled="reconciling" @click="loadReconcile">
          {{ reconciling ? '对账中…' : '一键对账' }}
        </button>
        <p v-if="reconcileMsg" class="adm-msg bad">{{ reconcileMsg }}</p>
        <div v-if="reconcile" class="adm-cards">
          <div class="adm-card" :class="{ bad: !reconcile.balance_replay.ok }">
            <b>余额重放对账</b>
            <strong class="num">{{ reconcile.balance_replay.ok ? '✓ 一致' : '✗ 不一致' }}</strong>
            <span>重放全部流水 vs 当前余额（异常用户 {{ reconcile.balance_replay.mismatch_users }} 个）</span>
          </div>
          <div class="adm-card" :class="{ bad: !reconcile.billing_cross.ok }">
            <b>计费交叉对账</b>
            <strong class="num">{{ reconcile.billing_cross.ok ? '✓ 一致' : '✗ 不一致' }}</strong>
            <span>计费明细 {{ fmt(reconcile.billing_cross.billed_requests) }} 条 vs 计费流水 {{ fmt(reconcile.billing_cross.billed_flows) }} 笔</span>
          </div>
          <div class="adm-card" :class="{ bad: !reconcile.upstream_cross.ok }">
            <b>上游交叉对账</b>
            <strong class="num">{{ reconcile.upstream_cross.ok ? '✓ 一致' : '✗ 不一致' }}</strong>
            <span>计费明细 {{ fmt(reconcile.upstream_cross.billed_requests) }} 条 vs 上游计数 {{ fmt(reconcile.upstream_cross.used_calls) }} 次</span>
          </div>
          <div class="adm-card" :class="{ bad: !reconcile.audit_chain.ok }">
            <b>审计哈希链</b>
            <strong class="num">{{ reconcile.audit_chain.ok ? '✓ 完整' : '✗ 断裂' }}</strong>
            <span>{{ reconcile.audit_chain.ok ? '全部记录连续无篡改' : '第 ' + reconcile.audit_chain.broken_at + ' 条之后链断裂' }}</span>
          </div>
        </div>
      </div>

      <!-- ▼ 系统更新 ▼ -->
      <div v-else-if="view === 'update'" class="adm-view">
        <p v-if="updateMsg" class="adm-msg" :class="{ ok: updateMsgOk, bad: !updateMsgOk }">{{ updateMsg }}</p>
        <!-- 检查中：加载动画（双源并发最长约 5 秒） -->
        <div v-if="loadingUpdate" class="adm-loading" role="status" aria-live="polite">
          <span class="spin" aria-hidden="true"></span>
          <span>正在检查更新源：gitee 主仓库 + github 镜像双源并发，通常 1-5 秒…</span>
        </div>
        <!-- 检查失败（仅在请求异常时出现） -->
        <p v-else-if="!update" class="adm-msg bad">
          无法连接更新源（上方为具体错误）。请检查网络；海外服务器访问 gitee 常被 CDN 拦截，可按《管理后台与自动更新指南》2.4 节配置 proxy 或 api_base。
        </p>
        <p v-else-if="!update.update_enabled" class="adm-msg">
          在线更新未启用：需在网关配置 <code>[update]</code> 段设置 <code>repo</code> 并开启 <code>enabled = true</code>，详见《管理后台与自动更新指南》。
        </p>
        <div v-if="update" class="adm-cards">
          <div class="adm-card wide">
            <b>当前运行版本</b>
            <strong class="num">{{ update.current_version }}</strong>
            <span v-if="update.repo || update.mirror_repo">
              gitee {{ update.repo }}（{{ update.sources?.gitee?.ok ? '可达' : '被拦' }}）
              · github 镜像 {{ update.mirror_repo }}（{{ update.sources?.github?.ok ? '可达' : '不可达' }})
            </span>
            <span>附件 {{ update.asset_name }} · 发版方式：构建提交进仓库 assets/ 目录并打 tag push（gitee 自动同步 github）</span>
            <div class="adm-card-foot">
              <button class="mini-btn" :disabled="loadingUpdate" @click="loadUpdate">重新检查</button>
              <span class="dim">刚发完版请等几分钟，github 镜像同步 tag 有延迟</span>
            </div>
          </div>
        </div>
        <div class="adm-cards">
          <div v-for="rel in update?.releases || []" :key="rel.tag" class="adm-card wide">
            <b>
              {{ rel.tag }}
              <span v-if="rel.is_current" class="adm-tag ok">当前版本</span>
              <span v-else-if="rel.downloadable" class="adm-tag">可更新</span>
              <span v-else class="adm-tag warn">无附件</span>
            </b>
            <span v-if="rel.name && rel.name !== rel.tag">{{ rel.name }}</span>
            <span class="tm">{{ rel.published_at }}</span>
            <ul v-if="fmtNotes(rel.notes).length" class="adm-notes">
              <li v-for="(n, i) in fmtNotes(rel.notes)" :key="i">{{ n }}</li>
            </ul>
            <div class="adm-card-foot">
              <button
                v-if="rel.downloadable && !rel.is_current" class="mini-btn"
                @click="askUpdate(rel.tag)"
              >更新到此版本</button>
              <span v-if="rel.asset_size" class="dim">附件 {{ (rel.asset_size / 1048576).toFixed(1) }} MB</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 更新确认弹窗 -->
      <div v-if="applyTag" class="adm-mask" @click.self="applyTag = ''">
        <div class="adm-modal">
          <h3>更新系统到 {{ applyTag }}</h3>
          <p class="adm-hint">将自动下载发行版附件 → 校验 → 备份当前程序 → 替换 → 重启服务（约 3-10 秒中断）。更新前会自动备份，可回滚。</p>
          <label>管理密码确认<input v-model="applyPw" type="password" autocomplete="current-password" @keydown.enter="doUpdate" /></label>
          <p v-if="applyMsg" class="adm-msg bad">{{ applyMsg }}</p>
          <div class="adm-modal-ops">
            <button class="mini-btn" @click="applyTag = ''">取消</button>
            <button class="btn tool-run" :disabled="applying || !applyPw" @click="doUpdate">
              {{ applying ? '下载更新中…' : '确认更新' }}
            </button>
          </div>
        </div>
      </div>

      <!-- 批余额弹窗 -->
      <div v-if="adjust.open" class="adm-mask" @click.self="adjust.open = false">
        <div class="adm-modal">
          <h3>{{ adjust.sign > 0 ? '给用户发放余额' : '扣减用户余额' }}</h3>
          <p class="adm-modal-user">#{{ adjust.uid }} {{ adjust.name }}</p>
          <label>金额（元）<input v-model="adjust.amount" type="number" min="0" step="0.001" :placeholder="adjust.sign > 0 ? '如 10' : '如 2'" /></label>
          <label>备注（必填，审计留痕）<input v-model="adjust.note" maxlength="100" placeholder="如：活动赠送 / 误发追回" /></label>
          <label>管理密码确认<input v-model="adjust.pw" type="password" autocomplete="current-password" /></label>
          <p v-if="adjust.sign < 0" class="adm-hint">减余额封底 0，不允许扣成负数。</p>
          <p v-if="adjustPreview" class="adm-hint" :class="{ bad: adjustPreview.includes('超发') }">{{ adjustPreview }}</p>
          <p v-if="adjustMsg" class="adm-msg bad">{{ adjustMsg }}</p>
          <div class="adm-modal-ops">
            <button class="mini-btn" @click="adjust.open = false">取消</button>
            <button class="btn tool-run" :disabled="adjusting || !adjust.amount || !adjust.pw" @click="doAdjust">
              {{ adjusting ? '执行中…' : (adjust.sign > 0 ? '确认发放' : '确认扣减') }}
            </button>
          </div>
        </div>
      </div>

      <!-- 用户详情弹窗 -->
      <div v-if="detail?.open" class="adm-mask" @click.self="detail!.open = false">
        <div class="adm-modal wide">
          <h3>#{{ detail.uid }} {{ detail.name }} · 账目详情</h3>
          <p v-if="detailMsg" class="adm-msg bad">{{ detailMsg }}</p>
          <div v-if="detail.user" class="adm-kv">
            <span>邮箱</span><b>{{ detail.user.email }}</b>
            <span>当前余额</span><b class="hl">¥{{ yuan(detail.user.balance_micro) }}</b>
            <span>累计发放</span><b>¥{{ yuan(detail.summary.topup_micro) }}</b>
            <span>累计消费</span><b>¥{{ yuan(detail.summary.cost_micro) }}</b>
            <span>累计退回</span><b>¥{{ yuan(detail.summary.refund_micro) }}</b>
            <span>计费调用</span><b>{{ fmt(detail.summary.calls) }} 次</b>
          </div>
          <table class="adm-table">
            <thead><tr><th>时间</th><th>类型</th><th class="num">金额</th><th class="num">余额</th><th>模型</th><th>备注</th></tr></thead>
            <tbody>
              <tr v-if="!detail.flows.length"><td colspan="6" class="adm-empty">暂无流水</td></tr>
              <tr v-for="f in detail.flows" :key="f.ts + '-' + f.type">
                <td class="tm">{{ fmtTime(f.ts) }}</td>
                <td><span class="adm-tag">{{ flowTypeLabel[f.type] || f.type }}</span></td>
                <td class="num" :class="{ ok: f.amount_micro > 0, bad: f.amount_micro < 0 }">
                  {{ f.amount_micro > 0 ? '+' : '' }}¥{{ yuan(Math.abs(f.amount_micro)) }}
                </td>
                <td class="num">¥{{ yuan(f.balance_after_micro) }}</td>
                <td>{{ f.model || '—' }}</td>
                <td>{{ f.note || '—' }}</td>
              </tr>
            </tbody>
          </table>
          <div class="adm-pager" v-if="Math.ceil(detail.total / 20) > 1">
            <button class="mini-btn" :disabled="detail.page <= 1" @click="detailPage(detail.page - 1)">上一页</button>
            <span>{{ detail.page }} / {{ Math.ceil(detail.total / 20) }} 页</span>
            <button class="mini-btn" :disabled="detail.page >= Math.ceil(detail.total / 20)" @click="detailPage(detail.page + 1)">下一页</button>
          </div>
          <div class="adm-modal-ops"><button class="mini-btn" @click="detail!.open = false">关闭</button></div>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.admin-page { min-height: 70vh; }

/* 密码门 */
.adm-gate { display: flex; align-items: center; justify-content: center; min-height: 60vh; }
.adm-gate-card { width: 340px; background: var(--card); border: 1px solid var(--border); border-radius: 16px; padding: 34px 30px; display: flex; flex-direction: column; gap: 12px; text-align: center; }
.adm-gate-logo { width: 56px; height: 56px; margin: 0 auto; border-radius: 16px; background: rgba(11,108,255,.12); color: var(--accent, #0b6cff); display: flex; align-items: center; justify-content: center; }
.adm-gate-card h1 { font-size: 19px; }
.adm-gate-sub { font-size: 12px; color: var(--muted, #8a94a6); }
.adm-gate-card input { padding: 12px 14px; border-radius: 10px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; text-align: center; font-size: 15px; }
.adm-gate-card input:focus { outline: none; border-color: var(--accent, #0b6cff); }

.adm-lastlogin { display: flex; align-items: center; gap: 6px; font-size: 12px; color: var(--muted, #8a94a6); background: var(--card); border: 1px solid var(--border); border-radius: 10px; padding: 8px 12px; margin-bottom: 12px; }

.adm-head { display: flex; align-items: center; gap: 14px; margin: 10px 0 18px; flex-wrap: wrap; }
.adm-head h1 { font-size: 21px; font-weight: 800; display: flex; align-items: center; gap: 10px; }
.adm-head h1 .ic { color: var(--accent, #0b6cff); }
.adm-tabs { display: flex; gap: 4px; flex-wrap: wrap; margin-left: auto; }
.adm-tab { display: inline-flex; align-items: center; gap: 6px; padding: 8px 13px; border: none; background: transparent; border-radius: 10px; color: var(--muted, #8a94a6); font-size: 13px; cursor: pointer; }
.adm-tab:hover { background: rgba(128,140,160,.1); color: inherit; }
.adm-tab.on { background: rgba(11,108,255,.14); color: var(--accent, #0b6cff); font-weight: 700; }

.adm-view { animation: viewin .18s ease; }
@keyframes viewin { from { opacity: 0; transform: translateY(4px); } to { opacity: 1; transform: none; } }

.adm-cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 14px; margin-bottom: 16px; }
.adm-card { background: var(--card); border: 1px solid var(--border); border-radius: 14px; padding: 16px 18px; display: flex; flex-direction: column; gap: 6px; font-size: 12.5px; color: var(--muted, #8a94a6); position: relative; }
.adm-card > b { font-size: 13px; color: var(--text, inherit); }
.adm-card strong.num { font-size: 26px; font-weight: 800; color: var(--text, inherit); font-family: var(--mono, monospace); }
.adm-card strong.num .unit { font-size: 12px; font-weight: 400; font-style: normal; color: var(--muted, #8a94a6); }
.adm-card.wide { grid-column: span 2; }
.adm-card.bad { border-color: #f87171; }
.adm-card .hl, .hl { color: var(--accent, #0b6cff); }
.adm-card-foot { font-size: 11.5px; display: flex; gap: 10px; flex-wrap: wrap; }
.adm-quota-bar { height: 10px; border-radius: 5px; background: var(--card2, rgba(128,140,160,.15)); overflow: hidden; margin-top: 4px; }
.adm-quota-bar span { display: block; height: 100%; border-radius: 5px; background: linear-gradient(90deg, var(--aqua, #22d3ee), #818cf8); transition: width .4s; }
.adm-quota-nums { display: flex; gap: 12px; font-size: 12px; flex-wrap: wrap; }

/* 额度监管 */
.adm-supban { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; font-size: 12.5px; border-radius: 10px; padding: 9px 13px; margin-bottom: 12px; border: 1px solid; }
.adm-supban.warn { background: rgba(245, 158, 11, .1); border-color: rgba(245, 158, 11, .5); color: #f59e0b; }
.adm-supban.danger { background: rgba(249, 115, 22, .1); border-color: rgba(249, 115, 22, .5); color: #fb923c; }
.adm-supban.critical { background: rgba(248, 113, 113, .1); border-color: rgba(248, 113, 113, .55); color: #f87171; }
.adm-supban b { font-weight: 800; }
.adm-supban span { color: var(--text, inherit); opacity: .85; }
.adm-supban .mini-btn { margin-left: auto; }

.adm-supverdict { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; border-radius: 12px; padding: 13px 16px; font-size: 13px; margin-bottom: 14px; border: 1px solid var(--border); background: var(--card); }
.adm-supverdict.normal { border-color: rgba(52, 211, 153, .4); }
.adm-supverdict.warn { border-color: rgba(245, 158, 11, .5); }
.adm-supverdict.danger { border-color: rgba(249, 115, 22, .5); }
.adm-supverdict.critical { border-color: rgba(248, 113, 113, .6); }
.adm-supverdict .ok-txt { color: #34d399; }
.adm-supverdict .bad-txt { color: #f87171; }

.adm-supth { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; font-size: 12px; color: var(--muted, #8a94a6); margin-bottom: 14px; }
.adm-supth input { width: 56px; padding: 5px 8px; border-radius: 8px; border: 1px solid var(--border, rgba(128, 140, 160, .3)); background: transparent; color: inherit; text-align: center; font-size: 12px; }
.sup-sum { font-weight: 400; font-size: 11.5px; color: var(--muted, #8a94a6); margin-left: 10px; }

.adm-grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; align-items: start; }

.adm-table { width: 100%; border-collapse: collapse; font-size: 12.5px; }
.adm-table th { text-align: left; font-weight: 600; color: var(--muted, #8a94a6); padding: 8px 10px; border-bottom: 1px solid var(--border, rgba(128,140,160,.25)); white-space: nowrap; }
.adm-table td { padding: 8px 10px; border-bottom: 1px solid var(--border, rgba(128,140,160,.12)); white-space: nowrap; }
.adm-table tbody tr:last-child td { border-bottom: none; }
.adm-table th.num, .adm-table td.num { text-align: right; font-variant-numeric: tabular-nums; font-family: var(--mono, monospace); }
.adm-table td.num.ok { color: #34d399; }
.adm-table td.num.bad { color: #f87171; }
.adm-table .nm { font-weight: 600; }
.adm-table .em, .adm-table .tm { color: var(--muted, #8a94a6); }
.adm-table .dt { max-width: 260px; overflow: hidden; text-overflow: ellipsis; }
.adm-table .hs { font-family: var(--mono, monospace); font-size: 11px; color: var(--muted, #8a94a6); }
.adm-table .ops { display: flex; gap: 6px; }
.adm-empty { text-align: center; color: var(--muted, #8a94a6); padding: 18px 10px !important; }

.adm-search { display: flex; gap: 8px; margin-bottom: 14px; }
.adm-search .ppill { padding: 5px 14px; border-radius: 999px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; font-size: 12.5px; cursor: pointer; }
.adm-search .ppill.active { background: var(--accent, #0b6cff); border-color: var(--accent, #0b6cff); color: #fff; }
.adm-search input { flex: 1; max-width: 380px; padding: 10px 14px; border-radius: 10px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; }
.adm-search input:focus { outline: none; border-color: var(--accent, #0b6cff); }

.adm-pager { display: flex; align-items: center; gap: 10px; margin-top: 10px; font-size: 12px; color: var(--muted, #8a94a6); }
.adm-fails { display: flex; gap: 6px; margin-top: 10px; flex-wrap: wrap; }
.adm-tag { display: inline-block; font-size: 11px; padding: 2px 8px; border-radius: 999px; background: rgba(128,140,160,.15); color: var(--muted, #8a94a6); }
.adm-tag.ok { background: rgba(52,211,153,.15); color: #34d399; }
.adm-tag.bad { background: rgba(248,113,113,.15); color: #f87171; }
.adm-tag.warn { background: rgba(251,191,36,.15); color: #fbbf24; }
.adm-notes { margin: 6px 0 0; padding-left: 18px; font-size: 11.5px; color: var(--muted, #8a94a6); line-height: 1.7; }
.adm-notes li { word-break: break-all; }
.dim { color: var(--muted, #8a94a6); font-size: 11.5px; }

.adm-kv { display: grid; grid-template-columns: auto 1fr; gap: 8px 18px; font-size: 13px; align-items: center; }
.adm-kv > span { color: var(--muted, #8a94a6); }
.adm-kv > b { font-family: var(--mono, monospace); }
.adm-hint { font-size: 11.5px; color: var(--muted, #8a94a6); margin-top: 6px; }
.adm-hint.ok { color: #34d399; }

.adm-form { display: flex; flex-direction: column; gap: 12px; margin-top: 8px; max-width: 420px; }
.adm-form label { display: flex; flex-direction: column; gap: 5px; font-size: 12.5px; color: var(--muted, #8a94a6); }
.adm-form input { padding: 10px 12px; border-radius: 10px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; font-size: 14px; }
.adm-form input:focus { outline: none; border-color: var(--accent, #0b6cff); }

.adm-msg { font-size: 12.5px; margin: 8px 0; }
.adm-msg.bad { color: #f87171; }
.adm-msg.ok { color: #34d399; }
.adm-loading { display: flex; align-items: center; gap: 10px; font-size: 12.5px; color: var(--muted, #8a94a6); background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 14px 16px; margin: 8px 0; }
.adm-loading .spin { width: 16px; height: 16px; flex: none; border: 2.5px solid var(--border, rgba(128,140,160,.3)); border-top-color: var(--accent, #0b6cff); border-radius: 50%; animation: adm-spin .8s linear infinite; }
@keyframes adm-spin { to { transform: rotate(360deg); } }

/* 弹窗 */
.adm-mask { position: fixed; inset: 0; background: rgba(0,0,0,.55); z-index: 200; display: flex; align-items: center; justify-content: center; padding: 20px; }
.adm-modal { width: 400px; max-height: 86vh; overflow-y: auto; background: var(--bg, #fff); border: 1px solid var(--border); border-radius: 16px; padding: 22px 24px; display: flex; flex-direction: column; gap: 12px; }
.adm-modal.wide { width: 720px; }
.adm-modal h3 { font-size: 16px; }
.adm-modal-user { font-size: 12.5px; color: var(--muted, #8a94a6); }
.adm-modal label { display: flex; flex-direction: column; gap: 5px; font-size: 12.5px; color: var(--muted, #8a94a6); }
.adm-modal input { padding: 10px 12px; border-radius: 10px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; font-size: 14px; }
.adm-modal input:focus { outline: none; border-color: var(--accent, #0b6cff); }
.adm-modal-ops { display: flex; gap: 10px; justify-content: flex-end; margin-top: 4px; }

.mini-btn { display: inline-flex; align-items: center; gap: 4px; font-size: 11.5px; padding: 4px 10px; border-radius: 8px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; cursor: pointer; }
.mini-btn.ok { border-color: #34d399; color: #34d399; }
.mini-btn.danger { border-color: #f87171; color: #f87171; }

@media (max-width: 960px) {
  .adm-cards { grid-template-columns: repeat(2, 1fr); }
  .adm-card.wide { grid-column: span 2; }
  .adm-grid2 { grid-template-columns: 1fr; }
  .adm-table .em, .adm-table .tm { display: none; }
}
@media (max-width: 640px) {
  .adm-cards { grid-template-columns: 1fr; }
  .adm-card.wide { grid-column: span 1; }
  .adm-head h1 { font-size: 18px; }
}
</style>
