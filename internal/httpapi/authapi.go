// authapi.go 用户认证端点族（/v1/auth/*）。
// 响应形状按前端 SPA 消费契约实现（login → {token}，me → 用户资料，avatar → 二进制）。
package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"acu-aqua/gateway/internal/auth"
)

// emailCodeTTL 验证码有效期
const emailCodeTTL = 10 * 60

// maxCodeFails 验证码最大试错次数（防暴力）
const maxCodeFails = 5

// authLogin POST /v1/auth/login {account, password}（account = 用户名或邮箱）
func (a *App) authLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Account  string `json:"account"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Account == "" || req.Password == "" {
		errOut(w, 400, "bad_request", "请输入账号与密码")
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
	jsonOut(w, 200, map[string]any{"ok": true, "token": tok, "user": a.userBrief(uid)})
}

// authLogout POST /v1/auth/logout
func (a *App) authLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("aqua_session"); err == nil {
		_, _ = a.DB.Exec("DELETE FROM sessions WHERE token=?", c.Value)
	}
	if tok := bearerToken(r); tok != "" {
		_, _ = a.DB.Exec("DELETE FROM sessions WHERE token=?", tok)
	}
	jsonOut(w, 200, map[string]any{"ok": true})
}

// authMe GET /v1/auth/me（user_json + key_count，与 Rust 版形状一致）
func (a *App) authMe(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil || actx.Via != "session" {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	u := a.userBrief(actx.UserID)
	if u == nil {
		errOut(w, 401, "unauthorized", "会话已失效")
		return
	}
	var keyCount int64
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM api_keys WHERE user_id=? AND revoked=0", actx.UserID).Scan(&keyCount)
	jsonOut(w, 200, map[string]any{
		"id": u["id"], "username": u["username"], "email": u["email"],
		"avatar_ext": u["avatar_ext"], "created_ts": u["created_ts"],
		"key_count": keyCount,
	})
}

// userBrief Rust 版 user_json 同构：{id, username, email, avatar_ext, created_ts}
func (a *App) userBrief(uid int64) map[string]any {
	var id, created int64
	var username, email, avatarExt string
	err := a.DB.QueryRow(
		"SELECT id, username, email, avatar_ext, created_ts FROM users WHERE id=? AND status=1", uid).
		Scan(&id, &username, &email, &avatarExt, &created)
	if err != nil {
		return nil
	}
	return map[string]any{
		"id": id, "username": username, "email": email,
		"avatar_ext": avatarExt, "created_ts": created,
	}
}

// authSendCode POST /v1/auth/send-code {email}（注册验证码）
func (a *App) authSendCode(w http.ResponseWriter, r *http.Request) {
	a.sendCodeFor(w, r, "register")
}

// authForgot POST /v1/auth/forgot {email}（找回密码验证码）
func (a *App) authForgot(w http.ResponseWriter, r *http.Request) {
	a.sendCodeFor(w, r, "reset")
}

// sendCodeFor 生成并发送验证码（purpose: register | reset）
func (a *App) sendCodeFor(w http.ResponseWriter, r *http.Request, purpose string) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		errOut(w, 400, "bad_request", "请输入邮箱")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	var n int
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM users WHERE email=?", email).Scan(&n)
	if purpose == "register" && n > 0 {
		errOut(w, 400, "email_taken", "该邮箱已注册")
		return
	}
	if purpose == "reset" && n == 0 {
		// 反枚举：不暴露邮箱是否注册
		jsonOut(w, 200, map[string]any{"ok": true, "message": "重置验证码已发送"})
		return
	}
	if a.Mail == nil && (a.MailPool == nil || a.MailPool.activeCount(a.DB.DB) == 0) {
		errOut(w, 500, "service_unavailable", "邮件服务未配置，请联系站长")
		return
	}
	code := genCode()
	now := time.Now().Unix()
	_, _ = a.DB.Exec(
		`INSERT INTO email_codes (email, purpose, code, fails, expire_ts) VALUES (?,?,?,0,?)
		 ON CONFLICT(email, purpose) DO UPDATE SET code=excluded.code, fails=0, expire_ts=excluded.expire_ts`,
		email, purpose, code, now+emailCodeTTL)
	subject := "AQUA 注册验证码"
	body := "您的验证码是：" + code + "，10 分钟内有效。如非本人操作请忽略本邮件。"
	if purpose == "reset" {
		subject = "AQUA 密码找回验证码"
		body = "您正在重置密码，验证码：" + code + "，10 分钟内有效。如非本人操作请立即检查账号安全。"
	}
	// 统一发信入口：微软池主线路（连败→阿里云试探→回池；单封失败阿里云兜底；全灭阿里云接管）
	channel, sender, err := a.SendAny(email, subject, body)
	if err != nil {
		log.Printf("[mail] 发送失败 to=%s err=%v", email, err)
		errOut(w, 500, "internal_error", "验证码发送失败，请稍后重试")
		return
	}
	_, _ = a.DB.Exec("UPDATE email_codes SET sender=?, channel=? WHERE email=? AND purpose=?",
		sender, channel, email, purpose)
	log.Printf("[mail] 验证码已发 to=%s channel=%s sender=%s purpose=%s", email, channel, sender, purpose)
	jsonOut(w, 200, map[string]any{"ok": true, "message": "验证码已发送"})
}

// verifyCode 校验验证码（错一次计一次失败，超过上限作废）
func (a *App) verifyCode(email, purpose, code string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	var stored string
	var fails int
	var expire int64
	err := a.DB.QueryRow("SELECT code, fails, expire_ts FROM email_codes WHERE email=? AND purpose=?", email, purpose).
		Scan(&stored, &fails, &expire)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expire || fails >= maxCodeFails {
		return false
	}
	if stored != strings.TrimSpace(code) {
		_, _ = a.DB.Exec("UPDATE email_codes SET fails=fails+1 WHERE email=? AND purpose=?", email, purpose)
		return false
	}
	_, _ = a.DB.Exec("DELETE FROM email_codes WHERE email=? AND purpose=?", email, purpose)
	return true
}

// authRegister POST /v1/auth/register {email, code, username, password, invite_code?}
func (a *App) authRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email      string `json:"email"`
		Code       string `json:"code"`
		Username   string `json:"username"`
		Password   string `json:"password"`
		InviteCode string `json:"invite_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errOut(w, 400, "bad_request", "请求体格式错误")
		return
	}
	if !a.verifyCode(req.Email, "register", req.Code) {
		errOut(w, 400, "invalid_code", "验证码错误或已过期")
		return
	}
	uid, err := auth.Register(a.DB.DB, req.Username, req.Email, req.Password)
	if err != nil {
		errOut(w, 400, "bad_request", err.Error())
		return
	}
	// 邀请关系绑定（静默失败不阻断注册；防刷闸在 inviteBind 内）
	if strings.TrimSpace(req.InviteCode) != "" {
		a.inviteBind(uid, req.InviteCode, clientIP(r))
	}
	// 注册即发默认密钥（Rust 版行为：用户注册后可立即调用 API；未分组，走默认计费分组）
	keyPlain, err := auth.CreateAPIKey(a.DB.DB, uid, "默认密钥", "")
	if err != nil {
		errOut(w, 500, "internal_error", "密钥创建失败")
		return
	}
	tok, err := auth.CreateUserSession(a.DB.DB, uid)
	if err != nil {
		errOut(w, 500, "internal_error", "会话创建失败")
		return
	}
	jsonOut(w, 200, map[string]any{
		"ok": true, "token": tok, "api_key": keyPlain,
		"api_key_prefix": keyPlain[:9] + "…" + keyPlain[len(keyPlain)-4:],
		"user":           a.userBrief(uid),
		"message":        "注册成功！密钥已存入你的控制台，可随时在密钥列表复制",
	})
}

