<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import CopyBtn from '@/components/CopyBtn.vue'
import { apiJson } from '@/composables/useApi'
import { isLoggedIn } from '@/composables/useAuth'
import { useMeta } from '@/composables/useMeta'
const { meta } = useMeta()
const baseUrl = 'https://api.ltzy.top/v1'
const code = `curl ${baseUrl}/chat/completions \\\n  -H "Authorization: Bearer sk-你的密钥" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"acu/deepseek-v4-flash",
       "messages":[{"role":"user","content":"你好"}]}'`
const rows = ref<{model:string; calls_1h:number; success_rate:number}[]>([])
const updated = ref('')
const failed = ref(false)
const lines = computed(() => [
  { id: 'acu/', name: '官方自营线', mode: '纯免费', desc: '官方自营纯免费线路。与收费线路独立，不扣个人余额。', to: '/models?view=free' },
  { id: 'aqua/', name: '按次模型', mode: '按次计费', desc: '每次成功请求按模型单价结算。选型前查看完整价格。', to: '/models?view=paid' },
  { id: 'codex/', name: '按量模型', mode: '按量计费', desc: '输入、缓存与输出分别计价，按实际用量核对费用。', to: '/models?view=paid' },
].map(line => {
  const samples = rows.value.filter(row => row.model.startsWith(line.id))
  const count = samples.reduce((n, row) => n + row.calls_1h, 0)
  const rate = count ? samples.reduce((n, row) => n + row.calls_1h * row.success_rate, 0) / count : null
  return { ...line, count, rate }
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
        <p>一个 OpenAI 兼容接口，连接免费与付费模型。<br class="desktop-break">选好线路，看清费用，把精力留给创造。</p>
        <div class="actions">
          <router-link class="btn primary" :to="isLoggedIn() ? '/console' : '/login'">开始接入 <span aria-hidden="true">↗</span></router-link>
          <router-link class="btn ghost" to="/models">查看模型与价格</router-link>
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
    <section class="routes">
      <div class="section-heading"><h2>三条线路，各自清楚。</h2><p>免费、按次、按量，独立选择与计费。</p></div>
      <div class="line-grid">
        <router-link v-for="line in lines" :key="line.id" :to="line.to" class="line-item">
          <div class="line-meta"><code>{{ line.id }}</code><span>{{ line.mode }}</span></div>
          <h3>{{ line.name }}</h3><p>{{ line.desc }}</p>
          <span class="line-link">查看模型 <span aria-hidden="true">↗</span></span>
        </router-link>
      </div>
    </section>
    <section class="status-band">
      <div><h2>服务状态，有据可查。</h2><p>近 1 小时 · 按请求量加权成功率</p><small>{{ failed ? '更新失败；已有数据可能过期' : updated ? `最后更新 ${updated}` : '正在获取样本' }}</small></div>
      <div v-for="line in lines" :key="line.id" class="line-status"><code>{{ line.id }}</code><strong>{{ line.rate === null ? '暂无样本' : `${line.rate.toFixed(1)}%` }}</strong><small>{{ line.count }} 次请求</small></div>
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
.hero { display:grid; grid-template-columns:1.05fr 1fr; gap:56px; align-items:center; padding:100px 0 88px; position:relative; }
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
@media(max-width:960px) { .hero { gap:28px; padding:64px 0; } .request-panel pre { font-size:11px; } }
@media(max-width:720px) { .hero { grid-template-columns:minmax(0,1fr); padding:44px 0; gap:36px; } .intro > p { font-size:16px; } .desktop-break { display:none; } .line-grid,.steps { grid-template-columns:minmax(0,1fr); } .line-item { padding:24px 8px; } .line-item + .line-item { border-left:0; border-top:1px solid var(--line); } .line-item p { min-height:0; } .line-meta { margin-bottom:12px; } .status-band { padding:20px; gap:24px; } .status-band > div:first-child { flex-basis:100%; } .start { padding:48px 0; } .steps { gap:28px; } .steps span { margin-bottom:8px; } }
</style>
