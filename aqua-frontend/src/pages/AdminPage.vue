<script setup lang="ts">
/* 站长管理控制台：单密码登录（独立 adm_ 会话，与用户体系隔离）
 * 视图：仪表盘 / 客户管理 / 上游管理 / 上游额度 / 众筹池 / 额度监管 / 审计日志 / 对账 / 系统更新 / 站点设置
 * 资金红线：高危操作二次密码；金额全程微元整数；任一接口 401 → 清 token 回登录视图。 */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { apiJson as apiJsonRaw, errText, fmt } from '@/composables/useApi'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'

/* 管理会话 401 全局兜底：任一接口 401 → 清 token 回登录视图，避免"莫名失效"卡在各页面 */
const sessMsg = ref('')
async function apiJson<T = any>(path: string, opts: Parameters<typeof apiJsonRaw>[1] = {}): Promise<T> {
  try {
    return await apiJsonRaw<T>(path, opts)
  } catch (e: any) {
    if (e?.status === 401 && token.value) {
      token.value = ''
      try { localStorage.removeItem(TOKEN_KEY) } catch { /* 忽略 */ }
      sessMsg.value = '管理会话已失效，请重新登录'
    }
    throw e
  }
}

type View = 'dashboard' | 'users' | 'lines' | 'quota' | 'pool' | 'supervision' | 'audit' | 'reconcile' | 'update' | 'settings'
const NAV: { id: View; label: string; icon: string }[] = [
  { id: 'dashboard', label: '仪表盘', icon: 'chart' },
  { id: 'users', label: '客户管理', icon: 'user' },
  { id: 'lines', label: '上游管理', icon: 'puzzle' },
  { id: 'quota', label: '上游额度', icon: 'bolt' },
  { id: 'pool', label: '众筹池', icon: 'coin' },
  { id: 'supervision', label: '额度监管', icon: 'gauge' },
  { id: 'audit', label: '审计日志', icon: 'list' },
  { id: 'reconcile', label: '对账', icon: 'shield' },
  { id: 'update', label: '系统更新', icon: 'refresh' },
  { id: 'settings', label: '站点设置', icon: 'settings' },
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
  logging.value = true
  loginMsg.value = ''
  try {
    const j = await apiJson<{ token: string; last_login?: { ts: number; ip: string } | null }>('/admin/login', {
      method: 'POST', body: { password: loginPw.value },
    })
    token.value = j.token
    try { localStorage.setItem(TOKEN_KEY, j.token) } catch { /* 忽略 */ }
    sessMsg.value = ''
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
  if (v === 'lines') { loadLines(); loadCodex() }
  if (v === 'quota') loadQuota()
  if (v === 'pool') loadPool()
  if (v === 'supervision') loadSupervision()
  if (v === 'audit') loadAudit()
  if (v === 'reconcile') loadReconcile()
  if (v === 'update') loadUpdate()
  if (v === 'settings') loadSettings()
}

/* 金额换算：微元 → 元（3 位小数） */
function yuan(micro: number | undefined | null): string {
  if (micro == null) return '—'
  return (micro / 1_000_000).toFixed(3)
}
function toMicro(y: string): number { return Math.round((Number(y) || 0) * 1_000_000) }
function fmtTime(ts: number): string { return ts ? new Date(ts * 1000).toLocaleString() : '—' }

const flowTypeLabel: Record<string, string> = {
  prehold: '预扣', billed: '计费确认', refunded: '退回', topup: '站长发放', deduct: '站长扣减',
}

/* ===== 站点设置（settings 表外置化：改完即时生效，无需改代码发版） ===== */
const SETTINGS_FIELDS: { key: string; label: string; ph: string; hint?: string }[] = [
  { key: 'site_name', label: '站点名称', ph: 'AQUA Gateway' },
  { key: 'qq_group', label: 'QQ 一群号', ph: '' },
  { key: 'qq_group_url', label: 'QQ 一群加群链接', ph: 'https://qm.qq.com/…' },
  { key: 'qq_group2', label: 'QQ 二群号', ph: '' },
  { key: 'qq_group_url2', label: 'QQ 二群加群链接', ph: '' },
  { key: 'rate_promo', label: '活动倍率', ph: '0.1', hint: '收费模型活动期计费倍率' },
  { key: 'rate_normal', label: '常规倍率', ph: '0.5', hint: '活动期结束后回落的常规倍率' },
  { key: 'rate_promo_vip', label: 'VIP 倍率', ph: '0.05' },
  { key: 'announcement', label: '全站公告内容', ph: '（支持一句自然文案，空=不展示）' },
  { key: 'announcement_enabled', label: '公告开关', ph: '1 开 / 0 关', hint: '仅 1 时前端展示公告' },
]
const settingsForm = ref<Record<string, string>>({})
const settingsMsg = ref('')
const settingsBusy = ref(false)

async function loadSettings() {
  settingsMsg.value = ''
  try {
    const j = await apiJson<{ settings?: Record<string, string> }>('/admin/settings', { key: token.value })
    settingsForm.value = j.settings || {}
  } catch (e) { settingsMsg.value = errText(e) }
}

async function saveSettings() {
  settingsBusy.value = true
  settingsMsg.value = ''
  try {
    const j = await apiJson<{ message?: string }>('/admin/settings', { method: 'POST', key: token.value, body: settingsForm.value })
    settingsMsg.value = j.message || '已保存'
  } catch (e) { settingsMsg.value = errText(e) }
  settingsBusy.value = false
}

/* ===== 众筹池（acu/ 公共算力池）：状态 + 官方注入 ===== */
const poolStatus = ref<any>(null)
const poolFlows = ref<any[]>([])
const seedAmt = ref<number>(10)
const seedPw = ref('')
const seeding = ref(false)
const poolMsg = ref('')
const poolMsgOk = ref(false)
async function loadPool() {
  try { poolStatus.value = await apiJson<any>('/pool/status') } catch { poolStatus.value = null }
  try {
    const j = await apiJson<{ items?: any[] }>('/pool/flows?limit=50')
    poolFlows.value = j.items || []
  } catch { poolFlows.value = [] }
}
async function doSeed() {
  const micro = Math.round((Number(seedAmt.value) || 0) * 1_000_000)
  if (micro <= 0) { poolMsg.value = '注入金额须大于 0'; poolMsgOk.value = false; return }
  seeding.value = true
  poolMsg.value = ''
  try {
    const j = await apiJson<{ balance_micro: number }>('/admin/pool/seed', {
      method: 'POST', key: token.value, body: { amount_micro: micro, confirm_password: seedPw.value },
    })
    poolMsg.value = `注入成功，池子当前 ¥${yuan(j.balance_micro)}`
    poolMsgOk.value = true
    seedPw.value = ''
    loadPool()
  } catch (e: any) { poolMsg.value = e?.message || String(e); poolMsgOk.value = false }
  seeding.value = false
}

/* ===== 仪表盘 ===== */
const stats = ref<any>(null)
const statsMsg = ref('')
const loadingStats = ref(false)
async function loadStats() {
  if (!token.value) return
  loadingStats.value = true
  statsMsg.value = ''
  try { stats.value = await apiJson<any>('/admin/stats', { key: token.value }) }
  catch (e) { statsMsg.value = errText(e) }
  loadingStats.value = false
}
const quotaPct = computed(() => {
  if (!stats.value?.upstream) return 0
  const { total_micro, used_micro } = stats.value.upstream
  return total_micro > 0 ? Math.min(100, Math.round((used_micro / total_micro) * 100)) : 0
})
const rate = (c: number, ok: number) => (c > 0 ? Math.round((ok / c) * 1000) / 10 : 100)
/** 近 8 日收入（div 条形图） */
const trend8 = computed<any[]>(() => (stats.value?.trend || []).slice(-8))
const trendMaxIncome = computed(() => Math.max(1, ...trend8.value.map((t: any) => Number(t.income_micro) || 0)))
function barH(v: number): number { return Math.max(3, Math.round(((Number(v) || 0) / trendMaxIncome.value) * 110)) }
function fmtDay(ts: number | string): string {
  const d = new Date(Number(ts) * 1000)
  const p = (x: number) => (x < 10 ? '0' : '') + x
  return `${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

/* ===== 客户管理 ===== */
const usersQ = ref('')
const usersPage = ref(1)
const usersTotal = ref(0)
const usersItems = ref<any[]>([])
const usersMsg = ref('')
const loadingUsers = ref(false)
async function loadUsers() {
  if (!token.value) return
  loadingUsers.value = true
  usersMsg.value = ''
  try {
    const q = usersQ.value.trim() ? `&q=${encodeURIComponent(usersQ.value.trim())}` : ''
    const j = await apiJson<{ items?: any[]; total?: number }>(`/admin/users?page=${usersPage.value}${q}`, { key: token.value })
    usersItems.value = j.items || []
    usersTotal.value = j.total || 0
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
  adjusting.value = true
  adjustMsg.value = ''
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

/* 用户详情抽屉（流水分页） */
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
    detail.value!.flows = j.flows.items
    detail.value!.page = j.flows.page
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
async function doTopup() {
  const micro = toMicro(topup.value.amount)
  if (micro <= 0) { topupMsg.value = '请输入充值金额'; return }
  topping.value = true
  topupMsg.value = ''
  try {
    const j = await apiJson<{ total_before: number; total_after: number }>('/admin/quota/topup', {
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
  syncing.value = true
  syncMsg.value = ''
  try {
    const j = await apiJson<{ remain_micro: number; total_before: number; total_after: number }>('/admin/quota/sync', {
      method: 'POST', key: token.value,
      body: { remain_micro: micro, note: sync.value.note.trim(), confirm_password: sync.value.pw },
    })
    syncMsg.value = `同步成功：上游剩余 ¥${yuan(j.remain_micro)}（池总额度 ¥${yuan(j.total_before)} → ¥${yuan(j.total_after)}）`
    sync.value = { amount: '', note: '', pw: '' }
    loadQuota(); loadStats(); loadSupervision()
  } catch (e) { syncMsg.value = errText(e) }
  syncing.value = false
}

/* ===== 审计 ===== */
const auditItems = ref<any[]>([])
const auditPage = ref(1)
const auditTotal = ref(0)
const auditMsg = ref('')
async function loadAudit() {
  if (!token.value) return
  try {
    const j = await apiJson<{ items?: any[]; total?: number }>(`/admin/audit?page=${auditPage.value}`, { key: token.value })
    auditItems.value = j.items || []
    auditTotal.value = j.total || 0
  } catch (e) { auditMsg.value = errText(e) }
}
const auditPages = computed(() => Math.max(1, Math.ceil(auditTotal.value / 30)))

/* ===== 对账 ===== */
const reconcile = ref<any>(null)
const reconcileMsg = ref('')
const reconciling = ref(false)
async function loadReconcile() {
  if (!token.value) return
  reconciling.value = true
  reconcileMsg.value = ''
  try { reconcile.value = await apiJson<any>('/admin/reconcile', { key: token.value }) }
  catch (e) { reconcileMsg.value = errText(e) }
  reconciling.value = false
}

/* ===== 额度监管（发放台账 + 余额持有） ===== */
const sup = ref<any>(null)
const supMsg = ref('')
const supQ = ref('')
const supPage = ref(1)
const loadingSup = ref(false)
async function loadSupervision() {
  if (!token.value) return
  loadingSup.value = true
  supMsg.value = ''
  try {
    const q = supQ.value.trim() ? `&q=${encodeURIComponent(supQ.value.trim())}` : ''
    sup.value = await apiJson<any>(`/admin/supervision?page=${supPage.value}${q}`, { key: token.value })
  } catch (e) { supMsg.value = errText(e) }
  loadingSup.value = false
}
function searchSup() { supPage.value = 1; loadSupervision() }
const supPages = computed(() => Math.max(1, Math.ceil(((sup.value?.ledger?.total) || 0) / 20)))

/* 余额持有占比 */
function supShare(micro: number): string {
  const liab = sup.value?.core?.liability_micro || 0
  return liab > 0 ? ((micro / liab) * 100).toFixed(1) + '%' : '—'
}

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
  loadingUpdate.value = true
  updateMsg.value = ''
  updateMsgOk.value = false
  try {
    update.value = await apiJson<any>('/admin/update/check', { key: token.value })
  } catch (e) { updateMsg.value = errText(e) }
  loadingUpdate.value = false
}
function askUpdate(tag: string) {
  applyTag.value = tag
  applyPw.value = ''
  applyMsg.value = ''
}
async function doUpdate() {
  if (!applyTag.value || !applyPw.value) return
  applying.value = true
  applyMsg.value = ''
  try {
    const j = await apiJson<{ message?: string }>('/admin/update/apply', {
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

/* ===== 上游管理（线路/密钥池/模型映射，DB 事实源 + 热重载） ===== */
const linesItems = ref<any[]>([])
const linesMsg = ref('')
const loadingLines = ref(false)
const lineOpen = ref('')               // 展开的线路 id
const lineDetail = ref<Record<string, { keys: any[]; models: any[] }>>({})
const rate10 = (r: number | undefined | null): string => (r == null ? '—' : (r / 10).toFixed(1)) // 万分率 → 元/百万tokens

async function loadLines() {
  if (!token.value) return
  loadingLines.value = true
  linesMsg.value = ''
  try {
    const j = await apiJson<{ lines?: any[] }>('/admin/lines', { key: token.value })
    linesItems.value = j.lines || []
  } catch (e) { linesMsg.value = errText(e) }
  loadingLines.value = false
}
async function openLine(id: string) {
  if (lineOpen.value === id) { lineOpen.value = ''; return }
  lineOpen.value = id
  if (!lineDetail.value[id]) await refreshLine(id)
}
async function refreshLine(id: string) {
  try {
    const [k, m] = await Promise.all([
      apiJson<{ keys?: any[] }>(`/admin/lines/${id}/keys`, { key: token.value }),
      apiJson<{ models?: any[] }>(`/admin/lines/${id}/models`, { key: token.value }),
    ])
    lineDetail.value = { ...lineDetail.value, [id]: { keys: k.keys || [], models: m.models || [] } }
  } catch (e) { linesMsg.value = errText(e) }
}
function modeLabel(m: string): string {
  return m === 'per_token' ? '按量' : m === 'per_call' ? '按次' : m === 'free' ? '免费' : m
}

/* ===== Codex 账号池运维（账号用量/存活/利润 + 代理健康/换线） ===== */
const codexData = ref<any>(null)
const codexMsg = ref('')
const codexBusy = ref(false)
const codexFmtM = (t?: number): string => t == null ? '—' : t >= 1e6 ? (t / 1e6).toFixed(2) + 'M' : t >= 1e3 ? (t / 1e3).toFixed(0) + 'K' : String(t)
const codexAgo = (ts?: number): string => {
  if (!ts) return '—'
  const d = Math.max(0, Math.floor(Date.now() / 1000 - ts))
  return d < 60 ? d + '秒前' : d < 3600 ? Math.floor(d / 60) + '分前' : Math.floor(d / 3600) + '时前'
}
async function loadCodex() {
  if (!token.value) return
  try { codexData.value = await apiJson<any>('/admin/codex', { key: token.value }) }
  catch (e) { codexMsg.value = errText(e) }
}
async function doCodexSwitch(node: string) {
  codexBusy.value = true
  codexMsg.value = ''
  try {
    const j = await apiJson<{ probe_ms: number }>('/admin/codex/proxy/switch', { method: 'POST', key: token.value, body: { node } })
    codexMsg.value = `已切换到 ${node}，探测 ${j.probe_ms}ms 正常`
  } catch (e) { codexMsg.value = errText(e) }
  await loadCodex()
  codexBusy.value = false
}

/* NVIDIA 动态目录手动同步（POST /admin/nvidia/sync） */
const nvidiaBusy = ref(false)
const nvidiaMsg = ref('')
async function doNvidiaSync() {
  nvidiaBusy.value = true
  nvidiaMsg.value = ''
  try {
    const j = await apiJson<{ added: number; removed: number }>('/admin/nvidia/sync', { method: 'POST', key: token.value })
    nvidiaMsg.value = `NVIDIA 动态目录同步完成：新增 ${j.added} 个模型 · 移除 ${j.removed} 个`
  } catch (e) { nvidiaMsg.value = errText(e) }
  nvidiaBusy.value = false
}

/* 新增线路弹窗 */
const lineNew = ref({ open: false, id: '', name: '', mode: 'per_token', base_url: '', vip_num: '9', vip_den: '10', key_face: '', keys: '', pw: '' })
const lineNewMsg = ref('')
const lineCreating = ref(false)
function openLineNew() {
  lineNew.value = { open: true, id: '', name: '', mode: 'per_token', base_url: '', vip_num: '9', vip_den: '10', key_face: '', keys: '', pw: '' }
  lineNewMsg.value = ''
}
async function doLineCreate() {
  const n = lineNew.value
  if (!n.id || !n.name || !n.base_url) { lineNewMsg.value = '请填写线路 ID / 名称 / 上游地址'; return }
  if (!n.pw) { lineNewMsg.value = '请输入管理密码确认'; return }
  lineCreating.value = true
  lineNewMsg.value = ''
  try {
    await apiJson('/admin/lines', {
      method: 'POST', key: token.value,
      body: {
        id: n.id.trim(), name: n.name.trim(), mode: n.mode, base_url: n.base_url.trim(),
        vip_num: Number(n.vip_num) || 0, vip_den: Number(n.vip_den) || 0,
        key_face_micro: toMicro(n.key_face), keys: n.keys,
        confirm_password: n.pw,
      },
    })
    lineNew.value.open = false
    loadLines()
  } catch (e) { lineNewMsg.value = errText(e) }
  lineCreating.value = false
}

/* 停用/启用线路 */
const lineBusy = ref('')
async function doLineToggle(id: string, enabled: boolean) {
  const pw = prompt(enabled ? `启用线路 ${id}：请输入管理密码确认` : `停用线路 ${id}（调用方将收到 404）：请输入管理密码确认`)
  if (!pw) return
  lineBusy.value = id
  try {
    await apiJson(`/admin/lines/${id}`, { method: 'POST', key: token.value, body: { enabled, confirm_password: pw } })
    loadLines()
  } catch (e) { linesMsg.value = errText(e) }
  lineBusy.value = ''
}
/* 删除线路（须先停用） */
async function doLineDelete(id: string) {
  const pw = prompt(`删除线路 ${id}（同时删除其密钥池与模型映射，不可恢复）：请输入管理密码确认`)
  if (!pw) return
  lineBusy.value = id
  try {
    await apiJson(`/admin/lines/${id}`, { method: 'DELETE', key: token.value, body: { confirm_password: pw } })
    if (lineOpen.value === id) lineOpen.value = ''
    loadLines()
  } catch (e) { linesMsg.value = errText(e) }
  lineBusy.value = ''
}

/* 批量加钥 */
const keyAdd = ref({ line: '', raw: '', pw: '' })
const keyAddMsg = ref('')
const keyAdding = ref(false)
function openKeyAdd(id: string) { keyAdd.value = { line: id, raw: '', pw: '' }; keyAddMsg.value = '' }
async function doKeysAdd() {
  const k = keyAdd.value
  if (!k.raw.trim() || !k.pw) { keyAddMsg.value = '请粘贴密钥并输入管理密码'; return }
  keyAdding.value = true
  keyAddMsg.value = ''
  try {
    const j = await apiJson<{ added: number; duplicates?: number }>(`/admin/lines/${k.line}/keys`, {
      method: 'POST', key: token.value, body: { keys: k.raw, confirm_password: k.pw },
    })
    keyAdd.value.raw = ''
    keyAdd.value.pw = ''
    keyAddMsg.value = `已添加 ${j.added} 把${j.duplicates ? `（重复跳过 ${j.duplicates}）` : ''}`
    await refreshLine(k.line)
    loadLines()
  } catch (e) { keyAddMsg.value = errText(e) }
  keyAdding.value = false
}
/* 余额校准（上游真实余额为唯一真源） */
const keyCal = ref({ line: '', text: '', pw: '' })
const keyCalMsg = ref('')
const keyCalOk = ref(false)
const keyCalBusy = ref(false)
function openKeyCal(id: string) { keyCal.value = { line: id, text: '', pw: '' }; keyCalMsg.value = ''; keyCalOk.value = false }
async function doKeysCal() {
  const k = keyCal.value
  if (!k.text.trim() || !k.pw) { keyCalMsg.value = '请粘贴查询结果并输入管理密码'; keyCalOk.value = false; return }
  keyCalBusy.value = true
  keyCalMsg.value = ''
  try {
    const j = await apiJson<any>(`/admin/lines/${k.line}/keys/calibrate`, {
      method: 'POST', key: token.value, body: { text: k.text, confirm_password: k.pw },
    })
    keyCal.value.pw = ''
    keyCalOk.value = true
    keyCalMsg.value = `校准完成：匹配 ${j.matched} 把（判死 ${(j.killed || []).length} 把 / 活性 ${(j.revived || []).length} 把 / 未匹配 ${j.unknown}），当前活钥余额合计 ¥${j.live_balance_yuan}`
    await refreshLine(k.line)
    loadLines()
  } catch (e) { keyCalMsg.value = errText(e); keyCalOk.value = false }
  keyCalBusy.value = false
}
/* 一键查询并校准（后端代查 ge.bbs0.cc 查询服务） */
async function doKeysCalAuto(id: string) {
  const pw = prompt('将实时查询该线全部存活密钥的上游余额并自动校准（余额为 0 的自动判死摘除）：请输入管理密码确认')
  if (!pw) return
  keyCalBusy.value = true
  keyCal.line = id
  keyCalMsg.value = ''
  keyCalOk.value = false
  try {
    const j = await apiJson<any>(`/admin/lines/${id}/keys/calibrate-auto`, {
      method: 'POST', key: token.value, body: { confirm_password: pw },
    })
    keyCalOk.value = true
    keyCalMsg.value = `查询并校准完成：查询 ${j.queried} 把（判死 ${(j.killed || []).length} 把 #${(j.killed || []).join(',#')} / 校准 ${(j.revived || []).length} 把 / 未返回 ${j.unqueried}），当前活钥余额合计 ¥${j.live_balance_yuan}`
    await refreshLine(id)
    loadLines()
  } catch (e) { keyCalMsg.value = errText(e); keyCalOk.value = false }
  keyCalBusy.value = false
}
/* 密钥停用/启用/删除 */
const keyBusy = ref(-1)
async function doKeyDead(id: string, idx: number, dead: boolean) {
  const pw = prompt(`#${idx} 号密钥将${dead ? '停用（不再参与轮询）' : '重新启用'}：请输入管理密码确认`)
  if (!pw) return
  keyBusy.value = idx
  try {
    await apiJson(`/admin/lines/${id}/keys/${idx}/dead`, { method: 'POST', key: token.value, body: { dead, confirm_password: pw } })
    await refreshLine(id)
    loadLines()
  } catch (e) { linesMsg.value = errText(e) }
  keyBusy.value = -1
}
async function doKeyDelete(id: string, idx: number) {
  const pw = prompt(`删除 #${idx} 号密钥（仅从线路摘除，面值台账保留）：请输入管理密码确认`)
  if (!pw) return
  keyBusy.value = idx
  try {
    await apiJson(`/admin/lines/${id}/keys/${idx}`, { method: 'DELETE', key: token.value, body: { confirm_password: pw } })
    await refreshLine(id)
    loadLines()
  } catch (e) { linesMsg.value = errText(e) }
  keyBusy.value = -1
}

