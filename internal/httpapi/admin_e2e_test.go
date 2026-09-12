// 管理后台端到端测试：登录 → 会话 → 仪表盘 → 高危操作二次密码 → 审计链
package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// TestAdminLinesUserOps 上游线路在线管理（热重载）+ 用户管理扩展全链路
func TestAdminLinesUserOps(t *testing.T) {
	app, up, _ := newTestApp(t)
	sum := sha256.Sum256([]byte("testpw"))
	t.Setenv("AQUA_ADMIN_PASSWORD_HASH", hex.EncodeToString(sum[:]))
	h := app.Routes()
	doJSON2 := func(method, path, body, token string) (*httptest.ResponseRecorder, map[string]any) {
		var rd *bytes.Reader
		if body != "" {
			rd = bytes.NewReader([]byte(body))
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

	rec, out := doJSON2("POST", "/v1/admin/login", `{"password":"testpw"}`, "")
	var lr struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &lr)
	at := lr.Token

	// A. 线路列表：toml 种子已导入（t/c 两线）
	rec, out = doJSON2("GET", "/v1/admin/lines", "", at)
	if rec.Code != 200 {
		t.Fatalf("lines 应 200: %d %v", rec.Code, out)
	}
	lines := out["lines"].([]any)
	if len(lines) != 2 {
		t.Fatalf("种子应导入 2 条线，得 %d", len(lines))
	}

	// B. 错二次密码 → 403
	rec, _ = doJSON2("POST", "/v1/admin/lines",
		`{"id":"x","name":"测试线","mode":"per_call","base_url":"`+up.URL+`","keys":"sk-upstream-1,sk-upstream-1,sk-x2","confirm_password":"bad"}`, at)
	if rec.Code != 403 {
		t.Fatalf("错二次密码应 403，得 %d", rec.Code)
	}
	// 对二次密码：新增线（重复钥去重 → 2 把）
	rec, out = doJSON2("POST", "/v1/admin/lines",
		`{"id":"x","name":"测试线","mode":"per_call","base_url":"`+up.URL+`","keys":"sk-upstream-1,sk-upstream-1,sk-x2","confirm_password":"testpw"}`, at)
	if rec.Code != 200 {
		t.Fatalf("新增线应 200: %d %v", rec.Code, out)
	}
	if out["keys_added"].(float64) != 2 {
		t.Fatalf("批量加钥应 2 把，得 %v", out["keys_added"])
	}

	// C. 新增模型 + 价格播种（normal+vip 两组；带成本率供面值台账回写）
	rec, out = doJSON2("POST", "/v1/admin/lines/x/models",
		`{"site_id":"xm","per_call_sell":3000,"per_call_cost":2000,"in_cost_rate10":120000,"cache_cost_rate10":15000,"out_cost_rate10":360000,"confirm_password":"testpw"}`, at)
	if rec.Code != 200 {
		t.Fatalf("新增模型应 200: %d %v", rec.Code, out)
	}
	if out["pricing_seeded"].(float64) != 2 {
		t.Fatalf("应播种 2 组价，得 %v", out["pricing_seeded"])
	}

	// D. 热重载生效：用户密钥直调 x/xm → 200 扣 3000 + 面值台账回写
	rec, out = doJSON2("POST", "/v1/user/register", `{"username":"grpu","email":"grpu@t.dev","password":"pw123456"}`, "")
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	if _, err := app.DB.Exec("UPDATE users SET balance_micro=100000 WHERE email='grpu@t.dev'"); err != nil {
		t.Fatal(err)
	}
	rec, out = doJSON2("POST", "/v1/my/keys", `{"name":"t1"}`, out["token"].(string))
	userKey := out["key"].(string)
	bal := func() int64 {
		var b int64
		_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE email='grpu@t.dev'").Scan(&b)
		return b
	}
	rec, _ = doJSON2("POST", "/v1/chat/completions", `{"model":"x/xm","messages":[{"role":"user","content":"hi"}]}`, userKey)
	if rec.Code != 200 {
		t.Fatalf("热重载后新线调用应 200: %d %s", rec.Code, rec.Body.String())
	}
	if b := bal(); b != 97000 {
		t.Fatalf("新线按次扣费应 97000，得 %d", b)
	}
	var faceUsed int64
	_ = app.DB.QueryRow("SELECT used_micro FROM line_keys WHERE line_id='x'").Scan(&faceUsed)
	if faceUsed == 0 {
		t.Fatal("新线面值台账应回写")
	}
	var resolved string
	_ = app.DB.QueryRow("SELECT resolved_line FROM requests WHERE model='x/xm' AND ok=1").Scan(&resolved)
	if resolved != "x" {
		t.Fatalf("resolved_line 应为 x，得 %q", resolved)
	}

	// E. 密钥池：脱敏 + 停用 + 删除
	rec, out = doJSON2("GET", "/v1/admin/lines/x/keys", "", at)
	first := out["keys"].([]any)[0].(map[string]any)
	if !strings.Contains(first["masked"].(string), "…") {
		t.Fatalf("密钥应脱敏: %v", first["masked"])
	}
	rec, _ = doJSON2("POST", "/v1/admin/lines/x/keys/0/dead", `{"dead":true,"confirm_password":"testpw"}`, at)
	if rec.Code != 200 {
		t.Fatalf("停用钥应 200: %d", rec.Code)
	}
	rec, out = doJSON2("GET", "/v1/admin/lines/x/keys", "", at)
	if ks := out["keys"].([]any); ks[0].(map[string]any)["dead"] != true {
		t.Fatalf("dead 标记未生效: %v", ks[0])
	}
	rec, _ = doJSON2("DELETE", "/v1/admin/lines/x/keys/1", `{"confirm_password":"testpw"}`, at)
	if rec.Code != 200 {
		t.Fatalf("删除钥应 200: %d", rec.Code)
	}
	// 批量加钥端点（补一把活钥供 F 段调用）
	rec, out = doJSON2("POST", "/v1/admin/lines/x/keys", `{"keys":"sk-x3\nsk-upstream-1","confirm_password":"testpw"}`, at)
	if rec.Code != 200 {
		t.Fatalf("批量加钥应 200: %d %v", rec.Code, out)
	}
	if out["added"].(float64) != 1 || out["duplicates"].(float64) != 1 {
		t.Fatalf("加钥应 1 新 1 重复，得 %v", out)
	}

	// F. 停用线路 → 调用 404；重新启用 → 200
	rec, _ = doJSON2("POST", "/v1/admin/lines/x", `{"enabled":false,"confirm_password":"testpw"}`, at)
	if rec.Code != 200 {
		t.Fatalf("停用线应 200: %d", rec.Code)
	}
	rec, _ = doJSON2("POST", "/v1/chat/completions", `{"model":"x/xm","messages":[{"role":"user","content":"hi"}]}`, userKey)
	if rec.Code != 404 {
		t.Fatalf("停用线后调用应 404，得 %d", rec.Code)
	}
	rec, _ = doJSON2("POST", "/v1/admin/lines/x", `{"enabled":true,"confirm_password":"testpw"}`, at)
	rec, _ = doJSON2("POST", "/v1/chat/completions", `{"model":"x/xm","messages":[{"role":"user","content":"hi"}]}`, userKey)
	if rec.Code != 200 {
		t.Fatalf("重新启用后调用应 200: %d", rec.Code)
	}

	// G. 用户管理：封禁 → 密钥 401；解封恢复
	utok := func() string {
		rec, out := doJSON2("POST", "/v1/user/login", `{"account":"grpu@t.dev","password":"pw123456"}`, "")
		if rec.Code != 200 {
			t.Fatalf("登录应 200: %d %s", rec.Code, rec.Body.String())
		}
		return out["token"].(string)
	}
	var uidF float64
	_ = app.DB.QueryRow("SELECT id FROM users WHERE email='grpu@t.dev'").Scan(&uidF)
	uid := fmt.Sprintf("%d", int64(uidF))

	rec, _ = doJSON2("POST", "/v1/admin/users/"+uid+"/status", `{"status":0,"note":"违规测试","confirm_password":"testpw"}`, at)
	if rec.Code != 200 {
		t.Fatalf("封禁应 200: %d", rec.Code)
	}
	rec, _ = doJSON2("POST", "/v1/chat/completions", `{"model":"x/xm","messages":[{"role":"user","content":"hi"}]}`, userKey)
	if rec.Code != 401 {
		t.Fatalf("封禁后密钥调用应 401，得 %d", rec.Code)
	}
	rec, _ = doJSON2("POST", "/v1/admin/users/"+uid+"/status", `{"status":1,"note":"恢复","confirm_password":"testpw"}`, at)
	if rec.Code != 200 {
		t.Fatalf("解封应 200: %d", rec.Code)
	}
	rec, _ = doJSON2("POST", "/v1/chat/completions", `{"model":"x/xm","messages":[{"role":"user","content":"hi"}]}`, userKey)
	if rec.Code != 200 {
		t.Fatalf("解封后应 200: %d", rec.Code)
	}

	// H. kick：登录会话 → 强制下线 → me 401
	sessTok := utok()
	rec, _ = doJSON2("GET", "/v1/user/me", "", sessTok)
	if rec.Code != 200 {
		t.Fatalf("会话应有效: %d", rec.Code)
	}
	rec, _ = doJSON2("POST", "/v1/admin/users/"+uid+"/kick", `{"confirm_password":"testpw"}`, at)
	if rec.Code != 200 {
		t.Fatalf("kick 应 200: %d", rec.Code)
	}
	rec, _ = doJSON2("GET", "/v1/user/me", "", sessTok)
	if rec.Code != 401 {
		t.Fatalf("kick 后会话应失效，得 %d", rec.Code)
	}

	// I. 重置密码：新密码可登录、旧密码失效
	rec, out = doJSON2("POST", "/v1/admin/users/"+uid+"/password", `{"confirm_password":"testpw"}`, at)
	newPwd := out["new_password"].(string)
	if newPwd == "" {
		t.Fatalf("重置应返回新密码: %v", out)
	}
	rec, _ = doJSON2("POST", "/v1/user/login", `{"account":"grpu@t.dev","password":"pw123456"}`, "")
	if rec.Code != 401 {
		t.Fatalf("旧密码应失效，得 %d", rec.Code)
	}
	rec, _ = doJSON2("POST", "/v1/user/login", `{"account":"grpu@t.dev","password":"`+newPwd+`"}`, "")
	if rec.Code != 200 {
		t.Fatalf("新密码应可登录: %d %s", rec.Code, rec.Body.String())
	}

	// J. 软删除：status=99 + 密钥失效 + 标识符释放
	rec, _ = doJSON2("DELETE", "/v1/admin/users/"+uid, `{"confirm_email":"grpu@t.dev","confirm_password":"testpw"}`, at)
	if rec.Code != 200 {
		t.Fatalf("软删除应 200: %d %s", rec.Code, rec.Body.String())
	}
	var st int
	_ = app.DB.QueryRow("SELECT status FROM users WHERE id=?", int64(uidF)).Scan(&st)
	if st != 99 {
		t.Fatalf("软删后 status 应 99，得 %d", st)
	}
	rec, _ = doJSON2("POST", "/v1/chat/completions", `{"model":"x/xm","messages":[{"role":"user","content":"hi"}]}`, userKey)
	if rec.Code != 401 {
		t.Fatalf("注销后密钥应 401，得 %d", rec.Code)
	}
	rec, _ = doJSON2("POST", "/v1/user/register", `{"username":"grpu2","email":"grpu@t.dev","password":"pw123456"}`, "")
	if rec.Code != 200 {
		t.Fatalf("原邮箱应可重新注册: %d %s", rec.Code, rec.Body.String())
	}

	// K. 删除线路（须先停用）
	rec, _ = doJSON2("POST", "/v1/admin/lines/x", `{"enabled":false,"confirm_password":"testpw"}`, at)
	rec, _ = doJSON2("DELETE", "/v1/admin/lines/x", `{"confirm_password":"testpw"}`, at)
	if rec.Code != 200 {
		t.Fatalf("删除线应 200: %d", rec.Code)
	}
	rec, out = doJSON2("GET", "/v1/admin/lines", "", at)
	if len(out["lines"].([]any)) != 2 {
		t.Fatalf("删除后应剩 2 条线: %v", out)
	}
}
