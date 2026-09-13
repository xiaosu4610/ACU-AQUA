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

/* ---- 五子棋对弈 9×9（评分引擎 / LLM 落子可切换） ---- */
const N = 9
const mode = ref('algo')
const board = ref<number[]>([])
const over = ref(false)
const busy = ref(false) // AI 回合锁：思考中禁止落子
let gen = 0             // 对局代际：重开后旧 AI 回调作废
const status = ref('轮到你了（黑棋）')
const resultMsg = ref('')

function reset() {
  board.value = []
  for (let i = 0; i < N * N; i++) board.value.push(0)
  over.value = false
}
function idx(r: number, c: number) { return r * N + c }
function checkWin(p: number): boolean {
  const dirs = [[0, 1], [1, 0], [1, 1], [1, -1]]
  for (let r = 0; r < N; r++) for (let c = 0; c < N; c++) {
    if (board.value[idx(r, c)] !== p) continue
    for (let d = 0; d < 4; d++) {
      let cnt = 1
      for (let k = 1; k < 5; k++) {
        const rr = r + dirs[d][0] * k, cc = c + dirs[d][1] * k
        if (rr < 0 || rr >= N || cc < 0 || cc >= N || board.value[idx(rr, cc)] !== p) break
        cnt++
      }
      if (cnt >= 5) return true
    }
  }
  return false
}
function lineScore(b: number[], r: number, c: number, dr: number, dc: number, p: number): number {
  let mine = 0, openEnds = 0
  for (let k = 1; k < 5; k++) {
    const rr = r + dr * k, cc = c + dc * k
    if (rr < 0 || rr >= N || cc < 0 || cc >= N) break
    if (b[idx(rr, cc)] === p) mine++
    else { if (b[idx(rr, cc)] === 0) openEnds++; break }
    if (k === 4) { const er = r + dr * 5, ec = c + dc * 5; if (er >= 0 && er < N && ec >= 0 && ec < N && b[idx(er, ec)] === 0) openEnds++ }
  }
  if (mine >= 4) return 100000
  if (mine === 3) return openEnds === 2 ? 5000 : (openEnds === 1 ? 500 : 0)
  if (mine === 2) return openEnds === 2 ? 200 : (openEnds === 1 ? 20 : 0)
  if (mine === 1) return openEnds === 2 ? 20 : (openEnds === 1 ? 4 : 0)
  return 0
}
function evalPoint(b: number[], r: number, c: number, p: number): number {
  let s = 0
  const dirs = [[0, 1], [1, 0], [1, 1], [1, -1]]
  for (let d = 0; d < 4; d++) s += lineScore(b, r, c, dirs[d][0], dirs[d][1], p) + lineScore(b, r, c, dirs[d][0], dirs[d][1], 3 - p) * 0.9
  return s
}
function bestMoveAlgo(): number[] {
  let best: number[] = []
  let bestS = -1
  for (let r = 0; r < N; r++) for (let c = 0; c < N; c++) {
    if (board.value[idx(r, c)]) continue
    board.value[idx(r, c)] = 2
    const winNow = checkWin(2)
    board.value[idx(r, c)] = 0
    if (winNow) return [r, c]
    board.value[idx(r, c)] = 1
    const blockNow = checkWin(1)
    board.value[idx(r, c)] = 0
    if (blockNow) return [r, c]
    const s = evalPoint(board.value, r, c, 2)
    if (s > bestS) { bestS = s; best = [r, c] }
  }
  if (bestS <= 0) {
    const mid = idx(4, 4)
    if (!board.value[mid]) return [4, 4]
    for (let i = 0; i < N * N; i++) if (!board.value[i]) return [Math.floor(i / N), i % N]
  }
  return best
}
function finish(msg: string) {
  over.value = true
  busy.value = false
  status.value = msg
  resultMsg.value = msg
}
function afterAiMove() {
  if (checkWin(2)) return finish('对手获胜')
  if (board.value.indexOf(0) === -1) return finish('平局') // 棋盘下满无人获胜，防止死局挂起
  status.value = '轮到你了（黑棋）'
}
function aiMoveAlgo() {
  const mv = bestMoveAlgo()
  board.value[idx(mv[0], mv[1])] = 2
  busy.value = false // 先释放锁再重绘，确保 .busy 视觉状态同步清除
  afterAiMove()
}
async function aiMoveLlm(myGen: number) {
  const model = llmModel.value
  const rows: string[] = []
  for (let r = 0; r < N; r++) {
    const line: string[] = []
    for (let c = 0; c < N; c++) line.push(board.value[idx(r, c)] === 0 ? '.' : board.value[idx(r, c)] === 1 ? 'X' : 'O')
    rows.push(line.join(' '))
  }
  status.value = 'LLM 思考中…'
  try {
    const out = await apiChat([
      { role: 'system', content: '你在下五子棋（9×9），执白棋 O，人类执黑棋 X。坐标格式为「行,列」（0-8）。只输出一个坐标如 4,4，不要输出其他内容。优先连五取胜，其次阻挡黑棋四连/活三。' },
      { role: 'user', content: '当前棋盘（. 为空）：\n' + rows.join('\n') + '\n轮到你（O）落子，输出坐标。' },
    ], { model, temperature: 0.2, max_tokens: 30 })
    if (myGen !== gen) return // 思考期间重开了对局，丢弃过期回调
    let mv: number[] | null = null
    if (out) {
      const m = String(out).match(/(\d)\D+(\d)/)
      if (m) { const rr = +m[1], cc = +m[2]; if (rr < N && cc < N && !board.value[idx(rr, cc)]) mv = [rr, cc] }
    }
    if (!mv) mv = bestMoveAlgo()
    board.value[idx(mv[0], mv[1])] = 2
    busy.value = false // 先释放锁再重绘，确保 .busy 视觉状态同步清除
    afterAiMove()
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
  board.value[i] = 1
  if (checkWin(1)) return finish('你赢了！')
  if (board.value.indexOf(0) === -1) return finish('平局') // 棋盘下满无人获胜，防止死局挂起
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
  reset()
  resultMsg.value = ''
  status.value = '轮到你了（黑棋）'
}
reset()
</script>

<template>
  <p class="tool-intro">9×9 棋盘，五连即胜。你是黑棋先手。「算法」对手基于活/冲棋型评分即时应对；「LLM 落子」模式把棋盘交给大模型解析（解析失败自动回退算法）。</p>
  <div class="tool-bar">
    <span>对手</span>
    <select v-model="mode" class="select">
      <option value="algo">评分算法</option>
      <option value="llm">LLM 落子</option>
    </select>
    <select v-show="mode === 'llm'" v-model="llmModel" class="select">
      <option v-for="m in chatOpts" :key="m" :value="m">{{ m }}</option>
    </select>
    <button class="btn" @click="restart"><AqIcon name="refresh" :size="14" />重新开始</button>
    <span class="tool-status">{{ status }}</span>
  </div>
  <div class="grid2">
    <div class="card">
      <div class="gk-board" :class="{ busy: busy && !over }">
        <div v-for="(v, i) in board" :key="i" class="gk-cell" :class="{ taken: !!v }" @click="onCell(i)">
          <span v-if="v === 1" class="gk-black"></span><span v-else-if="v === 2" class="gk-white"></span>
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
.gk-board { display: grid; grid-template-columns: repeat(9, 1fr); gap: 3px; }
.gk-cell { aspect-ratio: 1; border: 1px solid var(--line); border-radius: 4px; background: var(--bg1); display: flex; align-items: center; justify-content: center; cursor: pointer; transition: border-color var(--t-fast); }
.gk-cell:hover { border-color: var(--acc); }
.gk-cell.taken { cursor: default; }
.gk-board.busy .gk-cell { pointer-events: none; opacity: .8; }
.gk-black, .gk-white { width: 72%; height: 72%; border-radius: 99px; display: inline-block; }
.gk-black { background: var(--acc-grad); }
.gk-white { background: var(--txt2); }
</style>
