<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { apiJson } from '@/composables/useApi'
import { useModels } from '@/composables/useModels'
import { dsMaintenance } from '@/composables/modelMeta'
import AqIcon from '@/components/AqIcon.vue'

const { models, load: loadModels } = useModels()
onMounted(() => { loadModels() })

/* 对话模型池：过滤非对话类型与官方自营维护中模型（与旧版 pgModelOk 一致） */
const chatOpts = computed(() =>
  models.value.filter(m => !dsMaintenance(m.id) && (!m.type || m.type === 'chat')).map(m => m.id))
const llmModel = ref('')
watch(chatOpts, v => { if (!llmModel.value && v.length) llmModel.value = v[0] }, { immediate: true })

/* 非流式对话统一入口：默认选最优可用模型，失败自动换备用健康模型重试一次（与旧版 apiChat 等价） */
const APICHAT_FALLBACKS = ['deepseek-v4-flash', 'qwen3-8b', 'qwen3-4b', 'gpt-oss-120b', 'glm-4-9b-0414', 'gpt-oss-20b']
async function apiChat(messages: { role: string; content: string }[], opts: { model?: string; temperature?: number; max_tokens?: number; noFallback?: boolean } = {}): Promise<string> {
  function fallbackOf(failed: string): string | null {
    for (const fb of APICHAT_FALLBACKS) if (fb !== failed && chatOpts.value.includes(fb)) return fb
    return null
  }
  function defaultModel(): string {
    const pool = chatOpts.value
    return pool.includes('deepseek-v4-flash') ? 'deepseek-v4-flash' : (pool[0] || 'deepseek-v4-flash')
  }
  async function attempt(model: string, isRetry: boolean): Promise<string> {
    try {
      const j: any = await apiJson('/chat/completions', {
        method: 'POST',
        body: {
          model,
          messages,
          stream: false,
          temperature: opts.temperature != null ? opts.temperature : 0.3,
          max_tokens: opts.max_tokens || 1024,
        },
      })
      const c = (j && j.choices && j.choices[0] && j.choices[0].message) || {}
      // content 为空时回退 reasoning_content（部分推理模型把输出全放在思考字段）
      const out = c.content || c.reasoning_content || ''
      if (!out) throw new Error('模型未返回内容，请换一个模型重试')
      return out
    } catch (err) {
      if (isRetry || opts.noFallback) throw err
      const fb = fallbackOf(model)
      if (!fb) throw err
      return attempt(fb, true)
    }
  }
  return attempt(opts.model || defaultModel(), false)
}

/* ---- 井字棋对弈（minimax 算法 / LLM 对手可切换） ---- */
const mode = ref('algo')
const board = ref<string[]>(Array(9).fill(''))
const over = ref(false)
const busy = ref(false) // AI 回合锁：思考中禁止落子
let gen = 0             // 对局代际：重开后旧 AI 回调作废
const status = ref('轮到你了（X）')
const resultMsg = ref('')
const WINS = [[0, 1, 2], [3, 4, 5], [6, 7, 8], [0, 3, 6], [1, 4, 7], [2, 5, 8], [0, 4, 8], [2, 4, 6]]

