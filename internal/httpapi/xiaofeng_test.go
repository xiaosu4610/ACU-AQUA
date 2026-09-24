// 双通道支付回归（20260922）：签名口径 / 渠道路由 / 熔断降级 / 金额互斥锁 / 回调归因。
//
// 这些不变量错一条的后果都很重：
//   - 签名口径错 → 回调验签失败 → 用户付了钱不到账
//   - 金额锁错 → 同金额订单并存 → 平台按「通道+金额」匹配时把 A 的钱记到 B 头上
//   - 通道比对错 → 一条通道的回调能核销另一条通道的订单
//   - 熔断错 → 通道故障时微信支付整条断掉（或故障通道被一直使用）
package httpapi

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// xfTestSetup 给测试 App 配好自挂通道，网关指向一个**假平台**（返回实测同款响应）。
func xfTestSetup(t *testing.T, app *App) *httptest.Server {
	t.Helper()
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mapi.php":
			// 实测口径：成功码 code=1（不是易支付 V2 的 0），payurl 是收银台直链
			_, _ = w.Write([]byte(`{"code":1,"msg":"获取成功","trade_no":"T1",` +
				`"payurl":"https://pay.test/cashier/T1","price":"1.00","qrcode":"","urlscheme":"weixin://"}`))
		case "/api/pay/result":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"status":0}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(platform.Close)
	app.Cfg.Pay.Provider = payProviderXiaofeng
	app.Cfg.Pay.Xiaofeng.Gateway = platform.URL
	app.Cfg.Pay.Xiaofeng.PID = "XFPID"
	app.Cfg.Pay.Xiaofeng.Key = "xf-test-key"
	app.Cfg.Pay.Xiaofeng.Type = "wxpay"
	app.Cfg.Pay.Xiaofeng.NotifyBase = "https://acu.test"
	app.Cfg.Pay.Xiaofeng.ReturnBase = "https://acu.test"
	return platform
}

// 签名原文规范：剔除 sign/sign_type/空值 → ASCII 升序 k=v& → **末尾直接追加密钥**（不加分隔符）。
// 这条锁死很重要：写成 "&key=" 会验签失败（已对生产报文逐字节验证过）。
func TestPayMD5Sign_SpecAndKeyAppend(t *testing.T) {
	const key = "test-key"
	got := payMD5Sign(key, map[string]string{
		"money": "0.01", "pid": "P1", "type": "wxpay",
		"sign": "IGNORED", "sign_type": "MD5", "empty": "",
	})
	// 手算期望值：排序后 money < pid < type
	want := fmt.Sprintf("%x", md5.Sum([]byte("money=0.01&pid=P1&type=wxpay"+key)))
	if got != want {
		t.Fatalf("签名口径不符：got %s want %s", got, want)
	}
	// 反向断言：加分隔符的变体必须**不等于**正确值（防有人"顺手"改成 &key=）
	wrong := fmt.Sprintf("%x", md5.Sum([]byte("money=0.01&pid=P1&type=wxpay&key="+key)))
	if got == wrong {
		t.Fatal("末尾必须是直接追加密钥，不能加 &key= 分隔符")
	}
}