/* 模型映射 upsert / 删除 */
const modelEdit = ref({
  open: false, line: '', site_id: '', upstream_id: '', image: false,
  per_call_sell: '', per_call_cost: '',
  in_sell: '', cache_sell: '', out_sell: '', in_cost: '', cache_cost: '', out_cost: '', key_idx: '', pw: '',
})
const modelEditMsg = ref('')
const modelSaving = ref(false)
function openModelEdit(id: string, m?: any) {
  modelEdit.value = {
    open: true, line: id,
    site_id: m?.site_id || '', upstream_id: m?.upstream_id || '', image: !!m?.image,
    per_call_sell: m?.per_call_sell ? String(m.per_call_sell) : '',
    per_call_cost: m?.per_call_cost ? String(m.per_call_cost) : '',
    in_sell: m?.in_sell_rate10 ? String(m.in_sell_rate10) : '', cache_sell: m?.cache_sell_rate10 ? String(m.cache_sell_rate10) : '', out_sell: m?.out_sell_rate10 ? String(m.out_sell_rate10) : '',
    in_cost: m?.in_cost_rate10 ? String(m.in_cost_rate10) : '', cache_cost: m?.cache_cost_rate10 ? String(m.cache_cost_rate10) : '', out_cost: m?.out_cost_rate10 ? String(m.out_cost_rate10) : '',
    key_idx: m?.key_idx != null && m.key_idx >= 0 ? String(m.key_idx) : '',
    pw: '',
  }
  modelEditMsg.value = ''
}
async function doModelUpsert() {
  const m = modelEdit.value
  if (!m.site_id.trim() || !m.pw) { modelEditMsg.value = '请填写站点模型 ID 与管理密码'; return }
  modelSaving.value = true
  modelEditMsg.value = ''
  try {
    const j = await apiJson<any>(`/admin/lines/${m.line}/models`, {
      method: 'POST', key: token.value,
      body: {
        site_id: m.site_id.trim(), upstream_id: m.upstream_id.trim() || m.site_id.trim(), image: m.image,
        per_call_sell: Number(m.per_call_sell) || 0, per_call_cost: Number(m.per_call_cost) || 0,
        in_sell_rate10: Number(m.in_sell) || 0, cache_sell_rate10: Number(m.cache_sell) || 0, out_sell_rate10: Number(m.out_sell) || 0,
        in_cost_rate10: Number(m.in_cost) || 0, cache_cost_rate10: Number(m.cache_cost) || 0, out_cost_rate10: Number(m.out_cost) || 0,
        key_idx: m.key_idx.trim() === '' ? -1 : (Number(m.key_idx) ?? -1),
        confirm_password: m.pw,
      },
    })
    modelEditMsg.value = `已保存${j.pricing_seeded ? `，播种价目 ${j.pricing_seeded} 组` : ''}`
    await refreshLine(m.line)
    loadLines()
    setTimeout(() => { modelEdit.value.open = false }, 700)
  } catch (e) { modelEditMsg.value = errText(e) }
  modelSaving.value = false
}
async function doModelDelete(id: string, site: string) {
  const pw = prompt(`删除模型映射 ${site}（历史价目保留，用户将无法再调用该模型）：请输入管理密码确认`)
  if (!pw) return
  try {
    await apiJson(`/admin/lines/${id}/models/${site}`, { method: 'DELETE', key: token.value, body: { confirm_password: pw } })
    await refreshLine(id)
    loadLines()
  } catch (e) { linesMsg.value = errText(e) }
}
/* 手动热重载 */
const reloading = ref(false)
async function doLinesReload() {
  reloading.value = true
  try {
    await apiJson('/admin/lines/reload', { method: 'POST', key: token.value })
    linesMsg.value = '已重载全部线路（内存配置已刷新）'
  } catch (e) { linesMsg.value = errText(e) }
  reloading.value = false
}