function winner(b: string[]): { who: string; line: number[] } | null {
  for (let i = 0; i < WINS.length; i++) {
    const w = WINS[i]
    if (b[w[0]] && b[w[0]] === b[w[1]] && b[w[1]] === b[w[2]]) return { who: b[w[0]], line: w }
  }
  return b.indexOf('') === -1 ? { who: 'draw', line: [] } : null
}
function minimax(b: string[], isAi: boolean, depth: number): number {
  const w = winner(b)
  if (w) {
    if (w.who === 'O') return 10 - depth
    if (w.who === 'X') return depth - 10
    return 0
  }
  let best = isAi ? -99 : 99
  for (let i = 0; i < 9; i++) {
    if (b[i]) continue
    b[i] = isAi ? 'O' : 'X'
    const v = minimax(b, !isAi, depth + 1)
    b[i] = ''
    if (isAi ? v > best : v < best) best = v
  }
  return best
}
function bestMove(): number {
  const b = board.value.slice()
  let best = -99
  let cand: number[] = []
  for (let i = 0; i < 9; i++) {
    if (b[i]) continue
    b[i] = 'O'
    const v = minimax(b, false, 0)
    b[i] = ''
    if (v > best) { best = v; cand = [i] } else if (v === best) cand.push(i)
  }
  return cand[Math.floor(Math.random() * cand.length)]
}
function finish(w: { who: string }) {
  over.value = true
  busy.value = false
  const msg = w.who === 'X' ? '你赢了！' : w.who === 'O' ? '对手获胜' : '平局'
  status.value = msg
  resultMsg.value = msg
}
function aiMoveAlgo() {
  const i = bestMove()
  board.value[i] = 'O'
  busy.value = false // 先释放锁再重绘，确保 .busy 视觉状态同步清除
  const w = winner(board.value)
  if (w) return finish(w)
  status.value = '轮到你了（X）'
}
async function aiMoveLlm(myGen: number) {
  const model = llmModel.value
  let grid = ''
  for (let r = 0; r < 3; r++) {
    grid += board.value.slice(r * 3, r * 3 + 3).map(c => c || '-').join(' ') + '\n'
  }
  status.value = 'LLM 思考中…'
  try {
    const out = await apiChat([
      { role: 'system', content: '你在玩井字棋，执 O 对抗人类的 X。棋盘用 - 表示空格。只输出一个 0-8 的数字表示你的落子位置（0 左上，8 右下），不要输出任何其他内容。必须选择空格，优先取胜、其次阻挡人类连三。' },
      { role: 'user', content: '当前棋盘：\n' + grid + '轮到你（O）落子，输出空格编号。' },
    ], { model, temperature: 0.2, max_tokens: 20 })
    if (myGen !== gen) return // 思考期间重开了对局，丢弃过期回调
    let idx = -1
    if (out) { const mnum = String(out).match(/[0-8]/); if (mnum) idx = +mnum[0] }
    // LLM 输出无效时兜底选第一个空格
    if (idx < 0 || board.value[idx]) { idx = -1; for (let i = 0; i < 9; i++) if (!board.value[i]) { idx = i; break } }
    board.value[idx] = 'O'
    busy.value = false // 先释放锁再重绘，确保 .busy 视觉状态同步清除
    const w = winner(board.value)
    if (w) return finish(w)
    status.value = '轮到你了（X）'
  } catch {
    if (myGen !== gen) return
    // LLM 异常自动回退算法对手，对局不中断
    status.value = 'LLM 调用失败，回退算法对手'
    aiMoveAlgo()
  }
}
function onCell(i: number) {
  if (over.value || busy.value || board.value[i]) return // 思考中/已结束/已占用一律禁止
  busy.value = true
  board.value[i] = 'X'
  const w = winner(board.value)
  if (w) return finish(w)
  status.value = '对手思考中…'
  const myGen = gen
  setTimeout(() => {
    if (myGen !== gen) return // 重开后作废
    if (mode.value === 'llm') aiMoveLlm(myGen); else aiMoveAlgo()
  }, 300)
}
function restart() {
  gen++
  busy.value = false // 作废思考中的旧回调
  board.value = Array(9).fill('')
  over.value = false
  resultMsg.value = ''
  status.value = '轮到你了（X）'
}
</script>

<template>
  <p class="tool-intro">你是 <b><AqIcon name="x-mark" :size="13" />（X）</b>，对手是 <b><AqIcon name="circle-mark" :size="13" />（O）</b>。「算法」对手使用 minimax 全量搜索，理论上不可战胜（最好结果逼平）；「LLM 模型」对手由你选择的 AI 模型实时思考落子——试试能不能抓住大模型的逻辑漏洞。</p>
  <div class="tool-bar">
    <span>对手</span>
    <select v-model="mode" class="select">
      <option value="algo">minimax 算法</option>
      <option value="llm">LLM 模型</option>
    </select>
    <select v-show="mode === 'llm'" v-model="llmModel" class="select">
      <option v-for="m in chatOpts" :key="m" :value="m">{{ m }}</option>
    </select>
    <button class="btn" @click="restart"><AqIcon name="refresh" :size="14" />重新开始</button>
    <span class="tool-status">{{ status }}</span>
  </div>
  <div class="grid2">
    <div class="card">
      <div class="tt-board" :class="{ busy: busy && !over }">
        <div v-for="(c, i) in board" :key="i" class="tt-cell" :class="[c === 'X' ? 'x' : c === 'O' ? 'o' : '', { taken: !!c }]" @click="onCell(i)">
          <AqIcon v-if="c === 'X'" name="x-mark" :size="26" />
          <AqIcon v-else-if="c === 'O'" name="circle-mark" :size="26" />
        </div>
      </div>
    </div>
    <div class="card out-pane">
      <template v-if="resultMsg">
        <div class="row between"><b>对局结束</b></div>
        <div class="tool-prose">{{ resultMsg }}</div>
      </template>
      <div v-else class="out-empty">
        <AqIcon name="gamepad" :size="26" />
        <span>{{ status }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.tt-board { display: grid; grid-template-columns: repeat(3, 1fr); gap: 7px; }
.tt-cell { aspect-ratio: 1; border: 1px solid var(--line); border-radius: var(--r-md); background: var(--bg1); display: flex; align-items: center; justify-content: center; cursor: pointer; transition: border-color var(--t-fast), background var(--t-fast); }
.tt-cell:hover { border-color: var(--acc); }
.tt-cell.taken { cursor: default; }
.tt-cell.x { color: var(--acc); }
.tt-cell.o { color: var(--acc-2); }
.tt-board.busy .tt-cell { pointer-events: none; opacity: .65; }
</style>
