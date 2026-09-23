<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'
import { apiJson } from '@/composables/useApi'
import { isLoggedIn } from '@/composables/useAuth'
import { useMeta } from '@/composables/useMeta'
const { meta } = useMeta()
const baseUrl = 'https://api.ltzy.top/v1'
const code = `curl ${baseUrl}/chat/completions \\\n  -H "Authorization: Bearer sk-你的密钥" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"acu/deepseek-v4-flash",
       "messages":[{"role":"user","content":"你好"}]}'`
const rows = ref<{model:string; calls_1h:number; avg_latency_ms:number; avg_first_ms?:number}[]>([])
const updated = ref('')
const failed = ref(false)
/* 合作推广位（/v1/meta 下发、后台 settings 可改；内置秘塔 AI 兜底，站长可随时替换或移除） */
const ad = computed(() => ({
  img: meta.value?.ad_image || '/ads/metaso.png',
  link: meta.value?.ad_link || 'https://metaso.cn/minimax-h3/?s=AQUA',
  label: meta.value?.ad_label || '秘塔 AI',
}))
const lines = computed(() => [
  { id: 'acu/', name: '公益免费线', mode: '注册即用 · 纯免费', desc: '公益优先：官方自营线路直供，全部模型免费调用，不扣个人余额——先体验完整能力，再按需升级。', to: '/models?view=free' },
  { id: 'aqua/', name: '官方中转 · 超高速专线', mode: '按量计费', desc: '官方原版接口直连：零中间层、延迟不叠加，旗舰模型全覆盖，用多少付多少——面向生产环境与三方分发的稳定通道。', to: '/models?view=paid' },
  { id: 'codex/', name: '按量专线', mode: '按量计费', desc: '输入、缓存与输出分别计价，用多少付多少——账单逐笔可核对。', to: '/models?view=paid' },
].map(line => {
  const samples = rows.value.filter(row => row.model.startsWith(line.id))
  const count = samples.reduce((n, row) => n + row.calls_1h, 0)
  /* 展示口径：近 1h 按请求量加权的平均首字延迟（FRT，用户感知口径） */
  const lat = count ? samples.reduce((n, row) => n + row.calls_1h * (row.avg_first_ms || row.avg_latency_ms || 0), 0) / count : null
  return { ...line, count, lat }
}))
async function refresh() {
  if (document.hidden) return
  try {
    const result = await apiJson<{models?: typeof rows.value}>('/status')
    rows.value = result.models || []
    updated.value = new Date().toLocaleTimeString('zh-CN')
    failed.value = false
  } catch { failed.value = true }
}
let timer = 0
onMounted(() => { refresh(); timer = window.setInterval(refresh, 60000) })
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <main class="wrap home">
    <section class="hero">
      <div class="intro">
        <h1>让模型接入，<br><span>清晰而简单。</span></h1>
        <p>公益起点的模型开放平台：<b>免费模型优先，注册即用</b>；<br class="desktop-break">需要稳定接口与三方分发时，专线随时补位。</p>
        <div class="actions">
          <router-link class="btn primary" :to="isLoggedIn() ? '/console' : '/login'">免费开始接入 <span aria-hidden="true">↗</span></router-link>
          <router-link class="btn ghost" to="/models">查看免费模型</router-link>
          <router-link class="btn ghost" to="/community"><AqIcon name="chat" :size="15" /> 加入 Q 群交流</router-link>
        </div>
        <div class="endpoint"><span>API BASE URL</span><code>{{ baseUrl }}</code><CopyBtn :text="baseUrl" label="复制" /></div>
      </div>
      <div class="request-panel">
        <div class="request-top"><span>你的第一条请求</span><span class="mono">cURL</span></div>
        <pre>{{ code }}</pre>
        <div class="request-bottom"><span><b>acu/</b> 官方自营纯免费线路</span><CopyBtn :text="code" label="复制示例" /></div>
        <p>先在工作台创建密钥，再替换示例中的 sk-你的密钥。模型可用性以目录和实际响应为准。</p>
      </div>
    </section>
    <div v-if="meta?.announcement_enabled && meta?.announcement" class="banner">{{ meta.announcement }}</div>
    <!-- 合作推广位（后台 settings: ad_image / ad_link / ad_label 可随时替换或下线） -->
    <section class="ad-band">
      <a :href="ad.link" target="_blank" rel="noopener sponsored" class="ad-card">
        <img :src="ad.img" alt="合作伙伴推广" loading="lazy" decoding="async">
        <div class="ad-copy"><b>{{ ad.label }}</b><span>推广 · 点击了解 ↗</span></div>
        <span class="ad-tag">推广</span>
      </a>
    </section>
    <section class="routes">
      <div class="section-heading"><h2>免费优先，专线按需。</h2><p>公益免费注册即用；专线支撑稳定接口与三方分发。</p></div>
      <div class="line-grid">
        <router-link v-for="line in lines" :key="line.id" :to="line.to" class="line-item">
          <div class="line-meta"><code>{{ line.id }}</code><span>{{ line.mode }}</span></div>
          <h3>{{ line.name }}</h3><p>{{ line.desc }}</p>
          <span class="line-link">查看模型 <span aria-hidden="true">↗</span></span>
        </router-link>
      </div>
    </section>
    <section class="status-band">
      <div><h2>服务状态，有据可查。</h2><p>近 1 小时 · 按请求量加权的平均首字延迟</p><small>{{ failed ? '更新失败；已有数据可能过期' : updated ? `最后更新 ${updated}` : '正在获取样本' }}</small></div>
      <div v-for="line in lines" :key="line.id" class="line-status"><code>{{ line.id }}</code><strong>{{ line.lat === null ? '暂无样本' : (line.lat / 1000).toFixed(2) + 's' }}</strong><small>{{ line.count }} 次请求</small></div>
      <router-link to="/status">查看详情 ↗</router-link>
    </section>
    <section class="start">
      <div class="section-heading"><h2>从选择到调用，只需三步。</h2><router-link to="/api">打开接入文档 ↗</router-link></div>
      <div class="steps"><div><span>01</span><h3>选择模型</h3><p>比较模型能力、线路与价格，复制完整模型 ID。</p></div><div><span>02</span><h3>创建密钥</h3><p>在工作台管理 API 密钥，查看余额和调用记录。</p></div><div><span>03</span><h3>发送请求</h3><p>配置 Base URL 与密钥。遇到问题，按响应错误码排查。</p></div></div>
    </section>
    <section class="explore"><div><h2>先体验，再构建。</h2><p>在线对话、实用工具与社区，帮助你找到合适的用法。</p></div><div class="actions"><router-link class="btn" to="/playground">AI 对话</router-link><router-link class="btn ghost" to="/tools">工具箱</router-link><router-link class="btn ghost" to="/community">社区交流</router-link></div></section>
  </main>
