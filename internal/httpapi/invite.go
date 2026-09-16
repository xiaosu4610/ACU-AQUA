package httpapi

// 邀请返利（精算定稿 20260917，方案 A）：
//   首充门槛 ¥5 / 邀请奖 ¥2 / 消费返利 10% / 邀请人月有效邀请帽 30 / 单被邀请人月返利帽 ¥100 / 月度总闸 ¥300。
// 恒不亏三原则：奖励只发余额（不可提现）；返利 < 毛利率（85.2%）；首充门槛即羊毛税。
// 范围：仅 aqua/ 个人实付计费计入返利基数（acu 众筹池、免费单、失败单、预扣均不算）。
// 通道：chat 计费成功后异步队列结算（热路径零阻塞）；MaxOpenConns(1) 下所有查询串行短借短还，
//       严禁 rows 循环内再发 DB 调用（参照 mailpool 死锁教训）。

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"acu-aqua/gateway/internal/auth"
)

const (
	invFirstPayMin     = 5_000_000  // 首充门槛 ¥5
	invRewardMicro     = 2_000_000  // 邀请奖 ¥2
	invRebatePct       = 10         // 消费返利 10%
	invBonusPct        = 10         // 被邀请人首充加赠 10%
	invMonthlyCapMicro = 100_000_000 // 单被邀请人月返利帽 ¥100
	inviterMonthCap    = 30         // 邀请人月有效邀请帽
	invMonthGateMicro  = 300_000_000 // 月度奖励总闸 ¥300（邀请奖+返利合计）
	invRebateQueue     = 4096
)

// inviteQueue 计费成功 → 返利结算的异步队列（rid 载体）
var inviteCh chan int64

// inviteGate 月度总闸的内存缓存（跨进程重启以 balance_flows 实算为准，此处仅省查询）
// ——简化：直接每次 SUM 实算（月内流水少），无缓存。

func init() { inviteCh = make(chan int64, invRebateQueue) }

// StartInviteRebater 返利结算后台（启动即消费队列）
func (a *App) StartInviteRebater() {
	go func() {
		for rid := range inviteCh {
			a.inviteSettleRebate(rid)
		}
	}()
}

// inviteEnqueue 非阻塞投递（队满丢弃并日志——返利少一单不影响主流程，流水可补）
func (a *App) inviteEnqueue(rid int64) {
	select {
	case inviteCh <- rid:
	default:
		log.Printf("[invite] 返利队列已满，rid=%d 跳过", rid)
	}
}

// ———————— 邀请码与关系 ————————

// inviteCodeOf 懒生成邀请码（一人一码，8 hex）
func (a *App) inviteCodeOf(uid int64) (string, error) {
	var code string
	err := a.DB.QueryRow("SELECT code FROM invite_codes WHERE user_id=?", uid).Scan(&code)
	if err == nil {
		return code, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code = strings.ToUpper(hex.EncodeToString(b))
	_, err = a.DB.Exec("INSERT OR IGNORE INTO invite_codes (code, user_id, created_ts) VALUES (?,?,?)",
		code, uid, time.Now().Unix())
	if err != nil {
		return "", err
	}
	return code, nil
}

// inviteBind 注册后绑定关系（静默失败：不阻断注册）
func (a *App) inviteBind(uid int64, code, ip string) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return
	}
	var inviter int64
	var disabled int
	if err := a.DB.QueryRow("SELECT user_id, disabled FROM invite_codes WHERE code=?", code).Scan(&inviter, &disabled); err != nil || disabled == 1 || inviter == uid {
		return // 码无效/停用/自邀
	}
	// 防刷闸：同 IP 24h 内已绑关系 ≥3 → 静默不绑
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM invite_relations
		WHERE invitee_ip=? AND created_ts>?`, ip, time.Now().Unix()-86400).Scan(&n)
	if n >= 3 {
		log.Printf("[invite] 同 IP 绑定超限，静默跳过 ip=%s uid=%d", ip, uid)
		return
	}
	_, _ = a.DB.Exec(`INSERT OR IGNORE INTO invite_relations (inviter_id, invitee_id, code, created_ts, invitee_ip)
		VALUES (?,?,?,?,?)`, inviter, uid, code, time.Now().Unix(), ip)
}

// ———————— 用户 API ————————

// handleInviteMe GET /v1/invite/me：我的邀请码/链接/统计/明细
func (a *App) handleInviteMe(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil || actx.UserID == 0 {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	code, err := a.inviteCodeOf(actx.UserID)
	if err != nil {
		errOut(w, 500, "internal_error", "邀请码生成失败")
		return
	}
	var invited, qualified int
	var earnedMicro int64
	_ = a.DB.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN reward_ts>0 THEN 1 ELSE 0 END),0)
		FROM invite_relations WHERE inviter_id=?`, actx.UserID).Scan(&invited, &qualified)
	_ = a.DB.QueryRow(`SELECT COALESCE(SUM(amount_micro),0) FROM balance_flows
		WHERE user_id=? AND type IN ('invite_reward','invite_rebate')`, actx.UserID).Scan(&earnedMicro)

	type item struct {
		Email     string `json:"email,omitempty"`
		Username  string `json:"username"`
		CreatedTS int64  `json:"created_ts"`
		Qualified bool   `json:"qualified"`
	}
	rows, err := a.DB.Query(`SELECT COALESCE(u.username,''), COALESCE(u.email,''), rel.created_ts,
		CASE WHEN rel.reward_ts>0 THEN 1 ELSE 0 END
		FROM invite_relations rel LEFT JOIN users u ON u.id=rel.invitee_id
		WHERE rel.inviter_id=? ORDER BY rel.id DESC LIMIT 100`, actx.UserID)
	var list []item
	if err == nil {
		for rows.Next() {
			var it item
			var q int
			if rows.Scan(&it.Username, &it.Email, &it.CreatedTS, &q) == nil {
				it.Qualified = q == 1
				list = append(list, it)
			}
		}
		rows.Close()
	}
	jsonOut(w, 200, map[string]any{
		"ok": true, "code": code, "link": "/login?mode=register&code=" + code,
		"invited": invited, "qualified": qualified,
		"earned_micro": earnedMicro,
		"reward_each_micro": invRewardMicro,
		"first_pay_min_micro": invFirstPayMin,
		"rebate_pct": invRebatePct,
		"list": list,
	})
}