// authReset POST /v1/auth/reset {email, code, password}
func (a *App) authReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errOut(w, 400, "bad_request", "请求体格式错误")
		return
	}
	if !a.verifyCode(req.Email, "reset", req.Code) {
		errOut(w, 400, "invalid_code", "验证码错误或已过期")
		return
	}
	var uid int64
	if err := a.DB.QueryRow("SELECT id FROM users WHERE email=?", strings.ToLower(strings.TrimSpace(req.Email))).Scan(&uid); err != nil {
		errOut(w, 400, "bad_request", "账号不存在")
		return
	}
	if err := auth.SetPassword(a.DB.DB, uid, req.Password); err != nil {
		errOut(w, 400, "bad_request", err.Error())
		return
	}
	// 密码重置后清空该用户全部会话（安全）
	_, _ = a.DB.Exec("DELETE FROM sessions WHERE user_id=?", uid)
	jsonOut(w, 200, map[string]any{"ok": true, "message": "密码已重置"})
}

// authPassword POST /v1/auth/password {old_password, new_password}（登录态改密）
func (a *App) authPassword(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errOut(w, 400, "bad_request", "请求体格式错误")
		return
	}
	var stored string
	if err := a.DB.QueryRow("SELECT password_hash FROM users WHERE id=?", actx.UserID).Scan(&stored); err != nil {
		errOut(w, 404, "not_found", "用户不存在")
		return
	}
	if !auth.VerifyPasswordAny(req.OldPassword, stored) {
		errOut(w, 400, "invalid_credentials", "原密码错误")
		return
	}
	if err := auth.SetPassword(a.DB.DB, actx.UserID, req.NewPassword); err != nil {
		errOut(w, 400, "bad_request", err.Error())
		return
	}
	// 改密后全端踢下线（Rust 版行为）
	_, _ = a.DB.Exec("DELETE FROM sessions WHERE user_id=?", actx.UserID)
	jsonOut(w, 200, map[string]any{"ok": true, "message": "密码已修改，所有会话已注销，请重新登录"})
}

