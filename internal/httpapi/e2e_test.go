// 冒烟测试：模拟上游 + 全链路验证（注册→播种→计费→402→脱敏→CORS→反代）
package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"acu-aqua/gateway/internal/config"
	"acu-aqua/gateway/internal/db"
	"acu-aqua/gateway/internal/seeding"
)

// newTestApp 构造测试 App：内存上游 + 临时 DB
func newTestApp(t *testing.T) (*App, *httptest.Server, string) {
	t.Helper()
	tmp := t.TempDir()

	// 模拟上游：非流式 chat 返回 usage；流式返回 SSE（末帧含泄露字段验证剥层）
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" && r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(404)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if bytes.Contains(b, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"h\"}}],\"usage\":null,\"trace_id\":\"LEAK\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":74,\"completion_tokens\":178,\"prompt_tokens_details\":{\"cached_tokens\":0}},\"cost_cny\":0.007,\"billing_pending\":false,\"trace_id\":\"LEAK\"}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":74,"completion_tokens":178,"prompt_tokens_details":{"cached_tokens":0}},"cost_cny":0.007,"trace_id":"LEAK"}`))
	}))

	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Lines = []config.Line{{
		ID: "t", Name: "测试线", Mode: "per_token", BaseURL: up.URL, Keys: []string{"sk-upstream-1"},
		VipNum: 9, VipDen: 10, KeyFaceMicro: 67_980_000,
		Models: []config.Model{{
			SiteID: "m1", UpstreamID: "vendor-m1",
			InCostRate10: 120_000, CacheCostRate10: 15_000, OutCostRate10: 360_000,
			InSellRate10: 60_000, CacheSellRate10: 7_500, OutSellRate10: 180_000,
		}},
	}, {
		// 按次线：回归 2026-09-11 计费事故（per_call 带 usage 曾漏扣为 0）
		ID: "c", Name: "按次线", Mode: "per_call", BaseURL: up.URL, Keys: []string{"sk-upstream-1"},
		VipNum: 9, VipDen: 10, KeyFaceMicro: 67_980_000,
		Models: []config.Model{{
			SiteID: "pc1", UpstreamID: "vendor-pc1", PerCallSell: 2000,
		}},
	}}

	d, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.InitTables(d); err != nil {
		t.Fatal(err)
	}
	if err := seeding.SeedAll(d, cfg); err != nil {
		t.Fatal(err)
	}
	app := New(cfg, &db.DBx{DB: d})
	return app, up, tmp
}

func doJSON(t *testing.T, h http.Handler, method, path, token string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestFullFlow(t *testing.T) {
	app, _, _ := newTestApp(t)
	h := app.Routes()

	// 1. 注册
	rec, out := doJSON(t, h, "POST", "/v1/user/register", "", map[string]string{
		"username": "tester", "email": "t@t.dev", "password": "password123"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)

	// 2. 发余额（直接 DB 模拟充值）
	if _, err := app.DB.Exec("UPDATE users SET balance_micro=100000 WHERE email='t@t.dev'"); err != nil {
		t.Fatal(err)
	}

	// 3. 模型列表：价格已播种（normal in 6 元/M）
	rec, out = doJSON(t, h, "GET", "/v1/models", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("models: %d", rec.Code)
	}
	data := out["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("期望 2 个模型，得 %d", len(data))
	}
	m := data[0].(map[string]any)
	if m["id"] != "t/m1" || m["in_price"] != 6.0 {
		t.Fatalf("模型信息错误: %v", m)
	}

	// 4. chat 调用：74 in + 178 out，normal 价 = 74*60000/10000 + 178*180000/10000 = 444+3204 = 3648
	rec, out = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "t/m1", "messages": []map[string]string{{"role": "user", "content": "hi"}}, "max_tokens": 500})
	if rec.Code != 200 {
		t.Fatalf("chat: %d %v", rec.Code, out)
	}
	// 脱敏：无 cost_cny / trace_id
	s := rec.Body.String()
	if bytes.Contains([]byte(s), []byte("cost_cny")) || bytes.Contains([]byte(s), []byte("LEAK")) {
		t.Fatalf("信息隔离失败：泄露上游字段 %s", s)
	}

	// 5. 余额核对：100000 - 3648 = 96352
	var bal int64
	_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE email='t@t.dev'").Scan(&bal)
	if bal != 96352 {
		t.Fatalf("余额错误：期望 96352，得 %d", bal)
	}

	// 6. 面值台账：face = round(74*12) + round(178*36) = 888 + 6408 = 7296（rate10 口径：in 120000/out 360000 每万token）
	var used int64
	_ = app.DB.QueryRow("SELECT used_micro FROM line_keys WHERE line_id='t' AND idx=0").Scan(&used)
	if used != 7296 {
		t.Fatalf("面值台账错误：期望 7296，得 %d", used)
	}

	// 7. 请求行核对
	var amount int64
	var state string
	_ = app.DB.QueryRow("SELECT bill_amount_micro, bill_state FROM requests WHERE ok=1").Scan(&amount, &state)
	if amount != 3648 || state != "billed" {
		t.Fatalf("请求账错误：amount=%d state=%s", amount, state)
	}

	// 8. vip 计费：设 price_grp_token=vip 后 74*54000/10000+178*162000/10000 = 399+2883 = 3282
	_, _ = app.DB.Exec("UPDATE users SET price_grp_token='vip' WHERE email='t@t.dev'")
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "t/m1", "messages": []map[string]string{{"role": "user", "content": "hi"}}, "max_tokens": 500})
	if rec.Code != 200 {
		t.Fatalf("vip chat: %d", rec.Code)
	}
	_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE email='t@t.dev'").Scan(&bal)
	// 96352 - 3282 = 93070
	if bal != 93070 {
		t.Fatalf("vip 余额错误：期望 93070，得 %d", bal)
	}

	// 9. 零余额 429 insufficient_quota（OpenAI 国际标准配额错误）
	_, _ = app.DB.Exec("UPDATE users SET balance_micro=0 WHERE email='t@t.dev'")
	rec, out = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "t/m1", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec.Code != 429 {
		t.Fatalf("期望 429 insufficient_quota，得 %d %v", rec.Code, out)
	}
	if e, _ := out["error"].(map[string]any); e == nil || e["type"] != "insufficient_quota" || e["code"] != "insufficient_quota" {
		t.Fatalf("错误格式非 OpenAI 标准: %v", out)
	}
}

// TestStreamFlow 流式链路：SSE 转发 + 敏感字段剥层 + usage 计费
func TestStreamFlow(t *testing.T) {
	app, _, _ := newTestApp(t)
	h := app.Routes()

	rec, out := doJSON(t, h, "POST", "/v1/user/register", "", map[string]string{
		"username": "streamer", "email": "s@t.dev", "password": "password123"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)
	_, _ = app.DB.Exec("UPDATE users SET balance_micro=100000 WHERE email='s@t.dev'")

	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "t/m1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 500, "stream": true})
	if rec.Code != 200 {
		t.Fatalf("流式 chat: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// 脱敏：流帧里不得出现成本/计费/追踪字段
	if strings.Contains(body, "cost_cny") || strings.Contains(body, "trace_id") || strings.Contains(body, "LEAK") || strings.Contains(body, "billing_pending") {
		t.Fatalf("流式信息隔离失败: %s", body)
	}
	if !strings.Contains(body, "prompt_tokens") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("流式内容缺失: %s", body)
	}
	// 计费核对：74 in + 178 out = 3648；余额 100000-3648=96352
	var bal int64
	_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE email='s@t.dev'").Scan(&bal)
	if bal != 96352 {
		t.Fatalf("流式计费错误：期望 96352，得 %d", bal)
	}
}

// TestPerCallBilling 按次计费回归：带 usage 的成功请求必须收单价（事故时曾漏扣为 0）
func TestPerCallBilling(t *testing.T) {
	app, _, _ := newTestApp(t)
	h := app.Routes()

	rec, out := doJSON(t, h, "POST", "/v1/user/register", "", map[string]string{
		"username": "percaller", "email": "p@t.dev", "password": "password123"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)
	_, _ = app.DB.Exec("UPDATE users SET balance_micro=100000 WHERE email='p@t.dev'")

	// 1. 非流式：上游返回 usage(74/178)，但 per_call 必须收单价 2000（不是 0、不是 token 精算值）
	rec, out = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}}, "max_tokens": 500})
	if rec.Code != 200 {
		t.Fatalf("per_call 非流式: %d %v", rec.Code, out)
	}
	var bal int64
	_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE email='p@t.dev'").Scan(&bal)
	if bal != 98_000 {
		t.Fatalf("per_call 非流式漏扣：期望 98000（100000-2000），得 %d", bal)
	}
	var amount int64
	var state string
	_ = app.DB.QueryRow("SELECT bill_amount_micro, bill_state FROM requests WHERE ok=1 AND model='c/pc1'").Scan(&amount, &state)
	if amount != 2000 || state != "billed" {
		t.Fatalf("per_call 请求账错误：amount=%d state=%s（事故症状：amount=0 state=refunded）", amount, state)
	}

	// 2. 流式：同样必须收单价 2000
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 500, "stream": true})
	if rec.Code != 200 {
		t.Fatalf("per_call 流式: %d %s", rec.Code, rec.Body.String())
	}
	_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE email='p@t.dev'").Scan(&bal)
	if bal != 96_000 {
		t.Fatalf("per_call 流式漏扣：期望 96000，得 %d", bal)
	}

	// 3. VIP 按次组：2000×9/10=1800
	_, _ = app.DB.Exec("UPDATE users SET price_grp_call='vip' WHERE email='p@t.dev'")
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec.Code != 200 {
		t.Fatalf("per_call vip: %d", rec.Code)
	}
	_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE email='p@t.dev'").Scan(&bal)
	if bal != 94_200 {
		t.Fatalf("per_call vip 计费错误：期望 94200（96000-1800），得 %d", bal)
	}
}

// TestLegacyProxy 绞杀者模式：非收费模型与未注册路径反代旧网关
func TestLegacyProxy(t *testing.T) {
	app, up, _ := newTestApp(t)
	app.Cfg.Server.LegacyUpstreamURL = up.URL
	h := app.Routes()

	// 非收费模型（无 line-id/ 前缀）→ 整请求转发旧网关（免鉴权：免费线由旧网关自管）
	rec, out := doJSON(t, h, "POST", "/v1/chat/completions", "sk-legacy-free-key", map[string]any{
		"model": "some-free-model", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec.Code != 200 {
		t.Fatalf("免费模型应反代成功: %d %v", rec.Code, out)
	}
	if out["id"] != "x" {
		t.Fatalf("应为旧网关响应: %v", out)
	}

	// catch-all：Go 未注册路径 → 旧网关（mock 对未知路径回 404，透传状态码）
	req := httptest.NewRequest("GET", "/v1/usage", nil)
	req.Header.Set("Authorization", "Bearer sk-legacy-free-key")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != 404 {
		t.Fatalf("catch-all 应透传旧网关 404: %d", rec2.Code)
	}

	// 收费模型仍走 Go 原生计费链路（带线前缀不被反代）
	rec3, _ := doJSON(t, h, "POST", "/v1/chat/completions", "", map[string]any{
		"model": "t/m1", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec3.Code != 401 {
		t.Fatalf("收费模型仍需 Go 鉴权（401）: %d", rec3.Code)
	}
}

// TestCORSAndAdminProxy CORS 注入（原生路由）与 admin 整体反代（旧网关承接）
func TestCORSAndAdminProxy(t *testing.T) {
	app, up, _ := newTestApp(t)
	app.Cfg.Server.LegacyUpstreamURL = up.URL
	h := app.Routes()

	// 原生路由（Go 处理）必须带 CORS 头——SPA 跨域（acu.ltzy.top → api.ltzy.top）依赖
	req := httptest.NewRequest("GET", "/v1/meta", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("meta: %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("原生路由缺 CORS 头: %v", rec.Header())
	}

	// 收费 chat（原生）也必须带 CORS 头（未登录 401 同样要带，否则浏览器拦错误响应）
	req = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"t/m1"}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("chat 原生路由缺 CORS 头: %v", rec.Header())
	}

	// admin 路径不再由 Go 承接 → 反代旧网关（mock 对未知路径回 404，证明未落 Go 原生处理）
	req = httptest.NewRequest("POST", "/v1/admin/login", strings.NewReader(`{"password":"x"}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("admin/login 应反代旧网关（mock 404），得 %d %s", rec.Code, rec.Body.String())
	}

	// 反代路径不注入 CORS（旧网关自带，避免重复头）；mock 未回 CORS → 此处应为空
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("反代响应不应由 Go 注入 CORS: %v", rec.Header())
	}
}
