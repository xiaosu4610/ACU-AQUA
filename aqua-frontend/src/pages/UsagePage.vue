<script setup lang="ts">
/* 我的用量：登录态自动加载（/my/usage，按账号归集）；未登录显示引导 */
import { onMounted, onUnmounted, ref } from 'vue'
import AqIcon from '@/components/AqIcon.vue'
import { apiJson, errText, fmt } from '@/composables/useApi'
import { isLoggedIn } from '@/composables/useAuth'

interface UsageResp {
  today: { calls: number; ok_rate: number }
  week: { calls: number; ok_rate: number }
  by_model?: { model: string; calls: number }[]
  recent?: { ok: boolean; endpoint: string; model: string; ts: number; latency_ms: number; bill_amount_micro?: number }[]
}
interface ModelRow { model: string; width: number; val: string }
interface RecentRow { ok: boolean; endpoint: string; model: string; time: string; ms: number; amount: number | null }

const cards = ref({ today: '--', todayRate: '--', week: '--', weekRate: '--' })

/** 微元 → 元（去掉尾零，最多 5 位小数） */
function microYuan(m: number): string {
  return (m / 1e6).toFixed(5).replace(/0+$/, '').replace(/\.$/, '')
}
const modelRows = ref<ModelRow[]>([])
const recentRows = ref<RecentRow[]>([])
const msg = ref('')
const loaded = ref(false)
const loading = ref(true)

let aborter: AbortController | null = null

async function loadUsage() {
  if (aborter) aborter.abort()
  const ac = new AbortController()
  aborter = ac
  try {
    const j = await apiJson<UsageResp>('/my/usage', { session: true, signal: ac.signal })
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
      modelRows.value = byModel.map((m) => ({
        model: m.model,
        width: Math.max(4, Math.round((m.calls * 100) / maxc)),
        val: fmt(m.calls) + ' 次',
      }))
    }
    recentRows.value = (j.recent || []).map((r) => ({
      ok: !!r.ok,
      endpoint: r.endpoint,
      model: r.model || '',
      time: new Date(r.ts * 1000).toLocaleString(),
      ms: r.latency_ms,
      amount: r.bill_amount_micro && r.bill_amount_micro > 0 ? r.bill_amount_micro : null,
    }))
    loaded.value = true
  } catch (e) {
    if (ac.signal.aborted) return
    msg.value = '查询失败：' + errText(e)
  }
  loading.value = false
}

onMounted(() => {
  if (isLoggedIn()) void loadUsage()
  else loading.value = false
})
onUnmounted(() => {
  if (aborter) { aborter.abort(); aborter = null }
})
</script>

