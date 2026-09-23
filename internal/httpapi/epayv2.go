// 易支付 V2（RSA 版）对接：下单 /api/pay/create、查单 /api/pay/query、回调验签。
//
// 与 V1（MD5 + mapi.php/api.php）并存，由配置 [epay] api_version 切换（默认 v1）。
// 实测口径（20260921 对生产网关 xnoo.cn 逐项探测确认）：
//   - 签名：参数名 ASCII 升序拼 "k=v&"（跳过 sign/sign_type/空值），
//     用**商户私钥**做 RSA-SHA256 后 base64 —— 下单/查单请求用；
//     用**平台公钥**验签 —— 平台响应与异步回调用（已实测响应验签通过）。
//   - 必须带 timestamp（秒级）；缺它直接 -3 "时间戳(timestamp)字段不能为空"。
//   - sign_type 不传会回落 MD5（报 "MD5签名校验失败"），故必须显式传 RSA。
//   - **成功码是 code=0**（V1 是 code=1），注意区分。
//   - 查单只认 trade_no（平台单号）；传 out_trade_no 会 500。
//   - pay_type：qrcode → pay_info 是二维码内容（如 weixin://wxpay/bizpayurl?pr=…）；
//     jump → pay_info 是收银台 URL（支付宝/QQ 钱包均为 jump）。
//
// 20260922 站长定稿：本站**不做站内扫码**（既不用后端渲染二维码，也不 iframe 嵌收银台），
// 一律由前端正常跳转到平台收银台，支付完成靠平台回调 + return_url 回跳（见 myapi.go payCreate）。
package httpapi

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RSA 密钥解析缓存：DER 解析有开销，按配置原文做键缓存（配置变更自动失效）
var (
	epayKeyMu     sync.Mutex
	epayPrivCache *rsa.PrivateKey
	epayPrivSrc   string
	epayPubCache  *rsa.PublicKey
	epayPubSrc    string
)

// decodePEMBase64 接受「裸 base64（可含换行）」或「完整 PEM」，统一转 PEM 块
func decodePEMBase64(s, label string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("未配置 %s", label)
	}
	if strings.Contains(s, "-----BEGIN") {
		block, _ := pem.Decode([]byte(s))
		if block == nil {
			return nil, fmt.Errorf("%s PEM 解析失败", label)
		}
		return block.Bytes, nil
	}
	compact := strings.Join(strings.Fields(s), "")
	der, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("%s base64 解码失败: %w", label, err)
	}
	return der, nil
}

// epayPrivKey 商户私钥（下单/查单签名用）
func (a *App) epayPrivKey() (*rsa.PrivateKey, error) {
	src := a.Cfg.EPay.PrivateKey
	epayKeyMu.Lock()
	defer epayKeyMu.Unlock()
	if epayPrivCache != nil && epayPrivSrc == src {
		return epayPrivCache, nil
	}
	der, err := decodePEMBase64(src, "商户私钥")
	if err != nil {
		return nil, err
	}
	var key *rsa.PrivateKey
	if k, e := x509.ParsePKCS8PrivateKey(der); e == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("商户私钥不是 RSA 类型")
		}
		key = rk
	} else if rk, e2 := x509.ParsePKCS1PrivateKey(der); e2 == nil {
		key = rk
	} else {
		return nil, fmt.Errorf("商户私钥解析失败（支持 PKCS#8 / PKCS#1）")
	}
	epayPrivCache, epayPrivSrc = key, src
	return key, nil
}

// epayPlatPub 平台公钥（验平台响应与异步回调签名）
func (a *App) epayPlatPub() (*rsa.PublicKey, error) {
	src := a.Cfg.EPay.PlatformPub
	epayKeyMu.Lock()
	defer epayKeyMu.Unlock()
	if epayPubCache != nil && epayPubSrc == src {
		return epayPubCache, nil
	}
	der, err := decodePEMBase64(src, "平台公钥")
	if err != nil {
		return nil, err
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("平台公钥解析失败: %w", err)
	}
	rk, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("平台公钥不是 RSA 类型")
	}
	epayPubCache, epayPubSrc = rk, src
	return rk, nil
}

// epaySignString 易支付签名原文：参数名 ASCII 升序、k=v 以 & 连接，跳过 sign/sign_type 与空值
func epaySignString(params map[string]string) string {
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
	return strings.Join(pairs, "&")
}

