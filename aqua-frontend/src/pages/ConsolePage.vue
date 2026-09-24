<script setup lang="ts">
/* 个人控制台：顶部 tab 式工作台（总览 / API 密钥 / 请求历史 / 余额充值 / 账号设置） */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'
import { apiJson, copyText, errText, fmt } from '@/composables/useApi'
import { useMeta } from '@/composables/useMeta'
import {
  avatarUrl, changeKeyGroup, changePassword, createKey, fetchBalanceAlert, fetchCheckup, isLoggedIn, listKeys, loadMe, logout,
  me, revealKey, revokeKey, setBalanceAlert, setKeyQuota, resetKeyQuota, uploadAvatar,
  listDomains, addDomain, verifyDomain, uploadDomainCert, issueDomainCert, deleteDomain,
  type BillingGrp, type Checkup, type KeyItem, type DomainItem,
} from '@/composables/useAuth'

const router = useRouter()
const route = useRoute()

/* ===== 守卫 + 首屏加载 ===== */
/* 站点元信息（20260924）：控制台需读 announcement（余额使用期限提醒）与 pay_enabled（停售置灰）。
   与首页共用同一份 /v1/meta 单例，公告文案后台改完两处同时生效。 */
const { meta, loadMeta } = useMeta()
const announcementText = computed(() => (meta.value?.announcement_enabled ? meta.value?.announcement || '' : ''))
/* pay_enabled 语义：后端显式 false 才停售；undefined（老后端/未加载）一律按开放，
   避免前端先于后端上线时把充值入口误置灰。 */
const payDisabled = computed(() => meta.value?.pay_enabled === false)

onMounted(async () => {
  if (!isLoggedIn()) { router.replace('/login'); return }
  await loadMe()
  await Promise.all([loadKeys(), loadUsage(), loadCheckup(), loadHistory(), loadBalance(), loadPayOrders(), loadBalanceAlert(), loadInvite(), loadDomains(), loadPool(), loadMeta()])
  // 从支付平台回跳（return_url → /pay/return → /console）时恢复待支付订单并继续轮询到账
  restorePendingPay()
})

/* ===== 顶部 tab（记忆于 aqua_console_view；兼容 /console?view= 直达） ===== */
type View = 'dashboard' | 'keys' | 'domains' | 'history' | 'topup' | 'crowd' | 'invite' | 'settings'
const TABS: { id: View; label: string; icon: string }[] = [
  { id: 'dashboard', label: '总览', icon: 'layout' },
  { id: 'keys', label: 'API 密钥', icon: 'key' },
  { id: 'domains', label: '自定义域名', icon: 'server' },
  { id: 'history', label: '请求历史', icon: 'clock' },
  { id: 'topup', label: '余额充值', icon: 'spark' },
  { id: 'crowd', label: '众筹算力池', icon: 'users' },
  { id: 'invite', label: '邀请返利', icon: 'send' },
  { id: 'settings', label: '账号设置', icon: 'settings' },
]
const VIEWS: View[] = ['dashboard', 'keys', 'domains', 'history', 'topup', 'crowd', 'invite', 'settings']

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
  // 进入众筹页时拉取池子公开状态 + 我的众筹明细（进入即最新）
  if (v === 'crowd') { loadPool(); loadPoolMine() }
  window.scrollTo({ top: 0 })
}

const activeKeys = computed(() => keys.value.filter(k => !k.revoked).length)
const revokedKeys = computed(() => keys.value.filter(k => k.revoked).length)
/* 默认只显示生效中的密钥：吊销后该行立即从列表消失（此前是置灰留在表里，用户以为"没删掉"）。
 * 已吊销的收进「显示已吊销」开关，既符合直觉又保留可追溯的痕迹。 */
const showRevoked = ref(false)
const visibleKeys = computed(() => showRevoked.value ? keys.value : keys.value.filter(k => !k.revoked))
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
const newKeyGrp = ref<BillingGrp>('per_call') // 创建分组，默认「免费 + 收费」（收费线 aqua/）
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
  return g === 'per_call' ? '免费 + 按次' : g === 'per_token' ? '免费 + 按量' : g === 'official' ? '官方中转' : g === 'free' ? '纯免费' : '未分组'
}
function grpTagCls(g?: string) {
  return g === 'per_call' ? 'ok' : g === 'free' ? 'acc' : g === 'per_token' || g === 'official' ? 'warn' : ''
}
function grpTitle(g?: string) {
  if (g === 'per_call') return '免费 + 按次计费分组：acu/ 官方自营纯免费，aqua/ 按次模型成功请求按次从个人余额结算'
  if (g === 'per_token') return '免费 + 按量计费分组：aqua/ 官方原版直连专线（输入 / 缓存命中 / 输出分段计价），codex/ 账号池专线'
  if (g === 'official') return '官方中转分组已下架：该分组无法调用收费模型，请在下方切换为「免费 + 按次计费」'
  if (g === 'free') return '纯免费分组：仅可调用免费模型，调收费模型直接拒绝'
  return '旧式密钥未选分组，收费模型按默认分组（按次）计费'
}

async function loadKeys() {
  keysLoading.value = true
  try { keys.value = (await listKeys()).map(k => ({ ...k, billing_grp: k.billing_grp ?? '' })) } catch (e) { keysNote(errText(e)) }
  keysLoading.value = false
}

/* ===== 自定义域名与证书（P5） ===== */
const domains = ref<DomainItem[]>([])
const domainsLoading = ref(false)
const domainEnabled = ref(false)
const domainCname = ref('')
const domainMax = ref(2)
const leAvailable = ref(false)
const domainMsg = ref('')
const domainOk = ref(false)
const newDomain = ref('')
const domainAdding = ref(false)
const domainBusyId = ref(0)
// 证书上传面板（每个域名独立展开）
const certUploadId = ref(0)
const certFullchain = ref('')
const certPrivkey = ref('')

function domainNote(t: string, ok = false) { domainMsg.value = t; domainOk.value = ok }

async function loadDomains() {
  domainsLoading.value = true
  try {
    const d = await listDomains()
    domains.value = d.domains
    domainEnabled.value = d.enabled
    domainCname.value = d.cname
    domainMax.value = d.max
    leAvailable.value = d.le_available
  } catch (e) { domainNote(errText(e)) }
  domainsLoading.value = false
}

const domainStatusTag: Record<string, { cls: string; label: string }> = {
  active: { cls: 'ok', label: '已生效' },
  pending: { cls: 'warn', label: '待激活' },
  failed: { cls: 'bad', label: '校验失败' },
  disabled: { cls: 'bad', label: '已停用' },
}
function domainTag(s: string) { return domainStatusTag[s] || { cls: '', label: s } }
function certLabel(d: DomainItem) {
  if (!d.cert_type) return '未配置证书'
  const base = d.cert_type === 'letsencrypt' ? '平台签发' : '自传证书'
  if (d.cert_days_left != null && d.cert_days_left >= 0) return base + ' · 剩余 ' + d.cert_days_left + ' 天'
  return base
}

/** 添加域名（test=true 即「测试并添加」） */
async function doAddDomain(test: boolean) {
  const d = newDomain.value.trim().toLowerCase()
  if (!d) { domainNote('请输入域名'); return }
  domainAdding.value = true; domainNote('')
  try {
    const r = await addDomain(d, test)
    domainNote(r.message || '已添加', true)
    newDomain.value = ''
    await loadDomains()
  } catch (e) { domainNote(errText(e)) }
  domainAdding.value = false
}

/** 重新校验 DNS（并尝试激活） */
async function doVerifyDomain(d: DomainItem) {
  domainBusyId.value = d.id; domainNote('')
  try {
    const r = await verifyDomain(d.id)
    domainNote(r.message || '校验通过', true)
    await loadDomains()
  } catch (e) { domainNote(errText(e)) }
  domainBusyId.value = 0
}

/** 上传证书（用户自传） */
async function doUploadCert(d: DomainItem) {
  if (!certFullchain.value.trim() || !certPrivkey.value.trim()) {
    domainNote('请粘贴证书链（fullchain.pem）与私钥（privkey.pem）内容')
    return
  }
  domainBusyId.value = d.id; domainNote('')
  try {
    const r = await uploadDomainCert(d.id, certFullchain.value, certPrivkey.value)
    domainNote(r.message || '证书已保存', true)
    certUploadId.value = 0
    certFullchain.value = ''
    certPrivkey.value = ''
    await loadDomains()
  } catch (e) { domainNote(errText(e)) }
  domainBusyId.value = 0
}

/** 平台自动签发（Let's Encrypt） */
async function doIssueCert(d: DomainItem) {
  domainBusyId.value = d.id; domainNote('签发中，请稍候（约 10~60 秒）…')
  try {
    const r = await issueDomainCert(d.id)
    domainNote(r.message || '证书已签发', true)
    await loadDomains()
  } catch (e) { domainNote(errText(e)) }
  domainBusyId.value = 0
}

