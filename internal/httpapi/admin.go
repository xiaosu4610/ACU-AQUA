// 管理后台端点族 /v1/admin/*（Go 原生实现，响应形状对齐 Rust 版 admin.rs）。
// 安全红线：
//   - 单密码模型（无用户名），密码 pbkdf2 哈希存 env，明文零落盘
//   - adm_ 令牌独立会话表，与用户体系完全隔离
//   - 高危操作（动钱）必须二次输入密码 + 审计留痕（SHA-256 哈希链）
package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"acu-aqua/gateway/internal/billing"
)

// admin 会话有效期（Rust 口径：2 小时绝对过期 + 30 分钟空闲过期）
const (
	adminSessionTTL = 2 * 3600
	adminIdleTTL    = 30 * 60
)

// —— 登录防爆破（内存态，单进程足够）——
type bruteState struct {
	fails     int
	lockUntil int64
}

var (
	bruteMu    sync.Mutex
	bruteIPs   = map[string]*bruteState{}
	globWinTs  int64
	globWinCnt int
)

// adminLoginGate 登录限速：全局每分钟 10 次；同 IP 5 次失败锁 10 分钟
func adminLoginGate(r *http.Request) (string, bool) {
	ip := clientIP(r)
	now := time.Now().Unix()
	bruteMu.Lock()
	defer bruteMu.Unlock()
	// 先查 IP 锁：锁定中直接拒绝且不消耗全局名额（防被锁 IP 的请求刷爆全局 10/min 池，殃及正常登录）
	if st, ok := bruteIPs[ip]; ok && now < st.lockUntil {
		return ip, false
	}
	if globWinTs != now/60 {
		globWinTs = now / 60
		globWinCnt = 0
	}
	globWinCnt++
	if globWinCnt > 10 {
		return ip, false
	}
	return ip, true
}

// adminLoginFail 记录一次失败（同 IP 第 5 次锁 10 分钟）
func adminLoginFail(ip string) {
	now := time.Now().Unix()
	bruteMu.Lock()
	defer bruteMu.Unlock()
	st := bruteIPs[ip]
	if st == nil {
		st = &bruteState{}
		bruteIPs[ip] = st
	}
	st.fails++
	if st.fails >= 5 {
		st.lockUntil = now + 600
		st.fails = 0
	}
}

// adminLoginOk 登录成功：清该 IP 失败计数
func adminLoginOk(ip string) {
	bruteMu.Lock()
	defer bruteMu.Unlock()
	delete(bruteIPs, ip)
}

// —— 密码校验（与 Rust 版格式兼容）——

// pbkdf2SHA256 PBKDF2-HMAC-SHA256（标准库实现，零第三方依赖）
func pbkdf2SHA256(password, salt []byte, rounds, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	U := make([]byte, hashLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf[:4])
		dk = prf.Sum(dk)
		t := dk[len(dk)-hashLen:]
		copy(U, t)
		for n := 2; n <= rounds; n++ {
			prf.Reset()
			prf.Write(U)
			U = U[:0]
			U = prf.Sum(U)
			for x := range U {
				t[x] ^= U[x]
			}
		}
	}
	return dk[:keyLen]
}

// verifyAdminPassword 校验管理密码：
//   - 存储格式 A：pbkdf2$<rounds>$<salt_hex>$<hash_hex>（与 Rust/用户密码同构）
//   - 存储格式 B：64 位裸 hex（历史部署的 sha256(密码) 口径，兼容保留）
func verifyAdminPassword(input, stored string) bool {
	stored = strings.ReplaceAll(stored, "%24", "$") // systemd EnvironmentFile 会吞 $，Rust 同款转义
	if parts := strings.Split(stored, "$"); len(parts) == 4 && parts[0] == "pbkdf2" {
		rounds, err1 := strconv.Atoi(parts[1])
		salt, err2 := hex.DecodeString(parts[2])
		expect, err3 := hex.DecodeString(parts[3])
		if err1 != nil || err2 != nil || err3 != nil || rounds <= 0 || len(salt) == 0 || len(expect) == 0 {
			return false
		}
		out := pbkdf2SHA256([]byte(input), salt, rounds, len(expect))
		return subtle.ConstantTimeCompare(out, expect) == 1
	}
	if len(stored) == 64 {
		sum := sha256.Sum256([]byte(input))
		got := hex.EncodeToString(sum[:])
		return subtle.ConstantTimeCompare([]byte(got), []byte(strings.ToLower(stored))) == 1
	}
	return false
}

