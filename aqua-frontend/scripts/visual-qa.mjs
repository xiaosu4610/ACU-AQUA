import { chromium } from 'playwright'
import assert from 'node:assert/strict'
import { mkdirSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const browser = await chromium.launch({ headless: true, channel: 'msedge' })
const output = join(tmpdir(), 'aqua-visual-qa')
const base = process.env.QA_URL || 'http://127.0.0.1:5173'
mkdirSync(output, { recursive: true })
const errors = []
const routes = ['/home', '/api', '/models?view=paid', '/model/aqua%2Fdeepseek-v4-1-flash', '/tools', '/login', '/status', '/playground', '/treehole', '/prompts', '/arena', '/community', '/sponsor', '/pool', '/console', '/finance', '/usage']
const fixture = [{ id:'acu/deepseek-v4-flash',paid:false },{id:'aqua/deepseek-v4-1-flash',paid:true,mode:'per_call',price_micro:3480,base_price_micro:4280,price_view:'agent'},{id:'codex/gpt-5',paid:true,mode:'per_token',in_price:2,cache_price:0.2,out_price:10}]
const checks = []
const authenticatedChecks = []
const ts = 1789776000
const history = [
  { endpoint:'chat/completions', model:'aqua/deepseek-v4-1-flash', ok:true, prompt_tokens:100, completion_tokens:50, cached_tokens:0, total_tokens:150, tps:50, latency_ms:1000, status_code:200, error:'', usage_source:'upstream', ts, billed:true, bill_amount_micro:3480, stream_mode:'nonstream' },
  { endpoint:'chat/completions', model:'codex/gpt-5', ok:true, prompt_tokens:1000, completion_tokens:50, cached_tokens:100, total_tokens:1050, tps:50, latency_ms:1000, status_code:200, error:'', usage_source:'upstream', ts, billed:true, bill_amount_micro:2480, stream_mode:'passthrough' },
  { endpoint:'chat/completions', model:'acu/deepseek-v4-flash', ok:true, total_tokens:150, latency_ms:1000, status_code:200, ts, bill_amount_micro:0 },
  { endpoint:'chat/completions', model:'aqua/deepseek-v4-1-flash', ok:false, total_tokens:0, latency_ms:500, status_code:502, error:'synthetic upstream failure', ts, bill_state:'refunded' },
]
async function authenticatedQA(theme, width) {
  const context = await browser.newContext({ viewport:{width,height:900}, colorScheme:theme, reducedMotion:'reduce', serviceWorkers:'block' })
  const page = await context.newPage()
  const calls = []
  let group = 'per_call'
  page.on('pageerror', e => errors.push(e.message))
  page.on('console', m => { if (m.type() === 'error') errors.push(m.text()) })
  await context.route('**/*', async route => {
    const request = route.request()
    const url = new URL(request.url())
    if (!url.pathname.startsWith('/v1/')) {
      if (url.origin === new URL(base).origin) return route.continue()
      return route.fulfill({status:200,body:''})
    }
    const path = url.pathname.slice(3)
    const agent = request.headers().authorization === 'Bearer synthetic-agent-session'
    calls.push({path,method:request.method(),agent,query:url.search})
    let body
    if (path === '/auth/login') body = {token:request.postDataJSON().account === 'agent@example.test' ? 'synthetic-agent-session' : 'synthetic-normal-session'}
    else if (path === '/auth/logout') body = {ok:true}
    else if (path === '/auth/me') body = {id:1,username:'Synthetic QA',email:'qa@example.test',avatar_ext:'',created_ts:ts,key_count:1}
    else if (path === '/models') body = {data:fixture.map(m => m.price_view ? {...m,price_micro:agent ? 3480 : 4280,price_view:agent ? 'agent' : 'normal',base_price_micro:agent ? 4280 : undefined} : m)}
    else if (path === '/models/status') body = {data:[],generated_ts:ts}
    else if (path === '/meta') body = {name:'AQUA QA',announcement_enabled:false}
    else if (path === '/pool/status') body = {balance_micro:10000000,today_used_micro:0,used_micro:0}
    else if (path === '/my/keys') body = {keys:[{id:1,name:'Synthetic QA key',prefix:'sk-qa-masked',created_ts:ts,revoked:false,can_reveal:false,billing_grp:group}]}
    else if (path === '/my/keys/1/group') { group = request.postDataJSON().billing_grp; body = {ok:true} }
    else if (path === '/my/usage') body = {today:{calls:4,ok_rate:75},week:{calls:40,ok_rate:95},by_model:[{model:history[0].model,calls:20},{model:history[1].model,calls:10}],recent:history}
    else if (path === '/my/checkup') body = {score:100,items:[{id:'qa',level:'ok',ok:true,title:'模拟检查',detail:'通过',advice:''}]}
    else if (path === '/my/history') body = {items:url.searchParams.get('page') === '2' ? [history[3]] : history,total:21}
    else if (path === '/my/balance') body = {balance_micro:12500000,today_cost_micro:5960,total_cost_micro:59600}
    else if (path === '/my/balance-alert') body = {threshold_micro:0,armed:false,email:'qa@example.test'}
    else if (path === '/invite/me') body = {code:'SYNTHQA',link:'/login?mode=register&code=SYNTHQA',invited:0,qualified:0,earned_micro:0,reward_each_micro:2000000,first_pay_min_micro:5000000,rebate_pct:10,list:[]}
    else if (path === '/pay/orders') body = {items:[{out_trade_no:'SYNTHETIC-ORDER',channel:'alipay',amount_micro:10000000,status:'paid',created_ts:ts}]}
    else if (path === '/my/finance') body = {balance_micro:12500000,spend_today:5960,spend_week:59600,spend_total:596000,by_model:[{model:history[0].model,amount_micro:3480,calls:1}],recent:history.slice(0,2).map(r => ({...r,amount_micro:r.bill_amount_micro})),topups:[{amount_micro:10000000,status:'paid',channel:'alipay',created_ts:ts,paid_ts:ts}]}
    else { errors.push(`Unexpected authenticated API: ${request.method()} ${path}`); return route.fulfill({status:400,json:{error:{message:'Unmocked API blocked'}}}) }
    await route.fulfill({json:body})
  })
  async function proof(flow, text) {
    await page.getByText(text,{exact:false}).first().waitFor()
    assert.ok(await page.title())
    assert.equal(await page.locator('vite-error-overlay').count(),0)
    assert.equal(await page.locator('html').getAttribute('data-theme'),theme)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)
    assert.equal(overflow,false,`${flow} overflow at ${width}`)
    authenticatedChecks.push({theme,width,flow,path:new URL(page.url()).pathname,overflow})
    console.log(`PASS ${theme} ${width}px: ${flow}`)
  }
  async function loginAs(account) {
    await page.getByPlaceholder('邮箱或用户名').fill(account)
    await page.getByPlaceholder('密码',{exact:true}).fill('Synthetic-only-123')
    await page.locator('form button[type="submit"]').click()
    await page.waitForURL('**/console')
    await page.getByRole('button',{name:'总览',exact:true}).click()
    await page.locator('.kpi b').filter({hasText:'¥12.5'}).waitFor()
  }
  // All accounts and credentials are synthetic; every API request is intercepted.
  await page.goto(base+'/login')
  await loginAs('normal@example.test')
  await proof('normal login → console','今日成功率 75%')
  for (const [tab,text] of [['API 密钥','Synthetic QA key'],['请求历史','synthetic upstream failure'],['邀请返利','SYNTHQA'],['账号设置','qa@example.test']]) {
    await page.getByRole('button',{name:tab,exact:true}).click()
    await proof(`console tab ${tab}`,text)
  }
  await page.getByRole('button',{name:'API 密钥',exact:true}).click()
  await page.locator('.grp-select').selectOption('free')
  await proof('key group update','计费分组已更新，立即生效')
  assert.equal(await page.locator('.grp-select').inputValue(),'free')
  await page.getByRole('button',{name:'请求历史',exact:true}).click()
  await page.getByRole('button',{name:'下一页',exact:true}).click()
  await proof('history pagination','第 2 / 2 页')
  await page.getByRole('button',{name:'余额充值',exact:true}).click()
  await page.getByRole('button',{name:'¥10',exact:true}).click()
  await page.getByRole('button',{name:'微信支付',exact:true}).click()
  await proof('topup preview only','预计到账余额')
  assert.match(await page.locator('.col-ok:visible').innerText(),/¥10/)
  assert.equal(calls.some(c=>c.path === '/pay/create'),false)
  await page.screenshot({path:join(output,`${width}-console-topup-${theme}.png`),fullPage:true})
  await page.getByRole('link',{name:'消费账单',exact:true}).click()
  await proof('console → finance','¥0.00596')
  assert.equal(new URL(page.url()).pathname,'/finance')
  await page.screenshot({path:join(output,`${width}-finance-${theme}.png`),fullPage:true})
  await page.getByRole('link',{name:'去充值',exact:true}).click()
  await proof('finance → topup deep link','在线充值（支付宝 / 微信）')
  await page.getByRole('link',{name:'我的用量',exact:true}).last().click()
  await proof('console → usage','按量')
  assert.equal(new URL(page.url()).pathname,'/usage')
  const usage = await page.locator('tbody').innerText()
  assert.match(usage,/¥0\.00348.*按次/s)
  assert.match(usage,/¥0\.00248.*按量/s)
  assert.match(usage,/免费/)
  await page.screenshot({path:join(output,`${width}-usage-${theme}.png`),fullPage:true})
  // Router navigation preserves the shared model cache across logout and login.
  async function spa(path) {
    await page.locator(`a[href="${path}"]:visible`).first().click()
    await page.waitForURL(url=>url.pathname === path)
  }
  if (await page.locator('.burger').isVisible()) await page.getByRole('button',{name:'菜单',exact:true}).click()
  await spa('/models')
  // Detail links are router links; follow their observed href through the DOM.
  const paidTab = page.getByRole('button',{name:'收费模型',exact:true})
  await paidTab.click()
  const detail = page.locator('a[href*="deepseek-v4-1-flash"]').first()
  await detail.click()
  await proof('normal model pricing','0.00428')
  assert.equal(await page.getByText('代理拿货价已生效',{exact:true}).count(),0)
  if (await page.locator('.burger').isVisible()) await page.getByRole('button',{name:'菜单',exact:true}).click()
  await spa('/console')
  await page.getByRole('button',{name:'退出登录',exact:true}).last().click()
  await page.waitForURL('**/login')
  await loginAs('agent@example.test')
  if (await page.locator('.burger').isVisible()) await page.getByRole('button',{name:'菜单',exact:true}).click()
  await spa('/models')
  await paidTab.click()
  await proof('agent model cards','代理结算价')
  await detail.click()
  await proof('agent model detail','代理拿货价已生效')
  assert.match(await page.locator('#app').innerText(),/0\.00348/)
  assert.match(await page.locator('#app').innerText(),/0\.00428/)
  await page.screenshot({path:join(output,`${width}-agent-detail-${theme}.png`),fullPage:true})
  if (await page.locator('.burger').isVisible()) await page.getByRole('button',{name:'菜单',exact:true}).click()
  await spa('/console')
  await page.getByRole('button',{name:'退出登录',exact:true}).last().click()
  await page.waitForURL('**/login')
  if (await page.locator('.burger').isVisible()) await page.getByRole('button',{name:'菜单',exact:true}).click()
  await spa('/models')
  await paidTab.click()
  await detail.click()
  await proof('agent logout → normal price restored','0.00428')
  assert.equal(await page.getByText('代理拿货价已生效',{exact:true}).count(),0)
  assert.ok(calls.some(c=>c.path === '/models' && c.agent))
  assert.ok(calls.some(c=>c.path === '/my/keys/1/group' && c.method === 'PATCH'))
  assert.ok(calls.some(c=>c.path === '/my/history' && c.query.includes('page=2')))
  await context.close()
  console.log(`Authenticated QA passed: ${theme} ${width}px`)
}
try {
  for (const theme of (process.env.QA_AUTH_ONLY ? [] : ['dark', 'light'])) {
    for (const width of [320,390,768,1440]) {
      const page = await browser.newPage({ viewport: {width,height:900}, colorScheme:theme, reducedMotion:'reduce' })
      page.on('pageerror', e => errors.push(e.message))
      page.on('console', message => { if (message.type() === 'error') errors.push(message.text()) })
      await page.route('**/*', async route => {
        const url = new URL(route.request().url())
        if (url.pathname.startsWith('/v1/')) {
          let body = {}
          if (url.pathname === '/v1/models') body = {data:fixture}
          else if (url.pathname === '/v1/status') body = {version:'synthetic-qa',window:'1h',models:[{model:'aqua/deepseek-v4-1-flash',calls_1h:10,success_rate:90,avg_latency_ms:1000}]}
          else if (url.pathname === '/v1/models/status') body = {data:[],generated_ts:0}
          else if (url.pathname.includes('prompts')) body = {data:[],prompts:[],categories:[]}
          await route.fulfill({json:body})
        } else if (url.origin === new URL(base).origin) await route.continue()
        else await route.fulfill({status:200,body:''})
      })
      for (const path of routes) {
        await page.goto(base+path)
        await page.locator('h1').first().waitFor()
        assert.ok(await page.title())
        assert.equal(await page.locator('vite-error-overlay').count(), 0)
        assert.equal(await page.locator('html').getAttribute('data-theme'), theme)
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)
        checks.push({theme,width,path,actualPath:new URL(page.url()).pathname,overflow})
        if (path === '/home' || (width === 1440 && path === '/models?view=paid')) await page.screenshot({path:join(output,`${width}-${path.split('?')[0].slice(1)}-${theme}.png`),fullPage:true})
        if (path.startsWith('/model/')) assert.match(await page.locator('body').innerText(), /0\.00348/)
        if (path === '/api') {
          await page.getByRole('button',{name:'python',exact:true}).click()
          assert.match(await page.locator('pre').first().innerText(),/from openai import/)
        }
        if (path === '/home') {
          const other = theme === 'dark' ? 'light' : 'dark'
          await page.locator('.theme-btn').click()
          assert.equal(await page.locator('html').getAttribute('data-theme'), other)
          await page.reload()
          await page.locator('h1').first().waitFor()
          assert.equal(await page.locator('html').getAttribute('data-theme'), other)
          await page.locator('.theme-btn').click()
          if (await page.locator('.burger').isVisible()) {
            await page.getByRole('button',{name:'菜单',exact:true}).click()
            assert.equal(await page.locator('#mobile-navigation').isVisible(), true)
            await page.getByRole('button',{name:'菜单',exact:true}).press('Escape')
            assert.equal(await page.locator('#mobile-navigation').count(), 0)
          }
        }
      }
      await page.close()
    }
  }
  for (const theme of ['dark','light']) for (const width of [320,390,768,1440]) await authenticatedQA(theme,width)
  console.log(JSON.stringify({browser:browser.version(),publicChecks:checks.length,authenticatedChecks:authenticatedChecks.length,overflows:checks.filter(c=>c.overflow),errors,output},null,2))
  assert.equal(errors.length,0,'Browser runtime/console errors')
  assert.equal(checks.filter(c=>c.overflow).length,0,'Page overflow')
} finally {
  await browser.close()
}
