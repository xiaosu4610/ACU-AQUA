<script setup lang="ts">
/* 模型竞技场：接口对接 1:1 平移自旧版（盲测对比 + 投票 + 排行榜）
 *   POST /arena         发起盲测对决（服务端并行调双模型、隐藏身份、一次性 JSON 非流式）
 *   POST /arena/vote    投票并揭晓身份
 *   GET  /arena/leaderboard  全站胜率排行榜
 * 对决答案揭示采用双路并行打字机流（onDelta 与 streamChat 同契约，分别写入选手 A/B 区域）。 */
import { onMounted, onUnmounted, ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
import AqIcon from '@/components/AqIcon.vue'

interface ArenaSide { ok: boolean; content?: string; status?: number | string; latency_ms: number }
interface ArenaResp { battle_id?: string; a: ArenaSide; b: ArenaSide }
interface VoteResp { counted?: boolean; msg?: string; revealed: { a: string; b: string } }
interface LbItem { model: string; win_rate: number | string; wins: number; losses: number; ties: number; battles: number }
interface LbRow { model: string; wr: string; rec: string }

const prompt = ref('')
const maxtok = ref('512')
const hint = ref('同一问题 · 双模型并行 · 盲测匿名')

const stageVisible = ref(false)
const votesVisible = ref(false)
const revealVisible = ref(false)
const ansA = ref('')
const ansB = ref('')
const metaA = ref('')
const metaB = ref('')
const typing = ref(false)

const revealError = ref(false)
const revealNote = ref('')
const revealA = ref('')
const revealB = ref('')
const revealErrMsg = ref('')

const lbRows = ref<LbRow[]>([])
const lbMsg = ref('加载中…')
const lbLoading = ref(true)

let battleId = ''
let revealTimer: ReturnType<typeof setInterval> | null = null
let aborter: AbortController | null = null

/* 双路并行打字机流：把两份回答按增量并行写入选手 A/B 区域（onDelta 契约） */
function stopReveal() {
  if (revealTimer) { clearInterval(revealTimer); revealTimer = null }
  typing.value = false
}

function streamAnswers(fa: string, fb: string) {
  stopReveal()
  ansA.value = ''
  ansB.value = ''
  typing.value = true
  let i = 0
  revealTimer = setInterval(() => {
    if (i >= fa.length && i >= fb.length) { stopReveal(); return }
    if (i < fa.length) ansA.value += fa.slice(i, i + 4)
    if (i < fb.length) ansB.value += fb.slice(i, i + 4)
    i += 4
  }, 24)
}

/* 发起盲测对决：POST /arena */
async function startArena() {
  const q = prompt.value.trim()
  if (!q) { hint.value = '请先输入问题'; return }
  stopReveal()
  battleId = ''
  hint.value = '两位选手思考中…（并行调用，最长约 1-2 分钟）'
  stageVisible.value = false
  votesVisible.value = false
  revealVisible.value = false
  if (aborter) aborter.abort()
  const ac = new AbortController()
  aborter = ac
  try {
    const j = await apiJson<ArenaResp>('/arena', {
      method: 'POST',
      body: { prompt: q, max_tokens: parseInt(maxtok.value, 10) || 512 },
      signal: ac.signal,
    })
    if (ac.signal.aborted) return
    battleId = j.battle_id || ''
    hint.value = '对比左右两份回答，投出你的一票（盲测：身份投票后揭晓）'
    stageVisible.value = true
    votesVisible.value = true
    const fa = j.a.ok ? (j.a.content || '（该选手没有返回内容）') : '（该选手掉线了：' + (j.a.status || '?') + '）'
    const fb = j.b.ok ? (j.b.content || '（该选手没有返回内容）') : '（该选手掉线了：' + (j.b.status || '?') + '）'
    metaA.value = '耗时 ' + (j.a.latency_ms / 1000).toFixed(1) + 's'
    metaB.value = '耗时 ' + (j.b.latency_ms / 1000).toFixed(1) + 's'
    streamAnswers(fa, fb)
  } catch (e) {
    if (ac.signal.aborted) return
    hint.value = '对决失败：' + errText(e)
  }
}

/* 投票并揭晓身份：POST /arena/vote */
async function vote(w: 'a' | 'tie' | 'b') {
  if (!battleId) return
  try {
    const j = await apiJson<VoteResp>('/arena/vote', {
      method: 'POST',
      body: { battle_id: battleId, winner: w },
    })
    revealError.value = false
    revealNote.value = j.counted ? '一票已计入！' : (j.msg || '该战场已投过票')
    revealA.value = j.revealed.a
    revealB.value = j.revealed.b
    votesVisible.value = false
    revealVisible.value = true
    loadLeaderboard()
  } catch (e) {
    revealError.value = true
    revealErrMsg.value = errText(e)
    revealVisible.value = true
  }
}

/* 全站胜率排行榜：GET /arena/leaderboard */
async function loadLeaderboard() {
  lbLoading.value = true
  try {
    const j = await apiJson<LbResp>('/arena/leaderboard')
    const list = j.ranking || []
    if (!list.length) {
      lbRows.value = []
      lbMsg.value = '还没有投票数据——来 Arena 打第一场，写下历史第一票！'
      return
    }
    lbRows.value = list.map(r => ({
      model: r.model,
      wr: r.win_rate + '%',
      rec: r.wins + '胜 ' + r.losses + '负 ' + r.ties + '平 · ' + r.battles + '场',
    }))
  } catch {
    lbRows.value = []
    lbMsg.value = '排行榜加载失败，稍后自动重试'
  } finally {
    lbLoading.value = false
  }
}

onMounted(() => {
  loadLeaderboard()
})

onUnmounted(() => {
  stopReveal()
  if (aborter) { aborter.abort(); aborter = null }
})
</script>

<template>
  <div class="wrap">
    <div class="page-head">
      <div>
        <h1><AqIcon name="gamepad" :size="22" />模型竞技场</h1>
        <div class="sub">同一个问题，两位神秘选手同时作答——盲测对比后投票，才能揭晓真实身份。你的每一票都会计入全站胜率排行榜，帮后来者选出最好用的免费模型。</div>
      </div>
    </div>

    <!-- 出题 -->
    <div class="card fade-up">
      <div class="field">
        <label>向两位选手提同一个问题</label>
        <textarea v-model="prompt" class="textarea" rows="3" placeholder="例如：用一句话解释什么是量子纠缠 / 帮我写一句春联 / 翻译这段英文…" />
      </div>
      <div class="row wrap mt12">
        <span class="dim">回答长度</span>
        <select v-model="maxtok" class="select ar-tok">
          <option value="256">短（256 tokens）</option>
          <option value="512">中（512 tokens）</option>
          <option value="1024">长（1024 tokens）</option>
        </select>
        <button class="btn primary" type="button" @click="startArena"><AqIcon name="bolt" :size="14" />开始对决</button>
        <span class="dim ar-hint">{{ hint }}</span>
      </div>
    </div>

    <!-- 对战区：左右双面板 -->
    <div v-show="stageVisible" class="grid2 mt16">
      <div class="card fade-up">
        <div class="row between mb12">
          <b><AqIcon name="hand-left" :size="16" />选手 A</b>
          <span v-if="metaA" class="tag">{{ metaA }}</span>
        </div>
        <div class="ar-ans">{{ ansA }}<span v-if="typing" class="stream-cur" /></div>
      </div>
      <div class="card fade-up">
        <div class="row between mb12">
          <b><AqIcon name="hand-right" :size="16" />选手 B</b>
          <span v-if="metaB" class="tag">{{ metaB }}</span>
        </div>
        <div class="ar-ans">{{ ansB }}<span v-if="typing" class="stream-cur" /></div>
      </div>
    </div>

    <!-- 盲测投票 -->
    <div v-show="votesVisible" class="card mt16 fade-up ar-votebar">
      <span class="dim ar-votecap"><AqIcon name="vote" :size="15" />盲测投票</span>
      <button class="btn" type="button" @click="vote('a')"><AqIcon name="hand-left" :size="14" />A 更好</button>
      <button class="btn" type="button" @click="vote('tie')"><AqIcon name="handshake" :size="14" />平局</button>
      <button class="btn" type="button" @click="vote('b')"><AqIcon name="hand-right" :size="14" />B 更好</button>
    </div>

    <!-- 揭晓 -->
    <div v-show="revealVisible" class="mt16">
      <div v-if="!revealError" class="banner fade-up">
        <AqIcon name="vote" :size="15" />
        <span>{{ revealNote }} 身份揭晓：选手 A = <b>{{ revealA }}</b> · 选手 B = <b>{{ revealB }}</b></span>
        <button class="btn sm ar-again" type="button" @click="startArena">再来一场 →</button>
      </div>
      <div v-else class="banner bad"><AqIcon name="alert" :size="15" />投票失败：{{ revealErrMsg }}</div>
    </div>

    <!-- 排行榜 -->
    <div class="card mt24">
      <b><AqIcon name="trophy" :size="16" />全站胜率排行榜 <span class="dim" style="font-size: 12px; font-weight: 400;">胜 1 分 / 平 0.5 分 / 负 0 分</span></b>
      <div v-if="lbLoading" class="mt12"><div class="skeleton" style="height: 140px;" /></div>
      <div v-else-if="!lbRows.length" class="empty">
        <div class="big"><AqIcon name="trophy" :size="34" /></div>
        <b>{{ lbMsg }}</b>
      </div>
      <div v-else class="tbl-wrap mt12">
        <table class="table">
          <thead>
            <tr><th>名次</th><th>模型</th><th class="num">胜率</th><th>战绩</th></tr>
          </thead>
          <tbody>
            <tr v-for="(r, i) in lbRows" :key="r.model">
              <td>
                <span v-if="i === 0" class="tag warn">🥇 冠军</span>
                <span v-else-if="i === 1" class="tag">🥈 亚军</span>
                <span v-else-if="i === 2" class="tag warn">🥉 季军</span>
                <span v-else class="dim num">{{ i + 1 }}</span>
              </td>
              <td class="mono">{{ r.model }}</td>
              <td class="num">{{ r.wr }}</td>
              <td class="dim">{{ r.rec }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="!lbLoading && lbRows.length === 0 && lbMsg.startsWith('排行榜加载失败')" class="mt12" style="text-align: center;">
        <button class="btn sm" type="button" @click="loadLeaderboard"><AqIcon name="refresh" :size="13" />重试</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 布局微调：答案区最小高度 / 投票条排布，颜色全部走设计令牌 */
.ar-tok { width: auto; }
.ar-hint { max-width: 46ch; }

.ar-ans { min-height: 120px; max-height: 320px; overflow-y: auto; white-space: pre-wrap; word-break: break-word; color: var(--txt1); font-size: 13.5px; line-height: 1.75; }

.ar-votebar { display: flex; align-items: center; justify-content: center; gap: 12px; flex-wrap: wrap; }
.ar-votecap { display: inline-flex; align-items: center; gap: 6px; font-weight: 600; }
.ar-again { margin-left: auto; }

.stream-cur { display: inline-block; width: 7px; height: 14px; margin-left: 3px; vertical-align: -2px; border-radius: 2px; background: var(--acc); animation: ar-blink 1s steps(2, start) infinite; }
@keyframes ar-blink { 50% { opacity: 0; } }
</style>