/** 删除域名 */
async function doDeleteDomain(d: DomainItem) {
  if (!window.confirm('删除域名 ' + d.domain + '？其 nginx 配置会被清理，已签发的证书不再对外服务。')) return
  domainBusyId.value = d.id; domainNote('')
  try {
    await deleteDomain(d.id)
    domainNote('域名已删除', true)
    await loadDomains()
  } catch (e) { domainNote(errText(e)) }
  domainBusyId.value = 0
}

/* ===== 密钥分发配额（P4）：限额 / 重置周期 / 倍率 / 有效期 / 备注 ===== */
const quotaEditId = ref(0)
const quotaSaving = ref(false)
const quotaMsg = ref('')
const quotaOk = ref(false)
const quotaDraft = ref({ type: '', limitInput: '', reset: '', rate: '1', expiresDate: '', note: '' })

/** 配额使用百分比（用于进度条；无限额返回 0） */
function quotaPct(k: KeyItem): number {
  if (!k.limit || !k.used) return 0
  const p = Math.round((k.used / k.limit) * 100)
  return p > 100 ? 100 : p
}
/** 倍率值（rate_num / rate_den；缺省 1） */
function quotaRateVal(k: KeyItem): number {
  const n = k.rate_num || 1
  const d = k.rate_den || 1
  return d ? n / d : 1
}
/** 微元 → 元（配额展示用，6 位小数去尾零） */
function microYuan(v: number): string {
  return (v / 1e6).toFixed(6).replace(/0+$/, '').replace(/\.$/, '')
}
/** unix 秒 → date input 值（YYYY-MM-DD，本地时区） */
function tsToDateInput(ts?: number): string {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  const p = (x: number) => (x < 10 ? '0' : '') + x
  return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate())
}
/** 打开额度设置面板：把当前值回填草稿 */
function openQuota(k: KeyItem) {
  quotaMsg.value = ''
  if (quotaEditId.value === k.id) { quotaEditId.value = 0; return }
  quotaEditId.value = k.id
  quotaDraft.value = {
    type: k.type || '',
    // 按金额时后端存微元，前端展示元
    limitInput: k.limit ? (k.type === 'amount' ? microYuan(k.limit) : String(k.limit)) : '',
    reset: k.reset || '',
    rate: String(quotaRateVal(k) === 1 ? 1 : quotaRateVal(k)),
    expiresDate: tsToDateInput(k.expires_at),
    note: k.note || '',
  }
}
/** 保存配额 */
async function saveQuota(k: KeyItem) {
  quotaSaving.value = true; quotaMsg.value = ''
  try {
    const d = quotaDraft.value
    const limitNum = Number(d.limitInput)
    const body: any = {
      quota_type: d.type,
      quota_reset: d.reset,
      note: d.note.trim(),
    }
    if (d.type) {
      if (!Number.isFinite(limitNum) || limitNum <= 0) {
        quotaMsg.value = '请填写大于 0 的限额值'; quotaOk.value = false; quotaSaving.value = false; return
      }
      // 按金额：元 → 微元（整数，零浮点红线：先乘后取整）
      body.quota_limit = d.type === 'amount' ? Math.round(limitNum * 1e6) : Math.round(limitNum)
    } else {
      body.quota_limit = 0
    }
    // 倍率（支持小数如 2.5 → 25/10，整数运算零浮点）
    const rv = Number(d.rate || '1')
    if (Number.isFinite(rv) && rv > 0) {
      body.rate_num = Math.round(rv * 100)
      body.rate_den = 100
    }
    // 有效期（日期 → 当日 23:59:59 的 unix 秒）
    if (d.expiresDate) {
      body.expires_at = Math.floor(new Date(d.expiresDate + 'T23:59:59').getTime() / 1000)
    } else {
      body.expires_at = 0
    }
    await setKeyQuota(k.id, body)
    quotaMsg.value = '已保存，立即生效'; quotaOk.value = true
    await loadKeys()
  } catch (e) { quotaMsg.value = errText(e); quotaOk.value = false }
  quotaSaving.value = false
}
/** 手动清零本周期用量 */
async function doResetQuota(k: KeyItem) {
  quotaSaving.value = true; quotaMsg.value = ''
  try {
    await resetKeyQuota(k.id)
    quotaMsg.value = '本周期用量已清零'; quotaOk.value = true
    await loadKeys()
  } catch (e) { quotaMsg.value = errText(e); quotaOk.value = false }
  quotaSaving.value = false
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
  if (!confirm(`确认吊销密钥「${k.name}」？使用它的程序会立即 401。\n\n吊销后该密钥不再出现在列表中（可在「显示已吊销」里回看），且无法恢复。`)) return
  try {
    await revokeKey(k.id)
    await loadKeys()
    loadCheckup()
    keysNote('密钥已吊销：使用它的程序会立即 401，已从列表移除', true)
  } catch (e) { keysNote(errText(e)) }
}

