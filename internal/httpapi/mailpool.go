package httpapi

// 微软邮箱发信池（站长定稿 20260916）：全域主线路，目标「用户秒收、不进垃圾池」。
// - 账号台账 mail_accounts：MSA refresh_token 滚动续期（换新必须立即回写，旧 token 会作废）
// - 调度：active 且当日配额未尽中选 last_ok_ts 最早（雨露均沾）；单号日限 40 封
// - 回落节拍（站长指令）：微软池连败 5 → 阿里云试探 1 封 → 立即回池重试；
//   仅当 active=0（全灭）才由阿里云接管常态发信；单封微软池失败时阿里云立即兜底（用户永远收得到码）
// - 活体巡检：每 6h 全量刷新 token，invalid_grant 标 dead；probe 成功自动复活

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	msTenantURL   = "https://login.microsoftonline.com/consumers/oauth2/v2.0/token"
	msGraphSend   = "https://graph.microsoft.com/v1.0/me/sendMail"
	msGraphScope  = "https://graph.microsoft.com/Mail.Send offline_access"
	mpDailyCap  = 40 // 单号每日发信上限（个人号日限 100~300 的 1/3 余量养号）
	mpCooldown  = 55 * time.Second
	mpProbes    = 5   // 微软池连败 N 次后下一封阿里云试探
	mpProbeInterval = 6 * time.Hour
)

// MailPool 发信池运行态
type MailPool struct {
	mu         sync.Mutex
	failStreak int // 微软池连续投递失败（跨账号全局节拍，试探回落用）
	hc         *http.Client
}

func newMailPool() *MailPool {
	return &MailPool{hc: &http.Client{Timeout: 30 * time.Second}}
}

// ———————— 数据访问 ————————

type poolAcc struct {
	Email        string
	ClientID     string
	RefreshToken string
	AccessToken  string
	TokenExpTS   int64
	SentToday    int
	SentDate     string
	FailStreak   int
	lastOK       int64
}

var errPoolNoActive = errors.New("mailpool: 无 active 账号")
var errPoolThrottle = errors.New("mailpool: 连败节流，本封走阿里云试探")

// pickAccount 选号：active、当日配额未尽，last_ok_ts 最早优先（雨露均沾）。
// last_ok_ts 并入 SELECT（连接池 MaxOpenConns(1)：查询循环内禁止再发 DB 调用）。
func (p *MailPool) pickAccount(d *sql.DB) (*poolAcc, error) {
	rows, err := d.Query(`SELECT email, client_id, refresh_token, COALESCE(access_token,''),
		COALESCE(token_exp_ts,0), COALESCE(sent_today,0), COALESCE(sent_date,''), COALESCE(fail_streak,0),
		COALESCE(last_ok_ts,0)
		FROM mail_accounts WHERE status='active'
		ORDER BY CASE WHEN sent_date = strftime('%Y-%m-%d','now','+8 hours') THEN sent_today ELSE 0 END ASC,
		         last_ok_ts ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	today := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	for rows.Next() {
		var a poolAcc
		if err := rows.Scan(&a.Email, &a.ClientID, &a.RefreshToken, &a.AccessToken,
			&a.TokenExpTS, &a.SentToday, &a.SentDate, &a.FailStreak, &a.lastOK); err == nil {
			if a.SentDate == today && a.SentToday >= mpDailyCap {
				continue // 今日配额已尽
			}
			return &a, nil
		}
	}
	return nil, errPoolNoActive
}

// refresh 续期 access_token 并滚动回写 refresh_token（先写库成功才算数，crash-safe）
func (p *MailPool) refresh(d *sql.DB, a *poolAcc) error {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {a.ClientID},
		"refresh_token": {a.RefreshToken},
		"scope":         {msGraphScope},
	}
	req, _ := http.NewRequest("POST", msTenantURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("token 网络错误: %w", err) // 暂态，不标 dead
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var tok struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		ExpiresIn        int64  `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &tok)
	if tok.AccessToken == "" {
		if resp.StatusCode == 400 && (tok.Error == "invalid_grant" || tok.Error == "unauthorized_client") {
			_, _ = d.Exec(`UPDATE mail_accounts SET status='dead', dead_reason=? WHERE email=?`,
				fmt.Sprintf("%s: %s", tok.Error, truncate(tolowerASCII(tok.ErrorDescription), 120)), a.Email)
			return fmt.Errorf("token 失效（已标 dead）: %s", tok.Error)
		}
		return fmt.Errorf("token 刷新异常 http=%d err=%s", resp.StatusCode, tok.Error)
	}
	newRT := tok.RefreshToken
	if newRT == "" {
		newRT = a.RefreshToken // 理论不会发生，防御：保留旧 token
	}
	_, err = d.Exec(`UPDATE mail_accounts SET access_token=?, token_exp_ts=?, refresh_token=?
		WHERE email=?`,
		tok.AccessToken, time.Now().Unix()+tok.ExpiresIn-120, newRT, a.Email)
	if err != nil {
		return fmt.Errorf("token 回写失败: %w", err) // 回写失败不得继续用新 token（旧的可能已作废）
	}
	a.AccessToken = tok.AccessToken
	a.TokenExpTS = time.Now().Unix() + tok.ExpiresIn - 120
	a.RefreshToken = newRT
	return nil
}

