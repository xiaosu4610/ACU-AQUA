// P4 密钥分发配额回归（20260919）：限额拦截 / 惰性重置 / 有效期 / 倍率换算。
package httpapi

import (
	"net/http"
	"testing"
	"time"
)

// newQuotaApp 建测试站（复用 newTestApp 的模拟上游），返回 app 与一个已充值账号的会话令牌
func newQuotaApp(t *testing.T) (*App, http.Handler, string) {
	t.Helper()
	app, _, _ := newTestApp(t)
	h := app.Routes()
	seedRegCode(t, app, "quota@t.dev")
	rec, out := doJSON(t, h, "POST", "/v1/user/register", "", map[string]string{
		"username": "quotauser", "email": "quota@t.dev", "password": "password123", "code": "852341"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)
	if _, err := app.DB.Exec("UPDATE users SET balance_micro=10000000 WHERE email='quota@t.dev'"); err != nil {
		t.Fatal(err)
	}
	return app, h, tok
}

// TestKeyQuota_CountLimitBlocks 按次数限额：用满后必须 403 key_quota_exceeded
func TestKeyQuota_CountLimitBlocks(t *testing.T) {
	app, h, tok := newQuotaApp(t)
	// 创建带"2 次"限额的密钥
	rec, out := doJSON(t, h, "POST", "/v1/my/keys", tok, map[string]any{
		"name": "限次钥", "billing_grp": "per_call", "quota_type": "count", "quota_limit": 2})
	if rec.Code != 200 {
		t.Fatalf("创建密钥失败: %d %v", rec.Code, out)
	}
	key := out["key"].(string)

	call := func() (int, string) {
		rec, out := doJSON(t, h, "POST", "/v1/chat/completions", key, map[string]any{
			"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
			"max_tokens": 50})
		msg := ""
		if e, ok := out["error"].(map[string]any); ok {
			msg, _ = e["code"].(string)
		}
		return rec.Code, msg
	}

	// 前 2 次放行
	for i := 0; i < 2; i++ {
		if c, m := call(); c != 200 {
			t.Fatalf("第 %d 次应放行，得 %d (%s)", i+1, c, m)
		}
	}
	// 第 3 次必须被配额拦截
	c, m := call()
	if c != 403 || m != "key_quota_exceeded" {
		t.Fatalf("超出次数限额应 403 key_quota_exceeded，得 %d (%s)", c, m)
	}
	// 配额已用量应为 2
	var used int64
	if err := app.DB.QueryRow("SELECT quota_used FROM api_keys WHERE key_plain_enc=?", key).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 2 {
		t.Fatalf("配额已用量应为 2，得 %d", used)
	}
}

// TestKeyQuota_AmountLimitBlocks 按金额限额：消费满额后必须拦截
func TestKeyQuota_AmountLimitBlocks(t *testing.T) {
	app, h, tok := newQuotaApp(t)
	// 限额 2000 微元 = 正好一次单价（c/pc1 = 2000）
	rec, out := doJSON(t, h, "POST", "/v1/my/keys", tok, map[string]any{
		"name": "限额钥", "billing_grp": "per_call", "quota_type": "amount", "quota_limit": 2000})
	if rec.Code != 200 {
		t.Fatalf("创建密钥失败: %d %v", rec.Code, out)
	}
	key := out["key"].(string)

	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", key, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 50})
	if rec.Code != 200 {
		t.Fatalf("首次应放行，得 %d", rec.Code)
	}
	var used int64
	_ = app.DB.QueryRow("SELECT quota_used FROM api_keys WHERE key_plain_enc=?", key).Scan(&used)
	if used != 2000 {
		t.Fatalf("按金额配额应累计实扣 2000，得 %d", used)
	}
	// 再次调用：额度已用尽 → 403
	rec, out = doJSON(t, h, "POST", "/v1/chat/completions", key, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 50})
	e, _ := out["error"].(map[string]any)
	if rec.Code != 403 || e["code"] != "key_quota_exceeded" {
		t.Fatalf("金额额度用尽应 403，得 %d (%v)", rec.Code, e["code"])
	}
}

// TestKeyQuota_ResetClearsUsage 手动清零：清零后可继续调用
func TestKeyQuota_ResetClearsUsage(t *testing.T) {
	app, h, tok := newQuotaApp(t)
	rec, out := doJSON(t, h, "POST", "/v1/my/keys", tok, map[string]any{
		"name": "可重置钥", "billing_grp": "per_call", "quota_type": "count", "quota_limit": 1})
	if rec.Code != 200 {
		t.Fatalf("创建密钥失败: %d %v", rec.Code, out)
	}
	key := out["key"].(string)
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", key, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 50})
	if rec.Code != 200 {
		t.Fatalf("首次应放行，得 %d", rec.Code)
	}
	var id int64
	_ = app.DB.QueryRow("SELECT id FROM api_keys WHERE key_plain_enc=?", key).Scan(&id)
	// 清零
	rec, out = doJSON(t, h, "PATCH", "/v1/my/keys/"+itoaTest(id)+"/quota", tok, map[string]any{"action": "reset"})
	if rec.Code != 200 {
		t.Fatalf("清零失败: %d %v", rec.Code, out)
	}
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", key, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 50})
	if rec.Code != 200 {
		t.Fatalf("清零后应可继续调用，得 %d", rec.Code)
	}
}

