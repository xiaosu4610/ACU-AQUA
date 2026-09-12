package httpapi

import (
	"fmt"
	"testing"
	"time"
)

// TestReveal 复现生产 reveal 404：列表 can_reveal=true 但 reveal 报密钥不存在
func TestReveal(t *testing.T) {
	app, _, _ := newTestApp(t)
	legacyProxy = nil
	h := app.Routes()

	// 注册拿 token
	_, _ = app.DB.Exec(
		"INSERT INTO email_codes (email, purpose, code, fails, expire_ts) VALUES ('rv@t.dev','register','123456',0,?)",
		time.Now().Unix()+600)
	rec, out := doJSON(t, h, "POST", "/v1/auth/register", "", map[string]string{
		"email": "rv@t.dev", "code": "123456", "username": "rvuser", "password": "password123"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)

	// 创建密钥（响应无 id，从列表取）
	rec, out = doJSON(t, h, "POST", "/v1/my/keys", tok, map[string]string{"name": "t1"})
	if rec.Code != 200 {
		t.Fatalf("创建密钥失败: %d %v", rec.Code, out)
	}
	rec, out = doJSON(t, h, "GET", "/v1/my/keys", tok, nil)
	if rec.Code != 200 {
		t.Fatalf("列表失败: %d", rec.Code)
	}
	keys := out["keys"].([]any)
	if len(keys) == 0 {
		t.Fatalf("列表为空")
	}
	k0 := keys[0].(map[string]any)
	if k0["can_reveal"] != true {
		t.Fatalf("can_reveal 应为 true: %v", k0)
	}
	id := int64(k0["id"].(float64))

	// reveal
	rec, out = doJSON(t, h, "GET", "/v1/my/keys/"+fmt.Sprintf("%d", id)+"/reveal", tok, nil)
	if rec.Code != 200 {
		t.Logf("reveal 失败: %d %v", rec.Code, out)
	} else if out["key"] == "" {
		t.Logf("reveal 缺 key")
	}

	// DB 直查对照（同款 SQL 逐条件拆解）
	var plain string
	var uid2 int64
	var rev2 int64
	if err := app.DB.QueryRow("SELECT key_plain_enc, user_id, revoked FROM api_keys WHERE id=?", id).Scan(&plain, &uid2, &rev2); err != nil {
		t.Logf("直查A err=%v", err)
	} else {
		t.Logf("直查A ok: uid=%d revoked=%d len=%d", uid2, rev2, len(plain))
	}
	var n int
	if err := app.DB.QueryRow("SELECT COUNT(*) FROM api_keys WHERE id=? AND user_id=? AND revoked=0", id, uid2).Scan(&n); err != nil {
		t.Logf("直查B err=%v", err)
	} else {
		t.Logf("直查B COUNT=%d", n)
	}
	var p2 string
	if err := app.DB.QueryRow("SELECT key_plain_enc FROM api_keys WHERE id=? AND user_id=? AND revoked=0", id, uid2).Scan(&p2); err != nil {
		t.Fatalf("直查C(reveal同款SQL) err=%v", err)
	}
	t.Logf("直查C len=%d", len(p2))
}
