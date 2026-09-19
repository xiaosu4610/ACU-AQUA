import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { gunzipSync, brotliDecompressSync } from 'node:zlib'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
const host = 'aquaus'
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
// Linux renameat2 exchanges complete directories atomically; the old release remains at stage.
const exchange = `import ctypes,os; libc=ctypes.CDLL(None,use_errno=True); result=libc.renameat2(-100,b'${root}/frontend',-100,b'${stage}',2); assert result==0,os.strerror(ctypes.get_errno())`
run('ssh',[host,`set -eu; test -d ${root}/frontend; test ! -e ${stage}; cp -a ${root}/frontend ${stage}`])
run('scp',['-q','-r',`${dist}/.`,`${host}:${stage}/`])
console.log(`Prepared ${version}. After the exchange, rollback with: node scripts/rollback.mjs ${version}`)
run('ssh',[host,`set -eu; ${entryChecks}; python3 -c "${exchange}"; sha256sum ${root}/frontend/index.html; curl -fsS -o /dev/null https://aqua.ltzy.top/home`])
console.log(`Published ${version}. Previous complete frontend: ${stage}`)
console.log(`Rollback: node scripts/rollback.mjs ${version}. Database and gateway unchanged.`)
