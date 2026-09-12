<script setup lang="ts">
/* 我的用量：登录态自动加载（/my/usage，按账号归集）；未登录显示引导 */
import { onMounted, onUnmounted, ref } from 'vue'
import { apiJson, errText, fmt } from '@/composables/useApi'
import { isLoggedIn } from '@/composables/useAuth'
import AqIcon from '@/components/AqIcon.vue'

interface ModelRow { model: string; width: number; val: string }
interface RecentRow { ok: boolean; endpoint: string; model: string; time: string; ms: number }

const cards = ref({ today: '--', todayRate: '--', week: '--', weekRate: '--' })
const modelRows = ref<ModelRow[]>([])
const recentRows = ref<RecentRow[]>([])
const msg = ref('')
const loaded = ref(false)

let aborter: AbortController | null = null

async function loadUsage() {
  if (aborter) aborter.abort()
  const ac = new AbortController()
  aborter = ac
  try {
    const j = await apiJson<any>('/my/usage', { session: true, signal: ac.signal })
    if (ac.signal.aborted) return
    cards.value = {
      today: fmt(j.today.calls),
      todayRate: j.today.ok_rate + '%',
      week: fmt(j.week.calls),
      weekRate: j.week.ok_rate + '%',
    }
    const byModel = j.by_model || []
    if (!byModel.length) {
      modelRows.value = []
      msg.value = '近 7 天还没有调用记录——拿你的密钥去 Playground 聊一句再来刷新'
    } else {
      const maxc = byModel[0].calls || 1
      modelRows.value = byModel.map((m: any) => ({
        model: m.model,
        width: Math.max(4, Math.round((m.calls * 100) / maxc)),
        val: fmt(m.calls) + ' 次',
      }))
    }
    recentRows.value = (j.recent || []).map((r: any) => ({
      ok: !!r.ok,
      endpoint: r.endpoint,
      model: r.model || '',
      time: new Date(r.ts * 1000).toLocaleString(),
      ms: r.latency_ms,
    }))
    loaded.value = true
  } catch (e) {
    if (ac.signal.aborted) return
    msg.value = '查询失败：' + errText(e)
  }
}

onMounted(() => {
  if (isLoggedIn()) void loadUsage()
})
onUnmounted(() => {
  if (aborter) { aborter.abort(); aborter = null }
})
</script>

<template>
  <section class="route-page">
    <div class="dash-wrap">
      <div class="dash-head">
        <h1><span class="ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.21 15.89A10 10 0 1 1 8 2.83"/><path d="M22 12A10 10 0 0 0 12 2v10z"/></svg></span>我的用量</h1>
        <p>已登录账号的调用统计——今日 / 近 7 天调用量与成功率、模型分布、最近调用。用量明细保留 <b>7 天</b>，自动清理。</p>
      </div>

      <!-- 未登录引导 -->
      <div v-if="!isLoggedIn()" class="dash-sec usage-guest">
        <b>登录后查看</b>
        <p class="usage-guest-desc">用量按账号归集：注册登录后，你在控制台创建的所有密钥的调用记录都会汇总在这里。</p>
        <router-link class="btn tool-run usage-guest-btn" to="/login?redirect=/usage">登录 / 注册</router-link>
      </div>

      <!-- 已登录 -->
      <template v-else>
        <div class="dash-cards">
          <div class="dash-card"><b id="us-today">{{ cards.today }}</b><span>今日调用</span></div>
          <div class="dash-card"><b id="us-today-rate">{{ cards.todayRate }}</b><span>今日成功率</span></div>
          <div class="dash-card"><b id="us-week">{{ cards.week }}</b><span>近 7 天调用</span></div>
          <div class="dash-card"><b id="us-week-rate">{{ cards.weekRate }}</b><span>近 7 天成功率</span></div>
        </div>
        <div class="dash-sec">
          <b><AqIcon name="puzzle" :size="16" /> 你的模型分布 <span style="font-size:11.5px;color:var(--muted);font-weight:400;">（近 7 天 · Top 10）</span></b>
          <div id="us-models">
            <div v-if="!modelRows.length" class="dash-empty">{{ loaded ? msg : '加载中…' }}</div>
            <div v-for="(m, i) in modelRows" :key="i" class="stat-row">
              <span class="nm" :title="m.model">{{ m.model }}</span>
              <span class="trk"><span class="fil" :style="{ width: m.width + '%' }"></span></span>
              <span class="val">{{ m.val }}</span>
            </div>
          </div>
        </div>
        <div class="dash-sec">
          <b><AqIcon name="clock" :size="16" /> 最近调用 <span style="font-size:11.5px;color:var(--muted);font-weight:400;">（最多 50 条）</span></b>
          <div id="us-recent">
            <div v-if="!recentRows.length" class="dash-empty">{{ loaded ? '暂无最近调用' : '加载中…' }}</div>
            <div v-for="(r, i) in recentRows" :key="i" class="lb-row">
              <span class="rk" :style="r.ok ? { background: 'rgba(52,211,153,.15)', color: '#34d399' } : { background: 'rgba(248,113,113,.15)', color: '#f87171' }">{{ r.ok ? 'OK' : 'ERR' }}</span>
              <span class="md">{{ r.endpoint }}{{ r.model ? ' · ' + r.model : '' }}</span>
              <span class="rec">{{ r.time }} · {{ r.ms }}ms</span>
            </div>
          </div>
        </div>
      </template>
    </div>
  </section>
</template>

<style scoped>
.usage-guest { display: flex; flex-direction: column; gap: 8px; align-items: flex-start; }
.usage-guest-desc { font-size: 13.5px; color: var(--muted, #8a94a6); margin: 0; }
.usage-guest-btn { text-decoration: none; padding: 10px 22px; }
</style>
