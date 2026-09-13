<script setup lang="ts">
/* 个人控制台：顶部 tab 式工作台（总览 / API 密钥 / 请求历史 / 余额充值 / 账号设置） */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'
import { apiJson, copyText, errText, fmt } from '@/composables/useApi'
import {
  avatarUrl, changeKeyGroup, changePassword, createKey, fetchBalanceAlert, fetchCheckup, isLoggedIn, listKeys, loadMe, logout,
  me, revealKey, revokeKey, setBalanceAlert, uploadAvatar, type BillingGrp, type Checkup, type KeyItem,
} from '@/composables/useAuth'

const router = useRouter()
const route = useRoute()

/* ===== 守卫 + 首屏加载 ===== */
onMounted(async () => {
  if (!isLoggedIn()) { router.replace('/login'); return }
  await loadMe()
  await Promise.all([loadKeys(), loadUsage(), loadCheckup(), loadHistory(), loadBalance(), loadPayOrders(), loadBalanceAlert()])
})

/* ===== 顶部 tab（记忆于 aqua_console_view；兼容 /console?view= 直达） ===== */
type View = 'dashboard' | 'keys' | 'history' | 'topup' | 'settings'
const TABS: { id: View; label: string; icon: string }[] = [
  { id: 'dashboard', label: '总览', icon: 'layout' },
  { id: 'keys', label: 'API 密钥', icon: 'key' },
  { id: 'history', label: '请求历史', icon: 'clock' },
  { id: 'topup', label: '余额充值', icon: 'spark' },
  { id: 'settings', label: '账号设置', icon: 'settings' },
]
const VIEWS: View[] = ['dashboard', 'keys', 'history', 'topup', 'settings']

function savedView(): View {
  const q = String(route.query.view || '')
  if (VIEWS.includes(q as View)) return q as View
  const v = localStorage.getItem('aqua_console_view')
  return VIEWS.includes(v as View) ? (v as View) : 'dashboard'
}
const view = ref<View>(savedView())
function go(v: View) {
  view.value = v
  try { localStorage.setItem('aqua_console_view', v) } catch { /* 忽略 */ }
  window.scrollTo({ top: 0 })
}

const activeKeys = computed(() => keys.value.filter(k => !k.revoked).length)
const recentItems = computed(() => historyItems.value.slice(0, 5))

/* ===== 资料（头像 / 用户名） ===== */
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
  } catch (e) { note(errText(e)) }
  finally { if (fileInput.value) fileInput.value.value = '' }
}

/* ===== 账号体检 ===== */
const checkup = ref<Checkup | null>(null)
const checkupMsg = ref('')
const checking = ref(false)

async function loadCheckup() {
  checking.value = true; checkupMsg.value = ''
  try { checkup.value = await fetchCheckup() } catch (e) { checkupMsg.value = errText(e) }
  checking.value = false
}
const scoreCls = (s: number) => (s >= 80 ? 'pos' : s >= 50 ? 'mid' : 'neg')
const levelTag: Record<string, { cls: string; label: string }> = {
  ok: { cls: 'ok', label: '通过' },
  warn: { cls: 'warn', label: '注意' },
  bad: { cls: 'bad', label: '异常' },
}

/* ===== 密钥 ===== */
const keys = ref<KeyItem[]>([])
const keysLoading = ref(false)
const newKeyName = ref('')
const newKeyGrp = ref<BillingGrp>('per_token') // 创建分组，默认按量（按次线临时下架）
const creating = ref(false)
const freshKey = ref('') // 仅创建后展示一次
const keysMsg = ref('')
const keysOk = ref(false)
const copiedId = ref(0) // 刚复制成功的密钥 id（按钮短暂反馈）
const revealedId = ref(0) // 正在查看原文的密钥 id
const revealedKey = ref('')
const grpSavingId = ref(0)

function keysNote(t: string, ok = false) { keysMsg.value = t; keysOk.value = ok }

/** 分组显示名（成功态提示用） */
function grpLabel(g: string) {
  return g === 'per_call' ? '按次计费' : g === 'per_token' ? '按量计费' : g === 'official' ? '官方中转' : g === 'free' ? '纯免费' : '未分组'
}
function grpTagCls(g?: string) {
  return g === 'per_token' ? 'ok' : g === 'free' ? 'acc' : g === 'per_call' || g === 'official' ? 'warn' : ''
}
function grpTitle(g?: string) {
  if (g === 'per_token') return '该密钥调用收费模型时按 tokens 三段计费'
  if (g === 'per_call') return '按次计费线路已下架，调用收费模型将失败——建议切换为按量分组'
  if (g === 'official') return '官方中转线路已下架，调用收费模型将失败——建议切换为按量分组'
  if (g === 'free') return '纯免费分组：仅可调用免费模型，调收费模型直接拒绝'
  return '旧式密钥未选分组，收费模型按默认分组（按次）计费'
}

async function loadKeys() {
  keysLoading.value = true
  try { keys.value = (await listKeys()).map(k => ({ ...k, billing_grp: k.billing_grp ?? '' })) } catch (e) { keysNote(errText(e)) }
  keysLoading.value = false
}

