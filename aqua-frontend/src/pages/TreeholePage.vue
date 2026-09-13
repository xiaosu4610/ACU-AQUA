<script setup lang="ts">
/* 树洞（情绪陪伴）：接口对接 1:1 平移自旧版
 * （GET /tools/treehole/prompt 官方提示词 + POST /tools/treehole 专用端点流式 + stripThink 剥离思维链）
 * UI 全新：双角色选择卡（accent 选中边）+ 柔和气泡对话流（淡入）+ 右侧心理热线安全卡 */
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { apiJson, errText, GATEWAY } from '@/composables/useApi'
import { stripThink } from '@/composables/useSSE'
import CopyBtn from '@/components/CopyBtn.vue'
import AqIcon from '@/components/AqIcon.vue'

interface ThMsg { role: 'user' | 'assistant'; text: string; streaming?: boolean; err?: string }
interface ThPromptResp { modes?: { id: string; prompt: string }[] }

const mode = ref<'gentle' | 'anime'>('gentle')
const busy = ref(false)
const msgs = ref<ThMsg[]>([])
const inputText = ref('')
const promptData = ref<ThPromptResp | null>(null)
const inputEl = ref<HTMLTextAreaElement | null>(null)
const chatEl = ref<HTMLElement | null>(null)

let abort: AbortController | null = null
let chatHistory: { role: 'user' | 'assistant'; content: string }[] = []

/* 两种树洞伙伴（文案与旧版一字不差） */
const TH_MODES = {
  gentle: { name: '小溪', welcome: '我是小溪，这里没有评判，没有说教，只有认真听你说话的人。\n今天过得怎么样？慢慢说，我在。' },
  anime: { name: '星璃', welcome: '呐——是星璃哦，今天也辛苦了呢。\n烦恼也好，开心也好，都寄存在我这里吧。想从哪里说起？' },
} as const

/* 官方提示词：优先本次会话已加载数据，否则回读 sessionStorage 缓存 */
function thPromptOf(m: string): string {
  let cache = promptData.value
  if (!cache) {
    try { cache = JSON.parse(sessionStorage.getItem('aqua_th_prompts') || 'null') as ThPromptResp | null } catch { /* 忽略 */ }
  }
  if (cache && cache.modes) {
    for (const x of cache.modes) if (x.id === m) return x.prompt
  }
  return ''
}
const currentPrompt = computed(() => thPromptOf(mode.value))
const promptShow = computed(() => currentPrompt.value
  || '提示词加载失败，请稍后刷新重试（服务端会强制注入官方提示词，即使此处未展示也不影响对话质量）。')

async function loadPrompt() {
  try {
    const j = await apiJson<ThPromptResp>('/tools/treehole/prompt')
    promptData.value = j
    try { sessionStorage.setItem('aqua_th_prompts', JSON.stringify(j)) } catch { /* 忽略 */ }
  } catch { /* 展示兜底文案 */ }
}

function scrollBottom() {
  nextTick(() => { const el = chatEl.value; if (el) el.scrollTop = el.scrollHeight })
}

function autoGrow() {
  const el = inputEl.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 140) + 'px'
}

/* 网关统一错误结构：{error:{message(中文), code, status, hint}} → 中文消息（formatAquaErr 平移） */
function aquaErr(e: { code?: string; message?: string }, status: number): string {
  const code = e && e.code ? String(e.code) : ''
  const msg = (e && e.message) || ('请求失败（HTTP ' + status + '）')
  return (code && !/^\d+$/.test(code) ? '【' + code + '】' : '') + msg
}

function switchMode(m: 'gentle' | 'anime') {
  if (mode.value === m || busy.value) return
  mode.value = m
  chatHistory = []
  msgs.value = []
}

function clearChat() {
  if (busy.value && abort) { try { abort.abort() } catch { /* 忽略 */ } }
  chatHistory = []
  msgs.value = []
}

/* 发送：POST /tools/treehole 流式（服务端注入官方提示词与模型），思维链用 stripThink 剥离。
   注：streamChat 固定请求 /chat/completions，树洞必须走专用端点，故 SSE 解析在本组件内实现（与 streamChat 同构）。 */
