// keyquota.go 密钥分发配额内核（P4，20260919 站长定稿方案 B）。
//
// 资金模型：密钥上的"额度"是**虚拟限额标记**，不持有资金；实际扣款始终从
// `users.balance_micro` 按站内原价扣（倍率仅用于展示换算，不影响实扣）。
//
// 三个关键设计：
//  1. **惰性重置**（无定时任务）：请求时判定"上次重置时刻 < 当前周期起点"则清零，
//     跨周期自动生效，服务重启不漏。周期口径东八区（与全站统计一致）。
//  2. **原子消耗**：quota_used 累加用单条 UPDATE（带条件），并发安全，不读-改-写。
//  3. **红线遵守**：全程 int64 微元、零浮点；倍率整数乘除。
package httpapi

import (
	"database/sql"
	"strconv"
	"time"
)

// 配额口径常量
const (
	quotaTypeNone   = ""       // 不限
	quotaTypeCount  = "count"  // 按次数
	quotaTypeAmount = "amount" // 按金额（微元）

	quotaResetNone    = ""        // 不重置
	quotaResetDaily   = "daily"   // 每日
	quotaResetMonthly = "monthly" // 每月
)

// quotaInfo 密钥配额快照（鉴权后读取，供限额检查与前端展示）
type quotaInfo struct {
	Type      string // ''|count|amount
	Limit     int64  // 限额值
	Used      int64  // 本周期已用
	Reset     string // ''|daily|monthly
	ResetAt   int64  // 上次重置时刻
	RateNum   int64  // 倍率分子（仅展示）
	RateDen   int64  // 倍率分母
	ExpiresAt int64  // 有效期（0=永久）
	Note      string // 备注
}

// periodStart 当前周期起点（东八区）：daily=今日 00:00；monthly=本月 1 日 00:00；
// 不重置返回 0。东八区偏移固定 +8h，与 myFinance/admin_stats 口径一致。
func periodStart(reset string, now int64) int64 {
	const cst = int64(8 * 3600)
	switch reset {
	case quotaResetDaily:
		// 东八区当日零点：先平移到东八区，取整日，再平移回 UTC
		return ((now+cst)/86400)*86400 - cst
	case quotaResetMonthly:
		// 东八区当月 1 日零点
		local := time.Unix(now+cst, 0).UTC()
		first := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC)
		return first.Unix() - cst
	}
	return 0
}

// keyQuotaGet 读密钥配额（含惰性重置：跨周期自动清零并落库）
func (a *App) keyQuotaGet(keyID int64) (*quotaInfo, error) {
	if keyID <= 0 {
		return nil, nil
	}
	var q quotaInfo
	err := a.DB.QueryRow(
		`SELECT COALESCE(quota_type,''), COALESCE(quota_limit,0), COALESCE(quota_used,0),
		        COALESCE(quota_reset,''), COALESCE(quota_reset_at,0),
		        COALESCE(rate_num,1), COALESCE(rate_den,1),
		        COALESCE(expires_at,0), COALESCE(key_note,'')
		 FROM api_keys WHERE id=?`, keyID).
		Scan(&q.Type, &q.Limit, &q.Used, &q.Reset, &q.ResetAt, &q.RateNum, &q.RateDen, &q.ExpiresAt, &q.Note)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if q.RateDen <= 0 {
		q.RateDen = 1 // 防除零（历史脏数据兜底）
	}
	if q.RateNum <= 0 {
		q.RateNum = 1
	}
	// 惰性重置：跨周期则清零（单条原子 UPDATE，带 reset_at 条件防并发重复重置）
	if start := periodStart(q.Reset, time.Now().Unix()); start > 0 && q.ResetAt < start {
		if _, err := a.DB.Exec(
			`UPDATE api_keys SET quota_used=0, quota_reset_at=? WHERE id=? AND quota_reset_at < ?`,
			time.Now().Unix(), keyID, start); err == nil {
			q.Used = 0
			q.ResetAt = time.Now().Unix()
		}
	}
	return &q, nil
}

// keyQuotaCheck 限额判定：超限返回错误码与提示；未超限返回空串。
// 口径：count 比次数、amount 比微元（累计消耗金额）。限额为 0 视为不限。
func keyQuotaCheck(q *quotaInfo) (code, msg string) {
	if q == nil || q.Limit <= 0 {
		return "", ""
	}
	switch q.Type {
	case quotaTypeCount:
		if q.Used >= q.Limit {
			return "key_quota_exceeded", "该密钥的调用次数额度已用完（限额 " +
				strconv.FormatInt(q.Limit, 10) + " 次）：请在控制台为该密钥重置额度或提高限额，也可新建一把密钥继续调用"
		}
	case quotaTypeAmount:
		if q.Used >= q.Limit {
			return "key_quota_exceeded", "该密钥的消费额度已用完（限额 " +
				microToYuanStr(q.Limit) + " 元）：请在控制台为该密钥重置额度或提高限额，也可新建一把密钥继续调用"
		}
	}
	return "", ""
}