async function doCreateKey() {
  const name = newKeyName.value.trim()
  if (!name) return
  creating.value = true; keysMsg.value = ''
  try {
    const j = await createKey(name, newKeyGrp.value)
    freshKey.value = j.key
    newKeyName.value = ''
    await loadKeys()
    loadCheckup()
  } catch (e) { keysNote(errText(e)) }
  creating.value = false
}

function closeFresh() {
  freshKey.value = ''
}

function setDefaultKey() {
  if (!freshKey.value) return
  try { localStorage.setItem('aqua_default_key', freshKey.value); keysNote('已设为站内功能默认密钥（本浏览器生效）', true) } catch { /* 忽略 */ }
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
      keysNote('复制失败：浏览器剪贴板不可用，请手动保存')
    }
  } catch (e) { keysNote(errText(e)) }
}

/** 查看原文：向后端取解密原文展示（收起即清空） */
async function toggleReveal(k: KeyItem) {
  if (revealedId.value === k.id) {
    revealedId.value = 0; revealedKey.value = ''
    return
  }
  keysMsg.value = ''
  try {
    const plain = await revealKey(k.id)
    revealedId.value = k.id
    revealedKey.value = plain
  } catch (e) { keysNote(errText(e)) }
}

async function doRevoke(k: KeyItem) {
  if (!confirm(`确认吊销密钥「${k.name}」？使用它的程序会立即 401。`)) return
  try { await revokeKey(k.id); await loadKeys(); loadCheckup() } catch (e) { keysNote(errText(e)) }
}

/* ---- 密钥切换计费分组（下拉即改，立即生效，无需重建） ---- */
async function changeGrp(k: KeyItem) {
  grpSavingId.value = k.id
  keysMsg.value = ''
  try {
    await changeKeyGroup(k.id, (k.billing_grp || '') as BillingGrp)
    await loadKeys()
    keysNote('计费分组已更新，立即生效', true)
  } catch (e) {
    keysNote(errText(e))
    await loadKeys() // 失败回显真实分组
  }
  grpSavingId.value = 0
}

/* ===== 用量（总览 KPI） ===== */
interface ModelRow { model: string; width: number; val: string }
const cards = ref({ today: '--', todayRate: '--', week: '--', weekRate: '--' })
const modelRows = ref<ModelRow[]>([])
const usageMsg = ref('')

async function loadUsage() {
  try {
    const j = await apiJson<{ today: { calls: number; ok_rate: number }; week: { calls: number; ok_rate: number }; by_model?: { model: string; calls: number }[] }>('/my/usage', { session: true })
    cards.value = {
      today: fmt(j.today.calls), todayRate: j.today.ok_rate + '%',
      week: fmt(j.week.calls), weekRate: j.week.ok_rate + '%',
    }
    const byModel = j.by_model || []
    const maxc = byModel.length ? (byModel[0].calls || 1) : 1
    modelRows.value = byModel.map((m) => ({ model: m.model, width: Math.max(4, Math.round((m.calls * 100) / maxc)), val: fmt(m.calls) + ' 次' }))
  } catch (e) { usageMsg.value = errText(e) }
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
    const j = await apiJson<{ items?: HistoryItem[]; total?: number }>(`/my/history?${q}`, { session: true })
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
const streamModeLabel: Record<string, string> = {
  sim_stream: '模拟流式', passthrough: '流式透传', nonstream: '非流式',
}
const streamModeShort: Record<string, string> = {
  sim_stream: '模拟流', passthrough: '透传', nonstream: '非流',
}

function fmtTokens(n: number): string { return fmt(n || 0) }
function fmtTps(n: number): string { return n > 0 ? (Math.round(n * 100) / 100).toFixed(2) : '—' }

/* ===== 余额 / 充值（收费模型，预充值制） ===== */
interface BalanceInfo { balance_micro: number; today_cost_micro: number; total_cost_micro: number; price_micro: number | null; promo_ends_at: number; promo_active: boolean }
const balance = ref<BalanceInfo | null>(null)
const balanceMsg = ref('')
const yuan = (micro: number | null | undefined) => (micro == null ? '—' : (micro / 1_000_000).toFixed(2))

async function loadBalance() {
  try { balance.value = await apiJson<BalanceInfo>('/my/balance', { session: true }) } catch (e) { balanceMsg.value = errText(e) }
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
    const j = await apiJson<{ out_trade_no: string; pay_url: string }>('/pay/create', {
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
      const j = await apiJson<{ status: string; amount_micro: number; credit_micro?: number; channel?: 'alipay' | 'wxpay'; balance_micro: number }>(`/pay/status?out_trade_no=${payingOrder.value.out_trade_no}`, { session: true })
      if (j.status === 'paid') {
        setTopupMsg(`充值成功：支付 ¥${yuan(j.amount_micro)}，到账 ¥${yuan(j.credit_micro ?? topupCreditOf(j.amount_micro, j.channel ?? topupChannel.value))}，当前余额 ¥${yuan(j.balance_micro)}`, true)
        stopPolling()
        payingOrder.value = null
        loadBalance(); loadPayOrders()
      }
    } catch { /* 轮询失败忽略，下轮再试 */ }
  }, 3000)
}
function stopPolling() { if (pollTimer != null) { clearInterval(pollTimer); pollTimer = null } }
async function manualCheck() {
  if (!payingOrder.value) return
  try {
    const j = await apiJson<{ status: string; amount_micro: number; credit_micro?: number; channel?: 'alipay' | 'wxpay'; balance_micro: number }>(`/pay/status?out_trade_no=${payingOrder.value.out_trade_no}`, { session: true })
    if (j.status === 'paid') {
      setTopupMsg(`充值成功：支付 ¥${yuan(j.amount_micro)}，到账 ¥${yuan(j.credit_micro ?? topupCreditOf(j.amount_micro, j.channel ?? topupChannel.value))}，当前余额 ¥${yuan(j.balance_micro)}`, true)
      stopPolling(); payingOrder.value = null
      loadBalance(); loadPayOrders()
    } else { setTopupMsg('还未查询到支付结果，完成支付后稍等几秒') }
  } catch (e) { setTopupMsg(errText(e)) }
}
onUnmounted(stopPolling)

