<script setup lang="ts">
/* AI 对话（体验中心）：接口对接 1:1 平移自旧版
 * （useModels 模型池 / useSSE.streamChat 流式 / AbortController 停止 / ?model= 深链 / 试跑种子 aqua_pg_seed）
 * UI 全新：顶部工具条 + 消息流（用户渐变边气泡 / AI 纯排版 + <think> 折叠）+ 自增高输入区 */
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useModels, type ModelRow } from '@/composables/useModels'
import { classifyModel, dsMaintenance, dsRetired } from '@/composables/modelMeta'
import { streamChat, type ChatMessage } from '@/composables/useSSE'
import { errText, GATEWAY } from '@/composables/useApi'
import { isLoggedIn } from '@/composables/useAuth'
import AqIcon from '@/components/AqIcon.vue'

interface PgMsg { role: 'user' | 'assistant'; text: string; streaming?: boolean; err?: string }
interface PgView extends PgMsg { think: string; thinkOpen: boolean; body: string }

/* 思考链解析：<think>…</think> 折叠展示；未闭合时视为思考中（正文不含思考内容） */
function parseThink(raw: string): { think: string; thinkOpen: boolean; body: string } {
  const open = raw.indexOf('<think>')
  if (open === -1) return { think: '', thinkOpen: false, body: raw }
  const close = raw.indexOf('</think>')
  if (close === -1) return { think: raw.slice(open + 7), thinkOpen: true, body: raw.slice(0, open) }
  return { think: raw.slice(open + 7, close), thinkOpen: false, body: (raw.slice(0, open) + raw.slice(close + 8)).replace(/^\s+/, '') }
}

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
// 深链 ?model=：列表中出现该模型即选中
const qm = route.query.model
let deepLink = (Array.isArray(qm) ? qm[0] : qm) || ''

