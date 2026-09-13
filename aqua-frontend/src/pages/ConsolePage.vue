<script setup lang="ts">
/* 个人控制台：侧边栏式真控制台（总览 / 密钥 / 用量 / 历史 / 设置） */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { apiJson, copyText, errText, fmt } from '@/composables/useApi'
import {
  avatarUrl, changeKeyGroup, changePassword, createKey, fetchBalanceAlert, fetchCheckup, isLoggedIn, listKeys, loadMe, logout,
  me, revealKey, revokeKey, setBalanceAlert, uploadAvatar, type BillingGrp, type Checkup, type KeyItem,
} from '@/composables/useAuth'
import AqIcon from '@/components/AqIcon.vue'

const router = useRouter()

/* ===== 守卫：未登录跳 /login ===== */
onMounted(async () => {
  if (!isLoggedIn()) { router.replace('/login'); return }
  await loadMe()
  await Promise.all([loadKeys(), loadUsage(), loadCheckup(), loadHistory(), loadBalance(), loadBilling(), loadPayOrders(), loadBalanceAlert()])
})

/* ===== 视图切换（侧边栏） ===== */
type View = 'dashboard' | 'keys' | 'usage' | 'history' | 'billing' | 'topup' | 'settings'
const NAV: { id: View; label: string; icon: string }[] = [
  { id: 'dashboard', label: '总览', icon: 'layout' },
  { id: 'keys', label: 'API 密钥', icon: 'key' },
  { id: 'usage', label: '我的用量', icon: 'chart' },
  { id: 'history', label: '请求历史', icon: 'clock' },
  { id: 'billing', label: '消费账单', icon: 'bolt' },
  { id: 'topup', label: '余额充值', icon: 'spark' },
  { id: 'settings', label: '账号设置', icon: 'settings' },
]
const VIEWS: View[] = ['dashboard', 'keys', 'usage', 'history', 'billing', 'topup', 'settings']

function savedView(): View {
  const v = localStorage.getItem('aqua_console_view')
  return VIEWS.includes(v as View) ? (v as View) : 'dashboard'
}
const view = ref<View>(savedView())
function go(v: View) {
  view.value = v
  try { localStorage.setItem('aqua_console_view', v) } catch { /* 忽略 */ }
  window.scrollTo({ top: 0 })
}

/* 侧边栏折叠（记忆） */
const collapsed = ref(localStorage.getItem('aqua_console_side') === '1')
function toggleSide() {
  collapsed.value = !collapsed.value
  try { localStorage.setItem('aqua_console_side', collapsed.value ? '1' : '0') } catch { /* 忽略 */ }
}

const activeKeys = computed(() => keys.value.filter(k => !k.revoked).length)
const recentItems = computed(() => historyItems.value.slice(0, 5))

/* ===== 资料 ===== */
const fileInput = ref<HTMLInputElement | null>(null)
const avatarBust = ref(Date.now())
const profileMsg = ref('')
const profileOk = ref(false)
function note(t: string, ok = false) { profileMsg.value = t; profileOk.value = ok }

const nameEdit = ref(false)
const nameDraft = ref('')

async function saveName() {
  if (!nameDraft.value.trim()) return
  try {
    await apiJson('/auth/profile', { method: 'POST', session: true, body: { username: nameDraft.value.trim() } })
    note('用户名已更新', true)
    nameEdit.value = false
    await loadMe()
    loadCheckup()
  } catch (e) { note(errText(e)) }
}

async function onPickAvatar(e: Event) {
  const f = (e.target as HTMLInputElement).files?.[0]
  if (!f) return
  if (f.size > 2 * 1024 * 1024) { note('头像不能超过 2MB'); return }
  try {
    await uploadAvatar(f)
    avatarBust.value = Date.now()
    note('头像已更新', true)
    await loadMe()
    loadCheckup()
  } catch (e: any) { note(errText(e)) }
  finally { if (fileInput.value) fileInput.value.value = '' }
}

/* ===== 账号检查 ===== */
const checkup = ref<Checkup | null>(null)
const checkupMsg = ref('')
const checking = ref(false)

async function loadCheckup() {
  checking.value = true; checkupMsg.value = ''
  try { checkup.value = await fetchCheckup() } catch (e) { checkupMsg.value = errText(e) }
  checking.value = false
}

const scoreColor = (s: number) => (s >= 80 ? '#34d399' : s >= 50 ? '#fbbf24' : '#f87171')
// 圆环参数：半径 26，周长 163.4
const CIRC = 2 * Math.PI * 26
const dashoffset = (s: number) => CIRC * (1 - s / 100)

/* ===== 密钥 ===== */
const keys = ref<KeyItem[]>([])
const newKeyName = ref('')
const newKeyGrp = ref<BillingGrp>('per_token') // 创建分组，默认按量（按次线临时下架）
const creating = ref(false)
const freshKey = ref('') // 仅创建后展示一次
const keysMsg = ref('')
const copiedId = ref(0) // 刚复制成功的密钥 id（按钮短暂反馈）
const mkOpen = ref(false) // 创建密钥弹窗
const mkDone = ref(false) // 弹窗成功态（展示密钥原文）
const freshCopied = ref(false) // 弹窗内复制反馈

/** 打开创建弹窗（清空上次表单） */
function openMk() {
  newKeyName.value = ''
  newKeyGrp.value = 'per_token'
  mkDone.value = false
  freshCopied.value = false
  keysMsg.value = ''
  mkOpen.value = true
}

/** 关闭创建弹窗（成功后清空密钥原文防残留） */
function closeMk() {
  mkOpen.value = false
  mkDone.value = false
  freshKey.value = ''
  freshCopied.value = false
}

/** 分组显示名（成功态提示用） */
function grpLabel(g: BillingGrp) {
  return g === 'per_call' ? '按次计费' : g === 'per_token' ? '按量计费' : g === 'official' ? '官方中转' : g === 'free' ? '纯免费' : '未分组'
}

async function loadKeys() {
  try { keys.value = await listKeys() } catch (e) { keysMsg.value = errText(e) }
}

async function doCreateKey() {
  const name = newKeyName.value.trim()
  if (!name) return
  creating.value = true; keysMsg.value = ''
  try {
    const j = await createKey(name, newKeyGrp.value)
    freshKey.value = j.key
    newKeyName.value = ''
    mkDone.value = true // 弹窗切成功态：展示密钥原文（仅此一次）
    await loadKeys()
    loadCheckup()
  } catch (e) { keysMsg.value = errText(e) }
  creating.value = false
}

/** 随时复制：点击列表密钥的复制按钮 → 向后端取解密原文 → 写剪贴板 */
async function copyKey(k: KeyItem) {
  keysMsg.value = ''
  try {
    const plain = await revealKey(k.id)
    if (await copyText(plain)) {
      copiedId.value = k.id
      setTimeout(() => { if (copiedId.value === k.id) copiedId.value = 0 }, 1500)
    } else {
      keysMsg.value = '复制失败：浏览器剪贴板不可用，请手动保存'
    }
  } catch (e) { keysMsg.value = errText(e) }
}

async function doRevoke(k: KeyItem) {
  if (!confirm(`确认吊销密钥「${k.name}」？使用它的程序会立即 401。`)) return
  try { await revokeKey(k.id); await loadKeys(); loadCheckup() } catch (e) { keysMsg.value = errText(e) }
}

/* ---- 密钥切换计费分组（立即生效，无需重建） ---- */
const grpEdit = ref({ open: false, id: 0, name: '', prefix: '', grp: '' as BillingGrp })
const grpSaving = ref(false)
function openGrpEdit(k: KeyItem) {
  grpEdit.value = { open: true, id: k.id, name: k.name, prefix: k.prefix, grp: (k.billing_grp || '') as BillingGrp }
}
async function doChangeGrp() {
  grpSaving.value = true
  try {
    await changeKeyGroup(grpEdit.value.id, grpEdit.value.grp)
    grpEdit.value.open = false
    await loadKeys()
    note('计费分组已更新，立即生效', true)
  } catch (e) { keysMsg.value = errText(e) }
  grpSaving.value = false
}

function setDefaultKey() {
  if (!freshKey.value) return
  try { localStorage.setItem('aqua_default_key', freshKey.value); note('已设为站内功能默认密钥（本浏览器生效）', true) } catch { /* 忽略 */ }
}

async function copyFresh() {
  if (await copyText(freshKey.value)) {
    freshCopied.value = true
    setTimeout(() => { freshCopied.value = false }, 1500)
    note('密钥已复制到剪贴板', true)
  }
}

/* ===== 用量 ===== */
interface ModelRow { model: string; width: number; val: string }
const cards = ref({ today: '--', todayRate: '--', week: '--', weekRate: '--' })
const modelRows = ref<ModelRow[]>([])
const usageMsg = ref('')

async function loadUsage() {
  try {
    const j = await apiJson<any>('/my/usage', { session: true })
    cards.value = {
      today: fmt(j.today.calls), todayRate: j.today.ok_rate + '%',
      week: fmt(j.week.calls), weekRate: j.week.ok_rate + '%',
    }
    const byModel = j.by_model || []
    const maxc = byModel.length ? (byModel[0].calls || 1) : 1
    modelRows.value = byModel.map((m: any) => ({ model: m.model, width: Math.max(4, Math.round((m.calls * 100) / maxc)), val: fmt(m.calls) + ' 次' }))
  } catch (e) { usageMsg.value = errText(e) }
  loadMyPool()
}

