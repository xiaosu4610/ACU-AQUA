<script setup lang="ts">
/* AI 对话（体验中心）：自旧版 pgInit/pgFillModels/pgRenderHealth/pgSend 平移 */
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useModels, type ModelRow } from '@/composables/useModels'
import { classifyModel, dsMaintenance, dsRetired, fallbackModels } from '@/composables/modelMeta'
import { streamChat, type ChatMessage } from '@/composables/useSSE'
import { errText } from '@/composables/useApi'
import AuthBanner from '@/components/AuthBanner.vue'

interface PgMsg { role: 'user' | 'assistant'; text: string; streaming?: boolean; err?: string }

const route = useRoute()
const { models, load } = useModels()

const selected = ref('')
const temp = ref('0.7')
const inputText = ref('')
const busy = ref(false)
const msgs = ref<PgMsg[]>([])
const inputEl = ref<HTMLTextAreaElement | null>(null)
const chatEl = ref<HTMLElement | null>(null)

let abort: AbortController | null = null
let chatHistory: ChatMessage[] = []
// 深链 ?model=（#/playground?model=xxx）：列表中出现该模型即选中
const qm = route.query.model
let deepLink = (Array.isArray(qm) ? qm[0] : qm) || ''

/* 离线兜底（先有后优）：进页立即可选，在线列表返回后自动覆盖 */
const OFFLINE: ModelRow[] = ['auto', ...fallbackModels].map(id => ({ id, ...classifyModel(id) }))
const baseRows = computed<ModelRow[]>(() => (models.value.length ? models.value : OFFLINE))
const listRows = computed<ModelRow[]>(() => {
  // 仅对话类模型可选（维护中的官方自营模型除外）；无对话模型时退化为全部列表，避免空下拉
  const ok = baseRows.value.filter(r => !dsMaintenance(r.id) && (!r.type || r.type === 'chat'))
  return ok.length ? ok : baseRows.value
})
const options = computed<ModelRow[]>(() => listRows.value.map(r => ({
  id: r.id,
  label: r.id
    + (r.paid && r.mode === 'per_token' && r.in_price != null ? ' · 按量 ¥' + r.in_price + '/百万tok 起' : '')
    + (r.paid && (r.mode !== 'per_token' || r.in_price == null) && r.price_micro ? ' · ¥' + (r.price_micro / 1_000_000).toFixed(3) + '/次' : '')
    + (r.health && typeof r.health.score === 'number' ? ' · ' + r.health.score + '分' : ''),
})))

/* 保持原选择；否则默认 auto（智能路由）> deepseek-v4-flash（未到下线时刻）> 健康分最高者 > 第一项 */
function applySelection() {
  const rows = listRows.value
  if (!rows.length) return
  const ids = rows.map(r => r.id)
  if (deepLink && ids.includes(deepLink)) { selected.value = deepLink; deepLink = ''; return }
  if (selected.value && ids.includes(selected.value)) return
  const autoIdx = ids.indexOf('auto')
  if (autoIdx !== -1) { selected.value = 'auto'; return }
  const prefer = !dsRetired() ? ids.indexOf('deepseek-v4-flash') : -1
  if (prefer !== -1) { selected.value = 'deepseek-v4-flash'; return }
  let best = -1, bestScore = -1
  rows.forEach((r, i) => { const s = r.health?.score ?? -1; if (s > bestScore) { bestScore = s; best = i } })
  selected.value = rows[best !== -1 ? best : 0].id
}
watch(listRows, applySelection, { immediate: true })

function onModelChange() { deepLink = '' }

function scrollBottom() {
  nextTick(() => { const el = chatEl.value; if (el) el.scrollTop = el.scrollHeight })
}

function autoGrow() {
  const el = inputEl.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 140) + 'px'
}

/* pgSend 平移：流式对话统一走 useSSE.streamChat */
async function pgSend() {
  if (busy.value) return
  const text = inputText.value.trim()
  if (!text || !selected.value) return
  inputText.value = ''
  autoGrow()
  chatHistory.push({ role: 'user', content: text })
  msgs.value.push({ role: 'user', text })
  const ai = reactive<PgMsg>({ role: 'assistant', text: '', streaming: true })
  msgs.value.push(ai)
  busy.value = true
  abort = new AbortController()
  scrollBottom()
  let full = ''
  try {
    full = await streamChat({
      model: selected.value,
      messages: chatHistory.slice(-20),
      signal: abort.signal,
      temperature: parseFloat(temp.value || '0.7'),
      onDelta(piece) { full += piece; ai.text = full; scrollBottom() },
    })
    ai.streaming = false
    if (full) { ai.text = full; chatHistory.push({ role: 'assistant', content: full }) }
    else ai.err = '上游未返回任何内容，请换一个模型重试。'
  } catch (e) {
    ai.streaming = false
    const isAbort = (e as { name?: string })?.name === 'AbortError'
    const msg = isAbort ? '已停止生成。' : '出错了：' + (errText(e) || '网络异常')
    if (!ai.text) ai.err = msg
    else ai.text += '\n\n[' + msg + ']'
    // 失败时回滚最后一条用户消息，方便重试
    if (chatHistory.length && chatHistory[chatHistory.length - 1].role === 'user') chatHistory.pop()
  } finally {
    busy.value = false
    abort = null
    scrollBottom()
  }
}

