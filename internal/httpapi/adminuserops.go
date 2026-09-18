// 用户管理扩展：封禁/解封、重置密码、价目组、强制下线、密钥管理、软删除。
// 高危操作二次密码 + 审计哈希链；封禁/注销经 users.status 即时联动鉴权（status=1 才放行）。
package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"acu-aqua/gateway/internal/auth"
)

// userOpReq 高危操作通用请求体（二次密码）
type userOpReq struct {
	ConfirmPassword string `json:"confirm_password"`
}

// checkUserOp 二次密码校验（失败已写审计与响应）。返回 false 时调用方直接 return。
func (a *App) checkUserOp(w http.ResponseWriter, action string, uid int64, r *http.Request, req *userOpReq) bool {
	ip := clientIP(r)
	if !adminPasswordOk(req.ConfirmPassword) {
		a.auditAppend(action+"_fail", uid, "二次密码错误", ip)
		errAdmin(w, 403, "invalid_credentials", "确认密码错误")
		return false
	}
	return true
}

// userExists 用户存在性（返回 username/email/status）
func (a *App) userExists(uid int64) (string, string, int, bool) {
	var username, email string
	var status int
	err := a.DB.QueryRow("SELECT username, email, status FROM users WHERE id=?", uid).Scan(&username, &email, &status)
	return username, email, status, err == nil
}

// —— POST /v1/admin/users/{uid}/status {status:0|1}（封禁/解封，二次密码）——
func (a *App) handleAdminUserStatus(w http.ResponseWriter, r *http.Request, uidStr string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	uid, ok := parseUID(w, uidStr)
	if !ok {
		return
	}
	var req struct {
		Status int    `json:"status"`
		Note   string `json:"note"`
		userOpReq
	}
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	if req.Status != 0 && req.Status != 1 {
		errAdmin(w, 400, "bad_request", "status 仅允许 0（封禁）/1（正常）")
		return
	}
	username, _, _, exists := a.userExists(uid)
	if !exists {
		errAdmin(w, 404, "not_found", "用户不存在")
		return
	}
	if !a.checkUserOp(w, "user_status", uid, r, &req.userOpReq) {
		return
	}
	if _, err := a.DB.Exec("UPDATE users SET status=? WHERE id=?", req.Status, uid); err != nil {
		errAdmin(w, 500, "internal_error", "更新失败")
		return
	}
	if req.Status == 0 {
		_, _ = a.DB.Exec("DELETE FROM sessions WHERE user_id=?", uid) // 封禁即踢下线
	}
	act := "user_unban"
	if req.Status == 0 {
		act = "user_ban"
	}
	a.auditAppend(act, uid, fmt.Sprintf("用户=%s %s", username, strings.TrimSpace(req.Note)), clientIP(r))
	jsonOut(w, 200, map[string]any{"ok": true, "status": req.Status})
}

// —— POST /v1/admin/users/{uid}/password（重置密码：随机新密码仅返回一次，二次密码）——
func (a *App) handleAdminUserPassword(w http.ResponseWriter, r *http.Request, uidStr string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	uid, ok := parseUID(w, uidStr)
	if !ok {
		return
	}
	var req userOpReq
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	username, _, _, exists := a.userExists(uid)
	if !exists {
		errAdmin(w, 404, "not_found", "用户不存在")
		return
	}
	if !a.checkUserOp(w, "user_password_reset", uid, r, &req) {
		return
	}
	newPwd := randomPassword()
	if err := auth.SetPassword(a.DB.DB, uid, newPwd); err != nil {
		errAdmin(w, 500, "internal_error", "重置失败")
		return
	}
	_, _ = a.DB.Exec("DELETE FROM sessions WHERE user_id=?", uid) // 全端下线
	a.auditAppend("user_password_reset", uid, "用户="+username+" 密码已重置并强制下线", clientIP(r))
	jsonOut(w, 200, map[string]any{"ok": true, "new_password": newPwd})
}

// —— POST /v1/admin/users/{uid}/price-grp {price_grp_call,price_grp_token}（二次密码）——
func (a *App) handleAdminUserPriceGrp(w http.ResponseWriter, r *http.Request, uidStr string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	uid, ok := parseUID(w, uidStr)
	if !ok {
		return
	}
	var req struct {
		PriceGrpCall  string `json:"price_grp_call"`
		PriceGrpToken string `json:"price_grp_token"`
		userOpReq
	}
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	norm := func(g string) (string, bool) {
		switch strings.TrimSpace(g) {
		case "normal", "vip", "agent", "": // agent=代理拿货价组（20260919 代理体系，仅按次线价目在用）
			return strings.TrimSpace(g), true
		}
		return "", false
	}
	call, okC := norm(req.PriceGrpCall)
	token, okT := norm(req.PriceGrpToken)
	if !okC || !okT {
		errAdmin(w, 400, "bad_request", "价目组仅允许 normal/vip/agent/空（维持原值）")
		return
	}
	username, _, _, exists := a.userExists(uid)
	if !exists {
		errAdmin(w, 404, "not_found", "用户不存在")
		return
	}
	if !a.checkUserOp(w, "user_pricegrp", uid, r, &req.userOpReq) {
		return
	}
	if call != "" {
		if _, err := a.DB.Exec("UPDATE users SET price_grp_call=? WHERE id=?", call, uid); err != nil {
			errAdmin(w, 500, "internal_error", "更新失败")
			return
		}
	}
	if token != "" {
		if _, err := a.DB.Exec("UPDATE users SET price_grp_token=? WHERE id=?", token, uid); err != nil {
			errAdmin(w, 500, "internal_error", "更新失败")
			return
		}
	}
	a.auditAppend("user_pricegrp", uid, fmt.Sprintf("用户=%s call=%s token=%s", username, call, token), clientIP(r))
	jsonOut(w, 200, map[string]any{"ok": true})
}

