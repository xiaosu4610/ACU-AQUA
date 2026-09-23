/* ============================================================================
 * SEO 文案卡口（构建期，超限即构建失败）
 *
 * 为什么需要：Bing 站长工具曾报「大量页面标题太短 / 描述太短 / 标题重复 / 描述重复」——
 * 根因是纯 SPA 兜底导致所有动态页共用首页 title/description，且文案长期无人复核。
 * 现在 prerender 为每个公开路由生成独立页面，本脚本在构建期校验文案质量，防止再次漂移。
 *
 * 校验对象：dist 下所有 prerender 产出的 .html（即全部可被收录的页面）
 * 阈值依据：Bing 建议标题 ~60 字符内、描述 150 字符左右；中文信息密度更高，
 *          这里取标题 18~72、描述 60~165，并要求全局不重复。
 * ==========================================================================*/
import { readdirSync, statSync, readFileSync } from 'node:fs'
import { join, relative } from 'node:path'

const DIST = new URL('../dist', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')
const sep = process.platform === 'win32' ? '\\' : '/'
const LIMIT = { titleMin: 18, titleMax: 72, descMin: 60, descMax: 165 }

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) walk(p, out)
    else if (name.endsWith('.html')) out.push(p)
  }
  return out
}

const pick = (html, re) => { const m = html.match(re); return m ? m[1].trim() : '' }

const pages = walk(DIST).map(f => {
  const html = readFileSync(f, 'utf8')
  return {
    file: relative(DIST, f).split(sep).join('/'),
    title: pick(html, /<title>([\s\S]*?)<\/title>/),
    desc: pick(html, /<meta name="description" content="([^"]*)"/),
    canonical: pick(html, /<link rel="canonical" href="([^"]*)"/),
    ogImage: pick(html, /<meta property="og:image" content="([^"]*)"/),
    jsonld: /<script type="application\/ld\+json">/.test(html),
  }
})

const problems = []
// 同一 canonical 的多个文件是有意别名（/ 与 /home 内容相同、canonical 都指向根），
// 这类别名不参与「重复」判定，避免把设计意图误报成问题。
const byCanonical = new Set()
for (const p of pages) {
  if (!p.canonical) continue
  if (byCanonical.has(p.canonical)) p.alias = true
  else byCanonical.add(p.canonical)
}
const aliases = pages.filter(p => p.alias).length

for (const p of pages) {
  if (!p.title) problems.push(`${p.file}: 缺 <title>`)
  else if (p.title.length < LIMIT.titleMin) problems.push(`${p.file}: 标题过短 ${p.title.length} 字（<${LIMIT.titleMin}）：${p.title}`)
  else if (p.title.length > LIMIT.titleMax) problems.push(`${p.file}: 标题过长 ${p.title.length} 字（>${LIMIT.titleMax}）：${p.title}`)
  if (!p.desc) problems.push(`${p.file}: 缺 meta description`)
  else if (p.desc.length < LIMIT.descMin) problems.push(`${p.file}: 描述过短 ${p.desc.length} 字（<${LIMIT.descMin}）：${p.desc}`)
  else if (p.desc.length > LIMIT.descMax) problems.push(`${p.file}: 描述过长 ${p.desc.length} 字（>${LIMIT.descMax}）`)
  if (!p.canonical.startsWith('https://')) problems.push(`${p.file}: canonical 缺失或非绝对 URL`)
  if (!p.ogImage.startsWith('https://')) problems.push(`${p.file}: og:image 缺失或非绝对 URL`)
  if (!p.jsonld) problems.push(`${p.file}: 缺 JSON-LD 结构化数据`)
}

// 重复检测（Bing 明确会因此降权）：标题/描述必须在全站唯一
for (const field of ['title', 'desc']) {
  const seen = new Map()
  for (const p of pages) {
    if (!p[field] || p.alias) continue
    if (seen.has(p[field])) problems.push(`${p.file}: ${field === 'title' ? '标题' : '描述'}与 ${seen.get(p[field])} 重复`)
    else seen.set(p[field], p.file)
  }
}

console.log('=== SEO 文案卡口 ===')
console.log(`检查 ${pages.length} 个页面（含 ${aliases} 个 canonical 别名）：标题 ${LIMIT.titleMin}~${LIMIT.titleMax} 字、描述 ${LIMIT.descMin}~${LIMIT.descMax} 字、全站唯一、canonical / og:image / JSON-LD 齐备`)
if (problems.length) {
  console.error(`=== 发现 ${problems.length} 个问题，构建失败 ===`)
  problems.slice(0, 40).forEach(x => console.error('  ❌ ' + x))
  if (problems.length > 40) console.error(`  ... 另有 ${problems.length - 40} 条`)
  process.exit(1)
}
console.log('✅ SEO 文案全部达标')
