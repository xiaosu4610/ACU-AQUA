<script setup lang="ts">
/* 首页 · 沉浸式 hero + 实时数据条 + 功能卡矩阵 + 快速开始三步
 * 接口对接（与旧版 1:1）：
 * - GATEWAY（/composables/useApi）→ baseUrl 展示与复制（旧版 hero Base URL）
 * - isLoggedIn()（/composables/useAuth）→ CTA 注册/登录 ↔ 控制台切换
 * - /v1/meta（useMeta 单例）→ 公告横幅 / QQ 群链接（失败静默）
 * - /v1/status（apiJson('/status')）→ 版本 / 连续运行 / 近 1h 成功率与时延（30 秒轮询） */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'
import { apiJson, GATEWAY } from '@/composables/useApi'
import { isLoggedIn } from '@/composables/useAuth'
import { useMeta } from '@/composables/useMeta'
import { useModels } from '@/composables/useModels'

/* ---- 旧版保留：网关地址（Base URL 三步接入第一块） ---- */
const baseUrl = GATEWAY.startsWith('/') ? location.origin + GATEWAY : GATEWAY

/* ---- /v1/meta 站点配置（公告 / Q 群），失败静默走兜底 ---- */
const { meta, loadMeta } = useMeta()
loadMeta()

/* ---- /v1/status 实时数据条 ---- */
interface StatusModel { model: string; success_rate: number; calls_1h: number; avg_latency_ms: number }
interface StatusResp { version?: string; uptime_sec?: number; window?: string; models?: StatusModel[] }
const statusLoading = ref(true)
const statusErr = ref(false)
const stVersion = ref('--')
const stUptime = ref('--')
const stOkRate = ref('--')
const stLatency = ref('--')
/* 在线模型：站点全部模型（/v1/models 全量，仅排除 auto 聚合项），useModels 单例共享 */
const { models, load: loadModels } = useModels()
const stModels = computed(() => models.value.filter(m => m.id !== 'auto').length || '--')

function fmtUptime(sec: number): string {
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  return d > 0 ? `${d} 天 ${h} 时` : `${h} 时 ${Math.floor((sec % 3600) / 60)} 分`
}
async function loadStatus() {
  statusLoading.value = true
  try {
    const j = await apiJson<StatusResp>('/status')
    const list = j.models || []
    stVersion.value = j.version || '--'
    stUptime.value = j.uptime_sec ? fmtUptime(j.uptime_sec) : '--'
    if (list.length) {
      const avgRate = list.reduce((s, m) => s + (m.success_rate || 0), 0) / list.length
      const avgLat = list.reduce((s, m) => s + (m.avg_latency_ms || 0), 0) / list.length
      stOkRate.value = avgRate.toFixed(1) + '%'
      stLatency.value = (avgLat / 1000).toFixed(2) + ' s'
    }
    statusErr.value = false
  } catch { statusErr.value = true /* 保留上次成功数据，下一轮自动重试 */ }
  statusLoading.value = false
}
let statusTimer = 0
onMounted(() => { loadModels(); loadStatus(); statusTimer = window.setInterval(loadStatus, 30000) })
onUnmounted(() => { if (statusTimer) window.clearInterval(statusTimer) })

/* ---- 功能卡矩阵 ---- */
const FEATURES = [
  { icon: 'chat', title: '在线体验', desc: '登录后流式对话，全模型切换即开即用', to: '/playground' },
  { icon: 'layout', title: '个人控制台', desc: '创建 / 管理 API 密钥，余额与账号设置', to: '/console' },
  { icon: 'chart', title: '我的用量', desc: '登录后查看用量统计与调用日志', to: '/usage' },
  { icon: 'box', title: '模型中心', desc: '免费与收费模型一览，实时健康分，一键复制模型 ID', to: '/models' },
  { icon: 'puzzle', title: '工具箱', desc: 'IP 定位 / 翻译 / 子网计算 / 小游戏，纯免费', to: '/tools' },
  { icon: 'book', title: 'API 文档', desc: '端点、参数、错误码一览，OpenAI 协议全兼容', to: '/api' },
  { icon: 'trophy', title: '模型竞技场', desc: '双模型盲测对比，投票揭晓身份，全站胜率排行', to: '/arena' },
  { icon: 'activity', title: '状态大屏', desc: '全站调用量、成功率、模型健康度实时透明', to: '/status' },
  { icon: 'heart', title: '赞助支持', desc: '请作者喝杯咖啡，助服务器与算力走得更远', to: '/sponsor' },
]

