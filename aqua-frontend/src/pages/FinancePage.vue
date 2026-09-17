<script setup lang="ts">
/* 财务管理中心：余额 + 消费统计（今日/7日/累计）+ 按模型消费 Top + 消费流水 + 充值记录 */
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import AqIcon from '@/components/AqIcon.vue'
import { errText } from '@/composables/useApi'
import { fetchFinance, isLoggedIn, loadMe, me, type FinanceData } from '@/composables/useAuth'

const router = useRouter()
const data = ref<FinanceData | null>(null)
const loading = ref(true)
const errMsg = ref('')

onMounted(async () => {
  if (!isLoggedIn()) {
    router.push('/login?redirect=/finance')
    return
  }
  if (!me.value) await loadMe().catch(() => {})
  await load()
})

async function load() {
  loading.value = true
  errMsg.value = ''
  try {
    data.value = await fetchFinance()
  } catch (e) { errMsg.value = errText(e) }
  loading.value = false
}

/** 微元 → 元字符串 */
function yuan(micro?: number): string {
  if (micro == null) return '--'
  return (micro / 1e6).toFixed(2)
}
function fmtTime(ts?: number): string {
  if (!ts) return '--'
  return new Date(ts * 1000).toLocaleString()
}
function topupStatus(s: string): string {
  if (s === 'paid') return '已到账'
  if (s === 'pending') return '待支付'
  if (s === 'expired' || s === 'closed') return '已关闭'
  return s
}

/* 按模型消费条形：宽度 = 占比 */
const modelMax = computed(() => data.value?.by_model.reduce((m, x) => Math.max(m, x.amount_micro), 0) || 0)
function barW(amount: number): number {
  if (!modelMax.value) return 0
  return Math.max(3, Math.round((amount / modelMax.value) * 100))
}
</script>

