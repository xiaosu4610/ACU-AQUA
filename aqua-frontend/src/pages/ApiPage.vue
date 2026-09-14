<script setup lang="ts">
/* API 文档 · 左锚点导航（sticky）+ 右文档正文；每个端点一张 .card
 * 本页为纯静态文档（与旧版 1:1 平移，无接口调用、无 localStorage 依赖）；
 * 复制按钮统一 CopyBtn，text 为等价纯文本代码常量 */
import { onMounted, onUnmounted, ref } from 'vue'
import AqIcon from '@/components/AqIcon.vue'
import CopyBtn from '@/components/CopyBtn.vue'

/* ================= 复制文本常量（与旧版逐字一致） ================= */
const codeQuickCurl = `curl https://api.ltzy.top/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer sk-你的密钥" \\
  -d '{"model": "gpt-oss-20b", "messages": [{"role": "user", "content": "用一句话介绍你自己"}]}'`

const codePaidCurl = `curl https://api.ltzy.top/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer sk-你的密钥" \\
  -d '{"model": "aqua/deepseek-v4-flash", "messages": [{"role": "user", "content": "你好"}]}'`

const codeVolCurl = `curl https://api.ltzy.top/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer sk-你的密钥" \\
  -d '{"model": "aqua/glm-5.3-flash", "messages": [{"role": "user", "content": "你好"}], "max_tokens": 2048}'`

const codeQuickPy = `from openai import OpenAI

client = OpenAI(
    api_key="sk-你的密钥",                                # 官网控制台创建
    base_url="https://api.ltzy.top/v1",                   # 注意结尾带 /v1
)
r = client.chat.completions.create(
    model="gpt-oss-20b",                            # 去模型中心随便抄一个
    messages=[{"role": "user", "content": "用一句话介绍你自己"}],
)
print(r.choices[0].message.content)                       # 打印 AI 的回答`

const codeQuickJs = `import OpenAI from "openai";

const client = new OpenAI({
  apiKey: "sk-你的密钥",                                  // 官网控制台创建
  baseURL: "https://api.ltzy.top/v1",                     // 结尾带 /v1
});
const r = await client.chat.completions.create({
  model: "gpt-oss-20b",
  messages: [{ role: "user", content: "用一句话介绍你自己" }],
});
console.log(r.choices[0].message.content);`

const codeMultiTurn = `# 第二轮提问时，messages 里要带上第一轮的全部内容：
messages = [
  {"role": "system",   "content": "你是一个耐心的编程老师"},   # 人设说明书
  {"role": "user",     "content": "Python 的列表是什么？"},       # 第 1 问
  {"role": "assistant", "content": "列表像一个能装任何东西的收纳盒…"}, # 第 1 答（也要带上！）
  {"role": "user",     "content": "那字典呢？"}                    # 第 2 问
]
r = client.chat.completions.create(model="gpt-oss-20b", messages=messages)`

const codeStreamPy = `# Python：一行开启流式
stream = client.chat.completions.create(
    model="gpt-oss-20b",
    messages=[{"role": "user", "content": "讲个冷笑话"}],
    stream=True,                          # ← 打字机开关
)
for chunk in stream:                      # 每次循环 = 一小段
    piece = chunk.choices[0].delta.content or ""
    print(piece, end="", flush=True)     # 逐段打印，肉眼可见的打字机`

const codeStreamJs = `// JS（浏览器原生 SSE，无需任何库）
const res = await fetch("https://api.ltzy.top/v1/chat/completions", {
  method: "POST",
  headers: { "Content-Type": "application/json", "Authorization": "Bearer sk-你的密钥" },
  body: JSON.stringify({ model: "gpt-oss-20b", stream: true,
    messages: [{ role: "user", content: "讲个冷笑话" }] }),
});
const reader = res.body.getReader(), dec = new TextDecoder();
let buf = "", full = "";
while (true) {
  const { done, value } = await reader.read();
  if (done) break;
  buf += dec.decode(value, { stream: true });
  const lines = buf.split("\\n"); buf = lines.pop();
  for (const line of lines) {
    if (!line.startsWith("data: ")) continue;
    const data = line.slice(6);
    if (data === "[DONE]") break;        // 水管关闭信号
    const j = JSON.parse(data);
    full += j.choices[0].delta.content || "";
    // 每收到一段就把 full 更新到界面（如 box.textContent = full）→ 打字机效果
  }
}
console.log(full);`

const codeApiModelsResp = `{"object": "list",
  "data": [{
    "id": "gpt-oss-20b",
    "object": "model",
    "owned_by": "nvidia"
  }]}`

const codeApiModelsCurl = `curl https://api.ltzy.top/v1/models
  -H "Authorization: Bearer sk-****"`

const codeApiChatBody = `{"model": "gpt-oss-20b",
  "messages": [
    {"role": "system", "content": "你是 AQUA 助手"},
    {"role": "user", "content": "你好"}
  ],
  "temperature": 0.7,
  "stream": true  // 可选，流式返回
}`

const codeApiChatPy = `from openai import OpenAI
client = OpenAI(api_key="sk-****", base_url="https://api.ltzy.top/v1")
r = client.chat.completions.create(
  model="gpt-oss-20b",
  messages=[{"role": "user", "content": "你好"}])
print(r.choices[0].message.content)`

const codeApiMod = `curl https://api.ltzy.top/v1/moderations \\
  -H "Content-Type: application/json" \\
  -d '{"model": "llama-3.1-nemoguard-8b-content-safety", "input": "待检测文本"}'`

const codeApiImg = `curl https://api.ltzy.top/v1/images/generations \\
  -H "Content-Type: application/json" \\
  -d '{"model": "aqua/文生图模型ID", "prompt": "一只坐在沙发上的橘猫", "size": "1024x1024"}'`

const codeApiAsset = `curl https://api.ltzy.top/assets/2026/08/abc123.png \\
  -H "Authorization: Bearer sk-****"`

const codeApiErr = `{"error": {
  "message": "上游限流或额度受限，请稍后重试或更换模型（上游说明：…）",
  "type": "aqua_api_error",
  "code": "RATE_LIMITED",
  "status": 429,
  "hint": "如需帮助：加入 QQ 频道 pd57362562（https://pd.qq.com/s/e4ktxw1b8）反馈，或访问官网 https://acu.ltzy.top",
  "help": { "site": "https://acu.ltzy.top", "docs": "https://acu.ltzy.top/#/api",
            "qq_guild": "pd57362562", "qq_guild_url": "https://pd.qq.com/s/e4ktxw1b8",
            "qq_guild_invite": "公告与支持见 QQ 频道（频道号 pd57362562）；注册登录后在官网控制台创建 API 密钥",
            "qq_group": 1103667832, "qq_group2": 1006740220, "qq_group2_url": "https://qm.qq.com/q/o8QDbza2Ge" }
}}`

