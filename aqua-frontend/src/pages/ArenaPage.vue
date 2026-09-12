<script setup lang="ts">
/* 模型竞技场：自旧版 arenaInit/loadArenaLeaderboard 平移（盲测对比 + 投票 + 排行榜）
   说明：网关 /v1/arena 为一次性 JSON（服务端并行调双模型、隐藏身份、非流式，
   streamChat 只能打 /chat/completions 且需显式指定模型，无法产生服务端盲选对决），
   故对决答案揭示采用双路并行打字机流（onDelta 与 streamChat 同契约，分别写入选手 A/B 区域）；
   投票/揭晓/排行榜端点与鉴权（走 useApi 默认密钥链）完全照旧版。 */
import { onMounted, onUnmounted, ref } from 'vue'
import { apiJson, errText } from '@/composables/useApi'
import AuthBanner from '@/components/AuthBanner.vue'
import AqIcon from '@/components/AqIcon.vue'

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

const revealError = ref(false)
const revealNote = ref('')
const revealA = ref('')
const revealB = ref('')
const revealErrMsg = ref('')

const lbRows = ref<LbRow[]>([])
const lbMsg = ref('加载中…')

let battleId = ''
let revealTimer: ReturnType<typeof setInterval> | null = null
let aborter: AbortController | null = null

/* 双路并行打字机流：把两份回答按增量并行写入选手 A/B 区域（onDelta 契约） */
function stopReveal() {
  if (revealTimer) { clearInterval(revealTimer); revealTimer = null }
}

function streamAnswers(fa: string, fb: string) {
  stopReveal()
  ansA.value = ''
  ansB.value = ''
  const onDeltaA = (t: string) => { ansA.value += t }
  const onDeltaB = (t: string) => { ansB.value += t }
  let i = 0
  revealTimer = setInterval(() => {
    if (i >= fa.length && i >= fb.length) { stopReveal(); return }
    if (i < fa.length) onDeltaA(fa.slice(i, i + 4))
    if (i < fb.length) onDeltaB(fb.slice(i, i + 4))
    i += 4
  }, 24)
}

/* arRun 平移：POST /arena 发起盲测对决 */
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
    const j = await apiJson<any>('/arena', {
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

/* ar-vote-* 平移：POST /arena/vote 投票并揭晓身份 */
async function vote(w: 'a' | 'tie' | 'b') {
  if (!battleId) return
  try {
    const j = await apiJson<any>('/arena/vote', {
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

/* loadArenaLeaderboard 平移：GET /arena/leaderboard 全站胜率排行榜 */
async function loadLeaderboard() {
  try {
    const j = await apiJson<any>('/arena/leaderboard')
    const list = j.ranking || []
    if (!list.length) {
      lbRows.value = []
      lbMsg.value = '还没有投票数据——来 Arena 打第一场，写下历史第一票！'
      return
    }
    lbRows.value = list.map((r: any) => ({
      model: r.model,
      wr: r.win_rate + '%',
      rec: r.wins + '胜 ' + r.losses + '负 ' + r.ties + '平 · ' + r.battles + '场',
    }))
  } catch {
    lbRows.value = []
    lbMsg.value = '排行榜加载失败，稍后自动重试'
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
  <section class="route-page">
    <div class="dash-wrap">
      <div class="dash-head">
        <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 2 3 6v14a2 2 0 0 0 2 2h4a2 2 0 0 0 2-2V6L8 2z"/><path d="M4 6h16"/><path d="M18 2l3 4v14a2 2 0 0 1-2 2h-4a2 2 0 0 1-2-2V6l3-4z"/></svg></span>模型竞技场</h1>
        <p>同一个问题，两位神秘选手同时作答——<b>盲测对比后投票，才能揭晓真实身份</b>。你的每一票都会计入全站胜率排行榜，帮后来者选出最好用的免费模型。</p>
      </div>
      <AuthBanner />
      <div class="dash-sec">
        <b>出题</b>
        <div class="tool-io">
          <textarea id="ar-prompt" rows="3" placeholder="向两位选手提同一个问题，例如：用一句话解释什么是量子纠缠 / 帮我写一句春联 / 翻译这段英文…" v-model="prompt"></textarea>
          <div class="tool-bar">
            <span>回答长度</span>
            <select id="ar-maxtok" style="background:var(--card2);color:var(--text);border:1px solid var(--border);border-radius:8px;padding:7px 10px;" v-model="maxtok">
              <option value="256">短（256 tokens）</option>
              <option value="512">中（512 tokens）</option>
              <option value="1024">长（1024 tokens）</option>
            </select>
            <button class="btn tool-run" id="ar-run" @click="startArena">开始对决</button>
            <span class="tool-status" id="ar-hint">{{ hint }}</span>
          </div>
        </div>
        <div id="ar-stage" class="arena-stage" v-show="stageVisible">
          <div class="arena-box"><span class="ab-tag">选手 A</span><div class="ab-text" id="ar-a">{{ ansA }}</div><div class="ab-meta" id="ar-ameta">{{ metaA }}</div></div>
          <div class="arena-box"><span class="ab-tag">选手 B</span><div class="ab-text" id="ar-b">{{ ansB }}</div><div class="ab-meta" id="ar-bmeta">{{ metaB }}</div></div>
        </div>
        <div class="arena-vs" id="ar-vs" v-show="stageVisible">— WHO WINS? —</div>
        <div class="arena-votes" id="ar-votes" v-show="votesVisible">
          <button class="btn" id="ar-vote-a" @click="vote('a')">A 更好 <AqIcon name="hand-left" :size="14" /></button>
          <button class="btn" id="ar-vote-tie" @click="vote('tie')">平局 <AqIcon name="handshake" :size="14" /></button>
          <button class="btn" id="ar-vote-b" @click="vote('b')">B 更好 <AqIcon name="hand-right" :size="14" /></button>
        </div>
        <div class="arena-reveal" id="ar-reveal" v-show="revealVisible">
          <template v-if="!revealError"><AqIcon name="vote" :size="14" /> {{ revealNote }} 身份揭晓：选手 A = <b>{{ revealA }}</b> · 选手 B = <b>{{ revealB }}</b>
            <div style="margin-top:8px;"><button class="btn" @click="startArena">再来一场 →</button></div>
          </template>
          <template v-else>投票失败：{{ revealErrMsg }}</template>
        </div>
      </div>
      <div class="dash-sec">
        <b><AqIcon name="trophy" :size="16" /> 全站胜率排行榜 <span style="font-size:11.5px;color:var(--muted);font-weight:400;">胜 1 分 / 平 0.5 分 / 负 0 分</span></b>
        <div id="ar-lb">
          <div v-if="!lbRows.length" class="dash-empty">{{ lbMsg }}</div>
          <div v-for="(r, i) in lbRows" :key="i" class="lb-row">
            <span class="rk">{{ i + 1 }}</span>
            <span class="md">{{ r.model }}</span>
            <span class="wr">{{ r.wr }}</span>
            <span class="rec">{{ r.rec }}</span>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