// 自挂回调验签：签名往返通过；任一参数被篡改必须失败；pid 不符必须失败。
func TestXiaofengVerifyNotify_RoundTripAndTamper(t *testing.T) {
	app, _, _ := newTestApp(t)
	xfTestSetup(t, app)

	form := url.Values{}
	form.Set("pid", "XFPID")
	form.Set("type", "wxpay")
	form.Set("out_trade_no", "XF1")
	form.Set("trade_no", "T1")
	form.Set("money", "1.00")
	form.Set("trade_status", "TRADE_SUCCESS")
	form.Set("sign_type", "MD5")
	params := map[string]string{}
	for k := range form {
		params[k] = form.Get(k)
	}
	form.Set("sign", payMD5Sign(app.Cfg.Pay.Xiaofeng.Key, params))

	if !app.xiaofengVerifyNotify(form) {
		t.Fatal("签名正确应验签通过")
	}
	// 篡改金额 → 必须失败
	bad := url.Values{}
	for k, v := range form {
		bad[k] = v
	}
	bad.Set("money", "9.99")
	if app.xiaofengVerifyNotify(bad) {
		t.Fatal("金额被篡改必须验签失败")
	}
	// pid 不符 → 必须失败
	badPID := url.Values{}
	for k, v := range form {
		badPID[k] = v
	}
	badPID.Set("pid", "OTHER")
	if app.xiaofengVerifyNotify(badPID) {
		t.Fatal("pid 不符必须验签失败")
	}
	// 成功标识：实测是易支付 V1 口径 trade_status=TRADE_SUCCESS（不是文档写的 status=paid）
	if !app.xiaofengNotifyPaid(form) {
		t.Fatal("trade_status=TRADE_SUCCESS 应判定为已支付")
	}
	if app.xiaofengNotifyPaid(url.Values{"status": {"paid"}}) {
		t.Fatal("不应认官方文档写的 status=paid（实测口径不是它）")
	}
}

// 渠道路由：微信走主通道（自挂）；支付宝恒走易支付；主通道没配自挂时微信回落易支付。
func TestProviderForChannel_Routing(t *testing.T) {
	app, _, _ := newTestApp(t)
	xfTestSetup(t, app)

	if got := app.providerForChannel("wxpay"); got != payProviderXiaofeng {
		t.Fatalf("微信应走自挂主通道，得 %s", got)
	}
	if got := app.providerForChannel("alipay"); got != payProviderEpay {
		t.Fatalf("支付宝必须走易支付（自挂平台没有支付宝通道），得 %s", got)
	}
	// 主通道没配自挂 → 微信也应走易支付（保持旧行为）
	app.Cfg.Pay.Provider = "epay"
	if got := app.providerForChannel("wxpay"); got != payProviderEpay {
		t.Fatalf("provider=epay 时微信应走易支付，得 %s", got)
	}
	// provider 声明自挂但自挂没配齐 → 仍回落易支付（不能因为配置缺失就整条断掉）
	app.Cfg.Pay.Provider = payProviderXiaofeng
	app.Cfg.Pay.Xiaofeng.Key = ""
	if got := app.providerForChannel("wxpay"); got != payProviderEpay {
		t.Fatalf("自挂未配齐时微信应回落易支付，得 %s", got)
	}
}

// 熔断：连续失败达阈值即打开并降级；到期半开恢复；成功即清除。
func TestPayCircuitBreaker_OpenAndAutoRecover(t *testing.T) {
	app, _, _ := newTestApp(t)
	xfTestSetup(t, app)

	if app.payProviderOpen(payProviderXiaofeng) {
		t.Fatal("初始不应熔断")
	}
	for i := 0; i < payCircuitFailThreshold; i++ {
		app.payProviderFail(payProviderXiaofeng, "test")
	}
	if !app.payProviderOpen(payProviderXiaofeng) {
		t.Fatal("连续失败达阈值应打开熔断")
	}
	if got := app.wxProvider(); got != payProviderEpay {
		t.Fatalf("熔断中微信应自动降级易支付，得 %s", got)
	}
	// 把熔断到期时间拨到过去 → 半开放行（不再拦新单）
	if _, err := app.DB.Exec("UPDATE pay_provider_state SET until_ts=? WHERE provider=?",
		time.Now().Unix()-1, payProviderXiaofeng); err != nil {
		t.Fatal(err)
	}
	if app.payProviderOpen(payProviderXiaofeng) {
		t.Fatal("熔断到期后应半开放行")
	}
	if got := app.wxProvider(); got != payProviderXiaofeng {
		t.Fatalf("半开应恢复自挂，得 %s", got)
	}
	// 成功一次 → 彻底清除
	app.payProviderOK(payProviderXiaofeng)
	if app.payProviderOpen(payProviderXiaofeng) {
		t.Fatal("成功后应清除熔断")
	}
	var fails int
	_ = app.DB.QueryRow("SELECT fail_count FROM pay_provider_state WHERE provider=?", payProviderXiaofeng).Scan(&fails)
	if fails != 0 {
		t.Fatalf("成功后失败计数应清零，得 %d", fails)
	}
}