// sendGraph Graph API sendMail（to 单地址；验证码场景足够）
func (p *MailPool) sendGraph(d *sql.DB, a *poolAcc, to, subject, body string) error {
	if a.AccessToken == "" || a.TokenExpTS < time.Now().Unix()+300 {
		if err := p.refresh(d, a); err != nil {
			return err
		}
	}
	msg := map[string]any{
		"message": map[string]any{
			"subject": subject,
			"body":    map[string]any{"contentType": "Text", "content": body},
			"toRecipients": []any{map[string]any{
				"emailAddress": map[string]any{"address": to}}},
		},
		"saveToSentItems": false,
	}
	buf, _ := json.Marshal(msg)
	req, _ := http.NewRequest("POST", msGraphSend, bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+a.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("graph 网络错误: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 202 || resp.StatusCode == 200 {
		_, _ = d.Exec(`UPDATE mail_accounts
			SET sent_today = CASE WHEN sent_date = strftime('%Y-%m-%d','now','+8 hours') THEN sent_today+1 ELSE 1 END,
			    sent_date = strftime('%Y-%m-%d','now','+8 hours'),
			    sent_total = sent_total+1, last_ok_ts = ?, fail_streak = 0
			WHERE email=?`, time.Now().Unix(), a.Email)
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	detail := truncate(string(raw), 200)
	// 401 = token 中途失效：清缓存令下一封先刷新；403/429 = 账号被限/禁：连败计（≥5 由 probe 摘）
	if resp.StatusCode == 401 {
		_, _ = d.Exec("UPDATE mail_accounts SET access_token='', token_exp_ts=0 WHERE email=?", a.Email)
		return fmt.Errorf("graph 401 token 中途失效")
	}
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		_, _ = d.Exec("UPDATE mail_accounts SET fail_streak=fail_streak+1 WHERE email=?", a.Email)
		if a.FailStreak+1 >= mpProbes {
			_, _ = d.Exec(`UPDATE mail_accounts SET status='cooldown', dead_reason=? WHERE email=?`,
				"投递连败冷却（probe 可复活）", a.Email)
		}
	}
	return fmt.Errorf("graph http=%d %s", resp.StatusCode, detail)
}

// send 单封主路径。错误语义：
//   - errPoolNoActive  全灭（外层阿里云接管）
//   - errPoolThrottle  连败节流，本封让路阿里云试探（外层处理后回池）
//   - 其他             本封投递失败（外层阿里云兜底）
func (p *MailPool) send(d *sql.DB, to, subject, body string) (channel, sender string, err error) {
	p.mu.Lock()
	streak := p.failStreak
	p.mu.Unlock()
	if streak >= mpProbes {
		return "", "", errPoolThrottle
	}
	acc, err := p.pickAccount(d)
	if err != nil {
		return "", "", err // errPoolNoActive
	}
	// 单号冷却：该号 60s 内刚发过则换备用号；无备用沿用当前号（个人号 1 分钟两封在限额内）
	if time.Since(time.Unix(acc.lastOK, 0)) < mpCooldown {
		if alt, e2 := p.pickAlternate(d, acc.Email); e2 == nil {
			acc = alt
		}
	}
	if err := p.sendGraph(d, acc, to, subject, body); err != nil {
		p.mu.Lock()
		p.failStreak++
		p.mu.Unlock()
		return "", acc.Email, err
	}
	p.mu.Lock()
	p.failStreak = 0
	p.mu.Unlock()
	return "mspool", acc.Email, nil
}

// pickAlternate 备用号（排除 just-used）。
// 注意：连接池 MaxOpenConns(1)，绝不允许在 rows 未关闭的循环体内再发起 DB 调用（会永久死锁）——
// last_ok_ts 直接并入 SELECT，Go 侧过滤，单次查询完成。
func (p *MailPool) pickAlternate(d *sql.DB, exclude string) (*poolAcc, error) {
	rows, err := d.Query(`SELECT email, client_id, refresh_token, COALESCE(access_token,''),
		COALESCE(token_exp_ts,0), COALESCE(sent_today,0), COALESCE(sent_date,''), COALESCE(fail_streak,0),
		COALESCE(last_ok_ts,0)
		FROM mail_accounts WHERE status='active' AND email<>?
		ORDER BY last_ok_ts ASC LIMIT 10`, exclude)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	today := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	for rows.Next() {
		var a poolAcc
		var lastOK int64
		if err := rows.Scan(&a.Email, &a.ClientID, &a.RefreshToken, &a.AccessToken,
			&a.TokenExpTS, &a.SentToday, &a.SentDate, &a.FailStreak, &lastOK); err == nil {
			if a.SentDate == today && a.SentToday >= mpDailyCap {
				continue // 配额尽
			}
			if time.Since(time.Unix(lastOK, 0)) < mpCooldown {
				continue // 冷却中
			}
			return &a, nil
		}
	}
	return nil, errPoolNoActive
}

// probeAll 全量验活：逐号刷新 token（并发 8）；成功复活为 active，invalid_grant 标 dead
func (p *MailPool) probeAll(d *sql.DB) (alive, dead int) {
	rows, err := d.Query("SELECT email, client_id, refresh_token FROM mail_accounts")
	if err != nil {
		return 0, 0
	}
	type item struct{ email, cid, rt string }
	var all []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.email, &it.cid, &it.rt) == nil {
			all = append(all, it)
		}
	}
	rows.Close()

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, it := range all {
		wg.Add(1)
		go func(it item) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			acc := &poolAcc{Email: it.email, ClientID: it.cid, RefreshToken: it.rt}
			if err := p.refresh(d, acc); err != nil {
				mu.Lock()
				dead++
				mu.Unlock()
				log.Printf("[mailpool] 验活失败 %s: %v", it.email, err)
				return
			}
			_, _ = d.Exec(`UPDATE mail_accounts SET status='active', dead_reason='', fail_streak=0,
				last_ok_ts=? WHERE email=?`, time.Now().Unix(), it.email)
			mu.Lock()
			alive++
			mu.Unlock()
		}(it)
	}
	wg.Wait()
	p.mu.Lock()
	p.failStreak = 0 // 验活后重置全局连败节拍
	p.mu.Unlock()
	return
}

