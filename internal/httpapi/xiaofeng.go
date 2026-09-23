// 自建免签「自挂」支付通道（晓风/安逸云聚合支付，网关如 pay.61nb.vip）。
//
// 20260922 站长定稿双通道架构：
//   - 微信（wxpay）→ 本通道为主（钱直接进站长个人微信，平台不抽单笔），异常自动熔断降级易支付
//   - 支付宝（alipay）→ 恒走易支付（本平台没有支付宝通道）
//
// 全部口径均对生产网关**逐项实测**过（详见 docs/plans/自建免签支付通道对接研究-20260922.md）：
//   - 下单 POST mapi.php：MD5 签名，成功码 **code=1**（不是易支付 V2 的 0），返回 payurl 收银台直链
//   - 回调 **GET** query string，成功标识 **trade_status=TRADE_SUCCESS**（官方文档写的 status=paid 是错的），
//     带 MD5 sign（官方文档漏写），必须回纯文本 success；**平台不重试** → 对账轮询是硬需求
//   - 查单 GET /api/pay/result?trade_no=：匿名可读，status **0=待付 / 1=已付 / 2=已失效**；
//     ⚠️ 不能改用 api.php?act=order——它对失效单仍返回 status=0，无法区分"等待付款"与"已失效"
//   - 平台靠「通道 + 金额」匹配到账 → 同金额并存会错配（A 的钱记到 B），
//     且平台**没有取消订单接口** → 必须用金额互斥锁保证同金额同时只有一笔活跃订单
package httpapi

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// 支付通道标识（写入 payments.provider）
const (
	payProviderEpay     = "epay"
	payProviderXiaofeng = "xiaofeng"
)

// 熔断参数：连续失败达阈值即打开熔断，指数退避半开恢复
const (
	payCircuitFailThreshold = 3    // 连续失败次数阈值
	payCircuitBaseOpenSecs  = 600  // 首次熔断时长（10 分钟）
	payCircuitMaxOpenSecs   = 3600 // 熔断时长上限（1 小时）
)

// 自挂平台订单有效期（实测 5 分钟：收银台倒计时「失效勿付」+ expire_at）
const xiaofengOrderWindowSecs = 5 * 60

// ---------------------------------------------------------------------------
// 通用签名（易支付 V1 与自挂**口径完全一致**，实测逐字节验证通过）
// ---------------------------------------------------------------------------

// payMD5Sign 参数名 ASCII 升序拼 k=v&（跳过 sign/sign_type/空值）→ 末尾**直接追加**密钥 → MD5 小写。
// 注意是直接追加（不是 &key=），加分隔符会验签失败（已实测）。
func payMD5Sign(key string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+params[k])
	}
	sum := md5.Sum([]byte(strings.Join(pairs, "&") + key))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// 配置与就绪判定
// ---------------------------------------------------------------------------

// xiaofengType 支付类型（实测微信为 wxpay；配置为空时回落 wxpay）
func (a *App) xiaofengType() string {
	if t := strings.TrimSpace(a.Cfg.Pay.Xiaofeng.Type); t != "" {
		return t
	}
	return "wxpay"
}

// xiaofengNotifyBase / xiaofengReturnBase：空则回落 [epay] 的同名配置（同一个站点域名）
func (a *App) xiaofengNotifyBase() string {
	if v := strings.TrimSpace(a.Cfg.Pay.Xiaofeng.NotifyBase); v != "" {
		return strings.TrimRight(v, "/")
	}
	return strings.TrimRight(a.Cfg.EPay.NotifyBase, "/")
}

func (a *App) xiaofengReturnBase() string {
	if v := strings.TrimSpace(a.Cfg.Pay.Xiaofeng.ReturnBase); v != "" {
		return strings.TrimRight(v, "/")
	}
	return strings.TrimRight(a.Cfg.EPay.ReturnBase, "/")
}

// xiaofengConfigured 自挂通道是否已配置齐全（网关 + PID + 密钥）
func (a *App) xiaofengConfigured() bool {
	xf := a.Cfg.Pay.Xiaofeng
	return xf.Gateway != "" && xf.PID != "" && xf.Key != ""
}