// 金额互斥锁：同金额第二次占用必须失败；释放后可再占；过期自动失效。
func TestPayAmountLock_MutexReleaseAndExpiry(t *testing.T) {
	app, _, _ := newTestApp(t)
	now := time.Now().Unix()

	if !app.payLockAcquire(1_000_000, "A", now+300) {
		t.Fatal("首次占锁应成功")
	}
	if app.payLockAcquire(1_000_000, "B", now+300) {
		t.Fatal("同金额第二次占锁必须失败（互斥）")
	}
	if got := app.payLockWaitSecs(1_000_000); got <= 0 {
		t.Fatalf("被占用时应返回正等待秒数，得 %d", got)
	}
	// 不同金额互不影响
	if !app.payLockAcquire(2_000_000, "C", now+300) {
		t.Fatal("不同金额应可同时占用")
	}
	// 只释放自己的：拿别人的单号放不掉
	app.payLockRelease(1_000_000, "NOT-MINE")
	if app.payLockWaitSecs(1_000_000) <= 0 {
		t.Fatal("用他人单号不应释放掉锁")
	}
	app.payLockRelease(1_000_000, "A")
	if app.payLockWaitSecs(1_000_000) != 0 {
		t.Fatal("释放后该金额应空闲")
	}
	// 过期自动失效（平台订单窗口已过 → 锁不该继续挡人）
	if !app.payLockAcquire(3_000_000, "D", now-1) {
		t.Fatal("占锁本身应成功")
	}
	if app.payLockWaitSecs(3_000_000) != 0 {
		t.Fatal("release_ts 已过期的锁应视为空闲")
	}
}

// 同金额冲突时下单必须返回「排队」且**不落单**（否则会制造同金额并存 → 错配）。
func TestPayCreate_XiaofengQueuesWhenAmountLocked(t *testing.T) {
	app, _, _ := newTestApp(t)
	xfTestSetup(t, app)
	_, tok := seedPoolUser(t, app, 0)

	if !app.payLockAcquire(1_000_000, "HOLDER", time.Now().Unix()+300) {
		t.Fatal("占锁应成功")
	}
	rec, out := doJSON(t, app.Routes(), "POST", "/v1/pay/create", tok, map[string]any{
		"amount_micro": 1_000_000, "channel": "wxpay", "product": "balance",
	})
	if rec.Code != 200 {
		t.Fatalf("排队也应 200（前端据此轮询），得 %d：%v", rec.Code, out)
	}
	if out["queued"] != true {
		t.Fatalf("同金额冲突应返回 queued=true，得 %v", out)
	}
	if n := countRows(t, app, "SELECT COUNT(*) FROM payments"); n != 0 {
		t.Fatalf("排队时不应落单，得 %d 笔", n)
	}
}

// 自挂下单成功：落单带 provider/expire_ts，返回 payurl 与平台单号，且金额锁被持有。
func TestPayCreate_XiaofengHappyPath(t *testing.T) {
	app, _, _ := newTestApp(t)
	xfTestSetup(t, app)
	_, tok := seedPoolUser(t, app, 0)

	rec, out := doJSON(t, app.Routes(), "POST", "/v1/pay/create", tok, map[string]any{
		"amount_micro": 1_000_000, "channel": "wxpay", "product": "balance",
	})
	if rec.Code != 200 {
		t.Fatalf("下单应 200，得 %d：%v", rec.Code, out)
	}
	if out["pay_url"] != "https://pay.test/cashier/T1" {
		t.Fatalf("pay_url 应为平台收银台直链，得 %v", out["pay_url"])
	}
	if out["provider"] != payProviderXiaofeng {
		t.Fatalf("provider 应为 xiaofeng，得 %v", out["provider"])
	}
	if exp, _ := out["expire_ts"].(float64); exp <= float64(time.Now().Unix()) {
		t.Fatalf("自挂订单必须带未来失效时刻（前端倒计时用），得 %v", out["expire_ts"])
	}
	if n := countRows(t, app, "SELECT COUNT(*) FROM payments WHERE provider='xiaofeng' AND trade_no='T1'"); n != 1 {
		t.Fatalf("应落 1 笔自挂订单且带平台单号，得 %d", n)
	}
	if app.payLockWaitSecs(1_000_000) <= 0 {
		t.Fatal("下单成功后金额锁应被持有（防止同金额并存）")
	}
}

