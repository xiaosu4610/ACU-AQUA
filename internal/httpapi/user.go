package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"acu-aqua/gateway/internal/auth"
	"acu-aqua/gateway/internal/billing"
	"acu-aqua/gateway/internal/config"
)

var _ = time.Now

// handleRegister 注册
func (a *App) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username   string `json:"username"`
		Email      string `json:"email"`
		Password   string `json:"password"`
		InviteCode string `json:"invite_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errOut(w, 400, "bad_request", "请求体格式错误")
		return
	}
	uid, err := auth.Register(a.DB.DB, req.Username, req.Email, req.Password)
	if err != nil {
		errOut(w, 400, "bad_request", err.Error())
		return
	}
	// 邀请关系绑定（静默失败不阻断注册；同 IP 24h ≥3 静默跳过，防刷闸在 inviteBind 内）
	if strings.TrimSpace(req.InviteCode) != "" {
		a.inviteBind(uid, req.InviteCode, clientIP(r))
	}
	tok, err := auth.CreateUserSession(a.DB.DB, uid)
	if err != nil {
		errOut(w, 500, "internal_error", "会话创建失败")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "token": tok, "user_id": uid})
}

// handleLogin 登录
func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Account  string `json:"account"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errOut(w, 400, "bad_request", "请求体格式错误")
		return
	}
	uid, err := auth.Login(a.DB.DB, req.Account, req.Password)
	if err != nil {
		errOut(w, 401, "invalid_credentials", err.Error())
		return
	}
	tok, err := auth.CreateUserSession(a.DB.DB, uid)
	if err != nil {
		errOut(w, 500, "internal_error", "会话创建失败")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "token": tok, "user_id": uid})
}

// handleLogout 注销
func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("aqua_session"); err == nil {
		_, _ = a.DB.Exec("DELETE FROM sessions WHERE token=?", c.Value)
	}
	if tok := bearerToken(r); tok != "" {
		_, _ = a.DB.Exec("DELETE FROM sessions WHERE token=?", tok)
	}
	jsonOut(w, 200, map[string]any{"ok": true})
}

// handleMe 我的资料（含余额与价格组）
func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var u struct {
		ID          int64  `json:"id"`
		Username    string `json:"username"`
		Email       string `json:"email"`
		Balance     int64  `json:"balance_micro"`
		PriceGrpCall  string `json:"price_grp_call"`
		PriceGrpToken string `json:"price_grp_token"`
	}
	err := a.DB.QueryRow(
		"SELECT id, username, email, balance_micro, price_grp_call, price_grp_token FROM users WHERE id=?",
		actx.UserID).Scan(&u.ID, &u.Username, &u.Email, &u.Balance, &u.PriceGrpCall, &u.PriceGrpToken)
	if err != nil {
		errOut(w, 404, "not_found", "用户不存在")
		return
	}
	jsonOut(w, 200, u)
}

// handleFlows 我的计费流水（分页）
func (a *App) handleFlows(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	limit, offset := pageParams(r, 50)
	rows, err := a.DB.Query(
		`SELECT type, amount_micro, balance_after_micro, unit_price_micro, note, ts
		 FROM balance_flows WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?`,
		actx.UserID, limit, offset)
	if err != nil {
		errOut(w, 500, "internal_error", "流水查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var ty, note string
		var amount, after, unit, ts int64
		_ = rows.Scan(&ty, &amount, &after, &unit, &note, &ts)
		items = append(items, map[string]any{
			"type": ty, "amount_micro": amount, "balance_after_micro": after,
			"unit_price_micro": unit, "note": note, "ts": ts,
		})
	}
	jsonOut(w, 200, map[string]any{"items": items})
}

// handleCreateKey 签发 API 密钥
func (a *App) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var req struct {
		Name       string `json:"name"`
		BillingGrp string `json:"billing_grp"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	plain, err := auth.CreateAPIKey(a.DB.DB, actx.UserID, req.Name, config.NormalizeBillingGrp(req.BillingGrp))
	if err != nil {
		errOut(w, 500, "internal_error", "密钥创建失败")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "key": plain})
}

// handleListKeys 密钥列表（只显示前缀）
func (a *App) handleListKeys(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	rows, err := a.DB.Query(
		"SELECT id, key_prefix, name, revoked, created_ts FROM api_keys WHERE user_id=? ORDER BY created_ts DESC",
		actx.UserID)
	if err != nil {
		errOut(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, revoked, created int64
		var prefix, name string
		_ = rows.Scan(&id, &prefix, &name, &revoked, &created)
		items = append(items, map[string]any{
			"id": id, "key_prefix": prefix, "name": name, "revoked": revoked != 0, "created_ts": created,
		})
	}
	jsonOut(w, 200, map[string]any{"items": items})
}

// handleRevokeKey 吊销密钥
func (a *App) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	id := parseInt(r.PathValue("id"))
	if id <= 0 {
		errOut(w, 400, "bad_request", "参数错误")
		return
	}
	res, _ := a.DB.Exec("UPDATE api_keys SET revoked=1 WHERE id=? AND user_id=?", id, actx.UserID)
	if n, _ := res.RowsAffected(); n == 0 {
		errOut(w, 404, "not_found", "密钥不存在")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true})
}

// userGrpFor 用户在某线上的价格组
func (a *App) userGrpFor(uid int64, lineMode string) string {
	return billing.UserPriceGrp(a.DB.DB, uid, lineMode)
}

// pricingFor 生效价目（带保本防线）
func (a *App) pricingFor(model, grp string) *billing.PricingInfo {
	p, _ := billing.CurrentPricing(a.DB.DB, model, grp)
	return p
}

// pageParams 分页参数（limit 上限 200）
func pageParams(r *http.Request, defLimit int64) (limit, offset int64) {
	limit = defLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n := parseInt(v); n > 0 && n <= 200 {
			limit = n
		}
	}
	offset = 0
	if v := r.URL.Query().Get("offset"); v != "" {
		offset = parseInt(v)
		if offset < 0 {
			offset = 0
		}
	}
	return
}

func parseInt(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
