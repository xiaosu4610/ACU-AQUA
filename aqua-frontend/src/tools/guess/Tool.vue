<script setup lang="ts">
import { ref, watch } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat, useChatModels } from '@/composables/useToolChat'
import { TOOL_ICONS } from '@/tools/meta'
import AqIcon from '@/components/AqIcon.vue'

const { chatOpts, llmModel } = useChatModels()

const mode = ref('user')
const status = ref('AI 已出题，开始猜吧')
const resultTitle = ref('')
const resultMsg = ref('')
const input = ref('')
const inputDisabled = ref(false)
const beginDisabled = ref(false)
const history = ref<{ g: string; a: number; b: number }[]>([])
const secret = ref('')
let gen = 0 // 对局代际：重开/切模式后旧 LLM 回调作废

function randSecret() {
  const digits = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
  for (let i = digits.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    const t = digits[i]; digits[i] = digits[j]; digits[j] = t
  }
  return digits.slice(0, 4).join('')
}
function judge(guess: string, ans: string): [number, number] {
  let a = 0, b = 0
  for (let i = 0; i < 4; i++) {
    if (guess[i] === ans[i]) a++
    else if (ans.indexOf(guess[i]) !== -1) b++
  }
  return [a, b]
}
function valid(s: string) { return /^[0-9]{4}$/.test(s) && new Set(s.split('')).size === 4 }

function startUser() {
  gen++
  secret.value = randSecret()
  rounds.value = 0
  history.value = []
  input.value = ''
  inputDisabled.value = false
  beginDisabled.value = false
  status.value = 'AI 已出题，开始猜吧'
}
function startLlm() {
  gen++
  rounds.value = 0
  history.value = []
  input.value = ''
  inputDisabled.value = false
  beginDisabled.value = false
  status.value = '输入你的数字后点击开始'
}
watch(mode, v => {
  resultTitle.value = ''
  resultMsg.value = ''
  if (v === 'llm') startLlm(); else startUser()
})

/* 你猜 AI 出题 */
const rounds = ref(0)
function userGuess() {
  const g = input.value.trim()
  if (!valid(g)) { status.value = '格式错误：需要 4 位不重复数字'; return }
  input.value = ''
  rounds.value++
  const [a, b] = judge(g, secret.value)
  history.value.push({ g, a, b })
  if (a === 4) {
    status.value = rounds.value + ' 次猜中！'
    resultTitle.value = '胜利'
    resultMsg.value = '答案就是 ' + secret.value + '，你用了 ' + rounds.value + ' 次。'
  } else {
    status.value = g + ' → ' + a + 'A' + b + 'B'
  }
}

/* LLM 挑战模式：你想一个数字，看 AI 几步猜中 */
function beginChallenge() {
  const s = input.value.trim()
  if (!valid(s)) { status.value = '格式错误：需要 4 位不重复数字'; return }
  inputDisabled.value = true
  beginDisabled.value = true
  secret.value = s
  llmGuess(gen)
}
async function llmGuess(myGen: number) {
  if (myGen !== gen) return
  status.value = 'LLM 推理中…'
  const logText = history.value.map(h => h.g + ' → ' + h.a + 'A' + h.b + 'B').join('\n') || '（还没有猜测记录）'
  try {
    const out = await apiChat([
      { role: 'system', content: '你在玩 1A2B 猜数字：目标是 4 位不重复数字。A=数字位置都对，B=数字对位置不对。根据历史猜测反馈推理下一步。只输出一个 4 位数字候选，不要解释。' },
      { role: 'user', content: '历史猜测与反馈：\n' + logText + '\n输出你的下一个猜测（4 位不重复数字）。' },
    ], { model: llmModel.value, temperature: 0.3, max_tokens: 30 })
    if (myGen !== gen) return
    let g = ''
    const m = out && String(out).match(/[0-9]{4}/)
    if (m) g = m[0]
    if (!valid(g)) g = randSecret()
    rounds.value++
    const [a, b] = judge(g, secret.value)
    history.value.push({ g, a, b })
    if (a === 4) {
      status.value = 'AI 用 ' + rounds.value + ' 次猜中了'
      resultTitle.value = 'AI 获胜'
      resultMsg.value = '你的数字是 ' + secret.value + '，AI 用了 ' + rounds.value + ' 次推理命中。'
    } else {
      status.value = 'AI 猜 ' + g + ' → ' + a + 'A' + b + 'B'
      setTimeout(() => llmGuess(myGen), 800)
    }
  } catch (e) {
    if (myGen !== gen) return
    status.value = 'LLM 调用失败（' + errText(e) + '），请重新开始'
  }
}
function restart() {
  resultTitle.value = ''
  resultMsg.value = ''
  if (mode.value === 'llm') startLlm(); else startUser()
}
startUser()
</script>

<template>
  <p class="tool-intro">4 位不重复数字。<b>A</b> = 数字和位置都对，<b>B</b> = 数字对位置不对。「你猜 AI」模式下 AI 出题你猜；「LLM 挑战」模式下你想一个数字，看 AI 需要几步推理出来。</p>
  <div class="tool-bar">
    <span>模式</span>
    <select v-model="mode" class="select">
      <option value="user">你猜 AI 出题</option>
      <option value="llm">LLM 挑战模式</option>
    </select>
    <select v-show="mode === 'llm'" v-model="llmModel" class="select">
      <option v-for="m in chatOpts" :key="m" :value="m">{{ m }}</option>
    </select>
    <button class="btn" @click="restart"><AqIcon name="refresh" :size="14" />重新开始</button>
    <span class="tool-status">{{ status }}</span>
  </div>
  <div class="grid2">
    <div class="card out-pane">
      <!-- 你猜 AI 出题 -->
      <template v-if="mode === 'user'">
        <div class="field">
          <label>你的猜测（4 位不重复数字）</label>
          <input v-model="input" class="input mono" placeholder="如 0123" @keydown.enter="userGuess">
        </div>
        <button class="btn primary" @click="userGuess">猜！</button>
      </template>
      <!-- LLM 挑战模式 -->
      <template v-else>
        <div class="field">
          <label>想一个 4 位不重复数字（别告诉 AI）</label>
          <input v-model="input" class="input mono" placeholder="如 0123" :disabled="inputDisabled">
        </div>
        <button class="btn primary" :disabled="beginDisabled" @click="beginChallenge">开始挑战</button>
      </template>
    </div>
    <div class="card out-pane">
      <template v-if="resultMsg">
        <div class="row between"><b>{{ resultTitle }}</b></div>
        <div class="tool-prose">{{ resultMsg }}</div>
      </template>
      <div v-if="history.length" class="tool-kv">
        <div v-for="(h, i) in history" :key="i"><span>第 {{ i + 1 }} 次 · {{ h.g }}</span><b>{{ h.a }}A{{ h.b }}B</b></div>
      </div>
      <div v-if="!resultMsg && !history.length" class="out-empty"><svg class="ticon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" v-html="TOOL_ICONS.guess"></svg><span>{{ status }}</span></div>
    </div>
  </div>
</template>

<style scoped>
.ticon { width: 26px; height: 26px; opacity: .55; }
</style>