/* 离线兜底（先有后优）：进页立即可选，在线列表返回后自动覆盖；离线仅 auto，不落任何清单 */
const OFFLINE: ModelRow[] = [{ id: 'auto', ...classifyModel('auto') }]
const baseRows = computed<ModelRow[]>(() => (models.value.length ? models.value : OFFLINE))
const listRows = computed<ModelRow[]>(() => {
  // 仅对话类模型可选（维护中的官方自营模型除外）；无对话模型时退化为全部列表，避免空下拉
  const ok = baseRows.value.filter(r => !dsMaintenance(r.id) && (!r.type || r.type === 'chat'))
  return ok.length ? ok : baseRows.value
})
const options = computed(() => listRows.value.map(r => ({
  id: r.id,
  label: r.id
    + (r.paid && r.mode === 'per_token' && r.in_price != null && r.per_image == null ? ' · 按量 ¥' + r.in_price + '/百万tok 起' : '')
    + (r.paid && r.per_image != null ? ' · ¥' + (r.per_image / 1_000_000).toFixed(3) + '/张' : '')
    + (r.paid && r.per_image == null && (r.mode !== 'per_token' || r.in_price == null) && r.price_micro ? ' · ¥' + (r.price_micro / 1_000_000).toFixed(3) + '/次' : '')
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

/* 消息视图：AI 消息拆出思考链（折叠区）与正文 */
const views = computed<PgView[]>(() => msgs.value.map(m => {
  if (m.role === 'user') return { ...m, think: '', thinkOpen: false, body: m.text }
  return { ...m, ...parseThink(m.text) }
}))

function scrollBottom() {
  nextTick(() => { const el = chatEl.value; if (el) el.scrollTop = el.scrollHeight })
}

function autoGrow() {
  const el = inputEl.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 140) + 'px'
}

/* 发送：流式对话统一走 useSSE.streamChat（与旧版 pgSend 行为一致） */
async function send() {
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
    let msg = isAbort ? '已停止生成。' : '出错了：' + (errText(e) || '网络异常')
    // 通道波动类错误附加自救引导（与后端人性化文案衔接：告诉用户怎么办而不是只报错）
    if (!isAbort && /没有响应|拥堵|暂时不可用|维护|超时|波动/.test(msg)) {
      msg += '\n提示：可换一个模型，或选 auto（智能路由会自动避开故障模型）；通道恢复后即可继续使用。'
    }
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

function stopStream() { if (abort) { try { abort.abort() } catch { /* 忽略 */ } } }

function clearChat() {
  stopStream()
  chatHistory = []
  msgs.value = []
}

onMounted(() => {
  // 提示词工坊「去试跑」：自动把提示词填入输入框
  try {
    const seed = JSON.parse(sessionStorage.getItem('aqua_pg_seed') || 'null') as { prompt?: string } | null
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
  <div class="wrap">
    <div class="page-head">
      <div>
        <h1><AqIcon name="chat" :size="22" />AI 对话</h1>
        <div class="sub">与网关任意模型流式对话——SSE 实时输出、参数与鉴权行为和真实 API 完全一致，免登录即可体验。</div>
      </div>
      <div class="ops">
        <router-link class="btn" to="/api"><AqIcon name="book" :size="14" />API 文档</router-link>
      </div>
    </div>

    <!-- 未登录引导卡 -->
    <div v-if="!isLoggedIn()" class="card accent login-card fade-up">
      <span class="login-ic"><AqIcon name="key" :size="19" /></span>
      <div class="login-txt">
        <b>登录后体验更完整</b>
        <div class="dim">注册免费——登录后在控制台一键创建 API 密钥，解锁全部模型与更高额度；当前也可以直接免登录试用。</div>
      </div>
      <router-link to="/login" class="btn primary"><AqIcon name="user" :size="14" />登录 / 注册</router-link>
    </div>

    <div class="card chat-shell fade-up">
      <!-- 顶部工具条 -->
      <div class="chat-bar">
        <div class="cb-field cb-grow">
          <label>模型</label>
          <select class="select" v-model="selected" @change="onModelChange">
            <option v-for="o in options" :key="o.id" :value="o.id">{{ o.label }}</option>
          </select>
        </div>
        <div class="cb-field cb-temp">
          <label>随机性 <b class="num temp-val">{{ temp }}</b></label>
          <input type="range" min="0" max="2" step="0.1" v-model="temp" />
        </div>
        <button class="btn" type="button" @click="clearChat"><AqIcon name="trash" :size="14" />清空</button>
      </div>

      <!-- 消息区 -->
      <div ref="chatEl" class="chat-scroll">
        <div v-if="!msgs.length" class="empty">
          <div class="big"><AqIcon name="chat" :size="40" /></div>
          <b>开始体验 AQUA api 网关</b>
          <div class="dim">选择上方任意模型，输入消息即可对话。回复为 SSE 流式实时输出，与真实 API 行为完全一致。</div>
          <div class="code pg-endpoint">POST {{ GATEWAY }}/chat/completions</div>
        </div>
        <template v-for="(v, i) in views" :key="i">
          <!-- 用户：右对齐，accent 渐变边气泡 -->
          <div v-if="v.role === 'user'" class="msg-row me">
            <div class="bubble-user">{{ v.text }}</div>
          </div>
          <!-- AI：左对齐，无底纯排版，思考链折叠 -->
          <div v-else class="msg-row ai">
            <div class="ai-body">
              <div v-if="v.err && !v.text" class="msg bad">{{ v.err }}</div>
              <template v-else>
                <details v-if="v.think" class="think">
                  <summary>{{ v.thinkOpen ? '思考中…' : '思考过程' }}</summary>
                  <div class="think-txt">{{ v.think }}</div>
                </details>
                <div class="ai-txt">{{ v.body }}<span v-if="v.streaming" class="stream-cur" /></div>
              </template>
            </div>
          </div>
        </template>
      </div>

      <!-- 底部输入区 -->
      <div class="chat-input">
        <textarea
          ref="inputEl" v-model="inputText" class="textarea" rows="1"
          placeholder="输入消息，Enter 发送，Shift+Enter 换行"
          @keydown.enter.exact.prevent="send" @input="autoGrow"
        />
        <div class="chat-actions">
          <button v-show="busy" class="btn danger" type="button" @click="stopStream"><AqIcon name="cross" :size="14" />停止</button>
          <button class="btn primary" type="button" :disabled="busy" @click="send"><AqIcon name="send" :size="14" />发送</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 布局微调：聊天壳内部结构（气泡宽度 / 思考折叠 / 输入区），颜色全部走设计令牌 */
.chat-shell { display: flex; flex-direction: column; gap: 12px; }

.chat-bar { display: flex; align-items: flex-end; gap: 12px; flex-wrap: wrap; }
.cb-field { display: flex; flex-direction: column; gap: 6px; }
.cb-field label { font-size: 12.5px; font-weight: 600; color: var(--txt2); }
.cb-grow { flex: 1; min-width: 220px; }
.cb-temp { width: 210px; }
.cb-temp input[type='range'] { width: 100%; accent-color: var(--acc); margin: 9px 0 3px; }
.temp-val { color: var(--acc); }

.login-card { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; margin-bottom: 16px; }
.login-ic { width: 40px; height: 40px; flex: none; border-radius: 12px; background: var(--acc-soft); color: var(--acc); display: inline-flex; align-items: center; justify-content: center; }
.login-txt { flex: 1; min-width: 240px; }
.login-card .btn { margin-left: auto; }

.chat-scroll { height: min(52vh, 540px); min-height: 300px; overflow-y: auto; padding: 4px 2px; }
.msg-row { display: flex; margin: 14px 0; }
.msg-row.me { justify-content: flex-end; }
.msg-row.ai { justify-content: flex-start; }

.bubble-user {
  max-width: min(78%, 560px);
  padding: 10px 15px;
  border-radius: 14px 14px 4px 14px;
  border: 1px solid transparent;
  background: linear-gradient(var(--bg2-solid), var(--bg2-solid)) padding-box, var(--acc-grad) border-box;
  color: var(--txt0);
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 13.8px;
  line-height: 1.7;
}

.ai-body { max-width: min(86%, 720px); width: 100%; }
.ai-txt { white-space: pre-wrap; word-break: break-word; color: var(--txt1); font-size: 13.8px; line-height: 1.75; }
.ai-body .msg { margin: 0; }

.think { margin: 2px 0 8px; border-left: 2px solid var(--line-strong); padding-left: 11px; }
.think summary { cursor: pointer; user-select: none; color: var(--txt3); font-size: 12px; }
.think summary:hover { color: var(--acc); }
.think-txt { margin-top: 6px; color: var(--txt2); font-size: 12.5px; line-height: 1.7; white-space: pre-wrap; word-break: break-word; }

.stream-cur { display: inline-block; width: 7px; height: 14px; margin-left: 3px; vertical-align: -2px; border-radius: 2px; background: var(--acc); animation: cur-blink 1s steps(2, start) infinite; }
@keyframes cur-blink { 50% { opacity: 0; } }

.chat-input { display: flex; align-items: flex-end; gap: 10px; border-top: 1px solid var(--line); padding-top: 12px; }
.chat-input .textarea { flex: 1; min-height: 42px; max-height: 140px; resize: none; }
.chat-actions { display: flex; gap: 8px; flex-shrink: 0; }

.pg-endpoint { display: inline-block; margin-top: 12px; }

@media (max-width: 640px) {
  .bubble-user { max-width: 88%; }
  .ai-body { max-width: 100%; }
  .cb-temp { width: 100%; }
}
</style>
