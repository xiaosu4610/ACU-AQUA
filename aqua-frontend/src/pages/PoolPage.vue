<script setup lang="ts">
/* 众筹公共算力池（acu/）：资金池驾驶舱 + 众筹模型一览 + 充值翻倍注入 + 四大榜单 + 透明流水 + 个人注入记录
 * 铁律：本页零上游调用——只打本网关 /v1/pool/* /v1/pay/* /v1/my/pool/* 接口 */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'
import { apiJson, errText } from '@/composables/useApi'
import { isLoggedIn, sessionToken, me } from '@/composables/useAuth'

/* ===== 类型 ===== */
interface PoolStatus {
  balance_micro: number
  charged_micro: number
  used_micro: number
  today_used_micro: number
  consumers: number
}
interface PoolFlow {
  id: number | string
  type: string
  revival?: boolean
  user: string
  amount_micro: number
  balance_after_micro: number
  ts: number
}
interface RankRow {
  rank: number
  user: string
  note?: string
  extra?: number
  calls?: number
  amount_micro: number
}
interface HeroRow { user: string; saves: number; amount_micro: number; ts: number }
interface ElderRow { rank: number; user: string; amount_micro: number }
interface MyPoolFlow {
  ts: number
  type: string
  model?: string
  prompt_tokens?: number
  completion_tokens?: number
  amount_micro: number
}
interface MyPool {
  balance_micro?: number
  my_charged_micro?: number
  my_used_micro?: number
  net_micro?: number
  items?: MyPoolFlow[]
}

/* ===== 众筹模型一览（/v1/models acu/ 前缀，官方原版定价：per_token 三段价 / per_call 单次价） ===== */
interface CrowdModel { id: string; price_micro?: number | null; in_price?: number; cache_price?: number; out_price?: number; description?: string }
const crowdModels = ref<CrowdModel[]>([])
const crowdModelsMsg = ref('')
async function loadCrowdModels() {
  try {
    const j = await apiJson<{ data?: CrowdModel[] }>('/models')
    crowdModels.value = (j.data || []).filter(m => m.id.startsWith('acu/'))
  } catch (e) {
    crowdModelsMsg.value = errText(e)
    crowdModels.value = []
  }
}
function microYuan(v?: number | null): string {
  if (v == null) return '--'
  return (v / 1e6).toFixed(4).replace(/0+$/, '').replace(/\.$/, '')
}

/* ===== 池子状态与公开流水 ===== */
const status = ref<PoolStatus | null>(null)
const flows = ref<PoolFlow[]>([])
const loading = ref(true)
const statusMsg = ref('')
const flowsMsg = ref('')

async function loadStatus() {
  statusMsg.value = ''
  try {
    status.value = await apiJson<PoolStatus>('/pool/status')
  } catch (e) {
    statusMsg.value = errText(e)
    status.value = null
  }
}
async function loadFlows() {
  flowsMsg.value = ''
  try {
    const j = await apiJson<{ items?: PoolFlow[] }>('/pool/flows?limit=80')
    flows.value = j.items || []
  } catch (e) {
    flowsMsg.value = errText(e)
    flows.value = []
  }
}

/* ===== 个人注入记录（登录态） ===== */
const myPool = ref<MyPool>({})
const myPoolMsg = ref('')
async function loadMyPool() {
  myPoolMsg.value = ''
  try {
    myPool.value = await apiJson<MyPool>('/my/pool/flows', { session: true })
  } catch (e) {
    myPoolMsg.value = errText(e)
  }
}

/* ===== 榜单 ===== */
type RankTab = 'charge' | 'usage' | 'honor' | 'net'
const RANK_TABS: { id: RankTab; label: string }[] = [
  { id: 'charge', label: '充值榜' },
  { id: 'usage', label: '用量榜' },
  { id: 'honor', label: '荣誉墙' },
  { id: 'net', label: '净贡献榜' },
]
const rankTab = ref<RankTab>('charge')
const rankRange = ref<'week' | 'all'>('all')
const rankItems = ref<RankRow[]>([])
const heroes = ref<HeroRow[]>([])
const elders = ref<ElderRow[]>([])
const rankLoading = ref(false)
const rankMsg = ref('')

