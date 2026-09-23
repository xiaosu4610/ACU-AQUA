/* ============================================================================
 * 静态落地页生成（SEO/GEO 核心）
 *
 * 背景：本站是纯客户端渲染 SPA，百度/360/Sogou 与生成式引擎（GPTBot、ClaudeBot、
 * PerplexityBot 等）不执行 JS，只能抓到加载骨架。本脚本在 vite build 之后，
 * 以 dist/index.html 为模板，为每个公开路由写出「带真实正文 + 结构化数据」的静态 HTML：
 *
 *   dist/index.html              ← 首页正文（根路径）
 *   dist/home.html               ← 同首页（/home 的 canonical 归一到 /）
 *   dist/models.html             ← 各公开路由
 *   dist/tools/<id>.html         ← 19 个工具页
 *   dist/sitemap.xml             ← 由内容清单生成（单一事实源，不再手维护）
 *
 * 为什么是 <path>.html 而不是 <path>/index.html：目录写法会让 nginx 对 `/models`
 * 返回 301 补斜杠（→ /models/），而 canonical / sitemap 用的是不带斜杠的 URL，
 * 三者不一致会浪费收录预算。配 `try_files $uri $uri.html $uri/ /index.html` 后
 * `/models` 直接命中 models.html，URL 保持原样。
 *
 * 用户视角无感：正文放在 <div id="app"> 内，Vue 挂载时整体替换为完整应用
 * （同一份 HTML 服务所有访客，不做 UA 分流，避免 cloaking 风险）。
 *
 * 同时为每个生成的文件产出 .gz / .br（nginx gzip_static 可直接命中，省 CPU）。
 * ==========================================================================*/
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { gzipSync, brotliCompressSync } from 'node:zlib'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { SITE, SITE_NAME, API_BASE, CORE_PAGES, TOOL_CONTENT, TOOL_CATEGORY_LABEL,
  TOOL_TITLE_SUFFIX, TOOL_DESC_SUFFIX, TOOL_DESC_SUFFIX_LOCAL,
  MODEL_NAMES, LINE_LABELS } from './seo-content.mjs'

const DIST = fileURLToPath(new URL('../dist', import.meta.url))
const SRC = fileURLToPath(new URL('../src', import.meta.url))

const esc = s => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
const canonicalOf = path => (path === '/' || path === '/home') ? SITE + '/' : SITE + path
// 页面对外 URL：模型页必须用 encodeURIComponent(id) 形式（与 SPA 链接一致），故允许页对象自带 canonical
const pageURL = p => p.canonical || canonicalOf(p.path)

/* ---- 1. 从 src/tools/registry.ts 解析工具清单（新增工具自动纳入，无需改本脚本） ---- */
function loadTools() {
  const src = readFileSync(join(SRC, 'tools', 'registry.ts'), 'utf8')
  const re = /id:\s*'([^']+)',\s*title:\s*'([^']+)',\s*desc:\s*'([^']+)',\s*category:\s*'(\w+)',\s*(local:\s*true,)?/g
  const out = []
  let m
  while ((m = re.exec(src))) out.push({ id: m[1], title: m[2], desc: m[3], category: m[4], local: !!m[5] })
  if (out.length < 5) throw new Error(`registry.ts 解析异常：只解析到 ${out.length} 个工具，请检查文件结构`)
  return out
}