/* ---- 快速开始第三步：请求示例（curl / Python / JS 切换） ---- */
const DEMO_LANGS = ['curl', 'python', 'js'] as const
type DemoLang = (typeof DEMO_LANGS)[number]
const demoLang = ref<DemoLang>('curl')
const DEMO: Record<'curl' | 'python' | 'js', string> = {
  curl: `curl ${baseUrl}/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer sk-你的密钥" \\
  -d '{"model": "gpt-oss-20b", "messages": [{"role": "user", "content": "用一句话介绍你自己"}]}'`,
  python: `from openai import OpenAI

client = OpenAI(
    api_key="sk-你的密钥",              # 控制台创建
    base_url="${baseUrl}",               # 结尾已带 /v1
)
r = client.chat.completions.create(
    model="gpt-oss-20b",
    messages=[{"role": "user", "content": "用一句话介绍你自己"}],
)
print(r.choices[0].message.content)`,
  js: `import OpenAI from "openai";

const client = new OpenAI({
  apiKey: "sk-你的密钥",                // 控制台创建
  baseURL: "${baseUrl}",                 // 结尾已带 /v1
});
const r = await client.chat.completions.create({
  model: "gpt-oss-20b",
  messages: [{ role: "user", content: "用一句话介绍你自己" }],
});
console.log(r.choices[0].message.content);`,
}
const demoCode = computed(() => DEMO[demoLang.value])

/* ---- 社区 / 开源（静态内容与旧版一致，Q 群以 meta 下发优先） ---- */
const qqUrl = computed(() => meta.value?.qq_group_url || 'https://qm.qq.com/q/qoe6XbsVge')
const qqNum = computed(() => meta.value?.qq_group || '1103667832')
</script>