/* 渠道连通测试（诊断 D5：上游故障一屏定位） */
const lineTesting = ref('')                          // 正在测试的线 id
const lineTestResult = ref<Record<string, any>>({})  // line id → { ok, status_code, latency_ms, upstream_models }
const testAllBusy = ref(false)
async function doLineTest(id: string) {
  lineTesting.value = id
  try {
    const j = await apiJson<any>(`/admin/lines/${id}/test`, { method: 'POST', key: token.value })
    lineTestResult.value = { ...lineTestResult.value, [id]: j }
  } catch (e) { linesMsg.value = errText(e) }
  lineTesting.value = ''
}
async function doLinesTestAll() {
  testAllBusy.value = true
  try {
    const j = await apiJson<{ items?: any[] }>('/admin/lines/test-all', { method: 'POST', key: token.value })
    const map: Record<string, any> = {}
    for (const it of (j.items || [])) map[it.line] = it
    lineTestResult.value = map
  } catch (e) { linesMsg.value = errText(e) }
  testAllBusy.value = false
}
function testLabel(id: string): string {
  const r = lineTestResult.value[id]
  if (!r) return ''
  return `${r.ok ? '✓ 连通' : '✗ 异常'}${r.latency_ms != null ? ` ${r.latency_ms}ms` : ''}${r.status_code ? ` HTTP${r.status_code}` : ''}${r.upstream_models != null ? ` · 上游 ${r.upstream_models} 模型` : ''}`
}

/* 模型降级标记（诊断 D4：上游故障时手动标记，用户端 /v1/models 显示降级） */
const degradedBusy = ref('')
async function doModelDegraded(id: string, site: string, degraded: boolean) {
  const pw = prompt(`将模型 ${site} ${degraded ? '标记为降级（用户端显示"该模型上游异常"，请尽快切换渠道或修复）' : '恢复正常'}：请输入管理密码确认`)
  if (!pw) return
  degradedBusy.value = site
  try {
    await apiJson(`/admin/lines/${id}/models/${site}/degraded`, { method: 'POST', key: token.value, body: { degraded, confirm_password: pw } })
    await refreshLine(id)
  } catch (e) { linesMsg.value = errText(e) }
  degradedBusy.value = ''
}

/* 未定价判定：无按次价、无按量费率、无图价 → 用户侧 404，需补价 */
function isUnpriced(m: any): boolean {
  return !m.per_call_sell && !m.in_sell_rate10 && !m.out_sell_rate10 && !m.per_image_sell
}

/* 上游模型拉取（诊断 D5/A6：添加映射时直选上游 ID，免去手抄） */
const upModels = ref({ open: false, line: '', items: [] as string[], msg: '' })
async function openUpModels(id: string) {
  upModels.value = { open: true, line: id, items: [], msg: '拉取中…' }
  try {
    const j = await apiJson<{ models?: string[] }>(`/admin/lines/${id}/upstream-models`, { key: token.value })
    upModels.value.items = j.models || []
    upModels.value.msg = upModels.value.items.length ? '' : '上游未返回任何模型'
  } catch (e) { upModels.value.msg = errText(e) }
}
function pickUpModel(mid: string) {
  modelEdit.value.upstream_id = mid
  if (!modelEdit.value.site_id) modelEdit.value.site_id = mid.split('/').pop() || mid
  upModels.value.open = false
}

/* ===== 用户管理操作（封禁/重置密码/价目组/踢下线/密钥/注销） ===== */
const umgr = ref({
  open: false, uid: 0, name: '', email: '', status: 1,
  grpCall: 'normal', grpToken: 'normal', pw: '', confirmEmail: '',
  msg: '', msgOk: false, loading: '', newPwd: '',
})
function openUserMgr(u: any) {
  umgr.value = {
    open: true, uid: u.id, name: u.username, email: u.email, status: u.status,
    grpCall: u.price_grp_call || 'normal', grpToken: u.price_grp_token || 'normal',
    pw: '', confirmEmail: '', msg: '', msgOk: false, loading: '', newPwd: '',
  }
}
function umgrFail(e: unknown) { umgr.value.msg = errText(e); umgr.value.msgOk = false }
async function doUserBan(ban: boolean) {
  const m = umgr.value
  if (!m.pw) { m.msg = '请先输入管理密码'; return }
  m.loading = 'ban'
  try {
    await apiJson(`/admin/users/${m.uid}/status`, {
      method: 'POST', key: token.value,
      body: { status: ban ? 0 : 1, note: ban ? '管理后台封禁' : '管理后台解封', confirm_password: m.pw },
    })
    m.status = ban ? 0 : 1
    m.msg = ban ? '已封禁：密钥与会话全部失效' : '已解封：用户恢复正常'
    m.msgOk = true
    loadUsers()
  } catch (e) { umgrFail(e) }
  m.loading = ''
}
async function doUserKick() {
  const m = umgr.value
  if (!m.pw) { m.msg = '请先输入管理密码'; return }
  m.loading = 'kick'
  try {
    const j = await apiJson<{ sessions_removed: number }>(`/admin/users/${m.uid}/kick`, { method: 'POST', key: token.value, body: { confirm_password: m.pw } })
    m.msg = `已强制下线 ${j.sessions_removed} 个会话`
    m.msgOk = true
  } catch (e) { umgrFail(e) }
  m.loading = ''
}
async function doUserResetPwd() {
  const m = umgr.value
  if (!m.pw) { m.msg = '请先输入管理密码'; return }
  m.loading = 'pwd'
  try {
    const j = await apiJson<{ new_password: string }>(`/admin/users/${m.uid}/password`, { method: 'POST', key: token.value, body: { confirm_password: m.pw } })
    m.newPwd = j.new_password
    m.msg = '密码已重置并强制下线，新密码仅本次显示'
    m.msgOk = true
  } catch (e) { umgrFail(e) }
  m.loading = ''
}
async function doUserGrp() {
  const m = umgr.value
  if (!m.pw) { m.msg = '请先输入管理密码'; return }
  m.loading = 'grp'
  try {
    await apiJson(`/admin/users/${m.uid}/price-grp`, {
      method: 'POST', key: token.value,
      body: { price_grp_call: m.grpCall, price_grp_token: m.grpToken, confirm_password: m.pw },
    })
    m.msg = '价目组已更新'
    m.msgOk = true
    loadUsers()
  } catch (e) { umgrFail(e) }
  m.loading = ''
}
async function doUserDelete() {
  const m = umgr.value
  if (!m.pw) { m.msg = '请先输入管理密码'; return }
  if (m.confirmEmail.trim() !== m.email) { m.msg = '确认邮箱与用户邮箱不一致'; return }
  m.loading = 'del'
  try {
    await apiJson(`/admin/users/${m.uid}`, {
      method: 'DELETE', key: token.value,
      body: { confirm_email: m.confirmEmail.trim(), confirm_password: m.pw },
    })
    m.open = false
    loadUsers()
  } catch (e) { umgrFail(e) }
  m.loading = ''
}
/* 用户密钥列表 + 吊销 */
const ukeys = ref<{ open: boolean; uid: number; name: string; items: any[]; msg: string }>({ open: false, uid: 0, name: '', items: [], msg: '' })
async function openUserKeys(u: any) {
  ukeys.value = { open: true, uid: u.id, name: u.username, items: [], msg: '' }
  try {
    const j = await apiJson<{ keys?: any[] }>(`/admin/users/${u.id}/keys`, { key: token.value })
    ukeys.value.items = j.keys || []
  } catch (e) { ukeys.value.msg = errText(e) }
}
async function doUserKeyRevoke(kid: number) {
  const pw = prompt(`吊销密钥 #${kid}（用户将无法再用该密钥调用）：请输入管理密码确认`)
  if (!pw) return
  try {
    await apiJson(`/admin/users/${ukeys.value.uid}/keys/${kid}/revoke`, { method: 'POST', key: token.value, body: { confirm_password: pw } })
    const j = await apiJson<{ keys?: any[] }>(`/admin/users/${ukeys.value.uid}/keys`, { key: token.value })
    ukeys.value.items = j.keys || []
  } catch (e) { ukeys.value.msg = errText(e) }
}
</script>

