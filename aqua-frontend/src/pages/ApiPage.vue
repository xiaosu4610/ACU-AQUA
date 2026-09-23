<script setup lang="ts">
import { ref } from 'vue'
import CopyBtn from '@/components/CopyBtn.vue'
const language = ref('curl')
const sections = [{id:'a-start',label:'快速开始'},{id:'a-billing',label:'模型与计费'},{id:'a-ep-models',label:'模型目录'},{id:'a-chat',label:'对话与流式'},{id:'a-errors',label:'错误处理'},{id:'a-concepts',label:'概念教程'},{id:'a-tools',label:'工具与其他端点'}]
const examples: Record<string,string> = {
  curl: `curl https://api.ltzy.top/v1/chat/completions \\\n  -H "Authorization: Bearer sk-你的密钥" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"acu/deepseek-v4-flash","messages":[{"role":"user","content":"你好"}]}'`,
  python: `from openai import OpenAI
client = OpenAI(api_key="sk-你的密钥", base_url="https://api.ltzy.top/v1")
response = client.chat.completions.create(
    model="acu/deepseek-v4-flash",
    messages=[{"role": "user", "content": "你好"}]
)
print(response.choices[0].message.content)`,
  javascript: `import OpenAI from "openai";
const client = new OpenAI({
  apiKey: process.env.AQUA_API_KEY,
  baseURL: "https://api.ltzy.top/v1"
});
const response = await client.chat.completions.create({
  model: "acu/deepseek-v4-flash",
  messages: [{ role: "user", content: "你好" }]
});
console.log(response.choices[0].message.content);`
}
const modelCode = 'curl https://api.ltzy.top/v1/models'
const streamCode = `stream = client.chat.completions.create(
    model="acu/deepseek-v4-flash",
    messages=[{"role":"user","content":"你好"}],
    stream=True,
)
for chunk in stream:
    if chunk.choices:
        print(chunk.choices[0].delta.content or "", end="", flush=True)`
