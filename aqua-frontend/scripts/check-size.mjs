// 体积预算卡口：超预算构建失败（防止"越写越肥"重演单文件 461KB 的教训）
import { readdirSync, statSync, readFileSync } from 'node:fs'
import { join, relative } from 'node:path'

const DIST = new URL('../dist', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) walk(p, out)
    else out.push(p)
  }
  return out
}

const files = walk(DIST).filter(f => /\.(js|css|html|svg|json)$/.test(f))
// 压缩插件对低于阈值的超小文件可能不生成 .gz/.br，此时按原始体积计（更保守）
const gz = f => { try { return readFileSync(f + '.gz').length } catch { return statSync(f).size } }

// 分类：assets/ 与根 index.html = 应用体积；其余 .html = prerender 产出的 SEO 落地页
// （形如 dist/models.html、dist/tools/ip.html）。落地页属内容资产，单独预算，
// 避免"页面越多应用预算越紧"的误伤。
const sep = process.platform === 'win32' ? '\\' : '/'
const isRoutePage = f => {
  const r = relative(DIST, f)
  if (r === 'index.html') return false
  if (r.startsWith('assets' + sep)) return false
  return r.endsWith('.html')
}

const appFiles = files.filter(f => !isRoutePage(f))
const seoFiles = files.filter(isRoutePage)

const total = appFiles.reduce((s, f) => s + gz(f), 0)
const seoTotal = seoFiles.reduce((s, f) => s + gz(f), 0)
const byFile = appFiles.map(f => ({ f: relative(DIST, f), size: gz(f) })).sort((a, b) => b.size - a.size)

// 首屏 = index.html + 首个加载的 js/css（vite 入口 chunk 与其 css）
const html = gz(join(DIST, 'index.html'))
// 入口 chunk 通常是体量最大的非异步 chunk；简化：取全部非懒加载文件不易判定，改用 index.html 中直接引用的资源
const htmlSrc = readFileSync(join(DIST, 'index.html'), 'utf8')
const refs = [...htmlSrc.matchAll(/(?:src|href)="\.?\/?(assets\/[^"]+)"/g)].map(m => m[1])
const firstLoad = refs.reduce((s, r) => { const f = join(DIST, r); try { return s + gz(f) } catch { return s } }, html)

// SEO 落地页数量随在架模型数增长（公益通道动态目录会持续新增），故总量上限给足；
// 真正卡口是**单页上限**——防止某一页正文失控膨胀。
const BUDGET = { firstLoad: 120 * 1024, chunk: 60 * 1024, total: 350 * 1024, seo: 900 * 1024, seoPage: 12 * 1024 }
const KB = n => (n / 1024).toFixed(1) + 'KB'

console.log('=== 体积预算检查（gzip 实测）===')
console.log(`首屏(index.html+入口资源): ${KB(firstLoad)} / 预算 ${KB(BUDGET.firstLoad)} ${firstLoad <= BUDGET.firstLoad ? '✅' : '❌'}`)
console.log(`应用总量(不含 SEO 落地页): ${KB(total)} / 预算 ${KB(BUDGET.total)} ${total <= BUDGET.total ? '✅' : '❌'}`)
console.log(`SEO 落地页(${seoFiles.length} 个文件): ${KB(seoTotal)} / 预算 ${KB(BUDGET.seo)} ${seoTotal <= BUDGET.seo ? '✅' : '❌'}`)
console.log('最大文件 TOP5:', byFile.slice(0, 5).map(x => `${x.f} ${KB(x.size)}`).join(', '))

const badChunk = byFile.filter(x => x.size > BUDGET.chunk && !x.f.endsWith('.html'))
badChunk.forEach(x => console.log(`❌ 单文件超预算: ${x.f} ${KB(x.size)} > ${KB(BUDGET.chunk)}`))

const badPage = seoFiles.map(f => ({ f: relative(DIST, f), size: gz(f) })).filter(x => x.size > BUDGET.seoPage)
badPage.forEach(x => console.log(`❌ 落地页超预算: ${x.f} ${KB(x.size)} > ${KB(BUDGET.seoPage)}`))

if (firstLoad > BUDGET.firstLoad || total > BUDGET.total || seoTotal > BUDGET.seo || badChunk.length || badPage.length) {
  console.error('=== 体积预算超标，构建失败 ===')
  process.exit(1)
}
console.log('✅ 体积预算全部达标')