<template>
  <div class="wrap" style="max-width: 1080px;">
    <!-- ===== 未登录：密码门（居中玻璃卡） ===== -->
    <div v-if="!token" class="gate fade-up">
      <div class="card accent gate-card">
        <div class="gate-logo"><AqIcon name="shield" :size="26" /></div>
        <h1 style="margin: 0; font-size: 19px;">管理控制台</h1>
        <div class="dim">仅站长访问 · 所有操作全程审计</div>
        <input
          v-model="loginPw" class="input" type="password" placeholder="管理密码" autocomplete="current-password"
          :disabled="logging" @keydown.enter="doLogin"
        />
        <button class="btn primary block" :disabled="logging || !loginPw" @click="doLogin">
          {{ logging ? '验证中…' : '进入控制台' }}
        </button>
        <p v-if="loginMsg" class="msg bad" style="margin: 0;">{{ loginMsg }}</p>
        <p v-if="sessMsg" class="msg bad" style="margin: 0;">{{ sessMsg }}</p>
      </div>
    </div>

    <!-- ===== 已登录 ===== -->
    <template v-else>
      <div class="fade-up">
        <div class="page-head">
          <div>
            <h1><AqIcon name="shield" :size="24" /> 管理控制台</h1>
            <div class="sub">站点资金 / 客户 / 上游 / 审计一站式管理 · 高危操作全部二次密码确认</div>
          </div>
          <div class="ops">
            <button class="btn danger sm" @click="doLogout"><AqIcon name="arrow-right" :size="14" /> 退出登录</button>
          </div>
        </div>

        <!-- 上次登录自查横幅 -->
        <div v-if="lastLogin && lastLogin.ts" class="banner mb16">
          <AqIcon name="info" :size="14" />
          上次登录：{{ fmtTime(lastLogin.ts) }} · IP {{ lastLogin.ip || '—' }}（非本人操作请立即改密）
        </div>

        <!-- 视图 tab（横向可换行） -->
        <div class="chips mb16">
          <button v-for="n in NAV" :key="n.id" class="chip" :class="{ on: view === n.id }" @click="go(n.id)">
            <AqIcon :name="n.icon" :size="14" /> {{ n.label }}
          </button>
        </div>

        <p v-if="sessMsg" class="msg bad">{{ sessMsg }}</p>

        <!-- ▼ 仪表盘 ▼ -->
        <div v-if="view === 'dashboard'" style="display: grid; gap: 16px;">
          <p v-if="statsMsg" class="msg bad" style="margin: 0;">{{ statsMsg }}</p>
          <div v-if="loadingStats && !stats" style="display: grid; gap: 12px;">
            <div class="skeleton" style="min-height: 140px;"></div>
            <div class="skeleton" style="min-height: 90px;"></div>
          </div>
          <template v-else-if="stats">
            <!-- 资金池大卡 -->
            <div class="card accent">
              <b><AqIcon name="server" :size="16" /> 上游共享资金池（acu / acu2 共用上游钱包）</b>
              <div class="bar-track mt12" :title="`已消耗 ${quotaPct}%`"><span :style="{ width: quotaPct + '%' }"></span></div>
              <div class="row wrap mt8" style="gap: 18px;">
                <span class="dim">总额度 <b style="color: var(--txt0);">¥{{ yuan(stats.upstream.total_micro) }}</b></span>
                <span class="dim">已消耗 <b style="color: var(--txt0);">¥{{ yuan(stats.upstream.used_micro) }}</b></span>
                <span class="dim">剩余 <b class="grad-text" style="font-size: 15px;">¥{{ yuan(stats.upstream.remain_micro) }}</b></span>
                <span v-if="stats.upstream.circuit_open" class="tag bad">已熔断</span>
              </div>
              <div class="row between wrap mt12">
                <span class="dim">累计充值 ¥{{ yuan(stats.upstream.topup_total_micro) }} · 成本（池已消耗）¥{{ yuan(stats.upstream.cost_micro) }}<template v-if="stats.upstream.days_left >= 0"> · 预计耗尽约 {{ stats.upstream.days_left }} 天</template></span>
                <button class="btn sm primary" @click="go('quota')"><AqIcon name="bolt" :size="13" /> 去充值</button>
              </div>
            </div>

            <!-- KPI：收入 / 计费数 / 负债 -->
            <div class="kpis">
              <div class="kpi"><span>累计收入</span><b>¥{{ yuan(stats.income.all_micro) }}</b><span class="trend">今日 ¥{{ yuan(stats.income.today_micro) }} · 7 日 ¥{{ yuan(stats.income.week_micro) }} · 30 日 ¥{{ yuan(stats.income.month_micro) }}</span></div>
              <div class="kpi"><span>计费调用（累计）</span><b>{{ fmt(stats.calls.all) }}</b><span class="trend">成功率 {{ rate(stats.calls.all, stats.calls.all_ok) }}%</span></div>
              <div class="kpi"><span>待履约负债</span><b>¥{{ yuan(stats.users.liability_micro) }}</b><span class="trend">{{ stats.users.with_balance }} 人持有余额</span></div>
            </div>
            <div class="kpis">
              <div class="kpi">
                <span>计费模式</span>
                <b style="font-size: 19px;">按次计费</b>
                <span class="trend">每次成功请求扣一次，与生成长度无关；acu/ 众筹线扣站点额度，aqua/ 收费线扣余额</span>
              </div>
              <div class="kpi"><span>通道手续费（本站承担）</span><b>¥{{ yuan(stats.upstream.fee_micro || 0) }}</b><span class="trend">实付费率 {{ ((stats.upstream.fee_rate ?? 0) * 100).toFixed(2) }}%</span></div>
            </div>

            <!-- 调用统计 + 近 8 日收入条形图 -->
            <div class="grid2">
              <div class="card">
                <b><AqIcon name="activity" :size="16" /> 收费模型调用</b>
                <div class="tbl-wrap mt12">
                  <table class="table">
                    <thead><tr><th>范围</th><th class="num">调用</th><th class="num">成功率</th></tr></thead>
                    <tbody>
                      <tr><td>今日</td><td class="num">{{ fmt(stats.calls.today) }}</td><td class="num">{{ rate(stats.calls.today, stats.calls.today_ok) }}%</td></tr>
                      <tr><td>近 7 天</td><td class="num">{{ fmt(stats.calls.week) }}</td><td class="num">{{ rate(stats.calls.week, stats.calls.week_ok) }}%</td></tr>
                      <tr><td>近 30 天</td><td class="num">{{ fmt(stats.calls.month) }}</td><td class="num">{{ rate(stats.calls.month, stats.calls.month_ok) }}%</td></tr>
                      <tr><td>累计</td><td class="num">{{ fmt(stats.calls.all) }}</td><td class="num">{{ rate(stats.calls.all, stats.calls.all_ok) }}%</td></tr>
                    </tbody>
                  </table>
                </div>
                <div v-if="stats.calls.fail_dist?.length" class="row wrap mt8" style="gap: 6px;">
                  <span v-for="f in stats.calls.fail_dist" :key="f.reason" class="tag">{{ f.reason }} × {{ f.count }}</span>
                </div>
              </div>
              <div class="card">
                <b><AqIcon name="trend" :size="16" /> 近 8 日收入（¥）</b>
                <div class="chart mt12">
                  <div v-for="t in trend8" :key="t.date" class="bar-col" :title="`${fmtDay(t.date)} · 收入 ¥${yuan(t.income_micro)} · ${fmt(t.calls)} 次调用`">
                    <div class="bar" :style="{ height: barH(t.income_micro) + 'px' }"></div>
                    <span class="bar-d">{{ fmtDay(t.date) }}</span>
                  </div>
                  <div v-if="!trend8.length" class="empty" style="width: 100%;"><b>暂无数据</b></div>
                </div>
              </div>
            </div>

            <!-- 近 14 天趋势 + 消费 TOP10 -->
            <div class="grid2">
              <div class="card">
                <b><AqIcon name="clock" :size="16" /> 近 14 天趋势</b>
                <div class="tbl-wrap mt12">
                  <table class="table">
                    <thead><tr><th>日期</th><th class="num">调用</th><th class="num">收入（¥）</th></tr></thead>
                    <tbody>
                      <tr v-if="!stats.trend?.length"><td colspan="3" class="empty">暂无数据</td></tr>
                      <tr v-for="t in stats.trend" :key="t.date">
                        <td>{{ fmtDay(t.date) }}</td>
                        <td class="num">{{ fmt(t.calls) }}</td>
                        <td class="num">¥{{ yuan(t.income_micro) }}</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
              <div class="card">
                <b><AqIcon name="trophy" :size="16" /> 消费 TOP10</b>
                <div class="tbl-wrap mt12">
                  <table class="table">
                    <thead><tr><th>用户</th><th class="num">累计消费（¥）</th><th class="num">调用次数</th></tr></thead>
                    <tbody>
                      <tr v-if="!stats.top?.length"><td colspan="3" class="empty">暂无数据</td></tr>
                      <tr v-for="(t, i) in stats.top" :key="t.user_id">
                        <td><span class="tag" :class="i < 3 ? 'grad' : ''" style="margin-right: 6px;">{{ i + 1 }}</span>#{{ t.user_id }} {{ t.username || '—' }}</td>
                        <td class="num">¥{{ yuan(t.cost_micro) }}</td>
                        <td class="num">{{ fmt(t.calls) }}</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </template>
        </div>

        <!-- ▼ 客户管理 ▼ -->
        <div v-if="view === 'users'" style="display: grid; gap: 14px;">
          <div class="row wrap">
            <input v-model="usersQ" class="input" style="max-width: 380px;" placeholder="搜索用户名 / 邮箱 / UID" @keydown.enter="searchUsers" />
            <button class="btn primary" @click="searchUsers"><AqIcon name="user" :size="14" /> 搜索</button>
          </div>
          <p v-if="usersMsg" class="msg bad" style="margin: 0;">{{ usersMsg }}</p>
          <div class="tbl-wrap">
            <table class="table">
              <thead>
                <tr>
                  <th>用户</th><th>邮箱</th><th class="num">余额（¥）</th><th class="num">累计发放（¥）</th>
                  <th class="num">累计消费（¥）</th><th class="num">调用</th><th>最后活跃</th><th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="!usersItems.length">
                  <td colspan="8" class="empty">{{ loadingUsers ? '加载中…' : '没有匹配的用户' }}</td>
                </tr>
                <tr v-for="u in usersItems" :key="u.id">
                  <td>
                    <b>#{{ u.id }} {{ u.username }}</b>
                    <span v-if="u.status === 0" class="tag bad" style="margin-left: 6px;">已封禁</span>
                    <span v-else-if="u.status === 99" class="tag warn" style="margin-left: 6px;">已注销</span>
                  </td>
                  <td class="dim">{{ u.email }}</td>
                  <td class="num grad-text" style="font-weight: 700;">¥{{ yuan(u.balance_micro) }}</td>
                  <td class="num">¥{{ yuan(u.topup_micro) }}</td>
                  <td class="num">¥{{ yuan(u.cost_micro) }}</td>
                  <td class="num">{{ fmt(u.calls) }}</td>
                  <td class="dim">{{ fmtTime(u.last_login_ts) }}</td>
                  <td>
                    <div class="ops-cell">
                      <button class="btn xs" @click="openAdjust(u.id, u.username, 1)">加余额</button>
                      <button class="btn xs danger" @click="openAdjust(u.id, u.username, -1)">减余额</button>
                      <button class="btn xs" @click="openDetail(u.id)">详情</button>
                      <button class="btn xs" @click="openUserMgr(u)">管理</button>
                      <button class="btn xs" @click="openUserKeys(u)">密钥</button>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="paged" v-if="usersPages > 1">
            <button class="btn sm" :disabled="usersPage <= 1" @click="usersPage--; loadUsers()">上一页</button>
            <span class="cur">{{ usersPage }} / {{ usersPages }} 页 · 共 {{ fmt(usersTotal) }} 人</span>
            <button class="btn sm" :disabled="usersPage >= usersPages" @click="usersPage++; loadUsers()">下一页</button>
          </div>
        </div>

        <!-- ▼ 上游管理 ▼ -->
        <div v-if="view === 'lines'" style="display: grid; gap: 14px;">
          <div class="row between wrap">
            <span class="dim">线路/密钥/模型改动即时热重载生效；密钥明文添加后仅脱敏展示；高危操作需管理密码确认。</span>
            <div class="row wrap" style="gap: 8px;">
              <button class="btn sm" :disabled="testAllBusy" @click="doLinesTestAll"><AqIcon name="bolt" :size="13" /> {{ testAllBusy ? '测速中…' : '全部测速' }}</button>
              <button class="btn sm" :disabled="reloading" @click="doLinesReload"><AqIcon name="refresh" :size="13" /> {{ reloading ? '重载中…' : '重载内存配置' }}</button>
              <button class="btn sm" :disabled="nvidiaBusy" @click="doNvidiaSync"><AqIcon name="spark" :size="13" /> {{ nvidiaBusy ? '同步中…' : 'NVIDIA 目录同步' }}</button>
              <button class="btn sm primary" @click="openLineNew"><AqIcon name="plus" :size="13" /> 新增线路</button>
            </div>
          </div>
          <p v-if="nvidiaMsg" class="msg info" style="margin: 0;">{{ nvidiaMsg }}</p>
          <p v-if="linesMsg" class="msg bad" style="margin: 0;">{{ linesMsg }}</p>
          <p v-if="loadingLines && !linesItems.length" class="dim" style="margin: 0;">加载中…</p>

          <div v-for="l in linesItems" :key="l.id" class="card">
            <div class="row between wrap" style="cursor: pointer; gap: 10px;" @click="openLine(l.id)">
              <div class="row wrap" style="gap: 8px;">
                <b style="font-size: 14.5px;">{{ l.name }}</b>
                <code class="code-inline">{{ l.id }}</code>
                <span class="tag" :class="l.mode !== 'free' ? 'acc' : ''">{{ modeLabel(l.mode) }}</span>
                <span class="tag" :class="l.enabled ? 'ok' : 'bad'">{{ l.enabled ? '启用' : '停用' }}</span>
              </div>
              <div class="row wrap" style="gap: 6px;" @click.stop>
                <span class="dim">钥 {{ l.keys_total - l.keys_dead }}/{{ l.keys_total }} · 模型 {{ l.models_total }} · 近1h {{ rate(l.calls_1h, l.ok_1h) }}%</span>
                <span v-if="l.face_initial_micro > 0" class="dim">面值 ¥{{ yuan(l.face_used_micro) }} / ¥{{ yuan(l.face_initial_micro) }}</span>
                <button class="btn xs" :disabled="lineTesting === l.id" @click="doLineTest(l.id)">{{ lineTesting === l.id ? '测速中…' : '测速' }}</button>
                <button class="btn xs" :disabled="lineBusy === l.id" @click="doLineToggle(l.id, !l.enabled)">{{ l.enabled ? '停用' : '启用' }}</button>
                <button v-if="!l.enabled" class="btn xs danger" :disabled="lineBusy === l.id" @click="doLineDelete(l.id)">删除</button>
              </div>
            </div>
            <div v-if="lineTestResult[l.id]" class="mt8"><span class="tag" :class="lineTestResult[l.id].ok ? 'ok' : 'bad'">{{ testLabel(l.id) }}</span></div>

            <template v-if="lineOpen === l.id && lineDetail[l.id]">
              <!-- 密钥池 -->
              <div class="mt16 row between wrap">
                <b style="font-size: 13.5px;">密钥池（{{ lineDetail[l.id].keys.length }} 把存活）</b>
                <div class="row wrap" style="gap: 6px;">
                  <button class="btn xs" @click="openKeyAdd(l.id)"><AqIcon name="key" :size="12" /> 批量添加密钥</button>
                  <button class="btn xs" :disabled="keyCalBusy" @click="doKeysCalAuto(l.id)">{{ keyCalBusy ? '查询校准中…' : '一键查询并校准' }}</button>
                  <button class="btn xs" @click="openKeyCal(l.id)">余额校准</button>
                </div>
              </div>
              <div class="tbl-wrap mt8">
                <table class="table">
                  <thead><tr><th style="width: 56px;">#</th><th>密钥（脱敏）</th><th>状态</th><th class="num">面值已耗（¥）</th><th>操作</th></tr></thead>
                  <tbody>
                    <tr v-if="!lineDetail[l.id].keys.length"><td colspan="5" class="empty">暂无存活密钥，请添加</td></tr>
                    <tr v-for="k in lineDetail[l.id].keys" :key="k.idx">
                      <td class="num">{{ k.idx }}</td>
                      <td class="dim" style="font-family: var(--mono); font-size: 11.5px;">{{ k.masked }}</td>
                      <td><span class="tag" :class="k.dead ? 'bad' : 'ok'">{{ k.dead ? '已停用' : '存活' }}</span></td>
                      <td class="num">¥{{ yuan(k.face_used_micro) }}</td>
                      <td>
                        <div class="ops-cell">
                          <button class="btn xs" :disabled="keyBusy === k.idx" @click="doKeyDead(l.id, k.idx, !k.dead)">{{ k.dead ? '启用' : '停用' }}</button>
                          <button class="btn xs danger" :disabled="keyBusy === k.idx" @click="doKeyDelete(l.id, k.idx)">删除</button>
                        </div>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p v-if="keyAdd.line === l.id && keyAddMsg" class="msg mt8" :class="keyAddMsg.includes('已添加') ? 'ok' : 'bad'">{{ keyAddMsg }}</p>

              <!-- 模型映射 -->
              <div class="mt16 row between wrap">
                <b style="font-size: 13.5px;">模型映射（站点模型 → 上游模型 + 计费）</b>
                <button class="btn xs" @click="openModelEdit(l.id)"><AqIcon name="plus" :size="12" /> 添加模型映射</button>
              </div>
              <div class="tbl-wrap mt8">
                <table class="table">
                  <thead>
                    <tr><th>站点模型</th><th>上游模型</th><th class="num">售/次</th><th class="num">成/次</th>
                      <th class="num">入/百万</th><th class="num">缓存/百万</th><th class="num">出/百万</th><th>操作</th></tr>
                  </thead>
                  <tbody>
                    <tr v-if="!lineDetail[l.id].models.length"><td colspan="8" class="empty">暂无模型映射</td></tr>
                    <tr v-for="m in lineDetail[l.id].models" :key="m.site_id">
                      <td>
                        {{ l.id }}/{{ m.site_id }}
                        <span v-if="isUnpriced(m)" class="tag warn" title="未配置任何售价，用户调用将 404，请补价" style="margin-left: 4px;">未定价</span>
                      </td>
                      <td class="dim" style="font-family: var(--mono); font-size: 11.5px;">
                        {{ m.upstream_id }}
                        <span v-if="m.image" class="tag" style="margin-left: 4px;">图</span>
                        <span v-if="m.degraded" class="tag bad" style="margin-left: 4px;">降级</span>
                      </td>
                      <td class="num">{{ m.per_call_sell ? '¥' + yuan(m.per_call_sell) : '—' }}</td>
                      <td class="num">{{ m.per_call_cost ? '¥' + yuan(m.per_call_cost) : '—' }}</td>
                      <td class="num">{{ m.in_sell_rate10 ? '¥' + rate10(m.in_sell_rate10) : '—' }}</td>
                      <td class="num">{{ m.cache_sell_rate10 ? '¥' + rate10(m.cache_sell_rate10) : '—' }}</td>
                      <td class="num">{{ m.out_sell_rate10 ? '¥' + rate10(m.out_sell_rate10) : '—' }}</td>
                      <td>
                        <div class="ops-cell">
                          <button class="btn xs" @click="openModelEdit(l.id, m)">编辑</button>
                          <button class="btn xs" :disabled="degradedBusy === m.site_id" @click="doModelDegraded(l.id, m.site_id, !m.degraded)">{{ m.degraded ? '恢复' : '降级' }}</button>
                          <button class="btn xs danger" @click="doModelDelete(l.id, m.site_id)">删除</button>
                        </div>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </template>
          </div>

          <!-- ▼ Codex 账号池运维（账号用量/存活/利润 + 代理健康/换线）▼ -->
          <div v-if="codexData && linesItems.some(l => l.id === 'codex')" class="card">
            <b><AqIcon name="gauge" :size="16" /> Codex 账号池运维</b>
            <p v-if="codexMsg" class="msg" :class="codexMsg.includes('已切换') ? 'ok' : 'bad'" style="margin: 8px 0 0;">{{ codexMsg }}</p>
            <div class="kv mt12">
              <span>出口探测（{{ Math.round((codexData.proxy?.interval_sec || 300) / 60) }} 分钟轮询）</span>
              <b>
                <span v-if="!codexData.proxy?.enabled" class="tag">未启用</span>
                <span v-else-if="codexData.proxy?.probe_ok" class="tag ok">正常</span>
                <span v-else class="tag bad">异常{{ codexData.proxy?.fail_streak ? ' ×' + codexData.proxy?.fail_streak : '' }}</span>
                <span v-if="codexData.proxy?.last_probe_ts" class="dim" style="margin-left: 6px;">{{ codexAgo(codexData.proxy.last_probe_ts) }}</span>
              </b>
              <span>当前出口</span>
              <b><code class="code-inline">{{ codexData.proxy?.selector_now || '—' }}</code>
                <span v-if="codexData.proxy?.urltest_now" class="dim">（自动选中 {{ codexData.proxy.urltest_now }}）</span></b>
              <span v-if="codexData.proxy?.last_err">最近异常</span>
              <b v-if="codexData.proxy?.last_err" class="neg">{{ codexData.proxy.last_err }}</b>
            </div>
            <div class="row wrap mt12" style="gap: 6px;">
              <button v-for="n in codexData.proxy?.nodes ? Object.keys(codexData.proxy.nodes) : []" :key="n"
                class="btn xs" :disabled="codexBusy" @click="doCodexSwitch(n)"
                :title="codexData.proxy.nodes[n]?.last_err || ('切换到 ' + n)">
                {{ n }} {{ codexData.proxy.nodes[n]?.ms ? codexData.proxy.nodes[n].ms + 'ms' : '' }}
              </button>
              <button class="btn xs" :disabled="codexBusy" @click="doCodexSwitch('auto-us')" title="切回自动测优（urltest 按延迟选最快节点）">自动测优</button>
              <button class="btn xs" :disabled="codexBusy" @click="loadCodex()"><AqIcon name="refresh" :size="12" /> 刷新</button>
            </div>
            <p v-if="codexData.proxy?.switches?.length" class="dim" style="margin: 8px 0 0;">换线记录：{{ codexData.proxy.switches.slice().reverse().join('；') }}</p>

            <div class="tbl-wrap mt12">
              <table class="table">
                <thead><tr><th style="width: 48px;">#</th><th>账号</th><th>状态</th><th class="num">官方余量</th><th class="num">本地已用/额度</th><th class="num">请求(成/总)</th>
                  <th class="num">收入（¥）</th><th class="num">成本（¥）</th><th class="num">利润（¥）</th><th class="num">最近成功</th></tr></thead>
                <tbody>
                  <tr v-if="!codexData.accounts?.length"><td colspan="10" class="empty">暂无账号记录</td></tr>
                  <tr v-for="acc in codexData.accounts" :key="acc.idx">
                    <td class="num">{{ acc.idx }}</td>
                    <td>{{ acc.note || ('账号#' + acc.idx) }}
                      <span v-if="acc.plan_type" class="tag" :class="acc.plan_type === 'free' ? '' : 'ok'" title="官方套餐（周限号会显示非 free 高额窗口）" style="margin-left: 4px;">{{ acc.plan_type }}</span>
                    </td>
                    <td>
                      <span class="tag" :class="acc.dead ? 'bad' : 'ok'">{{ acc.dead ? '已判死' : '存活' }}</span>
                      <span v-if="!acc.dead && acc.official_used_pct >= 99" class="tag bad" title="官方用量已达上限" style="margin-left: 4px;">额度耗尽</span>
                    </td>
                    <td class="num" :title="'官方实时口径，窗口重置 ' + (acc.official_reset_at ? new Date(acc.official_reset_at * 1000).toLocaleString() : '—')">
                      <template v-if="acc.official_used_pct >= 0"><b :class="acc.official_used_pct >= 90 ? 'neg' : ''">{{ (100 - acc.official_used_pct).toFixed(1) }}%</b><span class="dim" style="font-size: 11px;"> · {{ codexAgo(acc.official_reset_at) }}重置</span></template>
                      <template v-else><span class="dim">—（待首次调用）</span></template>
                    </td>
                    <td class="num">
                      <div class="bar-track" style="width: 110px; height: 7px;"><span :style="{ width: Math.min(100, (1 - (acc.remain_ratio || 0)) * 100) + '%' }"></span></div>
                      <span class="dim" style="font-size: 11.5px;">{{ codexFmtM(acc.total_tokens) }} / {{ codexFmtM(acc.quota_tokens) }}</span>
                    </td>
                    <td class="num">{{ acc.ok_calls }}/{{ acc.calls }}</td>
                    <td class="num">¥{{ yuan(acc.income_micro) }}</td>
                    <td class="num">¥{{ yuan(acc.cost_micro) }}</td>
                    <td class="num" :class="acc.profit_micro >= 0 ? 'pos' : 'neg'">¥{{ yuan(acc.profit_micro) }}</td>
                    <td class="num">{{ codexAgo(acc.last_ok_ts) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p class="dim" style="margin: 8px 0 0;">额度按号商口径 8M/号记账（剩余比例=1-已用/额度）；利润=计费收入-成本台账；探测 401=穿透风控健康，403 地域拦截/连接失败自动换线（selector 依次试其他节点，恢复即停）。</p>
          </div>
        </div>

        <!-- ▼ 上游额度 ▼ -->
        <div v-if="view === 'quota'" style="display: grid; gap: 16px;">
          <p v-if="quotaMsg" class="msg bad" style="margin: 0;">{{ quotaMsg }}</p>
          <div v-if="quota" class="grid2">
            <div class="card">
              <b><AqIcon name="bolt" :size="16" /> 共享资金池（acu / acu2 共用上游钱包）</b>
              <div class="kv mt12">
                <span>总额度</span><b>¥{{ yuan(quota.pool?.total_micro) }}</b>
                <span>已消耗</span><b>¥{{ yuan(quota.pool?.used_micro) }}</b>
                <span>剩余</span><b class="grad-text" style="font-size: 15px;">¥{{ yuan(quota.pool?.remain_micro) }}</b>
                <span>熔断状态</span>
                <b>
                  <span v-if="quota.pool?.circuit_open" class="tag bad">已熔断</span>
                  <span v-else class="tag ok">正常</span>
                  <button v-if="quota.pool?.circuit_open" class="btn xs" style="margin-left: 8px;" @click="resetCircuit">解除熔断</button>
                </b>
              </div>
              <p class="dim" style="margin: 10px 0 0;">上游密钥共用同一个上游钱包，扣费统一从池里按各自上游成本计；池剩余过低自动熔断（防超额欠费），充值/同步后自动解除。</p>
              <div class="tbl-wrap mt12">
                <table class="table">
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
            </div>
            <div class="card">
              <b><AqIcon name="spark" :size="16" /> 给上游充值（进共享池）</b>
              <div class="mt12" style="display: grid; gap: 12px; max-width: 420px;">
                <div class="field"><label>充值金额（元）</label><input v-model="topup.amount" class="input" type="number" min="0" step="0.001" placeholder="如 10" /></div>
                <div class="field"><label>备注（可选）</label><input v-model="topup.note" class="input" maxlength="100" placeholder="如：微信充值" /></div>
                <div class="field"><label>管理密码确认</label><input v-model="topup.pw" class="input" type="password" autocomplete="current-password" /></div>
                <button class="btn primary" :disabled="topping || !topup.amount || !topup.pw" @click="doTopup">{{ topping ? '充值中…' : '确认充值' }}</button>
                <p v-if="topupMsg" class="msg" style="margin: 0;" :class="topupMsg.includes('失败') || topupMsg.includes('错误') ? 'bad' : 'ok'">{{ topupMsg }}</p>
              </div>
            </div>
          </div>
          <div class="grid2">
            <div class="card">
              <b><AqIcon name="refresh" :size="16" /> 同步上游真实剩余</b>
              <div class="mt12" style="display: grid; gap: 12px; max-width: 420px;">
                <div class="field"><label>上游当前剩余（元）</label><input v-model="sync.amount" class="input" type="number" min="0" step="0.001" placeholder="如：上游后台看到的余额" /></div>
                <div class="field"><label>备注（可选）</label><input v-model="sync.note" class="input" maxlength="100" placeholder="如：月度对齐" /></div>
                <div class="field"><label>管理密码确认</label><input v-model="sync.pw" class="input" type="password" autocomplete="current-password" /></div>
                <p class="dim" style="margin: 0;">填上游后台显示的剩余金额：系统按「池已消耗 + 剩余金额」直接对齐总额度（绝对值，非增量，自动消除漂移）。与「给上游充值」区别：充值是加钱，同步是对准真实值。</p>
                <button class="btn primary" :disabled="syncing || !sync.amount || !sync.pw" @click="doSync">{{ syncing ? '同步中…' : '同步真实余额' }}</button>
                <p v-if="syncMsg" class="msg" style="margin: 0;" :class="syncMsg.includes('失败') || syncMsg.includes('错误') ? 'bad' : 'ok'">{{ syncMsg }}</p>
              </div>
            </div>
          </div>
          <div v-if="quota?.topups?.length" class="card">
            <b><AqIcon name="list" :size="16" /> 充值/同步历史（资金口径）</b>
            <div class="tbl-wrap mt12">
              <table class="table">
                <thead><tr><th>时间</th><th>对象</th><th class="num">金额</th><th class="num">池总额度变化</th><th>备注</th></tr></thead>
                <tbody>
                  <tr v-for="(t, i) in quota.topups" :key="t.ts + '-' + i">
                    <td class="dim">{{ fmtTime(t.ts) }}</td>
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
        </div>

        <!-- ▼ 众筹池 ▼ -->
        <div v-else-if="view === 'pool'" style="display: grid; gap: 16px;">
          <div class="kpis">
            <div class="kpi" :style="poolStatus && poolStatus.balance_micro <= 0 ? 'border-color: var(--bad);' : ''">
              <span>池子余额</span><b>¥{{ yuan(poolStatus?.balance_micro) }}</b>
              <span class="trend">{{ poolStatus ? (poolStatus.balance_micro > 0 ? '供血中' : '已熔断 · acu/ 调用被拒') : '加载中…' }}</span>
            </div>
            <div class="kpi"><span>累计充值（净到手）</span><b>¥{{ yuan(poolStatus?.charged_micro) }}</b><span class="trend">用户充值 {{ poolStatus ? poolStatus.consumers : 0 }} 人共用消耗</span></div>
            <div class="kpi"><span>累计消耗（五折口径）</span><b>¥{{ yuan(poolStatus?.used_micro) }}</b><span class="trend">今日消耗 ¥{{ yuan(poolStatus?.today_used_micro) }} · 上游按 3.75 折烧，沉淀 ≈25% 毛利</span></div>
          </div>
          <div class="card accent">
            <b><AqIcon name="plus" :size="16" /> 官方注入（写 seed 流水，二次密码确认）</b>
            <div class="form-grid mt12" style="max-width: 640px;">
              <div class="field"><label>金额（元）</label><input v-model.number="seedAmt" class="input" type="number" min="0.01" max="1000" step="0.01" placeholder="如 10" /></div>
              <div class="field"><label>管理员二次密码</label><input v-model="seedPw" class="input" type="password" placeholder="管理密码" autocomplete="off" /></div>
              <div class="field"><label>&nbsp;</label><button class="btn primary" :disabled="seeding" @click="doSeed">{{ seeding ? '注入中…' : '注入池子' }}</button></div>
            </div>
            <p v-if="poolMsg" class="msg" :class="poolMsgOk ? 'ok' : 'bad'">{{ poolMsg }}</p>
          </div>
          <div class="card">
            <b><AqIcon name="list" :size="16" /> 最近流水（公开账本同源 · 脱敏展示）</b>
            <div class="tbl-wrap mt12">
              <table class="table">
                <thead><tr><th>时间</th><th>类型</th><th>用户</th><th class="num">变动（¥）</th><th class="num">池余（¥）</th></tr></thead>
                <tbody>
                  <tr v-if="!poolFlows.length"><td colspan="5" class="empty">暂无流水</td></tr>
                  <tr v-for="f in poolFlows" :key="f.id">
                    <td class="dim">{{ fmtTime(f.ts) }}</td>
                    <td>
                      <span class="tag" :class="f.type === 'charge' || f.type === 'seed' ? 'acc' : ''">{{ f.type === 'charge' ? '充值' : f.type === 'consume' ? '扣费' : f.type === 'seed' ? '注入' : '调整' }}</span>
                      <span v-if="f.revival" class="tag warn" style="margin-left: 4px;">救场</span>
                    </td>
                    <td>{{ f.user }}</td>
                    <td class="num" :class="f.amount_micro > 0 ? 'pos' : 'dim'">{{ f.amount_micro > 0 ? '+' : '' }}{{ yuan(f.amount_micro) }}</td>
                    <td class="num dim">¥{{ yuan(f.balance_after_micro) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>

        <!-- ▼ 额度监管 ▼ -->
        <div v-else-if="view === 'supervision'" style="display: grid; gap: 16px;">
          <p v-if="supMsg" class="msg bad" style="margin: 0;">{{ supMsg }}</p>

          <!-- 发放台账 -->
          <div class="card">
            <div class="row between wrap">
              <b><AqIcon name="list" :size="16" /> 发放台账（站长发放 / 扣减 / 在线充值）</b>
              <span class="dim">站长发放 ¥{{ yuan(sup?.summary?.admin_topup_micro) }} · 在线充值 ¥{{ yuan(sup?.summary?.epay_topup_micro) }} · 累计扣减 ¥{{ yuan(Math.abs(sup?.summary?.cum_deduct_micro || 0)) }} · 净发放 ¥{{ yuan(sup?.summary?.net_issued_micro) }}</span>
            </div>
            <div class="row wrap mt12">
              <input v-model="supQ" class="input" style="max-width: 380px;" placeholder="搜索发放对象：用户名 / 邮箱 / UID" @keydown.enter="searchSup" />
              <button class="btn primary" @click="searchSup"><AqIcon name="user" :size="14" /> 搜索</button>
            </div>
            <div class="tbl-wrap mt12">
              <table class="table">
                <thead><tr><th>时间</th><th>用户</th><th>类型</th><th class="num">金额</th><th>备注</th></tr></thead>
                <tbody>
                  <tr v-if="!(sup?.ledger?.items?.length)"><td colspan="5" class="empty">{{ loadingSup ? '加载中…' : '暂无发放记录' }}</td></tr>
                  <tr v-for="l in sup?.ledger?.items || []" :key="l.id">
                    <td class="dim">{{ fmtTime(l.ts) }}</td>
                    <td><b>#{{ l.user_id }} {{ l.username }}</b></td>
                    <td><span class="tag" :class="l.type === 'topup' ? 'ok' : 'bad'">{{ l.operator === 'epay' ? '在线充值' : (l.type === 'topup' ? '站长发放' : '站长扣减') }}</span></td>
                    <td class="num" :class="l.amount_micro >= 0 ? 'pos' : 'neg'">{{ l.amount_micro >= 0 ? '+' : '−' }}¥{{ yuan(Math.abs(l.amount_micro)) }}</td>
                    <td>{{ l.note || '—' }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="paged" v-if="supPages > 1">
              <button class="btn sm" :disabled="supPage <= 1" @click="supPage--; loadSupervision()">上一页</button>
              <span class="cur">{{ supPage }} / {{ supPages }} 页 · 共 {{ fmt(sup?.ledger?.total || 0) }} 笔</span>
              <button class="btn sm" :disabled="supPage >= supPages" @click="supPage++; loadSupervision()">下一页</button>
            </div>
          </div>

          <!-- 余额持有 TOP 榜 -->
          <div class="card">
            <b><AqIcon name="trophy" :size="16" /> 余额持有 TOP（当前有余额用户）</b>
            <div class="tbl-wrap mt12">
              <table class="table">
                <thead><tr><th>用户</th><th>邮箱</th><th class="num">当前余额（¥）</th><th class="num">占负债比</th><th class="num">累计发放（¥）</th><th class="num">累计消费（¥）</th><th class="num">计费调用</th></tr></thead>
                <tbody>
                  <tr v-if="!(sup?.top?.length)"><td colspan="7" class="empty">当前没有持有余额的用户</td></tr>
                  <tr v-for="t in sup?.top || []" :key="t.id">
                    <td><b>#{{ t.id }} {{ t.username }}</b></td>
                    <td class="dim">{{ t.email }}</td>
                    <td class="num grad-text" style="font-weight: 700;">¥{{ yuan(t.balance_micro) }}</td>
                    <td class="num">{{ supShare(t.balance_micro) }}</td>
                    <td class="num">¥{{ yuan(t.topup_micro) }}</td>
                    <td class="num">¥{{ yuan(t.spent_micro) }}</td>
                    <td class="num">{{ fmt(t.billed_calls) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>

        <!-- ▼ 审计日志 ▼ -->
        <div v-else-if="view === 'audit'" style="display: grid; gap: 14px;">
          <p v-if="auditMsg" class="msg bad" style="margin: 0;">{{ auditMsg }}</p>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>时间</th><th>操作</th><th>对象</th><th>详情</th><th>IP</th><th>哈希链</th></tr></thead>
              <tbody>
                <tr v-if="!auditItems.length"><td colspan="6" class="empty">暂无记录</td></tr>
                <tr v-for="a in auditItems" :key="a.ts + '-' + a.action">
                  <td class="dim">{{ fmtTime(a.ts) }}</td>
                  <td><span class="tag" :class="{ ok: a.action.includes('ok') || a.action.includes('topup'), bad: a.action.includes('fail') }">{{ a.action }}</span></td>
                  <td class="num">{{ a.target_user ? '#' + a.target_user : '—' }}</td>
                  <td class="dt-cell">{{ a.detail || '—' }}</td>
                  <td>{{ a.ip || '—' }}</td>
                  <td>
                    <span class="dim" style="font-family: var(--mono); font-size: 11px;" :title="a.self_hash">{{ a.self_hash?.slice(0, 8) }}…</span>
                    <CopyBtn v-if="a.self_hash" :text="a.self_hash" label="" size="xs" />
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="paged" v-if="auditPages > 1">
            <button class="btn sm" :disabled="auditPage <= 1" @click="auditPage--; loadAudit()">上一页</button>
            <span class="cur">{{ auditPage }} / {{ auditPages }} 页</span>
            <button class="btn sm" :disabled="auditPage >= auditPages" @click="auditPage++; loadAudit()">下一页</button>
          </div>
        </div>

        <!-- ▼ 对账 ▼ -->
        <div v-else-if="view === 'reconcile'" style="display: grid; gap: 16px;">
          <div class="row between wrap">
            <span class="dim">资金四重对账：余额重放 / 计费交叉 / 上游交叉 / 审计哈希链。任一不一致请立即排查。</span>
            <button class="btn primary" :disabled="reconciling" @click="loadReconcile">{{ reconciling ? '对账中…' : '一键对账' }}</button>
          </div>
          <p v-if="reconcileMsg" class="msg bad" style="margin: 0;">{{ reconcileMsg }}</p>
          <div v-if="reconcile" class="kpis">
            <div class="kpi" :style="!reconcile.balance_replay.ok ? 'border-color: var(--bad);' : ''">
              <span>余额重放对账</span>
              <b><span class="tag" :class="reconcile.balance_replay.ok ? 'ok' : 'bad'">{{ reconcile.balance_replay.ok ? '✓ 一致' : '✗ 不一致' }}</span></b>
              <span class="trend">重放全部流水 vs 当前余额（异常用户 {{ reconcile.balance_replay.mismatch_users }} 个）</span>
            </div>
            <div class="kpi" :style="!reconcile.billing_cross.ok ? 'border-color: var(--bad);' : ''">
              <span>计费交叉对账</span>
              <b><span class="tag" :class="reconcile.billing_cross.ok ? 'ok' : 'bad'">{{ reconcile.billing_cross.ok ? '✓ 一致' : '✗ 不一致' }}</span></b>
              <span class="trend">计费明细 {{ fmt(reconcile.billing_cross.billed_requests) }} 条 vs 计费流水 {{ fmt(reconcile.billing_cross.billed_flows) }} 笔</span>
            </div>
            <div class="kpi" :style="!reconcile.upstream_cross.ok ? 'border-color: var(--bad);' : ''">
              <span>上游交叉对账</span>
              <b><span class="tag" :class="reconcile.upstream_cross.ok ? 'ok' : 'bad'">{{ reconcile.upstream_cross.ok ? '✓ 一致' : '✗ 不一致' }}</span></b>
              <span class="trend">计费明细 {{ fmt(reconcile.upstream_cross.billed_requests) }} 条 vs 上游计数 {{ fmt(reconcile.upstream_cross.used_calls) }} 次</span>
            </div>
            <div class="kpi" :style="!reconcile.audit_chain.ok ? 'border-color: var(--bad);' : ''">
              <span>审计哈希链</span>
              <b><span class="tag" :class="reconcile.audit_chain.ok ? 'ok' : 'bad'">{{ reconcile.audit_chain.ok ? '✓ 完整' : '✗ 断裂' }}</span></b>
              <span class="trend">{{ reconcile.audit_chain.ok ? '全部记录连续无篡改' : '第 ' + reconcile.audit_chain.broken_at + ' 条之后链断裂' }}</span>
            </div>
          </div>
        </div>

        <!-- ▼ 系统更新 ▼ -->
        <div v-else-if="view === 'update'" style="display: grid; gap: 16px;">
          <p v-if="updateMsg" class="msg" :class="updateMsgOk ? 'ok' : 'bad'" style="margin: 0;">{{ updateMsg }}</p>
          <div v-if="loadingUpdate" class="row" style="gap: 10px;">
            <div class="skeleton" style="min-height: 16px; width: 16px; border-radius: 50%;"></div>
            <span class="dim">正在检查更新源：gitee 主仓库 + github 镜像双源并发，通常 1-5 秒…</span>
          </div>
          <p v-else-if="!update" class="msg bad" style="margin: 0;">
            无法连接更新源（上方为具体错误）。请检查网络；海外服务器访问 gitee 常被 CDN 拦截，可按《管理后台与自动更新指南》2.4 节配置 proxy 或 api_base。
          </p>
          <p v-else-if="!update.update_enabled" class="msg info" style="margin: 0;">
            在线更新未启用：需在网关配置 <code>[update]</code> 段设置 <code>repo</code> 并开启 <code>enabled = true</code>，详见《管理后台与自动更新指南》。
          </p>
          <div v-if="update" class="card accent">
            <b><AqIcon name="download" :size="16" /> 当前运行版本</b>
            <div class="grad-text" style="font-size: 26px; font-weight: 800; margin-top: 6px;">{{ update.current_version }}</div>
            <div class="dim" v-if="update.repo || update.mirror_repo">
              gitee {{ update.repo }}（{{ update.sources?.gitee?.ok ? '可达' : '被拦' }}）
              · github 镜像 {{ update.mirror_repo }}（{{ update.sources?.github?.ok ? '可达' : '不可达' }}）
            </div>
            <div class="dim">附件 {{ update.asset_name }} · 发版方式：构建提交进仓库 assets/ 目录并打 tag push（gitee 自动同步 github）</div>
            <div class="row wrap mt12">
              <button class="btn sm" :disabled="loadingUpdate" @click="loadUpdate"><AqIcon name="refresh" :size="13" /> 重新检查</button>
              <span class="dim">刚发完版请等几分钟，github 镜像同步 tag 有延迟</span>
            </div>
          </div>
          <div v-for="rel in update?.releases || []" :key="rel.tag" class="card">
            <div class="row between wrap">
              <div class="row wrap" style="gap: 8px;">
                <b style="font-size: 14.5px;">{{ rel.tag }}</b>
                <span v-if="rel.is_current" class="tag ok">当前版本</span>
                <span v-else-if="rel.downloadable" class="tag acc">可更新</span>
                <span v-else class="tag warn">无附件</span>
              </div>
              <span class="dim">{{ rel.published_at }}<template v-if="rel.asset_size"> · 附件 {{ (rel.asset_size / 1048576).toFixed(1) }} MB</template></span>
            </div>
            <div v-if="rel.name && rel.name !== rel.tag" class="dim mt8">{{ rel.name }}</div>
            <ul v-if="fmtNotes(rel.notes).length" style="margin: 8px 0 0; padding-left: 18px; font-size: 12.5px; color: var(--txt2); line-height: 1.8;">
              <li v-for="(n, i) in fmtNotes(rel.notes)" :key="i">{{ n }}</li>
            </ul>
            <div class="mt12">
              <button v-if="rel.downloadable && !rel.is_current" class="btn sm primary" @click="askUpdate(rel.tag)">更新到此版本</button>
            </div>
          </div>
        </div>

        <!-- ▼ 站点设置 ▼ -->
        <div v-else-if="view === 'settings'" style="display: grid; gap: 14px;">
          <div class="banner">
            <AqIcon name="info" :size="14" />
            站点配置（数据库外置 · 保存即生效）：留空 = 回退配置文件默认值。费率与公告改动即时下发到全站前端，无需重启网关。
          </div>
          <div class="card">
            <div class="form-grid">
              <div v-for="f in SETTINGS_FIELDS" :key="f.key" class="field">
                <label>{{ f.label }}<span v-if="f.hint" class="dim" style="font-weight: 400;"> · {{ f.hint }}</span></label>
                <input class="input" :placeholder="f.ph" v-model.trim="settingsForm[f.key]" :disabled="settingsBusy" />
              </div>
            </div>
            <div class="row wrap mt16">
              <button class="btn primary" :disabled="settingsBusy" @click="saveSettings">{{ settingsBusy ? '保存中…' : '保存站点配置' }}</button>
              <span v-if="settingsMsg" :class="settingsMsg.includes('失败') ? 'neg' : 'dim'">{{ settingsMsg }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- ============ 弹层区（全屏遮罩 + 卡片） ============ -->

      <!-- 更新确认弹窗 -->
      <div v-if="applyTag" class="mask" @click.self="applyTag = ''">
        <div class="card pop">
          <h3 style="margin: 0;">更新系统到 {{ applyTag }}</h3>
          <p class="dim" style="margin: 0;">将自动下载发行版附件 → 校验 → 备份当前程序 → 替换 → 重启服务（约 3-10 秒中断）。更新前会自动备份，可回滚。</p>
          <div class="field"><label>管理密码确认</label><input v-model="applyPw" class="input" type="password" autocomplete="current-password" @keydown.enter="doUpdate" /></div>
          <p v-if="applyMsg" class="msg bad" style="margin: 0;">{{ applyMsg }}</p>
          <div class="pop-ops">
            <button class="btn" @click="applyTag = ''">取消</button>
            <button class="btn primary" :disabled="applying || !applyPw" @click="doUpdate">{{ applying ? '下载更新中…' : '确认更新' }}</button>
          </div>
        </div>
      </div>

      <!-- 批余额弹窗 -->
      <div v-if="adjust.open" class="mask" @click.self="adjust.open = false">
        <div class="card pop">
          <h3 style="margin: 0;">{{ adjust.sign > 0 ? '给用户发放余额' : '扣减用户余额' }}</h3>
          <div class="dim">#{{ adjust.uid }} {{ adjust.name }}</div>
          <div class="field"><label>金额（元）</label><input v-model="adjust.amount" class="input" type="number" min="0" step="0.001" :placeholder="adjust.sign > 0 ? '如 10' : '如 2'" /></div>
          <div class="field"><label>备注（必填，审计留痕）</label><input v-model="adjust.note" class="input" maxlength="100" placeholder="如：活动赠送 / 误发追回" /></div>
          <div class="field"><label>管理密码确认</label><input v-model="adjust.pw" class="input" type="password" autocomplete="current-password" /></div>
          <p v-if="adjust.sign < 0" class="dim" style="margin: 0;">减余额封底 0，不允许扣成负数。</p>
          <p v-if="adjustMsg" class="msg bad" style="margin: 0;">{{ adjustMsg }}</p>
          <div class="pop-ops">
            <button class="btn" @click="adjust.open = false">取消</button>
            <button class="btn primary" :disabled="adjusting || !adjust.amount || !adjust.pw" @click="doAdjust">{{ adjusting ? '执行中…' : (adjust.sign > 0 ? '确认发放' : '确认扣减') }}</button>
          </div>
        </div>
      </div>

      <!-- 用户详情抽屉 -->
      <template v-if="detail?.open">
        <div class="drawer-mask" @click="detail!.open = false"></div>
        <aside class="drawer">
          <div class="row between">
            <h3 style="margin: 0;">#{{ detail.uid }} {{ detail.name }} · 账目详情</h3>
            <button class="btn xs" @click="detail!.open = false">关闭</button>
          </div>
          <p v-if="detailMsg" class="msg bad" style="margin: 0;">{{ detailMsg }}</p>
          <div v-if="detail.user" class="kv">
            <span>邮箱</span><b>{{ detail.user.email }}</b>
            <span>当前余额</span><b class="pos">¥{{ yuan(detail.user.balance_micro) }}</b>
            <span>累计发放</span><b>¥{{ yuan(detail.summary.topup_micro) }}</b>
            <span>累计消费</span><b>¥{{ yuan(detail.summary.cost_micro) }}</b>
            <span>累计退回</span><b>¥{{ yuan(detail.summary.refund_micro) }}</b>
            <span>计费调用</span><b>{{ fmt(detail.summary.calls) }} 次</b>
          </div>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>时间</th><th>类型</th><th class="num">金额</th><th class="num">余额</th><th>模型</th><th>备注</th></tr></thead>
              <tbody>
                <tr v-if="!detail.flows.length"><td colspan="6" class="empty">暂无流水</td></tr>
                <tr v-for="f in detail.flows" :key="f.ts + '-' + f.type">
                  <td class="dim">{{ fmtTime(f.ts) }}</td>
                  <td><span class="tag">{{ flowTypeLabel[f.type] || f.type }}</span></td>
                  <td class="num" :class="{ pos: f.amount_micro > 0, neg: f.amount_micro < 0 }">{{ f.amount_micro > 0 ? '+' : '' }}¥{{ yuan(Math.abs(f.amount_micro)) }}</td>
                  <td class="num">¥{{ yuan(f.balance_after_micro) }}</td>
                  <td>{{ f.model || '—' }}</td>
                  <td>{{ f.note || '—' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="paged" v-if="Math.ceil(detail.total / 20) > 1">
            <button class="btn sm" :disabled="detail.page <= 1" @click="detailPage(detail.page - 1)">上一页</button>
            <span class="cur">{{ detail.page }} / {{ Math.ceil(detail.total / 20) }} 页</span>
            <button class="btn sm" :disabled="detail.page >= Math.ceil(detail.total / 20)" @click="detailPage(detail.page + 1)">下一页</button>
          </div>
        </aside>
      </template>

      <!-- 新增线路弹窗 -->
      <div v-if="lineNew.open" class="mask" @click.self="lineNew.open = false">
        <div class="card pop">
          <h3 style="margin: 0;">新增上游线路</h3>
          <div class="field"><label>线路 ID（小写字母/数字/连字符，创建后不可改）</label><input v-model="lineNew.id" class="input" placeholder="如 nova" /></div>
          <div class="field"><label>名称</label><input v-model="lineNew.name" class="input" placeholder="如 Nova 免费线" /></div>
          <div class="field"><label>计费模式</label>
            <select v-model="lineNew.mode" class="select">
              <option value="per_token">按量（三段价）</option>
              <option value="per_call">按次</option>
              <option value="free">免费</option>
            </select>
          </div>
          <div class="field"><label>上游 BaseURL</label><input v-model="lineNew.base_url" class="input" placeholder="https://api.example.com" /></div>
          <div class="grid2">
            <div class="field"><label>VIP 分子</label><input v-model="lineNew.vip_num" class="input" type="number" min="0" /></div>
            <div class="field"><label>VIP 分母</label><input v-model="lineNew.vip_den" class="input" type="number" min="0" /></div>
          </div>
          <div class="field"><label>密钥面值（元/把，按量线适用，可留 0）</label><input v-model="lineNew.key_face" class="input" type="number" min="0" step="0.001" /></div>
          <div class="field"><label>上游密钥（逗号 / 换行分隔，自动去重）</label><textarea v-model="lineNew.keys" class="textarea" rows="3" placeholder="sk-xxx&#10;sk-yyy"></textarea></div>
          <div class="field"><label>管理密码确认</label><input v-model="lineNew.pw" class="input" type="password" autocomplete="current-password" /></div>
          <p v-if="lineNewMsg" class="msg bad" style="margin: 0;">{{ lineNewMsg }}</p>
          <div class="pop-ops">
            <button class="btn" @click="lineNew.open = false">取消</button>
            <button class="btn primary" :disabled="lineCreating || !lineNew.id || !lineNew.pw" @click="doLineCreate">{{ lineCreating ? '创建中…' : '创建线路' }}</button>
          </div>
        </div>
      </div>

      <!-- 模型映射弹窗 -->
      <div v-if="modelEdit.open" class="mask" @click.self="modelEdit.open = false">
        <div class="card pop">
          <h3 style="margin: 0;">{{ modelEdit.line }} / 模型映射</h3>
          <p class="dim" style="margin: 0;">站点模型 = 用户调用的 model（自动加线路前缀）；费率单位元/百万tokens（万分率÷10）。保存后自动播种 normal+vip 两组价目（不覆盖已调价）。</p>
          <div class="field"><label>站点模型 ID</label><input v-model="modelEdit.site_id" class="input" placeholder="如 gpt-4o-mini" /></div>
          <div class="field"><label>上游模型 ID（留空 = 同站点 ID）</label>
            <div class="row" style="gap: 8px;">
              <input v-model="modelEdit.upstream_id" class="input" placeholder="vendor/gpt-4o-mini" />
              <button class="btn sm" type="button" @click="openUpModels(modelEdit.line)"><AqIcon name="refresh" :size="12" /> 拉取</button>
            </div>
          </div>
          <div class="grid2">
            <div class="field"><label>售单价（微元/次，按次线）</label><input v-model="modelEdit.per_call_sell" class="input" type="number" min="0" placeholder="3000 = ¥0.003/次" /></div>
            <div class="field"><label>成单价（微元/次，成本台账）</label><input v-model="modelEdit.per_call_cost" class="input" type="number" min="0" /></div>
          </div>
          <div class="grid2">
            <div class="field"><label>入售率10</label><input v-model="modelEdit.in_sell" class="input" type="number" min="0" placeholder="600000" /></div>
            <div class="field"><label>入成率10</label><input v-model="modelEdit.in_cost" class="input" type="number" min="0" placeholder="120000" /></div>
          </div>
          <div class="grid2">
            <div class="field"><label>缓存售率10</label><input v-model="modelEdit.cache_sell" class="input" type="number" min="0" /></div>
            <div class="field"><label>缓存成率10</label><input v-model="modelEdit.cache_cost" class="input" type="number" min="0" /></div>
          </div>
          <div class="grid2">
            <div class="field"><label>出售率10</label><input v-model="modelEdit.out_sell" class="input" type="number" min="0" /></div>
            <div class="field"><label>出成率10</label><input v-model="modelEdit.out_cost" class="input" type="number" min="0" /></div>
          </div>
          <label class="row" style="gap: 8px; font-size: 13px;"><input v-model="modelEdit.image" type="checkbox" /> 图像模型（走绘图计费）</label>
          <div class="field"><label>专属密钥池序（按次线分工钥：0 起锁定第 N 把非停用密钥，留空 = 自动粘性池）</label><input v-model="modelEdit.key_idx" class="input" type="number" min="-1" placeholder="-1 = 自动" /></div>
          <div class="field"><label>管理密码确认</label><input v-model="modelEdit.pw" class="input" type="password" autocomplete="current-password" /></div>
          <p v-if="modelEditMsg" class="msg" :class="modelEditMsg.includes('已保存') ? 'ok' : 'bad'" style="margin: 0;">{{ modelEditMsg }}</p>
          <div class="pop-ops">
            <button class="btn" @click="modelEdit.open = false">取消</button>
            <button class="btn primary" :disabled="modelSaving || !modelEdit.site_id || !modelEdit.pw" @click="doModelUpsert">{{ modelSaving ? '保存中…' : '保存映射' }}</button>
          </div>
        </div>
      </div>

      <!-- 上游模型直选弹窗（诊断 D5：拉取上游 /models 一键选 ID） -->
      <div v-if="upModels.open" class="mask" @click.self="upModels.open = false">
        <div class="card pop">
          <h3 style="margin: 0;">{{ upModels.line }} / 上游模型列表</h3>
          <p class="dim" style="margin: 0;">点击任一模型填入"上游模型 ID"；站点 ID 留空时自动取上游 ID 末段。</p>
          <p v-if="upModels.msg" class="msg bad" style="margin: 0;">{{ upModels.msg }}</p>
          <div v-if="upModels.items.length" class="row wrap" style="max-height: 46vh; overflow-y: auto; gap: 6px; align-items: flex-start;">
            <button v-for="mid in upModels.items" :key="mid" class="btn xs" @click="pickUpModel(mid)">{{ mid }}</button>
          </div>
          <div class="pop-ops"><button class="btn" @click="upModels.open = false">关闭</button></div>
        </div>
      </div>

      <!-- 批量加钥弹窗 -->
      <div v-if="keyAdd.line" class="mask" @click.self="keyAdd.line = ''">
        <div class="card pop">
          <h3 style="margin: 0;">{{ keyAdd.line }} / 批量添加上游密钥</h3>
          <p class="dim" style="margin: 0;">密钥明文只进不出：添加后仅脱敏展示，可随时停用/删除。重复密钥自动跳过。</p>
          <div class="field"><label>密钥列表（逗号 / 换行 / 空白分隔）</label><textarea v-model="keyAdd.raw" class="textarea" rows="5" placeholder="sk-xxx&#10;sk-yyy, sk-zzz"></textarea></div>
          <div class="field"><label>管理密码确认</label><input v-model="keyAdd.pw" class="input" type="password" autocomplete="current-password" /></div>
          <p v-if="keyAddMsg" class="msg" :class="keyAddMsg.includes('已添加') ? 'ok' : 'bad'" style="margin: 0;">{{ keyAddMsg }}</p>
          <div class="pop-ops">
            <button class="btn" @click="keyAdd.line = ''">取消</button>
            <button class="btn primary" :disabled="keyAdding || !keyAdd.raw.trim() || !keyAdd.pw" @click="doKeysAdd">{{ keyAdding ? '添加中…' : '确认添加' }}</button>
          </div>
        </div>
      </div>

      <!-- 余额校准弹窗 -->
      <div v-if="keyCal.line" class="mask" @click.self="keyCal.line = ''">
        <div class="card pop">
          <h3 style="margin: 0;">{{ keyCal.line }} / 密钥余额校准</h3>
          <p class="dim" style="margin: 0;">上游计费与本地台账口径不同，以上游真实余额为唯一真源：可点「一键查询并校准」由后端代查（推荐），或把批量查询结果粘贴到下面手动校准——余额为 0 的密钥自动判死摘除，其余按真实余额重置台账。</p>
          <div class="field"><label>查询结果（每行一条：sk_xxx -&gt; 51.88 CNY）</label><textarea v-model="keyCal.text" class="textarea" rows="7" placeholder="sk_tr_xxx -> 51.88 CNY&#10;sk_tr_yyy -> 0.00 CNY"></textarea></div>
          <div class="field"><label>管理密码确认</label><input v-model="keyCal.pw" class="input" type="password" autocomplete="current-password" /></div>
          <p v-if="keyCalMsg" class="msg" :class="keyCalOk ? 'ok' : 'bad'" style="margin: 0;">{{ keyCalMsg }}</p>
          <div class="pop-ops">
            <button class="btn" @click="keyCal.line = ''">取消</button>
            <button class="btn primary" :disabled="keyCalBusy || !keyCal.text.trim() || !keyCal.pw" @click="doKeysCal">{{ keyCalBusy ? '校准中…' : '开始校准' }}</button>
          </div>
        </div>
      </div>

      <!-- 用户管理弹窗 -->
      <div v-if="umgr.open" class="mask" @click.self="umgr.open = false">
        <div class="card pop">
          <h3 style="margin: 0;">#{{ umgr.uid }} {{ umgr.name }} · 用户管理</h3>
          <p class="dim" style="margin: 0;">{{ umgr.email }} ·
            <span v-if="umgr.status === 99" class="neg">已注销（不可操作）</span>
            <span v-else-if="umgr.status === 0" class="neg">封禁中</span>
            <span v-else class="pos">正常</span>
          </p>
          <div class="field"><label>管理密码（本弹窗所有操作共用）</label><input v-model="umgr.pw" class="input" type="password" autocomplete="current-password" /></div>
          <div class="row wrap" style="gap: 8px;">
            <button class="btn xs danger" :disabled="!!umgr.loading || umgr.status !== 1" @click="doUserBan(true)">封禁</button>
            <button class="btn xs" :disabled="!!umgr.loading || umgr.status !== 0" @click="doUserBan(false)">解封</button>
            <button class="btn xs" :disabled="!!umgr.loading" @click="doUserKick">踢下线</button>
            <button class="btn xs" :disabled="!!umgr.loading" @click="doUserResetPwd">重置密码</button>
          </div>
          <div class="grid2">
            <div class="field"><label>按次价目组</label>
              <select v-model="umgr.grpCall" class="select"><option value="normal">normal</option><option value="vip">vip</option></select>
            </div>
            <div class="field"><label>按量价目组</label>
              <select v-model="umgr.grpToken" class="select"><option value="normal">normal</option><option value="vip">vip</option></select>
            </div>
          </div>
          <div class="row"><button class="btn sm" :disabled="!!umgr.loading" @click="doUserGrp">保存价目组</button></div>
          <p v-if="umgr.newPwd" class="msg ok" style="margin: 0;">新密码（仅本次显示，请立即交付用户）：<code style="font-family: var(--mono);">{{ umgr.newPwd }}</code> <CopyBtn :text="umgr.newPwd" label="复制" size="xs" /></p>
          <p v-if="umgr.msg" class="msg" :class="umgr.msgOk ? 'ok' : 'bad'" style="margin: 0;">{{ umgr.msg }}</p>
          <div class="row wrap" style="gap: 8px; border-top: 1px dashed var(--line); padding-top: 12px;">
            <input v-model="umgr.confirmEmail" class="input" style="flex: 1; min-width: 0; width: auto;" placeholder="输入用户邮箱以确认注销" />
            <button class="btn xs danger" :disabled="!!umgr.loading || umgr.status === 99" @click="doUserDelete">注销账号（软删除）</button>
          </div>
          <p class="dim" style="margin: 0;">注销 = 软删除：账单流水完整保留，邮箱/用户名立即释放可重新注册，全部密钥吊销并强制下线。</p>
          <div class="pop-ops"><button class="btn" @click="umgr.open = false">关闭</button></div>
        </div>
      </div>

      <!-- 用户密钥弹窗 -->
      <div v-if="ukeys.open" class="mask" @click.self="ukeys.open = false">
        <div class="card pop">
          <h3 style="margin: 0;">#{{ ukeys.uid }} {{ ukeys.name }} · API 密钥</h3>
          <p v-if="ukeys.msg" class="msg bad" style="margin: 0;">{{ ukeys.msg }}</p>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th class="num">ID</th><th>前缀</th><th>名称</th><th>分组</th><th>状态</th><th>创建</th><th>操作</th></tr></thead>
              <tbody>
                <tr v-if="!ukeys.items.length"><td colspan="7" class="empty">暂无密钥</td></tr>
                <tr v-for="k in ukeys.items" :key="k.id">
                  <td class="num">{{ k.id }}</td>
                  <td class="dim" style="font-family: var(--mono); font-size: 11.5px;">{{ k.prefix }}…</td>
                  <td>{{ k.name || '—' }}</td>
                  <td><span v-if="k.billing_grp" class="tag">{{ k.billing_grp }}</span><span v-else class="dim">—</span></td>
                  <td><span class="tag" :class="k.revoked ? 'bad' : 'ok'">{{ k.revoked ? '已吊销' : '有效' }}</span></td>
                  <td class="dim">{{ fmtTime(k.created_ts) }}</td>
                  <td><button v-if="!k.revoked" class="btn xs danger" @click="doUserKeyRevoke(k.id)">吊销</button></td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="pop-ops"><button class="btn" @click="ukeys.open = false">关闭</button></div>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
/* ===== 仅布局微调（进度条高度 / 弹层定位 / 抽屉 / 单元格排布），视觉全部走全局类 ===== */
/* 独立入口：无壳渲染，自带页面留白 */
.wrap { padding: 34px 18px 60px; }
/* 登录门 */
.gate { display: flex; align-items: center; justify-content: center; min-height: 62vh; }
.gate-card { width: 360px; max-width: 100%; display: flex; flex-direction: column; gap: 12px; text-align: center; padding: 30px 28px; }
.gate-logo { width: 54px; height: 54px; margin: 0 auto; border-radius: 16px; display: flex; align-items: center; justify-content: center; background: var(--acc-soft); color: var(--acc); }
/* 进度条（渐变填充，宽度驱动） */
.bar-track { height: 10px; border-radius: 5px; background: var(--bg3); overflow: hidden; }
.bar-track > span { display: block; height: 100%; border-radius: 5px; background: var(--acc-grad); transition: width .5s var(--ease); }
/* 近 8 日收入条形图 */
.chart { display: flex; align-items: stretch; gap: 8px; height: 160px; }
.bar-col { flex: 1; min-width: 0; display: flex; flex-direction: column; align-items: center; justify-content: flex-end; gap: 6px; }
.bar { width: 100%; max-width: 40px; border-radius: 6px 6px 2px 2px; background: var(--acc-grad); min-height: 2px; transition: height .4s var(--ease); }
.bar-d { font-size: 10.5px; color: var(--txt3); white-space: nowrap; }
/* 弹层 */
.mask { position: fixed; inset: 0; background: color-mix(in srgb, var(--bg0) 62%, transparent); backdrop-filter: blur(3px); z-index: 200; display: flex; align-items: center; justify-content: center; padding: 20px; }
.pop { width: 430px; max-width: 100%; max-height: 88vh; overflow-y: auto; background: var(--bg2-solid); box-shadow: var(--shadow-2); display: flex; flex-direction: column; gap: 12px; }
.pop-ops { display: flex; gap: 10px; justify-content: flex-end; margin-top: 4px; }
/* 用户详情抽屉 */
.drawer-mask { position: fixed; inset: 0; background: color-mix(in srgb, var(--bg0) 55%, transparent); z-index: 210; }
.drawer { position: fixed; top: 0; right: 0; bottom: 0; width: min(700px, 100vw); z-index: 211; background: var(--bg2-solid); box-shadow: var(--shadow-2); overflow-y: auto; padding: 24px; display: flex; flex-direction: column; gap: 14px; animation: drawer-in .25s var(--ease); }
@keyframes drawer-in { from { transform: translateX(40px); opacity: 0; } to { transform: none; opacity: 1; } }
/* 通用小布局 */
.ops-cell { display: flex; gap: 6px; white-space: nowrap; }
.kv { display: grid; grid-template-columns: auto 1fr; gap: 8px 16px; font-size: 13px; align-items: center; }
.kv > span { color: var(--txt2); }
.kv > b { font-family: var(--mono); font-weight: 600; word-break: break-all; }
.code-inline { font-family: var(--mono); font-size: 11px; color: var(--txt2); background: var(--bg3); border: 1px solid var(--line); border-radius: 6px; padding: 2px 7px; }
.th-in { width: 56px; text-align: center; padding: 4px 6px; font-size: 12px; }
.dt-cell { max-width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pos { color: var(--ok); }
.neg { color: var(--bad); }
@media (max-width: 640px) {
  .kv { grid-template-columns: 1fr; gap: 2px 0; }
  .kv > span { font-size: 11px; }
}
</style>