func adminPasswordHashEnv() string {
	return strings.TrimSpace(os.Getenv("AQUA_ADMIN_PASSWORD_HASH"))
}

// adminPasswordOk 密码是否通过
func adminPasswordOk(input string) bool {
	h := adminPasswordHashEnv()
	if h == "" {
		return false
	}
	return verifyAdminPassword(input, h)
}

// —— 审计（SHA-256 哈希链）——

// auditAppend 写入审计（哈希链：self = sha256("action|target|detail|ip|ts|prev")）。
// SELECT prev 与 INSERT 必须同事务：并发写入各自读到同一 prev 会导致哈希链分叉。
func (a *App) auditAppend(action string, target int64, detail, ip string) {
	ts := time.Now().Unix()
	tx, err := a.DB.Begin()
	if err != nil {
		log.Printf("[admin] 审计写入失败 action=%s: %v", action, err)
		return
	}
	var prev string
	if err := tx.QueryRow("SELECT self_hash FROM admin_audit ORDER BY id DESC LIMIT 1").Scan(&prev); err != nil || prev == "" {
		prev = "GENESIS"
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%s|%d|%s", action, target, detail, ip, ts, prev)))
	self := hex.EncodeToString(sum[:])
	if _, err := tx.Exec(
		"INSERT INTO admin_audit (action, target, detail, ip, prev_hash, self_hash, ts) VALUES (?,?,?,?,?,?,?)",
		action, fmt.Sprintf("%d", target), detail, ip, prev, self, ts); err != nil {
		_ = tx.Rollback()
		log.Printf("[admin] 审计写入失败 action=%s: %v", action, err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("[admin] 审计提交失败 action=%s: %v", action, err)
	}
}

// requireAdmin 管理会话守卫：adm_ 前缀 + 64hex 格式校验（不合规不查库）→ 双过期校验 → 滑动续期。
// 返回 token；失败时已写好 401/503 响应并返回 false。
func (a *App) requireAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	cred := adminCredential(r)
	if cred == "" {
		errOut(w, 401, "unauthorized", "请先登录管理控制台")
		return "", false
	}
	rest, ok := strings.CutPrefix(cred, "adm_")
	if !ok || len(rest) != 64 || !isHex(rest) {
		errOut(w, 401, "unauthorized", "管理会话无效")
		return "", false
	}
	var created, lastSeen int64
	err := a.DB.QueryRow("SELECT created_ts, last_seen_ts FROM admin_sessions WHERE token=?", cred).Scan(&created, &lastSeen)
	if err == sql.ErrNoRows {
		errOut(w, 401, "unauthorized", "管理会话已失效，请重新登录")
		return "", false
	}
	if err != nil {
		errOut(w, 503, "service_unavailable", "数据库繁忙，请稍后重试")
		return "", false
	}
	now := time.Now().Unix()
	if now-created > adminSessionTTL || now-lastSeen > adminIdleTTL {
		_, _ = a.DB.Exec("DELETE FROM admin_sessions WHERE token=?", cred)
		errOut(w, 401, "unauthorized", "管理会话已过期，请重新登录")
		return "", false
	}
	_, _ = a.DB.Exec("UPDATE admin_sessions SET last_seen_ts=? WHERE token=?", now, cred)
	return cred, true
}

// adminCredential 提取管理凭证（Bearer / x-api-key）
func adminCredential(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if k, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(k)
		}
		if k, ok := strings.CutPrefix(h, "bearer "); ok {
			return strings.TrimSpace(k)
		}
	}
	if k := r.Header.Get("X-Api-Key"); k != "" {
		return strings.TrimSpace(k)
	}
	return ""
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// errAdmin 与 errOut 同形（保留独立名以便语义区分）
func errAdmin(w http.ResponseWriter, code int, ecode, msg string) {
	errOut(w, code, ecode, msg)
}

// adminBody 读取并解析 JSON 请求体（限长）
func adminBody(r *http.Request, limit int64, v any) error {
	body, err := readLimited(r, limit)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}

// readLimited 读请求体（上限保护）
func readLimited(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, limit))
}

// cryptoRandRead CSPRNG 封装
func cryptoRandRead(b []byte) (int, error) {
	return rand.Read(b)
}

