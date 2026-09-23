// Package auth 三路鉴权：用户会话(cookie/token) / API 密钥(sk-) / 管理员(adm_)。
// 密钥格式与 Rust 版完全一致：sha256 哈希落库，现有生产 Key 无缝兼容。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"
)

// Ctx 鉴权上下文
type Ctx struct {
	UserID  int64
	Via     string // session | key | admin
	KeyHash string // sk- 密钥认证时为密钥哈希（落 requests.key_hash 用），其余为空
	KeyGrp  string // sk- 密钥的计费分组：''（未分组旧密钥）| per_call | per_token（统一前缀路由用）
	KeyID   int64  // sk- 密钥行 id（密钥级配额/有效期判定用；非密钥认证为 0）
	// KeyExpired 密钥已过期（仅在 KeyExpiredStrict 模式下返回非 nil Ctx，供调用方区分错误码）
	KeyExpired bool
}

type ctxKey struct{}

// SessionTTL 会话有效期
const SessionTTL = 30 * 86400

// Sha256Hex 小写十六进制哈希
func Sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// NewPlainKey 生成 sk- 开头的 API 密钥（24 字节 CSPRNG）
func NewPlainKey() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "sk-" + hex.EncodeToString(b)
}

// NewSessionToken 生成用户会话令牌（sess_ + 64 hex，与 Rust 版 require_session 校验规则一致）
func NewSessionToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "sess_" + hex.EncodeToString(b)
}

// Authenticate 从请求中解析凭证 → 鉴权。
// 返回 nil 表示未认证（调用方按需 401）。
func Authenticate(d *sql.DB, r *http.Request) *Ctx {
	cred := extractCredential(r)
	if cred == "" {
		return nil
	}
	now := time.Now().Unix()
	// 管理员令牌
	if strings.HasPrefix(cred, "adm_") {
		var lastSeen int64
		err := d.QueryRow("SELECT last_seen_ts FROM admin_sessions WHERE token=?", cred).Scan(&lastSeen)
		if err == nil && now-lastSeen < 8*3600 {
			_, _ = d.Exec("UPDATE admin_sessions SET last_seen_ts=? WHERE token=?", now, cred)
			return &Ctx{UserID: 0, Via: "admin"}
		}
		return nil
	}
	// API 密钥（sk-）
	var uid int64
	var status int
	var keyGrp string
	var keyID, expiresAt int64
	kh := Sha256Hex(cred)
	err := d.QueryRow("SELECT id, user_id, COALESCE(billing_grp,''), COALESCE(expires_at,0), (SELECT status FROM users WHERE id=api_keys.user_id) FROM api_keys WHERE key_hash=? AND revoked=0", kh).Scan(&keyID, &uid, &keyGrp, &expiresAt, &status)
	if err == nil && status == 1 {
		// 密钥有效期（P4）：已过期返回带 KeyExpired 标记的 Ctx，调用方按 key_expired 口径拒绝
		// （与 invalid_api_key 区分，便于下游排查"是密钥过期还是密钥错"）
		if expiresAt > 0 && now >= expiresAt {
			return &Ctx{UserID: uid, Via: "key", KeyHash: kh, KeyGrp: keyGrp, KeyID: keyID, KeyExpired: true}
		}
		return &Ctx{UserID: uid, Via: "key", KeyHash: kh, KeyGrp: keyGrp, KeyID: keyID}
	}
	if err != nil && err != sql.ErrNoRows {
		// DB 瞬时故障（busy 超时/IO 错）不能伪装成"密钥不存在"——留痕便于诊断
		// （20260919：单连接 + WAL 下偶发抖动曾表现为莫名 401）
		log.Printf("[auth] 密钥查询失败(非不存在): %v", err)
	}
	// 用户会话令牌
	err = d.QueryRow("SELECT user_id FROM sessions WHERE token=? AND last_seen_ts > ?", cred, now-SessionTTL).Scan(&uid)
	if err == nil {
		var st int
		if d.QueryRow("SELECT status FROM users WHERE id=?", uid).Scan(&st) == nil && st == 1 {
			_, _ = d.Exec("UPDATE sessions SET last_seen_ts=? WHERE token=?", now, cred)
			return &Ctx{UserID: uid, Via: "session"}
		}
	}
	return nil
}

// WithCtx 注入鉴权上下文
func WithCtx(ctx context.Context, c *Ctx) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// FromCtx 取出鉴权上下文
func FromCtx(ctx context.Context) *Ctx {
	if c, ok := ctx.Value(ctxKey{}).(*Ctx); ok {
		return c
	}
	return nil
}

// extractCredential 提取凭证：Authorization Bearer / X-Api-Key / cookie session
func extractCredential(r *http.Request) string {
	h := r.Header.Get("Authorization")
	// scheme 大小写不敏感（RFC 7235）：小写 "bearer xxx" 此前被拒为 401，
	// 部分 SDK/网关（如某些 one-api 分发端）确实发小写 scheme
	if len(h) >= 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if k := r.Header.Get("X-Api-Key"); k != "" {
		return strings.TrimSpace(k)
	}
	if c, err := r.Cookie("aqua_session"); err == nil {
		return c.Value
	}
	return ""
}

// CreateUserSession 建会话
func CreateUserSession(d *sql.DB, uid int64) (string, error) {
	tok := NewSessionToken()
	now := time.Now().Unix()
	_, err := d.Exec("INSERT INTO sessions (token, user_id, created_ts, last_seen_ts) VALUES (?,?,?,?)", tok, uid, now, now)
	return tok, err
}

// CreateAdminSession 建管理员会话
func CreateAdminSession(d *sql.DB) (string, error) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := "adm_" + hex.EncodeToString(b)
	now := time.Now().Unix()
	_, err := d.Exec("INSERT INTO admin_sessions (token, created_ts, last_seen_ts) VALUES (?,?,?)", tok, now, now)
	return tok, err
}