<template>
  <div class="wrap">
    <!-- ================= 沉浸式 Hero ================= -->
    <section class="hero fade-up">
      <div class="orb o1" aria-hidden="true"></div>
      <div class="orb o2" aria-hidden="true"></div>
      <div class="orb o3" aria-hidden="true"></div>

      <span class="tag acc hero-badge"><AqIcon name="bolt" :size="13" />OpenAI 兼容 · 注册即用 · 永久免费额度</span>
      <h1 class="hero-title">
        AQUA api · 算力如水 普惠共享<br />
        <span class="grad-text">一个接口接入全部大模型</span>
      </h1>
      <p class="hero-sub">
        半公益开放算力站 —— 免费 · 极速 · 注册即用，Nvidia NIM 与官方自营专线的 OpenAI 兼容 API 网关（ACU 工程系列旗舰项目）。
        客户端只改 base_url 与 api_key，协议级兼容，开箱即用。
      </p>

      <div class="hero-cta">
        <router-link v-if="!isLoggedIn()" class="btn primary" to="/login">
          <AqIcon name="key" :size="15" />注册 / 登录 · 创建密钥
        </router-link>
        <router-link v-else class="btn primary" to="/console">
          <AqIcon name="layout" :size="15" />进入我的控制台
        </router-link>
        <router-link class="btn" to="/api"><AqIcon name="book" :size="15" />查看 API 文档</router-link>
      </div>

      <!-- 实时数据条：/v1/status（30 秒轮询） -->
      <div class="card live-strip">
        <div class="li">
          <span>网关版本</span>
          <b v-if="statusLoading" class="skeleton" style="min-height: 18px; width: 72px;"></b>
          <b v-else class="num">{{ stVersion }}</b>
        </div>
        <div class="li">
          <span>连续运行</span>
          <b v-if="statusLoading" class="skeleton" style="min-height: 18px; width: 88px;"></b>
          <b v-else class="num">{{ stUptime }}</b>
        </div>
        <div class="li">
          <span>近 1h 成功率</span>
          <b v-if="statusLoading" class="skeleton" style="min-height: 18px; width: 64px;"></b>
          <b v-else class="num">{{ stOkRate }}</b>
        </div>
        <div class="li">
          <span>平均时延</span>
          <b v-if="statusLoading" class="skeleton" style="min-height: 18px; width: 64px;"></b>
          <b v-else class="num">{{ stLatency }}</b>
        </div>
        <div class="li">
          <span>在线模型</span>
          <b v-if="statusLoading" class="skeleton" style="min-height: 18px; width: 46px;"></b>
          <b v-else class="num">{{ stModels }}</b>
        </div>
        <div class="li st">
          <span class="dot" :class="statusErr ? 'bad' : 'ok'"></span>
          {{ statusErr ? '状态同步失败 · 自动重试中' : '实时健康' }}
        </div>
      </div>
      <div v-if="statusErr" class="msg bad">实时状态获取失败（/v1/status），将在 30 秒后自动重试；其余功能不受影响。</div>

      <!-- 公告：/v1/meta 配置下发 -->
      <div v-if="meta?.announcement_enabled && meta?.announcement" class="banner">
        <AqIcon name="info" :size="15" />
        <span>{{ meta.announcement }}</span>
      </div>
    </section>

    <!-- ================= 功能卡矩阵 ================= -->
    <section class="mt24">
      <div class="sec-head">
        <h2><AqIcon name="grid" :size="19" />站内直达</h2>
        <div class="sub">从对话体验到数据大屏，一个站点全覆盖</div>
      </div>
      <div class="grid3 fade-up">
        <router-link v-for="f in FEATURES" :key="f.to" :to="f.to" class="card hoverable feat">
          <span class="feat-ic"><AqIcon :name="f.icon" :size="19" /></span>
          <b>{{ f.title }}</b>
          <span class="dim">{{ f.desc }}</span>
          <span class="feat-go"><AqIcon name="arrow-right" :size="14" /></span>
        </router-link>
      </div>
    </section>

    <!-- ================= 快速开始三步 ================= -->
    <section class="mt24">
      <div class="sec-head">
        <h2><AqIcon name="bolt" :size="19" />三步接入</h2>
        <div class="sub">注册免费 · 密钥自助创建 · 随时吊销重建 · 免费模型注册即用</div>
      </div>
      <div class="grid3 fade-up">
        <div class="card">
          <div class="step-no">STEP 01</div>
          <b><AqIcon name="server" :size="16" />Base URL（接口地址）</b>
          <div class="row mt12" style="flex-wrap: nowrap;">
            <code class="code" style="flex: 1; padding: 9px 12px;">{{ baseUrl }}</code>
            <CopyBtn :text="baseUrl" />
          </div>
          <p class="dim mt8">客户端只需填入该地址，系统自动拼接 /chat/completions 等路径。</p>
        </div>
        <div class="card">
          <div class="step-no">STEP 02</div>
          <b><AqIcon name="key" :size="16" />API Key（密钥）</b>
          <p class="mt12">
            注册登录后，在<b>个人控制台一键创建密钥</b>（形如 <code>sk-****</code>）——
            注册免费、创建自助、随时吊销重建，每个密钥独立统计用量。
          </p>
          <router-link to="/console" class="btn block mt12"><AqIcon name="plus" :size="14" />进入控制台创建</router-link>
        </div>
        <div class="card">
          <div class="step-no">STEP 03</div>
          <b><AqIcon name="send" :size="16" />发起第一条请求</b>
          <div class="chips mt12">
            <button v-for="l in DEMO_LANGS" :key="l" class="chip" :class="{ on: demoLang === l }" @click="demoLang = l">
              {{ l === 'js' ? 'JavaScript' : l === 'curl' ? 'cURL' : 'Python' }}
            </button>
          </div>
          <pre class="code mt8 demo-code">{{ demoCode }}</pre>
          <div class="row mt8">
            <CopyBtn :text="demoCode" />
            <router-link to="/models" class="btn ghost sm">去模型中心选模型 <AqIcon name="arrow-right" :size="13" /></router-link>
          </div>
        </div>
      </div>
    </section>

    <!-- ================= 开源与社区 ================= -->
    <section class="mt24">
      <div class="sec-head">
        <h2><AqIcon name="heart" :size="19" />开源与社区</h2>
        <div class="sub">AGPL-3.0 完全开源 · 网关 + 前台全量源码 · 大版本公告在 QQ 频道同步</div>
      </div>
      <div class="grid2 fade-up">
        <div class="card">
          <b><AqIcon name="star" :size="16" />AQUA api · ACU 工程系列开源项目</b>
          <p class="mt8 dim">
            可自由自部署；二开对外提供服务需以同协议开源。喜欢就给作者点个 Star，是项目持续演进的最大动力。
          </p>
          <div class="row wrap mt12">
            <a class="btn sm" href="https://gitee.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener">
              <AqIcon name="external" :size="13" />Gitee 仓库
            </a>
            <a class="btn sm" href="https://github.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener">
              <AqIcon name="external" :size="13" />GitHub 仓库
            </a>
            <CopyBtn text="https://gitee.com/xiaosu4610/aqua-rust-workers" label="复制仓库地址" />
          </div>
        </div>
        <div class="card">
          <b><AqIcon name="message" :size="16" />官方交流群 · QQ 频道</b>
          <p class="mt8 dim">
            技术交流、使用反馈、问题求助都在这里；一群将满请加二群。频道号
            <code>pd57362562</code>，大版本更新等重要公告同步于此。
          </p>
          <div class="row wrap mt12">
            <span class="tag acc mono">群号 {{ qqNum }}</span>
            <a class="btn sm primary" :href="qqUrl" target="_blank" rel="noopener">
              <AqIcon name="arrow-right" :size="13" />加入群聊
            </a>
            <CopyBtn :text="qqNum" label="复制群号" />
          </div>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