<template>
  <div class="wrap" style="max-width: 1080px;">
    <div class="fade-up">
      <!-- 页头 -->
      <div class="page-head">
        <div>
          <h1><AqIcon name="chart" :size="24" />我的用量</h1>
          <div class="sub">已登录账号的调用统计——今日 / 近 7 天调用量与成功率、模型分布、最近调用。用量明细保留 <b>7 天</b>，自动清理。</div>
        </div>
      </div>

      <!-- 未登录引导 -->
      <div v-if="!isLoggedIn()" class="card accent guest">
        <b><AqIcon name="lock" :size="16" /> 登录后查看</b>
        <p class="dim mt8">用量按账号归集：注册登录后，你在控制台创建的所有密钥的调用记录都会汇总在这里。</p>
        <router-link class="btn primary mt12" to="/login?redirect=/usage">登录 / 注册</router-link>
      </div>

      <!-- 已登录 -->
      <template v-else>
        <!-- 错误态 -->
        <p v-if="msg && !loaded" class="msg bad">{{ msg }}</p>

        <!-- KPI 行 -->
        <div class="kpis">
          <div class="kpi">
            <span>今日调用</span>
            <div v-if="loading" class="skeleton" style="min-height: 26px; width: 64px; margin-top: 4px;"></div>
            <b v-else>{{ cards.today }}</b>
            <span class="trend">今日成功率 {{ cards.todayRate }}</span>
          </div>
          <div class="kpi">
            <span>今日成功率</span>
            <div v-if="loading" class="skeleton" style="min-height: 26px; width: 64px; margin-top: 4px;"></div>
            <b v-else>{{ cards.todayRate }}</b>
            <span class="trend">成功请求 / 全部请求</span>
          </div>
          <div class="kpi">
            <span>近 7 天调用</span>
            <div v-if="loading" class="skeleton" style="min-height: 26px; width: 64px; margin-top: 4px;"></div>
            <b v-else>{{ cards.week }}</b>
            <span class="trend">近 7 天成功率 {{ cards.weekRate }}</span>
          </div>
          <div class="kpi">
            <span>近 7 天成功率</span>
            <div v-if="loading" class="skeleton" style="min-height: 26px; width: 64px; margin-top: 4px;"></div>
            <b v-else>{{ cards.weekRate }}</b>
            <span class="trend">按天汇总</span>
          </div>
        </div>

        <!-- 模型分布（横向条形） -->
        <div class="card mt16">
          <b><AqIcon name="puzzle" :size="16" /> 你的模型分布 <span class="dim">（近 7 天 · Top 10）</span></b>
          <p v-if="msg && loaded" class="msg bad mt12">{{ msg }}</p>
          <div v-else-if="!modelRows.length" class="empty" style="padding: 28px 0;">
            <b>{{ loaded ? '暂无调用记录' : '加载中…' }}</b>
            <div v-if="loaded" class="dim">{{ msg }}</div>
          </div>
          <div v-else class="bar-list mt12">
            <div v-for="(m, i) in modelRows" :key="i" class="bar-row">
              <span class="bar-name" :title="m.model">{{ m.model }}</span>
              <span class="bar-track"><span class="bar-fill" :style="{ width: m.width + '%' }"></span></span>
              <span class="bar-val num">{{ m.val }}</span>
            </div>
          </div>
        </div>

        <!-- 最近调用 -->
        <div class="card mt16">
          <b><AqIcon name="clock" :size="16" /> 最近调用 <span class="dim">（最多 50 条）</span></b>
          <div v-if="!recentRows.length" class="empty" style="padding: 28px 0;">
            <b>{{ loaded ? '暂无最近调用' : '加载中…' }}</b>
          </div>
          <div v-else class="tbl-wrap mt12">
            <table class="table">
              <thead><tr><th>状态</th><th>端点</th><th>模型</th><th>时间</th><th class="num">延迟</th><th class="num">本次扣费</th></tr></thead>
              <tbody>
                <tr v-for="(r, i) in recentRows" :key="i">
                  <td><span class="tag" :class="r.ok ? 'ok' : 'bad'">{{ r.ok ? 'OK' : 'ERR' }}</span></td>
                  <td class="nowrap">{{ r.endpoint }}</td>
                  <td><span class="cell-clip" :title="r.model">{{ r.model || '—' }}</span></td>
                  <td class="dim nowrap">{{ r.time }}</td>
                  <td class="num">{{ r.ms }}ms</td>
                  <td class="num">
                    <template v-if="r.amount != null">
                      <b>¥{{ microYuan(r.amount) }}</b>
                      <span v-if="r.model.startsWith('aqua/')" class="dim"> · 按次</span><span v-else-if="r.model.startsWith('codex/')" class="dim"> · 按量</span>
                    </template>
                    <template v-else><span class="dim">免费</span></template>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
/* 布局微调：条形图行（高度 / 间距 / 截断） */
.guest { max-width: 560px; }
.bar-list { display: flex; flex-direction: column; gap: 10px; }
.bar-row { display: grid; grid-template-columns: minmax(120px, 220px) 1fr auto; align-items: center; gap: 12px; }
.bar-name { font-family: var(--mono); font-size: 12.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.bar-track { height: 12px; border-radius: 99px; background: var(--bg3); overflow: hidden; }
.bar-fill { display: block; height: 100%; border-radius: 99px; background: var(--acc-grad); transition: width var(--t-med); }
.bar-val { font-size: 13px; white-space: nowrap; }
.nowrap { white-space: nowrap; }
.cell-clip { display: inline-block; max-width: 200px; overflow: hidden; text-overflow: ellipsis; vertical-align: bottom; }
@media (max-width: 640px) {
  .bar-row { grid-template-columns: 1fr auto; }
  .bar-track { order: 3; grid-column: 1 / -1; }
}
</style>
