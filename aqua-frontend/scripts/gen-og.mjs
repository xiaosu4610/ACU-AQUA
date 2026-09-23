/* ============================================================================
 * OG 分享卡片生成（1200×630）
 *
 * 用途：og:image / twitter:image —— 微信、QQ、X、Telegram 等分享时的预览大图，
 * 直接影响社交引流的点击率（此前无 og:image，分享只显示纯文字）。
 *
 * 为什么本地渲染而不是文生图：卡片上必须出现清晰可读的中文品牌文案与域名，
 * 文生图模型无法可靠渲染文字，会出现错字/糊字，反而损害品牌。
 *
 * 用法（需本机已装 Edge/Chromium；不在 npm run build 链路里，改设计后手动重跑）：
 *   node scripts/gen-og.mjs
 * 产物：public/og-cover.png（随前端一起发布到站点根）
 * ==========================================================================*/
import { chromium } from 'playwright'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

const OUT = fileURLToPath(new URL('../public/og-cover.png', import.meta.url))

const html = `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><style>
*{margin:0;padding:0;box-sizing:border-box}
body{width:1200px;height:630px;overflow:hidden;background:#04060c;
  font-family:system-ui,-apple-system,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;color:#e8eef8}
.wrap{position:relative;width:1200px;height:630px;padding:74px 80px;display:flex;flex-direction:column;justify-content:space-between}
.glow{position:absolute;inset:0;background:
  radial-gradient(900px 520px at 82% 8%, rgba(11,99,197,.42), transparent 62%),
  radial-gradient(700px 420px at 6% 96%, rgba(24,190,220,.20), transparent 66%);}
.grid{position:absolute;inset:0;opacity:.30;
  background-image:linear-gradient(rgba(120,170,230,.09) 1px,transparent 1px),
                   linear-gradient(90deg,rgba(120,170,230,.09) 1px,transparent 1px);
  background-size:60px 60px;mask-image:linear-gradient(180deg,rgba(0,0,0,.9),transparent 78%)}
.inner{position:relative;display:flex;flex-direction:column;height:100%;justify-content:space-between}
.brand{display:flex;align-items:center;gap:18px}
.mark{width:62px;height:62px;border-radius:16px;display:grid;place-items:center;font-size:34px;font-weight:800;color:#fff;
  background:linear-gradient(145deg,#1273e6,#0b4fa5);box-shadow:0 12px 30px rgba(11,99,197,.45)}
.brand b{font-size:32px;letter-spacing:.02em;font-weight:750}
.brand span{display:block;font-size:14px;color:#8fa4bf;letter-spacing:.16em;margin-top:5px}
.mid h1{font-size:70px;line-height:1.14;letter-spacing:-.035em;font-weight:800}
.mid h1 em{font-style:normal;background:linear-gradient(96deg,#39a6ff,#12d0e6);-webkit-background-clip:text;background-clip:text;color:transparent}
.mid p{margin-top:22px;font-size:24px;color:#a8bcd6;letter-spacing:.01em}
.chips{display:flex;gap:12px;margin-top:30px}
.chip{font-size:15px;color:#cfe0f5;border:1px solid rgba(120,170,230,.32);background:rgba(18,60,120,.30);
  padding:9px 17px;border-radius:999px;letter-spacing:.01em}
.foot{display:flex;align-items:flex-end;justify-content:space-between}
.foot .mono{font:600 21px/1 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;color:#7fc4ff;letter-spacing:.02em}
.foot .tag{font-size:15px;color:#7d92ad}
</style></head><body>
<div class="wrap"><div class="glow"></div><div class="grid"></div>
  <div class="inner">
    <div class="brand"><div class="mark">A</div><div><b>AQUA api</b><span>ACU 工程系列</span></div></div>
    <div class="mid">
      <h1>免费 <em>AI API</em> 网关</h1>
      <p>OpenAI 兼容 · 多模型聚合 · 注册即用，改一个 base_url 就能接入</p>
      <div class="chips">
        <div class="chip">acu/ 公益免费线</div>
        <div class="chip">aqua/ 按次计费</div>
        <div class="chip">aqua/ 按量计费</div>
      </div>
    </div>
    <div class="foot">
      <div class="mono">acu.ltzy.top</div>
      <div class="tag">模型广场 · API 文档 · 免费工具箱</div>
    </div>
  </div>
</div></body></html>`

const browser = await chromium.launch({ headless: true, channel: 'msedge' })
const page = await browser.newPage({ viewport: { width: 1200, height: 630 }, deviceScaleFactor: 1 })
await page.setContent(html, { waitUntil: 'load' })
await page.screenshot({ path: OUT })
await browser.close()
console.log('已生成 OG 分享图：public/og-cover.png（1200×630）')