/* ---- 2. 组装页面清单 ---- */
const pages = CORE_PAGES.map(p => ({ ...p }))
for (const t of loadTools()) {
  const c = TOOL_CONTENT[t.id] || {}
  const cat = TOOL_CATEGORY_LABEL[t.category] || t.category
  const sections = []
  if (c.bullets?.length) sections.push({ h2: '它能做什么', body: `<ul>${c.bullets.map(b => `<li>${b}</li>`).join('')}</ul>` })
  if (c.steps?.length) sections.push({ h2: '怎么用', body: `<ol>${c.steps.map(s => `<li>${s}</li>`).join('')}</ol>` })
  sections.push({
    h2: '关于本站工具箱',
    body: `<p>本工具属于 AQUA api 免费工具箱的「${cat}」分类：无需安装、打开即用。同类工具见 <a href="/tools">工具箱首页</a>；纯算法类工具在浏览器本地完成计算，输入不上传；涉及大模型调用的工具优先使用免费线模型。</p>`,
  })
  pages.push({
    path: `/tools/${t.id}`,
    title: t.title + (TOOL_TITLE_SUFFIX[t.category] || ' — 免费在线使用｜AQUA api'),
    desc: (c.lead || t.desc) + (t.local ? TOOL_DESC_SUFFIX_LOCAL : (TOOL_DESC_SUFFIX[t.category] || ' 免费在线使用。')),
    h1: t.title,
    lead: c.lead || t.desc,
    crumb: t.title,
    parent: { name: '工具箱', href: '/tools' },
    changefreq: 'monthly',
    priority: '0.8',
    sections,
    faq: (c.faq || []).map(([q, a]) => ({ q, a })),
  })
}

/* ---- 2b. 模型详情页（构建期拉取在架模型 → 唯一标题/描述） ----
 * 此前 /model/<id> 走 SPA 兜底 → 42 个模型页全部返回同一份首页 title/description，
 * 这正是 Bing 报「大量页面标题相同 / 描述相同」的根因。这里为每个在架模型生成独立页面。 */
const MODELS_API = process.env.SEO_MODELS_API || 'https://acu.ltzy.top/v1/models'

async function loadModels() {
  try {
    const res = await fetch(MODELS_API, { headers: { accept: 'application/json' }, signal: AbortSignal.timeout(15000) })
    if (!res.ok) throw new Error('HTTP ' + res.status)
    const j = await res.json()
    const list = (j.data || []).filter(m => m && m.id && m.id !== 'auto')
    if (!list.length) throw new Error('返回空清单')
    return list
  } catch (e) {
    // 拉不到不阻断构建：本次跳过模型页（其余页面照常），避免"生产不可达导致前端发不出去"
    console.warn(`⚠️  模型清单拉取失败（${MODELS_API}）：${e.message} —— 本次跳过模型详情页`)
    return []
  }
}

const yuan = micro => (Number(micro) / 1e6).toFixed(6).replace(/0+$/, '').replace(/\.$/, '')
const trimNum = v => (typeof v === 'number' ? v.toFixed(4).replace(/0+$/, '').replace(/\.$/, '') : String(v ?? ''))

/** 从模型 ID 推断能力标签：只在特征明确时给出，拿不准返回空（宁可不说，也不写错能力） */
function kindHint(id) {
  const l = id.toLowerCase()
  if (/rerank/.test(l)) return '检索重排'
  if (/embed|bge-|arctic-embed/.test(l)) return '文本向量化'
  if (/asr|whisper|sensevoice|voice/.test(l)) return '语音识别'
  if (/tts|melotts/.test(l)) return '语音合成'
  if (/guard|moderation|safety|nonescape|nsfw/.test(l)) return '内容安全'
  if (/vision|-vl-|paligemma|neva|vila|llama-3\.2-(11b|90b)/.test(l)) return '多模态视觉'
  if (/diffusion|stable-diffusion|flux/.test(l)) return '图像生成'
  return ''
}