function jump(event: Event) {
  const id = (event.target as HTMLSelectElement).value
  location.hash = id
}
</script>
<template>
  <main class="wrap docs">
    <div class="page-head"><div><h1>从第一条请求开始。</h1><p class="sub">接口配置、模型费用与错误排查，按接入顺序阅读。</p></div><router-link class="btn" to="/console">创建 API 密钥</router-link></div>
    <label class="mobile-toc">选择章节<select class="select" @change="jump"><option v-for="s in sections" :key="s.id" :value="s.id">{{ s.label }}</option></select></label>
    <div class="docs-layout">
      <nav class="toc" aria-label="文档目录"><a v-for="s in sections" :key="s.id" :href="`#${s.id}`">{{ s.label }}</a></nav>
      <div class="doc-body">
        <section id="a-start"><h2>快速开始</h2><p>在<router-link to="/console">工作台</router-link>创建密钥，在<router-link to="/models">模型与价格</router-link>确认完整模型 ID 与费用，再发送请求。</p><div class="base-url"><code>https://api.ltzy.top/v1</code><CopyBtn text="https://api.ltzy.top/v1" /></div><div class="row wrap"><button v-for="lang in Object.keys(examples)" :key="lang" class="chip" :class="{on: language === lang}" :aria-pressed="language === lang" @click="language = lang">{{ lang }}</button><CopyBtn :text="examples[language]" label="复制示例" /></div><pre class="code mt12">{{ examples[language] }}</pre><p class="note">示例使用 acu/ 官方自营纯免费线路。将占位密钥替换为自己的 API 密钥；不要把密钥写进公开仓库或网页客户端。Python / JavaScript 示例需先安装对应 OpenAI SDK，JavaScript 示例运行于服务端。</p></section>
        <section id="a-billing"><h2>模型与计费</h2><div class="tbl-wrap"><table class="table"><thead><tr><th>线路</th><th>计费方式</th><th>资金口径</th></tr></thead><tbody><tr><td><code>acu/</code></td><td>官方自营纯免费</td><td>不扣个人余额，与收费线路独立</td></tr><tr><td><code>aqua/</code></td><td>按次计费</td><td>每次成功请求按模型单价结算</td></tr><tr><td><code>codex/</code></td><td>按量计费</td><td>输入、缓存命中、输出分别计价</td></tr></tbody></table></div><p>价格与可用性以接口和模型页为准，不要省略模型前缀。其他线路按其模型详情标示的方式计费。个人充值与自愿赞助是不同资金用途。</p><p>代理身份由服务端账号分组决定；有专价时显示结算价与官网零售价对照。价差未扣除经营成本，不代表保证收益。</p></section>
        <section id="a-ep-models"><h2>GET /v1/models</h2><p>获取当前模型目录。携带个人凭据时，接口可返回对应账号的价格视角。不要把登录态价目放进跨用户公共缓存。</p><pre class="code">{{ modelCode }}</pre><CopyBtn :text="modelCode" class="mt8" /><p><code>price_micro</code> 为微元单价（1 元 = 1,000,000 微元）；按量模型分别查看输入、缓存、输出价格，不与按次单价直接比较。</p></section>
        <section id="a-chat"><span id="a-ep-chat"></span><h2>POST /v1/chat/completions</h2><p>必填 <code>model</code> 与 <code>messages</code>。多轮对话需在 messages 中携带历史消息。参数与上下文限制按模型详情确认。</p><div class="tbl-wrap"><table class="table"><thead><tr><th>参数</th><th>说明</th></tr></thead><tbody><tr><td>model</td><td>完整模型 ID，包含线路前缀</td></tr><tr><td>messages</td><td>由 role 与 content 组成的消息数组</td></tr><tr><td>stream</td><td>设为 true 接收 SSE 流式响应</td></tr><tr><td>temperature / max_tokens</td><td>控制生成风格和输出上限，支持情况与范围因模型而异</td></tr></tbody></table></div><h3>流式示例（Python）</h3><pre class="code">{{ streamCode }}</pre><CopyBtn :text="streamCode" class="mt8" /><p class="note">HTTP 200 仅表示连接建立，不保证流式请求最终成功。客户端还应处理错误事件、断流与取消；请勿在已经输出内容后自动重放请求。</p></section>
        <section id="a-errors"><h2>错误处理</h2><div class="tbl-wrap"><table class="table"><thead><tr><th>情况</th><th>下一步</th></tr></thead><tbody><tr><td>401 / 403</td><td>检查密钥是否有效、分组是否支持所选线路，不要反复重试相同凭据。</td></tr><tr><td>404 / 410</td><td>核对完整模型 ID 与目录状态，不要猜测其他线路替代。</td></tr><tr><td>429</td><td>读取 error.code / message，区分限流和余额不足；余额不足不等于上游故障。</td></tr><tr><td>5xx / 断流</td><td>查看服务状态；对未输出的请求谨慎退避重试，避免重复调用。</td></tr></tbody></table></div><p>排查时保存时间、模型、状态码和请求标识。向社区反馈前遮盖密钥、会话令牌和个人信息。</p></section>
        <section id="a-concepts"><span id="a-intro"></span><h2>概念教程</h2><details><summary>Base URL、密钥和模型分别是什么？</summary><p>Base URL 是接口入口；API 密钥证明调用身份；model 选择具体模型与线路。三者各司其职，更换模型时先确认价格和密钥分组。</p></details><details><summary>Token、上下文与缓存</summary><p>Token 是模型处理文本的计量单位，不等于汉字数。上下文包含输入与输出；按量模型的缓存命中价格由实际响应和结算决定，不是客户端声明命中就可享受。</p></details><details><summary>向量、重排与 RAG</summary><p>向量用于相似度检索，重排用于对候选内容重新排序。RAG 将检索资料加入提示词辅助回答，不能保证答案完全正确。</p></details></section>
        <section id="a-tools"><span id="a-special"></span><h2>工具与其他端点</h2><p>以下保留可编程调用的端点参考。模型能力、权限和费用以当前目录为准；工具中涉及 AI 的能力不应视为纯算法免费接口。需要鉴权时使用 <code>Authorization: Bearer sk-你的密钥</code>。</p><div class="tbl-wrap"><table class="table"><thead><tr><th>方法与端点</th><th>输入与用途</th></tr></thead><tbody>
          <tr id="a-ep-mod"><td><code>POST /v1/moderations</code></td><td><code>{"model":"目录中的风控模型 ID","input":"待检测文本"}</code>；内容安全检测。</td></tr>
          <tr id="a-ep-img"><td><code>POST /v1/images/generations</code></td><td><code>{"model":"目录中的图片模型 ID","prompt":"图片描述"}</code>；先确认模型支持的参数与按张费用。</td></tr>
          <tr id="a-ep-asset"><td><code>GET /assets/{key}</code></td><td>访问生成接口返回的媒体地址，使用响应中的完整 URL，不自行猜测资源 key。</td></tr>
          <tr><td><code>POST /v1/tools/text-stats</code></td><td><code>{"text":"..."}</code>；文本统计。</td></tr>
          <tr><td><code>POST /v1/tools/dice</code></td><td><code>{"sides":6,"count":1}</code>；随机骰子。</td></tr>
          <tr><td><code>GET /v1/tools/uuid</code><br><code>POST /v1/tools/uuid-bulk</code></td><td>生成 UUID；批量请求 <code>{"count":10}</code>。</td></tr>
          <tr><td><code>GET /v1/tools/timestamp</code><br><code>POST /v1/tools/timestamp</code></td><td>当前时间；转换请求 <code>{"timestamp":123}</code>。</td></tr>
          <tr><td><code>POST /v1/tools/base64</code></td><td><code>{"action":"encode","text":"..."}</code>；action 为 encode / decode。</td></tr>
          <tr><td><code>POST /v1/tools/subnet</code></td><td><code>{"cidr":"192.168.1.0/24"}</code>；子网计算。</td></tr>
          <tr><td><code>POST /v1/tools/treehole</code></td><td><code>{"mode":"gentle","messages":[...]}</code>；树洞对话，mode 为 gentle / anime。</td></tr>
          <tr><td><code>GET /v1/tools/prompts</code></td><td>提示词目录。</td></tr>
          <tr><td><code>POST /v1/tools/translate</code></td><td><code>{"text":"...","to":"en"}</code>；AI 翻译。</td></tr>
          <tr><td><code>POST /v1/tools/url-summary</code></td><td><code>{"url":"https://..."}</code>；网页摘要。</td></tr>
          <tr><td><code>POST /v1/tools/hash</code></td><td><code>{"text":"...","algo":"sha256"}</code>；哈希计算。</td></tr>
          <tr><td><code>POST /v1/tools/password</code></td><td><code>{"length":16,"count":5}</code>；密码生成。</td></tr>
          <tr><td><code>POST /v1/tools/json</code></td><td><code>{"text":"..."}</code>；JSON 校验与格式化。</td></tr>
          <tr><td><code>POST /v1/tools/regex</code></td><td><code>{"pattern":"...","text":"...","flags":"g"}</code>；正则匹配。</td></tr>
          <tr><td><code>POST /v1/tools/color</code></td><td><code>{"input":"#3b82f6"}</code>；颜色转换。</td></tr>
          <tr><td><code>POST /v1/tools/url-code</code></td><td><code>{"text":"...","mode":"encode"}</code>；URL 编解码。</td></tr>
          <tr><td><code>POST /v1/tools/token-count</code></td><td><code>{"text":"..."}</code>；Token 近似估算，非结算依据。</td></tr>
          <tr><td><code>POST /v1/tools/shorten</code></td><td><code>{"url":"https://..."}</code>；生成短链。</td></tr>
          <tr><td><code>POST /v1/tools/webhook</code></td><td><code>{"action":"create|list|clear","id":"..."}</code>；创建、查看或清空收集器。</td></tr>
          </tbody></table></div><p>也可在<router-link to="/tools">工具箱</router-link>使用对应功能。不要把对话参数直接套到其他接口。</p></section>
        <section id="a-open"><h2>开放数据与社区 API</h2><div class="tbl-wrap"><table class="table"><thead><tr><th>方法与端点</th><th>鉴权与用途</th></tr></thead><tbody>
          <tr><td><code>GET /v1/stats</code><br><code>GET /status</code><br><code>GET /v1/meta</code></td><td>公开统计、服务健康与站点元数据，无需密钥。</td></tr>
          <tr><td><code>GET /v1/usage</code></td><td>Bearer API 密钥；查询该密钥的用量。</td></tr>
          <tr><td><code>POST /v1/arena</code></td><td>Bearer API 密钥；<code>{"prompt":"...","max_tokens":512}</code> 发起盲测。</td></tr>
          <tr><td><code>POST /v1/arena/vote</code></td><td>Bearer API 密钥；<code>{"battle_id":"...","winner":"a|b|tie"}</code> 投票。</td></tr>
          <tr><td><code>GET /v1/arena/leaderboard</code></td><td>公开排行榜，无需密钥。</td></tr>
          <tr><td><code>GET /s/{code}</code></td><td>站点域名下的短链跳转。</td></tr>
          <tr><td><code>ANY /hook/{id}</code></td><td>Webhook 收集地址；不要发送密钥或个人敏感信息。</td></tr>
          </tbody></table></div></section>
        <section id="a-integration"><span id="a-faq"></span><h2>接入检查清单</h2><ul><li>Base URL 结尾包含 /v1。</li><li>使用 API 密钥，而非网页登录会话令牌。</li><li>完整保留 acu/、aqua/、codex/ 等前缀。</li><li>先核对价格、权限与可用性，再发送业务请求。</li></ul><router-link to="/community">社区与支持 ↗</router-link></section>
      </div>
    </div>
  </main>