// handleInviteRotate POST /v1/invite/rotate：重置邀请码（关系不变）
func (a *App) handleInviteRotate(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil || actx.UserID == 0 {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		errOut(w, 500, "internal_error", "生成失败")
		return
	}
	code := strings.ToUpper(hex.EncodeToString(b))
	// upsert：用户可能从未打开过邀请页（无码记录），纯 UPDATE 会 0 行假成功
	if _, err := a.DB.Exec(`INSERT INTO invite_codes (code, user_id, created_ts) VALUES (?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET code=excluded.code`, code, actx.UserID, time.Now().Unix()); err != nil {
		errAdmin(w, 500, "internal_error", "重置失败")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "code": code})
}

// ———————— 首充钩子（epaySettle balance 分支成功后调用） ————————

// inviteOnFirstPay 被邀请人首充达标 → 邀请人发 ¥2 奖 + 被邀请人加赠 10%（均发余额，幂等）
func (a *App) inviteOnFirstPay(uid, amountMicro int64) {
	var relID, inviter int64
	var firstPayTS, rewardTS int64
	var rebateMonth string
	err := a.DB.QueryRow(`SELECT id, inviter_id, first_pay_ts, reward_ts, rebate_month
		FROM invite_relations WHERE invitee_id=?`, uid).
		Scan(&relID, &inviter, &firstPayTS, &rewardTS, &rebateMonth)
	if err != nil || rewardTS > 0 {
		return // 无邀请关系或已发过
	}
	if amountMicro < invFirstPayMin {
		return // 未达门槛（不消耗资格：reward_ts 仍为 0，下一笔达标再触发）
	}
	if invGateExceeded(a.DB.DB) {
		log.Printf("[invite] 月度总闸已满，邀请奖跳过 uid=%d", uid)
		return
	}
	now := time.Now().Unix()
	bonus := amountMicro * invBonusPct / 100
	// 邀请奖 ¥2 → 邀请人
	if err := a.inviteCredit(inviter, invRewardMicro, "invite_reward",
		fmt.Sprintf("邀请奖:被邀请人#%d", uid)); err != nil {
		log.Printf("[invite] 邀请奖发放失败 inviter=%d: %v", inviter, err)
		return
	}
	// 被邀请人首充加赠 10% → 被邀请人
	if bonus > 0 {
		if err := a.inviteCredit(uid, bonus, "invite_bonus",
			fmt.Sprintf("受邀首充加赠:%d%%", invBonusPct)); err != nil {
			log.Printf("[invite] 首充加赠失败 uid=%d: %v", uid, err)
		}
	}
	_, _ = a.DB.Exec("UPDATE invite_relations SET first_pay_ts=?, reward_ts=? WHERE id=?", now, now, relID)
	a.auditAppend("invite_reward", inviter, fmt.Sprintf("invitee=%d reward=%d bonus=%d", uid, invRewardMicro, bonus), "")
	log.Printf("[invite] 邀请奖已发 inviter=%d invitee=%d reward=%d bonus=%d", inviter, uid, invRewardMicro, bonus)
}

// ———————— 消费返利（异步结算） ————————

