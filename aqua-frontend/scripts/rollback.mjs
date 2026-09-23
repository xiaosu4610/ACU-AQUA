import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { gunzipSync, brotliDecompressSync } from 'node:zlib'
const version = process.argv[2]
if (!/^\d{14}$/.test(version || '')) throw new Error('Usage: node scripts/rollback.mjs <14-digit release version>')
// EXCHANGE 双执行 = 撤销回滚（把新版本重新上线）：现役 frontend 若带回滚标记则拒绝重复执行
// 20260921：目标改为香港主节点 HK-2（此前硬编码 aquaus=已退役美国机）
const host = 'aquahk2'
const hk1 = 'aquahk1'
const marked = spawnSync('ssh',[host,'ls /data/aqua/frontend/.rolled-back-* >/dev/null 2>&1'],{shell:false})
if (marked.status === 0) throw new Error('该版本已执行过回滚，重复执行会把新版本重新上线；如确需再次交换请先删除标记文件（/data/aqua/frontend/.rolled-back-*）')
const previous = `/data/aqua/frontend-release-${version}`
const entries = ['index.html', 'index.html.gz', 'index.html.br']
const contents = entries.map(file => {
  const result = spawnSync('ssh', [host, `cat ${previous}/${file}`], {shell:false, maxBuffer:16 * 1024 * 1024})
  if (result.status !== 0 || result.error) throw new Error(`Unable to verify rollback entry: ${file}`)
  return result.stdout
})
if (!gunzipSync(contents[1]).equals(contents[0]) || !brotliDecompressSync(contents[2]).equals(contents[0])) {
  throw new Error('Rollback HTML, gzip and Brotli entries must match')
}
const entryChecks = entries.map((file, i) => `test "$(sha256sum ${previous}/${file} | cut -d ' ' -f 1)" = '${createHash('sha256').update(contents[i]).digest('hex')}'`).join('; ')
// Retain new assets when restoring the old entry, so already-open clients still load chunks.
const code = `import ctypes,os; libc=ctypes.CDLL(None,use_errno=True); result=libc.renameat2(-100,b'/data/aqua/frontend',-100,b'${previous}',2); assert result==0,os.strerror(ctypes.get_errno())`
const healthURL = process.env.DEPLOY_HEALTH_URL || 'https://acu.ltzy.top/home'
// EXCHANGE 完成且三件套校验通过后，立即在交换后的现役 frontend 目录写入回滚标记；
// 健康检查独立执行：失败时旧版本已上线（标记已落盘，重复回滚会被开头检查拒绝）
const result = spawnSync('ssh',[host,`set -eu; ${entryChecks}; cp -an /data/aqua/frontend/assets/. ${previous}/assets/; python3 -c "${code}"; touch /data/aqua/frontend/.rolled-back-from-${version}; sha256sum /data/aqua/frontend/index.html`],{stdio:'inherit',shell:false})
if (result.status !== 0 || result.error) process.exit(1)
const health = spawnSync('ssh',[host,`curl -fsS -o /dev/null ${healthURL}`],{shell:false})
if (health.status !== 0 || health.error) {
  console.error(`旧版本已交换上线，但健康检查失败：${healthURL}。回滚标记已写入 /data/aqua/frontend/.rolled-back-from-${version}`)
  process.exit(1)
}
console.log('健康检查通过')
// HK-1 已于 20260922 改为纯反代（DNS 只解析 HK-2）→ 无需同步前端，只做反代健康检查
const hk1Health = spawnSync('ssh',[hk1,`curl -fsS -o /dev/null --resolve acu.ltzy.top:443:127.0.0.1 ${healthURL}`],{shell:false})
if (hk1Health.status !== 0 || hk1Health.error) {
  console.error(`HK-1 反代健康检查失败：${healthURL}。请排查 HK-1 nginx（它仍承接 DNS 缓存残留流量）`)
  process.exit(1)
}
console.log('HK-1 反代健康检查通过')
console.log(`Frontend exchanged with ${previous}. Database and gateway unchanged.`)