async function buildModelPages() {
  const out = []
  for (const m of await loadModels()) {
    const id = m.id
    const name = MODEL_NAMES[id] || id
    const line = m.owned_by || ''
    const isFree = !m.paid
    const mode = isFree ? 'free' : (m.mode || 'per_token')
    const lineLabel = LINE_LABELS[`${line}:${mode}`] || (isFree ? 'AQUA 公益免费线' : 'AQUA 计费专线')
    const kind = kindHint(id)
    const kindTxt = kind ? `能力类型：${kind}。` : ''
    const note = esc((m.description || '').trim())

    let title, desc, billing
    if (isFree) {
      const selfRun = line === 'acu'
      title = selfRun ? `${name} — 公益免费调用，注册即用｜AQUA api`
        : `${name} — 免费调用｜AQUA api 公益免费通道`
      desc = selfRun
        ? `${name} 在 AQUA 公益免费线（官方自营）提供：注册即用、不扣个人余额，OpenAI 兼容，任何 SDK 改 base_url 即可接入。${kindTxt}附能力说明与可直接复制的调用示例，适合先验证效果，再决定是否使用低价高速专线。`
        : `${name} 由 AQUA 公益免费通道提供，注册即用、不扣个人余额；OpenAI 兼容，改 base_url 即可接入。${kindTxt}免费额度适合体验与轻量调用，另有低价高速专线支撑生产负载，模型广场可实时比价。`
      billing = '免费（公益线路，不扣个人余额）'
    } else if (mode === 'per_call') {
      title = `${name} — 按次计费 ${yuan(m.price_micro)} 元/次｜AQUA api`
      desc = `${name} 在${lineLabel}提供，按次计费 ${yuan(m.price_micro)} 元/次，与输入输出长度无关。OpenAI 兼容，任何 SDK 改 base_url 即可接入；${kindTxt}附实时首字延迟与可复制的 curl / Python 示例，可先看免费线再决定是否升级。`
      billing = `按次计费 ${yuan(m.price_micro)} 元/次（与输入输出长度无关）`
    } else {
      const cin = trimNum(m.in_price), cout = trimNum(m.out_price), ccache = trimNum(m.cache_price ?? 0)
      title = `${name} API — 按量计费 ${cin}/${cout} 元每百万 tokens｜AQUA api`
      desc = `${name} 在${lineLabel}提供，按量计费：输入 ${cin} / 缓存命中 ${ccache} / 输出 ${cout} 元每百万 tokens，用多少付多少，账单逐笔可核对。${kindTxt}OpenAI 兼容，改 base_url 即可接入，适合长上下文与生产负载。`
      billing = `按量计费：输入 ${cin} / 缓存 ${ccache} / 输出 ${cout} 元每百万 tokens`
    }

    const curl = `curl ${API_BASE}/chat/completions \\\n  -H "Authorization: Bearer sk-你的密钥" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"${id}","messages":[{"role":"user","content":"你好"}]}'`
    const sections = [
      {
        h2: '线路与计费',
        body: `<ul><li><b>线路</b>：${lineLabel}</li><li><b>计费</b>：${billing}</li><li><b>模型 ID</b>：<code>${id}</code>（请求时填这个完整 ID）</li></ul>
<p>收费模型先付后用：请求前按上限预扣，完成后按实际用量多退少补，失败或已退款的请求不计费。全部在架模型与实时价格见 <a href="/models">模型广场</a>。</p>`,
      },
      {
        h2: '如何调用',
        body: `<p>Base URL：<code>${API_BASE}</code>，密钥在站点控制台创建。</p>
<pre><code>${curl}</code></pre>
<p>Python / Node 只需把 OpenAI 官方 SDK 的 base_url 指向本站，无需改其他代码；完整参数与错误码见 <a href="/api">API 文档</a>。</p>`,
      },
    ]
    if (note) sections.splice(1, 0, { h2: '模型说明', body: `<p>${note}</p>` })

    out.push({
      path: `/model/${id}`,
      canonical: `${SITE}/model/${encodeURIComponent(id)}`,
      title, desc, h1: `${name} API`,
      lead: `${name} 是 AQUA api 的在架模型之一，属于${lineLabel}；${billing}。${kind ? '能力类型：' + kind + '。' : ''}`,
      crumb: name,
      parent: { name: '模型广场', href: '/models' },
      changefreq: 'weekly',
      priority: isFree ? '0.7' : '0.8',
      sections,
      faq: [
        { q: `${name} 调用要收费吗？`, a: isFree ? '不要。该模型在公益免费线路 / 通道提供，注册即用、不扣个人余额。' : `${billing}。收费模型先付后用，请求前按上限预扣、完成后按实际用量多退少补，失败或已退款不计费。` },
        { q: `怎么用 ${name}？`, a: `把 base_url 指向 ${API_BASE}，请求体 model 字段填完整 ID ${id}，带上控制台创建的密钥即可；任何 OpenAI SDK 都无需改其他代码。` },
      ],
    })
  }
  return out
}