// keyQuotaConsume 消耗配额（结算成功后调用）：count 累加 1 次、amount 累加实扣金额。
// 单条原子 UPDATE（无读-改-写），并发安全。keyID<=0 或未设限额时直接返回。
func (a *App) keyQuotaConsume(keyID int64, q *quotaInfo, chargedMicro int64) {
	if keyID <= 0 || q == nil || q.Type == quotaTypeNone || q.Limit <= 0 {
		return
	}
	delta := int64(1)
	if q.Type == quotaTypeAmount {
		delta = chargedMicro
		if delta <= 0 {
			return
		}
	}
	if _, err := a.DB.Exec("UPDATE api_keys SET quota_used=quota_used+? WHERE id=?", delta, keyID); err != nil {
		// 配额累加失败不阻断用户请求（已交付），但必须留痕——否则限额失效会造成无限调用
		a.logError("key_quota_consume_failed", "", 0, 0, "key_id="+strconv.FormatInt(keyID, 10)+" delta="+strconv.FormatInt(delta, 10)+" err="+err.Error())
	}
}

// keyQuotaSettle 请求结束后的配额消耗（P4）：从 requests 回读实扣金额再累加配额。
//
// 为什么回读：两条 serve 路径（流式/非流式）各自内部完成结算，统一在 serve 返回后
// 调用本函数即可覆盖全部路径，无需给两个函数加返回值。**仅当密钥设了限额时才回查**，
// 未设限额的密钥（绝大多数）零额外开销。
func (a *App) keyQuotaSettle(rid, keyID int64, q *quotaInfo) {
	if rid <= 0 || keyID <= 0 || q == nil || q.Type == quotaTypeNone || q.Limit <= 0 {
		return
	}
	var amount int64
	var state string
	if err := a.DB.QueryRow("SELECT COALESCE(bill_amount_micro,0), COALESCE(bill_state,'') FROM requests WHERE rowid=?",
		rid).Scan(&amount, &state); err != nil {
		return
	}
	// 只统计真正计费成功的请求（失败/退款不占配额，避免用户为失败请求白扣次数）
	if state != "billed" && state != "billed_recovered" {
		return
	}
	a.keyQuotaConsume(keyID, q, amount)
}

// quotaViewFor 前端展示口径：把密钥配额换算为"代理成本/零售/利润/剩余可售"四段。
// 公式（方案 B）：零售额 = 成本消耗 × 倍率；利润 = 成本消耗 × (倍率 − 1)；
// 剩余可售 = 剩余额度 × 倍率。倍率为 1 时 retail == cost、profit == 0（非代理场景不展示）。
func quotaViewFor(q *quotaInfo) map[string]any {
	if q == nil {
		return nil
	}
	remaining := q.Limit - q.Used
	if q.Limit <= 0 {
		remaining = 0
	}
	if remaining < 0 {
		remaining = 0
	}
	num, den := q.RateNum, q.RateDen
	if den <= 0 {
		den = 1
	}
	if num <= 0 {
		num = 1
	}
	// 全部整数运算（微元）：成本 × num / den，零浮点
	retail := q.Used * num / den
	profit := q.Used * (num - den) / den
	remainingRetail := remaining * num / den
	// 下次重置时刻（前端倒计时展示）
	var nextReset int64
	if start := periodStart(q.Reset, time.Now().Unix()); start > 0 {
		switch q.Reset {
		case quotaResetDaily:
			nextReset = start + 86400
		case quotaResetMonthly:
			nextReset = periodStart(quotaResetMonthly, start+32*86400)
		}
	}
	return map[string]any{
		"type": q.Type, "limit": q.Limit, "used": q.Used,
		"remaining": remaining, "reset": q.Reset, "reset_at": q.ResetAt,
		"next_reset_at": nextReset,
		"rate_num":      num, "rate_den": den,
		"expires_at": q.ExpiresAt, "note": q.Note,
		// 代理口径换算（前端「代理拿货价」视图消费）
		"cost_micro":          q.Used,
		"retail_micro":        retail,
		"profit_micro":        profit,
		"remain_retail_micro": remainingRetail,
	}
}