/* ---- Hero 区（布局微调；颜色一律取令牌） ---- */
.hero {
  position: relative;
  padding: 72px 0 8px;
  text-align: center;
  overflow: hidden;
}
.orb {
  position: absolute;
  border-radius: 50%;
  filter: blur(72px);
  pointer-events: none;
  z-index: -1;
}
.o1 { width: 340px; height: 340px; left: 6%; top: -60px; background: var(--acc); opacity: .14; animation: twinkle 7s ease-in-out infinite; }
.o2 { width: 300px; height: 300px; right: 4%; top: 30px; background: var(--acc-2); opacity: .15; animation: twinkle 9s ease-in-out 1.2s infinite; }
.o3 { width: 260px; height: 260px; left: 42%; top: 140px; background: var(--acc-3); opacity: .12; animation: twinkle 11s ease-in-out 2.4s infinite; }
@keyframes twinkle {
  0%, 100% { opacity: .06; transform: scale(.92); }
  50% { opacity: .17; transform: scale(1.05); }
}
.hero-badge { margin-bottom: 18px; }
.hero-title {
  font-size: clamp(38px, 6.2vw, 64px);
  font-weight: 800;
  letter-spacing: -.03em;
  line-height: 1.14;
}
.hero-title .grad-text { font-size: 1.08em; }
.hero-sub {
  max-width: 640px;
  margin: 18px auto 0;
  color: var(--txt2);
  font-size: 15px;
}
.hero-cta {
  display: flex;
  justify-content: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 26px;
}
.hero-cta .btn { padding: 11px 22px; font-size: 14.5px; }

/* ---- 实时数据条 ---- */
.live-strip {
  margin: 34px auto 0;
  max-width: 920px;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 8px 18px;
  text-align: left;
  padding: 16px 20px;
}
.live-strip .li span { display: block; font-size: 11.5px; color: var(--txt2); }
.live-strip .li b { font-size: 17px; color: var(--txt0); font-weight: 700; margin-top: 2px; display: inline-block; min-height: 20px; }
.live-strip .li.st { display: flex; align-items: center; gap: 7px; font-size: 12.5px; color: var(--txt2); justify-content: flex-end; }

/* ---- 功能卡矩阵 ---- */
.sec-head { margin: 0 0 16px; }
.sec-head h2 { display: flex; align-items: center; gap: 9px; font-size: 21px; }
.sec-head .sub { color: var(--txt2); font-size: 13px; margin-top: 3px; }
.feat { display: flex; flex-direction: column; align-items: flex-start; gap: 6px; color: inherit; position: relative; }
.feat b { font-size: 15px; }
.feat-ic {
  width: 38px; height: 38px; border-radius: 11px;
  display: flex; align-items: center; justify-content: center;
  background: var(--acc-soft); color: var(--acc);
  margin-bottom: 4px;
}
.feat-go {
  position: absolute; right: 16px; top: 22px;
  color: var(--txt3);
  transition: transform var(--t-fast), color var(--t-fast);
}
.feat:hover .feat-go { color: var(--acc); transform: translateX(3px); }

/* ---- 快速开始 ---- */
.step-no {
  font-family: var(--mono);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: .12em;
  color: var(--acc);
  background: var(--acc-soft);
  border-radius: 7px;
  padding: 3px 9px;
  width: fit-content;
  margin-bottom: 10px;
}
.demo-code { min-height: 208px; white-space: pre; margin-top: 0; }
</style>