/* ================= 左侧锚点导航（IntersectionObserver 高亮） ================= */
const TOC = [
  { id: 'a-intro', label: '零基础入门' },
  { id: 'a-start', label: '三分钟跑通' },
  { id: 'a-concepts', label: '核心概念词典' },
  { id: 'a-billing', label: '计费说明' },
  { id: 'a-chat', label: '对话接口进阶' },
  { id: 'a-ep-models', label: 'GET /v1/models' },
  { id: 'a-ep-chat', label: 'POST /v1/chat/completions' },
  { id: 'a-ep-mod', label: 'POST /v1/moderations' },
  { id: 'a-ep-img', label: 'POST /v1/images/generations' },
  { id: 'a-ep-asset', label: 'GET /assets/{key}' },
  { id: 'a-special', label: '特殊模型说明' },
  { id: 'a-errors', label: '错误与状态码' },
  { id: 'a-tools', label: '工具 API' },
  { id: 'a-open', label: '开放数据与社区' },
  { id: 'a-integration', label: '对接指南' },
  { id: 'a-faq', label: 'FAQ' },
]
const activeId = ref(TOC[0].id)
let io: IntersectionObserver | null = null
onMounted(() => {
  io = new IntersectionObserver(entries => {
    for (const e of entries) if (e.isIntersecting) activeId.value = (e.target as HTMLElement).id
  }, { rootMargin: '-76px 0px -66% 0px' })
  for (const t of TOC) {
    const el = document.getElementById(t.id)
    if (el) io.observe(el)
  }
})
onUnmounted(() => io?.disconnect())
function jump(id: string) {
  document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
</script>

<template>
  <div class="wrap">
    <div class="page-head">
      <div>
        <h1><AqIcon name="book" :size="22" />API 文档</h1>
        <div class="sub">
          AQUA 网关完整兼容 OpenAI 协议，所有端点均可直接用 OpenAI SDK 调用。
          <b>API Key 账号制：注册登录后，在个人控制台一键创建密钥（sk-****），注册免费、随时吊销重建</b>；模型列表以「模型中心」为准（实时同步）。
        </div>
      </div>
      <div class="ops">
        <a class="btn sm" href="https://gitee.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener"><AqIcon name="star" :size="13" />Gitee</a>
        <a class="btn sm" href="https://github.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener"><AqIcon name="star" :size="13" />GitHub</a>
        <router-link class="btn sm primary" to="/models"><AqIcon name="box" :size="13" />模型中心</router-link>
      </div>
    </div>

    <div class="api-layout">
      <!-- ================= 左：锚点导航 ================= -->
      <aside class="toc card">
        <div class="toc-title"><AqIcon name="list" :size="14" />本页目录</div>
        <button v-for="t in TOC" :key="t.id" class="toc-item" :class="{ on: activeId === t.id }" @click="jump(t.id)">
          {{ t.label }}
        </button>
      </aside>

      <!-- ================= 右：文档正文 ================= -->
      <div class="doc fade-up">
        <!-- ===== 零基础入门 ===== -->
        <section id="a-intro" class="card sec">
          <h2>零基础入门：先别急着写代码</h2>
          <p class="desc">第一次接触 API？没问题——这一节全程说人话，把该懂的词都讲明白。已经会用的同学，直接往下看端点详解。</p>
          <h3>API 是什么？——一家「自动点餐」的餐厅</h3>
          <p class="desc">想象一家餐厅：<b>你（你的程序）</b>写好一份<b>点菜单（请求）</b>递给<b>服务员（API）</b>，服务员把单子送进<b>后厨（AI 模型）</b>，做好菜再把<b>结果（回复）</b>端出来。你不需要知道后厨怎么炒的——会递单子、能接菜，就够了。<b>API 就是这套「递单子 → 出菜」的规矩</b>，大家按同一个规矩来，程序之间就能互相使唤。</p>
          <h3>API 网关是什么？——前台总服务台</h3>
          <p class="desc">AQUA 就是那个前台。后面有几家后厨（Nvidia NIM、官方自营专线……几十个模型），但你只跟前台一个窗口打交道：<b>报一个菜名（模型 ID），前台自动转给对应的后厨</b>。免费模型的菜名是小写短 ID（比如 <code>gpt-oss-20b</code>），官方自营收费模型带 <code>aqua/</code> 前缀（比如 <code>aqua/deepseek-v4-flash</code>），前台拿着菜名查自己的「菜单目录」，就知道该送往哪家后厨。</p>
          <h3>API Key（密钥）是什么？——一张自助领取的门票</h3>
          <p class="desc">大部分网站的 API Key 像<b>演唱会门票</b>：要实名申请、要花钱、丢了要补办。AQUA 的 Key 是一张<b>自助领取的门票</b>：<b>注册登录后，进「个人控制台」一键创建</b>，立刻可用——<b>注册免费、创建自助、随时吊销重建</b>。每个密钥有独立备注和用量统计，丢了就吊销重发，不用问任何人。</p>
          <h3>一次调用的完整旅程</h3>
          <pre class="code">你的程序                          AQUA 网关                        AI 模型（后厨）
   │                                 │                                │
   │  ① 递单子：带密钥的 HTTP 请求     │                                │
   │  「模型 deepseek-v4-flash，   │  ② 看菜名找后厨                 │
   │   用户问：你好」  ───────────────►│  查目录 → 官方自营后厨 ────────►│  ③ 思考并作答
   │                                 │                                │
   │  ④ 端菜：JSON 回复               │  ◄─────────────  原样转交        │
   │  「你好！很高兴见到你」 ◄─────────│                                │
   ▼</pre>
          <p class="desc">所以对接 AQUA 你只需要记三样东西：<b>① 接口地址</b>（本站 <code>https://api.ltzy.top/v1</code>）；<b>② 密钥</b>（控制台创建）；<b>③ 模型 ID</b>（去「模型中心」抄）。往下看，三分钟跑通第一条请求。</p>
        </section>

        <!-- ===== 三分钟跑通 ===== -->
        <section id="a-start" class="card sec">
          <h2>三分钟跑通你的第一条请求</h2>
          <p class="desc">注册一个账号、创建一把密钥，不用充值、不用等审核。跟着做，三步出结果。</p>
          <h3>第 1 步：准备两样东西</h3>
          <ul class="tips">
            <li><b>接口地址</b>：<code>https://api.ltzy.top/v1</code>（自部署的同学换成你自己的网关域名）</li>
            <li><b>密钥</b>：<router-link to="/login">注册登录</router-link>后，在<router-link to="/console">个人控制台</router-link>创建——形如 <code>sk-****</code></li>
          </ul>
          <h3>第 2 步：挑一种你喜欢的方式，复制 → 运行</h3>
          <p class="desc"><b>方式一：命令行 curl</b>（电脑自带，直接粘贴到终端）：</p>
          <div class="codewrap"><div class="row between codetop"><span class="tag">cURL</span><CopyBtn :text="codeQuickCurl" /></div><pre class="code flat">{{ codeQuickCurl }}</pre></div>
          <p class="desc"><b>方式二：Python</b>（先 <code>pip install openai</code>）：</p>
          <div class="codewrap"><div class="row between codetop"><span class="tag">Python</span><CopyBtn :text="codeQuickPy" /></div><pre class="code flat">{{ codeQuickPy }}</pre></div>
          <p class="desc"><b>方式三：JavaScript / Node.js</b>：</p>
          <div class="codewrap"><div class="row between codetop"><span class="tag">JavaScript</span><CopyBtn :text="codeQuickJs" /></div><pre class="code flat">{{ codeQuickJs }}</pre></div>
          <h3>第 3 步：看懂返回结果（每个字段是什么）</h3>
          <pre class="code">{
  "id": "chatcmpl-xxxx",          // 这次对话的编号（流水号，出问题反馈时报它）
  "model": "gpt-oss-20b",   // 实际干活的模型
  "choices": [{                    // 回答在这里（数组，一般只取第 0 个）
    "message": {
      "role": "assistant",         // 说话的人是 AI
      "content": "你好！我是 GPT-OSS-20B…"  // 注意：你要的答案就是它
    },
    "finish_reason": "stop"        // stop=正常说完；length=字数到上限被截断
  }],
  "usage": { ... }                 // 用量统计（本站免费，仅供参考）
}</pre>
          <h3>新手最容易踩的三个坑</h3>
          <ul class="tips">
            <li><b>base_url 忘了带 <code>/v1</code></b>：写成 <code>https://api.ltzy.top</code> 会 404。SDK 的 base_url 要以 <code>/v1</code> 结尾。</li>
            <li><b>密钥没填或填错</b>：密钥是控制台创建的 <code>sk-****</code>，一字不差地复制；每个密钥创建时只完整展示一次，丢了就吊销重建。</li>
            <li><b>模型 ID 抄错</b>：ID 要一字不差（区分大小写），拿不准就去「模型中心 · 模型列表」复制，或调 <code>GET /v1/models</code> 查。</li>
          </ul>
          <p class="desc">跑通了？恭喜，你已经会用 80% 的功能了——剩下的端点全是「换个地址、换个参数」的重复动作。继续往下看概念词典和端点详解。</p>
        </section>

        <!-- ===== 概念词典 ===== -->
        <section id="a-concepts" class="card sec">
          <h2>说人话 · 核心概念词典</h2>
          <p class="desc">对接时常见的专业术语，每个都用大白话+比喻讲一遍。看不懂某段文档时回来查这张表。</p>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>术语</th><th>大白话解释（人话版）</th><th>什么时候关心它</th></tr></thead>
              <tbody>
                <tr><td><code>messages</code></td><td><b>聊天记录本</b>。AI 本身没有记忆——你把之前的对话从头复制进这个数组，它才「记得」聊过什么。</td><td>每次调用都要传；多轮对话的灵魂</td></tr>
                <tr><td><code>role</code> 三种角色</td><td><b>system=导演</b>（幕后定人设和规矩，观众看不见）；<b>user=观众</b>（你的提问）；<b>assistant=演员</b>（AI 的回答）。多轮对话=把整本聊天记录按顺序再念一遍。</td><td>构造 messages 时</td></tr>
                <tr><td><code>temperature</code></td><td><b>创造力旋钮</b>（0~2）。0=严谨复读机，问什么答什么（适合数学/代码/信息提取）；0.7=日常聊天刚刚好；1.5 以上=天马行空（适合创意写作），太高会开始胡说。</td><td>想要稳定答案调低，想要创意调高</td></tr>
                <tr><td><code>max_tokens</code></td><td><b>回答的字数上限</b>。相当于给 AI 的工作台大小设了限——防止它写三千字小作文。若返回里 <code>finish_reason=length</code> 就是说到一半被掐断了。</td><td>控制回复长度 / 省流量</td></tr>
                <tr><td><code>top_p</code></td><td>另一个创造力旋钮（0~1），和 temperature 干的是同一类活。两个都调容易打架，<b>调一个就行</b>。</td><td>一般不用动</td></tr>
                <tr><td><code>stream</code></td><td><b>打字机模式</b>开关。<code>false</code>=AI 憋个大招一次性整段返回；<code>true</code>=像打字一样一个字一个字往外蹦（SSE 流式），等待体验好得多。</td><td>做聊天界面强烈建议开</td></tr>
                <tr><td>token</td><td><b>AI 的积木块</b>。AI 不是按「字」读文本的，而是按 token：1 个汉字≈1~2 个 token，1 个英文单词≈1 个。各种长度限制都数它。</td><td>看懂 max_tokens / 上下文窗口</td></tr>
                <tr><td>上下文窗口</td><td><b>模型的工作记忆（桌面大小）</b>。桌面就那么大，聊天记录堆太多 earliest 的会被挤出去——长对话记得自己裁剪 messages（比如只带最近 20 条）。</td><td>长对话 / 长文档处理</td></tr>
                <tr><td>system prompt<br>（提示词）</td><td><b>AI 的人设说明书</b>。放在 messages 第一条（role=system），告诉它「你是谁、说话什么风格、什么能做什么不能做」。本站提示词工坊收录了大量现成的人设，可直接抄。</td><td>想给 AI 定人设 / 定规矩</td></tr>
                <tr><td>幻觉</td><td><b>AI 一本正经地编造事实</b>。它天生是个「想象力过剩的优等生」，不知道也硬答。数字、引用、法条、日期这类硬信息，务必自己核实。</td><td>任何时候都要留个心眼</td></tr>
                <tr><td>RAG</td><td><b>先翻资料再回答</b>。把检索到的资料塞进 prompt 里让 AI「照着资料答」，能大幅减少幻觉——开卷考试总比闭卷靠谱。</td><td>知识库问答 / 客服机器人</td></tr>
                <tr><td>embeddings<br>（向量/嵌入）</td><td><b>给文字发 GPS 坐标</b>。把一段文字变成一串数字（向量），意思越接近的文字坐标越靠近——「找相似」就变成了「算距离」。语义搜索、推荐、聚类全靠它。</td><td>语义搜索 / 去重 / 分类</td></tr>
                <tr><td>rerank<br>（重排）</td><td><b>给检索结果排座位的裁判</b>。先粗筛出 100 条候选，再让裁判按「和问题的相关度」重新排座次，取前几名。</td><td>RAG 流程的第二道工序</td></tr>
                <tr><td>ASR / TTS</td><td>AI 的<b>耳朵</b>和<b>嘴巴</b>。ASR（语音识别）=听录音转文字；TTS（语音合成）=把文字读出声来。</td><td>语音助手 / 有声内容</td></tr>
                <tr><td>SSE</td><td><b>服务器到你程序之间的一根小水管</b>。数据一段一段主动流过来，不用你反复问「好了没」。stream=true 时用的就是它。</td><td>处理流式回复时</td></tr>
              </tbody>
            </table>
          </div>
        </section>

        <!-- ===== 计费说明 ===== -->
        <section id="a-billing" class="card sec">
          <h2>计费说明：免费模型与收费模型</h2>
          <p class="desc">先把结论说清楚：<b>本站绝大多数模型完全免费，且会一直免费</b>。其中 <code>acu/</code> 前缀为<b>众筹公共模型</b>——任何密钥都能调，按次从站点公共额度扣费、个人余额分文不动。真正用余额付费的只有<b>官方自营收费系列</b>——统一使用 <code>aqua/</code> 前缀，采用<b>按次计费</b>（控制台创建密钥时选择「免费 + 按次计费」分组即可调用）。</p>
          <h3>哪些免费？——除它之外全部免费</h3>
          <ul class="tips">
            <li>Nvidia NIM 与官方自营<b>免费通道</b>的全部模型（对话/视觉/风控等）：<b>不收一分钱</b>，注册即可用。</li>
            <li>免费模型的用量统计照常记录，但<b>仅用于你在控制台查看</b>，不产生任何费用，今后也不会对这些模型收费。</li>
            <li>没有余额、不充值，免费模型<b>照样随便用</b>，二者互不影响。</li>
          </ul>
          <h3>哪些收费？——aqua/ 收费系列（统一前缀）</h3>
          <p class="desc">官方自营收费模型<b>统一 <code>aqua/</code> 前缀</b>，<b>按次计费</b>：每次成功请求扣一次，与生成长度无关（写一句话和写一千字同价），覆盖 DeepSeek / GLM / Kimi 全系列旗舰。调用方式和免费模型<b>完全一样</b>——同一个接口、同一套 SDK，只是 <code>model</code> 字段带上 <code>aqua/</code> 前缀：</p>
          <div class="codewrap"><div class="row between codetop"><span class="tag grad">收费模型示例</span><CopyBtn :text="codePaidCurl" /></div><pre class="code flat">{{ codePaidCurl }}</pre></div>
          <p class="desc">支持 <code>stream: true</code> 流式输出，效果与免费模型一致，对客户端完全透明。需先<b>注册登录</b>并使用<b>个人密钥</b>调用（会话令牌调用收费模型会被拒绝）。各模型实时单价见「模型中心」收费模型专区或 <code>/v1/models</code> 返回的 <code>price_micro</code> 字段。</p>
          <h3>计费方式——按次计费（每次成功请求扣一次）</h3>
          <p class="desc">收费模型<b>按次收费</b>：每次成功请求扣一次单价，与 tokens 用量、生成长度无关，价格透明可预期；失败请求<b>一分钱不收</b>。调用示例：</p>
          <div class="codewrap"><div class="row between codetop"><span class="tag grad">按次计费示例</span><CopyBtn :text="codeVolCurl" /></div><pre class="code flat">{{ codeVolCurl }}</pre></div>
          <p class="desc">收费模型全线<b>先付后用</b>：发起请求前按单价预扣，完成后按实际结果结算；上游失败、网络中断时预扣金额<b>自动全额退回</b>，绝不透支。</p>
          <h3>收费政策与资金安全（五条铁律）</h3>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>规则</th><th>具体内容</th><th>对你的意义</th></tr></thead>
              <tbody>
                <tr><td><b>按次计费</b></td><td><b>按次分组密钥</b>：每次成功请求扣一次单价，与 tokens 用量、生成长度无关，价格透明可预期。</td><td>一句话和一千字同价，失败全额退回</td></tr>
                <tr><td><b>预充值制</b></td><td>余额<b>在线充值即时到账</b>，每次请求前预扣，余额不足直接返回 <code>402</code>，绝不透支。</td><td>永远不可能「不知不觉欠费」</td></tr>
                <tr><td><b>失败不扣费</b></td><td>上游失败、网络中断、服务异常——预扣金额<b>自动全额退回</b>，错误请求一分钱不收。</td><td>只为成功的结果付费</td></tr>
                <tr><td><b>流水可查</b></td><td>每一笔扣费/退回都永久留存：个人控制台「消费账单」随时核对，含时间、金额、余额、请求明细。</td><td>账目自己随时能查，不用找人对账</td></tr>
              </tbody>
            </table>
          </div>
          <h3>如何获得余额</h3>
          <p class="desc"><b>在线充值即时到账</b>：登录后进入<router-link to="/console?view=topup">个人控制台 · 余额充值</router-link>，支付宝 / 微信任一渠道支付，<b>支付金额 100% 全额到账</b>（渠道手续费由本站承担），到账立即可用。余额与消费在<router-link to="/console">个人控制台</router-link>顶部实时可见。</p>
          <p class="desc"><AqIcon name="shield" :size="14" /> <b>安全承诺</b>：收费走整数微元记账（无浮点误差）、每笔余额变动只增不删、审计记录带哈希链防篡改；你的余额、账单、密钥仅自己可见。免费模型不涉及任何资金，请放心使用。</p>
        </section>

        <!-- ===== 对话接口进阶 ===== -->
        <section id="a-chat" class="card sec">
          <h2>对话接口进阶：多轮对话、流式输出、参数速查</h2>
          <p class="desc">把 <code>/v1/chat/completions</code> 玩明白，等于会用了全部 AI 接口——其他端点都只是「换个地址、换个参数」。</p>
          <h3>参数速查表</h3>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>参数</th><th>必填</th><th>默认</th><th>大白话</th></tr></thead>
              <tbody>
                <tr><td><code>model</code></td><td>必填</td><td>—</td><td>点哪个「厨师」做菜，如 <code>deepseek-v4-flash</code></td></tr>
                <tr><td><code>messages</code></td><td>必填</td><td>—</td><td>聊天记录本（见上方词典），至少一条</td></tr>
                <tr><td><code>temperature</code></td><td>可选</td><td>1</td><td>创造力旋钮 0~2，代码/数学用 0，聊天用 0.7</td></tr>
                <tr><td><code>max_tokens</code></td><td>可选</td><td>模型上限</td><td>回答字数上限，防止小作文</td></tr>
                <tr><td><code>stream</code></td><td>可选</td><td>false</td><td>打字机模式开关</td></tr>
                <tr><td><code>top_p</code></td><td>可选</td><td>1</td><td>另一个创造力旋钮，与 temperature 二选一调</td></tr>
                <tr><td><code>stop</code></td><td>可选</td><td>—</td><td>「到这里就停」的哨兵词，AI 说到它立刻收工</td></tr>
              </tbody>
            </table>
          </div>
          <h3>多轮对话：为什么 AI 会「失忆」，怎么治</h3>
          <p class="desc">AI 没有记忆。每次调用都是一场<b>全新的、没有前世记忆</b>的对话——所谓多轮对话，就是<b>你每次都把之前的完整聊天记录一起递回去</b>：</p>
          <div class="codewrap"><div class="row between codetop"><span class="tag">多轮对话</span><CopyBtn :text="codeMultiTurn" /></div><pre class="code flat">{{ codeMultiTurn }}</pre></div>
          <ul class="tips">
            <li><b>AI 回答「失忆」了？</b>九成是没把历史 messages 带回去。</li>
            <li>记录太长会超出上下文窗口——常规做法：只带<b>最近 10~20 条</b>，或把更早的内容总结成一条。</li>
            <li>「我不是、我没有」式人格漂移：在 system 里重申人设，或在每轮开头追加一条 system 提醒。</li>
          </ul>
          <h3>流式输出：做出「打字机」效果</h3>
          <p class="desc">请求里加 <code>"stream": true</code>，服务器就会通过 SSE 小水管一段段推送。每一段都是一个标准 JSON（<code>data: {...}</code>），你把 <code>choices[0].delta.content</code> 拼接到界面上即可：</p>
          <div class="codewrap"><div class="row between codetop"><span class="tag">Python · 流式</span><CopyBtn :text="codeStreamPy" /></div><pre class="code flat">{{ codeStreamPy }}</pre></div>
          <div class="codewrap"><div class="row between codetop"><span class="tag">JS · 流式（原生 SSE）</span><CopyBtn :text="codeStreamJs" /></div><pre class="code flat">{{ codeStreamJs }}</pre></div>
          <p class="desc"><AqIcon name="bulb" :size="14" /> 本站「体验中心 · AI 对话」就是按这套逻辑实现的流式前端，可对照源码学习（仓库开源）。</p>
        </section>

        <!-- ===== 端点：GET /v1/models ===== -->
        <section id="a-ep-models" class="card sec">
          <div class="row between wrap" style="gap: 8px;">
            <div class="row"><span class="tag ok method">GET</span><code class="ep">/v1/models</code></div>
            <CopyBtn :text="codeApiModelsCurl" label="复制示例" />
          </div>
          <p class="desc">获取可用模型列表。返回 Nvidia NIM 与官方自营专线的全部模型（含收费模型价格与健康分字段）。</p>
          <h3>响应结构</h3>
          <pre class="code">{{ codeApiModelsResp }}</pre>
          <h3>curl</h3>
          <pre class="code">{{ codeApiModelsCurl }}</pre>
        </section>

        <!-- ===== 端点：POST /v1/chat/completions ===== -->
        <section id="a-ep-chat" class="card sec">
          <div class="row between wrap" style="gap: 8px;">
            <div class="row"><span class="tag acc method">POST</span><code class="ep">/v1/chat/completions</code></div>
            <CopyBtn :text="codeApiChatBody" label="复制请求体" />
          </div>
          <p class="desc">对话补全，支持流式（SSE）。根据填入的模型 ID 自动路由到对应上游。</p>
          <h3>请求体</h3>
          <pre class="code">{{ codeApiChatBody }}</pre>
          <h3>python</h3>
          <pre class="code">{{ codeApiChatPy }}</pre>
        </section>

        <!-- ===== 端点：POST /v1/moderations ===== -->
        <section id="a-ep-mod" class="card sec">
          <div class="row between wrap" style="gap: 8px;">
            <div class="row"><span class="tag acc method">POST</span><code class="ep">/v1/moderations</code></div>
            <CopyBtn :text="codeApiMod" label="复制示例" />
          </div>
          <p class="desc">内容安全检测。支持 llama-3.1-nemoguard-8b-content-safety、nemotron-3.5-content-safety、llama-guard-4-12b 等风控模型（Nvidia NIM 免费提供）。</p>
          <pre class="code">{{ codeApiMod }}</pre>
        </section>

        <!-- ===== 端点：POST /v1/images/generations ===== -->
        <section id="a-ep-img" class="card sec">
          <div class="row between wrap" style="gap: 8px;">
            <div class="row"><span class="tag acc method">POST</span><code class="ep">/v1/images/generations</code></div>
            <CopyBtn :text="codeApiImg" label="复制示例" />
          </div>
          <p class="desc">文生图。由官方自营专线提供（按张计费，需余额充足），可用模型 ID 见「模型中心」收费专区。</p>
          <pre class="code">{{ codeApiImg }}</pre>
        </section>

        <!-- ===== 端点：GET /assets/{key} ===== -->
        <section id="a-ep-asset" class="card sec">
          <div class="row between wrap" style="gap: 8px;">
            <div class="row"><span class="tag ok method">GET</span><code class="ep">/assets/{key}</code></div>
            <CopyBtn :text="codeApiAsset" label="复制示例" />
          </div>
          <p class="desc">R2 媒体缓存访问。生成类接口（图片/音频/视频）产出的文件会写入缓存，可通过该路径直接访问，路径形如 <code>/assets/&#123;key&#125;</code>。</p>
          <pre class="code">{{ codeApiAsset }}</pre>
        </section>

        <!-- ===== 特殊模型说明 ===== -->
        <section id="a-special" class="card sec">
          <h2>特殊模型说明</h2>
          <ul class="tips">
            <li><b>Auto 路由</b>：填入 <code>auto</code>（旧写法 <code>acu/auto-models</code> 仍兼容），网关自动从 Nvidia 候选池随机命中一个主流模型。</li>
            <li><b>官方自营（收费）</b>：模型 ID 带 <code>aqua/</code> 前缀（如 <code>aqua/deepseek-v4-flash</code>），由 AQUA 官方自营专线通道提供，按次计费（每次成功请求扣一次），长期服务、稳定可靠。</li>
            <li><b>专线排队</b>：专线通道有并发保护，高峰期会自动排队等待（先到先得）；排队超时会返回提示，请稍后重试，不要重复提交。</li>
            <li><b>非标准端点</b>：付费图像模型（<code>/v1/images/generations</code>）等特殊能力已适配为标准 OpenAI SDK 端点，可直接用官方 SDK 调用。</li>
            <li><b>账号制密钥</b>：注册登录后在个人控制台创建密钥（<code>sk-****</code>）即可调用——注册免费、创建自助、随时吊销重建。大版本更新与公告在 QQ 频道（频道号 pd57362562）通知。</li>
          </ul>
        </section>

        <!-- ===== 错误与状态码 ===== -->
        <section id="a-errors" class="card sec">
          <h2>错误与状态码</h2>
          <p class="desc">所有错误响应为结构化 JSON（<b>非传统纯 HTTP 状态码机制</b>）：程序识别主键是字符串业务错误码 <code>error.code</code>，同时保留数字 <code>error.status</code>（与 HTTP 状态码一致，兼容 OpenAI SDK）。中文消息 <code>error.message</code> 直接可读（<code>Content-Type: application/json; charset=utf-8</code>），并附带 QQ 频道 / 官网自助引导字段：</p>
          <div class="codewrap"><div class="row between codetop"><span class="tag bad">错误响应结构</span><CopyBtn :text="codeApiErr" /></div><pre class="code flat">{{ codeApiErr }}</pre></div>
          <h3>业务错误码（<code>error.code</code>）</h3>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>错误码</th><th>HTTP</th><th>含义与处理</th></tr></thead>
              <tbody>
                <tr><td><code>INVALID_JSON</code></td><td>400</td><td>请求体不是合法 JSON</td></tr>
                <tr><td><code>MISSING_PARAM</code></td><td>400</td><td>缺少必填字段（如 <code>model</code>）</td></tr>
                <tr><td><code>INVALID_PARAM</code></td><td>400</td><td>参数格式非法（如 IP 格式错误、内网保留 IP）</td></tr>
                <tr><td><code>INVALID_REQUEST</code></td><td>400</td><td>请求格式不正确</td></tr>
                <tr><td><code>UPSTREAM_REJECTED</code></td><td>400</td><td>上游拒绝请求参数，请按模型要求调整请求体</td></tr>
                <tr><td><code>UNAUTHORIZED</code></td><td>401</td><td>密钥无效/已吊销/未携带：请登录后在控制台创建密钥并完整复制</td></tr>
                <tr><td><code>MODEL_NOT_FOUND</code></td><td>404</td><td>模型 ID 不在目录中（GET /v1/models 可查）</td></tr>
                <tr><td><code>MODEL_RETIRED</code></td><td>410</td><td>模型已在上游永久下线，请更换模型</td></tr>
                <tr><td><code>QUOTA_EXHAUSTED</code></td><td>429</td><td>上游通道当日免费额度用尽，次日自动重置</td></tr>
                <tr><td><code>RATE_LIMITED</code></td><td>429</td><td>上游限流 / 模型被临时封锁，稍后重试或换模型</td></tr>
                <tr><td><code>MODEL_BLOCKED</code></td><td>429</td><td>该模型在上游被封锁（3+ 密钥无权限），约 10 分钟自动恢复</td></tr>
                <tr><td><code>MODEL_UNAVAILABLE</code></td><td>502</td><td>模型在上游已下线或不存在，请更换模型</td></tr>
                <tr><td><code>UPSTREAM_UNREACHABLE</code></td><td>502</td><td>上游连接失败 / 上游 5xx，稍后重试</td></tr>
                <tr><td><code>UPSTREAM_AUTH_FAILED</code></td><td>502</td><td>上游鉴权失败（网关密钥异常），可向社区反馈</td></tr>
                <tr><td><code>UPSTREAM_ERROR</code></td><td>502</td><td>上游服务内部错误，稍后重试</td></tr>
                <tr><td><code>UPSTREAM_TIMEOUT</code></td><td>504</td><td>上游响应超时，稍后重试</td></tr>
                <tr><td><code>ALL_KEYS_BUSY</code></td><td>503</td><td>密钥池全部繁忙（并发满），稍后重试</td></tr>
                <tr><td><code>CHANNEL_UNAVAILABLE</code></td><td>502</td><td>对应供应商通道未配置或暂不可用</td></tr>
                <tr><td><code>INTERNAL_ERROR</code></td><td>500</td><td>网关内部异常，持续出现请到 QQ 频道反馈</td></tr>
              </tbody>
            </table>
          </div>
          <p class="desc">每个错误响应均带 <code>error.hint</code>（加入 QQ 频道 pd57362562 与官网引导），程序可读取 <code>error.help</code> 结构化字段直接展示求助入口。</p>
          <h3>模型状态字段（<code>/v1/models</code> 返回）</h3>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>status</th><th>含义</th><th>处理建议</th></tr></thead>
              <tbody>
                <tr><td>（无）</td><td>正常可用</td><td>直接调用</td></tr>
                <tr><td><code>exhausted</code></td><td>该模型今日免费额度已用尽</td><td>等待额度自动重置，或切换其他模型</td></tr>
                <tr><td><code>unavailable</code></td><td>模型在上游被封锁（3+ 密钥无访问权限）</td><td>约 10 分钟后自动恢复，期间可先切换其他模型</td></tr>
              </tbody>
            </table>
          </div>
          <h3>健康评分（<code>health.score</code>）</h3>
          <p class="desc">每个模型附带近 100 次调用的健康评分（0-100，由成功率 / 延迟 / 稳定性加权计算）。<code>90+</code> 优秀、<code>70-89</code> 良好、<code>50-69</code> 一般、<code>&lt;50</code> 较差，可作为选择稳定模型的参考。</p>
        </section>

        <!-- ===== 工具 API ===== -->
        <section id="a-tools" class="card sec">
          <h2>工具 API（/v1/tools/*）</h2>
          <p class="desc">不只 AI——网关同时开放纯算法工具 API（不消耗上游额度），鉴权与主 API 相同。工具箱页面的全部能力都可编程调用：</p>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>端点</th><th>方法</th><th>请求体</th><th>说明</th></tr></thead>
              <tbody>
                <tr><td><code>/v1/tools/text-stats</code></td><td>POST</td><td><code>{"text":"..."}</code></td><td>字数/中文字数/词频 Top10/阅读时长</td></tr>
                <tr><td><code>/v1/tools/dice</code></td><td>POST</td><td><code>{"sides":6,"count":1}</code></td><td>随机骰子（sides 2-1000，count 1-20）</td></tr>
                <tr><td><code>/v1/tools/uuid</code></td><td>GET</td><td>—</td><td>生成 UUID v4</td></tr>
                <tr><td><code>/v1/tools/timestamp</code></td><td>GET/POST</td><td>POST: <code>{"timestamp":123}</code></td><td>当前时间戳 / 时间戳转日期（ISO/UTC）</td></tr>
                <tr><td><code>/v1/tools/base64</code></td><td>POST</td><td><code>{"action":"encode","text":"..."}</code></td><td>Base64 编解码（action: encode/decode）</td></tr>
                <tr><td><code>/v1/tools/subnet</code></td><td>POST</td><td><code>{"cidr":"192.168.1.0/24"}</code></td><td>子网计算器：网络/广播地址、掩码、可用主机范围</td></tr>
                <tr><td><code>/v1/tools/treehole</code></td><td>POST</td><td><code>{"mode":"gentle","messages":[...]}</code></td><td>树洞情感陪伴（mode: gentle=小溪 / anime=星璃），OpenAI chat 格式，支持流式</td></tr>
                <tr><td><code>/v1/tools/prompts</code></td><td>GET</td><td>—</td><td>提示词工坊：官方收录的智能体人设与 Skill 提示词（分类/建议模型）</td></tr>
                <tr><td><code>/v1/tools/translate</code></td><td>POST</td><td><code>{"text":"...","to":"en"}</code></td><td>AI 翻译：自动识别源语言，to 支持中/英/日/韩等（zh/en/ja/ko/fr/de/ru/es）</td></tr>
                <tr><td><code>/v1/tools/url-summary</code></td><td>POST</td><td><code>{"url":"https://..."}</code></td><td>网页摘要：抓取网页正文并由 AI 提炼要点</td></tr>
                <tr><td><code>/v1/tools/hash</code></td><td>POST</td><td><code>{"text":"...","algo":"sha256"}</code></td><td>哈希计算（algo: md5/sha1/sha256/sha512，默认 sha256）</td></tr>
                <tr><td><code>/v1/tools/password</code></td><td>POST</td><td><code>{"length":16,"count":5}</code></td><td>批量强密码生成（length 8-64，count 1-20，可用 upper/lower/digits/symbols 开关）</td></tr>
                <tr><td><code>/v1/tools/json</code></td><td>POST</td><td><code>{"text":"..."}</code></td><td>JSON 校验 + 美化/压缩（valid/pretty/minified）</td></tr>
                <tr><td><code>/v1/tools/regex</code></td><td>POST</td><td><code>{"pattern":"...","text":"...","flags":"g"}</code></td><td>正则匹配测试（flags 支持 i/m/s），返回全部匹配与捕获组</td></tr>
                <tr><td><code>/v1/tools/color</code></td><td>POST</td><td><code>{"input":"#3b82f6"}</code></td><td>颜色互转：HEX / RGB / HSL 三格式同时返回</td></tr>
                <tr><td><code>/v1/tools/url-code</code></td><td>POST</td><td><code>{"text":"...","mode":"encode"}</code></td><td>URL 百分号编解码（mode: encode/decode）</td></tr>
                <tr><td><code>/v1/tools/token-count</code></td><td>POST</td><td><code>{"text":"..."}</code></td><td>Token 近似估算（中文≈0.6字/token，英文≈4字符/token）</td></tr>
                <tr><td><code>/v1/tools/uuid-bulk</code></td><td>POST</td><td><code>{"count":10}</code></td><td>批量 UUID v4 生成（count 1-100）</td></tr>
                <tr><td><code>/v1/tools/shorten</code></td><td>POST</td><td><code>{"url":"https://..."}</code></td><td>短链生成：返回 <code>acu.ltzy.top/s/码</code>，90 天无访问自动清理</td></tr>
                <tr><td><code>/v1/tools/webhook</code></td><td>POST</td><td><code>{"action":"create|list|clear","id":"..."}</code></td><td>Webhook 收集器：create 得到 /hook/{id} 地址，list 查看收到的请求，clear 清空</td></tr>
              </tbody>
            </table>
          </div>
          <p class="desc">示例：<code>curl https://你的网关/v1/tools/uuid -H "Authorization: Bearer sk-****"</code></p>
        </section>

        <!-- ===== 开放数据与社区玩法 ===== -->
        <section id="a-open" class="card sec">
          <h2>开放数据与社区玩法 API</h2>
          <p class="desc">全站数据透明化 + 社区玩法——这部分端点大多<b>无需密钥</b>，欢迎拿去做监控面板、二次开发或装进你自己的项目。</p>
          <h3>数据大屏与用量</h3>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>端点</th><th>方法</th><th>鉴权</th><th>说明</th></tr></thead>
              <tbody>
                <tr><td><code>/v1/stats</code></td><td>GET</td><td>无需密钥</td><td>全站今日数据大屏：总调用 / 成功率 / 平均延迟 / 活跃密钥数 / 热门模型 TOP10</td></tr>
                <tr><td><code>/status</code></td><td>GET</td><td>无需密钥</td><td>网关版本、连续运行时长、全部模型近 1 小时成功率与延迟（健康透明页）</td></tr>
                <tr><td><code>/v1/usage</code></td><td>GET</td><td>Bearer 你的密钥</td><td>查这个密钥自己的用量：今日/近 7 天调用与成功率、模型分布、最近 10 条调用。<b>填哪个密钥查哪个</b>，同一密钥统计永久连续（只存指纹不存明文）；登录用户在官网控制台看全量</td></tr>
              </tbody>
            </table>
          </div>
          <h3>模型竞技场（盲测 + 投票 + 排行）</h3>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>端点</th><th>方法</th><th>鉴权</th><th>说明</th></tr></thead>
              <tbody>
                <tr><td><code>/v1/arena</code></td><td>POST</td><td>Bearer 任意密钥</td><td>发起盲测对决：<code>{"prompt":"...","max_tokens":512}</code> → 两位匿名选手 A/B 的回答（含 battle_id 与耗时），身份隐藏防"看名投票"</td></tr>
                <tr><td><code>/v1/arena/vote</code></td><td>POST</td><td>Bearer 任意密钥</td><td>投票：<code>{"battle_id":"...","winner":"a|b|tie"}</code> → 返回计数结果并<b>揭晓双模型真实身份</b>，一票一场不可改</td></tr>
                <tr><td><code>/v1/arena/leaderboard</code></td><td>GET</td><td>无需密钥</td><td>全站胜率排行榜：胜 1 分 / 平 0.5 分 / 负 0 分，按积分排序</td></tr>
              </tbody>
            </table>
          </div>
          <h3>开发者小件</h3>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>端点</th><th>说明</th></tr></thead>
              <tbody>
                <tr><td><code>GET /s/{code}</code></td><td>短链 302 跳转（挂在站点域名下）</td></tr>
                <tr><td><code>ANY /hook/{id}</code></td><td>Webhook 收集地址：任意方法任意请求都会被完整记录（方法/路径/头/体），24 小时保留，配合 <code>/v1/tools/webhook</code> 的 list 动作查看</td></tr>
              </tbody>
            </table>
          </div>
          <p class="desc">试一把：<code>curl https://你的网关/v1/stats</code>（不带密钥也能访问）。</p>
        </section>

        <!-- ===== 对接指南 ===== -->
        <section id="a-integration" class="card sec">
          <h2>对接指南：把 AQUA 接进你的软件</h2>
          <p class="desc">AQUA 说的是标准「OpenAI 方言」——凡是支持自定义 OpenAI 接口的软件（聊天客户端、翻译插件、IDE 编程助手、自动化工作流…）都能直接接。</p>
          <h3>通用三要素（所有软件都问这三样）</h3>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>要素</th><th>填什么</th><th>备注</th></tr></thead>
              <tbody>
                <tr><td>接口地址<br>（API Host / Base URL）</td><td><code>https://api.ltzy.top</code></td><td><b>注意：有的软件要带 <code>/v1</code>，有的不带</b>——规则见下方「翻车对照表」第 1 条</td></tr>
                <tr><td>API Key</td><td>控制台创建的 <code>sk-****</code></td><td>注册登录 → 个人控制台创建，完整密钥只展示一次</td></tr>
                <tr><td>模型（Model）</td><td>去「模型中心」抄，如 <code>deepseek-v4-flash</code></td><td>一字不差，区分大小写</td></tr>
              </tbody>
            </table>
          </div>
          <h3>常见客户端配置示例</h3>
          <ul class="tips">
            <li><b>ChatGPT Next Web（NextChat）</b>：设置 → 自定义接口：API 地址填 <code>https://api.ltzy.top</code>（不带 /v1），API Key 填控制台创建的密钥，模型手动输入模型 ID。</li>
            <li><b>LobeChat / Open WebUI</b>：语言模型 → OpenAI 兼容：API 代理地址填 <code>https://api.ltzy.top/v1</code>（带 /v1），Key 填你的密钥，添加模型 ID。</li>
            <li><b>沉浸式翻译</b>：翻译服务 → OpenAI：APIKEY 填你的密钥，自定义模型接口地址 <code>https://api.ltzy.top/v1/chat/completions</code>（填到端点），模型填 ID。</li>
            <li><b>IDE 编程助手（Continue / Cline 等）</b>：provider 选 openai，apiBase 填 <code>https://api.ltzy.top/v1</code>，apiKey 填你的密钥，model 填 ID。</li>
            <li><b>通用口诀</b>：不确定带不带 <code>/v1</code> 时，两种都试一遍——密钥自助创建、随时重建，试错零成本。</li>
          </ul>
          <h3>对接翻车对照表（症状 → 病因 → 处方）</h3>
          <div class="tbl-wrap">
            <table class="table">
              <thead><tr><th>症状</th><th>病因</th><th>处方</th></tr></thead>
              <tbody>
                <tr><td>404 Not Found / 401</td><td>base_url 的 <code>/v1</code> 多了或少了</td><td>OpenAI SDK 类填<b>带 <code>/v1</code></b>（它自己再拼 /chat/completions）；NextChat 等「填域名」类填<b>不带</b>。两种各试一次</td></tr>
                <tr><td><code>MODEL_NOT_FOUND</code></td><td>模型 ID 抄错（大小写、多空格、前缀漏了）</td><td>去模型中心复制完整 ID，如 <code>aqua/deepseek-v4-flash</code> 不能只写 <code>deepseek-v4-flash</code></td></tr>
                <tr><td>连不上 / 超时</td><td>域名写错、本地网络拦截</td><td>核对 <code>api.ltzy.top</code>；浏览器先开 <code>https://api.ltzy.top/v1/models</code> 能看到 JSON 说明网络通</td></tr>
                <tr><td><code>INVALID_JSON</code></td><td>请求体不是合法 JSON（多了逗号、用了单引号）</td><td>用 JSON 校验器检查；字符串一律双引号</td></tr>
                <tr><td>中文显示乱码</td><td>没按 UTF-8 解码</td><td>响应头是 <code>charset=utf-8</code>；自写代码请用 UTF-8 读流</td></tr>
                <tr><td><code>RATE_LIMITED</code> / 429</td><td>上游限流或触发站点速率保护</td><td>等 10 秒再试；程序里加退避重试；换一个模型</td></tr>
                <tr><td><code>CHANNEL_BUSY</code> / 503</td><td>自营专线通道并发满（高峰期限流保护）</td><td>稍后重试；高峰期可换其他平台模型</td></tr>
                <tr><td>回复内容像「另一个人」</td><td>多轮对话没带历史 messages</td><td>把完整聊天记录按顺序传回（见「对话接口进阶」）</td></tr>
              </tbody>
            </table>
          </div>
        </section>

        <!-- ===== FAQ ===== -->
        <section id="a-faq" class="card sec">
          <h2>FAQ：新手高频提问</h2>
          <p class="desc">点开看答案。没找到你的问题？QQ 一群 1103667832 / 二群 1006740220 或频道 pd57362562 随时提问。</p>
          <details><summary><b>密钥到底怎么获取？要申请吗？</b></summary><p class="desc">自助领取，不用找任何人申请。<b>注册登录后进「个人控制台」一键创建</b>——形如 <code>sk-****</code>，注册免费、创建即用、随时吊销重建。每个密钥的完整明文只在创建时展示一次，请保存好；丢了就吊销重新创建。</p></details>
          <details><summary><b>真的免费吗？有没有隐藏收费？</b></summary><p class="desc">除官方自营收费系列（<code>aqua/</code> 前缀，预充值按次计费，见上方「计费说明」章节）外，本站全部模型与工具均免费使用，无隐藏收费。acu/ 众筹公共模型也免费调用（从站点公共额度按次扣费，不动个人余额）。个别通道有并发/额度保护（如部分通道日额度、收费通道并发限流），是为了让所有人都能公平使用，不是收费墙。免费模型不涉及任何余额与扣费，今后也不会收费。</p></details>
          <details><summary><b>有官方 SDK 吗？</b></summary><p class="desc">不需要专门的 SDK——AQUA 完整兼容 OpenAI 协议，<b>直接用 OpenAI 官方 SDK</b>（Python / JS / Go / Java 全平台都有），只改 <code>base_url</code> 和 <code>api_key</code> 两个参数即可（见「三分钟跑通第一条请求」）。</p></details>
          <details><summary><b>模型这么多，我该用哪个？</b></summary><p class="desc">按场景选：<b>日常聊天</b>→<code>gpt-oss-20b</code>（免费）或收费专区性价比款 <code>aqua/glm-5.3-flash</code>；<b>写代码 / 长文写作</b>→收费专区 <code>aqua/deepseek-v4-pro</code> / <code>aqua/kimi-k3</code> 旗舰款。每个卡片都有实时时延与价格标注，选哪个都不亏，自由试。</p></details>
          <details><summary><b>为什么 AI 不记得上一句说了什么？</b></summary><p class="desc">AI 没有记忆，每次调用都是全新开始。要多轮对话，请把历史聊天记录放进 <code>messages</code> 数组一起传回（详见「对话接口进阶 · 多轮对话」）。</p></details>
          <details><summary><b>报 429 / 排队怎么办？</b></summary><p class="desc">429 = 限流或额度保护，不是封禁。等 10 秒~1 分钟重试；高峰期换其他平台模型；程序里建议加「指数退避」重试（第一次等 1 秒、第二次 2 秒、第三次 4 秒…）。不要连续狂点，那样只会更堵。</p></details>
          <details><summary><b>支持联网搜索 / 文件上传吗？</b></summary><p class="desc">模型本身的能力决定：通用 chat 模型不做实时联网。需要最新信息时，把内容直接粘贴进问题里（RAG 思路：先给资料再提问）。</p></details>
          <details><summary><b>我想自己部署一套 AQUA 可以吗？</b></summary><p class="desc">可以！AQUA 是 ACU 工程系列开源项目（AGPL-3.0），Go 语言实现的 OpenAI 兼容网关，编译为单二进制，部署到任意 VPS 即可运行（Nginx/Caddy 反代 + HTTPS 即完成上线）。仓库：<a href="https://gitee.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener">Gitee</a> / <a href="https://github.com/xiaosu4610/aqua-rust-workers" target="_blank" rel="noopener">GitHub</a>，README 有完整自部署教程。</p></details>
          <details><summary><b>调用出错时，怎么向社区求助最有效？</b></summary><p class="desc">带上三样东西：① 完整的错误 JSON（里面有 <code>error.code</code> 和 <code>hint</code>）；② 你请求的模型 ID 和端点；③ 时间点。发到 QQ 一群 1103667832 / 二群 1006740220 或频道 pd57362562，一般很快有人响应。</p></details>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* ---- 左导航 + 右正文 双栏；窄屏导航隐藏 ---- */
.api-layout {
  display: grid;
  grid-template-columns: 232px minmax(0, 1fr);
  gap: 18px;
  align-items: start;
}
.toc {
  position: sticky;
  top: 68px;
  max-height: calc(100dvh - 90px);
  overflow-y: auto;
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.toc-title { display: flex; align-items: center; gap: 7px; font-size: 12px; font-weight: 700; color: var(--txt3); letter-spacing: .08em; text-transform: uppercase; padding: 0 8px 8px; }
.toc-item {
  text-align: left;
  border: 0; background: transparent;
  color: var(--txt2); font-size: 12.5px;
  padding: 6px 9px; border-radius: 7px;
  cursor: pointer; transition: all var(--t-fast);
  border-left: 2px solid transparent;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.toc-item:hover { color: var(--acc); background: var(--bg3); }
.toc-item.on { color: var(--acc); background: var(--acc-soft); border-left-color: var(--acc); font-weight: 600; }
@media (max-width: 980px) {
  .api-layout { grid-template-columns: 1fr; }
  .toc { display: none; }
}

/* ---- 文档正文 ---- */
.doc { min-width: 0; display: flex; flex-direction: column; gap: 16px; }
.sec { scroll-margin-top: 78px; }
.sec h2 { font-size: 19px; margin-bottom: 6px; }
.sec h3 { font-size: 14.5px; margin: 18px 0 8px; color: var(--txt0); }
.desc { color: var(--txt2); font-size: 13.5px; margin: 8px 0; max-width: 84ch; }
.tips { padding-left: 20px; display: grid; gap: 6px; color: var(--txt2); font-size: 13.5px; margin: 8px 0; }
details { border: 1px solid var(--line); border-radius: var(--r-sm); padding: 10px 14px; margin-top: 8px; }
details summary { cursor: pointer; color: var(--txt0); font-size: 13.5px; }
details[open] summary { margin-bottom: 6px; }
.method { font-family: var(--mono); font-weight: 700; padding: 4px 11px; }
.ep { font-size: 15px; font-weight: 700; color: var(--txt0); word-break: break-all; }

/* ---- 代码块 + 顶部标签行 ---- */
.codewrap { margin: 10px 0; }
.codetop {
  border: 1px solid var(--line); border-bottom: 0;
  border-radius: var(--r-md) var(--r-md) 0 0;
  background: var(--bg3);
  padding: 6px 10px 6px 12px;
}
.code.flat { border-radius: 0 0 var(--r-md) var(--r-md); border-top: 0; margin: 0; }
</style>
