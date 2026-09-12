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
	"time"

	"acu-aqua/gateway/internal/auth"
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

	// 3. 模型列表：auto 置顶 + 价格已播种（normal in 6 元/M）
	rec, out = doJSON(t, h, "GET", "/v1/models", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("models: %d", rec.Code)
	}
	data := out["data"].([]any)
	if len(data) != 3 {
		t.Fatalf("期望 3 个条目（auto + 2 模型），得 %d", len(data))
	}
	if m := data[0].(map[string]any); m["id"] != "auto" || m["auto"] != true {
		t.Fatalf("auto 条目缺失: %v", m)
	}
	m := data[1].(map[string]any)
	if m["id"] != "t/m1" || m["in_price"] != 6.0 || m["paid"] != true {
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

	// admin/login 已由 Go 原生接管：测试环境未配置 AQUA_ADMIN_PASSWORD_HASH
	// → 503 admin_disabled（Rust 版同款语义）；CORS 头由 corsGate 统一注入
	req = httptest.NewRequest("POST", "/v1/admin/login", strings.NewReader(`{"password":"x"}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatalf("admin/login 应原生处理（未配密码 503），得 %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("admin 响应应带统一 CORS 头: %v", rec.Header())
	}
}

// TestOptionsPreflight P0 登录修复回归：OPTIONS 预检必须全局 204 + CORS 头。
// 曾因预检落 catch-all 404 且无 CORS 头，浏览器拦截全部跨域请求（登录页"网络错误"）。
// 预检走任意路径（含不存在的路径）都必须放行，不得进路由。
func TestOptionsPreflight(t *testing.T) {
	app, _, _ := newTestApp(t)
	h := app.Routes()

	for _, path := range []string{
		"/v1/auth/login", "/v1/my/keys", "/v1/pay/create",
		"/v1/not-exist-at-all", "/", "/v1/chat/completions",
	} {
		req := httptest.NewRequest("OPTIONS", path, nil)
		req.Header.Set("Origin", "https://acu.ltzy.top")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 204 {
			t.Fatalf("OPTIONS %s 应 204，得 %d", path, rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("OPTIONS %s 缺 Allow-Origin: %v", path, rec.Header())
		}
		if rec.Header().Get("Access-Control-Allow-Headers") == "" {
			t.Fatalf("OPTIONS %s 缺 Allow-Headers", path)
		}
	}
}

// TestAuthLoginFlow P0 登录修复回归：/v1/auth/login（前端 SPA 契约路径）。
// 1. 用户名登录与邮箱登录均可；2. 错误密码 401；3. /v1/auth/me 返回完整资料；
// 4. pbkdf2 老格式（Rust 全量 981 用户）密码可登录。
func TestAuthLoginFlow(t *testing.T) {
	app, _, _ := newTestApp(t)
	h := app.Routes()

	// 注册（Go 新格式 pbkdf2；验证码直接注入 DB，send-code 走 SMTP 不在用例范围）
	_, _ = app.DB.Exec(
		"INSERT INTO email_codes (email, purpose, code, fails, expire_ts) VALUES ('alice@t.dev','register','123456',0,?)",
		time.Now().Unix()+600)
	rec, out := doJSON(t, h, "POST", "/v1/auth/register", "", map[string]string{
		"email": "alice@t.dev", "code": "123456", "username": "alice", "password": "password123"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}

	// 注册响应：token + api_key（自动发默认密钥，Rust 版行为）+ user
	if out["api_key"] == nil || out["api_key"] == "" {
		t.Fatalf("注册响应缺 api_key: %v", out)
	}
	if out["user"] == nil {
		t.Fatalf("注册响应缺 user: %v", out)
	}

	// 用户名登录：token 必须 sess_ 前缀（Rust require_session 硬校验）
	rec, out = doJSON(t, h, "POST", "/v1/auth/login", "", map[string]string{
		"account": "alice", "password": "password123"})
	if rec.Code != 200 || out["token"] == nil || out["token"] == "" {
		t.Fatalf("用户名登录失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)
	if !strings.HasPrefix(tok, "sess_") {
		t.Fatalf("会话令牌必须 sess_ 前缀，得 %q", tok[:12])
	}
	if out["user"] == nil {
		t.Fatalf("登录响应缺 user 对象: %v", out)
	}

	// 邮箱登录
	rec, out = doJSON(t, h, "POST", "/v1/auth/login", "", map[string]string{
		"account": "alice@t.dev", "password": "password123"})
	if rec.Code != 200 || out["token"] == "" {
		t.Fatalf("邮箱登录失败: %d %v", rec.Code, out)
	}

	// 错误密码 → 401（不是 500/404，前端据此提示"账号或密码错误"）
	rec, out = doJSON(t, h, "POST", "/v1/auth/login", "", map[string]string{
		"account": "alice", "password": "wrong-password"})
	if rec.Code != 401 {
		t.Fatalf("错误密码应 401，得 %d %v", rec.Code, out)
	}

	// me：user_json + key_count（Rust 版形状）
	rec, out = doJSON(t, h, "GET", "/v1/auth/me", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("me 失败: %d %v", rec.Code, out)
	}
	if out["username"] != "alice" || out["email"] != "alice@t.dev" {
		t.Fatalf("me 资料错误: %v", out)
	}
	if _, ok := out["key_count"]; !ok {
		t.Fatalf("me 缺 key_count: %v", out)
	}

	// 未带 token 的 me → 401
	rec, _ = doJSON(t, h, "GET", "/v1/auth/me", "", nil)
	if rec.Code != 401 {
		t.Fatalf("未登录 me 应 401，得 %d", rec.Code)
	}

	// Rust 老格式用户（pbkdf2$120000$盐hex$哈希hex）直接可登录
	oldHash := auth.HashPasswordPbkdf2("rust-legacy-pass")
	if !strings.HasPrefix(oldHash, "pbkdf2$") {
		t.Fatalf("新哈希格式错误: %s", oldHash)
	}
	if _, err := app.DB.Exec(
		"INSERT INTO users (username, email, password_hash, status, created_ts) VALUES ('rustu','rust@t.dev',?,1,?)",
		oldHash, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	rec, out = doJSON(t, h, "POST", "/v1/auth/login", "", map[string]string{
		"account": "rust@t.dev", "password": "rust-legacy-pass"})
	if rec.Code != 200 || out["token"] == "" {
		t.Fatalf("pbkdf2 老格式登录失败: %d %v", rec.Code, out)
	}
	// 老格式错误密码仍拒绝
	rec, _ = doJSON(t, h, "POST", "/v1/auth/login", "", map[string]string{
		"account": "rust@t.dev", "password": "rust-legacy-bad"})
	if rec.Code != 401 {
		t.Fatalf("老格式错误密码应 401，得 %d", rec.Code)
	}

	// 封禁用户（status=0）不可登录（Login 括号修复回归）
	_, _ = app.DB.Exec("UPDATE users SET status=0 WHERE email='alice@t.dev'")
	rec, _ = doJSON(t, h, "POST", "/v1/auth/login", "", map[string]string{
		"account": "alice", "password": "password123"})
	if rec.Code != 401 {
		t.Fatalf("封禁用户应 401，得 %d", rec.Code)
	}
}

// TestMyConsoleData P0 回归：/v1/my/* 控制台契约形状（keys/balance/usage/history/checkup）。
func TestMyConsoleData(t *testing.T) {
	app, _, _ := newTestApp(t)
	h := app.Routes()

	rec, out := doJSON(t, h, "POST", "/v1/user/register", "", map[string]string{
		"username": "bob", "email": "bob@t.dev", "password": "password123"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)

	// balance → {balance_micro, today_cost_micro, total_cost_micro, ...}（Rust 版对象形状）
	rec, out = doJSON(t, h, "GET", "/v1/my/balance", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("balance: %d", rec.Code)
	}
	if _, ok := out["balance_micro"]; !ok {
		t.Fatalf("balance 应为对象（含 balance_micro），得 %v", out)
	}
	if _, ok := out["today_cost_micro"]; !ok {
		t.Fatalf("balance 缺 today_cost_micro: %v", out)
	}

	// keys → {keys:[{id,prefix,name,revoked,created_ts,can_reveal}]}
	rec, out = doJSON(t, h, "POST", "/v1/my/keys", tok, map[string]string{"name": "k1"})
	if rec.Code != 200 {
		t.Fatalf("创建密钥失败: %d %v", rec.Code, out)
	}
	rec, out = doJSON(t, h, "GET", "/v1/my/keys", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("keys: %d", rec.Code)
	}
	keys, ok := out["keys"].([]any)
	if !ok || len(keys) != 1 {
		t.Fatalf("keys 形状错误: %v", out)
	}
	k0 := keys[0].(map[string]any)
	if _, ok := k0["prefix"]; !ok {
		t.Fatalf("keys[0] 缺 prefix 字段（前端消费 k.prefix）: %v", k0)
	}
	if _, ok := k0["can_reveal"]; !ok {
		t.Fatalf("keys[0] 缺 can_reveal 字段: %v", k0)
	}

	// usage → {user_id, username, today:{calls,ok_rate,avg_latency_ms}, week, by_model, recent}
	rec, out = doJSON(t, h, "GET", "/v1/my/usage", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("usage: %d", rec.Code)
	}
	today, _ := out["today"].(map[string]any)
	if today == nil {
		t.Fatalf("usage.today 形状错误: %v", out)
	}
	if _, ok := today["calls"]; !ok {
		t.Fatalf("usage.today.calls 缺失: %v", today)
	}
	if _, ok := today["avg_latency_ms"]; !ok {
		t.Fatalf("usage.today.avg_latency_ms 缺失: %v", today)
	}
	if out["recent"] == nil {
		t.Fatalf("usage.recent 缺失: %v", out)
	}

	// history → {items, total}（items 全 18 字段）
	rec, out = doJSON(t, h, "GET", "/v1/my/history?page=1&page_size=10", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("history: %d", rec.Code)
	}
	if out["items"] == nil || out["total"] == nil {
		t.Fatalf("history 形状错误（需 items+total）: %v", out)
	}

	// checkup → {ok, score, items:[{id,ok,level,title,detail,advice}], generated_ts}
	rec, out = doJSON(t, h, "GET", "/v1/my/checkup", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("checkup: %d", rec.Code)
	}
	if out["score"] == nil {
		t.Fatalf("checkup 形状错误（需 score）: %v", out)
	}
	items, ok := out["items"].([]any)
	if !ok || len(items) != 6 {
		t.Fatalf("checkup.items 应为 6 项，得 %d: %v", len(items), out)
	}
	c0 := items[0].(map[string]any)
	for _, f := range []string{"id", "level", "title", "detail", "advice"} {
		if _, ok := c0[f]; !ok {
			t.Fatalf("checkup.items[0] 缺 %s: %v", f, c0)
		}
	}

	// balance-alert → {threshold_micro, armed, email}
	rec, out = doJSON(t, h, "GET", "/v1/my/balance-alert", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("balance-alert: %d", rec.Code)
	}
	if _, ok := out["armed"]; !ok {
		t.Fatalf("balance-alert 缺 armed: %v", out)
	}

	// 头像上传：合法 PNG 魔数 → 200；非法内容 → 400
	png := append([]byte{0x89, 'P', 'N', 'G'}, make([]byte, 64)...)
	req := httptest.NewRequest("POST", "/v1/my/avatar", bytes.NewReader(png))
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("头像上传应 200，得 %d %s", rec.Code, rec.Body.String())
	}
	req2 := httptest.NewRequest("POST", "/v1/my/avatar", strings.NewReader("not-an-image"))
	req2.Header.Set("Content-Type", "image/png")
	req2.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req2)
	if rec.Code != 400 {
		t.Fatalf("伪造头像应 400，得 %d", rec.Code)
	}

	// 未登录 → 401
	rec, _ = doJSON(t, h, "GET", "/v1/my/keys", "", nil)
	if rec.Code != 401 {
		t.Fatalf("未登录 keys 应 401，得 %d", rec.Code)
	}
}

// TestFreeModelsAndVIP 免费线移植 + paid 标签 + VIP 可见下架模型回归
func TestFreeModelsAndVIP(t *testing.T) {
	tmp := t.TempDir()
	// 模拟免费上游：记录收到的 model 字段，回标准响应
	var gotModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			w.WriteHeader(404)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var m struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(b, &m)
		gotModel = m.Model
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	t.Cleanup(up.Close)

	cfg := config.Default()
	cfg.Lines = []config.Line{
		{ // 按次收费线：m1 在架，m2 normal 已下架（仅 vip 价生效）
			ID: "aqua", Name: "按次线", Mode: "per_call", BaseURL: up.URL, Keys: []string{"k"},
			Models: []config.Model{{SiteID: "m1"}, {SiteID: "m2"}},
		},
		{ // 免费线
			ID: "nvidia", Name: "免费线", Mode: "free", BaseURL: up.URL, Keys: []string{"fk"},
			Models: []config.Model{
				{SiteID: "qwen3-8b", UpstreamID: "vendor/qwen3-8b"},
				{SiteID: "cogview-3-flash", UpstreamID: "cog", Image: true},
			},
		},
	}
	d, err := db.Open(filepath.Join(tmp, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.InitTables(d); err != nil {
		t.Fatal(err)
	}
	app := New(cfg, &db.DBx{DB: d})
	legacyProxy = nil // 重置包级全局（绞杀者单测可能已设置，纯 Go 模式必须走原生 /v1/models）
	h := app.Routes()

	// 价目：m1 normal 5000 生效；m2 normal 已过期（下架），vip 3500 永久生效
	now := time.Now().Unix()
	for _, p := range []struct {
		model, grp string
		ends       any
		price      int64
	}{
		{"aqua/m1", "normal", nil, 5000},
		{"aqua/m2", "normal", now - 3600, 6000},
		{"aqua/m2", "vip", nil, 3500},
	} {
		if _, err := d.Exec(
			"INSERT INTO pricing (model, price_micro, starts_at, ends_at, grp, mode) VALUES (?,?,?,?,?,'per_call')",
			p.model, p.price, 0, p.ends, p.grp); err != nil {
			t.Fatal(err)
		}
	}

	// 注册普通用户（DB 注入邮箱验证码）
	_, _ = d.Exec(
		"INSERT INTO email_codes (email, purpose, code, fails, expire_ts) VALUES ('u1@t.dev','register','123456',0,?)",
		time.Now().Unix()+600)
	rec, out := doJSON(t, h, "POST", "/v1/auth/register", "", map[string]string{
		"email": "u1@t.dev", "code": "123456", "username": "user1", "password": "password123"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)

	modelsOf := func(token string) map[string]map[string]any {
		t.Helper()
		rec, out := doJSON(t, h, "GET", "/v1/models", token, nil)
		if rec.Code != 200 {
			t.Fatalf("models: %d %v", rec.Code, out)
		}
		m := map[string]map[string]any{}
		for _, it := range out["data"].([]any) {
			x := it.(map[string]any)
			m[x["id"].(string)] = x
		}
		return m
	}

	// 匿名：auto + aqua/m1（paid）+ 2 免费模型；无 m2（已下架）
	m := modelsOf("")
	if len(m) != 4 {
		t.Fatalf("期望 4 条目，得 %d: %v", len(m), m)
	}
	if m["auto"] == nil || m["auto"]["auto"] != true {
		t.Fatalf("auto 条目缺失: %v", m["auto"])
	}
	if m["aqua/m1"]["paid"] != true || m["aqua/m1"]["price_micro"] != float64(5000) {
		t.Fatalf("收费模型 paid 标签/价格错误: %v", m["aqua/m1"])
	}
	if _, has := m["aqua/m1"]["description"]; !has {
		t.Fatalf("收费模型缺 description: %v", m["aqua/m1"])
	}
	if _, has := m["qwen3-8b"]["paid"]; has {
		t.Fatalf("免费模型不应有 paid 标签: %v", m["qwen3-8b"])
	}
	if m["qwen3-8b"]["owned_by"] != "nvidia" {
		t.Fatalf("免费模型 owned_by 错误: %v", m["qwen3-8b"])
	}
	if _, has := m["aqua/m2"]; has {
		t.Fatalf("已下架模型不应出现在普通列表: %v", m["aqua/m2"])
	}

	// 普通用户：同匿名口径
	if _, has := modelsOf(tok)["aqua/m2"]; has {
		t.Fatal("普通用户不应看到已下架模型")
	}

	// VIP 用户：m2 可见（回退 vip 价 3500）
	_, _ = d.Exec("UPDATE users SET price_grp_call='vip' WHERE email='u1@t.dev'")
	m = modelsOf(tok)
	if m["aqua/m2"] == nil || m["aqua/m2"]["price_micro"] != float64(3500) {
		t.Fatalf("VIP 应见已下架模型（vip 价 3500）: %v", m["aqua/m2"])
	}
	if _, has := modelsOf("")["aqua/m2"]; has {
		t.Fatal("匿名不应看到已下架模型")
	}

	// 免费模型调用：成功 + 上游收到真实 ID + 请求行 billed=0
	rec, out = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "qwen3-8b", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec.Code != 200 {
		t.Fatalf("免费 chat: %d %v", rec.Code, out)
	}
	if rec.Header().Get("X-AQUA-Model") != "qwen3-8b" {
		t.Fatalf("缺 X-AQUA-Model: %s", rec.Header().Get("X-AQUA-Model"))
	}
	if gotModel != "vendor/qwen3-8b" {
		t.Fatalf("上游 model 应为真实 ID，得 %s", gotModel)
	}
	var billed int64
	var state string
	_ = d.QueryRow("SELECT billed, bill_state FROM requests WHERE model='qwen3-8b' AND ok=1").Scan(&billed, &state)
	if billed != 0 || state != "free" {
		t.Fatalf("免费请求应 billed=0 state=free，得 %d %s", billed, state)
	}

	// 旧 ID 兼容：nvidia/qwen3-8b → qwen3-8b
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "nvidia/qwen3-8b", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec.Code != 200 {
		t.Fatalf("旧 ID 免费 chat: %d", rec.Code)
	}

	// auto 路由：无健康数据 → 回退兜底（第一个非特殊免费模型）
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "auto", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec.Code != 200 {
		t.Fatalf("auto chat: %d", rec.Code)
	}
	if rec.Header().Get("X-AQUA-Model") != "qwen3-8b" {
		t.Fatalf("auto 应回退 qwen3-8b，得 %s", rec.Header().Get("X-AQUA-Model"))
	}

	// 未收录模型 → 404；免费模型未登录 → 401
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "no-such-model", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec.Code != 404 {
		t.Fatalf("未收录模型应 404，得 %d", rec.Code)
	}
	// 免登录免费对话（Rust AUTH_MODE=open 等价；体验中心/树洞/竞技场依赖）→ 200
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", "", map[string]any{
		"model": "qwen3-8b", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec.Code != 200 {
		t.Fatalf("免登录免费对话应 200，得 %d", rec.Code)
	}

	// retired：上游 404 两次登记 → 列表隐藏 + 调用 410
	_, _ = d.Exec("INSERT INTO retired_models (model, retired_ts, hits) VALUES ('vendor/qwen3-8b', ?, 2)", now)
	if _, has := modelsOf(tok)["qwen3-8b"]; has {
		t.Fatal("retired 免费模型应从列表隐藏")
	}
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "qwen3-8b", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec.Code != 410 {
		t.Fatalf("retired 模型应 410，得 %d", rec.Code)
	}
}

// 统一前缀分组路由（2026-09-12 收费改造）：
// aqua/ 前缀按密钥计费分组选线（per_call→按次线 / per_token→按量线）；
// 未分组旧密钥按 default_grp；模型不在当前分组列表 → 404；
// 旧前缀（t/）显式直连不受密钥分组影响
func TestUnifiedPrefixGroupRouting(t *testing.T) {
	app, _, _ := newTestApp(t)
	app.Cfg.Billing.UnifiedPrefix = "aqua"
	app.Cfg.Billing.DefaultGrp = "per_call"
	h := app.Routes()

	rec, out := doJSON(t, h, "POST", "/v1/user/register", "", map[string]string{
		"username": "grptester", "email": "grp@t.dev", "password": "password123"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)
	if _, err := app.DB.Exec("UPDATE users SET balance_micro=100000 WHERE email='grp@t.dev'"); err != nil {
		t.Fatal(err)
	}

	mkKey := func(name, grp string) string {
		t.Helper()
		rec, out := doJSON(t, h, "POST", "/v1/my/keys", tok, map[string]string{"name": name, "billing_grp": grp})
		if rec.Code != 200 {
			t.Fatalf("创建密钥 %s(%s): %d %v", name, grp, rec.Code, out)
		}
		return out["key"].(string)
	}
	keyCall := mkKey("按次钥", "per_call")
	keyTok := mkKey("按量钥", "per_token")
	keyOld := mkKey("旧式钥", "")

	// 密钥列表应带 billing_grp 标记
	rec, out = doJSON(t, h, "GET", "/v1/my/keys", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("密钥列表: %d", rec.Code)
	}
	grps := map[string]bool{}
	for _, it := range out["keys"].([]any) {
		grps[it.(map[string]any)["billing_grp"].(string)] = true
	}
	if !grps["per_call"] || !grps["per_token"] || !grps[""] {
		t.Fatalf("密钥分组标记错误: %v", grps)
	}

	balOf := func() int64 {
		t.Helper()
		var b int64
		_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE email='grp@t.dev'").Scan(&b)
		return b
	}
	chat := func(key, model string) int {
		t.Helper()
		rec, _ := doJSON(t, h, "POST", "/v1/chat/completions", key, map[string]any{
			"model": model, "messages": []map[string]string{{"role": "user", "content": "hi"}}})
		return rec.Code
	}

	// 按次密钥：aqua/pc1 → 按次线成功，收单价 2000
	if c := chat(keyCall, "aqua/pc1"); c != 200 {
		t.Fatalf("按次密钥调 aqua/pc1 应 200，得 %d", c)
	}
	if b := balOf(); b != 98000 {
		t.Fatalf("按次计费应扣单价 2000，余额 %d", b)
	}
	// 按次密钥：aqua/m1（仅按量分组提供）→ 404
	if c := chat(keyCall, "aqua/m1"); c != 404 {
		t.Fatalf("按次密钥调按量专属 aqua/m1 应 404，得 %d", c)
	}
	// 按量密钥：aqua/m1 → 按量线成功，按 tokens 计费
	b0 := balOf()
	if c := chat(keyTok, "aqua/m1"); c != 200 {
		t.Fatalf("按量密钥调 aqua/m1 应 200，得 %d", c)
	}
	if b := balOf(); b >= b0 {
		t.Fatalf("按量计费应有扣费：前 %d 后 %d", b0, b)
	}
	// 按量密钥：aqua/pc1（仅按次分组提供）→ 404
	if c := chat(keyTok, "aqua/pc1"); c != 404 {
		t.Fatalf("按量密钥调按次专属 aqua/pc1 应 404，得 %d", c)
	}
	// 未分组旧密钥：默认按次（default_grp）→ aqua/pc1 成功、aqua/m1 404
	if c := chat(keyOld, "aqua/pc1"); c != 200 {
		t.Fatalf("未分组密钥默认按次调 aqua/pc1 应 200，得 %d", c)
	}
	if c := chat(keyOld, "aqua/m1"); c != 404 {
		t.Fatalf("未分组密钥调按量专属 aqua/m1 应 404，得 %d", c)
	}
	// 旧前缀显式直连：不受密钥分组影响（按量钥调 t/m1 → 200）
	if c := chat(keyTok, "t/m1"); c != 200 {
		t.Fatalf("旧前缀 t/m1 显式直连应 200，得 %d", c)
	}
}