// epayV2Sign 商户私钥 RSA-SHA256 签名（base64）
func (a *App) epayV2Sign(params map[string]string) (string, error) {
	key, err := a.epayPrivKey()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(epaySignString(params)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("RSA 签名失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// epayV2Verify 平台公钥验签（响应与回调同口径）
func (a *App) epayV2Verify(params map[string]string) bool {
	got := strings.TrimSpace(params["sign"])
	if got == "" {
		return false
	}
	pub, err := a.epayPlatPub()
	if err != nil {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(epaySignString(params)))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) == nil
}

// epayV2On 是否启用 V2（RSA）接口
func (a *App) epayV2On() bool {
	return strings.EqualFold(strings.TrimSpace(a.Cfg.EPay.APIVersion), "v2")
}

// epayConfigured 支付通道是否已配置（V1 需 MD5 密钥，V2 需商户私钥 + 平台公钥）
func (a *App) epayConfigured() bool {
	e := a.Cfg.EPay
	if e.Gateway == "" || e.PID == "" {
		return false
	}
	if a.epayV2On() {
		return e.PrivateKey != "" && e.PlatformPub != ""
	}
	return e.Key != ""
}

// epayV2Order V2 下单结果
type epayV2Order struct {
	TradeNo string // 平台单号（查单必需）
	PayType string // qrcode=pay_info 是二维码内容；jump=pay_info 是收银台 URL
	PayInfo string
}

// epayV2Create 向 V2 /api/pay/create 下单
func (a *App) epayV2Create(outTradeNo string, amountMicro int64, channel, product, clientIP string) (*epayV2Order, error) {
	name := "余额充值"
	if product == "pool" {
		name = "众筹池充值"
	} else if product == "wallet2" {
		name = "折扣钱包充值" // 20260924：2 号折扣钱包独立充值
	}
	params := map[string]string{
		"pid":          a.Cfg.EPay.PID,
		"type":         channel,
		"out_trade_no": outTradeNo,
		"notify_url":   a.Cfg.EPay.NotifyBase + "/v1/pay/notify",
		"return_url":   a.Cfg.EPay.ReturnBase + "/pay/return",
		"name":         name,
		"money":        fmt.Sprintf("%.2f", float64(amountMicro)/1_000_000),
		"clientip":     clientIP,
		"timestamp":    strconv.FormatInt(time.Now().Unix(), 10),
	}
	sign, err := a.epayV2Sign(params)
	if err != nil {
		return nil, err
	}
	params["sign"] = sign
	params["sign_type"] = "RSA"

	v := url.Values{}
	for k, val := range params {
		v.Set(k, val)
	}
	resp, err := http.PostForm(strings.TrimRight(a.Cfg.EPay.Gateway, "/")+"/api/pay/create", v)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	var j struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		TradeNo string `json:"trade_no"`
		PayType string `json:"pay_type"`
		PayInfo string `json:"pay_info"`
		Sign    string `json:"sign"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return nil, fmt.Errorf("V2 下单响应异常: %w", err)
	}
	// V2 成功码是 0（V1 为 1）
	if j.Code != 0 {
		return nil, fmt.Errorf("V2 下单失败 code=%d msg=%s", j.Code, j.Msg)
	}
	if j.TradeNo == "" || j.PayInfo == "" {
		return nil, fmt.Errorf("V2 下单未返回 trade_no/pay_info")
	}
	return &epayV2Order{TradeNo: j.TradeNo, PayType: j.PayType, PayInfo: j.PayInfo}, nil
}

// epayV2QueryResult V2 查单结果
type epayV2QueryResult struct {
	TradeNo string
	Status  int    // 1=已支付
	Money   string // 元
}

// epayV2Query 向 V2 /api/pay/query 查单（**必须传 trade_no**，传 out_trade_no 会 500）
func (a *App) epayV2Query(tradeNo string) (*epayV2QueryResult, error) {
	params := map[string]string{
		"pid":       a.Cfg.EPay.PID,
		"trade_no":  tradeNo,
		"timestamp": strconv.FormatInt(time.Now().Unix(), 10),
	}
	sign, err := a.epayV2Sign(params)
	if err != nil {
		return nil, err
	}
	params["sign"] = sign
	params["sign_type"] = "RSA"

	v := url.Values{}
	for k, val := range params {
		v.Set(k, val)
	}
	resp, err := http.PostForm(strings.TrimRight(a.Cfg.EPay.Gateway, "/")+"/api/pay/query", v)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	var j struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		TradeNo string `json:"trade_no"`
		Status  int    `json:"status"`
		Money   string `json:"money"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return nil, fmt.Errorf("V2 查单响应异常: %w", err)
	}
	if j.Code != 0 {
		return nil, fmt.Errorf("V2 查单失败 code=%d msg=%s", j.Code, j.Msg)
	}
	return &epayV2QueryResult{TradeNo: j.TradeNo, Status: j.Status, Money: j.Money}, nil
}

// formToMap url.Values → map[string]string（验签用）
func formToMap(form url.Values) map[string]string {
	m := make(map[string]string, len(form))
	for k := range form {
		m[k] = form.Get(k)
	}
	return m
}

// epayVerifyNotify 异步回调验签：V2 用平台公钥验 RSA 签名，V1 用 MD5 密钥
func (a *App) epayVerifyNotify(form url.Values) bool {
	if a.epayV2On() {
		return a.epayV2Verify(formToMap(form))
	}
	return a.epayVerifySign(form)
}

// epayNotifyPaid 回调是否表示「支付成功」：V2 用 status=1，V1 用 trade_status=TRADE_SUCCESS
func (a *App) epayNotifyPaid(form url.Values) bool {
	if a.epayV2On() {
		return form.Get("status") == "1"
	}
	return form.Get("trade_status") == "TRADE_SUCCESS"
}

// epayV2QueryAndSettle V2 漏单自愈：用平台单号查单，已支付且金额一致则补账。
// 返回 true 表示本轮已补账（供对账任务统计）。
func (a *App) epayV2QueryAndSettle(outTradeNo string, amountMicro int64) bool {
	var tradeNo string
	if err := a.DB.QueryRow("SELECT COALESCE(trade_no,'') FROM payments WHERE out_trade_no=?", outTradeNo).Scan(&tradeNo); err != nil || tradeNo == "" {
		return false // 下单未成功（无平台单号）→ 无从查起
	}
	r, err := a.epayV2Query(tradeNo)
	if err != nil || r.Status != 1 {
		return false
	}
	if remote := yuanToMicro(r.Money); remote != amountMicro {
		log.Printf("[pay] V2 查单金额不符 no=%s remote=%d local=%d", outTradeNo, remote, amountMicro)
		return false
	}
	a.epaySettle(outTradeNo, r.TradeNo)
	return true
}