/* ---- 密钥切换计费分组（下拉即改，立即生效，无需重建） ---- */
async function changeGrp(k: KeyItem) {
  grpSavingId.value = k.id
  keysMsg.value = ''
  try {
    await changeKeyGroup(k.id, (k.billing_grp || '') as BillingGrp)
    await loadKeys()
    // 分组可调范围说明（20260919：用户切分组后调错模型报 403/404 的困惑源——
    // 切换即生效，但各分组可调模型集不同，必须把范围说清）
    const scope = (k.billing_grp || '') === 'per_call'
      ? '可调用 aqua/ 按次模型与全部免费模型'
      : (k.billing_grp || '') === 'per_token'
        ? '可调用 tide/ 按量模型；aqua/ 按次模型请用「免费 + 收费」分组密钥'
        : (k.billing_grp || '') === 'free'
          ? '仅可调用免费模型，调用收费模型会被拒绝'
          : '未分组旧密钥按「免费 + 收费」默认分组计费'
    keysNote(`计费分组已更新，立即生效：${scope}`, true)
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
/* 20260924 双钱包：balance_micro=主钱包（全部模型·正常价）；balance2_micro=2 号折扣钱包
   （仅折扣模型·0.5 折）。两者资金**完全独立**，不支持互转——折扣钱包只能独立充值。 */
interface BalanceInfo {
  balance_micro: number; today_cost_micro: number; total_cost_micro: number
  balance2_micro: number; today_cost2_micro: number; total_cost2_micro: number
  price_micro: number | null; promo_ends_at: number; promo_active: boolean
}
const balance = ref<BalanceInfo | null>(null)
const balanceMsg = ref('')
const yuan = (micro: number | null | undefined) => (micro == null ? '—' : (micro / 1_000_000).toFixed(6).replace(/\.?0+$/, '') || '0')

async function loadBalance() {
  try { balance.value = await apiJson<BalanceInfo>('/my/balance', { session: true }) } catch (e) { balanceMsg.value = errText(e) }
}

/* ===== 在线充值（双通道：微信=自建通道为主，异常自动降级易支付；支付宝=易支付） =====
 * 20260922 站长定稿：不做站内扫码（既不用后端渲染二维码，也不 iframe 嵌收银台），
 * 一律「创建订单 → **正常跳转**平台收银台 → 平台回调 + return_url 回跳」。
 * 跳走时本页已卸载，故待支付订单存 localStorage，回跳后恢复上下文并轮询确认到账。
 * 自建通道订单只有 **5 分钟**窗口 → 必须显示倒计时 + 到期给「重新下单」一键，
 * 否则用户回来会发现"付不了"，表现为"这按钮怎么没反应"。 */
const topupAmt = ref('')
/* 2 号折扣钱包独立充值金额（20260924）：与主钱包充值金额分开记，
   避免用户切 tab 时金额串味、误充到非预期钱包 */
const wallet2Amt = ref('')
const wallet2Msg = ref('')
const wallet2Ok = ref(false)
const wallet2Paying = ref(false)
const topupChannel = ref<'alipay' | 'wxpay'>('alipay')
const TOPUP_PRESETS = [1, 5, 10, 50, 100]
const topupMsg = ref('')
const topupOk = ref(false)
const paying = ref(false)
type PendingPay = {
  out_trade_no: string; amount_micro: number; credit_micro: number
  pay_url: string; channel: 'alipay' | 'wxpay'
  expire_ts: number   // 平台侧订单失效时刻（自建通道才有；0=不适用）
  provider: string    // epay | xiaofeng（仅用于「备用通道」提示，用户无需操作）
  fallback: boolean   // 是否走了备用通道（主通道配的是自建通道、本次降级到易支付）
}
const payingOrder = ref<PendingPay | null>(null)
/* 待支付订单的充值去向：决定到账文案与回跳后停在哪个页签
   （20260924 新增 wallet2：2 号折扣钱包独立充值，与主钱包资金不互通） */
type PayProduct = 'balance' | 'wallet2' | 'pool'
const payProduct = ref<PayProduct>('balance')
let pollTimer: number | null = null
let tickTimer: number | null = null
const nowSec = ref(Math.floor(Date.now() / 1000))
/* 自建通道订单剩余有效期（秒）；0 = 不适用或已失效 */
const payRemainSecs = computed(() => {
  const e = payingOrder.value?.expire_ts || 0
  return e ? Math.max(0, e - nowSec.value) : 0
})
const payExpired = computed(() => !!payingOrder.value?.expire_ts && payRemainSecs.value <= 0)
/* 是否走了备用通道——**由后端明确告知**（不能前端按 provider=epay && channel=wxpay 推断：
   主通道没配自建通道时，每笔微信单都会被误标成"备用通道"，20260922 修） */
const payViaFallback = computed(() => !!payingOrder.value?.fallback)
function startTick() {
  stopTick()
  tickTimer = window.setInterval(() => { nowSec.value = Math.floor(Date.now() / 1000) }, 1000)
}
function stopTick() { if (tickTimer != null) { clearInterval(tickTimer); tickTimer = null } }

/* 条件排队：仅自建通道「同金额撞车」时出现（后端金额互斥锁；站点并发低，绝大多数用户不会遇到）。
 * 不阻塞用户——按后端给的 retry_after_ms 自动轮询重试，放行即自动下单并跳转；
 * 有硬上限，到顶给明确提示 + 手动重试，并始终提供「取消排队」逃生口，**绝不静默卡死**。 */
const queueInfo = ref<{ product: PayProduct; waitSecs: number; tries: number } | null>(null)
let queueTimer: number | null = null
const MAX_QUEUE_TRIES = 40
function stopQueue() {
  if (queueTimer != null) { clearTimeout(queueTimer); queueTimer = null }
  queueInfo.value = null
}
function cancelQueue() {
  stopQueue()
  paying.value = false
  poolPaying.value = false
  wallet2Paying.value = false
  setTopupMsg('已取消排队')
}

/* 跳走支付时本页已卸载 → 待支付订单存 localStorage，回跳后恢复上下文继续轮询确认到账。 */
const PENDING_PAY_KEY = 'aqua_pay_pending'
function clearPendingPay() { try { localStorage.removeItem(PENDING_PAY_KEY) } catch { /* 忽略 */ } }
function savePendingPay() {
  if (!payingOrder.value) return
  try {
    localStorage.setItem(PENDING_PAY_KEY, JSON.stringify({ ...payingOrder.value, product: payProduct.value, ts: Date.now() }))
  } catch { /* 忽略 */ }
}
/* 回跳恢复：从平台收银台返回后找回订单号继续轮询（2 小时内有效，过期即丢弃） */
function restorePendingPay() {
  let raw = ''
  try { raw = localStorage.getItem(PENDING_PAY_KEY) || '' } catch { return }
  if (!raw) return
  try {
    const j = JSON.parse(raw) as PendingPay & { product: PayProduct; ts: number }
    if (!j.out_trade_no || Date.now() - (j.ts || 0) > 2 * 3600_000) { clearPendingPay(); return }
    payProduct.value = j.product === 'pool' ? 'pool' : j.product === 'wallet2' ? 'wallet2' : 'balance'
    payingOrder.value = {
      out_trade_no: j.out_trade_no, amount_micro: j.amount_micro, credit_micro: j.credit_micro,
      pay_url: j.pay_url, channel: j.channel === 'wxpay' ? 'wxpay' : 'alipay',
      expire_ts: j.expire_ts || 0, provider: j.provider || 'epay', fallback: !!j.fallback,
    }
    go(payProduct.value === 'pool' ? 'crowd' : 'topup')
    startPolling()
    startTick()
  } catch { clearPendingPay() }
}
/* 继续支付：跳回平台收银台（同一订单，不新建） */
function resumePay() { if (payingOrder.value?.pay_url) window.location.href = payingOrder.value.pay_url }
/* 重新下单：订单已失效（自建通道只有 5 分钟）时一键换新单，金额/去向沿用上一笔，免重新输入 */
function reorderPay() {
  const o = payingOrder.value
  if (!o) return
  const product = payProduct.value
  const yuanStr = String(o.amount_micro / 1_000_000)
  stopPolling(); stopTick(); stopQueue()
  payingOrder.value = null
  clearPendingPay()
  if (product === 'pool') { poolAmt.value = yuanStr } else if (product === 'wallet2') { wallet2Amt.value = yuanStr } else { topupAmt.value = yuanStr }
  void createTopup(product)
}
function cancelPendingPay() {
  stopPolling(); stopTick(); stopQueue()
  paying.value = false
  poolPaying.value = false
  wallet2Paying.value = false
  payingOrder.value = null
  clearPendingPay()
}

/* 渠道手续费由本站承担：用户按支付金额全额入账（1:1），与后端逻辑一致 */
function topupCreditOf(micro: number, _channel: 'alipay' | 'wxpay'): number {
  return micro
}
const topupCreditPreview = computed(() => {
  const micro = Math.round((Number(topupAmt.value) || 0) * 1_000_000)
  return micro >= 10_000 ? topupCreditOf(micro, topupChannel.value) : 0
})
/* 折扣钱包充值预览（20260924）：与主钱包同口径 1:1 到账 */
const wallet2CreditPreview = computed(() => {
  const micro = Math.round((Number(wallet2Amt.value) || 0) * 1_000_000)
  return micro >= 10_000 ? topupCreditOf(micro, topupChannel.value) : 0
})

function setTopupMsg(t: string, ok = false) { topupMsg.value = t; topupOk.value = ok }
function setWallet2Msg(t: string, ok = false) { wallet2Msg.value = t; wallet2Ok.value = ok }

/* 到账提示：按充值去向区分（balance=主钱包 / wallet2=折扣钱包 / pool=公共众筹池直接注资） */
function paidMsg(j: { amount_micro: number; credit_micro?: number; channel?: 'alipay' | 'wxpay'; balance_micro: number }): string {
  const credit = j.credit_micro ?? topupCreditOf(j.amount_micro, j.channel ?? topupChannel.value)
  if (payProduct.value === 'pool') {
    return `注资成功：支付 ¥${yuan(j.amount_micro)} 已全额注入公共众筹池（到账 ¥${yuan(credit)} 站点额度），acu/ 众筹模型即刻恢复可用`
  }
  if (payProduct.value === 'wallet2') {
    return `充值成功：支付 ¥${yuan(j.amount_micro)} 已到账折扣钱包（到账 ¥${yuan(credit)}），当前折扣钱包余额 ¥${yuan(j.balance_micro)}`
  }
  return `充值成功：支付 ¥${yuan(j.amount_micro)}，到账 ¥${yuan(credit)}，当前余额 ¥${yuan(j.balance_micro)}`
}

async function createTopup(product: PayProduct = 'balance') {
  // 防御（20260922 事故）：模板若写成 @click="createTopup"（漏括号），Vue 会把 MouseEvent 当第一个
  // 实参传入 → product 变成对象 → 请求体 product={"isTrusted":true,...} → 后端按 string 反序列化
  // 直接 400「请求体格式错误」，**整条余额充值链路静默不可用**。这里强制归一化。
  if (product !== 'pool' && product !== 'wallet2') product = 'balance'
  const src = product === 'pool' ? poolAmt.value : product === 'wallet2' ? wallet2Amt.value : topupAmt.value
  const micro = Math.round((Number(src) || 0) * 1_000_000)
  const fail = (t: string) => {
    if (product === 'pool') poolMsg.value = t
    else if (product === 'wallet2') setWallet2Msg(t)
    else setTopupMsg(t)
  }
  if (micro < 10_000) { fail('最低充值 ¥0.01'); return }
  if (product === 'pool') { poolPaying.value = true; poolMsg.value = '' }
  else if (product === 'wallet2') { wallet2Paying.value = true; wallet2Msg.value = '' }
  else { paying.value = true; setTopupMsg('') }
  try {
    const j = await apiJson<{
      out_trade_no?: string; pay_url?: string; trade_no?: string
      provider?: string; expire_ts?: number; fallback?: boolean
      queued?: boolean; retry_after_ms?: number; wait_secs?: number; message?: string
    }>('/pay/create', {
      method: 'POST', session: true,
      body: { amount_micro: micro, channel: topupChannel.value, product },
    })
    // 条件排队：同金额正被另一笔订单占用（后端金额互斥锁）。不阻塞用户——自动轮询重试
    if (j.queued) {
      const tries = (queueInfo.value?.tries ?? 0) + 1
      queueInfo.value = { product, waitSecs: j.wait_secs || 0, tries }
      fail(`该金额正在被另一笔订单占用，正在为你排队（约 ${j.wait_secs || 0} 秒）…`)
      if (tries >= MAX_QUEUE_TRIES) {
        stopQueue()
        fail('排队超时：该金额长时间被占用，请换一个金额或稍后再试')
        paying.value = false
        poolPaying.value = false
        wallet2Paying.value = false
        return
      }
      if (queueTimer != null) clearTimeout(queueTimer)
      queueTimer = window.setTimeout(() => { queueTimer = null; void createTopup(product) }, j.retry_after_ms || 2000)
      return // 保持按钮 loading 态，等放行后自动继续
    }
    if (!j.pay_url || !j.out_trade_no) throw new Error('支付通道未返回跳转地址，请稍后重试')
    stopQueue()
    payProduct.value = product
    payingOrder.value = {
      out_trade_no: j.out_trade_no, amount_micro: micro,
      credit_micro: topupCreditOf(micro, topupChannel.value),
      pay_url: j.pay_url, channel: topupChannel.value,
      expire_ts: j.expire_ts || 0, provider: j.provider || 'epay', fallback: !!j.fallback,
    }
    savePendingPay()   // 先落盘再跳转：即使跳转失败，页面上也有「继续支付」可用
    startTick()
    if (product === 'pool') { poolMsg.value = '正在跳转到支付页面…' }
    else if (product === 'wallet2') { setWallet2Msg('正在跳转到支付页面…', true) }
    else { setTopupMsg('正在跳转到支付页面…', true) }
    // 正常跳转（20260922 定稿）：本站不做扫码 / 不嵌收银台，直接交给平台收银台；
    // 支付完成由平台异步回调入账，并通过 return_url 跳回本页（订单上下文已存 localStorage）
    window.location.href = j.pay_url
  } catch (e) { stopQueue(); fail(errText(e)) }
  if (product === 'pool') { poolPaying.value = false }
  else if (product === 'wallet2') { wallet2Paying.value = false }
  else { paying.value = false }
}

/* ===== 众筹算力池（acu/ 公共池）===== */
type PoolStatus = {
  id: string; balance_micro: number; charged_micro: number; used_micro: number
  today_used_micro: number; consumers: number; alive: boolean; daily_cap_micro: number
}
type PoolFlow = {
  type: string; amount_micro: number; balance_after_micro: number
  model?: string; ts: number; prompt_tokens: number; completion_tokens: number; cached_tokens: number
}
const pool = ref<PoolStatus | null>(null)
const poolFlows = ref<PoolFlow[]>([])
const poolMine = ref<{ my_charged_micro: number; my_used_micro: number; net_micro: number } | null>(null)
const poolAmt = ref('')
const poolPaying = ref(false)
const poolMsg = ref('')

async function loadPool() {
  try { pool.value = await apiJson<PoolStatus>('/pool/status') } catch { /* 静默：下一轮重试 */ }
}
async function loadPoolMine() {
  try {
    const j = await apiJson<{ items: PoolFlow[]; my_charged_micro: number; my_used_micro: number; net_micro: number }>('/my/pool/flows', { session: true })
    poolFlows.value = j.items || []
    poolMine.value = { my_charged_micro: j.my_charged_micro, my_used_micro: j.my_used_micro, net_micro: j.net_micro }
  } catch { /* 静默 */ }
}

/* 余额划拨：**两次提醒确认**（站长定稿）——划拨进公共池后不可撤回，误操作只能找站长人工处理。
   step 0=输入金额；1=第一次确认（说明性质与不可撤回）；2=第二次确认（最终金额复核） */
const transferAmt = ref('')
const transferStep = ref<0 | 1 | 2>(0)
const transferBusy = ref(false)
const transferMsg = ref('')
const transferMicro = computed(() => Math.round((Number(transferAmt.value) || 0) * 1_000_000))

function askTransfer() {
  transferMsg.value = ''
  const y = Number(transferAmt.value)
  if (!(y >= 1)) { transferMsg.value = '单次划拨最低 ¥1.00'; return }
  if (y > 1000) { transferMsg.value = '单次划拨最高 ¥1000.00'; return }
  if (transferMicro.value > (balance.value?.balance_micro ?? 0)) { transferMsg.value = '划拨金额超过当前可用余额'; return }
  transferStep.value = 1
}
async function doTransfer() {
  if (transferBusy.value) return
  transferBusy.value = true
  transferMsg.value = ''
  try {
    const j = await apiJson<{ balance_micro: number; pool_balance_micro: number }>('/my/pool/transfer', {
      method: 'POST', session: true, body: { amount_micro: transferMicro.value },
    })
    transferStep.value = 0
    transferAmt.value = ''
    setTopupMsg(`划拨成功：¥${yuan(transferMicro.value)} 已注入众筹池，个人余额 ¥${yuan(j.balance_micro)}`, true)
    loadBalance(); loadPool(); loadPoolMine()
  } catch (e) { transferMsg.value = errText(e) }
  transferBusy.value = false
}

/* 状态提示按充值去向落到对应页签的消息位（pool 单在众筹页，balance 单在充值页）——
   否则众筹单到账后用户在众筹页看不到任何提示，会以为"没反应" */
function setPayMsg(t: string, ok = false) {
  if (payProduct.value === 'pool') { poolMsg.value = t } else { setTopupMsg(t, ok) }
}

function startPolling() {
  stopPolling()
  pollTimer = window.setInterval(async () => {
    if (!payingOrder.value) { stopPolling(); return }
    try {
      const j = await apiJson<{ status: string; amount_micro: number; credit_micro?: number; channel?: 'alipay' | 'wxpay'; balance_micro: number }>(`/pay/status?out_trade_no=${payingOrder.value.out_trade_no}`, { session: true })
      if (j.status === 'paid') {
        setPayMsg(paidMsg(j), true)
        stopPolling()
        stopTick()
        payingOrder.value = null
        clearPendingPay()
        loadBalance(); loadPayOrders(); loadPool()
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
      setPayMsg(paidMsg(j), true)
      stopPolling(); stopTick(); payingOrder.value = null; clearPendingPay()
      loadBalance(); loadPayOrders(); loadPool()
    } else { setPayMsg('还未查询到支付结果，完成支付后稍等几秒') }
  } catch (e) { setPayMsg(errText(e)) }
}
onUnmounted(() => { stopPolling(); stopTick(); stopQueue() })

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

/* ===== 邀请返利（好友首充 ≥¥5 你得 ¥2 + 好友加赠 10%；此后消费永久返 10%） ===== */
interface InviteItem { username: string; email: string; created_ts: number; qualified: boolean }
interface InviteInfo {
  code: string; link: string; invited: number; qualified: number
  earned_micro: number; reward_each_micro: number; first_pay_min_micro: number; rebate_pct: number
  list?: InviteItem[]
}
const invite = ref<InviteInfo | null>(null)
const inviteMsg = ref('')
const inviteOk = ref(false)
const rotating = ref(false)
const inviteLink = computed(() => invite.value?.link ? location.origin + invite.value.link : '')

async function loadInvite() {
  try { invite.value = await apiJson<InviteInfo>('/invite/me', { session: true }) } catch (e) { inviteMsg.value = errText(e) }
}

async function rotateInvite() {
  if (!confirm('确认重置邀请码？旧码立即失效（已填旧码的注册不受影响），已建立的邀请关系与返利照常。')) return
  rotating.value = true; inviteMsg.value = ''
  try {
    await apiJson('/invite/rotate', { method: 'POST', session: true })
    await loadInvite()
    inviteMsg.value = '邀请码已重置，旧码立即失效'; inviteOk.value = true
  } catch (e) { inviteMsg.value = errText(e); inviteOk.value = false }
  rotating.value = false
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

      <!-- 待支付订单（全局提示）：跳转支付后回跳、或用户中途返回，都能在这里继续支付 / 立即查询到账。
           自建通道订单只有 5 分钟窗口 → 必须显示倒计时；到期给「重新下单」一键（否则用户付不了，
           会表现为"这个按钮怎么没反应"）。 -->
      <div v-if="payingOrder" class="banner row wrap" :class="payExpired ? 'bad' : 'warn'">
        <span>
          订单 <code>{{ payingOrder.out_trade_no }}</code>（支付 ¥{{ yuan(payingOrder.amount_micro) }}，到账 ¥{{ yuan(payingOrder.credit_micro) }}）
          <template v-if="payExpired">
            <b>已失效</b>——支付平台订单只有 5 分钟有效期，请点「重新下单」生成新订单。
          </template>
          <template v-else-if="payRemainSecs > 0">
            等待支付中——请在 <b>{{ payRemainSecs }}</b> 秒内完成支付，完成后自动到账（每 3 秒检测）。
          </template>
          <template v-else>
            等待支付中——完成支付后会自动到账（每 3 秒检测）。若未跳转成功可点「继续支付」。
          </template>
        </span>
        <span v-if="payViaFallback" class="tag warn">备用通道</span>
        <button v-if="payExpired" class="btn xs primary" @click="reorderPay()">重新下单</button>
        <button v-else class="btn xs primary" @click="resumePay()">继续支付</button>
        <button class="btn xs" @click="manualCheck()">我已支付，立即查询</button>
        <button class="btn xs ghost" @click="cancelPendingPay()">取消检测</button>
      </div>

      <!-- ▼▼▼ 总览 ▼▼▼ -->
      <div v-show="view === 'dashboard'">
        <p v-if="usageMsg" class="msg bad">{{ usageMsg }}</p>
        <p v-if="balanceMsg" class="msg bad">{{ balanceMsg }}</p>

        <!-- 余额使用期限提醒（20260924 站长指令：关闭充值 + 通知用户尽快用完余额）。
             文案与首页公告同源（/v1/meta 的 announcement），后台改一处两处同时生效。 -->
        <div v-if="announcementText" class="banner warn">
          <AqIcon name="info" :size="14" />
          <span>{{ announcementText }}</span>
        </div>

        <!-- KPI 行 -->
        <div class="kpis">
          <div class="kpi">
            <span>当前余额</span>
            <b>¥{{ yuan(balance?.balance_micro) }}</b>
            <span class="trend">收费模型预充值 · 失败全额退回</span>
          </div>
          <div class="kpi">
            <span>折扣钱包余额</span>
            <b>¥{{ yuan(balance?.balance2_micro) }}</b>
            <span class="trend">2 号钱包 · 折扣模型专用 · 独立充值</span>
          </div>
          <div class="kpi">
            <span>有效密钥</span>
            <b>{{ fmt(activeKeys) }}</b>
            <span class="trend">共 {{ fmt(keys.length) }} 把<template v-if="revokedKeys">（已吊销 {{ fmt(revokedKeys) }}）</template></span>
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
            <div class="dim mt8">支付宝 / 微信在线充值，支付金额 100% 全额到账（渠道手续费由本站承担）。本页充值进入您的个人余额，用于收费模型调用。acu/ 官方自营纯免费，不扣个人余额。</div>
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
          <p class="dim mt8">密钥创建后请妥善保管；支持查看原文的密钥可在列表单独操作，默认只展示掩码。「免费 + 按次」分组支持 aqua/ 按次模型；「免费 + 按量」分组支持 aqua/ 官方原版直连专线；acu/ 官方自营纯免费。「纯免费」密钥不能调用收费模型。</p>
          <div class="form-grid mt12">
            <div class="field">
              <label>密钥名称</label>
              <input v-model="newKeyName" class="input" maxlength="32" placeholder="如：我的笔记本 / 生产环境" @keydown.enter="doCreateKey" />
            </div>
            <div class="field">
              <label>计费分组（仅影响收费模型）</label>
              <select v-model="newKeyGrp" class="select">
                <option value="per_call">免费 + 按次计费（推荐）</option>
                <option value="per_token">免费 + 按量计费（aqua/ 官方原版专线）</option>
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
            <div class="row wrap" style="gap: 8px;">
              <button v-if="revokedKeys" class="btn sm" @click="showRevoked = !showRevoked">
                <AqIcon name="eye" :size="13" /> {{ showRevoked ? '隐藏已吊销' : '显示已吊销 (' + fmt(revokedKeys) + ')' }}
              </button>
              <button class="btn sm" :disabled="keysLoading" @click="loadKeys"><AqIcon name="refresh" :size="13" /> 刷新</button>
            </div>
          </div>
          <p class="dim mt8">acu/ 是官方自营纯免费线路，不扣个人余额。aqua/ 按次或按量收费，codex/ 按量计费；请按线路选择密钥分组。</p>
          <div v-if="keysLoading && !keys.length" class="mt12"><div class="skeleton" style="min-height: 120px;"></div></div>
          <div v-else-if="!visibleKeys.length" class="empty">
            <div class="big"><AqIcon name="key" :size="34" /></div>
            <b>{{ keys.length ? '没有生效中的密钥' : '还没有密钥' }}</b>
            <div class="dim">{{ keys.length ? `另有 ${fmt(revokedKeys)} 把已吊销（已从列表移除）` : '创建一把密钥开始调用' }}</div>
            <button v-if="keys.length" class="btn sm mt12" @click="showRevoked = true">
              <AqIcon name="eye" :size="13" /> 查看已吊销密钥
            </button>
          </div>
          <div v-else class="tbl-wrap mt12">
            <table class="table">
              <thead><tr><th>名称</th><th>前缀</th><th>计费分组</th><th>额度 / 有效期</th><th>创建时间</th><th>操作</th></tr></thead>
              <tbody>
                <template v-for="k in visibleKeys" :key="k.id">
                  <tr :class="{ 'row-dim': k.revoked }">
                    <td>
                      <div class="row wrap" style="gap: 6px;">
                        <span class="key-name">{{ k.name }}</span>
                        <span class="tag" :class="grpTagCls(k.billing_grp)" :title="grpTitle(k.billing_grp)">{{ grpLabel(k.billing_grp || '') }}</span>
                        <span v-if="k.revoked" class="tag bad">已吊销</span>
                        <span v-if="!k.revoked && k.expires_in_sec != null && k.expires_in_sec <= 7*86400" class="tag warn" :title="'到期时间：' + fmtTime(k.expires_at || 0)">
                          {{ k.expires_in_sec <= 0 ? '已过期' : '临期 ' + Math.max(1, Math.floor(k.expires_in_sec / 86400)) + ' 天' }}
                        </span>
                      </div>
                    </td>
                    <td><code>{{ k.prefix }}</code></td>
                    <td>
                      <select
                        v-if="!k.revoked" v-model="k.billing_grp" class="select grp-select"
                        :disabled="grpSavingId === k.id" @change="changeGrp(k)"
                      >
                        <option value="" disabled>未分组（旧密钥）</option>
                        <option value="per_call">免费 + 按次计费（推荐）</option>
                        <option value="per_token">免费 + 按量计费（aqua/ 官方原版专线）</option>
                        <option value="official" disabled>官方中转（已下架，请改选）</option>
                        <option value="free">纯免费（仅免费模型）</option>
                      </select>
                      <span v-else class="dim">{{ grpLabel(k.billing_grp || '') }}</span>
                    </td>
                    <td>
                      <!-- 配额摘要：限额 / 已用 / 进度条；未设限额显示"不限" -->
                      <div v-if="k.type && k.limit" style="min-width: 150px;">
                        <div class="dim" style="font-size: 11.5px;">
                          {{ k.type === 'count'
                            ? fmt(k.used || 0) + ' / ' + fmt(k.limit) + ' 次'
                            : microYuan(k.used || 0) + ' / ' + microYuan(k.limit) + ' 元' }}
                          <span v-if="k.reset" class="tag" style="margin-left: 4px;">{{ k.reset === 'daily' ? '每日重置' : '每月重置' }}</span>
                        </div>
                        <div class="quota-bar"><i :style="{ width: quotaPct(k) + '%' }" :class="{ hot: quotaPct(k) >= 80 }"></i></div>
                      </div>
                      <span v-else class="dim">不限</span>
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
                        <button class="btn xs" @click="openQuota(k)">
                          <AqIcon name="gauge" :size="12" /> {{ quotaEditId === k.id ? '收起额度' : '额度设置' }}
                        </button>
                        <button class="btn xs danger" @click="doRevoke(k)">吊销</button>
                      </div>
                      <span v-else class="dim">—</span>
                    </td>
                  </tr>
                  <!-- 额度设置展开行（P4：限额 / 重置周期 / 倍率 / 有效期 / 备注） -->
                  <tr v-if="quotaEditId === k.id">
                    <td colspan="6">
                      <div class="quota-panel">
                        <b><AqIcon name="gauge" :size="14" /> 分发额度设置</b>
                        <p class="dim mt8" style="font-size: 12.5px;">
                          用于把密钥分发给下游：设定用量上限与有效期。实际扣费仍按站内原价从你的<b>账户余额</b>扣，
                          倍率仅用于对外报价换算（可售额 = 额度 × 倍率），不影响你的实际成本。
                        </p>
                        <div class="form-grid mt12">
                          <div class="field">
                            <label>限额方式</label>
                            <select v-model="quotaDraft.type" class="select">
                              <option value="">不限（不限制用量）</option>
                              <option value="count">按次数（限调用多少次）</option>
                              <option value="amount">按金额（限消费多少钱）</option>
                            </select>
                          </div>
                          <div class="field">
                            <label>限额值 {{ quotaDraft.type === 'count' ? '（次）' : quotaDraft.type === 'amount' ? '（元）' : '' }}</label>
                            <input v-model="quotaDraft.limitInput" class="input" :disabled="!quotaDraft.type" placeholder="如 200 或 2" />
                          </div>
                          <div class="field">
                            <label>额度重置周期</label>
                            <select v-model="quotaDraft.reset" class="select">
                              <option value="">不重置（用完为止）</option>
                              <option value="daily">每日重置（东八区 0 点）</option>
                              <option value="monthly">每月重置（每月 1 日）</option>
                            </select>
                          </div>
                          <div class="field">
                            <label>对外倍率（仅报价换算）</label>
                            <div class="row" style="gap: 6px; align-items: center;">
                              <input v-model="quotaDraft.rate" class="input" placeholder="1" style="max-width: 90px;" />
                              <span class="dim">× 下游看到的价格 = 站内价 × 该倍率</span>
                            </div>
                          </div>
                          <div class="field">
                            <label>有效期至（留空=永久）</label>
                            <input v-model="quotaDraft.expiresDate" type="date" class="input" />
                          </div>
                          <div class="field">
                            <label>备注（下游标识，便于对账）</label>
                            <input v-model="quotaDraft.note" class="input" maxlength="120" placeholder="如：闲鱼买家 A / 客户张三" />
                          </div>
                        </div>
                        <!-- 代理利润换算（倍率 > 1 时展示） -->
                        <div v-if="quotaRateVal(k) > 1" class="profit-box mt12">
                          <b>代理口径换算</b>
                          <dl class="price-compare">
                            <dt>成本（已消耗）</dt><dd>{{ microYuan(k.cost_micro || 0) }} 元</dd>
                            <dt>已产生零售额</dt><dd>{{ microYuan(k.retail_micro || 0) }} 元</dd>
                            <dt>已赚利润</dt><dd>{{ microYuan(k.profit_micro || 0) }} 元</dd>
                            <dt>剩余可售额度</dt><dd>{{ microYuan(k.remain_retail_micro || 0) }} 元</dd>
                          </dl>
                          <p>倍率仅用于换算展示，实扣恒按站内原价从你的账户余额扣除。</p>
                        </div>
                        <p v-if="quotaMsg" class="msg mt12" :class="quotaOk ? 'ok' : 'bad'">{{ quotaMsg }}</p>
                        <div class="row wrap mt12" style="gap: 8px;">
                          <button class="btn primary sm" :disabled="quotaSaving" @click="saveQuota(k)">{{ quotaSaving ? '保存中…' : '保存设置' }}</button>
                          <button class="btn sm" :disabled="quotaSaving" @click="doResetQuota(k)">清零本周期用量</button>
                        </div>
                      </div>
                    </td>
                  </tr>
                  <!-- 查看原文展开行 -->
                  <tr v-if="revealedId === k.id">
                    <td colspan="6">
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

      <!-- ▼▼▼ 自定义域名与证书 ▼▼▼ -->
      <div v-show="view === 'domains'">
        <div class="card">
          <div class="row between wrap">
            <b><AqIcon name="server" :size="16" /> 自定义域名 <span class="dim">（把接口挂到自己的域名，给下游白标）</span></b>
            <button class="btn sm" :disabled="domainsLoading" @click="loadDomains"><AqIcon name="refresh" :size="13" /> {{ domainsLoading ? '刷新中…' : '刷新' }}</button>
          </div>

          <p v-if="!domainEnabled" class="msg bad mt12">自定义域名功能当前未开放，请联系站长开通。</p>

          <template v-else>
            <div class="howto mt12">
              <b>三步接入</b>
              <ol>
                <li>在你的 DNS 服务商添加解析：<code>{{ newDomain || 'api.你的域名.com' }}</code> → CNAME → <code>{{ domainCname }}</code>
                  <CopyBtn :text="domainCname" label="复制目标" size="xs" /></li>
                <li>点「测试并添加」验证解析是否生效（通常 1~10 分钟）</li>
                <li>配置证书：上传你自己的证书，或用平台免费自动签发（Let's Encrypt）</li>
              </ol>
              <p class="dim">每个账号最多绑定 {{ domainMax }} 个域名。删除域名会同时清理 nginx 配置。</p>
            </div>

            <div class="row wrap mt12" style="gap: 8px; align-items: flex-end;">
              <div class="field" style="flex: 1; min-width: 220px; margin: 0;">
                <label>要绑定的域名</label>
                <input v-model="newDomain" class="input" placeholder="如 api.yourdomain.com" @keydown.enter="doAddDomain(true)" />
              </div>
              <button class="btn primary sm" :disabled="domainAdding || !newDomain.trim()" @click="doAddDomain(true)">
                <AqIcon name="check" :size="13" /> {{ domainAdding ? '添加中…' : '测试并添加' }}
              </button>
              <button class="btn sm" :disabled="domainAdding || !newDomain.trim()" @click="doAddDomain(false)">直接添加</button>
            </div>
          </template>

          <p v-if="domainMsg" class="msg mt12" :class="domainOk ? 'ok' : 'bad'">{{ domainMsg }}</p>

          <div v-if="domainsLoading && !domains.length" class="mt12"><div class="skeleton" style="min-height: 100px;"></div></div>
          <div v-else-if="domainEnabled && !domains.length" class="empty">
            <div class="big"><AqIcon name="server" :size="34" /></div>
            <b>还没有绑定域名</b>
            <div class="dim">绑定后可把 <code>https://你的域名/v1</code> 提供给下游使用</div>
          </div>
          <div v-else class="tbl-wrap mt12">
            <table class="table">
              <thead><tr><th>域名</th><th>状态</th><th>证书</th><th>操作</th></tr></thead>
              <tbody>
                <template v-for="d in domains" :key="d.id">
                  <tr>
                    <td>
                      <div class="mono" style="font-weight: 600;">{{ d.domain }}</div>
                      <div v-if="d.last_error" class="dim" style="font-size: 11.5px; color: var(--bad);">{{ d.last_error }}</div>
                    </td>
                    <td><span class="tag" :class="domainTag(d.status).cls">{{ domainTag(d.status).label }}</span></td>
                    <td class="dim">{{ certLabel(d) }}</td>
                    <td>
                      <div class="row wrap" style="gap: 6px;">
                        <button class="btn xs" :disabled="domainBusyId === d.id" @click="doVerifyDomain(d)">校验并激活</button>
                        <button class="btn xs" :disabled="domainBusyId === d.id" @click="certUploadId = certUploadId === d.id ? 0 : d.id">
                          {{ certUploadId === d.id ? '收起' : '上传证书' }}
                        </button>
                        <button v-if="leAvailable" class="btn xs" :disabled="domainBusyId === d.id" @click="doIssueCert(d)">
                          <AqIcon name="bolt" :size="12" /> 自动签发
                        </button>
                        <button class="btn xs danger" :disabled="domainBusyId === d.id" @click="doDeleteDomain(d)">删除</button>
                      </div>
                    </td>
                  </tr>
                  <tr v-if="certUploadId === d.id">
                    <td colspan="4">
                      <div class="quota-panel">
                        <b>上传自己的证书</b>
                        <p class="dim mt8" style="font-size: 12.5px;">
                          粘贴 PEM 格式内容（如用 certbot 申请，对应
                          <code>/etc/letsencrypt/live/你的域名/fullchain.pem</code> 与 <code>privkey.pem</code>）。
                          平台会校验证书与私钥是否匹配、是否包含该域名、是否在有效期内；私钥仅存于服务器（权限 0600），不回显。
                        </p>
                        <div class="field mt12">
                          <label>证书链（fullchain.pem）</label>
                          <textarea v-model="certFullchain" class="input" rows="4" placeholder="-----BEGIN CERTIFICATE-----"></textarea>
                        </div>
                        <div class="field mt12">
                          <label>私钥（privkey.pem）</label>
                          <textarea v-model="certPrivkey" class="input" rows="4" placeholder="-----BEGIN PRIVATE KEY-----"></textarea>
                        </div>
                        <div class="row wrap mt12" style="gap: 8px;">
                          <button class="btn primary sm" :disabled="domainBusyId === d.id" @click="doUploadCert(d)">保存并激活</button>
                          <button class="btn sm" @click="certUploadId = 0; certFullchain = ''; certPrivkey = ''">取消</button>
                        </div>
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
        <!-- 停售总提示（20260924）：置顶显示，明确"不要再支付"与"已付款仍到账"，
             避免用户看到下面的充值卡以为还能充（后端已 503 拦截，这里只是别让人白填） -->
        <div v-if="payDisabled" class="banner warn">
          <AqIcon name="alert" :size="14" />
          <span>
            <b>在线充值已停止开放</b>——请勿再下单支付。已支付但未到账的订单仍会正常入账，如有疑问请联系站长。
            <template v-if="announcementText"><br />{{ announcementText }}</template>
          </span>
        </div>
        <div v-else class="banner warn">
          <AqIcon name="info" :size="14" />
          <span>在线充值支付金额 <b>100% 全额到账</b>（渠道手续费由本站承担）；本次充值进入您的个人余额，用于收费模型调用，与赞助等其它资金用途相互独立。</span>
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
            <!-- ⚠️ 必须带括号：写成 @click="createTopup" 时 Vue 会把 MouseEvent 当第一个实参传入，
                 于是 product 变成对象 → 请求体 product={"isTrusted":true,...} → 后端按 string
                 反序列化直接 400「请求体格式错误」，**余额充值整条链路不可用**（20260922 事故）。 -->
            <button class="btn primary" :disabled="payDisabled || paying || !topupAmt" @click="createTopup()">
              {{ paying ? (queueInfo ? '排队中…' : '创建中…') : '去支付' }}
            </button>
            <!-- 排队时的逃生口：保证用户任何时候都能退出等待，绝不会"点了没反应又退不出来" -->
            <button v-if="queueInfo && queueInfo.product !== 'pool'" class="btn ghost" @click="cancelQueue()">取消排队</button>
            <span class="dim">当前余额 ¥{{ yuan(balance?.balance_micro) }} · 收费模型按次计费（每次成功请求扣一次），余额不足时收费模型返回 402，免费模型照常可用</span>
          </div>
          <p v-if="topupCreditPreview > 0" class="dim mt8">
            支付 ¥{{ topupAmt }}，预计到账余额 <b class="col-ok">¥{{ yuan(topupCreditPreview) }}</b>（100% 全额到账，渠道手续费由本站承担）
          </p>
          <p v-if="topupMsg" class="msg mt12" :class="topupOk ? 'ok' : 'bad'">{{ topupMsg }}</p>
          <p class="dim mt8">点「去支付」会跳转到支付平台收银台，完成支付后自动跳回本页并到账（订单上下文已保留）；到账前请勿关闭浏览器。收费模型按次计费（每次成功请求扣一次，单价见模型中心收费专区），余额低于阈值可在「账号设置」开启邮件提醒。</p>
          <p class="dim mt8 pay-help">
            <b class="col-warn">支付遇到问题？</b>已支付但余额未到账、重复扣款、金额有误——请勿重复支付，保留支付凭证（账单截图 / 商户单号），
            <a href="https://pd.qq.com/s/e4ktxw1b8" target="_blank" rel="noopener">加入 QQ 频道</a> 或
            <a href="https://qm.qq.com/cgi-bin/qm/qr?k=&jump_from=&group=1103667832" target="_blank" rel="noopener">加入 QQ 一群（1103667832）</a>
            / <a href="https://qm.qq.com/q/o8QDbza2Ge" target="_blank" rel="noopener">二群（1006740220）</a>
            联系站长人工核实补账。
          </p>
        </div>

        <!-- ▼ 2 号折扣钱包独立充值（20260924）：与主钱包资金完全独立、禁止互转 -->
        <div class="card mt16">
          <b><AqIcon name="spark" :size="16" /> 折扣钱包充值（2 号钱包）</b>
          <p class="dim mt8">
            折扣钱包是<b>独立钱包</b>，专门用于国模等折扣模型（按折扣价结算）。它与上方主钱包
            <b class="col-warn">资金完全独立、不支持互相划转</b>——只能在这里单独充值。
            用主钱包余额调用折扣模型会被拒绝（不会静默按原价扣主钱包）。
          </p>
          <p class="dim mt8">当前折扣钱包余额 <b class="col-ok">¥{{ yuan(balance?.balance2_micro) }}</b> · 折扣钱包今日消费 ¥{{ yuan(balance?.today_cost2_micro) }}</p>
          <div class="chips mt12">
            <button v-for="p in TOPUP_PRESETS" :key="'w2-' + p" type="button" class="chip" :class="{ on: Number(wallet2Amt) === p }" @click="wallet2Amt = String(p)">¥{{ p }}</button>
          </div>
          <div class="form-grid mt12">
            <div class="field">
              <label>自定义金额（0.01 ~ 1000 元）</label>
              <input v-model="wallet2Amt" class="input" type="number" min="0.01" max="1000" step="0.01" placeholder="如 10" />
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
            <button class="btn primary" :disabled="payDisabled || wallet2Paying || !wallet2Amt" @click="createTopup('wallet2')">
              {{ wallet2Paying ? (queueInfo ? '排队中…' : '创建中…') : '充值到折扣钱包' }}
            </button>
            <button v-if="queueInfo && queueInfo.product === 'wallet2'" class="btn ghost" @click="cancelQueue()">取消排队</button>
            <span class="dim">100% 全额到账，渠道手续费由本站承担</span>
          </div>
          <p v-if="wallet2CreditPreview > 0" class="dim mt8">
            支付 ¥{{ wallet2Amt }}，预计到账折扣钱包 <b class="col-ok">¥{{ yuan(wallet2CreditPreview) }}</b>
          </p>
          <p v-if="wallet2Msg" class="msg mt12" :class="wallet2Ok ? 'ok' : 'bad'">{{ wallet2Msg }}</p>
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

      <!-- ▼▼▼ 众筹算力池 ▼▼▼ -->
      <div v-show="view === 'crowd'">
        <div class="banner" :class="pool?.alive ? 'acc' : 'warn'">
          <AqIcon name="info" :size="14" />
          <span v-if="pool?.alive">众筹池 <b>存活中</b>：acu/ 官方自营模型由公共池统一付费——调用时 <b>不扣你的个人余额</b>。池子越充足，大家用得越久。</span>
          <span v-else>众筹池 <b>已被用完</b>：acu/ 模型当前返回 403，正在等待注资复活。注资任意金额立刻点亮，救场者将登上荣誉墙。</span>
        </div>

        <!-- 池子状态 -->
        <div class="card mt16">
          <b><AqIcon name="users" :size="16" /> 公共池状态</b>
          <div class="pool-stats mt12">
            <div><span>池子余额</span><b class="grad-text" style="font-size: 17px;">¥{{ yuan(pool?.balance_micro) }}</b></div>
            <div><span>累计注资</span><b>¥{{ yuan(pool?.charged_micro) }}</b></div>
            <div><span>累计消耗</span><b>¥{{ yuan(pool?.used_micro) }}</b></div>
            <div><span>今日消耗</span><b>¥{{ yuan(pool?.today_used_micro) }}</b></div>
            <div><span>参与人数</span><b>{{ pool?.consumers ?? 0 }}</b></div>
            <div><span>单人日上限</span><b>¥{{ yuan(pool?.daily_cap_micro) }}</b></div>
          </div>
        </div>

        <!-- 注资方式一：在线充值直接进池子 -->
        <div class="card mt16">
          <b><AqIcon name="spark" :size="16" /> 注资众筹池（在线充值）</b>
          <p class="dim mt8" style="font-size: 12.5px;">支付金额 100% 全额注入公共池（渠道手续费由本站承担），<b>不进入你的个人余额</b>。池子归零时注资即"救场"。</p>
          <div class="chips mt12">
            <button v-for="p in TOPUP_PRESETS" :key="p" type="button" class="chip" :class="{ on: Number(poolAmt) === p }" @click="poolAmt = String(p)">¥{{ p }}</button>
          </div>
          <div class="form-grid mt12">
            <div class="field">
              <label>自定义金额（0.01 ~ 1000 元）</label>
              <input v-model="poolAmt" class="input" type="number" min="0.01" max="1000" step="0.01" placeholder="如 10" />
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
            <button class="btn primary" :disabled="payDisabled || poolPaying || !poolAmt" @click="createTopup('pool')">{{ poolPaying ? (queueInfo ? '排队中…' : '创建中…') : '充值到池子' }}</button>
            <button v-if="queueInfo && queueInfo.product === 'pool'" class="btn ghost" @click="cancelQueue()">取消排队</button>
            <span v-if="poolMsg" class="dim">{{ poolMsg }}</span>
          </div>
        </div>

        <!-- 注资方式二：个人余额划拨（两次确认） -->
        <div class="card mt16">
          <b><AqIcon name="wallet" :size="16" /> 用个人余额划拨注资</b>
          <p class="dim mt8" style="font-size: 12.5px;">把个人余额划拨进公共池，立即为所有人点亮 acu/ 模型。当前个人余额 <b>¥{{ yuan(balance?.balance_micro) }}</b>。</p>
          <div class="banner warn mt12">
            <AqIcon name="info" :size="14" />
            <span><b>划拨是单向操作、不可撤回</b>：资金进入公共池后由全体用户共享消耗，站方不接受撤回（否则会引发挤兑）。请务必核对金额。</span>
          </div>
          <div class="form-grid mt12">
            <div class="field">
              <label>划拨金额（最低 ¥1.00，最高 ¥1000.00）</label>
              <input v-model="transferAmt" class="input" type="number" min="1" max="1000" step="0.01" placeholder="如 10" />
            </div>
          </div>
          <div class="row wrap mt12">
            <button class="btn primary" :disabled="transferBusy || !transferAmt" @click="askTransfer">划拨到众筹池</button>
            <span v-if="transferMsg" class="dim">{{ transferMsg }}</span>
          </div>
        </div>

        <!-- 我的众筹明细 -->
        <div class="card mt16">
          <div class="row between">
            <b><AqIcon name="clock" :size="16" /> 我的众筹明细</b>
            <button class="btn ghost xs" @click="loadPoolMine">刷新</button>
          </div>
          <div v-if="poolMine" class="row wrap mt8" style="gap: 14px;">
            <span class="dim">我累计注资 <b class="col-ok">¥{{ yuan(poolMine.my_charged_micro) }}</b></span>
            <span class="dim">我从池子消耗 <b>¥{{ yuan(poolMine.my_used_micro) }}</b></span>
            <span class="dim">净贡献 <b>¥{{ yuan(poolMine.net_micro) }}</b></span>
          </div>
          <div class="tbl-wrap mt12">
            <table class="table">
              <thead><tr><th>时间</th><th>类型</th><th>模型 / 说明</th><th class="num">金额</th><th class="num">池子余额</th></tr></thead>
              <tbody>
                <tr v-if="!poolFlows.length"><td colspan="5" class="empty">还没有众筹记录</td></tr>
                <tr v-for="(f, i) in poolFlows" :key="i">
                  <td class="dim nowrap">{{ fmtTime(f.ts) }}</td>
                  <td><span class="tag" :class="f.type === 'consume' ? '' : 'ok'">{{ f.type === 'consume' ? '消耗' : f.type === 'charge' ? '注资' : f.type }}</span></td>
                  <td class="mono">{{ f.model || '-' }}</td>
                  <td class="num" :class="f.amount_micro < 0 ? '' : 'col-ok'">{{ f.amount_micro < 0 ? '-' : '+' }}¥{{ yuan(Math.abs(f.amount_micro)) }}</td>
                  <td class="num dim">¥{{ yuan(f.balance_after_micro) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <!-- 划拨两次提醒确认 -->
        <div v-if="transferStep > 0" class="pay-mask" @click.self="transferStep = 0">
          <div class="card pay-dialog">
            <b><AqIcon name="info" :size="16" /> {{ transferStep === 1 ? '确认划拨（第 1 / 2 步）' : '最终确认（第 2 / 2 步）' }}</b>
            <p class="mt12" style="font-size: 13px; line-height: 1.75;">
              <template v-if="transferStep === 1">
                你将把个人余额中的 <b class="grad-text">¥{{ yuan(transferMicro) }}</b> 划拨进 <b>公共众筹池</b>。<br />
                这笔钱会立刻变成所有人共享的算力额度，<b>不再属于你的个人余额</b>，也 <b>无法撤回</b>。
              </template>
              <template v-else>
                请再次核对金额：<b class="grad-text" style="font-size: 17px;">¥{{ yuan(transferMicro) }}</b><br />
                划拨后个人余额将从 ¥{{ yuan(balance?.balance_micro) }} 变为 ¥{{ yuan((balance?.balance_micro ?? 0) - transferMicro) }}，此操作 <b>不可撤销</b>。
              </template>
            </p>
            <p v-if="transferMsg" class="dim mt8">{{ transferMsg }}</p>
            <div class="row wrap mt16">
              <button class="btn ghost" @click="transferStep = 0">取消</button>
              <button v-if="transferStep === 1" class="btn primary" @click="transferStep = 2">我已知晓，继续</button>
              <button v-else class="btn primary" :disabled="transferBusy" @click="doTransfer">{{ transferBusy ? '划拨中…' : '确认划拨' }}</button>
            </div>
          </div>
        </div>
      </div>

      <!-- ▼▼▼ 邀请返利 ▼▼▼ -->
      <div v-show="view === 'invite'">
        <div class="banner acc">
          <AqIcon name="send" :size="14" />
          <span>邀请好友，双方得利：好友用你的链接注册并首充 ≥<b>¥{{ yuan(invite?.first_pay_min_micro) }}</b>，你得 <b>¥{{ yuan(invite?.reward_each_micro) }}</b> 邀请奖、好友额外得 <b>{{ invite?.rebate_pct || 10 }}% 充值加赠</b>；此后好友每笔 acu/ 模型消费，你永久返 <b>{{ invite?.rebate_pct || 10 }}%</b>（直接发到余额）。奖励仅入余额用于调用；同 IP 短时间多次注册等刷单行为会被风控拦截。</span>
        </div>

        <!-- 邀请码 + 链接 -->
        <div class="card mt16">
          <div class="row between wrap">
            <b><AqIcon name="send" :size="16" /> 我的邀请码</b>
            <div class="row wrap">
              <button class="btn sm" :disabled="rotating" @click="rotateInvite"><AqIcon name="refresh" :size="13" /> 重置邀请码</button>
              <button class="btn sm" :disabled="rotating" @click="loadInvite"><AqIcon name="refresh" :size="13" /> 刷新</button>
            </div>
          </div>
          <div class="form-grid mt12">
            <div class="field">
              <label>邀请码</label>
              <div class="row wrap">
                <code class="inv-code">{{ invite?.code || '…' }}</code>
                <CopyBtn v-if="invite?.code" :text="invite.code" label="复制邀请码" />
              </div>
            </div>
            <div class="field">
              <label>邀请链接（好友打开自动填码直达注册）</label>
              <div class="row wrap">
                <code class="inv-link">{{ inviteLink || '…' }}</code>
                <CopyBtn v-if="inviteLink" :text="inviteLink" label="复制链接" />
              </div>
            </div>
          </div>
          <p v-if="inviteMsg" class="msg mt12" :class="inviteOk ? 'ok' : 'bad'">{{ inviteMsg }}</p>
        </div>

        <!-- 统计 KPI -->
        <div class="kpis mt16">
          <div class="kpi">
            <span>累计邀请</span>
            <b>{{ fmt(invite?.invited || 0) }} 人</b>
            <span class="trend">好友注册即计入</span>
          </div>
          <div class="kpi">
            <span>达标人数</span>
            <b>{{ fmt(invite?.qualified || 0) }} 人</b>
            <span class="trend">首充 ≥¥{{ yuan(invite?.first_pay_min_micro) }} 发奖</span>
          </div>
          <div class="kpi">
            <span>累计奖励收入</span>
            <b>¥{{ yuan(invite?.earned_micro) }}</b>
            <span class="trend">邀请奖 + 消费返利，已入余额</span>
          </div>
        </div>

        <!-- 明细 -->
        <div class="card mt16">
          <b><AqIcon name="list" :size="16" /> 邀请明细</b>
          <div v-if="!invite?.list?.length" class="empty">
            <div class="big"><AqIcon name="send" :size="34" /></div>
            <b>还没有邀请记录</b>
            <div class="dim">复制上面的链接发给好友，TA 注册首充后奖励自动到账</div>
          </div>
          <div v-else class="tbl-wrap mt12">
            <table class="table">
              <thead><tr><th>好友</th><th>注册时间</th><th>状态</th><th>说明</th></tr></thead>
              <tbody>
                <tr v-for="(it, i) in invite.list" :key="it.email + '-' + i">
                  <td>
                    <div>{{ it.username || '—' }}</div>
                    <div class="dim" style="font-size: 12px;">{{ it.email }}</div>
                  </td>
                  <td class="dim nowrap">{{ fmtTime(it.created_ts) }}</td>
                  <td>
                    <span v-if="it.qualified" class="tag ok">已达标 · 奖励已发</span>
                    <span v-else class="tag warn">待首充 ≥¥{{ yuan(invite?.first_pay_min_micro) }}</span>
                  </td>
                  <td class="dim">达标后 TA 的每笔消费你返 {{ invite?.rebate_pct || 10 }}%</td>
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

/* ---- 密钥分发配额（P4） ---- */
.quota-bar { height: 5px; border-radius: 99px; background: var(--bg3); overflow: hidden; margin-top: 4px; }
.quota-bar i { display: block; height: 100%; border-radius: 99px; background: var(--acc-grad); transition: width .25s; }
.quota-bar i.hot { background: linear-gradient(135deg, #f59e0b, #dc2626); }
.quota-panel { padding: 14px; border-radius: 10px; background: var(--bg2); border: 1px solid var(--line); }
.profit-box { padding: 12px; border-radius: 8px; background: var(--bg3); border: 1px dashed var(--line); }
.profit-box .price-compare { display: grid; grid-template-columns: auto 1fr; gap: 2px 12px; margin: 8px 0 0; }
.profit-box .price-compare dt { font-size: 12px; color: var(--txt2); }
.profit-box .price-compare dd { margin: 0; font-size: 12.5px; font-weight: 700; color: var(--txt0); }
.profit-box p { margin: 8px 0 0; font-size: 11.5px; color: var(--txt2); }

/* ---- 自定义域名（P5） ---- */
.howto { padding: 14px; border-radius: 10px; background: var(--bg2); border: 1px solid var(--line); }
.howto ol { margin: 8px 0 0; padding-left: 20px; display: grid; gap: 6px; font-size: 12.5px; color: var(--txt1); }
.howto code { padding: 1px 5px; border-radius: 4px; background: var(--bg3); font-size: 12px; }
.howto p { margin: 8px 0 0; font-size: 11.5px; }
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
.banner.acc { background: var(--acc-soft); color: var(--acc); }
.inv-code { font-size: 18px; font-weight: 800; letter-spacing: 2px; color: var(--acc); padding: 4px 10px; border: 1px dashed var(--acc); border-radius: var(--r-sm); }
.inv-link { max-width: 100%; overflow: hidden; text-overflow: ellipsis; font-size: 12.5px; color: var(--txt1); }
@media (max-width: 640px) {
  .tabbar .jump { margin-left: 0; }
}

/* ---- 弹窗遮罩（众筹池余额划拨的两步确认用；支付已改为跳转平台收银台，不再有支付弹窗） ---- */
.pay-mask {
  position: fixed; inset: 0; z-index: 120;
  background: rgba(0, 0, 0, .58);
  display: flex; align-items: center; justify-content: center;
  padding: 18px;
}
.pay-dialog { width: 100%; max-width: 430px; max-height: 92vh; overflow: auto; }

/* ---- 众筹池状态栅格 ---- */
.pool-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px 16px;
}
.pool-stats > div { display: flex; flex-direction: column; gap: 3px; }
.pool-stats span { font-size: 11.5px; color: var(--dim); }
.pool-stats b { font-size: 14px; }
@media (max-width: 640px) {
  .pool-stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>