// TestKeyQuota_LazyDailyReset 惰性每日重置：跨周期（reset_at 早于今日 0 点）自动清零
func TestKeyQuota_LazyDailyReset(t *testing.T) {
	app, h, tok := newQuotaApp(t)
	rec, out := doJSON(t, h, "POST", "/v1/my/keys", tok, map[string]any{
		"name": "日重置钥", "billing_grp": "per_call",
		"quota_type": "count", "quota_limit": 5, "quota_reset": "daily"})
	if rec.Code != 200 {
		t.Fatalf("创建密钥失败: %d %v", rec.Code, out)
	}
	key := out["key"].(string)
	var id int64
	_ = app.DB.QueryRow("SELECT id FROM api_keys WHERE key_plain_enc=?", key).Scan(&id)
	// 模拟"昨天用满"：quota_used=5、reset_at 设为 2 天前
	if _, err := app.DB.Exec("UPDATE api_keys SET quota_used=5, quota_reset_at=? WHERE id=?",
		time.Now().Unix()-2*86400, id); err != nil {
		t.Fatal(err)
	}
	// 列表读取应触发惰性重置
	rec, _ = doJSON(t, h, "GET", "/v1/my/keys", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("列表失败: %d", rec.Code)
	}
	var used int64
	_ = app.DB.QueryRow("SELECT quota_used FROM api_keys WHERE id=?", id).Scan(&used)
	if used != 0 {
		t.Fatalf("跨日应惰性清零，得 %d", used)
	}
	// 调用应放行（限额已重置）
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", key, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 50})
	if rec.Code != 200 {
		t.Fatalf("惰性重置后应放行，得 %d", rec.Code)
	}
}

// TestKeyQuota_ExpiredKeyRejected 密钥有效期：过期密钥必须 401 key_expired
func TestKeyQuota_ExpiredKeyRejected(t *testing.T) {
	app, h, tok := newQuotaApp(t)
	rec, out := doJSON(t, h, "POST", "/v1/my/keys", tok, map[string]any{
		"name": "过期钥", "billing_grp": "per_call"})
	if rec.Code != 200 {
		t.Fatalf("创建密钥失败: %d %v", rec.Code, out)
	}
	key := out["key"].(string)
	// 设为已过期
	if _, err := app.DB.Exec("UPDATE api_keys SET expires_at=? WHERE key_plain_enc=?",
		time.Now().Unix()-60, key); err != nil {
		t.Fatal(err)
	}
	rec, out = doJSON(t, h, "POST", "/v1/chat/completions", key, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 50})
	e, _ := out["error"].(map[string]any)
	if rec.Code != 401 || e["code"] != "key_expired" {
		t.Fatalf("过期密钥应 401 key_expired，得 %d (%v)", rec.Code, e["code"])
	}
}

// TestKeyQuota_RateView 倍率仅用于展示换算，不影响实扣金额
func TestKeyQuota_RateView(t *testing.T) {
	app, h, tok := newQuotaApp(t)
	// 倍率 2×（200/100）
	rec, out := doJSON(t, h, "POST", "/v1/my/keys", tok, map[string]any{
		"name": "代理钥", "billing_grp": "per_call",
		"quota_type": "amount", "quota_limit": 2000, "rate_num": 200, "rate_den": 100})
	if rec.Code != 200 {
		t.Fatalf("创建密钥失败: %d %v", rec.Code, out)
	}
	key := out["key"].(string)
	// 实扣必须仍是站内价 2000（倍率不影响实扣）
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", key, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 50})
	if rec.Code != 200 {
		t.Fatalf("应放行，得 %d", rec.Code)
	}
	var bal int64
	_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE email='quota@t.dev'").Scan(&bal)
	if bal != 10000000-2000 {
		t.Fatalf("实扣必须为站内价 2000（倍率不影响实扣），余额 %d", bal)
	}
	// 列表接口应返回代理口径换算（成本 2000 → 零售 4000 → 利润 2000）
	var keyID int64
	if err := app.DB.QueryRow("SELECT id FROM api_keys WHERE key_plain_enc=?", key).Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	rec, out = doJSON(t, h, "GET", "/v1/my/keys", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("列表失败: %d", rec.Code)
	}
	keys, _ := out["keys"].([]any)
	for _, it := range keys {
		m, _ := it.(map[string]any)
		if m["id"] != float64(keyID) {
			continue
		}
		if m["cost_micro"] == float64(2000) && m["retail_micro"] == float64(4000) && m["profit_micro"] == float64(2000) {
			return // 换算口径正确
		}
		t.Fatalf("换算口径错误：cost=%v retail=%v profit=%v", m["cost_micro"], m["retail_micro"], m["profit_micro"])
	}
	t.Fatal("未在列表中找到该密钥")
}

// itoaTest 测试用整数转字符串（避免引入 strconv 到测试文件顶部）
func itoaTest(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
