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

const total = files.reduce((s, f) => s + gz(f), 0)
const byFile = files.map(f => ({ f: relative(DIST, f), size: gz(f) })).sort((a, b) => b.size - a.size)

// 首屏 = index.html + 首个加载的 js/css（vite 入口 chunk 与其 css）
const html = gz(join(DIST, 'index.html'))
const firstJs = byFile.filter(x => /assets\/.*\.js$/.test(x.f) && !/-\d+\.js$/.test(x.f) ? false : true)
// 入口 chunk 通常是体量最大的非异步 chunk；简化：取全部非懒加载文件不易判定，改用 index.html 中直接引用的资源
const htmlSrc = readFileSync(join(DIST, 'index.html'), 'utf8')
const refs = [...htmlSrc.matchAll(/(?:src|href)="\.?\/?(assets\/[^"]+)"/g)].map(m => m[1])
const firstLoad = refs.reduce((s, r) => { const f = join(DIST, r); try { return s + gz(f) } catch { return s } }, html)

const BUDGET = { firstLoad: 120 * 1024, chunk: 60 * 1024, total: 350 * 1024 }
const KB = n => (n / 1024).toFixed(1) + 'KB'

console.log('=== 体积预算检查（gzip 实测）===')
console.log(`首屏(index.html+入口资源): ${KB(firstLoad)} / 预算 ${KB(BUDGET.firstLoad)} ${firstLoad <= BUDGET.firstLoad ? '✅' : '❌'}`)
console.log(`全站总量: ${KB(total)} / 预算 ${KB(BUDGET.total)} ${total <= BUDGET.total ? '✅' : '❌'}`)
console.log('最大文件 TOP5:', byFile.slice(0, 5).map(x => `${x.f} ${KB(x.size)}`).join(', '))

const badChunk = byFile.filter(x => x.size > BUDGET.chunk && !x.f.endsWith('.html'))
badChunk.forEach(x => console.log(`❌ 单文件超预算: ${x.f} ${KB(x.size)} > ${KB(BUDGET.chunk)}`))

if (firstLoad > BUDGET.firstLoad || total > BUDGET.total || badChunk.length) {
  console.error('=== 体积预算超标，构建失败 ===')
  process.exit(1)
}
console.log('✅ 体积预算全部达标')