pages.push(...await buildModelPages())

/* ---- 3. 结构化数据（JSON-LD） ---- */
const SITE_GRAPH = [
  {
    '@type': 'WebSite', '@id': SITE + '/#website', url: SITE + '/', name: SITE_NAME,
    alternateName: [SITE_NAME + ' 免费 AI API 网关', 'ACU 工程系列'],
    description: '免费 AI API 网关，OpenAI 兼容，多模型聚合，注册即用；需要稳定接口时专线按次/按量补位。',
    inLanguage: 'zh-CN', publisher: { '@id': SITE + '/#org' },
  },
  {
    '@type': 'Organization', '@id': SITE + '/#org', name: SITE_NAME, url: SITE + '/',
    logo: { '@type': 'ImageObject', url: SITE + '/logo.png', width: 256, height: 256 },
    description: 'ACU 工程系列开源旗舰项目，提供 OpenAI 兼容的多模型聚合 API 网关。',
  },
]

function jsonld(p) {
  const url = pageURL(p)
  const items = [{ name: '首页', item: SITE + '/' }]
  if (p.parent) items.push({ name: p.parent.name, item: SITE + p.parent.href })
  items.push({ name: p.crumb || p.h1, item: url })
  const graph = [
    ...SITE_GRAPH,
    {
      '@type': 'WebPage', '@id': url + '#webpage', url, name: p.title, description: p.desc,
      isPartOf: { '@id': SITE + '/#website' }, inLanguage: 'zh-CN',
      breadcrumb: { '@id': url + '#breadcrumb' },
    },
    {
      '@type': 'BreadcrumbList', '@id': url + '#breadcrumb',
      itemListElement: items.map((it, i) => ({ '@type': 'ListItem', position: i + 1, name: it.name, item: it.item })),
    },
  ]
  if (p.path.startsWith('/tools/')) {
    graph.push({
      '@type': 'WebApplication', '@id': url + '#app', name: p.h1, url,
      applicationCategory: 'UtilitiesApplication', operatingSystem: 'Web',
      description: p.desc, inLanguage: 'zh-CN',
      offers: { '@type': 'Offer', price: '0', priceCurrency: 'CNY' },
    })
  }
  if (p.faq?.length) {
    graph.push({
      '@type': 'FAQPage', '@id': url + '#faq',
      mainEntity: p.faq.map(f => ({ '@type': 'Question', name: f.q, acceptedAnswer: { '@type': 'Answer', text: f.a } })),
    })
  }
  return JSON.stringify({ '@context': 'https://schema.org', '@graph': graph })
}

