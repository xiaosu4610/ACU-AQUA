<script setup lang="ts">
import { ref } from 'vue'
import { errText } from '@/composables/useApi'
import { apiChat, useChatModels } from '@/composables/useToolChat'

const { chatOpts, llmModel } = useChatModels()

const status = ref('输入一个四字成语开始')
const input = ref('')
const chain = ref<{ who: 'user' | 'ai'; word: string }[]>([])
const resultMsg = ref('')
let needChar = ''
let busy = false // AI 思考锁：AI 回合中禁止玩家提交
let gen = 0      // 对局代际：重开后旧 AI 回调作废

function isIdiom(s: string) { return /^[\u4e00-\u9fa5]{4}$/.test(s) }

function win(msg: string) {
  status.value = msg
  resultMsg.value = msg
}

function userGo() {
  if (busy) { status.value = 'AI 思考中，请稍候…'; return }
  const w = input.value.trim()
  input.value = ''
  if (!isIdiom(w)) { status.value = '请输入四字成语'; return }
  if (needChar && w[0] !== needChar) { status.value = '接龙失败：需要以「' + needChar + '」开头'; return }
  if (chain.value.some(c => c.word === w)) { status.value = '这个成语已经用过了'; return }
  busy = true
  chain.value.push({ who: 'user', word: w })
  needChar = w[3]
  status.value = 'AI 思考中…（需以「' + needChar + '」开头）'
  aiGo(w, needChar, gen)
}

async function aiGo(prevWord: string, startChar: string, myGen: number) {
  const used = chain.value.map(c => c.word).join('、')
  try {
    const out = await apiChat([
      { role: 'system', content: '你在玩成语接龙。只输出一个四字成语本身，不要标点不要解释。成语必须以指定汉字开头，且不能与已用过的成语重复。' },
      { role: 'user', content: '上一个成语是「' + prevWord + '」。你必须输出一个以「' + startChar + '」开头的四字成语。已用过的成语（禁止重复）：' + (used || '无') },
    ], { model: llmModel.value, temperature: 0.6, max_tokens: 30 })
    if (myGen !== gen) return // 思考期间重开了对局，丢弃过期回调
    const w = out ? String(out).replace(/[^一-鿿]/g, '').slice(0, 4) : ''
    if (!isIdiom(w) || w[0] !== startChar || chain.value.some(c => c.word === w)) {
      busy = false
      win('AI 接不上「' + startChar + '」开头的成语，你赢了！共 ' + chain.value.length + ' 手')
      return
    }
    chain.value.push({ who: 'ai', word: w })
    needChar = w[3]
    busy = false
    status.value = '轮到你了（需以「' + needChar + '」开头）'
  } catch (e) {
    if (myGen !== gen) return
    busy = false
    win('AI 调用失败（' + errText(e) + '），本轮不计胜负，请重新开始')
  }
}

function restart() {
  gen++
  busy = false // 作废思考中的旧回调
  chain.value = []
  needChar = ''
  resultMsg.value = ''
  status.value = '输入一个四字成语开始'
}
</script>

<template>
  <p class="tool-intro">与 AI 轮流接龙：你出一个四字成语，AI 必须用它的最后一个字开头接一个新成语，如此往复。首尾字自动校验，AI 接不上或接错就判你赢！</p>
  <div class="tool-bar">
    <span>模型</span>
    <select v-model="llmModel">
      <option v-for="m in chatOpts" :key="m" :value="m">{{ m }}</option>
    </select>
    <button class="btn tool-run" @click="restart">重新开始</button>
    <span class="tool-status">{{ status }}</span>
  </div>
  <div class="tool-io">
    <div class="tool-btns" style="width:100%;">
      <input v-model="input" class="tool-flex1" placeholder="输入一个四字成语开始，如：一帆风顺" @keydown.enter="userGo">
      <button class="btn tool-run" @click="userGo">接龙</button>
    </div>
  </div>
  <div class="tool-kv">
    <div v-for="(c, i) in chain" :key="i" class="pg-hrow"><span>第 {{ i + 1 }} 手 · {{ c.who === 'user' ? '你' : 'AI' }}</span><b>{{ c.word }}</b></div>
  </div>
  <div class="tool-result">
    <div v-if="resultMsg" class="tool-ai-box"><b>对局结束</b><div class="tool-ai-text">{{ resultMsg }}</div></div>
  </div>
</template>