/* 充值订单记录 */
interface PayOrder { out_trade_no: string; channel: string; amount_micro: number; status: string; created_ts: number }
const payOrders = ref<PayOrder[]>([])
async function loadPayOrders() {
  try { const j = await apiJson<{ items?: PayOrder[] }>('/pay/orders', { session: true }); payOrders.value = j.items || [] } catch { /* 忽略 */ }
}
const channelLabel: Record<string, string> = { alipay: '支付宝', wxpay: '微信' }

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
  <div class="wrap" style="max-width: 1080px;">
    <div class="fade-up">
      <!-- 页头 -->
      <div class="page-head">
        <div>
          <h1><AqIcon name="user" :size="24" />个人控制台</h1>
          <div class="sub">管理你的账号、API 密钥、用量与全部请求明细</div>
        </div>
        <div class="ops">
          <button class="btn danger sm" @click="doLogout"><AqIcon name="arrow-right" :size="13" /> 退出登录</button>
        </div>
      </div>

      <!-- 顶部横向 tab 条 -->
      <div class="chips tabbar">
        <button v-for="t in TABS" :key="t.id" type="button" class="chip" :class="{ on: view === t.id }" @click="go(t.id)">
          <AqIcon :name="t.icon" :size="14" /> {{ t.label }}
        </button>
        <router-link class="chip jump" to="/usage"><AqIcon name="chart" :size="14" /> 我的用量</router-link>
        <router-link class="chip" to="/finance"><AqIcon name="bolt" :size="14" /> 消费账单</router-link>
      </div>

      <!-- ▼▼▼ 总览 ▼▼▼ -->
      <div v-show="view === 'dashboard'">
        <p v-if="usageMsg" class="msg bad">{{ usageMsg }}</p>
        <p v-if="balanceMsg" class="msg bad">{{ balanceMsg }}</p>

        <!-- KPI 行 -->
        <div class="kpis">
          <div class="kpi">
            <span>当前余额</span>
            <b>¥{{ yuan(balance?.balance_micro) }}</b>
            <span class="trend">收费模型预充值 · 失败全额退回</span>
          </div>
          <div class="kpi">
            <span>有效密钥</span>
            <b>{{ fmt(activeKeys) }}</b>
            <span class="trend">共 {{ fmt(keys.length) }} 把</span>
          </div>
          <div class="kpi">
            <span>今日调用</span>
            <b>{{ cards.today }}</b>
            <span class="trend">今日成功率 {{ cards.todayRate }}</span>
          </div>
          <div class="kpi">
            <span>近 7 天成功率</span>
            <b>{{ cards.weekRate }}</b>
            <span class="trend">近 7 天调用 {{ cards.week }} 次</span>
          </div>
        </div>

        <!-- 快捷卡 -->
        <div class="grid2 mt16">
          <div class="card hoverable">
            <b><AqIcon name="spark" :size="16" /> 余额充值</b>
            <div class="dim mt8">支付宝 / 微信在线充值，支付金额 100% 全额到账（渠道手续费由本站承担）。余额只影响收费模型 aqua/ 收费专线，其余模型完全免费。</div>
            <div class="row wrap mt12">
              <button class="btn primary sm" @click="go('topup')"><AqIcon name="spark" :size="13" /> 立即充值</button>
              <span class="tag acc">今日消费 ¥{{ yuan(balance?.today_cost_micro) }}</span>
            </div>
          </div>
          <div class="card hoverable">
            <b><AqIcon name="key" :size="16" /> API 密钥</b>
            <div class="dim mt8">一把密钥即可调用全部端点；分组决定收费模型的计费方式，随时可切换、随时可复制原文。</div>
            <div class="row wrap mt12">
              <button class="btn sm" @click="go('keys')">管理密钥</button>
              <span class="tag ok">{{ activeKeys }} 把有效</span>
            </div>
          </div>
          <div class="card hoverable">
            <b><AqIcon name="clock" :size="16" /> 最近请求</b>
            <div v-if="historyLoading && !recentItems.length" class="mt12"><div class="skeleton" style="min-height: 96px;"></div></div>
            <div v-else-if="!recentItems.length" class="empty" style="padding: 18px 0;">
              <b>暂无请求记录</b>
              <div class="dim">拿你的密钥去 Playground 聊一句</div>
            </div>
            <div v-else class="tbl-wrap mt8">
              <table class="table">
                <thead><tr><th>时间</th><th>模型</th><th>状态</th><th class="num">Tokens</th><th class="num">延迟</th></tr></thead>
                <tbody>
                  <tr v-for="(r, i) in recentItems" :key="r.ts + '-' + i">
                    <td class="dim nowrap">{{ fmtTime(r.ts) }}</td>
                    <td><span class="cell-clip" :title="r.model">{{ r.model || '—' }}</span></td>
                    <td><span class="tag" :class="r.ok ? 'ok' : 'bad'">{{ r.status_code }}</span></td>
                    <td class="num">{{ fmtTokens(r.total_tokens) }}</td>
                    <td class="num">{{ fmt(r.latency_ms) }} ms</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="mt12">
              <button class="btn ghost sm" @click="go('history')"><AqIcon name="list" :size="13" /> 查看全部明细（共 {{ fmt(historyTotal) }} 条）</button>
            </div>
          </div>
          <div class="card hoverable">
            <b><AqIcon name="stethoscope" :size="16" /> 账号体检</b>
            <div v-if="checkup" class="mt12">
              <div class="row wrap">
                <b class="grad-text score-big">{{ checkup.score }}</b>
                <div>
                  <div><span class="tag" :class="scoreCls(checkup.score) === 'pos' ? 'ok' : scoreCls(checkup.score) === 'mid' ? 'warn' : 'bad'">健康分 {{ checkup.score }}</span></div>
                  <div class="dim mt8">{{ checkup.items.filter(i => i.level === 'ok').length }}/{{ checkup.items.length }} 项通过</div>
                </div>
              </div>
              <div class="dim mt8">{{ checkup.items.find(i => i.level !== 'ok')?.advice || '一切正常，继续保持' }}</div>
            </div>
            <div v-else class="dim mt12">{{ checking ? '正在检查…' : (checkupMsg || '加载中…') }}</div>
            <div class="row wrap mt12">
              <button class="btn ghost sm" :disabled="checking" @click="loadCheckup"><AqIcon name="refresh" :size="13" /> 重新检查</button>
              <button class="btn sm" @click="go('settings')">查看详情</button>
            </div>
          </div>
        </div>
      </div>

      <!-- ▼▼▼ API 密钥 ▼▼▼ -->
      <div v-show="view === 'keys'">
        <!-- 创建表单 -->
        <div class="card">
          <b><AqIcon name="key" :size="16" /> 创建密钥</b>
          <p class="dim mt8">密钥原文仅在创建后展示一次，之后可随时在列表「复制 / 查看原文」，请妥善保管。按量分组按 tokens 三段精算、模型最全；纯免费密钥只可调免费模型，绝不产生扣费。</p>
          <div class="form-grid mt12">
            <div class="field">
              <label>密钥名称</label>
              <input v-model="newKeyName" class="input" maxlength="32" placeholder="如：我的笔记本 / 生产环境" @keydown.enter="doCreateKey" />
            </div>
            <div class="field">
              <label>计费分组（仅影响收费模型）</label>
              <select v-model="newKeyGrp" class="select">
                <option value="per_token">免费 + 按量计费（推荐）</option>
                <option value="free">纯免费（仅免费模型）</option>
              </select>
            </div>
          </div>
          <div class="row mt12">
            <button class="btn primary" :disabled="creating || !newKeyName.trim()" @click="doCreateKey">
              <AqIcon name="plus" :size="14" /> {{ creating ? '创建中…' : '创建密钥' }}
            </button>
          </div>
          <p v-if="keysMsg" class="msg mt12" :class="keysOk ? 'ok' : 'bad'">{{ keysMsg }}</p>
        </div>

        <!-- 创建成功：展示一次密钥原文 -->
        <div v-if="freshKey" class="card accent mt16">
          <b><AqIcon name="check" :size="16" /> 密钥已创建 · 分组 {{ grpLabel(newKeyGrp) }}</b>
          <div class="code mt12 key-plain">{{ freshKey }}</div>
          <div class="row wrap mt12">
            <CopyBtn :text="freshKey" label="复制密钥" />
            <button class="btn sm" @click="setDefaultKey">设为站内默认</button>
            <button class="btn ghost sm" @click="closeFresh">完成</button>
          </div>
        </div>

        <!-- 密钥列表 -->
        <div class="card mt16">
          <div class="row between wrap">
            <b><AqIcon name="list" :size="16" /> 密钥列表</b>
            <button class="btn sm" :disabled="keysLoading" @click="loadKeys"><AqIcon name="refresh" :size="13" /> 刷新</button>
          </div>
          <p class="dim mt8">acu/ 前缀为<router-link to="/pool">众筹公共模型</router-link>——任何分组密钥都可调用，按<b>官方原价</b>从站点额度扣费（充 1 元 = 2 元额度），个人余额不受影响。</p>
          <div v-if="keysLoading && !keys.length" class="mt12"><div class="skeleton" style="min-height: 120px;"></div></div>
          <div v-else-if="!keys.length" class="empty">
            <div class="big"><AqIcon name="key" :size="34" /></div>
            <b>还没有密钥</b>
            <div class="dim">创建一把密钥开始调用</div>
          </div>
          <div v-else class="tbl-wrap mt12">
            <table class="table">
              <thead><tr><th>名称</th><th>前缀</th><th>计费分组</th><th>创建时间</th><th>操作</th></tr></thead>
              <tbody>
                <template v-for="k in keys" :key="k.id">
                  <tr :class="{ 'row-dim': k.revoked }">
                    <td>
                      <div class="row wrap" style="gap: 6px;">
                        <span class="key-name">{{ k.name }}</span>
                        <span class="tag" :class="grpTagCls(k.billing_grp)" :title="grpTitle(k.billing_grp)">{{ grpLabel(k.billing_grp || '') }}</span>
                        <span v-if="k.revoked" class="tag bad">已吊销</span>
                      </div>
                    </td>
                    <td><code>{{ k.prefix }}</code></td>
                    <td>
                      <select
                        v-if="!k.revoked" v-model="k.billing_grp" class="select grp-select"
                        :disabled="grpSavingId === k.id" @change="changeGrp(k)"
                      >
                        <option value="" disabled>未分组（旧密钥）</option>
                        <option value="per_token">按量计费（推荐）</option>
                        <option value="per_call">按次计费（已下架）</option>
                        <option value="official">官方中转（已下架）</option>
                        <option value="free">纯免费</option>
                      </select>
                      <span v-else class="dim">{{ grpLabel(k.billing_grp || '') }}</span>
                    </td>
                    <td class="dim nowrap">{{ fmtTime(k.created_ts) }}</td>
                    <td>
                      <div v-if="!k.revoked" class="row wrap" style="gap: 6px;">
                        <button v-if="k.can_reveal !== false" class="btn xs" @click="copyKey(k)">
                          <AqIcon :name="copiedId === k.id ? 'check' : 'copy'" :size="12" /> {{ copiedId === k.id ? '已复制' : '复制' }}
                        </button>
                        <span v-else class="tag warn" title="旧版密钥未存原文，无法查看">旧密钥</span>
                        <button v-if="k.can_reveal !== false" class="btn xs" @click="toggleReveal(k)">
                          <AqIcon name="eye" :size="12" /> {{ revealedId === k.id ? '收起' : '查看原文' }}
                        </button>
                        <button class="btn xs danger" @click="doRevoke(k)">吊销</button>
                      </div>
                      <span v-else class="dim">—</span>
                    </td>
                  </tr>
                  <!-- 查看原文展开行 -->
                  <tr v-if="revealedId === k.id">
                    <td colspan="5">
                      <div class="code key-plain">{{ revealedKey }}</div>
                      <div class="row wrap mt8">
                        <CopyBtn :text="revealedKey" label="复制原文" size="xs" />
                        <span class="dim">密钥原文，请妥善保管，泄露请立即吊销重建</span>
                      </div>
                    </td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <!-- ▼▼▼ 请求历史 ▼▼▼ -->
      <div v-show="view === 'history'">
        <div class="card">
          <div class="row between wrap">
            <b><AqIcon name="clock" :size="16" /> 请求历史 <span class="dim">（每条请求全字段明细 · 保留 90 天 · 共 {{ fmt(historyTotal) }} 条）</span></b>
            <button class="btn sm" :disabled="historyLoading" @click="loadHistory"><AqIcon name="refresh" :size="13" /> {{ historyLoading ? '刷新中…' : '刷新' }}</button>
          </div>

          <p v-if="historyMsg && !historyItems.length && !historyLoading" class="msg bad mt12">{{ historyMsg }}</p>
          <div v-if="historyLoading && !historyItems.length" class="mt12" style="display: grid; gap: 8px;">
            <div class="skeleton" style="min-height: 30px;"></div>
            <div class="skeleton" style="min-height: 30px;"></div>
            <div class="skeleton" style="min-height: 30px;"></div>
          </div>
          <div v-else-if="!historyItems.length" class="empty">
            <div class="big"><AqIcon name="clock" :size="34" /></div>
            <b>暂无请求记录</b>
            <div class="dim">拿你的密钥去 Playground 聊一句</div>
          </div>
          <div v-else class="tbl-wrap mt12">
            <table class="table table-wide">
              <thead>
                <tr>
                  <th>时间</th><th>端点</th><th>模型</th><th>状态</th>
                  <th class="num">输入</th><th class="num">输出</th><th class="num">缓存</th><th class="num">总计</th>
                  <th class="num">Tok/s</th><th class="num">延迟</th><th>传输</th><th>计费</th><th>来源</th><th>错误</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(r, i) in historyItems" :key="r.ts + '-' + i" :class="{ 'row-fail': !r.ok }">
                  <td class="dim nowrap">{{ fmtTime(r.ts) }}</td>
                  <td class="nowrap">{{ r.endpoint }}</td>
                  <td><span class="cell-clip" :title="r.model">{{ r.model || '—' }}</span></td>
                  <td><span class="tag" :class="r.ok ? 'ok' : 'bad'">{{ r.status_code }}</span></td>
                  <td class="num">{{ fmtTokens(r.prompt_tokens) }}</td>
                  <td class="num">{{ fmtTokens(r.completion_tokens) }}</td>
                  <td class="num" :class="{ 'col-acc': r.cached_tokens > 0 }">{{ fmtTokens(r.cached_tokens) }}</td>
                  <td class="num">{{ fmtTokens(r.total_tokens) }}</td>
                  <td class="num">{{ fmtTps(r.tps) }}</td>
                  <td class="num">{{ fmt(r.latency_ms) }} ms</td>
                  <td><span class="dim" :title="streamModeLabel[r.stream_mode] || ''">{{ r.stream_mode ? (streamModeShort[r.stream_mode] || r.stream_mode) : '—' }}</span></td>
                  <td>
                    <span v-if="r.billed" class="col-bad nowrap">¥{{ yuan(r.bill_amount_micro) }}</span>
                    <span v-else-if="r.bill_state === 'refunded'" class="tag ok" title="失败已退回">已退</span>
                    <span v-else class="dim">—</span>
                  </td>
                  <td><span v-if="r.usage_source === 'upstream'" class="tag ok">{{ sourceLabel(r.usage_source) }}</span><span v-else class="dim">{{ sourceLabel(r.usage_source) }}</span></td>
                  <td><span class="cell-clip col-bad" :title="r.error">{{ r.error || '—' }}</span></td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-if="historyPages > 1 || historyPage > 1" class="paged">
            <button class="btn sm" :disabled="historyPage <= 1 || historyLoading" @click="goHistoryPage(1)">首页</button>
            <button class="btn sm" :disabled="historyPage <= 1 || historyLoading" @click="goHistoryPage(historyPage - 1)">上一页</button>
            <span class="cur">第 {{ historyPage }} / {{ historyPages }} 页</span>
            <button class="btn sm" :disabled="historyPage >= historyPages || historyLoading" @click="goHistoryPage(historyPage + 1)">下一页</button>
            <button class="btn sm" :disabled="historyPage >= historyPages || historyLoading" @click="goHistoryPage(historyPages)">末页</button>
          </div>
        </div>
      </div>

      <!-- ▼▼▼ 余额充值 ▼▼▼ -->
      <div v-show="view === 'topup'">
        <div class="banner warn">
          <AqIcon name="info" :size="14" />
          <span>在线充值支付金额 <b>100% 全额到账</b>（渠道手续费由本站承担）；充值余额用于收费模型（aqua/ 前缀，按量计费）扣费，免费模型不受影响。<b>「充 1 = 2」充值翻倍仅限众筹公共池</b>——想用 acu/ 众筹模型请到 <router-link to="/pool">众筹池充值</router-link>。</span>
        </div>

        <!-- 在线充值 -->
        <div class="card mt16">
          <b><AqIcon name="spark" :size="16" /> 在线充值（支付宝 / 微信）</b>
          <div class="chips mt12">
            <button v-for="p in TOPUP_PRESETS" :key="p" type="button" class="chip" :class="{ on: Number(topupAmt) === p }" @click="topupAmt = String(p)">¥{{ p }}</button>
          </div>
          <div class="form-grid mt12">
            <div class="field">
              <label>自定义金额（0.01 ~ 1000 元）</label>
              <input v-model="topupAmt" class="input" type="number" min="0.01" max="1000" step="0.01" placeholder="如 10" />
            </div>
            <div class="field">
              <label>支付渠道</label>
              <div class="chips">
                <button type="button" class="chip" :class="{ on: topupChannel === 'alipay' }" @click="topupChannel = 'alipay'"><AqIcon name="wallet" :size="13" /> 支付宝</button>
                <button type="button" class="chip" :class="{ on: topupChannel === 'wxpay' }" @click="topupChannel = 'wxpay'"><AqIcon name="chat" :size="13" /> 微信支付</button>
              </div>
            </div>
          </div>
          <div class="row wrap mt12">
            <button class="btn primary" :disabled="paying || !topupAmt" @click="createTopup">
              {{ paying ? '创建中…' : '去支付' }}
            </button>
            <span class="dim">当前余额 ¥{{ yuan(balance?.balance_micro) }} · 收费模型按量计费，余额不足时收费模型返回 402，免费模型照常可用</span>
          </div>
          <p v-if="topupCreditPreview > 0" class="dim mt8">
            支付 ¥{{ topupAmt }}，预计到账余额 <b class="col-ok">¥{{ yuan(topupCreditPreview) }}</b>（100% 全额到账，渠道手续费由本站承担）
          </p>
          <p v-if="topupMsg" class="msg mt12" :class="topupOk ? 'ok' : 'bad'">{{ topupMsg }}</p>
          <div v-if="payingOrder" class="banner warn mt12 row wrap">
            <span>订单 {{ payingOrder.out_trade_no }}（支付 ¥{{ yuan(payingOrder.amount_micro) }}，到账 ¥{{ yuan(payingOrder.credit_micro) }}）等待支付中——在新窗口完成支付后，这里会自动到账（每 3 秒检测）。</span>
            <button class="btn xs" @click="manualCheck">我已支付，立即查询</button>
            <button class="btn xs ghost" @click="stopPolling(); payingOrder = null">取消检测</button>
          </div>
          <p class="dim mt8">支付成功后自动入账；到账前请勿关闭本页。收费模型按量计费（实时单价见模型中心收费专区），余额低于阈值可在「账号设置」开启邮件提醒。</p>
          <p class="dim mt8 pay-help">
            <b class="col-warn">支付遇到问题？</b>已支付但余额未到账、重复扣款、金额有误——请勿重复支付，保留支付凭证（账单截图 / 商户单号），
            <a href="https://pd.qq.com/s/e4ktxw1b8" target="_blank" rel="noopener">加入 QQ 频道</a> 或
            <a href="https://qm.qq.com/cgi-bin/qm/qr?k=&jump_from=&group=1103667832" target="_blank" rel="noopener">加入 QQ 一群（1103667832）</a>
            / <a href="https://qm.qq.com/q/o8QDbza2Ge" target="_blank" rel="noopener">二群（1006740220）</a>
            联系站长人工核实补账。
          </p>
        </div>

        <!-- 充值记录 -->
        <div class="card mt16">
          <b><AqIcon name="list" :size="16" /> 充值记录</b>
          <div class="tbl-wrap mt12">
            <table class="table">
              <thead><tr><th>时间</th><th>订单号</th><th>渠道</th><th class="num">金额</th><th>状态</th></tr></thead>
              <tbody>
                <tr v-if="!payOrders.length">
                  <td colspan="5" class="empty">暂无充值记录——完成第一笔在线充值后这里会出现记录</td>
                </tr>
                <tr v-for="o in payOrders" :key="o.out_trade_no">
                  <td class="dim nowrap">{{ fmtTime(o.created_ts) }}</td>
                  <td><code>{{ o.out_trade_no }}</code></td>
                  <td>{{ channelLabel[o.channel] || o.channel }}</td>
                  <td class="num">¥{{ yuan(o.amount_micro) }}</td>
                  <td><span class="tag" :class="o.status === 'paid' ? 'ok' : 'warn'">{{ o.status === 'paid' ? '已到账' : '待支付' }}</span></td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <!-- ▼▼▼ 账号设置 ▼▼▼ -->
      <div v-show="view === 'settings'" class="grid2">
        <!-- 个人资料 -->
        <div class="card">
          <b><AqIcon name="user" :size="16" /> 个人资料</b>
          <div class="row wrap mt12">
            <div class="avatar-box" title="点击更换头像" @click="fileInput?.click()">
              <img v-if="me?.avatar_ext" :src="avatarUrl() + '?t=' + avatarBust" alt="头像" />
              <span v-else class="avatar-fallback">{{ (me?.username || '?').slice(0, 1) }}</span>
              <span class="avatar-edit">更换</span>
            </div>
            <input ref="fileInput" type="file" accept="image/png,image/jpeg,image/webp" style="display: none;" @change="onPickAvatar" />
            <div class="profile-main">
              <template v-if="!nameEdit">
                <b class="profile-name">{{ me?.username || '…' }}</b>
                <div class="row mt8">
                  <button class="btn xs" @click="nameEdit = true; nameDraft = me?.username || ''">改名</button>
                </div>
              </template>
              <template v-else>
                <div class="row wrap">
                  <input v-model="nameDraft" class="input name-input" maxlength="20" @keydown.enter="saveName" />
                  <button class="btn xs primary" @click="saveName">保存</button>
                  <button class="btn xs ghost" @click="nameEdit = false">取消</button>
                </div>
              </template>
              <div class="dim mt8">UID #{{ me?.id }} · {{ me?.email }}</div>
              <div class="dim">注册于 {{ me ? fmtTime(me.created_ts) : '…' }}</div>
            </div>
          </div>
          <p v-if="profileMsg" class="msg mt12" :class="profileOk ? 'ok' : 'bad'">{{ profileMsg }}</p>
        </div>

        <!-- 修改密码 -->
        <div class="card">
          <b><AqIcon name="lock" :size="16" /> 修改密码</b>
          <p class="dim mt8">修改成功后全端会话注销，需重新登录。</p>
          <div class="form-grid mt12">
            <div class="field">
              <label>当前密码</label>
              <input v-model="oldPw" class="input" type="password" placeholder="当前密码" autocomplete="current-password" />
            </div>
            <div class="field">
              <label>新密码</label>
              <input v-model="newPw" class="input" type="password" placeholder="8~72 位，含字母和数字" autocomplete="new-password" />
            </div>
          </div>
          <div class="row mt12">
            <button class="btn primary" :disabled="!oldPw || !newPw" @click="doChangePw">修改</button>
          </div>
          <p v-if="pwMsg" class="msg mt12" :class="pwOk ? 'ok' : 'bad'">{{ pwMsg }}</p>
        </div>

        <!-- 余额不足邮件提醒 -->
        <div class="card">
          <b><AqIcon name="mail" :size="16" /> 余额不足邮件提醒</b>
          <p class="dim mt8">余额低于阈值时发邮件（发送至 {{ me?.email || '绑定邮箱' }}），避免调用中断；充值使余额回到阈值上方后，下次低于阈值会再次提醒。收费模型余额不足时调用将返回 402。</p>
          <div class="row wrap mt12">
            <span class="dim">余额低于</span>
            <input v-model="alertYuan" class="input alert-input" type="number" min="0" max="100" step="0.01" placeholder="如 1.00" @keydown.enter="doSaveAlert" />
            <span class="dim">元时，提醒我</span>
            <button class="btn primary sm" :disabled="alertSaving" @click="doSaveAlert">{{ alertSaving ? '保存中…' : '保存' }}</button>
            <button v-if="alertThreshold > 0" class="btn sm" :disabled="alertSaving" @click="disableAlert">关闭提醒</button>
          </div>
          <p class="mt8">
            <span class="tag" :class="alertThreshold > 0 ? 'ok' : ''">当前状态：{{ alertThreshold > 0 ? `已开启 · 阈值 ¥${yuan(alertThreshold)}` : '未开启' }}</span>
          </p>
          <p v-if="alertMsg" class="msg mt12" :class="alertOk ? 'ok' : 'bad'">{{ alertMsg }}</p>
        </div>

        <!-- 账号体检 -->
        <div class="card">
          <div class="row between wrap">
            <b><AqIcon name="stethoscope" :size="16" /> 账号体检</b>
            <button class="btn sm" :disabled="checking" @click="loadCheckup"><AqIcon name="refresh" :size="13" /> {{ checking ? '检查中…' : '重新检查' }}</button>
          </div>
          <div v-if="checkup" class="mt12">
            <div class="row wrap mb12">
              <b class="grad-text score-big">{{ checkup.score }}</b>
              <div>
                <div><span class="tag" :class="scoreCls(checkup.score) === 'pos' ? 'ok' : scoreCls(checkup.score) === 'mid' ? 'warn' : 'bad'">健康分 {{ checkup.score }}</span></div>
                <div class="dim mt8">{{ checkup.items.filter(i => i.level === 'ok').length }}/{{ checkup.items.length }} 项通过</div>
              </div>
            </div>
            <div class="checkup-list">
              <div v-for="it in checkup.items" :key="it.id" class="row wrap checkup-item">
                <AqIcon :name="it.level === 'ok' ? 'check' : it.level === 'warn' ? 'alert' : 'cross'" :size="15" />
                <span class="tag" :class="levelTag[it.level]?.cls">{{ levelTag[it.level]?.label || it.level }}</span>
                <span><b>{{ it.title }}</b> <span class="dim">{{ it.detail }}</span></span>
                <span v-if="it.advice" class="dim checkup-advice">{{ it.advice }}</span>
              </div>
            </div>
          </div>
          <div v-else class="dim mt12">{{ checking ? '正在检查…' : (checkupMsg || '加载中…') }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 布局微调：tab 条 / 行高亮 / 单元格截断 / 头像 / 条形 */
.tabbar { margin: 4px 0 18px; }
.tabbar .jump { margin-left: auto; text-decoration: none; }
.score-big { font-size: 36px; line-height: 1.1; }
.pos { color: var(--ok); }
.mid { color: var(--warn); }
.neg { color: var(--bad); }
tr.row-dim td { opacity: .55; }
tr.row-fail td { background: var(--bad-soft); }
.nowrap { white-space: nowrap; }
.cell-clip { display: inline-block; max-width: 180px; overflow: hidden; text-overflow: ellipsis; vertical-align: bottom; }
.col-acc { color: var(--acc-3); }
.col-bad { color: var(--bad); }
.col-ok { color: var(--ok); }
.col-warn { color: var(--warn); }
.key-plain { word-break: break-all; font-weight: 700; color: var(--acc); }
.key-name { font-weight: 600; }
.grp-select { width: auto; min-width: 168px; }
.checkup-list { display: flex; flex-direction: column; gap: 8px; }
.checkup-item { gap: 8px; font-size: 13px; padding: 7px 10px; border: 1px solid var(--line); border-radius: var(--r-sm); }
.checkup-advice { flex-basis: 100%; padding-left: 23px; }
.avatar-box {
  position: relative; width: 72px; height: 72px; border-radius: 50%; overflow: hidden;
  cursor: pointer; flex-shrink: 0; background: var(--acc);
  display: flex; align-items: center; justify-content: center;
}
.avatar-box img { width: 100%; height: 100%; object-fit: cover; }
.avatar-fallback { color: var(--on-acc); font-size: 30px; font-weight: 800; }
.avatar-edit {
  position: absolute; bottom: 0; left: 0; right: 0; padding: 3px 0;
  background: color-mix(in srgb, var(--bg0) 60%, transparent);
  color: var(--txt0); font-size: 10.5px; text-align: center; opacity: 0; transition: opacity .15s;
}
.avatar-box:hover .avatar-edit { opacity: 1; }
.profile-name { font-size: 17px; }
.name-input { width: 180px; }
.alert-input { width: 110px; font-variant-numeric: tabular-nums; }
.pay-help { font-size: 12px; line-height: 1.7; }
@media (max-width: 640px) {
  .tabbar .jump { margin-left: 0; }
}
</style>