async function loadRanks() {
  rankLoading.value = true
  rankMsg.value = ''
  try {
    if (rankTab.value === 'honor') {
      const j = await apiJson<{ heroes?: HeroRow[]; elders?: ElderRow[] }>('/pool/ranks?type=honor')
      heroes.value = j.heroes || []
      elders.value = j.elders || []
      rankItems.value = []
    } else {
      const j = await apiJson<{ items?: RankRow[] }>(`/pool/ranks?type=${rankTab.value}&range=${rankRange.value}`)
      rankItems.value = j.items || []
      heroes.value = []
      elders.value = []
    }
  } catch (e) {
    rankMsg.value = errText(e)
    rankItems.value = []
    heroes.value = []
    elders.value = []
  }
  rankLoading.value = false
}
function setTab(t: RankTab) {
  rankTab.value = t
  if (t === 'usage') rankRange.value = 'week' // 用量榜仅周榜
  loadRanks()
}
function setRange(r: 'week' | 'all') {
  rankRange.value = r
  loadRanks()
}
function isMe(u?: string): boolean {
  return !!u && !!me.value && u === me.value.username
}
const amtLabel = computed(() =>
  rankTab.value === 'charge' ? '累计充值（¥）' : rankTab.value === 'usage' ? '消耗金额（¥）' : '净贡献（¥）',
)
const emptyHint = computed(() =>
  rankTab.value === 'charge'
    ? '第一笔充值将载入史册'
    : rankTab.value === 'usage'
      ? '本周还没有众筹模型调用'
      : '充值大于消耗即可上榜',
)

/* ===== 充值（复用站内支付：product=pool 进公共池） ===== */
const payOpen = ref(false)
const topupAmt = ref<number>(10)
const topupChannel = ref<'alipay' | 'wxpay'>('alipay')
const paying = ref(false)
const payMsg = ref('')
const payOk = ref(false)
const payingOrder = ref<{ out_trade_no: string; pay_url: string } | null>(null)
let pollTimer: number | null = null
const AMTS = [5, 10, 30, 100]

function yuan(v?: number | null): string {
  if (v == null) return '--'
  return (v / 1e6).toFixed(2).replace(/\.00$/, '').replace(/(\.\d)0$/, '$1')
}
const topupMicro = computed(() => Math.round((Number(topupAmt.value) || 0) * 1_000_000))
const giftYuan = computed(() => yuan((Number(topupAmt.value) || 0) * 2_000_000)) // 充值翻倍：到账 = 实付 × 2
const alive = computed(() => !!status.value && status.value.balance_micro > 0)
/* 额度换算：¥1 站点额度 ≈ 50 万输入 tokens（acu/deepseek-v4-flash 官方原价 ¥2/1M 口径） */
const estTokens = computed(() => (status.value ? Math.floor(status.value.balance_micro / 2) : 0))

