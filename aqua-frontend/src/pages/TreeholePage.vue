<script setup lang="ts">
/* 树洞（情绪陪伴）：自旧版 th* 系列函数平移（服务端官方提示词 + 非 Nvidia 模型链，流式） */
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { apiJson, errText, GATEWAY } from '@/composables/useApi'
import { stripThink } from '@/composables/useSSE'
import CopyBtn from '@/components/CopyBtn.vue'
import AuthBanner from '@/components/AuthBanner.vue'
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
    try { cache = JSON.parse(sessionStorage.getItem('aqua_th_prompts') || 'null') } catch { /* 忽略 */ }
  }
  if (cache && cache.modes) {
    for (const x of cache.modes) if (x.id === m) return x.prompt
  }
  return ''
}
const currentPrompt = computed(() => thPromptOf(mode.value))
const promptShow = computed(() => currentPrompt.value
  || '提示词加载失败，请稍后刷新重试（服务端会强制注入官方提示词，即使此处未展示也不影响对话质量）。')

async function thLoadPrompt() {
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

function thSwitchMode(m: 'gentle' | 'anime') {
  if (mode.value === m || busy.value) return
  mode.value = m
  chatHistory = []
  msgs.value = []
}

function thClear() {
  if (busy.value && abort) { try { abort.abort() } catch { /* 忽略 */ } }
  chatHistory = []
  msgs.value = []
}

/* thSend 平移：POST /tools/treehole 流式（服务端注入官方提示词与模型），思维链用 stripThink 剥离。
   注：streamChat 固定请求 /chat/completions，树洞必须走专用端点，故 SSE 解析在本组件内实现（与 streamChat 同构）。 */
async function thSend() {
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
      const j = JSON.parse(payload)
      const piece = (j?.choices?.[0]?.delta?.content || '') as string
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
  thLoadPrompt()
})
</script>

<template>
  <section class="route-page">
    <div class="models-page-head">
      <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg></span>体验中心</h1>
      <p>登录账号后即可倾诉对话、玩 AI 对弈游戏、用免费实用工具——所有体验与真实 API 行为完全一致。</p>
    </div>
    <AuthBanner />
    <nav class="hub-subnav" aria-label="体验中心子导航">
      <router-link class="hub-tab" to="/playground" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg></span>AI 对话</router-link>
      <router-link class="hub-tab" to="/treehole" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22c4-3 8-6.5 8-11a8 8 0 1 0-16 0c0 4.5 4 8 8 11z" style="display:none"/><path d="M12 3c-4.4 0-8 3.6-8 8 0 4.4 3.6 8 8 8h.5c.3 0 .5.2.5.5V21l3.8-2.6C19.9 17 20.5 14 20.5 11 20.5 6.6 16.4 3 12 3z"/><circle cx="8.5" cy="10.5" r=".8" fill="currentColor"/><circle cx="12" cy="10.5" r=".8" fill="currentColor"/><circle cx="15.5" cy="10.5" r=".8" fill="currentColor"/></svg></span>树洞</router-link>
      <router-link class="hub-tab" to="/tools" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg></span>工具箱</router-link>
      <router-link class="hub-tab" to="/prompts" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z"/></svg></span>提示词工坊</router-link>
    </nav>

    <div class="th-wrap">
      <div class="th-toolbar">
        <div class="th-modes">
          <button class="th-mode" :class="{ active: mode === 'gentle' }" data-th-mode="gentle" type="button" @click="thSwitchMode('gentle')">
            <span class="th-avatar"><AqIcon name="wave" :size="16" /></span>
            <span class="th-mode-info"><b>小溪</b><i>温柔树洞 · 深夜电台的倾听伙伴</i></span>
          </button>
          <button class="th-mode" :class="{ active: mode === 'anime' }" data-th-mode="anime" type="button" @click="thSwitchMode('anime')">
            <span class="th-avatar"><AqIcon name="spark" :size="16" /></span>
            <span class="th-mode-info"><b>星璃</b><i>二次元陪伴 · 跨次元的元气伙伴</i></span>
          </button>
        </div>
        <button class="btn" id="th-clear" @click="thClear">清空倾诉</button>
      </div>
      <div class="th-main">
        <div class="th-chat" id="th-chat" ref="chatEl">
          <div v-if="!msgs.length" class="th-welcome">
            <b>{{ TH_MODES[mode].name }} 在听</b>
            <p style="white-space:pre-wrap;">{{ TH_MODES[mode].welcome }}</p>
          </div>
          <div v-for="(m, i) in msgs" :key="i" class="th-msg" :class="[m.role, { streaming: m.streaming }]">
            <span v-if="m.err && !m.text" class="th-err">{{ m.err }}</span>
            <template v-else>{{ m.text }}{{ m.err ? '\n\n[' + m.err + ']' : '' }}</template>
          </div>
        </div>
        <div class="th-inputbar">
          <textarea id="th-input" ref="inputEl" rows="1" placeholder="把心里的话说给树洞听…（Enter 发送，Shift+Enter 换行）" v-model="inputText" @keydown.enter.exact.prevent="thSend" @input="autoGrow"></textarea>
          <button class="btn th-send" id="th-send" @click="thSend">说给树洞</button>
        </div>
      </div>
      <aside class="th-side">
        <div class="th-side-card th-hotline">
          <b>如果你或身边的人正处于危机</b>
          <p>请立即联系专业支持（24 小时），他们和你一样认真：</p>
          <div class="th-tel">全国心理援助热线 <a href="tel:12356">12356</a></div>
          <div class="th-tel">希望24热线 <a href="tel:400-161-9995">400-161-9995</a></div>
        </div>
        <div class="th-side-card">
          <b>树洞伙伴如何思考（完全公开）</b>
          <p>树洞的温柔来自一套精心打磨的提示词——我们把它完整公开，欢迎复制到你的项目里：</p>
          <div class="th-actions">
            <CopyBtn v-if="currentPrompt" :text="currentPrompt" label="复制当前模式提示词" />
            <button v-else class="btn" id="th-copy-prompt" type="button" @click="thLoadPrompt">复制当前模式提示词</button>
            <router-link class="btn" to="/prompts">提示词工坊</router-link>
          </div>
          <details class="th-prompt-box"><summary>展开完整提示词</summary><pre id="th-prompt-text">{{ promptShow }}</pre></details>
        </div>
        <div class="th-side-card th-disclaimer">
          <b>温柔的边界</b>
          <p>树洞伙伴是 AI：能陪伴、能倾听，但不能替代专业心理援助。对话内容不会被保存。</p>
        </div>
      </aside>
    </div>
  </section>
</template>
