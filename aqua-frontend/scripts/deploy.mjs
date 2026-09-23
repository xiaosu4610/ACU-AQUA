import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { gunzipSync, brotliDecompressSync } from 'node:zlib'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
// 20260921 香港双节点：部署目标是**香港主节点 HK-2**（此前硬编码 aquaus=已退役美国机，
// 会把新前端发到退役机、香港两台仍是旧版本 → 三机版本分裂）。
const host = 'aquahk2'
// HK-1 已于 20260922 摘除 DNS A 记录（DNS 现只解析到 HK-2），并**改为纯反代**：
// 不再本地服务静态，因此**不需要同步前端**——反代拿到的永远是 HK-2 的实时产物，
// 从根上消除了「发布瞬间 HK-1 还是旧版」的版本漂移窗口。
// 但它仍在承接**客户端 DNS 缓存残留**的流量，所以保留一次反代健康检查。
const hk1 = 'aquahk1'
const root = '/data/aqua'
const dist = fileURLToPath(new URL('../dist', import.meta.url))
const version = new Date().toISOString().replace(/[^0-9]/g, '').slice(0,14)
const stage = `${root}/frontend-release-${version}`
function run(command, args) {
  const result = spawnSync(command, args, {stdio:'inherit',shell:false})
  if (result.status !== 0 || result.error) process.exit(1)
}
const entries = ['index.html','index.html.gz','index.html.br']
const contents = entries.map(file => readFileSync(join(dist, file)))
if (!gunzipSync(contents[1]).equals(contents[0]) || !brotliDecompressSync(contents[2]).equals(contents[0])) {
  throw new Error('HTML, gzip and Brotli release entries must match')
}
const entryChecks = entries.map((file, i) => `test "$(sha256sum ${stage}/${file} | cut -d ' ' -f 1)" = '${createHash('sha256').update(contents[i]).digest('hex')}'`).join('; ')
const healthURL = process.env.DEPLOY_HEALTH_URL || 'https://acu.ltzy.top/home'
// Linux renameat2 exchanges complete directories atomically; the old release remains at stage.
const exchange = `import ctypes,os; libc=ctypes.CDLL(None,use_errno=True); result=libc.renameat2(-100,b'${root}/frontend',-100,b'${stage}',2); assert result==0,os.strerror(ctypes.get_errno())`
// 干净全量 + 保留历史 assets（20260919 紧急修复）：
// 此前用空 stage 整目录替换，导致旧版本 chunk（如 ConsolePage-<oldhash>.js）被删除——
// 老用户浏览器缓存的 index.html 仍引用旧 chunk，动态 import 404 → 路由跳转中断
// （症状：首页点头像无法进入控制台）。现改为：stage 先合并"现役目录的全部 assets"再覆盖新产物，
// 保证任何历史版本引用的 chunk 始终可取；index.html 等入口文件仍由新产物覆盖。
run('ssh',[host,`set -eu; test -d ${root}/frontend; test ! -e ${stage}; mkdir -p ${stage}/assets; cp -an ${root}/frontend/assets/. ${stage}/assets/`])
run('scp',['-q','-r',`${dist}/.`,`${host}:${stage}/`])
console.log(`Prepared ${version}. After the exchange, rollback with: node scripts/rollback.mjs ${version}`)
run('ssh',[host,`set -eu; ${entryChecks}; python3 -c "${exchange}"; sha256sum ${root}/frontend/index.html`])
// 健康检查独立于交换执行：失败时新版本已上线，需明确提示回滚路径而非笼统"部署失败"
const health = spawnSync('ssh',[host,`curl -fsS -o /dev/null ${healthURL}`],{shell:false})
if (health.status !== 0 || health.error) {
  console.error(`新版本已交换上线，但健康检查失败：${healthURL}。如需回滚：node scripts/rollback.mjs ${version}`)
  process.exit(1)
}
console.log('健康检查通过')
// HK-1 反代健康检查：失败必须显式报错（它仍在服务 DNS 缓存残留的老客户端，
// nginx 挂了这些用户会直接失败；不再做版本比对——纯反代天然与 HK-2 同版本）
const hk1Health = spawnSync('ssh',[hk1,`curl -fsS -o /dev/null --resolve acu.ltzy.top:443:127.0.0.1 ${healthURL}`],{shell:false})
if (hk1Health.status !== 0 || hk1Health.error) {
  console.error(`HK-1 反代健康检查失败：${healthURL}。请排查 HK-1 nginx（它仍承接 DNS 缓存残留流量）`)
  process.exit(1)
}
console.log('HK-1 反代健康检查通过')
console.log(`Published ${version}. Previous complete frontend: ${stage}`)
console.log(`Rollback: node scripts/rollback.mjs ${version}. Database and gateway unchanged.`)
