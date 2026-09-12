// 一键回滚：把上一次发布的 index.html.bak 恢复回去（资产指纹文件无需动，旧版入口引用的是旧指纹）
// spawnSync 参数数组直调 ssh，不经本地 shell，规避引号与 % 转义问题
import { spawnSync } from 'node:child_process'
const r = spawnSync('ssh', ['aqua', `cp -f /data/aqua/frontend/index.html.bak /data/aqua/frontend/index.html && curl -s -o /dev/null -w 'rollback %{http_code}\\n' http://127.0.0.1:8788/`], { stdio: 'inherit', shell: false })
if (r.status !== 0 || r.error) { console.error('❌ 回滚失败'); process.exit(1) }
console.log('✅ 已回滚到上一版')
