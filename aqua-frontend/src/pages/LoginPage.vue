<script setup lang="ts">
/* 登录 / 注册 / 忘记密码一体页：邮箱验证码注册 + 邮箱密码登录 + 邮箱验证码找回密码 */
import { computed, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AqIcon from '@/components/AqIcon.vue'
import { errText } from '@/composables/useApi'
import { forgotPassword, login, register, resetPassword, sendCode } from '@/composables/useAuth'

const router = useRouter()
const route = useRoute()
type Mode = 'login' | 'register' | 'forgot'
// 邀请链接 /login?mode=register&code=XXX 直达注册 tab
const mode = ref<Mode>(route.query.mode === 'register' ? 'register' : route.query.mode === 'forgot' ? 'forgot' : 'login')

/* ===== 表单状态 ===== */
const email = ref('')
const code = ref('')
const username = ref('')
const password = ref('')
const inviteCode = ref(String(route.query.code || '').trim().toUpperCase())
const busy = ref(false)
const msg = ref('')
const msgOk = ref(false)

function show(text: string, ok = false) { msg.value = text; msgOk.value = ok }
function setMode(m: Mode) { mode.value = m; msg.value = '' }

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
    const j = await register(email.value.trim().toLowerCase(), code.value.trim(), username.value.trim(), password.value, inviteCode.value || undefined)
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
    // 重置成功后全端会话已注销 → 回到登录表单并清空密码
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
  <div class="wrap">
    <div class="fade-up">
      <div class="login-shell">
        <div class="card accent login-card">
          <div class="row">
            <span class="brand-ic"><AqIcon name="wave" :size="22" /></span>
            <div>
              <h1 class="login-title">AQUA api 账号</h1>
              <p class="sub">注册登录后即可创建 API 密钥、查看用量与调用日志</p>
            </div>
          </div>

          <!-- 登录 / 注册 tab -->
          <div class="chips mt16">
            <button type="button" class="chip" :class="{ on: mode === 'login' }" @click="setMode('login')"><AqIcon name="user" :size="13" /> 登录</button>
            <button type="button" class="chip" :class="{ on: mode === 'register' }" @click="setMode('register')"><AqIcon name="plus" :size="13" /> 注册</button>
          </div>

          <!-- 登录 -->
          <form v-if="mode === 'login'" class="form mt16" @submit.prevent="doLogin">
            <div class="field">
              <label>邮箱或用户名</label>
              <input v-model="email" class="input" type="text" autocomplete="username" placeholder="邮箱或用户名" required />
            </div>
            <div class="field">
              <label>密码</label>
              <input v-model="password" class="input" type="password" autocomplete="current-password" placeholder="密码" required />
            </div>
            <button class="btn primary block" type="submit" :disabled="busy || !email || !password">
              <AqIcon name="key" :size="14" /> {{ busy ? '登录中…' : '登录' }}
            </button>
            <button type="button" class="btn ghost block" @click="goForgot">忘记密码？</button>
          </form>

          <!-- 忘记密码：发重置码 → 验证码 + 新密码 -->
          <form v-else-if="mode === 'forgot'" class="form mt16" @submit.prevent="doResetPassword">
            <div class="banner"><AqIcon name="shield" :size="14" /> 找回密码：验证码将发送到注册邮箱</div>
            <div class="field">
              <label>注册邮箱</label>
              <div class="row">
                <input v-model="email" class="input grow" type="email" autocomplete="email" placeholder="you@example.com" required />
                <button class="btn code-btn" type="button" :disabled="busy || !emailOk || countdown > 0" @click="doSendResetCode">
                  {{ countdown > 0 ? countdown + 's' : '发送验证码' }}
                </button>
              </div>
            </div>
            <div class="field">
              <label>验证码</label>
              <input v-model="resetCode" class="input" inputmode="numeric" maxlength="6" placeholder="6 位数字验证码" required />
            </div>
            <div class="field">
              <label>新密码</label>
              <input v-model="newPassword" class="input" type="password" autocomplete="new-password" minlength="8" maxlength="72" placeholder="8~72 位，需包含字母和数字" required />
            </div>
            <button class="btn primary block" type="submit" :disabled="busy || !email || !resetCode || !newPassword">
              {{ busy ? '重置中…' : '重置密码' }}
            </button>
            <button type="button" class="btn ghost block" @click="setMode('login')">返回登录</button>
          </form>

          <!-- 注册 -->
          <form v-else class="form mt16" @submit.prevent="doRegister">
            <div class="field">
              <label>邮箱</label>
              <div class="row">
                <input v-model="email" class="input grow" type="email" autocomplete="email" placeholder="you@example.com" required />
                <button class="btn code-btn" type="button" :disabled="busy || !emailOk || countdown > 0" @click="doSendCode">
                  {{ countdown > 0 ? countdown + 's' : '发送验证码' }}
                </button>
              </div>
            </div>
            <div class="field">
              <label>验证码</label>
              <input v-model="code" class="input" inputmode="numeric" maxlength="6" placeholder="6 位数字验证码" required />
            </div>
            <div class="field">
              <label>用户名</label>
              <input v-model="username" class="input" maxlength="20" placeholder="2~20 位，支持中文 / 字母 / 数字 / 下划线" required />
            </div>
            <div class="field">
              <label>密码</label>
              <input v-model="password" class="input" type="password" autocomplete="new-password" minlength="8" maxlength="72" placeholder="8~72 位，需包含字母和数字" required />
            </div>
            <div class="field">
              <label>邀请码 <span class="opt">选填</span></label>
              <input v-model="inviteCode" class="input" maxlength="16" placeholder="好友邀请码，填写后双方均有奖励" />
            </div>
            <button class="btn primary block" type="submit" :disabled="busy || !email || !code || !username || !password">
              {{ busy ? '注册中…' : '注册并登录' }}
            </button>
          </form>

          <p v-if="msg" class="msg mt12" :class="msgOk ? 'ok' : 'bad'">{{ msg }}</p>
          <p v-if="msg && !msgOk" class="dim center mt8">
            解决不了？<a href="https://qm.qq.com/q/qoe6XbsVge" target="_blank" rel="noopener">QQ 一群 1103667832</a>
            / <a href="https://qm.qq.com/q/o8QDbza2Ge" target="_blank" rel="noopener">二群 1006740220</a>
            或 <a href="https://pd.qq.com/s/e4ktxw1b8" target="_blank" rel="noopener">QQ 频道</a> 联系我们
          </p>
          <p class="dim center foot">注册即表示同意：仅用于技术学习与交流，勿用于违法违规用途</p>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 布局微调：居中玻璃卡 + 验证码行 + 文案居中 */
.login-shell { display: flex; justify-content: center; padding: 40px 0 72px; }
.login-card { width: 100%; max-width: 420px; padding: 28px; }
.brand-ic {
  width: 44px; height: 44px; flex: none; border-radius: 12px;
  background: var(--acc-soft); color: var(--acc);
  display: flex; align-items: center; justify-content: center;
}
.login-title { font-size: 22px; }
.login-card .sub { color: var(--txt2); font-size: 13px; margin-top: 2px; }
.form { display: flex; flex-direction: column; gap: 14px; }
.grow { flex: 1; min-width: 0; }
.code-btn { white-space: nowrap; padding: 0 14px; font-size: 13px; }
.opt { font-size: 11px; color: var(--txt3); font-weight: normal; border: 1px solid var(--line); border-radius: 6px; padding: 1px 6px; margin-left: 4px; }
.center { text-align: center; }
.foot { margin-top: 16px; font-size: 11.5px; color: var(--txt3); }
</style>
