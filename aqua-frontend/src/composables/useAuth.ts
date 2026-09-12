/* 用户会话管理：localStorage 令牌 + /v1/auth|me 接口封装（全面强制登录版） */
import { ref } from 'vue'
import { apiJson, errText } from './useApi'

const TOKEN_KEY = 'aqua_session'

export interface Me {
  id: number
  username: string
  email: string
  avatar_ext: string
  created_ts: number
  key_count: number
}

/** 全局单例会话状态 */
export const sessionToken = ref(localStorage.getItem(TOKEN_KEY) || '')
export const me = ref<Me | null>(null)

export function setToken(t: string) {
  sessionToken.value = t
  if (t) localStorage.setItem(TOKEN_KEY, t)
  else localStorage.removeItem(TOKEN_KEY)
}

export function isLoggedIn(): boolean {
  return !!sessionToken.value
}

export function avatarUrl(): string {
  if (!me.value || !me.value.avatar_ext) return ''
  return GATEWAY_AVATAR + me.value.id
}
// 头像挂在 API 域（静态文件在 8788 之外）
const GATEWAY_AVATAR = ((): string => {
  // 与 useApi.detectGateway 同规则，但去掉 /v1 尾巴
  if (location.hostname.startsWith('acu.')) return 'https://api.' + location.hostname.slice(4) + '/v1/auth/avatar/'
  if (location.port === '8788') return `${location.protocol}//${location.hostname}:8787/v1/auth/avatar/`
  return location.origin + '/v1/auth/avatar/'
})()

export async function loadMe(): Promise<Me | null> {
  if (!sessionToken.value) { me.value = null; return null }
  try {
    me.value = await apiJson<Me>('/auth/me', { session: true })
    return me.value
  } catch {
    // 令牌失效 → 清掉
    setToken('')
    me.value = null
    return null
  }
}

export async function sendCode(email: string): Promise<void> {
  await apiJson('/auth/send-code', { method: 'POST', body: { email } })
}

export async function register(email: string, code: string, username: string, password: string) {
  const j = await apiJson<any>('/auth/register', { method: 'POST', body: { email, code, username, password } })
  setToken(j.token || '')
  await loadMe()
  return j
}

export async function login(account: string, password: string) {
  const j = await apiJson<any>('/auth/login', { method: 'POST', body: { account, password } })
  setToken(j.token || '')
  await loadMe()
  return j
}

export async function logout() {
  try { await apiJson('/auth/logout', { method: 'POST', session: true, body: {} }) } catch { /* 忽略 */ }
  setToken('')
  me.value = null
}

/* ===== 个人控制台 API ===== */

export interface KeyItem { id: number; prefix: string; name: string; revoked: boolean; created_ts: number; can_reveal?: boolean; billing_grp?: string }

export async function listKeys(): Promise<KeyItem[]> {
  const j = await apiJson<any>('/my/keys', { session: true })
  return j.keys || []
}

/** 计费分组：per_call=免费+按次计费；per_token=免费+按量计费；free=纯免费（仅可调免费模型）；''=旧式未分组 */
export type BillingGrp = '' | 'per_call' | 'per_token' | 'free'

export async function createKey(name: string, billingGrp: BillingGrp = ''): Promise<{ key: string; prefix: string; billing_grp: string }> {
  return apiJson('/my/keys', { method: 'POST', session: true, body: { name, billing_grp: billingGrp } })
}

/** 随时切换密钥计费分组（立即生效，无需重建密钥） */
export async function changeKeyGroup(id: number, billingGrp: BillingGrp): Promise<void> {
  await apiJson(`/my/keys/${id}/group`, { method: 'PATCH', session: true, body: { billing_grp: billingGrp } })
}

/* ===== 财务管理中心 ===== */
export interface FinanceData {
  balance_micro: number
  spend_today: number
  spend_week: number
  spend_total: number
  by_model: { model: string; amount_micro: number; calls: number }[]
  recent: { model: string; amount_micro: number; ok: boolean; ts: number }[]
  topups: { amount_micro: number; status: string; channel: string; created_ts: number; paid_ts: number }[]
}

export async function fetchFinance(): Promise<FinanceData> {
  return apiJson('/my/finance', { session: true })
}

/** 随时查看密钥原文（服务端加密存储回显） */
export async function revealKey(id: number): Promise<string> {
  const j = await apiJson<any>(`/my/keys/${id}/reveal`, { session: true })
  return j.key || ''
}

export async function revokeKey(id: number): Promise<void> {
  await apiJson('/my/keys/' + id, { method: 'DELETE', session: true })
}

/* ===== 账号检查 ===== */
export interface CheckupItem { id: string; ok: boolean; level: 'ok' | 'warn' | 'bad'; title: string; detail: string; advice: string }
export interface Checkup { score: number; items: CheckupItem[] }

export async function fetchCheckup(): Promise<Checkup> {
  const j = await apiJson<any>('/my/checkup', { session: true })
  return { score: j.score || 0, items: j.items || [] }
}

export async function changePassword(oldPw: string, newPw: string): Promise<string> {
  const j = await apiJson<any>('/auth/password', { method: 'POST', session: true, body: { old_password: oldPw, new_password: newPw } })
  return j.message || '密码已修改'
}

/* ===== 余额邮件提醒 ===== */

export interface BalanceAlert { threshold_micro: number; armed: boolean; email: string }

/** 查询当前用户的余额提醒设置 */
export async function fetchBalanceAlert(): Promise<BalanceAlert> {
  return apiJson<BalanceAlert>('/my/balance-alert', { session: true })
}

/** 设置余额提醒阈值（threshold_micro=0 表示关闭） */
export async function setBalanceAlert(thresholdMicro: number): Promise<string> {
  const j = await apiJson<any>('/my/balance-alert', { method: 'POST', session: true, body: { threshold_micro: thresholdMicro } })
  return j.message || '已保存'
}

/** 忘记密码：发送重置验证码（要求邮箱已注册） */
export async function forgotPassword(email: string): Promise<string> {
  const j = await apiJson<any>('/auth/forgot', { method: 'POST', body: { email } })
  return j.message || '重置验证码已发送'
}

/** 忘记密码：验证码 + 新密码重置（成功后全端会话注销） */
export async function resetPassword(email: string, code: string, password: string): Promise<string> {
  const j = await apiJson<any>('/auth/reset', { method: 'POST', body: { email, code, password } })
  return j.message || '密码已重置'
}

export async function uploadAvatar(file: File): Promise<void> {
  const res = await fetch(GATEWAY_AVATAR_BASE + 'my/avatar', {
    method: 'POST',
    headers: { 'Content-Type': file.type || 'image/png', Authorization: 'Bearer ' + sessionToken.value },
    body: file,
  })
  if (!res.ok) {
    let msg = `上传失败（HTTP ${res.status}）`
    try { const j = await res.json(); if (j?.error?.message) msg = String(j.error.message) } catch { /* 忽略 */ }
    throw new Error(msg)
  }
}
const GATEWAY_AVATAR_BASE = ((): string => {
  if (location.hostname.startsWith('acu.')) return 'https://api.' + location.hostname.slice(4) + '/v1/'
  if (location.port === '8788') return `${location.protocol}//${location.hostname}:8787/v1/`
  return location.origin + '/v1/'
})()

export { errText }