// —— POST /v1/admin/login ——
func (a *App) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if adminPasswordHashEnv() == "" {
		errAdmin(w, 503, "admin_disabled", "管理控制台未启用（未配置 AQUA_ADMIN_PASSWORD_HASH）")
		return
	}
	ip, ok := adminLoginGate(r)
	if !ok {
		errAdmin(w, 429, "rate_limit_exceeded", "尝试过于频繁，请稍后再试")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	if req.Password == "" || !adminPasswordOk(req.Password) {
		time.Sleep(500 * time.Millisecond) // 拖慢猜解
		adminLoginFail(ip)
		a.auditAppend("login_fail", 0, "", ip)
		errAdmin(w, 401, "invalid_credentials", "密码错误")
		return
	}
	adminLoginOk(ip)
	// 单点登录：踢掉全部旧会话并原子建新会话（同事务，防并发登录互踢竞态）
	tx, err := a.DB.Begin()
	if err != nil {
		errAdmin(w, 500, "internal_error", "会话创建失败")
		return
	}
	if _, err := tx.Exec("DELETE FROM admin_sessions"); err != nil {
		_ = tx.Rollback()
		errAdmin(w, 500, "internal_error", "会话创建失败")
		return
	}
	tok, err := createAdminSessionFull(tx)
	if err != nil {
		_ = tx.Rollback()
		errAdmin(w, 500, "internal_error", "会话创建失败")
		return
	}
	if err := tx.Commit(); err != nil {
		errAdmin(w, 500, "internal_error", "会话创建失败")
		return
	}
	a.auditAppend("login_ok", 0, "", ip)
	// 上次成功登录（倒数第二条 login_ok）
	var last struct {
		ts int64
		ip string
	}
	_ = a.DB.QueryRow(
		"SELECT ts, ip FROM admin_audit WHERE action='login_ok' ORDER BY id DESC LIMIT 1 OFFSET 1",
	).Scan(&last.ts, &last.ip)
	jsonOut(w, 200, map[string]any{
		"token": tok,
		"last_login": map[string]any{
			"ts": last.ts, "ip": last.ip,
		},
	})
}

// createAdminSessionFull 建管理员会话（复用 auth 包令牌格式：adm_ + 64hex）
// d 传 *sql.DB 或 *sql.Tx（单点登录踢旧会话须与新会话同事务）
func createAdminSessionFull(d interface {
	Exec(query string, args ...any) (sql.Result, error)
}) (string, error) {
	b := make([]byte, 32)
	if _, err := cryptoRandRead(b); err != nil {
		return "", err
	}
	tok := "adm_" + hex.EncodeToString(b)
	now := time.Now().Unix()
	_, err := d.Exec("INSERT INTO admin_sessions (token, created_ts, last_seen_ts) VALUES (?,?,?)", tok, now, now)
	return tok, err
}

// —— POST /v1/admin/logout ——
func (a *App) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	tok, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	_, _ = a.DB.Exec("DELETE FROM admin_sessions WHERE token=?", tok)
	a.auditAppend("logout", 0, "", clientIP(r))
	jsonOut(w, 200, map[string]any{"ok": true})
}

// —— GET /v1/admin/users?page&q ——
func (a *App) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := atoiDefault(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	const pageSize = 20
	where := "1=1"
	var args []any
	if q != "" {
		where = "(username LIKE ? OR email LIKE ? OR CAST(id AS TEXT)=?)"
		args = append(args, "%"+q+"%", "%"+q+"%", q)
	}
	var total int64
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM users WHERE "+where, args...).Scan(&total); err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	rows, err := a.DB.Query(
		`SELECT u.id, u.username, u.email, u.balance_micro, u.balance2_micro, u.status, u.created_ts, u.last_login_ts,
		        u.price_grp_call, u.price_grp_token,
		        COALESCE((SELECT SUM(amount_micro) FROM balance_flows f WHERE f.user_id=u.id AND f.type IN ('topup','prehold') AND f.amount_micro>0),0),
		        COALESCE((SELECT SUM(unit_price_micro) FROM balance_flows f WHERE f.user_id=u.id AND f.type='billed'),0),
		        COALESCE((SELECT SUM(amount_micro) FROM balance_flows f WHERE f.user_id=u.id AND f.type='refunded'),0),
		        COALESCE((SELECT COUNT(*) FROM requests rq WHERE rq.user_id=u.id),0)
		 FROM users u WHERE `+where+" ORDER BY u.id DESC LIMIT ? OFFSET ?",
		append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, balance, balance2, status, created, lastLogin, topup, cost, refund, calls int64
		var username, email, grpCall, grpToken string
		if rows.Scan(&id, &username, &email, &balance, &balance2, &status, &created, &lastLogin, &grpCall, &grpToken, &topup, &cost, &refund, &calls) == nil {
			items = append(items, map[string]any{
				"id": id, "username": username, "email": email,
				"balance_micro": balance, "balance2_micro": balance2, "status": status,
				"created_ts": created, "last_login_ts": lastLogin,
				"price_grp": "normal", "price_grp_call": grpCall, "price_grp_token": grpToken,
				"topup_micro": topup, "cost_micro": cost, "refund_micro": refund, "calls": calls,
			})
		}
	}
	jsonOut(w, 200, map[string]any{"page": page, "page_size": pageSize, "total": total, "q": q, "items": items})
}

