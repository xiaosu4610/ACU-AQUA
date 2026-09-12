<script setup lang="ts">
/* 登录 / 注册 / 忘记密码一体页：邮箱验证码注册 + 邮箱密码登录 + 邮箱验证码找回密码 */
import { computed, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { forgotPassword, loadMe, login, register, resetPassword, sendCode } from '@/composables/useAuth'
import { errText } from '@/composables/useApi'

const router = useRouter()
const route = useRoute()
const mode = ref<'login' | 'register' | 'forgot'>('login')

/* ===== 表单状态 ===== */
const email = ref('')
const code = ref('')
const username = ref('')
const password = ref('')
const busy = ref(false)
const msg = ref('')
const msgOk = ref(false)

function show(text: string, ok = false) { msg.value = text; msgOk.value = ok }

/* ===== 验证码倒计时 ===== */
const countdown = ref(0)
let timer: ReturnType<typeof setInterval> | null = null
function startCountdown() {
  countdown.value = 60
  if (timer) clearInterval(timer)
  timer = setInterval(() => {
    countdown.value--
    if (countdown.value <= 0 && timer) { clearInterval(timer); timer = null }
  }, 1000)
}
onUnmounted(() => { if (timer) clearInterval(timer) })

const emailOk = computed(() => /^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$/.test(email.value.trim()))

async function doSendCode() {
  if (!emailOk.value || countdown.value > 0) return
  busy.value = true; show('')
  try {
    await sendCode(email.value.trim().toLowerCase())
    show('验证码已发送，请查收邮箱（30 分钟内有效，注意垃圾箱；若延迟请耐心等待，勿重复发送）', true)
    startCountdown()
  } catch (e) { show(errText(e)) }
  busy.value = false
}

async function doRegister() {
  busy.value = true; show('')
  try {
    const j = await register(email.value.trim().toLowerCase(), code.value.trim(), username.value.trim(), password.value)
    show('注册成功！默认密钥已生成，请到控制台复制保存', true)
    setTimeout(() => goConsole(), 1200)
    if (j?.api_key) { try { localStorage.setItem('aqua_default_key', j.api_key) } catch { /* 忽略 */ } }
  } catch (e) { show(errText(e)) }
  busy.value = false
}

async function doLogin() {
  busy.value = true; show('')
  try {
    // 后端 username 匹配大小写敏感，email 已在注册时统一小写入库；
    // 这里不能 toLowerCase()，否则含大写字母的用户名永远匹配不上
    await login(email.value.trim(), password.value)
    show('登录成功，正在进入控制台…', true)
    setTimeout(() => goConsole(), 500)
  } catch (e) { show(errText(e)) }
  busy.value = false
}

/* ===== 忘记密码 ===== */
const resetCode = ref('')
const newPassword = ref('')

function goForgot() {
  mode.value = 'forgot'
  show('')
  resetCode.value = ''
  newPassword.value = ''
}

async function doSendResetCode() {
  if (!emailOk.value || countdown.value > 0) return
  busy.value = true; show('')
  try {
    const m = await forgotPassword(email.value.trim().toLowerCase())
    show(m + '（30 分钟内有效，注意垃圾箱）', true)
    startCountdown()
  } catch (e) { show(errText(e)) }
  busy.value = false
}

async function doResetPassword() {
  busy.value = true; show('')
  try {
    const m = await resetPassword(email.value.trim().toLowerCase(), resetCode.value.trim(), newPassword.value)
    show(m + '，请用新密码登录', true)
    // 重置成功后全端会话已注销 → 回到登录表单并预填邮箱
    setTimeout(() => {
      mode.value = 'login'
      password.value = ''
      show('密码已重置，请登录', true)
    }, 1500)
  } catch (e) { show(errText(e)) }
  busy.value = false
}

function goConsole() {
  const redirect = String(route.query.redirect || '/console')
  router.replace(redirect)
}
</script>

<template>
  <section class="route-page">
    <div class="auth-wrap">
      <div class="auth-card">
        <h1 class="auth-title">AQUA 账号</h1>
        <p class="auth-sub">注册登录后即可创建 API 密钥、查看用量与调用日志</p>
        <div class="auth-tabs">
          <button type="button" :class="{ active: mode === 'login' }" @click="mode = 'login'; msg = ''">登录</button>
          <button type="button" :class="{ active: mode === 'register' }" @click="mode = 'register'; msg = ''">注册</button>
        </div>

        <!-- 登录 -->
        <form v-if="mode === 'login'" class="auth-form" @submit.prevent="doLogin">
          <label>邮箱或用户名</label>
          <input v-model="email" type="text" autocomplete="username" placeholder="邮箱或用户名" required />
          <label>密码</label>
          <input v-model="password" type="password" autocomplete="current-password" placeholder="密码" required />
          <button class="btn tool-run auth-btn" type="submit" :disabled="busy || !email || !password">
            {{ busy ? '登录中…' : '登录' }}
          </button>
          <button type="button" class="forgot-link" @click="goForgot">忘记密码？</button>
        </form>

        <!-- 忘记密码：发重置码 → 验证码 + 新密码 -->
        <form v-else-if="mode === 'forgot'" class="auth-form" @submit.prevent="doResetPassword">
          <label>注册邮箱</label>
          <div class="code-row">
            <input v-model="email" type="email" autocomplete="email" placeholder="you@example.com" required />
            <button class="btn tool-run code-btn" type="button" :disabled="busy || !emailOk || countdown > 0" @click="doSendResetCode">
              {{ countdown > 0 ? countdown + 's' : '发送验证码' }}
            </button>
          </div>
          <label>验证码</label>
          <input v-model="resetCode" inputmode="numeric" maxlength="6" placeholder="6 位数字验证码" required />
          <label>新密码</label>
          <input v-model="newPassword" type="password" autocomplete="new-password" minlength="8" maxlength="72" placeholder="8~72 位，需包含字母和数字" required />
          <button class="btn tool-run auth-btn" type="submit" :disabled="busy || !email || !resetCode || !newPassword">
            {{ busy ? '重置中…' : '重置密码' }}
          </button>
          <button type="button" class="forgot-link" @click="mode = 'login'; msg = ''">返回登录</button>
        </form>

        <!-- 注册 -->
        <form v-else class="auth-form" @submit.prevent="doRegister">
          <label>邮箱</label>
          <div class="code-row">
            <input v-model="email" type="email" autocomplete="email" placeholder="you@example.com" required />
            <button class="btn tool-run code-btn" type="button" :disabled="busy || !emailOk || countdown > 0" @click="doSendCode">
              {{ countdown > 0 ? countdown + 's' : '发送验证码' }}
            </button>
          </div>
          <label>验证码</label>
          <input v-model="code" inputmode="numeric" maxlength="6" placeholder="6 位数字验证码" required />
          <label>用户名</label>
          <input v-model="username" maxlength="20" placeholder="2~20 位，支持中文 / 字母 / 数字 / 下划线" required />
          <label>密码</label>
          <input v-model="password" type="password" autocomplete="new-password" minlength="8" maxlength="72" placeholder="8~72 位，需包含字母和数字" required />
          <button class="btn tool-run auth-btn" type="submit" :disabled="busy || !email || !code || !username || !password">
            {{ busy ? '注册中…' : '注册并登录' }}
          </button>
        </form>

        <p v-if="msg" class="auth-msg" :class="{ ok: msgOk }">{{ msg }}</p>
        <p v-if="msg && !msgOk" class="auth-help">
          解决不了？<a href="https://qm.qq.com/q/qoe6XbsVge" target="_blank" rel="noopener">QQ 一群 1103667832</a>
          / <a href="https://qm.qq.com/q/o8QDbza2Ge" target="_blank" rel="noopener">二群 1006740220</a>
          或 <a href="https://pd.qq.com/s/e4ktxw1b8" target="_blank" rel="noopener">QQ 频道</a> 联系我们
        </p>
        <p class="auth-foot">注册即表示同意：仅用于技术学习与交流，勿用于违法违规用途</p>
      </div>
    </div>
  </section>
</template>

<style scoped>
.auth-wrap { display: flex; justify-content: center; padding: 48px 0 64px; }
.auth-card {
  width: 100%; max-width: 420px;
  background: var(--card-bg, rgba(255,255,255,.04));
  border: 1px solid var(--border, rgba(128,140,160,.2));
  border-radius: 16px; padding: 32px 28px;
}
.auth-title { font-size: 22px; font-weight: 800; margin: 0 0 4px; }
.auth-sub { font-size: 13px; color: var(--muted, #8a94a6); margin: 0 0 20px; }
.auth-tabs { display: flex; gap: 8px; margin-bottom: 20px; }
.auth-tabs button {
  flex: 1; padding: 10px 0; border-radius: 10px; border: 1px solid var(--border, rgba(128,140,160,.25));
  background: transparent; color: inherit; font-weight: 600; cursor: pointer; transition: all .15s;
}
.auth-tabs button.active {
  background: var(--btn-grad, linear-gradient(135deg, #0891b2, #0284c7));
  color: #fff; border-color: transparent;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, .28), 0 2px 10px rgba(8, 145, 178, .35);
}
.auth-form { display: flex; flex-direction: column; gap: 6px; }
.auth-form label { font-size: 12.5px; color: var(--muted, #8a94a6); margin-top: 8px; }
.auth-form input {
  padding: 11px 12px; border-radius: 10px; border: 1px solid var(--border, rgba(128,140,160,.3));
  background: transparent; color: inherit; font-size: 14px; outline: none; font-family: inherit;
}
.auth-form input:focus { border-color: var(--accent, #0b6cff); }
.code-row { display: flex; gap: 8px; }
.code-row input { flex: 1; min-width: 0; }
.code-btn { white-space: nowrap; padding: 0 14px; font-size: 13px; }
.auth-btn { margin-top: 16px; width: 100%; padding: 12px 0; font-size: 15px; }
.forgot-link {
  margin-top: 12px; background: none; border: none; cursor: pointer;
  font-size: 12.5px; color: var(--accent, #5eead4); padding: 0; align-self: center;
}
.forgot-link:hover { text-decoration: underline; }
.auth-msg { margin: 14px 0 0; font-size: 13px; color: #f87171; text-align: center; }
.auth-msg.ok { color: #34d399; }
.auth-help { margin: 8px 0 0; font-size: 12px; color: var(--muted, #8a94a6); text-align: center; }
.auth-help a { color: var(--accent, #5eead4); text-decoration: none; }
.auth-help a:hover { text-decoration: underline; }
.auth-foot { margin: 18px 0 0; font-size: 11.5px; color: var(--muted, #8a94a6); text-align: center; }
</style>
