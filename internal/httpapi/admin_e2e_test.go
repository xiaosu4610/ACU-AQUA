// 管理后台端到端测试：登录 → 会话 → 仪表盘 → 高危操作二次密码 → 审计链
package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func adminTestSetup(t *testing.T) *App {
	t.Helper()
	app, _, _ := newTestApp(t)
	sum := sha256.Sum256([]byte("testpw"))
	t.Setenv("AQUA_ADMIN_PASSWORD_HASH", hex.EncodeToString(sum[:]))
	return app
}

// TestAdminFlow 登录/鉴权/仪表盘/审计 全链路
func TestAdminFlow(t *testing.T) {
	app := adminTestSetup(t)
	h := app.Routes()
	do := func(method, path, body, token string) *httptest.ResponseRecorder {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// 错密码 401
	if rec := do("POST", "/v1/admin/login", `{"password":"wrong"}`, ""); rec.Code != 401 {
		t.Fatalf("错密码应 401，得 %d %s", rec.Code, rec.Body.String())
	}
	// 正确密码 200 + token
	rec := do("POST", "/v1/admin/login", `{"password":"testpw"}`, "")
	if rec.Code != 200 {
		t.Fatalf("登录应 200，得 %d %s", rec.Code, rec.Body.String())
	}
	var lr struct {
		Token     string `json:"token"`
		LastLogin struct {
			Ts int64 `json:"ts"`
		} `json:"last_login"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &lr)
	if !strings.HasPrefix(lr.Token, "adm_") || len(lr.Token) != 4+64 {
		t.Fatalf("token 形状错误: %q", lr.Token)
	}

	// 无凭证 stats → 401
	if rec := do("GET", "/v1/admin/stats", "", ""); rec.Code != 401 {
		t.Fatalf("无凭证 stats 应 401，得 %d", rec.Code)
	}
	// 凭证 stats → 200 + 关键字段
	rec = do("GET", "/v1/admin/stats", "", lr.Token)
	if rec.Code != 200 {
		t.Fatalf("stats 应 200，得 %d %s", rec.Code, rec.Body.String())
	}
	var stats map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &stats)
	for _, k := range []string{"income", "upstream", "calls", "users", "tide", "trend", "top", "prices", "floor_safety"} {
		if _, ok := stats[k]; !ok {
			t.Fatalf("stats 缺字段 %s", k)
		}
	}
	// 其他端点形状
	if rec := do("GET", "/v1/admin/quota", "", lr.Token); rec.Code != 200 {
		t.Fatalf("quota 应 200，得 %d", rec.Code)
	}
	if rec := do("GET", "/v1/admin/reconcile", "", lr.Token); rec.Code != 200 {
		t.Fatalf("reconcile 应 200，得 %d", rec.Code)
	}
	if rec := do("GET", "/v1/admin/supervision", "", lr.Token); rec.Code != 200 {
		t.Fatalf("supervision 应 200，得 %d", rec.Code)
	}
	if rec := do("GET", "/v1/admin/users?page=1", "", lr.Token); rec.Code != 200 {
		t.Fatalf("users 应 200，得 %d", rec.Code)
	}
	// 审计已有 login_fail + login_ok
	rec = do("GET", "/v1/admin/audit", "", lr.Token)
	var al struct {
		Items []struct {
			Action   string `json:"action"`
			SelfHash string `json:"self_hash"`
		} `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &al)
	if len(al.Items) < 2 {
		t.Fatalf("审计应含 login_fail/login_ok，得 %d 条", len(al.Items))
	}
	if al.Items[0].SelfHash == "" {
		t.Fatalf("审计缺哈希链")
	}
	// 单点登录：再次登录后旧 token 失效
	rec = do("POST", "/v1/admin/login", `{"password":"testpw"}`, "")
	var lr2 struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &lr2)
	if rec := do("GET", "/v1/admin/stats", "", lr.Token); rec.Code != 401 {
		t.Fatalf("旧会话应被踢（401），得 %d", rec.Code)
	}
	if rec := do("GET", "/v1/admin/stats", "", lr2.Token); rec.Code != 200 {
		t.Fatalf("新会话应有效，得 %d", rec.Code)
	}
	// logout
	if rec := do("POST", "/v1/admin/logout", "", lr2.Token); rec.Code != 200 {
		t.Fatalf("logout 应 200，得 %d", rec.Code)
	}
	if rec := do("GET", "/v1/admin/stats", "", lr2.Token); rec.Code != 401 {
		t.Fatalf("logout 后应 401，得 %d", rec.Code)
	}
}

// TestAdminBalanceConfirm 余额操作：二次密码 + 落账 + 审计
func TestAdminBalanceConfirm(t *testing.T) {
	app := adminTestSetup(t)
	h := app.Routes()
	do := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	// 注册一个用户（走用户面）→ 拿 uid
	rec := do("POST", "/v1/user/register", `{"username":"user1","email":"user1@t.cn","password":"pw123456"}`, "")
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %s", rec.Code, rec.Body.String())
	}
	rec = do("POST", "/v1/admin/login", `{"password":"testpw"}`, "")
	var lr struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &lr)

	// 错二次密码 → 403
	rec = do("POST", "/v1/admin/users/1/balance",
		`{"amount_micro":5000,"note":"测试发放","confirm_password":"bad"}`, lr.Token)
	if rec.Code != 403 {
		t.Fatalf("错二次密码应 403，得 %d %s", rec.Code, rec.Body.String())
	}
	// 对二次密码 → 200
	rec = do("POST", "/v1/admin/users/1/balance",
		`{"amount_micro":5000,"note":"测试发放","confirm_password":"testpw"}`, lr.Token)
	if rec.Code != 200 {
		t.Fatalf("发放应 200，得 %d %s", rec.Code, rec.Body.String())
	}
	var br struct {
		Before int64 `json:"before_micro"`
		After  int64 `json:"after_micro"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &br)
	if br.Before != 0 || br.After != 5000 {
		t.Fatalf("余额变化错误: %+v", br)
	}
	// 用户详情可见流水
	rec = do("GET", "/v1/admin/users/1", "", lr.Token)
	var ud struct {
		User struct {
			BalanceMicro int64 `json:"balance_micro"`
		} `json:"user"`
		Flows struct {
			Total int64 `json:"total"`
		} `json:"flows"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ud)
	if ud.User.BalanceMicro != 5000 || ud.Flows.Total != 1 {
		t.Fatalf("用户详情不符: %+v", ud)
	}
	// 扣减到负 → 400
	rec = do("POST", "/v1/admin/users/1/balance",
		`{"amount_micro":-99999,"note":"超扣","confirm_password":"testpw"}`, lr.Token)
	if rec.Code != 400 {
		t.Fatalf("超扣应 400，得 %d", rec.Code)
	}
}