/* ---- 4. 正文 HTML（放在 #app 内，Vue 挂载时整体替换） ---- */
const STATIC_CSS = `<style id="seo-static">
.seo-page{max-width:820px;margin:0 auto;padding:88px 24px 64px;font:16px/1.85 system-ui,-apple-system,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;color:#1b2430}
html[data-theme="dark"] .seo-page{color:#c9d4e2}
.seo-load{position:fixed;top:0;left:0;right:0;height:3px;background:rgba(11,99,197,.16);overflow:hidden;z-index:99}
.seo-load i{display:block;width:40%;height:100%;background:#0b63c5;animation:seo-slide 1.1s ease-in-out infinite}
@keyframes seo-slide{0%{transform:translateX(-110%)}100%{transform:translateX(320%)}}
@media (prefers-reduced-motion:reduce){.seo-load i{animation:none;width:100%}}
.seo-crumb{font-size:13px;color:#68758a;margin-bottom:20px}
.seo-crumb a{color:#0b63c5;text-decoration:none}
.seo-page h1{font-size:clamp(28px,3.6vw,42px);line-height:1.25;letter-spacing:-.03em;font-weight:750;margin-bottom:18px}
.seo-page h2{font-size:20px;margin:38px 0 12px;letter-spacing:-.02em}
.seo-lead{font-size:17px;color:#4a5768;margin-bottom:8px}
html[data-theme="dark"] .seo-lead{color:#93a2b6}
.seo-page ul,.seo-page ol{padding-left:22px}
.seo-page li{margin:6px 0}
.seo-page a{color:#0b63c5}
.seo-page code{font:13px/1.7 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;background:rgba(11,99,197,.08);padding:1px 6px;border-radius:5px}
.seo-page pre{margin:14px 0;padding:16px 18px;background:rgba(11,99,197,.06);border-radius:10px;overflow:auto}
.seo-page pre code{background:none;padding:0;font-size:12.5px}
.seo-faq{margin:0}
.seo-faq dt{font-weight:650;margin-top:18px}
.seo-faq dd{margin:6px 0 0;color:#4a5768}
html[data-theme="dark"] .seo-faq dd{color:#93a2b6}
.seo-foot{margin-top:52px;padding-top:22px;border-top:1px solid rgba(120,140,170,.25);font-size:13.5px;color:#68758a}
.seo-foot a{color:#0b63c5}
</style>`

function article(p) {
  const crumbs = `<nav class="seo-crumb" aria-label="面包屑"><a href="/">首页</a>${p.parent ? ` / <a href="${p.parent.href}">${esc(p.parent.name)}</a>` : ''} / <span>${esc(p.crumb || p.h1)}</span></nav>`
  const secs = p.sections.map(s => `<section><h2>${esc(s.h2)}</h2>${s.body}</section>`).join('\n')
  const faq = p.faq?.length
    ? `<section><h2>常见问题</h2><dl class="seo-faq">${p.faq.map(f => `<dt>${esc(f.q)}</dt><dd>${esc(f.a)}</dd>`).join('')}</dl></section>`
    : ''
  const foot = `<div class="seo-foot">${SITE_NAME} · OpenAI 兼容的多模型聚合 API 网关（Base URL：<code>${API_BASE}</code>）·
相关：<a href="/models">模型广场</a> · <a href="/api">API 文档</a> · <a href="/tools">工具箱</a> · <a href="/status">服务状态</a> · <a href="/community">社区</a>
</div>`
  return `<div class="seo-load" aria-hidden="true"><i></i></div>
<article class="seo-page">${crumbs}<h1>${esc(p.h1)}</h1><p class="seo-lead">${esc(p.lead)}</p>
${secs}
${faq}
${foot}</article>`
}

/* ---- 5. 注入模板 ---- */
// 注意：vite build 会把入口 module 脚本与 css 提到 <head>，因此不能按脚本标签切分；
// 以 <body>…</body> 为边界替换整段 body（构建产物 body 内只有 #app 一个节点）。
const base = readFileSync(join(DIST, 'index.html'), 'utf8')
const BODY_OPEN = '<body>'
const BODY_CLOSE = '</body>'
const bodyOpen = base.indexOf(BODY_OPEN)
const bodyClose = base.lastIndexOf(BODY_CLOSE)
if (bodyOpen < 0 || bodyClose < bodyOpen) throw new Error('dist/index.html 结构异常：未找到 <body> 边界')

const head = base.slice(0, bodyOpen + BODY_OPEN.length)
const tail = base.slice(bodyClose)