// authProfile POST /v1/auth/profile {username}（改昵称）
func (a *App) authProfile(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
		errOut(w, 400, "bad_request", "请输入新昵称")
		return
	}
	var n int
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM users WHERE username=? AND id != ?", req.Username, actx.UserID).Scan(&n)
	if n > 0 {
		errOut(w, 400, "username_taken", "用户名已被占用")
		return
	}
	if _, err := a.DB.Exec("UPDATE users SET username=? WHERE id=?", req.Username, actx.UserID); err != nil {
		errOut(w, 500, "internal_error", "保存失败")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "message": "已保存"})
}

// authAvatar GET /v1/auth/avatar/{id}（头像文件 <avatars>/<uid>.<ext>）
func (a *App) authAvatar(w http.ResponseWriter, r *http.Request) {
	id := parseInt(r.PathValue("id"))
	if id <= 0 {
		errOut(w, 400, "bad_request", "参数错误")
		return
	}
	var ext string
	if err := a.DB.QueryRow("SELECT avatar_ext FROM users WHERE id=?", id).Scan(&ext); err != nil || ext == "" {
		errOut(w, 404, "not_found", "无头像")
		return
	}
	// 路径清洗：ext 仅允许字母数字（防穿越）
	if strings.Trim(ext, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" {
		errOut(w, 404, "not_found", "无头像")
		return
	}
	path := filepath.Join(a.AvatarsDir, fmt.Sprintf("%d.%s", id, ext))
	f, err := os.Open(path)
	if err != nil {
		errOut(w, 404, "not_found", "无头像")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mime.TypeByExtension("."+ext))
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = io.Copy(w, f)
}

// genCode 6 位数字验证码
func genCode() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	n := (uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])) % 1000000
	return fmt.Sprintf("%06d", n)
}

// authAvatarUpload POST /v1/my/avatar（PNG/JPG/WebP，2MB，魔数校验，与 Rust 版行为一致）
func (a *App) authAvatarUpload(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil || actx.Via != "session" {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	var ext string
	switch {
	case strings.Contains(ct, "jpeg") || strings.Contains(ct, "jpg"):
		ext = "jpg"
	case strings.Contains(ct, "png"):
		ext = "png"
	case strings.Contains(ct, "webp"):
		ext = "webp"
	default:
		errOut(w, 400, "bad_request", "头像仅支持 PNG / JPG / WebP 格式")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024+1))
	if err != nil || len(body) > 2*1024*1024 {
		errOut(w, 413, "too_large", "头像最大 2MB")
		return
	}
	// 魔数校验，防伪装
	var magicOK bool
	switch ext {
	case "jpg":
		magicOK = len(body) >= 3 && body[0] == 0xFF && body[1] == 0xD8 && body[2] == 0xFF
	case "png":
		magicOK = len(body) >= 4 && body[0] == 0x89 && body[1] == 'P' && body[2] == 'N' && body[3] == 'G'
	case "webp":
		magicOK = len(body) > 12 && string(body[0:4]) == "RIFF" && string(body[8:12]) == "WEBP"
	}
	if !magicOK {
		errOut(w, 400, "bad_request", "文件内容与格式不符")
		return
	}
	_ = os.MkdirAll(a.AvatarsDir, 0o755)
	// 清同 ID 旧格式再写新文件
	for _, e := range []string{"jpg", "png", "webp"} {
		_ = os.Remove(filepath.Join(a.AvatarsDir, fmt.Sprintf("%d.%s", actx.UserID, e)))
	}
	if err := os.WriteFile(filepath.Join(a.AvatarsDir, fmt.Sprintf("%d.%s", actx.UserID, ext)), body, 0o644); err != nil {
		errOut(w, 500, "internal_error", "头像保存失败，请重试")
		return
	}
	if _, err := a.DB.Exec("UPDATE users SET avatar_ext=? WHERE id=?", ext, actx.UserID); err != nil {
		errOut(w, 500, "internal_error", "头像保存失败，请重试")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "avatar_ext": ext})
}