</template>

<style scoped>
.home { padding-bottom: 24px; }
.hero { display:grid; grid-template-columns:1.05fr 1fr; gap:56px; align-items:center; padding:76px 0 64px; position:relative; }
.hero::before { content:''; position:absolute; inset:-20px -15px; background:radial-gradient(ellipse at 75% 40%,var(--acc-soft),transparent 65%); z-index:-1; pointer-events:none; }
h1 { font-size:clamp(36px,4.8vw,62px); line-height:1.2; letter-spacing:-.045em; font-weight:750; }
h1 span { color:var(--acc); }
.intro > p { color:var(--txt2); font-size:17px; line-height:1.9; margin:24px 0 28px; }
.actions { display:flex; gap:12px; flex-wrap:wrap; align-items:center; }
.actions .btn { min-height:46px; padding:10px 20px; font-size:14px; }
.endpoint { display:flex; flex-wrap:wrap; gap:10px; align-items:center; margin-top:30px; }
.endpoint > span { font-size:11px; letter-spacing:.12em; width:100%; color:var(--txt2); }
.endpoint code { font-size:13px; overflow-wrap:anywhere; }
.request-panel { min-width:0; background:var(--bg1); border:1px solid var(--line-strong); border-radius:16px; box-shadow:var(--shadow-2); overflow:hidden; }
.request-top,.request-bottom { display:flex; align-items:center; justify-content:space-between; gap:12px; padding:18px 22px; font-size:13px; }
.request-top { border-bottom:1px solid var(--line); color:var(--txt2); }
.request-panel pre { padding:28px 22px; overflow:auto; font-size:12px; line-height:2; color:var(--txt0); }
.request-bottom { border-top:1px solid var(--line); flex-wrap:wrap; }
.request-panel > p { padding:0 22px 20px; font-size:12px; color:var(--txt2); }
.section-heading { display:flex; justify-content:space-between; align-items:baseline; flex-wrap:wrap; gap:12px; margin-bottom:28px; }
.section-heading h2,.status-band h2,.explore h2 { font-size:24px; letter-spacing:-.03em; }
.section-heading p,.steps p,.explore p { color:var(--txt2); }
.line-grid { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); border-top:1px solid var(--line-strong); border-bottom:1px solid var(--line-strong); }
.line-item { padding:30px 28px; color:var(--txt1); transition:background-color var(--t-fast); }
.line-item + .line-item { border-left:1px solid var(--line); }
.line-item:hover { background:var(--acc-soft); }
.line-meta { display:flex; align-items:center; justify-content:space-between; font-size:12px; color:var(--txt2); margin-bottom:24px; }
.line-meta code { font-size:20px; color:var(--acc); }
.line-item h3 { font-size:21px; margin-bottom:12px; }
.line-item p { color:var(--txt2); min-height:72px; }
.line-link { display:flex; justify-content:space-between; margin-top:24px; font-size:13px; color:var(--txt0); }
.status-band { display:flex; align-items:center; flex-wrap:wrap; gap:30px; background:var(--bg1); padding:28px; margin-top:32px; border-radius:12px; border:1px solid var(--line); }
/* 合作推广位：使用主题背景与明确推广标识 */
.ad-band { margin: 8px 0 56px; }
.ad-card { display:flex; align-items:center; gap:24px; padding:18px 24px; background:var(--bg1); border:1px solid var(--line-strong); border-radius:16px; text-decoration:none; position:relative; overflow:hidden; transition:var(--tr-ui); }
.ad-card:hover { border-color:var(--acc); transform:translateY(-2px); box-shadow:var(--shadow-2); }
.ad-card img { height:64px; width:auto; border-radius:10px; flex:none; }
.ad-copy { display:flex; flex-direction:column; gap:4px; }
.ad-copy b { color:var(--txt0); font-size:15px; letter-spacing:-.01em; }
.ad-copy span { color:var(--txt2); font-size:12.5px; }
.ad-tag { position:absolute; top:10px; right:12px; font-size:10.5px; color:var(--txt3); border:1px solid var(--line); border-radius:6px; padding:1px 8px; letter-spacing:.1em; }
.status-band > div:first-child { flex:1; min-width:220px; }
.status-band h2 { font-size:18px; margin-bottom:6px; }
.status-band p,.status-band small { font-size:12px; color:var(--txt2); }
.line-status { display:grid; gap:4px; min-width:86px; }
.line-status code { color:var(--txt2); }
.line-status strong { font-size:20px; font-variant-numeric:tabular-nums; }
.start { padding:76px 0; }
.steps { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:40px; }
.steps span { font:14px var(--mono); color:var(--acc); display:block; margin-bottom:20px; }
.steps h3 { margin-bottom:10px; }
.explore { display:flex; justify-content:space-between; align-items:center; gap:24px; flex-wrap:wrap; padding:36px 0; border-top:1px solid var(--line); }
.explore p { margin-top:10px; }
@media(max-width:960px) { .hero { gap:28px; padding:56px 0 48px; } .request-panel pre { font-size:11px; } }
@media(max-width:720px) { .hero { grid-template-columns:minmax(0,1fr); padding:44px 0; gap:36px; } .intro > p { font-size:16px; } .desktop-break { display:none; } .line-grid,.steps { grid-template-columns:minmax(0,1fr); } .line-item { padding:24px 8px; } .line-item + .line-item { border-left:0; border-top:1px solid var(--line); } .line-item p { min-height:0; } .line-meta { margin-bottom:12px; } .status-band { padding:20px; gap:24px; } .status-band > div:first-child { flex-basis:100%; } .start { padding:48px 0; } .steps { gap:28px; } .steps span { margin-bottom:8px; } .ad-card { gap:14px; padding:12px 14px; } .ad-card img { height:44px; } .ad-copy b { font-size:13.5px; } .ad-copy span { font-size:11.5px; } }
</style>