<template>
  <div class="wrap" style="max-width: 1080px;">
    <div class="fade-up">
      <!-- 页头 -->
      <div class="page-head">
        <div>
          <h1><AqIcon name="coin" :size="24" />财务管理中心</h1>
          <div class="sub">余额、消费统计、按模型账单与充值记录的统一入口。先付后用、失败全额退回。</div>
        </div>
        <div class="ops">
          <router-link class="btn primary" to="/console?view=topup"><AqIcon name="spark" :size="14" /> 去充值</router-link>
        </div>
      </div>

      <!-- 加载态 -->
      <template v-if="loading">
        <div class="kpis">
          <div v-for="i in 4" :key="i" class="kpi"><span><span class="skeleton" style="min-width: 72px; display: inline-block;"></span></span><div class="skeleton mt8" style="min-height: 26px; width: 60%;"></div></div>
        </div>
        <div class="card mt16"><div class="skeleton" style="min-height: 200px;"></div></div>
      </template>

      <!-- 错误态 -->
      <div v-else-if="errMsg" class="empty">
        <div class="big"><AqIcon name="alert" :size="36" /></div>
        <b>加载失败</b>
        <div class="dim">{{ errMsg }}</div>
        <button class="btn sm mt12" @click="load"><AqIcon name="refresh" :size="13" /> 重试</button>
      </div>

      <!-- 数据态 -->
      <template v-else-if="data">
        <!-- 余额 + 汇总 KPI -->
        <div class="kpis">
          <div class="kpi">
            <span>当前余额</span>
            <b class="grad-text">¥{{ yuan(data.balance_micro) }}</b>
            <span class="trend">收费模型预充值 · 先付后用</span>
          </div>
          <div class="kpi">
            <span>今日消费</span>
            <b>¥{{ yuan(data.spend_today) }}</b>
            <span class="trend">按服务器自然日</span>
          </div>
          <div class="kpi">
            <span>近 7 日消费</span>
            <b>¥{{ yuan(data.spend_week) }}</b>
            <span class="trend">成功扣费合计</span>
          </div>
          <div class="kpi">
            <span>累计消费</span>
            <b>¥{{ yuan(data.spend_total) }}</b>
            <span class="trend">全部历史合计</span>
          </div>
        </div>

        <!-- 按模型消费 Top -->
        <div class="card mt16">
          <b><AqIcon name="chart" :size="16" /> 近 30 天按模型消费 <span class="dim">Top 10</span></b>
          <div v-if="!data.by_model.length" class="empty" style="padding: 24px 0;">
            <b>暂无消费记录</b>
            <div class="dim">使用众筹模型（acu/ 前缀，按次计费）后这里会出现统计</div>
          </div>
          <div v-else class="bar-list mt12">
            <div v-for="m in data.by_model" :key="m.model" class="bar-row">
              <span class="bar-name" :title="m.model">{{ m.model }}</span>
              <span class="bar-track"><span class="bar-fill" :style="{ width: barW(m.amount_micro) + '%' }"></span></span>
              <span class="bar-val num">¥{{ yuan(m.amount_micro) }}<i class="dim"> · {{ m.calls }} 次</i></span>
            </div>
          </div>
        </div>

        <!-- 最近消费流水 -->
        <div class="card mt16">
          <b><AqIcon name="bolt" :size="16" /> 最近消费流水 <span class="dim">20 条</span></b>
          <div v-if="!data.recent.length" class="empty" style="padding: 24px 0;">
            <b>暂无消费记录</b>
            <div class="dim">失败请求会自动全额退回，不产生流水</div>
          </div>
          <div v-else class="tbl-wrap mt12">
            <table class="table">
              <thead><tr><th>模型</th><th class="num">金额</th><th>结果</th><th>时间</th></tr></thead>
              <tbody>
                <tr v-for="(f, i) in data.recent" :key="i">
                  <td><code>{{ f.model }}</code></td>
                  <td class="num">¥{{ yuan(f.amount_micro) }}</td>
                  <td><span class="tag" :class="f.ok ? 'ok' : 'bad'">{{ f.ok ? '成功' : '已退回' }}</span></td>
                  <td class="dim">{{ fmtTime(f.ts) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <!-- 最近充值记录 -->
        <div class="card mt16">
          <b><AqIcon name="wallet" :size="16" /> 最近充值记录 <span class="dim">10 条</span></b>
          <div v-if="!data.topups.length" class="empty" style="padding: 24px 0;">
            <b>暂无充值记录</b>
            <div class="dim">去 <router-link to="/console?view=topup">控制台充值</router-link>，支付金额 100% 全额到账</div>
          </div>
          <div v-else class="tbl-wrap mt12">
            <table class="table">
              <thead><tr><th class="num">金额</th><th>状态</th><th>渠道</th><th>创建时间</th><th>到账时间</th></tr></thead>
              <tbody>
                <tr v-for="(t, i) in data.topups" :key="i">
                  <td class="num">¥{{ yuan(t.amount_micro) }}</td>
                  <td><span class="tag" :class="t.status === 'paid' ? 'ok' : ''">{{ topupStatus(t.status) }}</span></td>
                  <td>{{ t.channel || '--' }}</td>
                  <td class="dim">{{ fmtTime(t.created_ts) }}</td>
                  <td class="dim">{{ t.paid_ts ? fmtTime(t.paid_ts) : '--' }}</td>
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
.bar-list { display: flex; flex-direction: column; gap: 10px; }
.bar-row { display: grid; grid-template-columns: minmax(120px, 220px) 1fr auto; align-items: center; gap: 12px; }
.bar-name { font-family: var(--mono); font-size: 12.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.bar-track { height: 12px; border-radius: 99px; background: var(--bg3); overflow: hidden; }
.bar-fill { display: block; height: 100%; border-radius: 99px; background: var(--acc-grad); transition: width var(--t-med); }
.bar-val { font-size: 13px; white-space: nowrap; }
.bar-val i { font-style: normal; }
@media (max-width: 640px) {
  .bar-row { grid-template-columns: 1fr auto; }
  .bar-track { order: 3; grid-column: 1 / -1; }
}
</style>