function render(p) {
  const url = pageURL(p)
  let h = head
  h = h.replace(/<title>[\s\S]*?<\/title>/, `<title>${esc(p.title)}</title>`)
  h = h.replace(/<meta name="description" content="[^"]*">/, `<meta name="description" content="${esc(p.desc)}">`)
  h = h.replace(/<link rel="canonical" href="[^"]*">/, `<link rel="canonical" href="${url}">`)
  h = h.replace(/<meta property="og:title" content="[^"]*">/, `<meta property="og:title" content="${esc(p.title)}">`)
  h = h.replace(/<meta property="og:description" content="[^"]*">/, `<meta property="og:description" content="${esc(p.desc)}">`)
  h = h.replace(/<meta property="og:url" content="[^"]*">/, `<meta property="og:url" content="${url}">`)
  h = h.replace(/<meta name="twitter:title" content="[^"]*">/, `<meta name="twitter:title" content="${esc(p.title)}">`)
  h = h.replace(/<meta name="twitter:description" content="[^"]*">/, `<meta name="twitter:description" content="${esc(p.desc)}">`)
  h = h.replace(/<script type="application\/ld\+json">[\s\S]*?<\/script>/, `<script type="application/ld+json">${jsonld(p)}</script>`)
  h = h.replace('</head>', STATIC_CSS + '\n</head>')
  return h + '\n  <div id="app">' + article(p) + '</div>\n' + tail
}

function writeWithCompression(file, content) {
  const buf = Buffer.from(content, 'utf8')
  writeFileSync(file, buf)
  writeFileSync(file + '.gz', gzipSync(buf, { level: 9 }))
  writeFileSync(file + '.br', brotliCompressSync(buf))
}

let n = 0
for (const p of pages) {
  const html = render(p)
  const file = p.path === '/' ? join(DIST, 'index.html') : join(DIST, p.path.slice(1) + '.html')
  mkdirSync(dirname(file), { recursive: true })
  writeWithCompression(file, html)
  if (p.path === '/home') writeWithCompression(join(DIST, 'index.html'), html) // 根路径同首页
  n++
}

/* ---- 6. 站点地图（由内容清单生成，单一事实源） ---- */
const today = new Date().toISOString().slice(0, 10)
// 按 canonical URL 去重：/home 的 canonical 归一到 /，两者合并为一条根条目（否则首页会漏收录）
const seenLoc = new Set()
const urls = pages
  .map(p => ({ loc: pageURL(p), changefreq: p.changefreq || 'monthly', priority: p.priority || '0.7' }))
  .filter(u => (seenLoc.has(u.loc) ? false : (seenLoc.add(u.loc), true)))
  .map(u => `  <url><loc>${u.loc}</loc><lastmod>${today}</lastmod><changefreq>${u.changefreq}</changefreq><priority>${u.priority}</priority></url>`)
  .join('\n')
const sitemap = `<?xml version="1.0" encoding="UTF-8"?>
<!-- 由 scripts/prerender.mjs 依据 scripts/seo-content.mjs 生成，请勿手改 -->
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
${urls}
</urlset>
`
writeWithCompression(join(DIST, 'sitemap.xml'), sitemap)

/* ---- 7. 文本类 GEO 资源补压缩（nginx gzip_static 可直接命中） ---- */
for (const f of ['llms.txt', 'llms-full.txt', 'openapi.json', 'api-docs.md']) {
  try {
    writeWithCompression(join(DIST, f), readFileSync(join(DIST, f), 'utf8'))
  } catch { /* 文件不存在则跳过 */ }
}

console.log(`=== 静态落地页 ===`)
console.log(`生成 ${n} 个页面（工具页 ${pages.filter(p => p.path.startsWith('/tools/')).length} 个、模型页 ${pages.filter(p => p.path.startsWith('/model/')).length} 个）+ sitemap.xml（${seenLoc.size} 条 URL，含根路径）`)
console.log(`站点主域：${SITE}`)