function pgStopStream() { if (abort) { try { abort.abort() } catch { /* 忽略 */ } } }

function pgClear() {
  pgStopStream()
  chatHistory = []
  msgs.value = []
}

onMounted(() => {
  // 提示词工坊「去试跑」：自动把提示词填入输入框
  try {
    const seed = JSON.parse(sessionStorage.getItem('aqua_pg_seed') || 'null')
    if (seed && seed.prompt) {
      sessionStorage.removeItem('aqua_pg_seed')
      inputText.value = String(seed.prompt)
      nextTick(() => { autoGrow(); inputEl.value?.focus() })
    }
  } catch { /* 忽略 */ }
  load()
})
</script>

<template>
  <section class="route-page">
    <div class="models-page-head">
      <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg></span>体验中心</h1>
      <p>免登录直接对话、玩 AI 对弈游戏、用免费实用工具——所有体验与真实 API 行为完全一致。</p>
    </div>
    <nav class="hub-subnav" aria-label="体验中心子导航">
      <router-link class="hub-tab" to="/playground" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg></span>AI 对话</router-link>
      <router-link class="hub-tab" to="/treehole" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22c4-3 8-6.5 8-11a8 8 0 1 0-16 0c0 4.5 4 8 8 11z" style="display:none"/><path d="M12 3c-4.4 0-8 3.6-8 8 0 4.4 3.6 8 8 8h.5c.3 0 .5.2.5.5V21l3.8-2.6C19.9 17 20.5 14 20.5 11 20.5 6.6 16.4 3 12 3z"/><circle cx="8.5" cy="10.5" r=".8" fill="currentColor"/><circle cx="12" cy="10.5" r=".8" fill="currentColor"/><circle cx="15.5" cy="10.5" r=".8" fill="currentColor"/></svg></span>树洞</router-link>
      <router-link class="hub-tab" to="/tools" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg></span>工具箱</router-link>
      <router-link class="hub-tab" to="/prompts" active-class="active"><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z"/></svg></span>提示词工坊</router-link>
    </nav>

    <AuthBanner />

    <div class="pg-wrap">
      <div class="pg-toolbar">
        <div class="pg-model-pick">
          <label>模型</label>
          <select id="pg-model" v-model="selected" @change="onModelChange">
            <option v-for="o in options" :key="o.id" :value="o.id">{{ o.label }}</option>
          </select>
        </div>
        <div class="pg-temp">
          <label>随机性 <span id="pg-temp-val">{{ temp }}</span></label>
          <input type="range" id="pg-temp" min="0" max="2" step="0.1" v-model="temp">
        </div>
        <button class="btn" id="pg-clear" @click="pgClear">清空对话</button>
      </div>
      <div class="pg-main">
        <div class="pg-chat" id="pg-chat" ref="chatEl">
          <div v-if="!msgs.length" class="pg-welcome">
            <b>开始体验 AQUA 网关</b>
            <p>选择上方任意模型，输入消息即可对话。回复为 SSE 流式实时输出，与真实 API 行为完全一致。</p>
          </div>
          <div v-for="(m, i) in msgs" :key="i" class="pg-msg" :class="[m.role, { streaming: m.streaming }]">
            <span v-if="m.err && !m.text" class="pg-err">{{ m.err }}</span>
            <template v-else>{{ m.text }}{{ m.err ? '\n\n[' + m.err + ']' : '' }}</template>
          </div>
        </div>
        <aside class="pg-side" id="pg-side">
          <div class="pg-side-card">
            <b>调用方式</b>
            <p>本页面与 API 调用行为一致，接口地址 <code>https://api.ltzy.top/v1/chat/completions</code>，登录后在控制台创建密钥即可调用。模型健康分见下拉标注与「模型中心」页。</p>
          </div>
        </aside>
      </div>
      <div class="pg-inputbar">
        <textarea id="pg-input" ref="inputEl" rows="1" placeholder="输入消息，Enter 发送，Shift+Enter 换行" v-model="inputText" @keydown.enter.exact.prevent="pgSend" @input="autoGrow"></textarea>
        <button class="btn pg-send" id="pg-send" :disabled="busy" @click="pgSend">发送</button>
        <button class="btn pg-stop" id="pg-stop" v-show="busy" @click="pgStopStream">停止</button>
      </div>
    </div>
  </section>
</template>