// —— POST /v1/admin/users/{uid}/kick 强制下线（清 sessions，二次密码）——
func (a *App) handleAdminUserKick(w http.ResponseWriter, r *http.Request, uidStr string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	uid, ok := parseUID(w, uidStr)
	if !ok {
		return
	}
	var req userOpReq
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	username, _, _, exists := a.userExists(uid)
	if !exists {
		errAdmin(w, 404, "not_found", "用户不存在")
		return
	}
	if !a.checkUserOp(w, "user_kick", uid, r, &req) {
		return
	}
	n, _ := a.DB.Exec("DELETE FROM sessions WHERE user_id=?", uid)
	rows, _ := n.RowsAffected()
	a.auditAppend("user_kick", uid, fmt.Sprintf("用户=%s 踢下线 %d 个会话", username, rows), clientIP(r))
	jsonOut(w, 200, map[string]any{"ok": true, "sessions_removed": rows})
}

// —— GET /v1/admin/users/{uid}/keys → 用户 API 密钥（脱敏 + 分组）——
func (a *App) handleAdminUserKeys(w http.ResponseWriter, r *http.Request, uidStr string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	uid, ok := parseUID(w, uidStr)
	if !ok {
		return
	}
	rows, err := a.DB.Query(
		`SELECT id, key_prefix, name, revoked, created_ts, COALESCE(billing_grp,'')
		 FROM api_keys WHERE user_id=? ORDER BY created_ts DESC`, uid)
	if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, revoked, created int64
		var prefix, name, grp string
		if rows.Scan(&id, &prefix, &name, &revoked, &created, &grp) == nil {
			items = append(items, map[string]any{
				"id": id, "prefix": prefix, "name": name, "revoked": revoked != 0,
				"created_ts": created, "billing_grp": grp,
			})
		}
	}
	jsonOut(w, 200, map[string]any{"uid": uid, "keys": items})
}

// —— POST /v1/admin/users/{uid}/keys/{kid}/revoke（吊销用户密钥，二次密码）——
func (a *App) handleAdminUserKeyRevoke(w http.ResponseWriter, r *http.Request, uidStr, kidStr string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	uid, ok := parseUID(w, uidStr)
	if !ok {
		return
	}
	kid, err := strconv.ParseInt(kidStr, 10, 64)
	if err != nil || kid <= 0 {
		errAdmin(w, 400, "bad_request", "密钥 ID 不合法")
		return
	}
	var req userOpReq
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	username, _, _, exists := a.userExists(uid)
	if !exists {
		errAdmin(w, 404, "not_found", "用户不存在")
		return
	}
	if !a.checkUserOp(w, "user_key_revoke", uid, r, &req) {
		return
	}
	n, _ := a.DB.Exec("UPDATE api_keys SET revoked=1 WHERE id=? AND user_id=?", kid, uid)
	if cnt, _ := n.RowsAffected(); cnt == 0 {
		errAdmin(w, 404, "not_found", "密钥不存在或不属于该用户")
		return
	}
	a.auditAppend("user_key_revoke", uid, fmt.Sprintf("用户=%s 密钥 id=%d 已吊销", username, kid), clientIP(r))
	jsonOut(w, 200, map[string]any{"ok": true})
}

// —— DELETE /v1/admin/users/{uid}（软删除：注销 + 邮箱/用户名释放 + 密钥吊销 + 下线；二次密码）——
func (a *App) handleAdminUserDelete(w http.ResponseWriter, r *http.Request, uidStr string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	uid, ok := parseUID(w, uidStr)
	if !ok {
		return
	}
	var req struct {
		ConfirmEmail string `json:"confirm_email"` // 输入用户邮箱二次确认
		userOpReq
	}
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	username, email, status, exists := a.userExists(uid)
	if !exists {
		errAdmin(w, 404, "not_found", "用户不存在")
		return
	}
	if status == 99 {
		errAdmin(w, 400, "bad_request", "用户已注销")
		return
	}
	if strings.TrimSpace(req.ConfirmEmail) != email {
		errAdmin(w, 400, "bad_request", "确认邮箱与用户邮箱不一致")
		return
	}
	if !a.checkUserOp(w, "user_delete", uid, r, &req.userOpReq) {
		return
	}
	// 软删除：标记注销 + 标识符加后缀释放（账单/流水/审计完整保留）
	if _, err := a.DB.Exec(
		"UPDATE users SET status=99, email=?, username=? WHERE id=?",
		fmt.Sprintf("%s.deleted.%d", email, uid), fmt.Sprintf("%s.deleted.%d", username, uid), uid); err != nil {
		errAdmin(w, 500, "internal_error", "删除失败")
		return
	}
	_, _ = a.DB.Exec("UPDATE api_keys SET revoked=1 WHERE user_id=?", uid)
	_, _ = a.DB.Exec("DELETE FROM sessions WHERE user_id=?", uid)
	a.auditAppend("user_delete", uid, fmt.Sprintf("用户=%s（%s）已注销（软删除，账单保留）", username, email), clientIP(r))
	jsonOut(w, 200, map[string]any{"ok": true})
}

// parseUID 路径参数 uid 解析
func parseUID(w http.ResponseWriter, uidStr string) (int64, bool) {
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil || uid <= 0 {
		errAdmin(w, 400, "bad_request", "用户 ID 不合法")
		return 0, false
	}
	return uid, true
}

// randomPassword 管理员重置密码用随机口令（16 位无歧义字符集）
func randomPassword() string {
	const chars = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	out := make([]byte, 16)
	for i := range b {
		out[i] = chars[int(b[i])%len(chars)]
	}
	return hex.EncodeToString([]byte{}) + string(out)
}