// inviteSettleRebate 单笔计费成功 → 邀请人返 10%（幂等由 balance_flows request_id 唯一性兜底）
func (a *App) inviteSettleRebate(rid int64) {
	var uid, amount int64
	var line string
	if err := a.DB.QueryRow("SELECT user_id, bill_amount_micro, COALESCE(resolved_line,'') FROM requests WHERE rowid=? AND billed=1 AND bill_state='billed' AND ok=1", rid).
		Scan(&uid, &amount, &line); err != nil {
		return
	}
	if uid <= 0 || amount <= 0 || line == "acu" || line == "codex" || strings.HasPrefix(line, "tide") {
		return // crowd 池/免费线不计返利（精算定稿：仅 aqua/ 个人实付）
	}
	var inviter int64
	var relID int64
	var rebateMonth string
	var rebateMicro int64
	err := a.DB.QueryRow(`SELECT id, inviter_id, rebate_month, rebate_month_micro
		FROM invite_relations WHERE invitee_id=?`, uid).Scan(&relID, &inviter, &rebateMonth, &rebateMicro)
	if err != nil {
		return // 非被邀请人
	}
	// 幂等：该请求已发过返利则跳过
	var dup int
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM balance_flows WHERE request_id=? AND type='invite_rebate'", rid).Scan(&dup)
	if dup > 0 {
		return
	}
	// 单被邀请人月返利帽
	thisMonth := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01")
	if rebateMonth != thisMonth {
		rebateMonth, rebateMicro = thisMonth, 0
	}
	rebate := amount * invRebatePct / 100
	if rebateMicro+rebate > invMonthlyCapMicro {
		rebate = invMonthlyCapMicro - rebateMicro
		if rebate <= 0 {
			return // 月帽已满
		}
	}
	if invGateExceeded(a.DB.DB) {
		log.Printf("[invite] 月度总闸已满，返利跳过 rid=%d", rid)
		return
	}
	if err := a.inviteCredit(inviter, rebate, "invite_rebate",
		fmt.Sprintf("消费返利:被邀请人#%d:req=%d", uid, rid)); err != nil {
		log.Printf("[invite] 返利发放失败 rid=%d: %v", rid, err)
		return
	}
	_, _ = a.DB.Exec("UPDATE invite_relations SET rebate_month=?, rebate_month_micro=? WHERE id=?",
		rebateMonth, rebateMicro+rebate, relID)
}

// inviteCredit 入余额 + 流水（事务）
func (a *App) inviteCredit(uid, amount int64, typ, note string) error {
	tx, err := a.DB.DB.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE users SET balance_micro=balance_micro+? WHERE id=?", amount, uid); err != nil {
		_ = tx.Rollback()
		return err
	}
	var balance int64
	if err := tx.QueryRow("SELECT balance_micro FROM users WHERE id=?", uid).Scan(&balance); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`INSERT INTO balance_flows (user_id, request_id, type, amount_micro,
		balance_before_micro, balance_after_micro, unit_price_micro, note, operator, ts)
		VALUES (?,0,?,?,?,?,0,?,'system',?)`, uid, typ, amount, balance-amount, balance, note, time.Now().Unix()); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// invGateExceeded 月度总闸：当月邀请奖+返利+加赠合计 ≥ ¥300
func invGateExceeded(d *sql.DB) bool {
	cst := time.FixedZone("CST", 8*3600)
	now := time.Now().In(cst)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, cst).Unix()
	var sum int64
	_ = d.QueryRow(`SELECT COALESCE(SUM(amount_micro),0) FROM balance_flows
		WHERE type IN ('invite_reward','invite_rebate','invite_bonus') AND ts>=?`, start).Scan(&sum)
	return sum >= invMonthGateMicro
}

// ———————— 管理端点 ————————

// handleAdminInvites GET /v1/admin/invites：统计 + 明细
func (a *App) handleAdminInvites(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var relations, qualified int
	var rewards, rebates int64
	_ = a.DB.QueryRow("SELECT COUNT(*), COALESCE(SUM(CASE WHEN reward_ts>0 THEN 1 ELSE 0 END),0) FROM invite_relations").Scan(&relations, &qualified)
	_ = a.DB.QueryRow(`SELECT COALESCE(SUM(amount_micro),0) FROM balance_flows WHERE type='invite_reward'`).Scan(&rewards)
	_ = a.DB.QueryRow(`SELECT COALESCE(SUM(amount_micro),0) FROM balance_flows WHERE type='invite_rebate'`).Scan(&rebates)

	type row struct {
		ID          int64  `json:"id"`
		Inviter     int64  `json:"inviter_id"`
		Invitee     int64  `json:"invitee_id"`
		Code        string `json:"code"`
		CreatedTS   int64  `json:"created_ts"`
		IP          string `json:"invitee_ip"`
		RewardTS    int64  `json:"reward_ts"`
		RebateMicro int64  `json:"rebate_month_micro"`
	}
	rows, err := a.DB.Query(`SELECT id, inviter_id, invitee_id, code, created_ts, invitee_ip, reward_ts, rebate_month_micro
		FROM invite_relations ORDER BY id DESC LIMIT 200`)
	var list []row
	if err == nil {
		for rows.Next() {
			var x row
			if rows.Scan(&x.ID, &x.Inviter, &x.Invitee, &x.Code, &x.CreatedTS, &x.IP, &x.RewardTS, &x.RebateMicro) == nil {
				list = append(list, x)
			}
		}
		rows.Close()
	}
	jsonOut(w, 200, map[string]any{"ok": true,
		"relations": relations, "qualified": qualified,
		"reward_paid_micro": rewards, "rebate_paid_micro": rebates,
		"monthly_gate_micro": invMonthGateMicro, "list": list})
}
