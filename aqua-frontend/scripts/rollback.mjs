import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { gunzipSync, brotliDecompressSync } from 'node:zlib'
const version = process.argv[2]
if (!/^\d{14}$/.test(version || '')) throw new Error('Usage: node scripts/rollback.mjs <14-digit release version>')
const previous = `/data/aqua/frontend-release-${version}`
const entries = ['index.html', 'index.html.gz', 'index.html.br']
const contents = entries.map(file => {
  const result = spawnSync('ssh', ['aquaus', `cat ${previous}/${file}`], {shell:false, maxBuffer:16 * 1024 * 1024})
  if (result.status !== 0 || result.error) throw new Error(`Unable to verify rollback entry: ${file}`)
  return result.stdout
})
if (!gunzipSync(contents[1]).equals(contents[0]) || !brotliDecompressSync(contents[2]).equals(contents[0])) {
  throw new Error('Rollback HTML, gzip and Brotli entries must match')
}
const entryChecks = entries.map((file, i) => `test "$(sha256sum ${previous}/${file} | cut -d ' ' -f 1)" = '${createHash('sha256').update(contents[i]).digest('hex')}'`).join('; ')
// Retain new assets when restoring the old entry, so already-open clients still load chunks.
const code = `import ctypes,os; libc=ctypes.CDLL(None,use_errno=True); result=libc.renameat2(-100,b'/data/aqua/frontend',-100,b'${previous}',2); assert result==0,os.strerror(ctypes.get_errno())`
const result = spawnSync('ssh',['aquaus',`set -eu; ${entryChecks}; cp -an /data/aqua/frontend/assets/. ${previous}/assets/; python3 -c "${code}"; sha256sum /data/aqua/frontend/index.html; curl -fsS -o /dev/null https://aqua.ltzy.top/home`],{stdio:'inherit',shell:false})
if (result.status !== 0 || result.error) process.exit(1)
console.log(`Frontend exchanged with ${previous}. Database and gateway unchanged.`)