async function send() {
  if (busy.value) return
  const text = inputText.value.trim()
  if (!text) return
  inputText.value = ''
  autoGrow()
  chatHistory.push({ role: 'user', content: text })
  msgs.value.push({ role: 'user', text })
  const ai = reactive<ThMsg>({ role: 'assistant', text: '', streaming: true })
  msgs.value.push(ai)
  busy.value = true
  abort = new AbortController()
  scrollBottom()
  let full = ''
  const handleLine = (line: string) => {
    const s = line.trim()
    if (!s.startsWith('data:')) return
    const payload = s.slice(5).trim()
    if (payload === '[DONE]') return
    try {
      const j = JSON.parse(payload) as { choices?: { delta?: { content?: string } }[] }
      const piece = j?.choices?.[0]?.delta?.content || ''
      if (piece) { full += piece; ai.text = stripThink(full); scrollBottom() }
    } catch { /* 半包/心跳，跳过 */ }
  }
  try {
    // 密钥链：用户默认密钥（控制台保存）> 登录会话；都没有则不带头（由后端 401 提示登录）
    let key = ''
    try { key = localStorage.getItem('aqua_default_key') || '' } catch { /* 忽略 */ }
    if (!key) { try { key = localStorage.getItem('aqua_session') || '' } catch { /* 忽略 */ } }
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (key) headers['Authorization'] = 'Bearer ' + key
    const res = await fetch(GATEWAY + '/tools/treehole', {
      method: 'POST',
      headers,
      body: JSON.stringify({ mode: mode.value, stream: true, messages: chatHistory.slice(-20) }),
      signal: abort.signal,
    })
    if (!res.ok || !res.body || typeof res.body.getReader !== 'function') {
      // 非 JSON 错误 / 无 ReadableStream（兼容降级）：整段读取后一次性解析
      const raw = await res.text()
      if (!res.ok) {
        let e: { code?: string; message?: string; hint?: string } = {}
        try { e = (JSON.parse(raw) as { error?: typeof e }).error || {} } catch { /* 非 JSON 响应 */ }
        const err = new Error(aquaErr(e, res.status)) as Error & { hint?: string }
        err.hint = e.hint
        throw err
      }
      raw.split('\n').forEach(handleLine)
    } else {
      const reader = res.body.getReader()
      const dec = new TextDecoder()
      let buf = ''
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += dec.decode(value, { stream: true })
        const lines = buf.split('\n')
        buf = lines.pop() || ''
        for (const line of lines) handleLine(line)
      }
    }
    ai.streaming = false
    const shown = stripThink(full)
    if (shown) { ai.text = shown; chatHistory.push({ role: 'assistant', content: shown }) }
    else ai.err = '树洞小伙伴走神了，再试一次好吗？'
  } catch (e) {
    ai.streaming = false
    const err = e as { name?: string; hint?: string }
    if (err?.name !== 'AbortError') {
      let msg = errText(e) || '网络异常，稍后再试'
      if (err?.hint) msg += '\n' + err.hint
      if (!ai.text) ai.err = msg
      else ai.text += '\n\n[' + msg + ']'
    }
  } finally {
    busy.value = false
    abort = null
    scrollBottom()
  }
}

onMounted(() => {
  loadPrompt()
})
</script>

<template>
  <div class="wrap">
    <div class="page-head">
      <div>
        <h1><AqIcon name="heart" :size="22" />温柔树洞</h1>
        <div class="sub">把心里的话说给树洞听——两位倾听伙伴在线，没有评判也没有说教，对话内容不会被保存。</div>
      </div>
    </div>

    <div class="th-grid fade-up">
      <!-- 左：角色选择 + 对话流 -->
      <div>
        <div class="th-pick">
          <button
            v-for="(cfg, key) in TH_MODES" :key="key"
            class="card hoverable th-role" :class="{ accent: mode === key }"
            type="button" @click="switchMode(key)"
          >
            <span class="th-avatar"><AqIcon :name="key === 'gentle' ? 'wave' : 'spark'" :size="18" /></span>
            <span class="th-role-txt">
              <b>{{ cfg.name }}</b>
              <i>{{ key === 'gentle' ? '温柔树洞 · 深夜电台的倾听伙伴' : '二次元陪伴 · 跨次元的元气伙伴' }}</i>
            </span>
            <AqIcon v-if="mode === key" name="check" :size="16" class="th-check" />
          </button>
        </div>

        <div class="card th-flow">
          <div class="row between th-flow-head">
            <b><AqIcon :name="mode === 'gentle' ? 'wave' : 'spark'" :size="15" />与 {{ TH_MODES[mode].name }} 的对话</b>
            <button class="btn ghost sm" type="button" @click="clearChat"><AqIcon name="trash" :size="13" />清空倾诉</button>
          </div>

          <div ref="chatEl" class="th-scroll">
            <div v-if="!msgs.length" class="empty">
              <div class="big"><AqIcon :name="mode === 'gentle' ? 'wave' : 'spark'" :size="38" /></div>
              <b>{{ TH_MODES[mode].name }} 在听</b>
              <div class="dim th-welcome">{{ TH_MODES[mode].welcome }}</div>
            </div>
            <div v-for="(m, i) in msgs" :key="i" class="th-row fade-up" :class="m.role">
              <div v-if="m.role === 'user'" class="th-bubble me">{{ m.text }}</div>
              <div v-else class="th-bubble bot" :class="{ streaming: m.streaming }">
                <div v-if="m.err && !m.text" class="msg bad">{{ m.err }}</div>
                <template v-else>{{ m.text }}{{ m.err ? '\n\n[' + m.err + ']' : '' }}<span v-if="m.streaming" class="stream-cur" /></template>
              </div>
            </div>
          </div>

          <div class="th-inputbar">
            <textarea
              ref="inputEl" v-model="inputText" class="textarea" rows="1"
              placeholder="把心里的话说给树洞听…（Enter 发送，Shift+Enter 换行）"
              @keydown.enter.exact.prevent="send" @input="autoGrow"
            />
            <button class="btn primary" type="button" :disabled="busy" @click="send"><AqIcon name="send" :size="14" />说给树洞</button>
          </div>
        </div>
      </div>

      <!-- 右：安全与说明 -->
      <aside class="th-side">
        <div class="card">
          <b><AqIcon name="heart" :size="15" />如果你或身边的人正处于危机</b>
          <p class="dim mt8">请立即联系专业支持（24 小时），他们和你一样认真：</p>
          <div class="th-tel"><span>全国心理援助热线</span><a class="num" href="tel:12356">12356</a></div>
          <div class="th-tel"><span>希望24热线</span><a class="num" href="tel:400-161-9995">400-161-9995</a></div>
        </div>

        <div class="card">
          <b><AqIcon name="bulb" :size="15" />树洞伙伴如何思考（完全公开）</b>
          <p class="dim mt8">树洞的温柔来自一套精心打磨的提示词——我们把它完整公开，欢迎复制到你的项目里：</p>
          <div class="row wrap mt12">
            <CopyBtn v-if="currentPrompt" :text="currentPrompt" label="复制当前模式提示词" size="sm" />
            <button v-else class="btn sm" type="button" @click="loadPrompt">复制当前模式提示词</button>
            <router-link class="btn sm" to="/prompts">提示词工坊</router-link>
          </div>
          <details class="th-prompt-box">
            <summary>展开完整提示词</summary>
            <pre>{{ promptShow }}</pre>
          </details>
        </div>

        <div class="card">
          <b><AqIcon name="info" :size="15" />温柔的边界</b>
          <p class="dim mt8">树洞伙伴是 AI：能陪伴、能倾听，但不能替代专业心理援助。对话内容不会被保存。</p>
        </div>
      </aside>
    </div>
  </div>