/* ===== 我的众筹池（acu/ 前缀模型公共池明细） ===== */
const myPool = ref<any>({})
function poolYuan(v?: number): string {
  if (v == null) return '--'
  return (v / 1e6).toFixed(2).replace(/\.00$/, '').replace(/(\.\d)0$/, '$1')
}
function fmtPoolTs(t: number): string {
  const d = new Date((t || 0) * 1000), p = (x: number) => (x < 10 ? '0' : '') + x
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
async function loadMyPool() {
  try { myPool.value = await apiJson<any>('/my/pool/flows', { session: true }) } catch { /* 忽略 */ }
}

/* ===== 请求历史明细（分页，全字段） ===== */
interface HistoryItem {
  endpoint: string; model: string; ok: boolean
  prompt_tokens: number; completion_tokens: number; cached_tokens: number; total_tokens: number
  tps: number; latency_ms: number; status_code: number
  error: string; usage_source: string; ts: number
  billed?: boolean; bill_amount_micro?: number; balance_after_micro?: number
  bill_state?: string; stream_mode?: string
}
const historyItems = ref<HistoryItem[]>([])
const historyPage = ref(1)
const historyPageSize = ref(20)
const historyTotal = ref(0)
const historyLoading = ref(false)
const historyMsg = ref('')

const historyPages = computed(() => Math.max(1, Math.ceil(historyTotal.value / historyPageSize.value)))

async function loadHistory() {
  historyLoading.value = true; historyMsg.value = ''
  try {
    const q = `page=${historyPage.value}&page_size=${historyPageSize.value}`
    const j = await apiJson<any>(`/my/history?${q}`, { session: true })
    historyItems.value = j.items || []
    historyTotal.value = j.total || 0
    if (historyPage.value > historyPages.value) { historyPage.value = historyPages.value }
  } catch (e) { historyMsg.value = errText(e) }
  historyLoading.value = false
}

function goHistoryPage(p: number) {
  if (p < 1 || p > historyPages.value || p === historyPage.value) return
  historyPage.value = p
  loadHistory()
}

const usageSourceLabel: Record<string, string> = {
  upstream: '上游实测', estimated: '估算', stream_no_usage: '估算（流式未报用量）',
  stream_request_failed: '流式请求失败',
}
const sourceLabel = (s: string) => usageSourceLabel[s] || s || '—'

function fmtTokens(n: number): string { return fmt(n || 0) }
function fmtTps(n: number): string { return n > 0 ? (Math.round(n * 100) / 100).toFixed(2) : '—' }

/* ===== 余额 / 消费账单（收费模型，预充值制） ===== */
const balance = ref<{ balance_micro: number; today_cost_micro: number; total_cost_micro: number; price_micro: number | null; promo_ends_at: number; promo_active: boolean } | null>(null)
const balanceMsg = ref('')
const yuan = (micro: number | null | undefined) => (micro == null ? '—' : (micro / 1_000_000).toFixed(3))

async function loadBalance() {
  try { balance.value = await apiJson<any>('/my/balance', { session: true }) } catch (e) { balanceMsg.value = errText(e) }
}

/* ===== 余额不足邮件提醒（阈值自设；充值回到阈值上方自动重新生效） ===== */
const alertThreshold = ref(0)   // 微元；0 = 未开启
const alertYuan = ref('')       // 输入框（元）
const alertMsg = ref('')
const alertOk = ref(false)
const alertSaving = ref(false)

async function loadBalanceAlert() {
  try {
    const j = await fetchBalanceAlert()
    alertThreshold.value = j?.threshold_micro || 0
    if (j?.threshold_micro) alertYuan.value = (j.threshold_micro / 1_000_000).toFixed(2)
  } catch { /* 静默：设置面板会显示未开启 */ }
}

async function saveBalanceAlert(micro: number) {
  alertSaving.value = true; alertMsg.value = ''
  try {
    const msg = await setBalanceAlert(micro)
    alertThreshold.value = micro
    alertOk.value = micro > 0
    alertMsg.value = msg
  } catch (e) { alertOk.value = false; alertMsg.value = errText(e) }
  alertSaving.value = false
}

function doSaveAlert() {
  const y = Number(alertYuan.value)
  if (!(y >= 0 && y <= 100)) { alertOk.value = false; alertMsg.value = '阈值需在 0~100 元之间'; return }
  saveBalanceAlert(Math.round(y * 1_000_000))
}

function disableAlert() { alertYuan.value = ''; saveBalanceAlert(0) }

interface BillItem {
  type: string; amount_micro: number; balance_after_micro: number
  unit_price_micro: number; note: string; operator: string; ts: number; model: string
}
const billItems = ref<BillItem[]>([])
const billPage = ref(1)
const billTotal = ref(0)
const billLoading = ref(false)
const billMsg = ref('')
const billPages = computed(() => Math.max(1, Math.ceil(billTotal.value / 20)))

async function loadBilling() {
  billLoading.value = true; billMsg.value = ''
  try {
    const j = await apiJson<any>(`/my/billing?page=${billPage.value}&page_size=20`, { session: true })
    billItems.value = j.items || []
    billTotal.value = j.total || 0
    if (billPage.value > billPages.value) { billPage.value = billPages.value }
  } catch (e) { billMsg.value = errText(e) }
  billLoading.value = false
}

function goBillPage(p: number) {
  if (p < 1 || p > billPages.value || p === billPage.value) return
  billPage.value = p
  loadBilling()
}

const billTypeLabel: Record<string, string> = {
  prehold: '请求预扣', billed: '计费确认', refunded: '失败退回', topup: '站长发放', deduct: '站长扣减',
}

/* ===== 在线充值（易支付：支付宝 / 微信） ===== */
const topupAmt = ref('')
const topupChannel = ref<'alipay' | 'wxpay'>('alipay')
const TOPUP_PRESETS = [1, 5, 10, 50, 100]
const topupMsg = ref('')
const topupOk = ref(false)
const paying = ref(false)
const payingOrder = ref<{ out_trade_no: string; amount_micro: number; credit_micro: number } | null>(null)
let pollTimer: number | null = null

/* 渠道手续费由本站承担：用户按支付金额全额入账（1:1），与后端逻辑一致 */
function topupCreditOf(micro: number, _channel: 'alipay' | 'wxpay'): number {
  return micro
}
const topupCreditPreview = computed(() => {
  const micro = Math.round((Number(topupAmt.value) || 0) * 1_000_000)
  return micro >= 10_000 ? topupCreditOf(micro, topupChannel.value) : 0
})

function setTopupMsg(t: string, ok = false) { topupMsg.value = t; topupOk.value = ok }

async function createTopup() {
  const micro = Math.round((Number(topupAmt.value) || 0) * 1_000_000)
  if (micro < 10_000) { setTopupMsg('最低充值 ¥0.01'); return }
  paying.value = true; setTopupMsg('')
  try {
    const j = await apiJson<any>('/pay/create', {
      method: 'POST', session: true,
      body: { amount_micro: micro, channel: topupChannel.value },
    })
    payingOrder.value = { out_trade_no: j.out_trade_no, amount_micro: micro, credit_micro: topupCreditOf(micro, topupChannel.value) }
    window.open(j.pay_url, '_blank')
    startPolling()
  } catch (e) { setTopupMsg(errText(e)) }
  paying.value = false
}

function startPolling() {
  stopPolling()
  pollTimer = window.setInterval(async () => {
    if (!payingOrder.value) { stopPolling(); return }
    try {
      const j = await apiJson<any>(`/pay/status?out_trade_no=${payingOrder.value.out_trade_no}`, { session: true })
      if (j.status === 'paid') {
        setTopupMsg(`充值成功：支付 ¥${yuan(j.amount_micro)}，到账 ¥${yuan(j.credit_micro ?? topupCreditOf(j.amount_micro, j.channel))}，当前余额 ¥${yuan(j.balance_micro)}`, true)
        stopPolling()
        payingOrder.value = null
        loadBalance(); loadBilling(); loadPayOrders()
      }
    } catch { /* 轮询失败忽略，下轮再试 */ }
  }, 3000)
}
function stopPolling() { if (pollTimer != null) { clearInterval(pollTimer); pollTimer = null } }
async function manualCheck() {
  if (!payingOrder.value) return
  try {
    const j = await apiJson<any>(`/pay/status?out_trade_no=${payingOrder.value.out_trade_no}`, { session: true })
    if (j.status === 'paid') {
      setTopupMsg(`充值成功：支付 ¥${yuan(j.amount_micro)}，到账 ¥${yuan(j.credit_micro ?? topupCreditOf(j.amount_micro, j.channel))}，当前余额 ¥${yuan(j.balance_micro)}`, true)
      stopPolling(); payingOrder.value = null
      loadBalance(); loadBilling(); loadPayOrders()
    } else { setTopupMsg('还未查询到支付结果，完成支付后稍等几秒') }
  } catch (e) { setTopupMsg(errText(e)) }
}
onUnmounted(stopPolling)

/* 充值订单记录 */
const payOrders = ref<any[]>([])
async function loadPayOrders() {
  try { const j = await apiJson<any>('/pay/orders', { session: true }); payOrders.value = j.items || [] } catch { /* 忽略 */ }
}
const channelLabel: Record<string, string> = { alipay: '支付宝', wxpay: '微信' }
const streamModeLabel: Record<string, string> = {
  sim_stream: '模拟流式', passthrough: '流式透传', nonstream: '非流式',
}
const streamModeShort: Record<string, string> = {
  sim_stream: '模拟流', passthrough: '透传', nonstream: '非流',
}

/* ===== 改密码 ===== */
const oldPw = ref('')
const newPw = ref('')
const pwMsg = ref('')
const pwOk = ref(false)

async function doChangePw() {
  pwMsg.value = ''; pwOk.value = false
  try {
    const msg = await changePassword(oldPw.value, newPw.value)
    pwMsg.value = msg; pwOk.value = true
    // 后端已注销全部会话 → 跳登录
    setTimeout(() => router.replace('/login'), 1500)
  } catch (e) { pwMsg.value = errText(e) }
}

async function doLogout() {
  await logout()
  router.replace('/login')
}

function fmtTime(ts: number): string {
  return ts ? new Date(ts * 1000).toLocaleString() : '—'
}
</script>

<template>
  <section class="route-page console-page">
    <div class="dash-head">
      <h1><span class="ic"><AqIcon name="user" :size="18" /></span>个人控制台</h1>
      <p>管理你的账号、API 密钥、用量与全部请求明细</p>
    </div>

    <div class="console-shell">
      <!-- ===== 左侧边栏 ===== -->
      <aside class="cside" :class="{ mini: collapsed }">
        <div class="cside-user">
          <div class="cside-avatar">
            <img v-if="me?.avatar_ext" :src="avatarUrl() + '?t=' + avatarBust" alt="" />
            <span v-else>{{ (me?.username || '?').slice(0, 1) }}</span>
          </div>
          <div v-if="!collapsed" class="cside-who">
            <b>{{ me?.username || '…' }}</b>
            <span>UID #{{ me?.id }}</span>
          </div>
        </div>

        <nav class="cnav">
          <button
            v-for="n in NAV" :key="n.id" class="cnav-item" :class="{ on: view === n.id }"
            :title="n.label" @click="go(n.id)"
          >
            <AqIcon :name="n.icon" :size="17" />
            <span v-if="!collapsed" class="cnav-label">{{ n.label }}</span>
            <i v-if="n.id === 'keys' && !collapsed && activeKeys" class="cnav-badge">{{ activeKeys }}</i>
          </button>
          <button class="cnav-item" title="财务管理中心" @click="router.push('/finance')">
            <AqIcon name="coin" :size="17" />
            <span v-if="!collapsed" class="cnav-label">财务管理中心</span>
          </button>
        </nav>

        <div v-if="!collapsed" class="cside-foot">
          <button class="cnav-item foot-out" @click="doLogout">
            <AqIcon name="arrow-right" :size="16" />
            <span class="cnav-label">退出登录</span>
          </button>
        </div>
        <button class="cside-fold" :title="collapsed ? '展开侧栏' : '收起侧栏'" @click="toggleSide">
          <AqIcon :name="collapsed ? 'arrow-right' : 'menu'" :size="15" />
        </button>
      </aside>

      <!-- ===== 右主区 ===== -->
      <main class="cmain">

        <!-- ▼▼▼ 总览 ▼▼▼ -->
        <div v-show="view === 'dashboard'" class="view">
          <!-- HUD 驾驶舱：欢迎 + 余额 + 快捷操作（科技感主视觉，v5.5） -->
          <div class="hud">
            <i class="hud-grid" aria-hidden="true"></i>
            <i class="hud-glow g1" aria-hidden="true"></i>
            <i class="hud-glow g2" aria-hidden="true"></i>
            <div class="hud-top">
              <div class="avatar-box" @click="fileInput?.click()" title="点击更换头像">
                <img v-if="me?.avatar_ext" :src="avatarUrl() + '?t=' + avatarBust" alt="头像" />
                <span v-else class="avatar-fallback">{{ (me?.username || '?').slice(0, 1) }}</span>
                <span class="avatar-edit">更换</span>
              </div>
              <input ref="fileInput" type="file" accept="image/png,image/jpeg,image/webp" style="display:none" @change="onPickAvatar" />
              <div class="ov-hero-txt">
                <b>{{ me?.username || '…' }}<span class="ov-hi">，欢迎回来</span></b>
                <span>{{ me?.email }} · UID #{{ me?.id }} · 注册于 {{ me ? fmtTime(me.created_ts) : '…' }}</span>
              </div>
              <button class="mini-btn danger ov-logout" @click="doLogout">退出登录</button>
            </div>
            <p v-if="profileMsg" class="hint-line" :class="{ ok: profileOk }">{{ profileMsg }}</p>
            <div class="hud-main">
              <div class="hud-bal">
                <span class="hud-label">当前余额 · 仅用于收费模型 aqua/（按量计费）专线 · 预充值、先付后用、用完即停</span>
                <b class="hud-num">¥{{ yuan(balance?.balance_micro) }}</b>
                <span class="hud-sub">今日消费 <b>¥{{ yuan(balance?.today_cost_micro) }}</b> · 累计消费 <b>¥{{ yuan(balance?.total_cost_micro) }}</b> · 失败请求自动全额退回 · 每笔流水永久可查</span>
              </div>
              <div class="hud-ops">
                <button class="btn tool-run" @click="go('topup')"><AqIcon name="spark" :size="14" /> 立即充值</button>
                <button class="mini-btn" @click="go('billing')"><AqIcon name="bolt" :size="12" /> 消费账单</button>
              </div>
            </div>
            <div class="hud-strip">
              <span>余额只影响收费模型 <b>aqua/ 收费专线</b>（输入 / 缓存命中 / 输出按 tokens 三段精算），其余全部模型完全免费，无余额照样用</span>
              <span>在线充值支付金额 <b>100% 全额到账</b>（渠道手续费由本站承担）</span>
            </div>
          </div>

          <!-- 用量统计 -->
          <div class="dash-cards">
            <div class="dash-card"><b>{{ cards.today }}</b><span>今日调用</span></div>
            <div class="dash-card"><b>{{ cards.todayRate }}</b><span>今日成功率</span></div>
            <div class="dash-card"><b>{{ cards.week }}</b><span>近 7 天调用</span></div>
            <div class="dash-card"><b>{{ cards.weekRate }}</b><span>近 7 天成功率</span></div>
          </div>

          <!-- 健康分 + 密钥概况 -->
          <div class="ov-grid">
            <div class="dash-sec">
              <b><AqIcon name="stethoscope" :size="16" /> 账号健康</b>
              <div v-if="checkup" class="ov-health">
                <svg width="72" height="72" viewBox="0 0 72 72">
                  <circle cx="36" cy="36" r="26" fill="none" stroke="rgba(128,140,160,.25)" stroke-width="7" />
                  <circle
                    cx="36" cy="36" r="26" fill="none" :stroke="scoreColor(checkup.score)" stroke-width="7"
                    stroke-linecap="round" :stroke-dasharray="CIRC" :stroke-dashoffset="dashoffset(checkup.score)"
                    transform="rotate(-90 36 36)"
                  />
                  <text x="36" y="41" text-anchor="middle" :fill="scoreColor(checkup.score)" font-size="18" font-weight="700">{{ checkup.score }}</text>
                </svg>
                <div class="ov-health-txt">
                  <b>健康分 {{ checkup.score }}<span class="sec-sub">（{{ checkup.items.filter(i => i.level === 'ok').length }}/{{ checkup.items.length }} 项通过）</span></b>
                  <span class="ov-health-advice">{{ checkup.items.find(i => i.level !== 'ok')?.advice || '一切正常，继续保持' }}</span>
                </div>
              </div>
              <div v-else class="dash-empty">{{ checking ? '正在检查…' : (checkupMsg || '加载中…') }}</div>
              <div class="ov-sec-ops">
                <button class="mini-btn" :disabled="checking" @click="loadCheckup">
                  <AqIcon name="refresh" :size="12" /> {{ checking ? '检查中…' : '重新检查' }}
                </button>
                <button class="mini-btn" @click="go('settings')">查看详情</button>
              </div>
            </div>

            <div class="dash-sec">
              <b><AqIcon name="key" :size="16" /> 密钥概况</b>
              <div class="ov-keys">
                <div class="ov-keys-num"><b>{{ activeKeys }}</b><span>把有效密钥</span></div>
                <div class="ov-keys-list">
                  <div v-if="!keys.length" class="dash-empty">还没有密钥</div>
                  <div v-for="k in keys.filter(x => !x.revoked).slice(0, 3)" :key="k.id" class="ov-key-row">
                    <AqIcon name="key" :size="13" />
                    <span class="nm">{{ k.name }}</span>
                    <code>{{ k.prefix }}</code>
                  </div>
                </div>
              </div>
              <div class="ov-sec-ops">
                <button class="mini-btn ok" @click="go('keys')"><AqIcon name="key" :size="12" /> 管理密钥</button>
              </div>
            </div>
          </div>

          <!-- 最近请求 -->
          <div class="dash-sec">
            <b><AqIcon name="clock" :size="16" /> 最近请求 <span class="sec-sub">（最新 {{ recentItems.length }} 条，共 {{ fmt(historyTotal) }} 条）</span></b>
            <div class="hist-scroll">
              <table class="hist-table">
                <thead>
                  <tr><th>时间</th><th>模型</th><th>状态</th><th class="num">Tokens</th><th class="num">延迟</th></tr>
                </thead>
                <tbody>
                  <tr v-if="!recentItems.length">
                    <td colspan="5" class="hist-empty">{{ historyLoading ? '加载中…' : '暂无请求记录——拿你的密钥去 Playground 聊一句' }}</td>
                  </tr>
                  <tr v-for="(r, i) in recentItems" :key="r.ts + '-' + i" :class="{ failed: !r.ok }">
                    <td class="hist-time">{{ fmtTime(r.ts) }}</td>
                    <td><span class="hist-model" :title="r.model">{{ r.model || '—' }}</span></td>
                    <td><span class="hist-badge" :class="r.ok ? 'ok' : 'bad'">{{ r.status_code }}</span></td>
                    <td class="num">{{ fmtTokens(r.total_tokens) }}</td>
                    <td class="num">{{ fmt(r.latency_ms) }} ms</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="ov-sec-ops">
              <button class="mini-btn" @click="go('history')"><AqIcon name="list" :size="12" /> 查看全部明细</button>
            </div>
          </div>
        </div>

        <!-- ▼▼▼ API 密钥 ▼▼▼ -->
        <div v-show="view === 'keys'" class="view">
          <div class="vhead">
            <span class="vic"><AqIcon name="key" :size="17" /></span>
            <div><h2>API 密钥</h2><p>调用 https://api.ltzy.top/v1 全部端点；支持随时复制查看</p></div>
          </div>
          <div class="dash-sec">
            <div class="keys-head">
              <p class="keys-head-txt">一把密钥即可调用全部端点；分组决定收费模型的计费方式，随时可切换。</p>
              <button class="btn tool-run keys-cta" @click="openMk"><AqIcon name="plus" :size="14" /> 创建密钥</button>
            </div>
            <p class="hint-line neutral">按量分组：按 tokens 三段精算，模型最全；纯免费密钥只可调免费模型，绝不产生扣费；acu/ 前缀为<router-link to="/pool">众筹公共模型</router-link>——任何分组密钥都可调用，按<b>官方原价</b>从<router-link to="/pool">站点额度</router-link>扣费（充 1 元 = 2 元额度），个人余额不受影响。</p>
            <div class="key-list">
              <div v-if="!keys.length" class="dash-empty">还没有密钥，创建一把开始调用</div>
              <div v-for="(k, ki) in keys" :key="k.id" class="key-card" :class="{ revoked: k.revoked }" :style="{ '--i': ki }">
                <div class="kc-main">
                  <div class="kc-title">
                    <span class="key-name">{{ k.name }}</span>
                    <span v-if="k.billing_grp === 'per_call'" class="key-grp call" title="按次计费线路已下架，调用收费模型将失败——建议切换为按量分组">按次</span>
                    <span v-else-if="k.billing_grp === 'per_token'" class="key-grp token" title="该密钥调用收费模型时按 tokens 三段计费">按量</span>
                    <span v-else-if="k.billing_grp === 'free'" class="key-grp free" title="纯免费分组：仅可调用免费模型，调收费模型直接拒绝">纯免费</span>
                    <span v-else-if="k.billing_grp === 'official'" class="key-grp official" title="官方中转线路已下架，调用收费模型将失败——建议切换为按量分组">官方中转</span>
                    <span v-else class="key-grp legacy" title="旧式密钥未选分组，收费模型按默认分组（按次）计费">未分组</span>
                    <span v-if="k.revoked" class="key-revoked">已吊销</span>
                  </div>
                  <code class="key-prefix">{{ k.prefix }}</code>
                </div>
                <div class="kc-side">
                  <span class="key-time">{{ fmtTime(k.created_ts) }}</span>
                  <div v-if="!k.revoked" class="kc-ops">
                    <button v-if="k.can_reveal !== false" class="mini-btn ok" @click="copyKey(k)">
                      <AqIcon :name="copiedId === k.id ? 'check' : 'copy'" :size="12" /> {{ copiedId === k.id ? '已复制' : '复制' }}
                    </button>
                    <span v-else class="key-legacy" title="旧版密钥未存原文，无法查看">旧密钥</span>
                    <button class="mini-btn" @click="openGrpEdit(k)">切换分组</button>
                    <button class="mini-btn danger" @click="doRevoke(k)">吊销</button>
                  </div>
                </div>
              </div>
            </div>
            <p v-if="keysMsg" class="hint-line">{{ keysMsg }}</p>
          </div>
        </div>

        <!-- 密钥切换分组弹窗 -->
        <div v-if="grpEdit.open" class="grp-mask" @click.self="grpEdit.open = false">
          <div class="grp-edit-modal">
            <h3>切换密钥计费分组</h3>
            <p class="hint-line neutral">密钥 <code>{{ grpEdit.prefix }}</code>（{{ grpEdit.name }}）——切换立即生效，无需重建密钥。</p>
            <div class="grp-pick grid">
              <button type="button" class="grp-opt" :class="{ on: grpEdit.grp === 'per_token' }" @click="grpEdit.grp = 'per_token'">
                <b>免费 + 按量计费<i class="mk-rec">推荐</i></b><span>按 tokens 三段精算 · 模型最全 · 当前全场一折</span>
              </button>
              <button type="button" class="grp-opt dim" :class="{ on: grpEdit.grp === 'per_call' }" @click="grpEdit.grp = 'per_call'">
                <b>免费 + 按次计费<i class="mk-dead">已下架</i></b><span>按次线路已并入众筹池 · 调收费模型将失败</span>
              </button>
              <button type="button" class="grp-opt official dim" :class="{ on: grpEdit.grp === 'official' }" @click="grpEdit.grp = 'official'">
                <b>官方中转 · 高速专线<i class="mk-dead">已下架</i></b><span>tlk/ 前缀线路已并入众筹池 · 调收费模型将失败</span>
              </button>
              <button type="button" class="grp-opt" :class="{ on: grpEdit.grp === 'free' }" @click="grpEdit.grp = 'free'">
                <b>纯免费</b><span>仅免费模型 · 绝不产生扣费</span>
              </button>
            </div>
            <div class="grp-edit-ops">
              <button class="mini-btn" @click="grpEdit.open = false">取消</button>
              <button class="btn tool-run" :disabled="grpSaving" @click="doChangeGrp">{{ grpSaving ? '保存中…' : '保存' }}</button>
            </div>
          </div>
        </div>

        <!-- 创建 API 密钥弹窗（表单态 → 成功态） -->
        <div v-if="mkOpen" class="grp-mask" @click.self="closeMk">
          <div class="grp-edit-modal mk-modal">
            <template v-if="!mkDone">
              <h3><AqIcon name="key" :size="16" /> 创建 API 密钥</h3>
              <p class="hint-line neutral">密钥原文仅在创建后展示一次，之后可随时在列表「复制」查看，请妥善保管。</p>
              <input v-model="newKeyName" class="mk-name" maxlength="32" placeholder="密钥备注，如：我的笔记本 / 生产环境" @keydown.enter="doCreateKey" />
              <p class="mk-label">选择计费分组 <span>仅影响收费模型 · 随时可切换</span></p>
              <div class="grp-pick grid">
                <button type="button" class="grp-opt" :class="{ on: newKeyGrp === 'per_token' }" @click="newKeyGrp = 'per_token'">
                  <b>免费 + 按量计费<i class="mk-rec">推荐</i></b><span>按 tokens 三段精算 · 模型最全 · 当前全场一折</span>
                </button>
                <button type="button" class="grp-opt" :class="{ on: newKeyGrp === 'free' }" @click="newKeyGrp = 'free'">
                  <b>纯免费</b><span>仅免费模型 · 绝不产生扣费</span>
                </button>
              </div>
              <p v-if="keysMsg" class="hint-line">{{ keysMsg }}</p>
              <div class="grp-edit-ops">
                <button class="mini-btn" @click="closeMk">取消</button>
                <button class="btn tool-run" :disabled="creating || !newKeyName.trim()" @click="doCreateKey">{{ creating ? '创建中…' : '创建密钥' }}</button>
              </div>
            </template>
            <template v-else>
              <div class="mk-done-head">
                <span class="mk-done-ic"><AqIcon name="check" :size="20" /></span>
                <h3>密钥已创建</h3>
              </div>
              <p class="hint-line neutral">分组 <b>{{ grpLabel(newKeyGrp) }}</b> · 已加密保存在账号里，可随时在列表复制或切换分组。</p>
              <code class="mk-key">{{ freshKey }}</code>
              <div class="grp-edit-ops">
                <button class="mini-btn" @click="setDefaultKey">设为站内默认</button>
                <button class="mini-btn ok" @click="copyFresh"><AqIcon :name="freshCopied ? 'check' : 'copy'" :size="12" /> {{ freshCopied ? '已复制' : '复制密钥' }}</button>
                <button class="btn tool-run" @click="closeMk">完成</button>
              </div>
            </template>
          </div>
        </div>

        <!-- ▼▼▼ 我的用量 ▼▼▼ -->
        <div v-show="view === 'usage'" class="view">
          <div class="vhead">
            <span class="vic"><AqIcon name="chart" :size="17" /></span>
            <div><h2>我的用量</h2><p>调用统计；请求明细见「请求历史」</p></div>
          </div>
          <div class="dash-sec">
            <div class="dash-cards">
              <div class="dash-card"><b>{{ cards.today }}</b><span>今日调用</span></div>
              <div class="dash-card"><b>{{ cards.todayRate }}</b><span>今日成功率</span></div>
              <div class="dash-card"><b>{{ cards.week }}</b><span>近 7 天调用</span></div>
              <div class="dash-card"><b>{{ cards.weekRate }}</b><span>近 7 天成功率</span></div>
            </div>
            <div class="usage-models">
              <div v-if="!modelRows.length" class="dash-empty">还没有调用记录——拿你的密钥去 Playground 聊一句</div>
              <div v-for="(m, i) in modelRows" :key="i" class="stat-row">
                <span class="nm" :title="m.model">{{ m.model }}</span>
                <span class="trk"><span class="fil" :style="{ width: m.width + '%' }"></span></span>
                <span class="val">{{ m.val }}</span>
              </div>
            </div>
            <p v-if="usageMsg" class="hint-line">{{ usageMsg }}</p>
          </div>
          <!-- 我的众筹池（acu/ 前缀模型按官方原价从站点额度扣费，与个人余额无关） -->
          <div class="dash-sec" style="margin-top:14px;">
            <div class="vhead" style="margin-bottom:10px;">
              <span class="vic" style="color:#a5b4fc;"><AqIcon name="coin" :size="16" /></span>
              <div><h2 style="font-size:15px;">我的众筹池</h2><p>acu/ 前缀众筹模型按官方原价从站点额度扣费（充 1 元 = 2 元额度），个人余额未动</p></div>
              <router-link to="/pool" class="mini-btn" style="margin-left:auto;text-decoration:none;">额度账本与榜单 →</router-link>
            </div>
            <div class="dash-cards">
              <div class="dash-card"><b>¥{{ poolYuan(myPool.balance_micro) }}</b><span>站点额度余额</span></div>
              <div class="dash-card"><b>¥{{ poolYuan(myPool.my_charged_micro) }}</b><span>我累计注入</span></div>
              <div class="dash-card"><b>¥{{ poolYuan(myPool.my_used_micro) }}</b><span>我累计消耗</span></div>
              <div class="dash-card"><b :style="{ color: myPool.net_micro >= 0 ? '#34d399' : '#fbbf24' }">¥{{ poolYuan(myPool.net_micro) }}</b><span>我的净贡献</span></div>
            </div>
            <div class="usage-models" style="max-height:260px;overflow-y:auto;">
              <div v-if="!myPool.items || !myPool.items.length" class="dash-empty">还没有众筹池记录——acu/ 模型免充值即可调用</div>
              <div v-for="(f, i) in myPool.items" :key="i" class="stat-row">
                <span class="nm" :title="f.model || ''">{{ fmtPoolTs(f.ts) }} · {{ f.type === 'consume' ? (f.model || '扣费') : (f.type === 'charge' ? '充值' : '官方注入') }}<template v-if="f.type === 'consume'">（入 {{ f.prompt_tokens }} / 出 {{ f.completion_tokens }}）</template></span>
                <span class="val" :style="{ color: f.amount_micro > 0 ? '#34d399' : 'var(--muted,#8a94a6)' }">{{ f.amount_micro > 0 ? '+' : '' }}¥{{ poolYuan(f.amount_micro) }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- ▼▼▼ 请求历史 ▼▼▼ -->
        <div v-show="view === 'history'" class="view">
          <div class="vhead">
            <span class="vic"><AqIcon name="clock" :size="17" /></span>
            <div><h2>请求历史</h2><p>每条请求全字段明细 · 保留 90 天 · 存于数据盘</p></div>
          </div>
          <div class="dash-sec">
            <div class="hist-toolbar">
              <span class="hist-total">共 {{ fmt(historyTotal) }} 条记录</span>
              <button class="mini-btn" :disabled="historyLoading" @click="loadHistory">
                <AqIcon name="refresh" :size="12" /> {{ historyLoading ? '刷新中…' : '刷新' }}
              </button>
            </div>
            <div class="hist-scroll">
              <table class="hist-table">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>端点</th>
                    <th>模型</th>
                    <th>状态</th>
                    <th class="num">输入</th>
                    <th class="num">输出</th>
                    <th class="num">缓存</th>
                    <th class="num">总计</th>
                    <th class="num">Tok/s</th>
                    <th class="num">延迟</th>
                    <th>传输</th>
                    <th>计费</th>
                    <th>来源</th>
                    <th>错误</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-if="!historyItems.length">
                    <td colspan="14" class="hist-empty">{{ historyLoading ? '加载中…' : (historyMsg || '暂无请求记录——拿你的密钥去 Playground 聊一句') }}</td>
                  </tr>
                  <tr v-for="(r, i) in historyItems" :key="r.ts + '-' + i" :class="{ failed: !r.ok }">
                    <td class="hist-time">{{ fmtTime(r.ts) }}</td>
                    <td><span class="hist-ep">{{ r.endpoint }}</span></td>
                    <td><span class="hist-model" :title="r.model">{{ r.model || '—' }}</span></td>
                    <td>
                      <span class="hist-badge" :class="r.ok ? 'ok' : 'bad'">{{ r.ok ? r.status_code : r.status_code }}</span>
                    </td>
                    <td class="num">{{ fmtTokens(r.prompt_tokens) }}</td>
                    <td class="num">{{ fmtTokens(r.completion_tokens) }}</td>
                    <td class="num" :class="{ cached: r.cached_tokens > 0 }">{{ fmtTokens(r.cached_tokens) }}</td>
                    <td class="num">{{ fmtTokens(r.total_tokens) }}</td>
                    <td class="num">{{ fmtTps(r.tps) }}</td>
                    <td class="num">{{ fmt(r.latency_ms) }} ms</td>
                    <td>
                      <span v-if="r.stream_mode" class="hist-src" :title="streamModeLabel[r.stream_mode] || r.stream_mode">{{ streamModeShort[r.stream_mode] || r.stream_mode }}</span>
                      <span v-else class="hist-src">—</span>
                    </td>
                    <td class="num">
                      <span v-if="r.billed" class="bill-chg">¥{{ yuan(r.bill_amount_micro) }}</span>
                      <span v-else-if="r.bill_state === 'refunded'" class="bill-ret" title="失败已退回">已退</span>
                      <span v-else class="hist-src">—</span>
                    </td>
                    <td><span class="hist-src" :class="{ real: r.usage_source === 'upstream' }">{{ sourceLabel(r.usage_source) }}</span></td>
                    <td class="hist-err" :title="r.error">{{ r.error || '—' }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="hist-pager" v-if="historyPages > 1 || historyPage > 1">
              <button class="mini-btn" :disabled="historyPage <= 1 || historyLoading" @click="goHistoryPage(1)">首页</button>
              <button class="mini-btn" :disabled="historyPage <= 1 || historyLoading" @click="goHistoryPage(historyPage - 1)">上一页</button>
              <span class="hist-pageinfo">第 {{ historyPage }} / {{ historyPages }} 页</span>
              <button class="mini-btn" :disabled="historyPage >= historyPages || historyLoading" @click="goHistoryPage(historyPage + 1)">下一页</button>
              <button class="mini-btn" :disabled="historyPage >= historyPages || historyLoading" @click="goHistoryPage(historyPages)">末页</button>
            </div>
          </div>
        </div>

        <!-- ▼▼▼ 余额充值 ▼▼▼ -->
        <div v-show="view === 'topup'" class="view">
          <div class="vhead">
            <span class="vic"><AqIcon name="spark" :size="17" /></span>
            <div><h2>余额充值</h2><p>支付宝 / 微信在线充值，支付金额 100% 全额到账（渠道手续费由本站承担）· 充值余额用于收费模型（aqua/ 前缀，按量计费）扣费，免费模型不受影响。<b>注意：「充 1 = 2」充值翻倍仅限众筹公共池</b>——想用 acu/ 众筹模型或享受充值翻倍，请到 <router-link to="/pool">众筹池充值</router-link>；本页充值入的是个人余额（1:1 到账），众筹模型调用不消耗个人余额。</p></div>
          </div>
          <div class="dash-sec bal-strip" style="margin:0 0 14px">
            <span>当前余额 <b>¥{{ yuan(balance?.balance_micro) }}</b></span>
            <span>收费模型<b>按量计费</b>：输入 / 缓存命中 / 输出按 tokens 三段精算，用多少付多少，实时单价见「模型中心」</span>
            <span>余额不足时收费模型返回 402，免费模型照常可用</span>
          </div>

          <!-- 在线充值 -->
          <div class="dash-sec topup-card">
            <b><AqIcon name="spark" :size="16" /> 在线充值（支付宝 / 微信）</b>
            <div class="topup-row">
              <div class="topup-presets">
                <button v-for="p in TOPUP_PRESETS" :key="p" class="mini-btn" :class="{ on: Number(topupAmt) === p }" @click="topupAmt = String(p)">¥{{ p }}</button>
              </div>
              <div class="topup-input">
                <input v-model="topupAmt" type="number" min="0.01" max="1000" step="0.01" placeholder="自定义金额（0.01 ~ 1000 元）" />
              </div>
              <div class="topup-channels">
                <button class="mini-btn" :class="{ on: topupChannel === 'alipay' }" @click="topupChannel = 'alipay'">支付宝</button>
                <button class="mini-btn" :class="{ on: topupChannel === 'wxpay' }" @click="topupChannel = 'wxpay'">微信支付</button>
              </div>
              <button class="btn tool-run" :disabled="paying || !topupAmt" @click="createTopup">
                {{ paying ? '创建中…' : '去支付' }}
              </button>
            </div>
            <p v-if="topupCreditPreview > 0" class="topup-credit">
              支付 ¥{{ topupAmt }}，预计到账余额 <b>¥{{ yuan(topupCreditPreview) }}</b>
              （100% 全额到账，渠道手续费由本站承担）
            </p>
            <p v-if="topupMsg" class="topup-msg" :class="{ ok: topupOk }">{{ topupMsg }}</p>
            <div v-if="payingOrder" class="topup-pending">
              <span>订单 {{ payingOrder.out_trade_no }}（支付 ¥{{ yuan(payingOrder.amount_micro) }}，到账 ¥{{ yuan(payingOrder.credit_micro) }}）等待支付中——在新窗口完成支付后，这里会自动到账（每 3 秒检测）。</span>
              <button class="mini-btn" @click="manualCheck">我已支付，立即查询</button>
              <button class="mini-btn" @click="stopPolling(); payingOrder = null">取消检测</button>
            </div>
            <p class="topup-hint">支付成功后自动入账；到账前请勿关闭本页。收费模型按量计费（实时单价见模型中心收费专区），余额低于阈值可开启邮件提醒。</p>
            <div class="topup-help">
              <b>支付遇到问题？</b>已支付但余额未到账、重复扣款、金额有误——请勿重复支付，保留支付凭证（账单截图 / 商户单号），
              <a href="https://pd.qq.com/s/e4ktxw1b8" target="_blank" rel="noopener">加入 QQ 频道</a> 或
              <a href="https://qm.qq.com/cgi-bin/qm/qr?k=&jump_from=&group=1103667832" target="_blank" rel="noopener">加入 QQ 一群（1103667832）</a>
              / <a href="https://qm.qq.com/q/o8QDbza2Ge" target="_blank" rel="noopener">二群（1006740220）</a>
              联系站长人工核实补账。
            </div>
          </div>

          <!-- 充值记录 -->
          <div class="dash-sec" v-if="payOrders.length">
            <b><AqIcon name="list" :size="16" /> 充值记录</b>
            <div class="hist-scroll">
              <table class="hist-table bill-table">
                <thead>
                  <tr><th>时间</th><th>订单号</th><th>渠道</th><th class="num">金额</th><th>状态</th></tr>
                </thead>
                <tbody>
                  <tr v-for="o in payOrders" :key="o.out_trade_no">
                    <td class="hist-time">{{ fmtTime(o.created_ts) }}</td>
                    <td class="hist-model">{{ o.out_trade_no }}</td>
                    <td>{{ channelLabel[o.channel] || o.channel }}</td>
                    <td class="num bill-in">¥{{ yuan(o.amount_micro) }}</td>
                    <td>
                      <span class="bill-tag" :class="o.status === 'paid' ? 'topup' : 'refunded'">{{ o.status === 'paid' ? '已到账' : '待支付' }}</span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>

        <!-- ▼▼▼ 消费账单 ▼▼▼ -->
        <div v-show="view === 'billing'" class="view">
          <div class="vhead">
            <span class="vic"><AqIcon name="bolt" :size="17" /></span>
            <div><h2>消费账单</h2><p>收费模型（aqua/ 前缀，按量计费）每笔扣费 / 退回 / 发放记录 · 预充值、先付后用、用完即停、失败自动退回 · 免费模型不产生任何账单</p></div>
          </div>

          <div class="dash-sec">
            <div class="bal-strip">
              <span>当前余额 <b>¥{{ yuan(balance?.balance_micro) }}</b></span>
              <span>今日消费 <b>¥{{ yuan(balance?.today_cost_micro) }}</b></span>
              <span>累计消费 <b>¥{{ yuan(balance?.total_cost_micro) }}</b></span>
              <button class="mini-btn ok" @click="go('topup')"><AqIcon name="spark" :size="12" /> 余额充值</button>
              <button class="mini-btn" :disabled="billLoading" @click="loadBilling">
                <AqIcon name="refresh" :size="12" /> {{ billLoading ? '刷新中…' : '刷新' }}
              </button>
            </div>
            <div class="hist-scroll">
              <table class="hist-table bill-table">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>类型</th>
                    <th>模型</th>
                    <th class="num">金额</th>
                    <th class="num">余额</th>
                    <th>备注</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-if="!billItems.length">
                    <td colspan="6" class="hist-empty">{{ billLoading ? '加载中…' : (billMsg || '暂无账单——使用收费模型（aqua/ 前缀，按量计费）后这里会出现记录') }}</td>
                  </tr>
                  <tr v-for="(b, i) in billItems" :key="b.ts + '-' + i">
                    <td class="hist-time">{{ fmtTime(b.ts) }}</td>
                    <td><span class="bill-tag" :class="b.type">{{ (b.type === 'topup' && b.operator === 'epay') ? '在线充值' : (billTypeLabel[b.type] || b.type) }}</span></td>
                    <td><span class="hist-model" :title="b.model">{{ b.model || '—' }}</span></td>
                    <td class="num" :class="{ 'bill-in': b.amount_micro > 0, 'bill-out': b.amount_micro < 0 }">
                      {{ b.amount_micro > 0 ? '+' : '' }}¥{{ yuan(Math.abs(b.amount_micro)) }}
                    </td>
                    <td class="num">¥{{ yuan(b.balance_after_micro) }}</td>
                    <td class="bill-note" :title="b.note">{{ b.note || (b.type === 'billed' ? '扣费确认' : '—') }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="hist-pager" v-if="billPages > 1 || billPage > 1">
              <button class="mini-btn" :disabled="billPage <= 1 || billLoading" @click="goBillPage(1)">首页</button>
              <button class="mini-btn" :disabled="billPage <= 1 || billLoading" @click="goBillPage(billPage - 1)">上一页</button>
              <span class="hist-pageinfo">第 {{ billPage }} / {{ billPages }} 页</span>
              <button class="mini-btn" :disabled="billPage >= billPages || billLoading" @click="goBillPage(billPage + 1)">下一页</button>
              <button class="mini-btn" :disabled="billPage >= billPages || billLoading" @click="goBillPage(billPages)">末页</button>
            </div>
          </div>
        </div>

        <!-- ▼▼▼ 账号设置 ▼▼▼ -->
        <div v-show="view === 'settings'" class="view">
          <div class="vhead">
            <span class="vic"><AqIcon name="settings" :size="17" /></span>
            <div><h2>账号设置</h2><p>资料 / 头像 / 体检详情 / 修改密码</p></div>
          </div>

          <!-- 资料 -->
          <div class="dash-sec profile-card">
            <b><AqIcon name="user" :size="16" /> 个人资料</b>
            <div class="profile-row">
              <div class="avatar-box" @click="fileInput?.click()" title="点击更换头像">
                <img v-if="me?.avatar_ext" :src="avatarUrl() + '?t=' + avatarBust" alt="头像" />
                <span v-else class="avatar-fallback">{{ (me?.username || '?').slice(0, 1) }}</span>
                <span class="avatar-edit">更换</span>
              </div>
              <div class="profile-main">
                <template v-if="!nameEdit">
                  <b class="profile-name">{{ me?.username || '…' }}</b>
                  <button class="mini-btn" @click="nameEdit = true; nameDraft = me?.username || ''">改名</button>
                </template>
                <template v-else>
                  <input v-model="nameDraft" class="name-input" maxlength="20" @keydown.enter="saveName" />
                  <button class="mini-btn ok" @click="saveName">保存</button>
                  <button class="mini-btn" @click="nameEdit = false">取消</button>
                </template>
                <div class="profile-meta">UID #{{ me?.id }} · {{ me?.email }}</div>
                <div class="profile-meta">注册于 {{ me ? fmtTime(me.created_ts) : '…' }}</div>
              </div>
            </div>
            <p v-if="profileMsg" class="hint-line" :class="{ ok: profileOk }">{{ profileMsg }}</p>
          </div>

          <!-- 余额不足邮件提醒 -->
          <div class="dash-sec">
            <b><AqIcon name="mail" :size="16" /> 余额不足邮件提醒 <span class="sec-sub">（余额低于阈值时发邮件，避免调用中断）</span></b>
            <div class="alert-form">
              <div class="alert-input-row">
                <span class="alert-label">余额低于</span>
                <input v-model="alertYuan" class="alert-input" type="number" min="0" max="100" step="0.01" placeholder="如 1.00" @keydown.enter="doSaveAlert" />
                <span class="alert-label">元时，提醒我</span>
                <button class="btn tool-run" :disabled="alertSaving" @click="doSaveAlert">{{ alertSaving ? '保存中…' : '保存' }}</button>
                <button v-if="alertThreshold > 0" class="mini-btn" :disabled="alertSaving" @click="disableAlert">关闭提醒</button>
              </div>
              <p class="alert-status">
                当前状态：{{ alertThreshold > 0 ? `已开启 · 阈值 ¥${yuan(alertThreshold)} · 发送至 ${me?.email || '绑定邮箱'}` : '未开启' }}
              </p>
              <p class="alert-note">提醒发送至账号邮箱（{{ me?.email || '—' }}）；充值使余额回到阈值上方后，下次低于阈值会再次提醒。收费模型余额不足时调用将返回 402。</p>
            </div>
            <p v-if="alertMsg" class="hint-line" :class="{ ok: alertOk }">{{ alertMsg }}</p>
          </div>

          <!-- 账号检查 -->
          <div class="dash-sec">
            <b><AqIcon name="stethoscope" :size="16" /> 账号检查 <span class="sec-sub">（邮箱 / 头像 / 密钥 / 活跃度体检）</span></b>
            <div v-if="checkup" class="checkup">
              <div class="checkup-score">
                <svg width="72" height="72" viewBox="0 0 72 72">
                  <circle cx="36" cy="36" r="26" fill="none" stroke="rgba(128,140,160,.25)" stroke-width="7" />
                  <circle
                    cx="36" cy="36" r="26" fill="none" :stroke="scoreColor(checkup.score)" stroke-width="7"
                    stroke-linecap="round" :stroke-dasharray="CIRC" :stroke-dashoffset="dashoffset(checkup.score)"
                    transform="rotate(-90 36 36)"
                  />
                  <text x="36" y="41" text-anchor="middle" :fill="scoreColor(checkup.score)" font-size="18" font-weight="700">{{ checkup.score }}</text>
                </svg>
                <span class="checkup-score-label">健康分</span>
              </div>
              <div class="checkup-items">
                <div v-for="it in checkup.items" :key="it.id" class="checkup-item" :class="'lv-' + it.level">
                  <AqIcon :name="it.level === 'ok' ? 'check' : it.level === 'warn' ? 'alert' : 'cross'" :size="15" />
                  <div class="checkup-txt">
                    <b>{{ it.title }} <span class="checkup-detail">{{ it.detail }}</span></b>
                    <span v-if="it.advice" class="checkup-advice">{{ it.advice }}</span>
                  </div>
                </div>
              </div>
              <button class="mini-btn" style="align-self:flex-start;" :disabled="checking" @click="loadCheckup">
                <AqIcon name="refresh" :size="12" /> {{ checking ? '检查中…' : '重新检查' }}
              </button>
            </div>
            <div v-else class="dash-empty">{{ checking ? '正在检查…' : (checkupMsg || '加载中…') }}</div>
          </div>

          <!-- 改密码 -->
          <div class="dash-sec">
            <b><AqIcon name="lock" :size="16" /> 修改密码 <span class="sec-sub">（改完全端下线，需重新登录）</span></b>
            <div class="pw-form">
              <input v-model="oldPw" type="password" placeholder="当前密码" autocomplete="current-password" />
              <input v-model="newPw" type="password" placeholder="新密码（8~72 位，含字母和数字）" autocomplete="new-password" />
              <button class="btn tool-run" :disabled="!oldPw || !newPw" @click="doChangePw">修改</button>
            </div>
            <p v-if="pwMsg" class="hint-line" :class="{ ok: pwOk }">{{ pwMsg }}</p>
          </div>

          <!-- 退出 -->
          <div class="dash-sec">
            <b><AqIcon name="arrow-right" :size="16" /> 会话</b>
            <div class="logout-row">
              <span class="sec-sub">退出当前账号，所有浏览器会话不受影响</span>
              <button class="mini-btn danger" @click="doLogout">退出登录</button>
            </div>
          </div>
        </div>

      </main>
    </div>
  </section>
</template>

<style scoped>
.sec-sub { font-size: 11.5px; color: var(--muted, #8a94a6); font-weight: 400; }
.hint-line { font-size: 12.5px; color: #f87171; margin: 8px 0 0; display: block; line-height: 1.7; }
.hint-line :deep(b) { font-size: inherit; }
.hint-line.ok { color: #34d399; }
.mini-btn { display: inline-flex; align-items: center; gap: 4px; font-size: 11.5px; padding: 4px 10px; border-radius: 8px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; cursor: pointer; transition: background .15s ease, box-shadow .15s ease, transform .15s ease; }
.mini-btn:hover { background: rgba(128,140,160,.10); }
.mini-btn.ok { border-color: rgba(52,211,153,.55); color: #10b981; background: rgba(52,211,153,.10); }
.mini-btn.ok:hover { background: rgba(52,211,153,.20); box-shadow: 0 2px 10px rgba(52,211,153,.22); }
.mini-btn.danger { border-color: rgba(248,113,113,.55); color: #ef4444; background: rgba(248,113,113,.10); }
.mini-btn.danger:hover { background: rgba(248,113,113,.20); box-shadow: 0 2px 10px rgba(248,113,113,.22); }

/* ===== 控制台骨架 ===== */
.console-shell { display: flex; gap: 20px; align-items: flex-start; padding-bottom: 30px; }
.cside { position: sticky; top: 78px; width: 224px; flex: none; display: flex; flex-direction: column; gap: 4px; background: var(--card, #fff); border: 1px solid var(--border, rgba(128,140,160,.25)); border-radius: 14px; padding: 10px; transition: width .18s ease; }
.cside.mini { width: 62px; }
.cside-user { display: flex; align-items: center; gap: 10px; padding: 6px 8px 12px; border-bottom: 1px solid var(--border, rgba(128,140,160,.2)); margin-bottom: 6px; }
.cside.mini .cside-user { justify-content: center; padding: 6px 0 12px; }
.cside-avatar { width: 34px; height: 34px; border-radius: 50%; overflow: hidden; flex: none; background: var(--accent, #0b6cff); display: flex; align-items: center; justify-content: center; color: #fff; font-size: 15px; font-weight: 800; }
.cside-avatar img { width: 100%; height: 100%; object-fit: cover; }
.cside-who { display: flex; flex-direction: column; min-width: 0; }
.cside-who b { font-size: 13.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cside-who span { font-size: 11px; color: var(--muted, #8a94a6); }
.cnav { display: flex; flex-direction: column; gap: 3px; }
.cnav-item { display: flex; align-items: center; gap: 10px; width: 100%; padding: 9px 10px; border: none; background: transparent; border-radius: 10px; color: var(--muted, #8a94a6); font-size: 13.5px; cursor: pointer; text-align: left; transition: background .15s, color .15s; font-family: inherit; }
.cnav-item:hover { background: rgba(128,140,160,.1); color: inherit; }
.cnav-item.on { background: linear-gradient(90deg, rgba(56,189,248,.16), rgba(56,189,248,.04)); color: var(--aqua, #38bdf8); font-weight: 700; box-shadow: inset 2.5px 0 0 var(--aqua, #38bdf8), 0 0 14px rgba(56,189,248,.12); }
.cnav-item .cnav-label { flex: none; }
.cnav-badge { margin-left: auto; font-style: normal; font-size: 10.5px; font-weight: 700; background: rgba(128,140,160,.2); color: var(--muted, #8a94a6); border-radius: 999px; padding: 1px 7px; }
.cnav-item.on .cnav-badge { background: rgba(56,189,248,.18); color: var(--aqua, #38bdf8); }
.cside.mini .cnav-item { justify-content: center; padding: 10px 0; }
.cside.mini .cnav-badge { display: none; }
.cside-foot { border-top: 1px solid var(--border, rgba(128,140,160,.2)); padding-top: 6px; margin-top: 2px; }
.cnav-item.foot-out:hover { color: #f87171; background: rgba(248,113,113,.08); }
.cside-fold { margin-top: 6px; display: flex; align-items: center; justify-content: center; gap: 6px; padding: 7px 0; border: none; border-radius: 8px; background: transparent; color: var(--muted, #8a94a6); cursor: pointer; }
.cside-fold:hover { background: rgba(128,140,160,.1); color: inherit; }
.cmain { flex: 1; min-width: 0; }
.view { animation: viewin .18s ease; }
@keyframes viewin { from { opacity: 0; transform: translateY(4px); } to { opacity: 1; transform: none; } }

/* 视图头 */
.vhead { display: flex; align-items: center; gap: 12px; margin-bottom: 14px; }
.vic { width: 36px; height: 36px; flex: none; border-radius: 10px; background: rgba(56,189,248,.12); color: var(--aqua, #38bdf8); display: flex; align-items: center; justify-content: center; box-shadow: inset 0 0 0 1px rgba(56,189,248,.25), 0 0 14px rgba(56,189,248,.15); }
.vhead h2 { font-size: 17px; line-height: 1.2; }
.vhead p { font-size: 12px; color: var(--muted, #8a94a6); margin-top: 2px; }

/* ===== 总览 ===== */
.ov-hero { display: flex; align-items: center; gap: 16px; flex-wrap: wrap; background: var(--glass-bg, var(--card)); border: 1px solid var(--glass-border, var(--border)); border-radius: var(--radius-card, 16px); padding: 16px 20px; margin-bottom: 16px; box-shadow: var(--shadow-card); }
.ov-hero-txt { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.ov-hero-txt b { font-size: 17px; }
.ov-hi { font-size: 13px; color: var(--muted, #8a94a6); font-weight: 400; }
.ov-hero-txt > span { font-size: 12px; color: var(--muted, #8a94a6); }
.ov-logout { margin-left: auto; align-self: flex-start; }
.ov-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; margin: 16px 0; align-items: stretch; }
.ov-health { display: flex; align-items: center; gap: 16px; margin-top: 12px; flex-wrap: wrap; }
.ov-health-txt { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.ov-health-txt b { font-size: 15px; }
.ov-health-advice { font-size: 12px; color: var(--muted, #8a94a6); }
.ov-sec-ops { display: flex; gap: 8px; margin-top: 12px; flex-wrap: wrap; }
.ov-keys { display: flex; align-items: center; gap: 16px; margin-top: 12px; flex-wrap: wrap; }
.ov-keys-num { display: flex; flex-direction: column; align-items: center; gap: 2px; flex: none; min-width: 64px; }
.ov-keys-num b { font-size: 26px; color: var(--accent, #0b6cff); font-weight: 800; }
.ov-keys-num span { font-size: 11px; color: var(--muted, #8a94a6); }
.ov-keys-list { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 6px; }
.ov-key-row { display: flex; align-items: center; gap: 8px; font-size: 12.5px; padding: 6px 10px; border-radius: 8px; border: 1px solid var(--border, rgba(128,140,160,.2)); color: var(--muted, #8a94a6); }
.ov-key-row .nm { font-weight: 600; color: inherit; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; flex: 1; }
.ov-key-row code { font-size: 11.5px; color: var(--accent, #0b6cff); }

/* 资料 */
.profile-card { display: flex; flex-direction: column; }
.profile-row { display: flex; align-items: center; gap: 16px; flex-wrap: wrap; margin-top: 10px; }
.avatar-box { position: relative; width: 72px; height: 72px; border-radius: 50%; overflow: hidden; cursor: pointer; flex-shrink: 0; background: var(--accent, #0b6cff); display: flex; align-items: center; justify-content: center; }
.avatar-box img { width: 100%; height: 100%; object-fit: cover; }
.avatar-fallback { color: #fff; font-size: 30px; font-weight: 800; }
.avatar-edit { position: absolute; bottom: 0; left: 0; right: 0; background: rgba(0,0,0,.55); color: #fff; font-size: 10.5px; text-align: center; padding: 3px 0; opacity: 0; transition: opacity .15s; }
.avatar-box:hover .avatar-edit { opacity: 1; }
.profile-main { display: flex; flex-direction: column; gap: 3px; }
.profile-name { font-size: 17px; }
.profile-meta { font-size: 12px; color: var(--muted, #8a94a6); }
.name-input { padding: 5px 8px; border-radius: 6px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; font-size: 14px; width: 160px; }

/* 账号检查 */
.checkup { display: flex; gap: 18px; margin-top: 12px; align-items: flex-start; flex-wrap: wrap; }
.checkup-score { display: flex; flex-direction: column; align-items: center; gap: 4px; }
.checkup-score-label { font-size: 11.5px; color: var(--muted, #8a94a6); }
.checkup-items { flex: 1; min-width: 260px; display: flex; flex-direction: column; gap: 6px; }
.checkup-item { display: flex; gap: 8px; align-items: flex-start; padding: 7px 10px; border-radius: 8px; border: 1px solid var(--border, rgba(128,140,160,.2)); font-size: 12.5px; }
.checkup-item .checkup-txt { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.checkup-item .checkup-detail { font-weight: 400; color: var(--muted, #8a94a6); font-size: 12px; }
.checkup-item .checkup-advice { font-size: 11.5px; color: var(--muted, #8a94a6); }
.checkup-item.lv-ok { border-left: 3px solid #34d399; }
.checkup-item.lv-ok svg { color: #34d399; }
.checkup-item.lv-warn { border-left: 3px solid #fbbf24; }
.checkup-item.lv-warn svg { color: #fbbf24; }
.checkup-item.lv-bad { border-left: 3px solid #f87171; }
.checkup-item.lv-bad svg { color: #f87171; }

/* 密钥 */
/* 区头部：说明 + 创建 CTA（创建走弹窗） */
.keys-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin: 4px 0 2px; }
.keys-head-txt { margin: 0; font-size: 13px; color: var(--muted, #8a94a6); }
.keys-cta { white-space: nowrap; display: inline-flex; align-items: center; gap: 5px; }
.key-list { display: flex; flex-direction: column; gap: 9px; }
/* 密钥卡片：左主区（名称+分组徽章 / 密钥摘要）+ 右侧（时间 / 操作按钮组） */
.key-card { display: flex; align-items: center; gap: 14px; padding: 12px 15px; border-radius: 13px; border: 1px solid var(--border, rgba(128,140,160,.2)); background: var(--card2, transparent); font-size: 13px; transition: transform .16s ease, border-color .16s ease, box-shadow .16s ease; animation: keyIn .35s ease backwards; animation-delay: calc(var(--i, 0) * 60ms); }
@keyframes keyIn { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }
.key-card:hover { transform: translateY(-1px); border-color: rgba(56,189,248,.4); box-shadow: 0 6px 20px rgba(56,189,248,.10); }
.key-card.revoked { opacity: .55; }
.kc-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.kc-title { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.kc-main .key-name { font-weight: 600; font-size: 13.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 100%; }
.kc-main .key-prefix { font-size: 12px; color: var(--muted, #8a94a6); letter-spacing: .3px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 100%; }
.kc-side { flex-shrink: 0; display: flex; flex-direction: column; align-items: flex-end; gap: 7px; }
.kc-ops { display: flex; gap: 6px; flex-wrap: wrap; justify-content: flex-end; }
.key-time { font-size: 11.5px; color: var(--muted, #8a94a6); }
@media (max-width: 640px) {
  .key-card { flex-direction: column; align-items: stretch; gap: 9px; }
  .kc-side { flex-direction: row; align-items: center; justify-content: space-between; }
}
.key-grp { font-size: 11px; padding: 2px 8px; border-radius: 999px; white-space: nowrap; }
.key-grp.call { background: rgba(56,189,248,.12); color: var(--aqua, #38bdf8); }
.key-grp.token { background: rgba(52,211,153,.14); color: #34d399; }
.key-grp.free { background: rgba(34,197,94,.14); color: #16a34a; border: 1px solid rgba(34,197,94,.35); }
.key-grp.official { background: rgba(129,140,248,.16); color: #818cf8; border: 1px solid rgba(129,140,248,.4); }
.key-grp.legacy { background: rgba(128,140,160,.15); color: var(--muted, #8a94a6); }
/* 密钥弹窗（切换分组 / 创建密钥共用） */
.grp-mask { position: fixed; inset: 0; background: rgba(0,0,0,.55); backdrop-filter: blur(3px); z-index: 200; display: flex; align-items: center; justify-content: center; padding: 20px; animation: aquaFade .18s ease; }
.grp-edit-modal { background: var(--card, #121a26); border: 1px solid var(--border, rgba(128,140,160,.3)); border-radius: 16px; padding: 20px 22px; width: min(420px, 92vw); max-height: 86vh; overflow-y: auto; animation: aquaPop .22s cubic-bezier(.2,.9,.3,1.15); }
.grp-edit-modal h3 { margin: 0 0 10px; font-size: 16px; display: flex; align-items: center; gap: 7px; }
.grp-edit-modal .hint-line { margin: 0 0 12px; }
.grp-edit-ops { display: flex; justify-content: flex-end; gap: 8px; margin-top: 14px; }
@keyframes aquaFade { from { opacity: 0; } to { opacity: 1; } }
@keyframes aquaPop { from { opacity: 0; transform: translateY(10px) scale(.97); } to { opacity: 1; transform: none; } }
/* 分组选择：2×2 网格 */
.grp-pick { display: grid; grid-template-columns: 1fr 1fr; gap: 9px; }
.grp-opt { display: flex; flex-direction: column; align-items: flex-start; gap: 3px; padding: 12px 14px; border-radius: 12px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; cursor: pointer; font-size: 12px; color: inherit; text-align: left; transition: border-color .15s ease, background .15s ease, box-shadow .15s ease; }
.grp-opt:hover { border-color: rgba(56,189,248,.45); background: rgba(56,189,248,.06); }
.grp-opt b { font-size: 13px; }
.grp-opt span { color: var(--muted, #8a94a6); font-size: 11px; line-height: 1.5; }
.grp-opt.on { border-color: var(--aqua, #38bdf8); background: rgba(56,189,248,.12); box-shadow: 0 0 0 1px rgba(56,189,248,.35), 0 4px 14px rgba(56,189,248,.15); }
.grp-opt.on b { color: var(--aqua, #38bdf8); }
.grp-opt.official.on { border-color: #818cf8; background: rgba(129,140,248,.12); box-shadow: 0 0 0 1px rgba(129,140,248,.4), 0 4px 14px rgba(129,140,248,.18); }
.grp-opt.official.on b { color: #a5b4fc; }
/* 分组角标：推荐 / 已下架 */
.mk-rec { font-style: normal; font-size: 9px; font-weight: 700; letter-spacing: .5px; padding: 1px 6px; border-radius: 999px; margin-left: 6px; vertical-align: 2px; color: #fff; background: linear-gradient(135deg, #10b981, #34d399); box-shadow: 0 2px 6px rgba(16,185,129,.4); }
.mk-dead { font-style: normal; font-size: 9px; font-weight: 700; letter-spacing: .5px; padding: 1px 6px; border-radius: 999px; margin-left: 6px; vertical-align: 2px; color: #9ca3af; background: rgba(128,140,160,.18); }
.grp-opt.dim { opacity: .62; }
.grp-opt.dim b { color: var(--muted, #8a94a6); }
/* 创建密钥弹窗细节 */
.mk-modal { width: min(560px, 94vw); }
.hint-line.neutral { color: var(--muted, #8a94a6); }
.mk-name { width: 100%; box-sizing: border-box; padding: 11px 13px; border-radius: 10px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; font-size: 13.5px; margin-bottom: 13px; }
.mk-name:focus { outline: none; border-color: var(--aqua, #38bdf8); box-shadow: 0 0 0 3px rgba(56,189,248,.15); }
.mk-label { margin: 0 0 8px; font-size: 12.5px; font-weight: 600; }
.mk-label span { font-weight: 400; color: var(--muted, #8a94a6); font-size: 11.5px; margin-left: 5px; }
/* 创建成功态 */
.mk-done-head { display: flex; align-items: center; gap: 10px; margin: 2px 0 10px; }
.mk-done-head h3 { margin: 0; }
.mk-done-ic { width: 34px; height: 34px; border-radius: 50%; display: flex; align-items: center; justify-content: center; background: rgba(52,211,153,.14); color: #34d399; border: 1px solid rgba(52,211,153,.4); flex: none; }
.mk-key { display: block; word-break: break-all; font-size: 14.5px; font-weight: 700; padding: 13px 14px; border-radius: 12px; background: rgba(56,189,248,.08); border: 1px dashed var(--aqua, #38bdf8); margin: 0 0 2px; color: var(--aqua, #38bdf8); }
.key-revoked { color: #f87171; font-size: 11.5px; }
.key-legacy { color: #fbbf24; font-size: 11px; border: 1px solid rgba(251,191,36,.4); border-radius: 6px; padding: 1px 6px; }

/* 用量 */
.usage-models { margin-top: 12px; }

/* 请求历史明细表 */
.hist-toolbar { display: flex; align-items: center; gap: 10px; margin-top: 10px; }
.hist-total { font-size: 12px; color: var(--muted, #8a94a6); }
.hist-scroll { margin-top: 10px; overflow-x: auto; border: 1px solid var(--border, rgba(128,140,160,.2)); border-radius: 10px; }
.hist-table { width: 100%; border-collapse: collapse; font-size: 12px; min-width: 860px; }
.hist-table th { text-align: left; font-weight: 600; color: var(--muted, #8a94a6); padding: 9px 10px; border-bottom: 1px solid var(--border, rgba(128,140,160,.25)); white-space: nowrap; position: sticky; top: 0; background: var(--bg, #fff); z-index: 1; }
.hist-table td { padding: 8px 10px; border-bottom: 1px solid var(--border, rgba(128,140,160,.12)); white-space: nowrap; }
.hist-table tbody tr:last-child td { border-bottom: none; }
.hist-table tbody tr.failed { background: rgba(248,113,113,.05); }
.hist-table th.num, .hist-table td.num { text-align: right; font-variant-numeric: tabular-nums; }
.hist-time { color: var(--muted, #8a94a6); }
.hist-ep { font-weight: 600; }
.hist-model { display: inline-block; max-width: 180px; overflow: hidden; text-overflow: ellipsis; vertical-align: bottom; }
.hist-badge { display: inline-block; min-width: 34px; text-align: center; font-size: 11px; font-weight: 700; padding: 2px 7px; border-radius: 999px; }
.hist-badge.ok { background: rgba(52,211,153,.15); color: #34d399; }
.hist-badge.bad { background: rgba(248,113,113,.15); color: #f87171; }
.hist-table td.num.cached { color: #38bdf8; }
.hist-src { font-size: 11px; color: var(--muted, #8a94a6); }
.hist-src.real { color: #34d399; }
.hist-err { max-width: 220px; overflow: hidden; text-overflow: ellipsis; color: #f87171; }
.hist-empty { text-align: center; color: var(--muted, #8a94a6); padding: 22px 10px !important; }
.hist-pager { display: flex; align-items: center; gap: 8px; margin-top: 10px; flex-wrap: wrap; }
.hist-pageinfo { font-size: 12px; color: var(--muted, #8a94a6); }

/* ===== HUD 驾驶舱（v5.5 科技感主视觉） ===== */
.hud {
  position: relative;
  overflow: hidden;
  border-radius: var(--radius-card, 16px);
  border: 1px solid var(--glass-border, var(--border));
  background:
    linear-gradient(135deg, rgba(56,189,248,.10), rgba(129,140,248,.06) 42%, transparent 72%),
    var(--glass-bg, var(--card));
  padding: 22px 24px;
  margin-bottom: 16px;
  box-shadow: var(--shadow-card);
}
.hud::before {
  content: "";
  position: absolute;
  inset: 0 0 auto 0;
  height: 1px;
  background: linear-gradient(90deg, transparent, var(--glass-highlight), transparent);
  pointer-events: none;
}
/* 科技网格：右上角透视淡出 */
.hud-grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(56,189,248,.07) 1px, transparent 1px),
    linear-gradient(90deg, rgba(56,189,248,.07) 1px, transparent 1px);
  background-size: 26px 26px;
  -webkit-mask-image: radial-gradient(62% 100% at 72% 0%, #000, transparent 78%);
  mask-image: radial-gradient(62% 100% at 72% 0%, #000, transparent 78%);
  pointer-events: none;
}
/* 双色氛围光斑 */
.hud-glow {
  position: absolute;
  width: 320px;
  height: 320px;
  border-radius: 50%;
  filter: blur(72px);
  opacity: .55;
  pointer-events: none;
}
.hud-glow.g1 { background: rgba(56,189,248,.32); top: -130px; right: -70px; }
.hud-glow.g2 { background: rgba(129,140,248,.26); bottom: -150px; left: 8%; }
.hud-top { position: relative; display: flex; align-items: center; gap: 16px; flex-wrap: wrap; }
.hud-main {
  position: relative;
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 18px;
  flex-wrap: wrap;
  margin-top: 16px;
  padding-top: 18px;
  border-top: 1px solid var(--glass-border, var(--border));
}
.hud-bal { display: flex; flex-direction: column; gap: 5px; min-width: 0; }
.hud-label { font-size: 12px; color: var(--muted, #8a94a6); letter-spacing: .05em; }
/* 超大发光余额数字：三色渐变 + 外发光 */
.hud-num {
  font-size: clamp(34px, 5.2vw, 48px);
  font-weight: 800;
  font-family: var(--mono, monospace);
  font-variant-numeric: tabular-nums;
  line-height: 1.12;
  letter-spacing: -.01em;
  background: linear-gradient(120deg, #67e8f9, #38bdf8 45%, #818cf8);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  filter: drop-shadow(0 0 20px rgba(56,189,248,.35));
}
.hud-sub { font-size: 12.5px; color: var(--muted, #8a94a6); }
.hud-sub b { color: var(--text, inherit); font-family: var(--mono, monospace); }
.hud-ops { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.hud-strip {
  position: relative;
  display: flex;
  gap: 18px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--muted, #8a94a6);
  margin-top: 15px;
  padding-top: 12px;
  border-top: 1px dashed var(--glass-border, var(--border));
  line-height: 1.7;
}
.hud-strip b { color: var(--text, inherit); }
@media (max-width: 640px) {
  .hud { padding: 16px; }
  .hud-ops { width: 100%; }
  .hud-ops .btn { flex: 1; }
}

/* 充值视图余额条（dash-sec + bal-strip 组合） */
.bal-strip { display: flex; align-items: center; gap: 18px; flex-wrap: wrap; font-size: 12.5px; color: var(--muted, #8a94a6); }
.bal-strip b { font-family: var(--mono, monospace); color: var(--text, inherit); }

/* 计费列 / 账单表 */
.bill-chg { color: #f87171; font-family: var(--mono, monospace); }
.bill-ret { color: #34d399; font-size: 11px; }
.bill-table { min-width: 640px; }
.bill-tag { display: inline-block; font-size: 11px; padding: 2px 8px; border-radius: 999px; background: rgba(128,140,160,.15); color: var(--muted, #8a94a6); white-space: nowrap; }
.bill-tag.topup { background: rgba(52,211,153,.15); color: #34d399; }
.bill-tag.refunded { background: rgba(56,189,248,.15); color: #38bdf8; }
.bill-tag.deduct { background: rgba(251,191,36,.15); color: #fbbf24; }
.bill-tag.prehold { background: rgba(251,191,36,.1); color: var(--muted, #8a94a6); }
.bill-in { color: #34d399; }

/* 在线充值 */
.topup-card { display: flex; flex-direction: column; gap: 10px; }
.topup-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.topup-presets, .topup-channels { display: flex; gap: 6px; flex-wrap: wrap; }
.mini-btn.on { background: var(--btn-grad); border-color: transparent; color: #fff; font-weight: 700; box-shadow: inset 0 1px 0 rgba(255,255,255,.25), 0 2px 8px rgba(8,145,178,.35); }
.topup-input input { width: 220px; padding: 8px 12px; border-radius: 10px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; font-size: 13px; }
.topup-input input:focus { outline: none; border-color: var(--accent, #0b6cff); }
.topup-msg { font-size: 12.5px; color: #f87171; }
.topup-credit { font-size: 12.5px; color: var(--muted, #8a94a6); margin-top: 6px; }
.topup-credit b { color: #34d399; }
.topup-help { font-size: 12px; color: var(--muted, #8a94a6); background: rgba(148, 163, 184, .08); border: 1px solid rgba(148, 163, 184, .22); border-radius: 10px; padding: 8px 12px; margin-top: 8px; line-height: 1.7; }
.topup-help b { color: #f59e0b; }
.topup-help a { color: var(--aqua, #22d3ee); text-decoration: underline; }
.topup-msg.ok { color: #34d399; }
.topup-pending { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; font-size: 12.5px; color: #f59e0b; background: rgba(245, 158, 11, .08); border: 1px solid rgba(245, 158, 11, .35); border-radius: 10px; padding: 8px 12px; }
.topup-hint { font-size: 11.5px; color: var(--muted, #8a94a6); }
.bill-out { color: #f87171; }
.bill-note { max-width: 200px; overflow: hidden; text-overflow: ellipsis; color: var(--muted, #8a94a6); }

/* 改密码 / 会话 */
.pw-form { display: flex; gap: 8px; margin-top: 12px; flex-wrap: wrap; }
.pw-form input { flex: 1; min-width: 140px; padding: 10px 12px; border-radius: 10px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; }
.logout-row { display: flex; align-items: center; gap: 12px; margin-top: 10px; flex-wrap: wrap; }

/* 余额不足邮件提醒 */
.alert-form { margin-top: 12px; display: flex; flex-direction: column; gap: 6px; }
.alert-input-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.alert-label { font-size: 13px; color: var(--muted); }
.alert-input { width: 110px; padding: 9px 11px; border-radius: 10px; border: 1px solid var(--border, rgba(128,140,160,.3)); background: transparent; color: inherit; font-variant-numeric: tabular-nums; }
.alert-status { font-size: 13px; color: #16a34a; }
.alert-note { font-size: 12px; color: var(--muted); line-height: 1.7; }

/* ===== 响应式 ===== */
@media (max-width: 960px) {
  .ov-grid { grid-template-columns: 1fr; }
}
@media (max-width: 860px) {
  .console-shell { flex-direction: column; }
  .cside { position: static; width: 100%; flex-direction: row; align-items: center; gap: 2px; overflow-x: auto; padding: 8px; }
  .cside-user { border-bottom: none; border-right: 1px solid var(--border, rgba(128,140,160,.2)); margin: 0 6px 0 0; padding: 2px 10px 2px 2px; flex: none; }
  .cside.mini .cside-user { padding: 2px 10px 2px 2px; }
  .cside-who { display: none; }
  .cnav { flex-direction: row; gap: 2px; }
  .cnav-item { white-space: nowrap; width: auto; flex: none; }
  .cside.mini .cnav-item { padding: 9px 10px; }
  .cside.mini .cnav-label { display: inline; }
  .cside-foot { display: none; }
  .cside-fold { display: none; }
  .ov-logout { margin-left: 0; }
}
@media (max-width: 640px) {
  .key-time { display: none; }
  .keys-head { flex-direction: column; align-items: stretch; gap: 8px; }
  .keys-cta { justify-content: center; }
  .grp-pick { grid-template-columns: 1fr; }
  .mk-modal, .grp-edit-modal { width: min(440px, 94vw); }
}
</style>
