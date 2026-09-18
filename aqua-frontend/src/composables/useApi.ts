/* 统一 API 封装：网关地址自动探测 + 结构化错误（中文消息）+ 剪贴板 */
export function detectGateway(): string {
  // 前台与网关同级约定：acu.* → api.*（主机名推导，acu.ltzy.top → api.ltzy.top）
  if (location.hostname.startsWith('acu.')) return 'https://api.' + location.hostname.slice(4) + '/v1'
  // ltzy.top 主站（aqua.ltzy.top）：接口域名按惯例固定 api.ltzy.top（20260919 站长指定）
  if (location.hostname.endsWith('.ltzy.top')) return 'https://api.ltzy.top/v1'
  // 局域网部署：前端 8788 / API 8787
  if (location.port === '8788') return `${location.protocol}//${location.hostname}:8787/v1`
  // 其他（本地 dev 由 vite 代理 /v1；自定义域同源）
  return location.origin + '/v1'
}
export const GATEWAY = detectGateway()

export interface AquaError { status: number; code: string; message: string }

/** 会话令牌存取（useAuth 写入，这里循环依赖所以走 localStorage 直读） */
function token(): string {
  try { return localStorage.getItem('aqua_session') || '' } catch { return '' }
}

/** 401 时跳登录页的钩子（router 挂载后由 main.ts 注入） */
export let onUnauthorized: (() => void) | null = null
export function setUnauthorizedHook(fn: () => void) { onUnauthorized = fn }

export async function apiJson<T = any>(
  path: string,
  opts: { method?: string; body?: any; key?: string; session?: boolean; signal?: AbortSignal } = {},
): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (opts.session) {
    // 会话请求：带登录令牌（sess_…）
    const t = token()
    if (t) headers['Authorization'] = 'Bearer ' + t
  } else {
    // 网关数据请求：显式 key > 用户默认密钥（控制台保存）> 不带头（由后端 401 提示登录）
    let k = opts.key || ''
    if (!k) { try { k = localStorage.getItem('aqua_default_key') || '' } catch { /* 忽略 */ } }
    if (k) headers['Authorization'] = 'Bearer ' + k
  }
  const doFetch = () => fetch(GATEWAY + path, {
    method: opts.method || (opts.body !== undefined ? 'POST' : 'GET'),
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    signal: opts.signal,
  })
  // GET 网络抖动自愈：非 5xx/4xx 的 TypeError（断网/超时）静默重试 1 次；非幂等方法不重试
  let res: Response
  try {
    res = await doFetch()
  } catch (err) {
    const isGet = (opts.method || 'GET') === 'GET' && opts.body === undefined
    try {
      if (isGet && !opts.signal) {
        await new Promise(r => setTimeout(r, 600))
        res = await doFetch()
      } else throw err
    } catch (err2) {
      // 网络层失败（浏览器报 "Failed to fetch"）：统一转结构化中文错误
      throw { status: 0, code: 'NETWORK', message: '网络异常，请检查网络后重试（若持续出现，可能是网络波动或服务维护中，稍等片刻即可恢复）' } as AquaError
    }
  }
  let j: any = null
  try { j = await res.json() } catch { /* 非 JSON 响应 */ }
  if (!res.ok) {
    const e = j && j.error ? j.error : {}
    // 会话失效 → 清令牌并跳登录
    if (res.status === 401 && opts.session) {
      try { localStorage.removeItem('aqua_session') } catch { /* 忽略 */ }
      if (onUnauthorized) onUnauthorized()
    }
    throw { status: res.status, code: e.code || 'UNKNOWN', message: e.message || `请求失败（HTTP ${res.status}）` } as AquaError
  }
  return j as T
}

export function errText(e: any): string {
  return e && e.message ? String(e.message) : String(e)
}

/** 剪贴板：优先现代 API，旧内核回退 execCommand */
export async function copyText(text: string): Promise<boolean> {
  try { await navigator.clipboard.writeText(text); return true }
  catch {
    try {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      const ok = document.execCommand('copy')
      ta.remove()
      return ok
    } catch { return false }
  }
}

/** 千分位格式化 */
export function fmt(n: number | string): string {
  const num = typeof n === 'string' ? Number(n) : n
  if (!isFinite(num)) return String(n)
  return num.toLocaleString('en-US')
}