// ---------------------------------------------------------------------------
// 渠道路由（含熔断降级）
// ---------------------------------------------------------------------------

// wxProvider 微信支付当前应走哪个通道：
// 主通道配的是自挂且未熔断 → 自挂；否则一律回落易支付（**用户无感**，照常付款）。
// 这样即使自挂平台故障 / 监控端掉线 / VIP 到期，微信支付也不会断。
func (a *App) wxProvider() string {
	if !strings.EqualFold(strings.TrimSpace(a.Cfg.Pay.Provider), payProviderXiaofeng) {
		return payProviderEpay
	}
	if !a.xiaofengConfigured() {
		return payProviderEpay
	}
	if a.payProviderOpen(payProviderXiaofeng) {
		return payProviderEpay // 熔断中 → 自动降级
	}
	return payProviderXiaofeng
}

// providerForChannel 渠道路由：微信走主通道（可能降级），支付宝恒走易支付。
func (a *App) providerForChannel(channel string) string {
	if channel == "wxpay" {
		return a.wxProvider()
	}
	return payProviderEpay
}

// providerConfigured 该通道是否可用（下单前校验，避免"点了去支付没反应"）
func (a *App) providerConfigured(provider string) bool {
	if provider == payProviderXiaofeng {
		return a.xiaofengConfigured()
	}
	return a.epayConfigured()
}

// ---------------------------------------------------------------------------
// 熔断（全自动，零人工）
// ---------------------------------------------------------------------------

// payProviderOpen 通道是否处于熔断中（true = 不接新单，走备用通道）。
// 到 until_ts 后自动"半开"放行探测：成功即清状态，再次失败则退避时长翻倍。
func (a *App) payProviderOpen(provider string) bool {
	var state string
	var until int64
	if err := a.DB.QueryRow("SELECT state, until_ts FROM pay_provider_state WHERE provider=?", provider).
		Scan(&state, &until); err != nil {
		return false // 无记录 = 正常
	}
	return state == "open" && time.Now().Unix() < until
}

// payProviderFail 记一次失败；连续失败达阈值则打开熔断（指数退避）。
func (a *App) payProviderFail(provider, signal string) {
	now := time.Now().Unix()
	var fails int
	_ = a.DB.QueryRow("SELECT fail_count FROM pay_provider_state WHERE provider=?", provider).Scan(&fails)
	fails++
	state, until := "ok", int64(0)
	if fails >= payCircuitFailThreshold {
		state = "open"
		shift := fails - payCircuitFailThreshold
		if shift > 3 {
			shift = 3 // 10min → 20 → 40 → 80(封顶 60)
		}
		open := int64(payCircuitBaseOpenSecs) << uint(shift)
		if open > payCircuitMaxOpenSecs {
			open = payCircuitMaxOpenSecs
		}
		until = now + open
	}
	_, err := a.DB.Exec(`INSERT INTO pay_provider_state (provider, state, until_ts, fail_count, ok_count, last_signal, updated_ts)
		VALUES (?,?,?,?,0,?,?)
		ON CONFLICT(provider) DO UPDATE SET state=excluded.state, until_ts=excluded.until_ts,
			fail_count=excluded.fail_count, last_signal=excluded.last_signal, updated_ts=excluded.updated_ts`,
		provider, state, until, fails, signal, now)
	if err != nil {
		return
	}
	if state == "open" {
		log.Printf("[pay] 通道 %s 熔断开启：连续失败 %d 次（信号=%s），%d 秒后半开自动探测",
			provider, fails, signal, until-now)
		a.auditAppend("pay_provider_open", 0, fmt.Sprintf("provider=%s fails=%d signal=%s", provider, fails, signal), "")
	}
}

// payProviderOK 记一次成功：清熔断、重置失败计数。
func (a *App) payProviderOK(provider string) {
	_, err := a.DB.Exec(`INSERT INTO pay_provider_state (provider, state, until_ts, fail_count, ok_count, last_signal, updated_ts)
		VALUES (?, 'ok', 0, 0, 1, '', ?)
		ON CONFLICT(provider) DO UPDATE SET state='ok', until_ts=0, fail_count=0,
			ok_count=ok_count+1, updated_ts=excluded.updated_ts`,
		provider, time.Now().Unix())
	if err != nil {
		log.Printf("[pay] 熔断状态写入失败 provider=%s: %v", provider, err)
	}
}

