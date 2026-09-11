package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// emailRe 邮箱格式
var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// usernameRe 用户名：3-24 位字母数字下划线中文
var usernameRe = regexp.MustCompile(`^[\p{Han}A-Za-z0-9_]{3,24}$`)

// HashPassword 密码哈希（盐 + sha256 迭代；与 Rust 版 sha256(password) 兼容场景由调用方处理）
// 新版采用 scrypt 风格简化方案：sha256(salt + password) 迭代 10000 次
func HashPassword(password, salt string) string {
	h := sha256.Sum256([]byte(salt + password))
	for i := 0; i < 9999; i++ {
		h = sha256.Sum256(h[:])
	}
	return hex.EncodeToString(h[:])
}

// VerifyPassword 恒定时间比较
func VerifyPassword(password, salt, stored string) bool {
	got := HashPassword(password, salt)
	return subtle.ConstantTimeCompare([]byte(got), []byte(stored)) == 1
}

// NewSalt 生成盐
func NewSalt() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Register 注册（返回新用户 ID）
func Register(d *sql.DB, username, email, password string) (int64, error) {
	username = strings.TrimSpace(username)
	email = strings.ToLower(strings.TrimSpace(email))
	if !usernameRe.MatchString(username) {
		return 0, fmt.Errorf("用户名格式不正确（3-24 位，支持中文/字母/数字/下划线）")
	}
	if !emailRe.MatchString(email) {
		return 0, fmt.Errorf("邮箱格式不正确")
	}
	if len(password) < 8 {
		return 0, fmt.Errorf("密码至少 8 位")
	}
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM users WHERE email=?", email).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, fmt.Errorf("该邮箱已注册")
	}
	if err := d.QueryRow("SELECT COUNT(*) FROM users WHERE username=?", username).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, fmt.Errorf("用户名已被占用")
	}
	salt := NewSalt()
	hash := HashPassword(password, salt)
	// 存储格式：salt$hash（与旧格式 sha256(password) 区分：带盐的一定含 $）
	res, err := d.Exec(
		"INSERT INTO users (username, email, password_hash, status, created_ts) VALUES (?,?,?,1,?)",
		username, email, salt+"$"+hash, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Login 登录校验（username_or_email + password）
// 兼容 Rust 旧库：无 $ 的 password_hash 视为裸 sha256(password)。
func Login(d *sql.DB, account, password string) (int64, error) {
	account = strings.TrimSpace(account)
	var uid int64
	var stored string
	err := d.QueryRow("SELECT id, password_hash FROM users WHERE email=? OR username=? AND status=1", account, account).Scan(&uid, &stored)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("账号或密码错误")
	}
	if err != nil {
		return 0, err
	}
	if i := strings.IndexByte(stored, '$'); i > 0 {
		if !VerifyPassword(password, stored[:i], stored[i+1:]) {
			return 0, fmt.Errorf("账号或密码错误")
		}
	} else {
		// 旧格式兼容：裸 sha256
		h := sha256.Sum256([]byte(password))
		if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(h[:])), []byte(stored)) != 1 {
			return 0, fmt.Errorf("账号或密码错误")
		}
	}
	_, _ = d.Exec("UPDATE users SET last_login_ts=? WHERE id=?", time.Now().Unix(), uid)
	return uid, nil
}

// CreateAPIKey 为用户签发 API 密钥
func CreateAPIKey(d *sql.DB, uid int64, name string) (string, error) {
	plain := NewPlainKey()
	prefix := plain[:9] + "…" + plain[len(plain)-4:]
	_, err := d.Exec(
		"INSERT INTO api_keys (user_id, key_hash, key_prefix, name, revoked, created_ts, key_plain_enc) VALUES (?,?,?,?,0,?,'')",
		uid, Sha256Hex(plain), prefix, name, time.Now().Unix())
	return plain, err
}
