// 易支付 V2（RSA）回归：签名原文规范 / 签名验签往返 / 篡改必失败 / 版本分流语义。
// 签名口径错会直接导致下单失败或回调被拒（收不到钱），故逐条锁死。
// 测试用运行时生成的临时密钥对，仓库内不落任何真实商户密钥。
package httpapi

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// v2TestKeys 生成临时 RSA 密钥对并写入配置（返回清理无需处理，app 为测试局部变量）
func v2TestKeys(t *testing.T, app *App) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试密钥失败: %v", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("序列化私钥失败: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("序列化公钥失败: %v", err)
	}
	app.Cfg.EPay.APIVersion = "v2"
	app.Cfg.EPay.Gateway = "https://epay.test"
	app.Cfg.EPay.PID = "1000"
	app.Cfg.EPay.PrivateKey = base64.StdEncoding.EncodeToString(privDER)
	app.Cfg.EPay.PlatformPub = base64.StdEncoding.EncodeToString(pubDER)
}

// 签名原文：参数名 ASCII 升序、k=v 以 & 连接，跳过 sign / sign_type / 空值
func TestEpaySignString_Spec(t *testing.T) {
	got := epaySignString(map[string]string{
		"pid": "1000", "money": "0.01", "type": "wxpay",
		"sign": "IGNORED", "sign_type": "RSA", "empty": "",
	})
	want := "money=0.01&pid=1000&type=wxpay"
	if got != want {
		t.Fatalf("签名原文不符：got %q want %q", got, want)
	}
}

// 私钥签名 → 公钥验签必须通过；任一参数被篡改必须失败
func TestEpayV2SignVerify_RoundTripAndTamper(t *testing.T) {
	app, _, _ := newTestApp(t)
	v2TestKeys(t, app)

	params := map[string]string{
		"pid": "1000", "type": "wxpay", "out_trade_no": "T1",
		"money": "0.01", "timestamp": "1790000000",
	}
	sig, err := app.epayV2Sign(params)
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	params["sign"] = sig
	if !app.epayV2Verify(params) {
		t.Fatal("自签自验应通过")
	}

	// 篡改金额 → 必须失败（防回调金额被改）
	tampered := map[string]string{}
	for k, v := range params {
		tampered[k] = v
	}
	tampered["money"] = "999.00"
	if app.epayV2Verify(tampered) {
		t.Fatal("金额被篡改后验签必须失败")
	}
	// 去掉签名 → 必须失败
	delete(tampered, "sign")
	if app.epayV2Verify(tampered) {
		t.Fatal("缺 sign 必须验签失败")
	}
}

// 版本分流：V2 需私钥+平台公钥；成功态取 status=1（V1 是 trade_status）
func TestEpayVersionBranching(t *testing.T) {
	app, _, _ := newTestApp(t)
	v2TestKeys(t, app)
	if !app.epayV2On() {
		t.Fatal("api_version=v2 应启用 V2")
	}
	if !app.epayConfigured() {
		t.Fatal("V2 已配私钥+平台公钥应视为已配置")
	}

	// V2：status=1 才算支付成功
	if !app.epayNotifyPaid(url.Values{"status": {"1"}}) {
		t.Fatal("V2 status=1 应判定为已支付")
	}
	if app.epayNotifyPaid(url.Values{"status": {"0"}}) {
		t.Fatal("V2 status=0 不应判定为已支付")
	}

	// V1：trade_status=TRADE_SUCCESS 才算成功，且需要 MD5 密钥才算已配置
	app.Cfg.EPay.APIVersion = "v1"
	app.Cfg.EPay.Key = ""
	if app.epayConfigured() {
		t.Fatal("V1 未配 MD5 密钥不应视为已配置")
	}
	app.Cfg.EPay.Key = "md5key"
	if !app.epayConfigured() {
		t.Fatal("V1 配了 MD5 密钥应视为已配置")
	}
	if !app.epayNotifyPaid(url.Values{"trade_status": {"TRADE_SUCCESS"}}) {
		t.Fatal("V1 TRADE_SUCCESS 应判定为已支付")
	}
	if app.epayNotifyPaid(url.Values{"status": {"1"}}) {
		t.Fatal("V1 不应认 status=1（口径不同）")
	}
}

// 支付跳转口径（20260922 站长定稿：本站不做站内扫码弹窗 / iframe 嵌收银台，
// 一律正常跳转 + 正常回调）：
//   jump   → pay_url 直接用平台给的收银台 URL；
//   qrcode → pay_url 换成平台收银台页 /pay/submit/{trade_no}/（weixin:// 这类 scheme
//            桌面浏览器直接跳转无意义）；
// 且响应里**不得**再出现 qr_png（站内扫码已下线）。
func TestPayCreate_RedirectOnly(t *testing.T) {
	const jumpURL = "https://cashier.example/cashier/abc"
	for _, tc := range []struct {
		name    string
		payType string
		payInfo string
		want    string // 期望 pay_url（qrcode 分支需拼接测试网关地址，故留后缀比较）
	}{
		{"jump 直用平台收银台", "jump", jumpURL, jumpURL},
		{"qrcode 改走平台收银台页", "qrcode", "weixin://wxpay/bizpayurl?pr=XYZ", "/pay/submit/PT123/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _, _ := newTestApp(t)
			_, tok := seedPoolUser(t, app, 0)

			gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"code":0,"trade_no":"PT123","pay_type":%q,"pay_info":%q}`, tc.payType, tc.payInfo)
			}))
			defer gw.Close()
			v2TestKeys(t, app)
			app.Cfg.EPay.Gateway = gw.URL

			rec, out := doJSON(t, app.Routes(), "POST", "/v1/pay/create", tok, map[string]any{
				"amount_micro": 1_000_000, "channel": "wxpay", "product": "balance",
			})
			if rec.Code != 200 {
				t.Fatalf("下单应 200，得 %d：%v", rec.Code, out)
			}
			if _, bad := out["qr_png"]; bad {
				t.Fatal("不应再返回 qr_png（站内扫码已下线）")
			}
			want := tc.want
			if tc.payType == "qrcode" {
				want = gw.URL + tc.want
			}
			if got, _ := out["pay_url"].(string); got != want {
				t.Fatalf("pay_url 应为 %q，得 %q", want, got)
			}
		})
	}
}
