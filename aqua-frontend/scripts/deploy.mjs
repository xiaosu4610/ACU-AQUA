// 一键零停机发布：scp 资产文件（带指纹，与旧版不同名、互不冲突）→ 最后覆盖 index.html
// 用 spawnSync 参数数组直调 ssh/scp，不经本地 shell，规避引号与 % 转义问题
import { spawnSync } from 'node:child_process'
import { readdirSync, statSync, existsSync } from 'node:fs'
import { join } from 'node:path'

const HOST = 'aquaus' // 生产前端在美国机（192.204.56.83 /data/aqua/frontend，nginx 443 直出；ssh 别名见 ~/.ssh/config）
const REMOTE = '/data/aqua/frontend'
const DIST = new URL('../dist', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')

if (!existsSync(join(DIST, 'index.html'))) { console.error('dist 不存在，先 npm run build'); process.exit(1) }

function run(cmd, args) {
  console.log('> ' + cmd + ' ' + args.join(' '))
  const r = spawnSync(cmd, args, { stdio: 'inherit', shell: false })
  if (r.status !== 0 || r.error) { console.error('❌ 命令失败: ' + cmd); process.exit(1) }
}

// 1) 备份线上入口（回滚用）并确保 assets 目录存在
run('ssh', [HOST, `cp -f ${REMOTE}/index.html ${REMOTE}/index.html.bak 2>/dev/null; mkdir -p ${REMOTE}/assets`])

// 2) 先传全部资产（指纹文件名，新旧共存互不影响；.gz/.br 预压缩文件一并上传）
run('scp', ['-q', '-r', join(DIST, 'assets'), `${HOST}:${REMOTE}/`])
for (const f of readdirSync(DIST)) {
  const p = join(DIST, f)
  if (f === 'assets' || f === 'index.html') continue
  if (statSync(p).isFile()) run('scp', ['-q', p, `${HOST}:${REMOTE}/${f}`])
}

// 3) 最后覆盖 index.html —— 此刻新访客切到新版，旧访客用已缓存的旧指纹文件，无白屏无 404
run('scp', ['-q', join(DIST, 'index.html'), `${HOST}:${REMOTE}/index.html`])

// 4) 线上验证
console.log('=== 发布完成，线上验证 ===')
run('ssh', [HOST, `curl -s -o /dev/null -w 'nginx直出 %{http_code}\\n' http://127.0.0.1/ -H 'Host: acu.ltzy.top' && curl -s -o /dev/null -w 'public %{http_code}\\n' -m 20 https://acu.ltzy.top/`])
console.log('✅ 零停机发布成功。如需回滚：npm run rollback')