// 回调入账：金额以订单为准，入账后**必须释放金额锁**（否则该档位被锁死 5 分钟）。
func TestPayNotify_XiaofengSettlesAndReleasesLock(t *testing.T) {
	app, _, _ := newTestApp(t)
	xfTestSetup(t, app)
	uid, _ := seedPoolUser(t, app, 0)

	if _, err := app.DB.Exec(`INSERT INTO payments
		(out_trade_no, user_id, amount_micro, channel, product, provider, expire_ts, status, ip, created_ts)
		VALUES ('XF1',?,1000000,'wxpay','balance','xiaofeng',?, 'pending','',?)`,
		uid, time.Now().Unix()+300, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if !app.payLockAcquire(1_000_000, "XF1", time.Now().Unix()+300) {
		t.Fatal("占锁应成功")
	}

	form := url.Values{}
	form.Set("pid", "XFPID")
	form.Set("type", "wxpay")
	form.Set("out_trade_no", "XF1")
	form.Set("trade_no", "T1")
	form.Set("money", "1.00")
	form.Set("trade_status", "TRADE_SUCCESS")
	form.Set("sign_type", "MD5")
	params := map[string]string{}
	for k := range form {
		params[k] = form.Get(k)
	}
	form.Set("sign", payMD5Sign(app.Cfg.Pay.Xiaofeng.Key, params))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/pay/notify?"+form.Encode(), nil)
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != 200 || rec.Body.String() != "success" {
		t.Fatalf("回调应 200 且回纯文本 success，得 %d %q", rec.Code, rec.Body.String())
	}
	if got := userBalanceOf(t, app, uid); got != 1_000_000 {
		t.Fatalf("应入账 1000000，得 %d", got)
	}
	if app.payLockWaitSecs(1_000_000) != 0 {
		t.Fatal("到账后金额锁必须释放")
	}
	// 幂等：重复回调不应重复入账
	rec2 := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/v1/pay/notify?"+form.Encode(), nil))
	if got := userBalanceOf(t, app, uid); got != 1_000_000 {
		t.Fatalf("重复回调不应重复入账，得 %d", got)
	}
}

// 「备用通道」标记只能由**后端**给出：
//   - 主通道 = 易支付（自挂没启用）→ 微信单**不能**被标成备用通道（否则每笔都误标）
//   - 主通道 = 自挂且已熔断 → 微信单降级到易支付，**必须**标备用通道
func TestPayCreate_FallbackFlagOnlyWhenDegraded(t *testing.T) {
	// 假易支付网关（V1 mapi.php）
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"msg":"ok","payurl":"https://epay.test/pay/x","trade_no":"E1"}`))
	}))
	defer gw.Close()

	setup := func(t *testing.T, provider string) (*App, string) {
		t.Helper()
		app, _, _ := newTestApp(t)
		app.Cfg.EPay.Gateway = gw.URL
		app.Cfg.EPay.PID = "EPPID"
		app.Cfg.EPay.Key = "epay-test-key"
		app.Cfg.EPay.APIVersion = "" // V1
		app.Cfg.Pay.Provider = provider
		_, tok := seedPoolUser(t, app, 0)
		return app, tok
	}
	body := map[string]any{"amount_micro": 10000, "channel": "wxpay", "product": "balance"}

	// A：主通道 = 易支付 → 不是备用通道
	appA, tokA := setup(t, "epay")
	recA, outA := doJSON(t, appA.Routes(), "POST", "/v1/pay/create", tokA, body)
	if recA.Code != 200 {
		t.Fatalf("下单应 200，得 %d：%v", recA.Code, outA)
	}
	if outA["fallback"] != false {
		t.Fatalf("主通道=易支付时不该标备用通道，得 %v", outA["fallback"])
	}

	// B：主通道 = 自挂且已熔断 → 降级易支付，必须标备用通道
	appB, tokB := setup(t, payProviderXiaofeng)
	appB.Cfg.Pay.Xiaofeng.Gateway = "https://pay.test"
	appB.Cfg.Pay.Xiaofeng.PID = "XFPID"
	appB.Cfg.Pay.Xiaofeng.Key = "xf-test-key"
	for i := 0; i < payCircuitFailThreshold; i++ {
		appB.payProviderFail(payProviderXiaofeng, "test")
	}
	recB, outB := doJSON(t, appB.Routes(), "POST", "/v1/pay/create", tokB, body)
	if recB.Code != 200 {
		t.Fatalf("降级下单应 200，得 %d：%v", recB.Code, outB)
	}
	if outB["fallback"] != true {
		t.Fatalf("熔断降级时必须标备用通道，得 %v", outB["fallback"])
	}
	if outB["provider"] != payProviderEpay {
		t.Fatalf("熔断时微信应降级易支付，得 %v", outB["provider"])
	}
}

// 防串通道：自挂订单收到「用易支付密钥签的回调」必须拒绝（否则一条通道可核销另一条的订单）。
func TestPayNotify_RejectsCrossProviderCallback(t *testing.T) {
	app, _, _ := newTestApp(t)
	xfTestSetup(t, app)
	// 同时把易支付也配起来（模拟双通道并存）
	app.Cfg.EPay.Gateway = "https://epay.test"
	app.Cfg.EPay.PID = "EPPID"
	app.Cfg.EPay.Key = "epay-test-key"

	uid, _ := seedPoolUser(t, app, 0)
	if _, err := app.DB.Exec(`INSERT INTO payments
		(out_trade_no, user_id, amount_micro, channel, product, provider, expire_ts, status, ip, created_ts)
		VALUES ('XF9',?,1000000,'wxpay','balance','xiaofeng',0,'pending','',?)`,
		uid, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	// 用易支付密钥签、但 pid 填易支付的 → 通道判定为 epay，而订单 provider=xiaofeng → 必须 400
	form := url.Values{}
	form.Set("pid", "EPPID")
	form.Set("type", "wxpay")
	form.Set("out_trade_no", "XF9")
	form.Set("trade_no", "T9")
	form.Set("money", "1.00")
	form.Set("trade_status", "TRADE_SUCCESS")
	form.Set("sign_type", "MD5")
	params := map[string]string{}
	for k := range form {
		params[k] = form.Get(k)
	}
	form.Set("sign", payMD5Sign(app.Cfg.EPay.Key, params))

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/pay/notify?"+form.Encode(), nil))
	if rec.Code == 200 && rec.Body.String() == "success" {
		t.Fatal("跨通道回调必须被拒绝")
	}
	if got := userBalanceOf(t, app, uid); got != 0 {
		t.Fatalf("跨通道回调不得入账，余额应为 0，得 %d", got)
	}
}

// —— 在线充值总开关（20260924 站长指令：关闭支付接口）——
//
// 红线：**只拦新单**。已下单待支付的用户付了钱必须照常到账，
// 否则就是"收了钱不发货"——比停售本身严重得多。
func TestPayEnabled_SwitchBlocksNewOrdersOnly(t *testing.T) {
	app, _, _ := newTestApp(t)
	xfTestSetup(t, app)
	uid, tok := seedPoolUser(t, app, 0)
	// settings 建表走包级 sync.Once（绑定首个 testApp 的 DB），同进程后续测试的
	// 新内存库不会自动建表 → 这里显式建，否则直接 INSERT 会撞 "no such table: settings"。
	if _, err := app.DB.Exec(settingsSchema); err != nil {
		t.Fatal(err)
	}

	// ① 无 pay_enabled 键 → 默认开放（加开关这个动作本身不得把支付关掉）
	if !app.payEnabled() {
		t.Fatal("settings 无 pay_enabled 键时应默认开放")
	}
	rec, out := doJSON(t, app.Routes(), "POST", "/v1/pay/create", tok, map[string]any{
		"amount_micro": 1_000_000, "channel": "wxpay", "product": "balance",
	})
	if rec.Code != 200 {
		t.Fatalf("默认应可下单，得 %d：%v", rec.Code, out)
	}

	// ② 显式 "0" → 新单 503 pay_disabled，且**不落单**
	if _, err := app.DB.Exec(`INSERT INTO settings (key,value,updated_ts) VALUES ('pay_enabled','0',?)
		ON CONFLICT(key) DO UPDATE SET value='0'`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if app.payEnabled() {
		t.Fatal("pay_enabled=0 应判定为已停售")
	}
	before := countRows(t, app, "SELECT COUNT(*) FROM payments")
	rec, out = doJSON(t, app.Routes(), "POST", "/v1/pay/create", tok, map[string]any{
		"amount_micro": 2_000_000, "channel": "wxpay", "product": "balance",
	})
	if rec.Code != 503 {
		t.Fatalf("停售后下单应 503，得 %d：%v", rec.Code, out)
	}
	if out["error"] == nil {
		t.Fatalf("应返回错误对象，得 %v", out)
	}
	if after := countRows(t, app, "SELECT COUNT(*) FROM payments"); after != before {
		t.Fatalf("停售后不得落单，订单数 %d → %d", before, after)
	}

	// ③ 停售**不影响**已下单待支付订单的回调入账（核心红线）
	if _, err := app.DB.Exec(`INSERT INTO payments
		(out_trade_no, user_id, amount_micro, channel, product, provider, expire_ts, status, ip, created_ts)
		VALUES ('PAYOFF1',?,3000000,'wxpay','balance','xiaofeng',0,'pending','',?)`,
		uid, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	form := url.Values{}
	form.Set("pid", "XFPID")
	form.Set("type", "wxpay")
	form.Set("out_trade_no", "PAYOFF1")
	form.Set("trade_no", "TOFF1")
	form.Set("money", "3.00")
	form.Set("trade_status", "TRADE_SUCCESS")
	form.Set("sign_type", "MD5")
	params := map[string]string{}
	for k := range form {
		params[k] = form.Get(k)
	}
	form.Set("sign", payMD5Sign(app.Cfg.Pay.Xiaofeng.Key, params))

	rec = httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/pay/notify?"+form.Encode(), nil))
	if rec.Code != 200 || rec.Body.String() != "success" {
		t.Fatalf("停售期间回调仍须 200 success，得 %d %q", rec.Code, rec.Body.String())
	}
	if got := userBalanceOf(t, app, uid); got != 3_000_000 {
		t.Fatalf("停售后已支付订单必须照常入账 3000000，得 %d", got)
	}

	// ④ 开关可逆：改回 "1" 即恢复开放（无需改配置/重启）
	if _, err := app.DB.Exec("UPDATE settings SET value='1' WHERE key='pay_enabled'"); err != nil {
		t.Fatal(err)
	}
	if !app.payEnabled() {
		t.Fatal("pay_enabled=1 应恢复开放")
	}
	rec, out = doJSON(t, app.Routes(), "POST", "/v1/pay/create", tok, map[string]any{
		"amount_micro": 3_000_000, "channel": "wxpay", "product": "balance",
	})
	if rec.Code != 200 {
		t.Fatalf("恢复开关后应可下单，得 %d：%v", rec.Code, out)
	}
}