// activeCount 存活可用数
func (p *MailPool) activeCount(d *sql.DB) int {
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM mail_accounts WHERE status='active'`).Scan(&n)
	return n
}

// ———————— App 统一发信入口 ————————

// SendAny 发信统一入口（验证码/群发共用）。返回实际通道与发信账号。
// 节拍（站长定稿）：微软池主发 → 连败 5 阿里云试探 1 封 → 回池；单封失败阿里云兜底；全灭阿里云接管。
func (a *App) SendAny(to, subject, body string) (channel, sender string, err error) {
	if a.MailPool != nil {
		if a.MailPool.activeCount(a.DB.DB) > 0 {
			a.MailPool.mu.Lock()
			streak := a.MailPool.failStreak
			a.MailPool.mu.Unlock()
			if streak >= mpProbes {
				// 阿里云试探 1 封 → 立即回池（failStreak=4，下一封再试微软池）
				if a.Mail != nil {
					if e := a.Mail.Send(to, subject, body); e == nil {
						a.MailPool.setStreak(4)
						return "aliyun_probe", a.Mail.Cfg.From, nil
					}
				}
				a.MailPool.setStreak(4)
			}
			ch, se, e := a.MailPool.send(a.DB.DB, to, subject, body)
			if e == nil {
				return ch, se, nil
			}
			if !errors.Is(e, errPoolNoActive) && !errors.Is(e, errPoolThrottle) {
				log.Printf("[mailpool] 微软池投递失败 to=%s acc=%s: %v（阿里云兜底）", to, se, e)
			}
		}
	}
	if a.Mail != nil {
		if e := a.Mail.Send(to, subject, body); e == nil {
			return "aliyun", a.Mail.Cfg.From, nil
		} else {
			return "aliyun", "", e
		}
	}
	return "", "", fmt.Errorf("无可用发信通道")
}

func (p *MailPool) setStreak(n int) {
	p.mu.Lock()
	p.failStreak = n
	p.mu.Unlock()
}

// StartMailProbe 后台活体巡检（每 6h；启动 1 分钟后首跑）
func (a *App) StartMailProbe() {
	if a.MailPool == nil {
		return
	}
	go func() {
		time.Sleep(time.Minute)
		if n := a.MailPool.activeCount(a.DB.DB); n > 0 {
			al, de := a.MailPool.probeAll(a.DB.DB)
			log.Printf("[mailpool] 首轮验活：alive=%d dead=%d", al, de)
		}
		t := time.NewTicker(mpProbeInterval)
		defer t.Stop()
		for range t.C {
			al, de := a.MailPool.probeAll(a.DB.DB)
			log.Printf("[mailpool] 定时验活：alive=%d dead=%d", al, de)
		}
	}()
}

// ———————— 管理端点 ————————

// handleAdminMailpoolImport POST /v1/admin/mailpool/import {text}
// 格式：email----password----client_id----refresh_token（每行一号；密码留档不入库用途字段）
func (a *App) handleAdminMailpoolImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := adminBody(r, 1<<20, &req); err != nil || strings.TrimSpace(req.Text) == "" {
		errAdmin(w, 400, "bad_request", "text 必填（每行：email----password----client_id----refresh_token）")
		return
	}
	imported, skipped := 0, 0
	for _, line := range strings.Split(req.Text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "----")
		if len(parts) < 4 || !strings.Contains(parts[0], "@") {
			skipped++
			continue
		}
		email, cid, rt := strings.ToLower(strings.TrimSpace(parts[0])),
			strings.TrimSpace(parts[2]), strings.TrimSpace(parts[3])
		if _, err := a.DB.Exec(`INSERT INTO mail_accounts (email, client_id, refresh_token, status)
			VALUES (?,?,?,'active')
			ON CONFLICT(email) DO UPDATE SET client_id=excluded.client_id, refresh_token=excluded.refresh_token,
				status='active', dead_reason='', fail_streak=0`, email, cid, rt); err != nil {
			skipped++
			continue
		}
		imported++
	}
	a.auditAppend("mailpool_import", 0, fmt.Sprintf("imported=%d skipped=%d", imported, skipped), clientIP(r))
	adminJSON(w, map[string]any{"ok": true, "imported": imported, "skipped": skipped,
		"message": "导入完成，建议立即 POST /v1/admin/mailpool/probe 全量验活"})
}

// handleAdminMailpoolList GET /v1/admin/mailpool 台账
func (a *App) handleAdminMailpoolList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	rows, err := a.DB.Query(`SELECT email, status, COALESCE(dead_reason,''), COALESCE(sent_today,0),
		COALESCE(sent_total,0), COALESCE(fail_streak,0), COALESCE(last_ok_ts,0),
		(length(COALESCE(refresh_token,''))>0), COALESCE(token_exp_ts,0)
		FROM mail_accounts ORDER BY status ASC, email ASC`)
	if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	type row struct {
		Email       string `json:"email"`
		Status      string `json:"status"`
		DeadReason  string `json:"dead_reason,omitempty"`
		SentToday   int    `json:"sent_today"`
		SentTotal   int    `json:"sent_total"`
		FailStreak  int    `json:"fail_streak"`
		LastOKTS    int64  `json:"last_ok_ts"`
		HasToken    bool   `json:"has_token"`
		TokenExpTS  int64  `json:"token_exp_ts"`
	}
	var out []row
	stat := map[string]int{}
	for rows.Next() {
		var x row
		var hasTok int
		if rows.Scan(&x.Email, &x.Status, &x.DeadReason, &x.SentToday, &x.SentTotal,
			&x.FailStreak, &x.LastOKTS, &hasTok, &x.TokenExpTS) == nil {
			x.HasToken = hasTok > 0
			out = append(out, x)
			stat[x.Status]++
		}
	}
	adminJSON(w, map[string]any{"ok": true, "stat": stat, "accounts": out})
}

// handleAdminMailpoolProbe POST /v1/admin/mailpool/probe 全量验活（同步，约 15~30s）
func (a *App) handleAdminMailpoolProbe(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	alive, dead := a.MailPool.probeAll(a.DB.DB)
	a.auditAppend("mailpool_probe", 0, fmt.Sprintf("alive=%d dead=%d", alive, dead), clientIP(r))
	adminJSON(w, map[string]any{"ok": true, "alive": alive, "dead": dead})
}

// handleAdminMailpoolTest POST /v1/admin/mailpool/test {to} 单号测试发信（走 SendAny 主路径）
func (a *App) handleAdminMailpoolTest(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var req struct{ To string `json:"to"` }
	if err := adminBody(r, 4<<10, &req); err != nil || !strings.Contains(req.To, "@") {
		errAdmin(w, 400, "bad_request", "to 必填")
		return
	}
	code := genCode()
	ch, se, err := a.SendAny(strings.ToLower(strings.TrimSpace(req.To)),
		"AQUA 发信池测试", "这是微软发信池测试邮件，验证码："+code+"。收到即通道健康。")
	if err != nil {
		errAdmin(w, 502, "send_failed", "发信失败: "+err.Error())
		return
	}
	a.auditAppend("mailpool_test", 0, fmt.Sprintf("to=%s channel=%s sender=%s", req.To, ch, se), clientIP(r))
	adminJSON(w, map[string]any{"ok": true, "channel": ch, "sender": se})
}

// ———————— 小工具 ————————

func tolowerASCII(s string) string {
	return strings.ToLower(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