</template>

<style scoped>
/* 布局微调：双列结构 / 气泡宽度 / 热线行，颜色全部走设计令牌 */
.th-grid { display: grid; grid-template-columns: minmax(0, 1fr) 300px; gap: 16px; align-items: start; }

.th-pick { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-bottom: 14px; }
.th-role { display: flex; align-items: center; gap: 12px; text-align: left; cursor: pointer; padding: 14px 16px; width: 100%; }
.th-role-txt { flex: 1; min-width: 0; }
.th-role-txt i { display: block; font-style: normal; font-size: 12px; color: var(--txt3); margin-top: 2px; }
.th-check { color: var(--acc); flex: none; }
.th-avatar { width: 38px; height: 38px; flex: none; border-radius: 12px; background: var(--acc-soft); color: var(--acc); display: inline-flex; align-items: center; justify-content: center; }

.th-flow { display: flex; flex-direction: column; }
.th-flow-head { padding-bottom: 10px; border-bottom: 1px solid var(--line); }
.th-scroll { height: min(46vh, 470px); min-height: 260px; overflow-y: auto; padding: 4px 2px; }
.th-welcome { white-space: pre-wrap; max-width: 46ch; margin: 0 auto; }

.th-row { display: flex; margin: 13px 0; }
.th-row.user { justify-content: flex-end; }
.th-bubble {
  max-width: min(82%, 560px);
  padding: 11px 15px;
  border-radius: 16px;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 13.8px;
  line-height: 1.75;
}
.th-bubble.me { background: var(--acc-soft); color: var(--txt0); border-radius: 16px 16px 4px 16px; }
.th-bubble.bot { background: var(--bg3); color: var(--txt1); border-radius: 16px 16px 16px 4px; }
.th-bubble .msg { margin: 0; }

.stream-cur { display: inline-block; width: 7px; height: 14px; margin-left: 3px; vertical-align: -2px; border-radius: 2px; background: var(--acc); animation: th-blink 1s steps(2, start) infinite; }
@keyframes th-blink { 50% { opacity: 0; } }

.th-inputbar { display: flex; align-items: flex-end; gap: 10px; border-top: 1px solid var(--line); padding-top: 12px; margin-top: 8px; }
.th-inputbar .textarea { flex: 1; min-height: 42px; max-height: 140px; resize: none; }

.th-tel { display: flex; justify-content: space-between; align-items: center; gap: 10px; padding: 9px 12px; background: var(--bg3); border-radius: var(--r-sm); margin-top: 8px; font-size: 13px; }
.th-tel a { font-weight: 700; }

.th-side .card + .card { margin-top: 14px; }
.th-prompt-box { margin-top: 12px; }
.th-prompt-box summary { cursor: pointer; user-select: none; color: var(--txt3); font-size: 12.5px; }
.th-prompt-box summary:hover { color: var(--acc); }
.th-prompt-box pre { margin-top: 8px; padding: 12px; background: var(--bg1); border: 1px solid var(--line); border-radius: var(--r-sm); font-size: 12px; line-height: 1.7; color: var(--txt2); white-space: pre-wrap; word-break: break-word; max-height: 260px; overflow-y: auto; }

@media (max-width: 960px) { .th-grid { grid-template-columns: 1fr; } }
@media (max-width: 640px) {
  .th-pick { grid-template-columns: 1fr; }
  .th-bubble { max-width: 88%; }
}
</style>