// ---------------------------------------------------------------------------
// 金额互斥锁（仅自挂通道需要）
// ---------------------------------------------------------------------------

// payLockAcquire 尝试占用该金额。成功返回 true。
//
// 为什么需要（实测依据）：自挂平台靠「通道 + 金额」匹配到账，同金额多笔 pending 并存会错配；
// 且平台**没有取消订单接口**（act=cancel/close/expire/void 全不支持），订单一旦创建就必然
// 占用该金额满 5 分钟 → 唯一安全做法是让同金额在任意时刻只有一笔活跃订单。
// 用 amount_micro 主键 + INSERT OR IGNORE 天然实现互斥，无需显式锁。
// releaseTs 传平台订单失效时刻：到期自动失效（不必依赖显式释放，进程重启也不丢）。
func (a *App) payLockAcquire(amountMicro int64, outTradeNo string, releaseTs int64) bool {
	now := time.Now().Unix()
	// 先清掉已过期的占用（release_ts 已过 = 平台侧订单也已失效）
	if _, err := a.DB.Exec("DELETE FROM pay_amount_locks WHERE amount_micro=? AND release_ts<=?", amountMicro, now); err != nil {
		log.Printf("[pay] 金额锁清理失败 amount=%d: %v", amountMicro, err)
		return false
	}
	res, err := a.DB.Exec(`INSERT OR IGNORE INTO pay_amount_locks (amount_micro, out_trade_no, acquired_ts, release_ts)
		VALUES (?,?,?,?)`, amountMicro, outTradeNo, now, releaseTs)
	if err != nil {
		log.Printf("[pay] 金额锁占用失败 amount=%d: %v", amountMicro, err)
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// payLockRelease 释放金额锁（只释放自己持有的那把，避免误放他人）
func (a *App) payLockRelease(amountMicro int64, outTradeNo string) {
	if _, err := a.DB.Exec("DELETE FROM pay_amount_locks WHERE amount_micro=? AND out_trade_no=?", amountMicro, outTradeNo); err != nil {
		log.Printf("[pay] 金额锁释放失败 amount=%d no=%s: %v", amountMicro, outTradeNo, err)
	}
}

// payLockWaitSecs 该金额被占用时还需等待多少秒（0 = 空闲，可立即下单）。
// 用于「条件排队」：常态下同金额无冲突 → 立即下单，用户零感知。
func (a *App) payLockWaitSecs(amountMicro int64) int64 {
	var release int64
	if err := a.DB.QueryRow("SELECT release_ts FROM pay_amount_locks WHERE amount_micro=?", amountMicro).Scan(&release); err != nil {
		return 0
	}
	if wait := release - time.Now().Unix(); wait > 0 {
		return wait
	}
	return 0
}

// ---------------------------------------------------------------------------
// 下单 / 查单 / 验签
// ---------------------------------------------------------------------------

// xiaofengCreate 向自挂平台下单（POST mapi.php），返回收银台直链与平台单号。
// 必须带超时：下单在用户可感知路径上，卡住会表现为"点了去支付没反应"。
func (a *App) xiaofengCreate(outTradeNo string, amountMicro int64, product string) (string, string, error) {
	xf := a.Cfg.Pay.Xiaofeng
	name := "余额充值"
	if product == "pool" {
		name = "众筹池充值"
	} else if product == "wallet2" {
		name = "折扣钱包充值" // 20260924：2 号折扣钱包独立充值
	}
	params := map[string]string{
		"pid":          xf.PID,
		"type":         a.xiaofengType(),
		"out_trade_no": outTradeNo,
		"name":         name,
		"money":        fmt.Sprintf("%.2f", float64(amountMicro)/1_000_000),
		"notify_url":   a.xiaofengNotifyBase() + "/v1/pay/notify",
		"return_url":   a.xiaofengReturnBase() + "/pay/return",
	}
	params["sign"] = payMD5Sign(xf.Key, params)
	params["sign_type"] = "MD5"

	v := url.Values{}
	for k, val := range params {
		v.Set(k, val)
	}
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.PostForm(strings.TrimRight(xf.Gateway, "/")+"/mapi.php", v)
	if err != nil {
		return "", "", fmt.Errorf("自挂下单请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", "", fmt.Errorf("自挂下单读取响应失败: %w", err)
	}
	var j struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		TradeNo string `json:"trade_no"`
		PayURL  string `json:"payurl"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return "", "", fmt.Errorf("自挂下单响应异常: %w", err)
	}
	// 实测成功码是 1（不是易支付 V2 的 0）
	if j.Code != 1 || j.TradeNo == "" || j.PayURL == "" {
		return "", "", fmt.Errorf("自挂下单失败 code=%d msg=%s", j.Code, j.Msg)
	}
	return j.PayURL, j.TradeNo, nil
}

// xiaofengOrderStatus 查订单状态：0=待支付 / 1=已支付 / 2=已失效。
// 用 /api/pay/result（匿名可读）。⚠️ 不能改用 api.php?act=order：它对失效单仍返回 status=0。
func (a *App) xiaofengOrderStatus(tradeNo string) (int, error) {
	if tradeNo == "" {
		return 0, fmt.Errorf("缺少平台单号")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(strings.TrimRight(a.Cfg.Pay.Xiaofeng.Gateway, "/") +
		"/api/pay/result?trade_no=" + url.QueryEscape(tradeNo))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return 0, err
	}
	var j struct {
		Code int `json:"code"`
		Data struct {
			Status int `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return 0, err
	}
	if j.Code != 0 {
		return 0, fmt.Errorf("自挂查单失败 code=%d", j.Code)
	}
	return j.Data.Status, nil
}

// xiaofengQueryAndSettle 自挂通道漏单自愈（**硬需求**：平台回调只发一次、实测 6 分钟零重试）。
//   - status=1（已支付）→ 金额以本地订单为准，入账并释放金额锁
//   - status=2（已失效）→ **只释放金额锁**，本地 status 保持 pending：
//     用户"迟付"时平台仍会回调，那时必须照常入账（设计规则：过期单收到到账仍归其持有人）
//   - 其他（0 待付 / 网络异常）→ 不作结论
func (a *App) xiaofengQueryAndSettle(outTradeNo string, amountMicro int64) bool {
	var tradeNo, status string
	if err := a.DB.QueryRow("SELECT COALESCE(trade_no,''), status FROM payments WHERE out_trade_no=?", outTradeNo).
		Scan(&tradeNo, &status); err != nil {
		return false
	}
	if status != "pending" {
		return false
	}
	st, err := a.xiaofengOrderStatus(tradeNo)
	if err != nil {
		return false
	}
	switch st {
	case 1:
		a.epaySettle(outTradeNo, tradeNo)
		a.payLockRelease(amountMicro, outTradeNo)
		a.payProviderOK(payProviderXiaofeng)
		return true
	case 2:
		a.payLockRelease(amountMicro, outTradeNo)
	}
	return false
}

// xiaofengVerifyNotify 自挂回调验签（与易支付 V1 同口径，密钥取自挂的）
func (a *App) xiaofengVerifyNotify(form url.Values) bool {
	xf := a.Cfg.Pay.Xiaofeng
	if xf.Key == "" || form.Get("pid") != xf.PID {
		return false
	}
	got := form.Get("sign")
	if got == "" {
		return false
	}
	params := map[string]string{}
	for k := range form {
		params[k] = form.Get(k)
	}
	return payMD5Sign(xf.Key, params) == got
}

// xiaofengNotifyPaid 回调是否表示支付成功。
// 实测口径是**易支付 V1 的 trade_status=TRADE_SUCCESS**，不是官方文档写的 status=paid。
func (a *App) xiaofengNotifyPaid(form url.Values) bool {
	return form.Get("trade_status") == "TRADE_SUCCESS"
}