async function createTopup() {
  if (!isLoggedIn()) {
    payMsg.value = '请先登录再充值'
    payOk.value = false
    return
  }
  if (topupMicro.value < 10_000) {
    payMsg.value = '单笔金额须在 0.01 ~ 1000 元之间'
    payOk.value = false
    return
  }
  paying.value = true
  payMsg.value = ''
  try {
    const j = await apiJson<{ out_trade_no: string; pay_url: string }>('/pay/create', {
      method: 'POST',
      session: true,
      body: { amount_micro: topupMicro.value, channel: topupChannel.value, product: 'pool' },
    })
    payingOrder.value = { out_trade_no: j.out_trade_no, pay_url: j.pay_url }
    payOpen.value = true
    window.open(j.pay_url, '_blank')
    startPolling()
  } catch (e) {
    payMsg.value = errText(e)
    payOk.value = false
  }
  paying.value = false
}
function startPolling() {
  stopPolling()
  pollTimer = window.setInterval(async () => {
    if (!payingOrder.value) {
      stopPolling()
      return
    }
    try {
      const j = await apiJson<{ status: string; amount_micro: number; pool_balance_micro: number }>(
        `/pay/status?out_trade_no=${payingOrder.value.out_trade_no}`,
        { session: true },
      )
      if (j.status === 'paid') {
        payOk.value = true
        payMsg.value = `充值成功：实付 ¥${yuan(j.amount_micro)} → 翻倍到账 ¥${yuan(j.amount_micro * 2)} 站点额度（当前站点额度 ¥${yuan(j.pool_balance_micro)}），感谢扩充公共算力！`
        stopPolling()
        payingOrder.value = null
        payOpen.value = false
        loadStatus()
        loadFlows()
        loadRanks()
        loadMyPool()
      }
    } catch {
      /* 轮询失败忽略 */
    }
  }, 3000)
}
function stopPolling() {
  if (pollTimer != null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}
async function manualCheck() {
  if (!payingOrder.value) return
  try {
    const j = await apiJson<{ status: string; amount_micro: number; pool_balance_micro: number }>(
      `/pay/status?out_trade_no=${payingOrder.value.out_trade_no}`,
      { session: true },
    )
    if (j.status === 'paid') {
      payOk.value = true
      payMsg.value = `充值成功：实付 ¥${yuan(j.amount_micro)} → 翻倍到账 ¥${yuan(j.amount_micro * 2)} 站点额度（当前站点额度 ¥${yuan(j.pool_balance_micro)}）`
      stopPolling()
      payingOrder.value = null
      payOpen.value = false
      loadStatus()
      loadFlows()
      loadRanks()
      loadMyPool()
    } else {
      payMsg.value = '还未查询到支付结果，完成支付后稍等几秒'
      payOk.value = false
    }
  } catch (e) {
    payMsg.value = errText(e)
    payOk.value = false
  }
}
function closePay() {
  payingOrder.value = null
  payOpen.value = false
  stopPolling()
}
onUnmounted(stopPolling)

/* ===== 展示辅助 ===== */
const typeLabel: Record<string, string> = { charge: '充值', seed: '官方注入', consume: '扣费', adjust: '调整' }
function fmtTs(t: number): string {
  const d = new Date((t || 0) * 1000)
  const p = (x: number) => (x < 10 ? '0' : '') + x
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function myTypeText(f: MyPoolFlow): string {
  if (f.type === 'consume') return f.model || '扣费'
  if (f.type === 'charge') return '充值'
  if (f.type === 'seed') return '官方注入'
  return '调整'
}
const netCls = computed(() => ((myPool.value.net_micro ?? 0) >= 0 ? 'pos' : 'negw'))

onMounted(async () => {
  await Promise.all([loadStatus(), loadFlows(), loadRanks(), loadCrowdModels()])
  loading.value = false
  if (sessionToken.value) loadMyPool()
})
</script>

<template>
  <div class="wrap" style="max-width: 1080px;">
    <div class="fade-up">
      <!-- 页头 -->
      <div class="page-head">
        <div>
          <h1><AqIcon name="coin" :size="24" />众筹公共算力池</h1>
          <div class="sub">
            acu/ 前缀众筹模型按<b>官方原版定价</b>从公共站点额度扣费——人人可调、无需充值、个人余额分文不动。站点额度由大家共同充值维持（<b>充 1 元 = 2 元站点额度</b>翻倍到账），见底即暂停，充值即复活。每一笔充值与扣费全部公开可查。
          </div>
        </div>
        <div class="ops">
          <span v-if="status" class="tag" :class="alive ? 'ok' : 'bad'">
            <span class="dot" :class="alive ? 'ok' : 'bad'"></span>{{ alive ? '额度可用' : '等待充值复活' }}
          </span>
        </div>
      </div>

      <p v-if="statusMsg" class="msg bad">{{ statusMsg }}</p>

      <!-- 资金池大卡 -->
      <div class="card accent">
        <div v-if="loading && !status" style="display: grid; gap: 10px;">
          <div class="skeleton" style="min-height: 18px; width: 42%;"></div>
          <div class="skeleton" style="min-height: 50px; width: 56%;"></div>
        </div>
        <template v-else-if="status">
          <div class="row between wrap">
            <span class="dim"><AqIcon name="droplet" :size="14" /> 当前站点额度<template v-if="estTokens > 0"> ≈ 可供 {{ (estTokens / 10000).toLocaleString(undefined, { maximumFractionDigits: 0 }) }} 万输入 tokens（acu/deepseek-v4-flash 官方原价口径）</template></span>
          </div>
          <div class="pool-balance grad-text">¥{{ yuan(status.balance_micro) }}</div>
        </template>
        <div v-else class="empty">
          <div class="big"><AqIcon name="coin" :size="36" /></div>
          <b>池子状态加载失败</b>
          <div class="dim">请刷新页面重试</div>
        </div>
      </div>

      <!-- 四 KPI -->
      <div class="kpis mt16">
        <div class="kpi"><span>累计充值</span><b>{{ status ? '¥' + yuan(status.charged_micro) : '--' }}</b><span class="trend">充值翻倍到账站点额度</span></div>
        <div class="kpi"><span>累计消耗</span><b>{{ status ? '¥' + yuan(status.used_micro) : '--' }}</b><span class="trend">按官方原版定价扣费</span></div>
        <div class="kpi"><span>今日消耗</span><b>{{ status ? '¥' + yuan(status.today_used_micro) : '--' }}</b><span class="trend">每天 0 点重置</span></div>
        <div class="kpi"><span>共同使用人数</span><b>{{ status ? status.consumers : '--' }}</b><span class="trend">无需充值即可调用</span></div>
      </div>

      <!-- 众筹模型一览 -->
      <div class="card mt16">
        <b><AqIcon name="server" :size="16" /> 众筹模型一览（acu/ 前缀 · 按官方原版定价扣池）</b>
        <p class="msg info mt12" style="margin: 0;">以下模型<b>所有分组密钥均可调用</b>（含纯免费），按下方<b>官方原版定价</b>从公共站点额度扣费（充值翻倍到账），个人余额分文不动；失败请求全额退回池子。</p>
        <p v-if="crowdModelsMsg" class="msg bad mt12">{{ crowdModelsMsg }}</p>
        <div v-else-if="!crowdModels.length" class="empty" style="padding: 18px 0;">
          <b>模型列表加载中或暂未上架</b>
          <div class="dim">以 /v1/models 实时下发为准 · 每分钟自动刷新</div>
        </div>
        <div v-else class="tbl-wrap mt12">
          <table class="table">
            <thead><tr><th>模型 ID</th><th class="num">官方原版定价</th><th>计费说明</th></tr></thead>
            <tbody>
              <tr v-for="m in crowdModels" :key="m.id">
                <td><code class="crowd-id">{{ m.id }}</code> <CopyBtn :text="m.id" size="xs" /></td>
                <td class="num">
                  <template v-if="m.in_price != null"><b class="grad-text">¥{{ m.in_price }}</b><span class="dim"> 输入</span> / <b class="grad-text">¥{{ m.cache_price }}</b><span class="dim"> 缓存</span> / <b class="grad-text">¥{{ m.out_price }}</b><span class="dim"> 输出 · 元/百万 tokens</span></template>
                  <template v-else><b class="grad-text">¥{{ microYuan(m.price_micro) }}</b><span class="dim"> / 次</span></template>
                </td>
                <td class="dim">{{ m.description || '按官方原版定价从公共站点额度扣费，个人余额分文不动' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- 登录提示卡 / 个人注入记录 -->
      <div v-if="!sessionToken" class="card mt16 row between wrap">
        <div>
          <b><AqIcon name="user" :size="16" /> 登录后查看我的注入记录</b>
          <div class="dim mt8">登录后可查看个人站点额度余额、累计注入 / 消耗、净贡献与逐笔明细；充值也需要先登录。</div>
        </div>
        <router-link to="/login" class="btn primary"><AqIcon name="key" :size="14" /> 去登录 / 注册</router-link>
      </div>
      <div v-else class="card mt16">
        <b><AqIcon name="wallet" :size="16" /> 我的注入记录</b>
        <p v-if="myPoolMsg" class="msg bad">{{ myPoolMsg }}</p>
        <template v-else>
          <div class="kpis mt12">
            <div class="kpi"><span>站点额度余额</span><b>¥{{ yuan(myPool.balance_micro) }}</b></div>
            <div class="kpi"><span>我累计注入</span><b>¥{{ yuan(myPool.my_charged_micro) }}</b></div>
            <div class="kpi"><span>我累计消耗</span><b>¥{{ yuan(myPool.my_used_micro) }}</b></div>
            <div class="kpi"><span>我的净贡献</span><b :class="netCls">¥{{ yuan(myPool.net_micro) }}</b></div>
          </div>
          <div class="tbl-wrap mt12">
            <table class="table">
              <thead><tr><th>时间</th><th>类型</th><th class="num">金额（¥）</th></tr></thead>
              <tbody>
                <tr v-if="!myPool.items || !myPool.items.length">
                  <td colspan="3" class="empty">还没有众筹池记录——acu/ 模型免充值即可调用</td>
                </tr>
                <tr v-for="(f, i) in myPool.items" :key="i">
                  <td class="dim">{{ fmtTs(f.ts) }}</td>
                  <td>
                    {{ myTypeText(f) }}<span v-if="f.type === 'consume'" class="dim">（入 {{ f.prompt_tokens }} / 出 {{ f.completion_tokens }}）</span>
                  </td>
                  <td class="num" :class="f.amount_micro > 0 ? 'pos' : 'dim'">{{ f.amount_micro > 0 ? '+' : '' }}{{ yuan(f.amount_micro) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
      </div>

      <!-- 充值注入（1:1） -->
      <div class="card mt16">
        <b><AqIcon name="plus" :size="16" /> 充值 · 扩充站点额度</b>
        <p class="dim mt8" style="max-width: 72ch;">
          <b>充 1 元 = 2 元站点额度（翻倍到账）</b>，注入公共池由所有人共用消耗，<b style="color: var(--warn);">不可退、不可转个人余额</b>；无最低充值限制。救场者（额度归零后第一笔充值）将登上荣誉墙。
        </p>
        <div class="chips mt12">
          <button v-for="a in AMTS" :key="a" type="button" class="chip" :class="{ on: topupAmt === a }" @click="topupAmt = a">¥{{ a }}</button>
        </div>
        <div class="form-grid mt12" style="max-width: 560px;">
          <div class="field">
            <label>自定义金额（元 · 0.01 ~ 1000）</label>
            <input v-model.number="topupAmt" class="input" type="number" min="0.01" max="1000" step="0.01" placeholder="如 30" />
          </div>
          <div class="field">
            <label>支付渠道</label>
            <div class="chips">
              <button type="button" class="chip" :class="{ on: topupChannel === 'alipay' }" @click="topupChannel = 'alipay'"><AqIcon name="wallet" :size="13" /> 支付宝</button>
              <button type="button" class="chip" :class="{ on: topupChannel === 'wxpay' }" @click="topupChannel = 'wxpay'"><AqIcon name="chat" :size="13" /> 微信</button>
            </div>
          </div>
        </div>
        <div class="row wrap mt12">
          <button class="btn primary" :disabled="paying || !isLoggedIn()" @click="createTopup">
            {{ paying ? '下单中…' : isLoggedIn() ? `充 ¥${topupAmt || 0} → 到账 ¥${giftYuan}` : '请先登录后充值' }}
          </button>
          <span v-if="isLoggedIn()" class="dim">下单后自动打开收银台，本页每 3 秒自动确认到账</span>
        </div>
        <p v-if="payMsg && !payOpen" class="msg mt12" :class="payOk ? 'ok' : 'bad'">{{ payMsg }}</p>
      </div>

      <!-- 榜单与荣誉 -->
      <div class="card mt16">
        <div class="row between wrap">
          <b><AqIcon name="trophy" :size="16" /> 榜单与荣誉</b>
          <div v-if="rankTab === 'charge' || rankTab === 'net'" class="chips">
            <button type="button" class="chip" :class="{ on: rankRange === 'week' }" @click="setRange('week')">本周</button>
            <button type="button" class="chip" :class="{ on: rankRange === 'all' }" @click="setRange('all')">总榜</button>
          </div>
        </div>
        <div class="chips mt12">
          <button v-for="t in RANK_TABS" :key="t.id" type="button" class="chip" :class="{ on: rankTab === t.id }" @click="setTab(t.id)">{{ t.label }}</button>
        </div>

        <p v-if="rankMsg" class="msg bad mt12">{{ rankMsg }}</p>
        <div v-else-if="rankLoading" style="display: grid; gap: 8px;" class="mt12">
          <div class="skeleton" style="min-height: 34px;"></div>
          <div class="skeleton" style="min-height: 34px;"></div>
          <div class="skeleton" style="min-height: 34px;"></div>
        </div>

        <!-- 荣誉墙 -->
        <template v-else-if="rankTab === 'honor'">
          <div class="mt12">
            <b style="font-size: 13.5px;">救场英雄（池子归零后第一笔复活充值）</b>
            <div class="tbl-wrap mt8">
              <table class="table">
                <thead><tr><th style="width: 56px;">#</th><th>用户</th><th>徽记</th><th class="num">充值（¥）</th><th>时间</th></tr></thead>
                <tbody>
                  <tr v-if="!heroes.length"><td colspan="5" class="empty">暂无救场记录——池子还没熔断过，大家都在续命</td></tr>
                  <tr v-for="(h, i) in heroes" :key="i" :class="{ me: isMe(h.user) }">
                    <td class="dim">{{ i + 1 }}</td>
                    <td>{{ h.user }}</td>
                    <td><span class="tag warn">第 {{ h.saves }} 次救场</span></td>
                    <td class="num">¥{{ yuan(h.amount_micro) }}</td>
                    <td class="dim">{{ fmtTs(h.ts) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
          <div class="mt16">
            <b style="font-size: 13.5px;">开服元老（最早 10 位众筹充值用户）</b>
            <div class="tbl-wrap mt8">
              <table class="table">
                <thead><tr><th style="width: 56px;">#</th><th>用户</th><th>徽记</th><th class="num">累计充值（¥）</th></tr></thead>
                <tbody>
                  <tr v-if="!elders.length"><td colspan="4" class="empty">虚位以待——第一位充值者将永久留名</td></tr>
                  <tr v-for="e in elders" :key="e.rank" :class="{ me: isMe(e.user) }">
                    <td class="dim">{{ e.rank }}</td>
                    <td>{{ e.user }}</td>
                    <td><span class="tag acc">元老</span></td>
                    <td class="num">¥{{ yuan(e.amount_micro) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </template>

        <!-- 常规榜单 -->
        <div v-else class="tbl-wrap mt12">
          <table class="table">
            <thead>
              <tr>
                <th style="width: 56px;">排名</th>
                <th>用户</th>
                <th>徽记</th>
                <th class="num">{{ rankTab === 'usage' ? '调用' : '占比' }}</th>
                <th class="num">{{ amtLabel }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="!rankItems.length"><td colspan="5" class="empty">虚位以待——{{ emptyHint }}</td></tr>
              <tr v-for="e in rankItems" :key="e.rank" :class="{ me: isMe(e.user) }">
                <td><span class="tag" :class="e.rank <= 3 ? 'grad' : ''">{{ e.rank }}</span></td>
                <td>{{ e.user }}</td>
                <td><span v-if="e.note" class="tag acc">{{ e.note }}</span><span v-else class="dim">—</span></td>
                <td class="num">
                  <template v-if="rankTab === 'charge'">{{ e.extra != null ? e.extra.toFixed(1) + '%' : '—' }}</template>
                  <template v-else-if="rankTab === 'usage'">{{ e.calls ?? 0 }} 次</template>
                  <template v-else>—</template>
                </td>
                <td class="num">¥{{ yuan(e.amount_micro) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- 透明账本 -->
      <div class="card mt16">
        <b><AqIcon name="list" :size="16" /> 透明账本（最近 80 笔）</b>
        <p v-if="flowsMsg" class="msg bad mt12">{{ flowsMsg }}</p>
        <div v-else class="tbl-wrap mt12">
          <table class="table">
            <thead><tr><th>时间</th><th>类型</th><th>用户</th><th class="num">变动（¥）</th><th class="num">池余（¥）</th></tr></thead>
            <tbody>
              <tr v-if="loading && !flows.length"><td colspan="5" style="padding: 12px;"><div class="skeleton" style="min-height: 120px;"></div></td></tr>
              <tr v-else-if="!flows.length"><td colspan="5" class="empty">暂无流水——池子从第一笔充值开始书写历史</td></tr>
              <tr v-for="f in flows" :key="f.id">
                <td class="dim">{{ fmtTs(f.ts) }}</td>
                <td>
                  <span class="tag" :class="f.type === 'charge' || f.type === 'seed' ? 'acc' : ''">{{ typeLabel[f.type] || f.type }}</span>
                  <span v-if="f.revival" class="tag warn" style="margin-left: 4px;">救场</span>
                </td>
                <td>{{ f.user }}</td>
                <td class="num" :class="f.amount_micro > 0 ? 'pos' : 'dim'">{{ f.amount_micro > 0 ? '+' : '' }}{{ yuan(f.amount_micro) }}</td>
                <td class="num dim">¥{{ yuan(f.balance_after_micro) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- 支付等待弹层（下单后打开，轮询 + 手动检查照旧） -->
    <Teleport to="body">
      <div v-if="payOpen && payingOrder" class="mask" @click.self="closePay">
        <div class="card accent pop">
          <b><AqIcon name="qr" :size="16" /> 等待支付确认</b>
          <div class="row wrap">
            <code class="order-no">{{ payingOrder.out_trade_no }}</code>
            <CopyBtn :text="payingOrder.out_trade_no" label="复制订单号" size="xs" />
          </div>
          <div class="dim">
            收银台已在新窗口打开（{{ topupChannel === 'alipay' ? '支付宝' : '微信' }} · 实付 ¥{{ topupAmt || 0 }} → 到账 ¥{{ giftYuan }}）。完成支付后本页自动确认；若窗口被拦截，可点下方重新打开。
          </div>
          <div class="row wrap">
            <a class="btn" :href="payingOrder.pay_url" target="_blank" rel="noopener"><AqIcon name="external" :size="14" /> 重新打开支付页</a>
            <button class="btn primary" @click="manualCheck"><AqIcon name="check" :size="14" /> 我已支付，立即检查</button>
            <button class="btn ghost" @click="closePay">关闭</button>
          </div>
          <p v-if="payMsg" class="msg" style="margin: 0;" :class="payOk ? 'ok' : 'bad'">{{ payMsg }}</p>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
/* 布局微调：大数字 / 弹层 / 高亮行 / 着色 */
.pool-balance { font-size: 54px; font-weight: 800; line-height: 1.2; margin-top: 6px; font-variant-numeric: tabular-nums; letter-spacing: -.01em; }
@media (max-width: 640px) { .pool-balance { font-size: 40px; } }
.mask { position: fixed; inset: 0; background: color-mix(in srgb, var(--bg0) 62%, transparent); backdrop-filter: blur(3px); z-index: 300; display: flex; align-items: center; justify-content: center; padding: 20px; }
.pop { width: 400px; max-width: 100%; background: var(--bg2-solid); box-shadow: var(--shadow-2); display: flex; flex-direction: column; gap: 12px; }
.order-no { font-family: var(--mono); font-size: 12px; color: var(--txt1); background: var(--bg3); border: 1px solid var(--line); border-radius: 7px; padding: 4px 10px; word-break: break-all; }
tr.me td { background: var(--acc-soft); }
.crowd-id { font-family: var(--mono); font-size: 12.5px; color: var(--acc); font-weight: 600; }
.pos { color: var(--ok); }
.neg { color: var(--bad); }
.negw { color: var(--warn); }
</style>