// —— GET /v1/admin/users/{uid}?page ——
func (a *App) handleAdminUserDetail(w http.ResponseWriter, r *http.Request, uidStr string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil || uid <= 0 {
		errAdmin(w, 400, "bad_request", "用户 ID 不合法")
		return
	}
	page := atoiDefault(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	const pageSize = 20
	var username, email, grp, grpCall, grpToken string
	var balance, balance2, status, created, lastLogin int64
	err = a.DB.QueryRow(
		"SELECT username, email, balance_micro, balance2_micro, status, created_ts, last_login_ts, price_grp, price_grp_call, price_grp_token FROM users WHERE id=?", uid,
	).Scan(&username, &email, &balance, &balance2, &status, &created, &lastLogin, &grp, &grpCall, &grpToken)
	if err == sql.ErrNoRows {
		errAdmin(w, 404, "not_found", "用户不存在")
		return
	}
	if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	var topup, cost, refund int64
	var calls int64
	_ = a.DB.QueryRow(`SELECT
			COALESCE(SUM(CASE WHEN type IN ('topup','prehold') AND amount_micro>0 THEN amount_micro END),0),
			COALESCE(SUM(CASE WHEN type='billed' THEN unit_price_micro END),0),
			COALESCE(SUM(CASE WHEN type='refunded' THEN amount_micro END),0)
		 FROM balance_flows WHERE user_id=?`, uid).Scan(&topup, &cost, &refund)
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM requests WHERE user_id=?", uid).Scan(&calls)
	var flowTotal int64
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM balance_flows WHERE user_id=?", uid).Scan(&flowTotal)
	rows, err := a.DB.Query(
		`SELECT type, amount_micro, balance_before_micro, balance_after_micro, unit_price_micro, note, operator, ts,
		        COALESCE((SELECT model FROM requests rq WHERE rq.rowid=f.request_id),'')
		 FROM balance_flows f WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?`,
		uid, pageSize, (page-1)*pageSize)
	if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	flows := []map[string]any{}
	for rows.Next() {
		var fType, note, operator, model string
		var amount, before, after, unitPrice, ts int64
		if rows.Scan(&fType, &amount, &before, &after, &unitPrice, &note, &operator, &ts, &model) == nil {
			flows = append(flows, map[string]any{
				"type": fType, "amount_micro": amount,
				"balance_before_micro": before, "balance_after_micro": after,
				"unit_price_micro": unitPrice, "note": note, "operator": operator,
				"ts": ts, "model": model,
			})
		}
	}
	jsonOut(w, 200, map[string]any{
		"user": map[string]any{
			"id": uid, "username": username, "email": email,
			"balance_micro": balance, "balance2_micro": balance2, "status": status,
			"created_ts": created, "last_login_ts": lastLogin,
			"price_grp": grp, "price_grp_call": grpCall, "price_grp_token": grpToken,
		},
		"summary": map[string]any{
			"topup_micro": topup, "cost_micro": cost, "refund_micro": refund, "calls": calls,
		},
		"flows": map[string]any{
			"page": page, "page_size": pageSize, "total": flowTotal, "items": flows,
		},
	})
}

// —— POST /v1/admin/users/{uid}/balance ——（高危：二次密码）
func (a *App) handleAdminUserBalance(w http.ResponseWriter, r *http.Request, uidStr string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil || uid <= 0 {
		errAdmin(w, 400, "bad_request", "用户 ID 不合法")
		return
	}
	var req struct {
		AmountMicro     int64  `json:"amount_micro"`
		Note            string `json:"note"`
		ConfirmPassword string `json:"confirm_password"`
		Wallet          int    `json:"wallet"` // 1=主钱包（默认）2=2 号折扣钱包（20260924）
	}
	if err := adminBody(r, 8192, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	ip := clientIP(r)
	if req.AmountMicro == 0 {
		errAdmin(w, 400, "bad_request", "金额不能为 0")
		return
	}
	// 钱包白名单：未传（0）按主钱包兜底，兼容旧调用；显式传入只认 1/2
	if req.Wallet == 0 {
		req.Wallet = billing.WalletMain
	}
	if req.Wallet != billing.WalletMain && req.Wallet != billing.WalletDiscount {
		errAdmin(w, 400, "bad_request", "wallet 须为 1（主钱包）或 2（折扣钱包）")
		return
	}
	if strings.TrimSpace(req.Note) == "" {
		errAdmin(w, 400, "bad_request", "备注必填（审计留痕）")
		return
	}
	if !adminPasswordOk(req.ConfirmPassword) {
		a.auditAppend("balance_confirm_fail", uid, req.Note, ip)
		errAdmin(w, 403, "invalid_credentials", "确认密码错误")
		return
	}
	tx, err := a.DB.Begin() // 诊断 D1：余额变更与流水必须原子（非事务曾致账实分离风险）
	if err != nil {
		errAdmin(w, 500, "internal_error", "事务失败")
		return
	}
	// 当前余额在事务内读取；UPDATE 用相对量（绝对值覆盖会冲掉并发扣费/调账，致丢账）
	// 20260924：按 req.Wallet 选列（1=主钱包 / 2=折扣钱包），流水同步记 wallet
	col := billing.WalletColumn(req.Wallet)
	var before int64
	if err := tx.QueryRow("SELECT "+col+" FROM users WHERE id=?", uid).Scan(&before); err == sql.ErrNoRows {
		_ = tx.Rollback()
		errAdmin(w, 404, "not_found", "用户不存在")
		return
	} else if err != nil {
		_ = tx.Rollback()
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	after := before + req.AmountMicro
	if after < 0 {
		_ = tx.Rollback()
		errAdmin(w, 400, "bad_request", "扣减后余额不能为负")
		return
	}
	if _, err := tx.Exec("UPDATE users SET "+col+"="+col+"+? WHERE id=?", req.AmountMicro, uid); err != nil {
		_ = tx.Rollback()
		errAdmin(w, 500, "internal_error", "更新失败")
		return
	}
	fType := "topup"
	if req.AmountMicro < 0 {
		fType = "deduct"
	}
	if _, err := tx.Exec(
		`INSERT INTO balance_flows (user_id, request_id, type, amount_micro, balance_before_micro, balance_after_micro, unit_price_micro, note, operator, ts, wallet)
		 VALUES (?,0,?,?,?,?,0,?,'admin',?,?)`,
		uid, fType, req.AmountMicro, before, after, req.Note, time.Now().Unix(), req.Wallet); err != nil {
		_ = tx.Rollback()
		errAdmin(w, 500, "internal_error", "流水写入失败")
		return
	}
	if err := tx.Commit(); err != nil {
		errAdmin(w, 500, "internal_error", "提交失败")
		return
	}
	walletName := "主钱包"
	if req.Wallet == billing.WalletDiscount {
		walletName = "折扣钱包"
	}
	a.auditAppend("balance_"+fType, uid, fmt.Sprintf("%s【%s】：%+d 微元（%d→%d）", req.Note, walletName, req.AmountMicro, before, after), ip)
	jsonOut(w, 200, map[string]any{"ok": true, "before_micro": before, "after_micro": after, "wallet": req.Wallet})
}

// —— GET /v1/admin/audit?page ——
func (a *App) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	page := atoiDefault(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	const pageSize = 30
	var total int64
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM admin_audit").Scan(&total)
	rows, err := a.DB.Query(
		"SELECT action, target, detail, ip, prev_hash, self_hash, ts FROM admin_audit ORDER BY id DESC LIMIT ? OFFSET ?",
		pageSize, (page-1)*pageSize)
	if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var action, target, detail, ip, prev, self string
		var ts int64
		if rows.Scan(&action, &target, &detail, &ip, &prev, &self, &ts) == nil {
			tu, _ := strconv.ParseInt(target, 10, 64)
			items = append(items, map[string]any{
				"action": action, "target_user": tu, "detail": detail, "ip": ip,
				"prev_hash": prev, "self_hash": self, "ts": ts,
			})
		}
	}
	jsonOut(w, 200, map[string]any{"page": page, "page_size": pageSize, "total": total, "items": items})
}

// —— GET /v1/admin/reconcile ——（四项对账）
func (a *App) handleAdminReconcile(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	// 1) 余额重放：流水求和 vs users.balance
	// 口径（诊断 D1 实证锁定）：prehold 扣款行（amount<0）不计入；billed 记 -unit_price；
	// refunded 行 amount 恒为 0（退回资金不得与预扣重复计入）——Rust 时代 +delta 旧行已一次性清洗
	mismatch := int64(0)
	var replayDiffSum, replayDiffMax int64
	rows, err := a.DB.Query(
		`SELECT u.id, u.balance_micro,
		        COALESCE((SELECT SUM(CASE WHEN f.type IN ('topup','prehold') AND f.amount_micro>0 THEN f.amount_micro
		                                       WHEN f.type='deduct' THEN f.amount_micro
		                                       WHEN f.type='billed' THEN -f.unit_price_micro END)
		                  FROM balance_flows f WHERE f.user_id=u.id),0)
		 FROM users u`)
	if err == nil {
		for rows.Next() {
			var id, balance, replay int64
			if rows.Scan(&id, &balance, &replay) == nil && balance != replay {
				mismatch++
				d := balance - replay
				if d < 0 {
					d = -d
				}
				replayDiffSum += d
				if d > replayDiffMax {
					replayDiffMax = d
				}
			}
		}
		rows.Close()
	}
	// 2) 计费交叉：billed 请求数 vs billed 流水数
	var billedReq, billedFlow int64
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM requests WHERE bill_state='billed'").Scan(&billedReq)
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM balance_flows WHERE type='billed'").Scan(&billedFlow)
	// 3) 上游交叉：billed 请求数 vs 上游 used_calls 合计
	// 判定改阈值（诊断 D3）：上游配额池未覆盖全部渠道，口径天然不等；
	// 按 billed ≤ used×1.2 + 500 判定（20% 余量 + 500 笔免赔额度）
	var usedCalls sql.NullInt64
	_ = a.DB.QueryRow("SELECT SUM(used_calls) FROM upstream_quota WHERE provider!='pool'").Scan(&usedCalls)
	upstreamOK := usedCalls.Valid && billedReq <= usedCalls.Int64*12/10+500
	// 4) 审计哈希链全量重放
	brokenAt := int64(0)
	chainOK := true
	chainRows, err := a.DB.Query("SELECT id, action, target, detail, ip, ts, prev_hash, self_hash FROM admin_audit ORDER BY id ASC")
	if err == nil {
		prev := "GENESIS"
		for chainRows.Next() {
			var id, ts int64
			var action, target, detail, ip, ph, sh string
			if chainRows.Scan(&id, &action, &target, &detail, &ip, &ts, &ph, &sh) == nil {
				if ph != prev {
					brokenAt = id
					chainOK = false
					break
				}
				sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%d|%s", action, target, detail, ip, ts, prev)))
				if hex.EncodeToString(sum[:]) != sh {
					brokenAt = id
					chainOK = false
					break
				}
				prev = sh
			}
		}
		chainRows.Close()
	}
	jsonOut(w, 200, map[string]any{
		"balance_replay": map[string]any{"ok": mismatch == 0, "mismatch_users": mismatch, "diff_sum_micro": replayDiffSum, "diff_max_micro": replayDiffMax},
		"billing_cross":  map[string]any{"ok": billedReq == billedFlow, "billed_requests": billedReq, "billed_flows": billedFlow},
		"upstream_cross": map[string]any{"ok": upstreamOK, "billed_requests": billedReq, "used_calls": nullOr(usedCalls), "rule": "billed<=used*1.2+500"},
		"audit_chain":    map[string]any{"ok": chainOK, "broken_at": brokenAt},
	})
}

func nullOr(v sql.NullInt64) int64 {
	if !v.Valid {
		return 0
	}
	return v.Int64
}

func atoiDefault(s string, def int64) int64 {
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}
