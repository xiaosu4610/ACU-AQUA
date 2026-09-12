package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// emailRe 邮箱格式
var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// usernameRe 用户名：3-24 位字母数字下划线中文
var usernameRe = regexp.MustCompile(`^[\p{Han}A-Za-z0-9_]{3,24}$`)

// pbkdf2 口径（与 Rust 版一致）：pbkdf2$120000$<盐32hex>$<哈希64hex>，HMAC-SHA256
const (
	pbkdf2Iters  = 120000
	pbkdf2KeyLen = 32
)

// pbkdf2Key PBKDF2-HMAC-SHA256（手写实现，无外部依赖）
func pbkdf2Key(password, salt []byte, iter, keyLen int) []byte {
	// U1 = HMAC(P, S || INT(1) 大端)
	h := hmac.New(sha256.New, password)
	h.Write(salt)
	h.Write([]byte{0, 0, 0, 1})
	u := h.Sum(nil)
	t := make([]byte, len(u))
	copy(t, u)
	for i := 1; i < iter; i++ {
		h.Reset()
		h.Write(u)
		u = h.Sum(nil)
		for j := range t {
			t[j] ^= u[j]
		}
	}
	return t[:keyLen]
}

// HashPasswordPbkdf2 生成 pbkdf2$iter$salt$hash 格式（新用户/改密用，与 Rust 库同构）
func HashPasswordPbkdf2(password string) string {
	sb := make([]byte, 16)
	_, _ = rand.Read(sb)
	saltHex := hex.EncodeToString(sb)
	sum := pbkdf2Key([]byte(password), []byte(saltHex), pbkdf2Iters, pbkdf2KeyLen)
	return fmt.Sprintf("pbkdf2$%d$%s$%s", pbkdf2Iters, saltHex, hex.EncodeToString(sum))
}

// VerifyPasswordPbkdf2 校验 pbkdf2$iter$saltHex$hashHex。
// 盐编码双解释兼容（Rust 逆向无法确认原样/hex 解码）：任一命中即通过。
func VerifyPasswordPbkdf2(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 || iter > 10_000_000 {
		return false
	}
	saltHex, expect := parts[2], parts[3]
	if len(expect) != pbkdf2KeyLen*2 {
		return false
	}
	// 解释 A：盐原样字符串
	if hex.EncodeToString(pbkdf2Key([]byte(password), []byte(saltHex), iter, pbkdf2KeyLen)) == expect {
		return true
	}
	// 解释 B：盐 hex 解码
	if sb, e := hex.DecodeString(saltHex); e == nil {
		if hex.EncodeToString(pbkdf2Key([]byte(password), sb, iter, pbkdf2KeyLen)) == expect {
			return true
		}
	}
	return false
}

// VerifyPasswordAny 密码校验分发（兼容三代格式）：
// 1. pbkdf2$iter$salt$hash —— Rust 版全量用户（981 个生产账号）
// 2. salt$hash —— Go 过渡期自建账号
// 3. 裸 sha256 hex —— 更早期遗留
func VerifyPasswordAny(password, stored string) bool {
	if strings.HasPrefix(stored, "pbkdf2$") {
		return VerifyPasswordPbkdf2(stored, password)
	}
	if i := strings.IndexByte(stored, '$'); i > 0 {
		return subtle.ConstantTimeCompare([]byte(HashPassword(password, stored[:i])), []byte(stored[i+1:])) == 1
	}
	h := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(h[:])), []byte(stored)) == 1
}

// HashPassword 密码哈希（盐 + sha256 迭代）
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

// Register 注册（返回新用户 ID；密码采用 pbkdf2 格式，与 Rust 库同构）
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
	res, err := d.Exec(
		"INSERT INTO users (username, email, password_hash, status, created_ts) VALUES (?,?,?,1,?)",
		username, email, HashPasswordPbkdf2(password), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetPassword 重置/修改密码（pbkdf2 格式）
func SetPassword(d *sql.DB, uid int64, password string) error {
	if len(password) < 8 {
		return fmt.Errorf("密码至少 8 位")
	}
	_, err := d.Exec("UPDATE users SET password_hash=? WHERE id=?", HashPasswordPbkdf2(password), uid)
	return err
}

// Login 登录校验（account = 用户名或邮箱）
// 密码校验三代格式兼容（见 VerifyPasswordAny）。
func Login(d *sql.DB, account, password string) (int64, error) {
	account = strings.TrimSpace(account)
	var uid int64
	var stored string
	// 注意括号：AND 优先级高于 OR，不加括号会漏 status 条件（曾致封禁用户可登录）
	err := d.QueryRow(
		"SELECT id, password_hash FROM users WHERE (email=? OR username=?) AND status=1",
		strings.ToLower(account), account).Scan(&uid, &stored)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("账号或密码错误")
	}
	if err != nil {
		return 0, err
	}
	if !VerifyPasswordAny(password, stored) {
		return 0, fmt.Errorf("账号或密码错误")
	}
	_, _ = d.Exec("UPDATE users SET last_login_ts=? WHERE id=?", time.Now().Unix(), uid)
	return uid, nil
}

// CreateAPIKey 为用户签发 API 密钥（key_plain_enc 存明文供控制台 reveal；
// 站长若需强安全可改为主密钥加密，见 data/master.key）
func CreateAPIKey(d *sql.DB, uid int64, name string) (string, error) {
	plain := NewPlainKey()
	prefix := plain[:9] + "…" + plain[len(plain)-4:]
	_, err := d.Exec(
		"INSERT INTO api_keys (user_id, key_hash, key_prefix, name, revoked, created_ts, key_plain_enc) VALUES (?,?,?,?,0,?,?)",
		uid, Sha256Hex(plain), prefix, name, time.Now().Unix(), plain)
	return plain, err
}