</template>
<style scoped>
.docs { padding-bottom:48px; }
.docs-layout { display:grid; grid-template-columns:190px minmax(0,1fr); gap:56px; margin-top:36px; }
.toc { display:flex; flex-direction:column; align-self:start; position:sticky; top:92px; border-left:1px solid var(--line); }
.toc a { padding:10px 20px; color:var(--txt2); font-size:14px; }
.toc a:hover { background:var(--acc-soft); color:var(--acc); }
.doc-body { max-width:850px; min-width:0; }
section { padding:0 0 40px; margin-bottom:36px; border-bottom:1px solid var(--line); scroll-margin-top:90px; }
section h2 { margin-bottom:20px; font-size:26px; }
section h3 { margin:24px 0 16px; }
section p { margin:16px 0; line-height:1.9; }
.base-url { display:flex; align-items:center; justify-content:space-between; flex-wrap:wrap; gap:12px; padding:16px; margin:20px 0; background:var(--bg1); border:1px solid var(--line); border-radius:10px; }
.base-url code { overflow-wrap:anywhere; }
.note { color:var(--txt2); font-size:13px; }
.mobile-toc { display:none; }
details { border-bottom:1px solid var(--line); padding:18px 0; }
summary { cursor:pointer; color:var(--txt0); min-height:32px; }
ul { padding-left:22px; margin-bottom:20px; }
@media(max-width:800px) { .docs-layout { grid-template-columns:minmax(0,1fr); margin-top:24px; } .toc { display:none; } .mobile-toc { display:grid; gap:8px; color:var(--txt2); } section h2 { font-size:23px; } }
</style>
