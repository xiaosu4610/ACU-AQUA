// myapi.go 用户控制台数据端点（/v1/my/*）与充值（/v1/pay/*）。
// 响应形状按前端 SPA 消费契约实现（keys → {keys}, history/billing → {items,total},
// usage → {today,week,by_model}, pay/create → {out_trade_no,pay_url}）。
package httpapi

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"acu-aqua/gateway/internal/auth"
	"acu-aqua/gateway/internal/billing"
	"acu-aqua/gateway/internal/config"
)

// ---- /v1/my/* ----

// myBalance GET /v1/my/balance → {balance_micro, today_cost_micro, total_cost_micro, price_micro, prices, promo_ends_at, promo_active}
// 形状与 Rust 版 admin::my_balance 一致（前端控制台余额卡消费）
// 20260924 新增 balance2_micro（2 号折扣钱包余额，与主钱包独立）：
// 前端控制台分别展示，充值/消费都按钱包区分。
func (a *App) myBalance(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var bal, bal2 int64
	_ = a.DB.QueryRow("SELECT balance_micro, balance2_micro FROM users WHERE id=?", actx.UserID).Scan(&bal, &bal2)
	now := time.Now().Unix()
	today0 := now - (now+8*3600)%86400 // 东八区当日零点
	var todayCost, totalCost int64
	_ = a.DB.QueryRow(
		"SELECT COALESCE(SUM(unit_price_micro),0) FROM balance_flows WHERE user_id=? AND type='billed' AND ts>=?",
		actx.UserID, today0).Scan(&todayCost)
	_ = a.DB.QueryRow(
		"SELECT COALESCE(SUM(unit_price_micro),0) FROM balance_flows WHERE user_id=? AND type='billed'",
		actx.UserID).Scan(&totalCost)
	// 折扣钱包口径（wallet=2）：单独统计，控制台「折扣钱包」卡片展示专用消费
	var todayCost2, totalCost2 int64
	_ = a.DB.QueryRow(
		"SELECT COALESCE(SUM(unit_price_micro),0) FROM balance_flows WHERE user_id=? AND type='billed' AND wallet=2 AND ts>=?",
		actx.UserID, today0).Scan(&todayCost2)
	_ = a.DB.QueryRow(
		"SELECT COALESCE(SUM(unit_price_micro),0) FROM balance_flows WHERE user_id=? AND type='billed' AND wallet=2",
		actx.UserID).Scan(&totalCost2)
	// 各收费模型当前价目（pricing 表当前生效价）
	type pm struct {
		Model      string `json:"model"`
		PriceMicro int64  `json:"price_micro"`
		Mode       string `json:"mode"`
	}
	prices := []pm{}
	rows, err := a.DB.Query(
		`SELECT model, price_micro, mode FROM pricing WHERE starts_at<=? AND (ends_at IS NULL OR ends_at>?)`,
		now, now)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p pm
			_ = rows.Scan(&p.Model, &p.PriceMicro, &p.Mode)
			prices = append(prices, p)
		}
	}
	var priceMicro any
	if len(prices) > 0 {
		priceMicro = prices[0].PriceMicro
	}
	jsonOut(w, 200, map[string]any{
		"balance_micro":     bal,
		"balance2_micro":    bal2, // 2 号折扣钱包余额（与主钱包独立，不可互转）
		"today_cost_micro":  todayCost,
		"total_cost_micro":  totalCost,
		"today_cost2_micro": todayCost2, // 折扣钱包当日消费
		"total_cost2_micro": totalCost2, // 折扣钱包累计消费
		"price_micro":       priceMicro,
		"prices":            prices,
		"promo_ends_at":     0,
		"promo_active":      false,
	})
}

// myBalanceAlert GET /v1/my/balance-alert → {threshold_micro, armed, email}
func (a *App) myBalanceAlertGet(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var th int64
	var armed int
	var email string
	_ = a.DB.QueryRow("SELECT alert_threshold_micro, alert_armed, email FROM users WHERE id=?", actx.UserID).
		Scan(&th, &armed, &email)
	jsonOut(w, 200, map[string]any{"threshold_micro": th, "armed": armed == 1, "email": email})
}

func (a *App) myBalanceAlertSet(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var req struct {
		ThresholdMicro int64 `json:"threshold_micro"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errOut(w, 400, "bad_request", "请求体格式错误")
		return
	}
	if req.ThresholdMicro < 0 || req.ThresholdMicro > 100_000_000 {
		errOut(w, 400, "bad_request", "提醒阈值需为 0（关闭）~ 100 元之间的金额")
		return
	}
	if _, err := a.DB.Exec(
		"UPDATE users SET alert_threshold_micro=?, alert_armed=1, alert_last_ts=0 WHERE id=?",
		req.ThresholdMicro, actx.UserID); err != nil {
		errOut(w, 500, "internal_error", "保存失败")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "threshold_micro": req.ThresholdMicro, "message": "已保存"})
}

// myKeysGet GET /v1/my/keys → {keys:[{id,prefix,name,revoked,created_ts,can_reveal,...配额}]}
// 字段名与 Rust 版一致（前端消费 k.prefix / k.can_reveal）；
// 20260919 P4 扩展：附带密钥分发配额（限额/已用/重置/有效期/倍率）与代理利润换算。
func (a *App) myKeysGet(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	rows, err := a.DB.Query(
		`SELECT id, key_prefix, name, revoked, created_ts, key_plain_enc, COALESCE(billing_grp,''),
		        COALESCE(quota_type,''), COALESCE(quota_limit,0), COALESCE(quota_used,0),
		        COALESCE(quota_reset,''), COALESCE(quota_reset_at,0),
		        COALESCE(rate_num,1), COALESCE(rate_den,1),
		        COALESCE(expires_at,0), COALESCE(key_note,'')
		 FROM api_keys WHERE user_id=? ORDER BY created_ts DESC`,
		actx.UserID)
	if err != nil {
		errOut(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	type row struct {
		id, revoked, created                     int64
		prefix, name, enc, grp                   string
		qType, qReset, qNote                     string
		qLimit, qUsed, qResetAt, rNum, rDen, exp int64
	}
	var list []row
	for rows.Next() {
		var x row
		if rows.Scan(&x.id, &x.prefix, &x.name, &x.revoked, &x.created, &x.enc, &x.grp,
			&x.qType, &x.qLimit, &x.qUsed, &x.qReset, &x.qResetAt,
			&x.rNum, &x.rDen, &x.exp, &x.qNote) == nil {
			list = append(list, x)
		}
	}
	rows.Close() // 单连接：必须先关结果集再执行惰性重置查询
	for _, x := range list {
		q := &quotaInfo{
			Type: x.qType, Limit: x.qLimit, Used: x.qUsed, Reset: x.qReset, ResetAt: x.qResetAt,
			RateNum: x.rNum, RateDen: x.rDen, ExpiresAt: x.exp, Note: x.qNote,
		}
		// 惰性重置：跨周期则清零（列表口径与调用口径一致，用户看到的就是真实生效值）
		if start := periodStart(q.Reset, time.Now().Unix()); start > 0 && q.ResetAt < start {
			now := time.Now().Unix()
			if _, err := a.DB.Exec(
				`UPDATE api_keys SET quota_used=0, quota_reset_at=? WHERE id=? AND quota_reset_at < ?`,
				now, x.id, start); err == nil {
				q.Used = 0
				q.ResetAt = now
			}
		}
		item := map[string]any{
			"id": x.id, "prefix": x.prefix, "name": x.name, "revoked": x.revoked != 0,
			"created_ts": x.created, "can_reveal": x.enc != "", "billing_grp": x.grp,
		}
		if v := quotaViewFor(q); v != nil {
			for k, val := range v {
				item[k] = val
			}
		}
		// 有效期剩余（前端临期高亮）
		if x.exp > 0 {
			item["expires_in_sec"] = x.exp - time.Now().Unix()
		}
		items = append(items, item)
	}
	jsonOut(w, 200, map[string]any{"keys": items})
}

// myKeysCreate POST /v1/my/keys {name, billing_grp, quota_type, quota_limit, quota_reset,
// rate_num, rate_den, expires_at, note}
// billing_grp：密钥计费分组 per_call（免费+按次）| per_token（免费+按量）| 空（旧式未分组）
// 20260919 P4：创建时可一并指定分发配额（限额/重置周期/倍率/有效期/备注），全部可选。
func (a *App) myKeysCreate(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var req struct {
		Name       string `json:"name"`
		BillingGrp string `json:"billing_grp"`
		QuotaType  string `json:"quota_type"`
		QuotaLimit int64  `json:"quota_limit"`
		QuotaReset string `json:"quota_reset"`
		RateNum    int64  `json:"rate_num"`
		RateDen    int64  `json:"rate_den"`
		ExpiresAt  int64  `json:"expires_at"`
		Note       string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Name) == "" {
		req.Name = "默认密钥"
	}
	grp := config.NormalizeBillingGrp(req.BillingGrp)
	// 配额字段校验（与 myKeysQuotaSet 同口径；非法值明确报错不静默忽略）
	switch req.QuotaType {
	case quotaTypeNone, quotaTypeCount, quotaTypeAmount:
	default:
		errOut(w, 400, "bad_request", "quota_type 必须为 ''（不限）/ count（按次数）/ amount（按金额）之一")
		return
	}
	switch req.QuotaReset {
	case quotaResetNone, quotaResetDaily, quotaResetMonthly:
	default:
		errOut(w, 400, "bad_request", "quota_reset 必须为 ''（不重置）/ daily（每日）/ monthly（每月）之一")
		return
	}
	if req.QuotaLimit < 0 || req.QuotaLimit > 100_000_000_000 {
		errOut(w, 400, "bad_request", "quota_limit 需为 0 ~ 100000 元（或次数）之间")
		return
	}
	if req.RateNum < 0 || req.RateNum > 1000 || req.RateDen < 0 || req.RateDen > 1000 {
		errOut(w, 400, "bad_request", "倍率分子/分母需为 0（按 1 处理）~ 1000")
		return
	}
	if req.ExpiresAt < 0 {
		errOut(w, 400, "bad_request", "expires_at 需为 0（永久）或未来的 unix 秒")
		return
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > 120 {
		note = note[:120]
	}
	// 未设任何配额时走简化路径（保持旧行为，字段全默认）
	plain, err := auth.CreateAPIKeyFull(a.DB.DB, actx.UserID, req.Name, grp,
		req.QuotaType, req.QuotaLimit, req.QuotaReset, req.RateNum, req.RateDen, req.ExpiresAt, note)
	if err != nil {
		errOut(w, 500, "internal_error", "密钥创建失败")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "key": plain, "billing_grp": grp, "message": "密钥已创建"})
}

// myKeysGroup PATCH /v1/my/keys/{id}/group {billing_grp} → 随时切换密钥计费分组
// 合法值：per_call（免费+按次）| per_token（免费+按量）| free（纯免费，仅可调免费模型）
func (a *App) myKeysGroup(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		BillingGrp string `json:"billing_grp"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	grp := config.NormalizeBillingGrp(req.BillingGrp)
	// 拒绝空/非法分组：切换接口必须给出明确目标（per_call|per_token|free），
	// 防止前端异常请求把分组静默重置为"未分组"
	if grp == "" {
		errOut(w, 400, "bad_request", "billing_grp 必须为 per_call / per_token / free 之一")
		return
	}
	res, err := a.DB.Exec("UPDATE api_keys SET billing_grp=? WHERE id=? AND user_id=? AND revoked=0",
		grp, id, actx.UserID)
	if err != nil {
		errOut(w, 500, "internal_error", "更新失败")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		errOut(w, 404, "not_found", "密钥不存在或已吊销")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "billing_grp": grp, "message": "计费分组已更新，立即生效"})
}

// myKeysQuotaSet PATCH /v1/my/keys/{id}/quota → 密钥分发配额设置（P4）
//
// 请求体（全部可选，nil=不改动；显式传值才写）：
//
//	{quota_type:"", quota_limit:0, quota_reset:"", rate_num:1, rate_den:1, expires_at:0, note:"", action:"reset"}
//
// 口径（站长定稿方案 B）：
//   - quota_type：”=不限 / count=按次数 / amount=按金额（微元）
//   - quota_reset：”=不重置 / daily / monthly（惰性重置，东八区周期）
//   - rate_num/rate_den：分发倍率，**仅用于展示换算**（可售额=额度×倍率），不影响实扣
//   - expires_at：0=永久；>0 为到期时刻（unix 秒）
//   - action="reset"：手动清零本周期已用量
func (a *App) myKeysQuotaSet(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		errOut(w, 400, "bad_request", "密钥 ID 无效")
		return
	}
	var req struct {
		QuotaType  *string `json:"quota_type"`
		QuotaLimit *int64  `json:"quota_limit"`
		QuotaReset *string `json:"quota_reset"`
		RateNum    *int64  `json:"rate_num"`
		RateDen    *int64  `json:"rate_den"`
		ExpiresAt  *int64  `json:"expires_at"`
		Note       *string `json:"note"`
		Action     string  `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errOut(w, 400, "bad_request", "请求体格式错误")
		return
	}
	// 归属校验（同时取出当前值用于合并判定）
	var owner int64
	if err := a.DB.QueryRow("SELECT user_id FROM api_keys WHERE id=? AND revoked=0", id).Scan(&owner); err != nil {
		errOut(w, 404, "not_found", "密钥不存在或已吊销")
		return
	}
	if owner != actx.UserID {
		errOut(w, 403, "forbidden", "无权操作该密钥")
		return
	}

	// 手动重置已用量
	if req.Action == "reset" {
		if _, err := a.DB.Exec("UPDATE api_keys SET quota_used=0, quota_reset_at=? WHERE id=?", time.Now().Unix(), id); err != nil {
			errOut(w, 500, "internal_error", "重置失败")
			return
		}
		a.auditAppend("key_quota_reset", actx.UserID, "key_id="+strconv.FormatInt(id, 10), clientIP(r))
		jsonOut(w, 200, map[string]any{"ok": true, "message": "该密钥本周期用量已清零"})
		return
	}

	// 逐字段校验（非法值明确报错，不静默忽略）
	if req.QuotaType != nil {
		switch *req.QuotaType {
		case quotaTypeNone, quotaTypeCount, quotaTypeAmount:
		default:
			errOut(w, 400, "bad_request", "quota_type 必须为 ''（不限）/ count（按次数）/ amount（按金额）之一")
			return
		}
	}
	if req.QuotaReset != nil {
		switch *req.QuotaReset {
		case quotaResetNone, quotaResetDaily, quotaResetMonthly:
		default:
			errOut(w, 400, "bad_request", "quota_reset 必须为 ''（不重置）/ daily（每日）/ monthly（每月）之一")
			return
		}
	}
	if req.QuotaLimit != nil && (*req.QuotaLimit < 0 || *req.QuotaLimit > 100_000_000_000) {
		errOut(w, 400, "bad_request", "quota_limit 需为 0 ~ 100000 元（或次数）之间")
		return
	}
	if req.RateNum != nil && (*req.RateNum < 1 || *req.RateNum > 1000) {
		errOut(w, 400, "bad_request", "rate_num 需为 1 ~ 1000")
		return
	}
	if req.RateDen != nil && (*req.RateDen < 1 || *req.RateDen > 1000) {
		errOut(w, 400, "bad_request", "rate_den 需为 1 ~ 1000")
		return
	}
	if req.ExpiresAt != nil && *req.ExpiresAt < 0 {
		errOut(w, 400, "bad_request", "expires_at 需为 0（永久）或未来的 unix 秒")
		return
	}

	// 动态拼装 UPDATE（只改传入字段；nil 保持原值）
	sets := []string{}
	args := []any{}
	add := func(col string, v any) { sets = append(sets, col+"=?"); args = append(args, v) }
	if req.QuotaType != nil {
		add("quota_type", *req.QuotaType)
	}
	if req.QuotaLimit != nil {
		add("quota_limit", *req.QuotaLimit)
	}
	if req.QuotaReset != nil {
		add("quota_reset", *req.QuotaReset)
		// 切换重置周期时同步刷新基准时刻，避免新周期立即触发一次意外重置
		add("quota_reset_at", time.Now().Unix())
	}
	if req.RateNum != nil {
		add("rate_num", *req.RateNum)
	}
	if req.RateDen != nil {
		add("rate_den", *req.RateDen)
	}
	if req.ExpiresAt != nil {
		add("expires_at", *req.ExpiresAt)
	}
	if req.Note != nil {
		n := strings.TrimSpace(*req.Note)
		if len(n) > 120 {
			n = n[:120] // 防超长备注撑爆列表展示
		}
		add("key_note", n)
	}
	if len(sets) == 0 {
		errOut(w, 400, "bad_request", "未提供任何要修改的字段")
		return
	}
	args = append(args, id, actx.UserID)
	if _, err := a.DB.Exec("UPDATE api_keys SET "+strings.Join(sets, ", ")+" WHERE id=? AND user_id=?", args...); err != nil {
		errOut(w, 500, "internal_error", "保存失败")
		return
	}
	a.auditAppend("key_quota_set", actx.UserID, "key_id="+strconv.FormatInt(id, 10)+" fields="+strconv.Itoa(len(sets)), clientIP(r))

	// 回读最新配额供前端即时刷新
	q, _ := a.keyQuotaGet(id)
	jsonOut(w, 200, map[string]any{"ok": true, "quota": quotaViewFor(q), "message": "密钥配额已保存，立即生效"})
}

// myFinance GET /v1/my/finance → 财务管理中心：余额 + 消费统计 + 按模型统计 + 流水 + 充值记录
func (a *App) myFinance(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	uid := actx.UserID
	now := time.Now().Unix()
	dayStart := now - (now+8*3600)%86400 // 东八区当日零点（与 myBalance/myUsage 同口径）

	var balance int64
	_ = a.DB.QueryRow("SELECT COALESCE(balance_micro,0) FROM users WHERE id=?", uid).Scan(&balance)

	// 消费汇总（billed=1 的成功扣费请求）
	var spendToday, spendWeek, spendTotal float64
	_ = a.DB.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN ts>=? THEN bill_amount_micro END),0),
		COALESCE(SUM(CASE WHEN ts>=? THEN bill_amount_micro END),0),
		COALESCE(SUM(bill_amount_micro),0)
		FROM requests WHERE user_id=? AND billed=1`,
		dayStart, now-7*86400, uid).Scan(&spendToday, &spendWeek, &spendTotal)

	// 近 30 天按模型消费 Top10
	type modelSpend struct {
		Model  string  `json:"model"`
		Amount float64 `json:"amount_micro"`
		Calls  int64   `json:"calls"`
	}
	byModel := []modelSpend{}
	mrows, err := a.DB.Query(`SELECT model, COALESCE(SUM(bill_amount_micro),0), COUNT(*)
		FROM requests WHERE user_id=? AND billed=1 AND ts>=? GROUP BY model ORDER BY 2 DESC LIMIT 10`,
		uid, now-30*86400)
	if err == nil {
		for mrows.Next() {
			var m modelSpend
			if mrows.Scan(&m.Model, &m.Amount, &m.Calls) == nil {
				byModel = append(byModel, m)
			}
		}
		mrows.Close()
	}

	// 最近消费流水（20 条）
	type flow struct {
		Model  string  `json:"model"`
		Amount float64 `json:"amount_micro"`
		Ok     bool    `json:"ok"`
		Ts     int64   `json:"ts"`
	}
	recent := []flow{}
	frows, err := a.DB.Query(`SELECT model, bill_amount_micro, ok, ts FROM requests
		WHERE user_id=? AND billed=1 ORDER BY ts DESC LIMIT 20`, uid)
	if err == nil {
		for frows.Next() {
			var f flow
			var okI int64
			if frows.Scan(&f.Model, &f.Amount, &okI, &f.Ts) == nil {
				f.Ok = okI != 0
				recent = append(recent, f)
			}
		}
		frows.Close()
	}

	// 最近充值记录（10 条）
	type topup struct {
		Amount    float64 `json:"amount_micro"`
		Status    string  `json:"status"`
		Channel   string  `json:"channel"`
		CreatedTs int64   `json:"created_ts"`
		PaidTs    int64   `json:"paid_ts"`
	}
	topupRows := []topup{}
	prows, err := a.DB.Query(`SELECT amount_micro, status, COALESCE(channel,''), created_ts, paid_ts
		FROM payments WHERE user_id=? ORDER BY id DESC LIMIT 10`, uid)
	if err == nil {
		for prows.Next() {
			var t topup
			if prows.Scan(&t.Amount, &t.Status, &t.Channel, &t.CreatedTs, &t.PaidTs) == nil {
				topupRows = append(topupRows, t)
			}
		}
		prows.Close()
	}

	jsonOut(w, 200, map[string]any{
		"balance_micro": balance,
		"spend_today":   spendToday,
		"spend_week":    spendWeek,
		"spend_total":   spendTotal,
		"by_model":      byModel,
		"recent":        recent,
		"topups":        topupRows,
		"generated_ts":  now,
	})
}

// myKeysDelete DELETE /v1/my/keys/{id}
func (a *App) myKeysDelete(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	id := parseInt(r.PathValue("id"))
	res, _ := a.DB.Exec("UPDATE api_keys SET revoked=1 WHERE id=? AND user_id=?", id, actx.UserID)
	if n, _ := res.RowsAffected(); n == 0 {
		errOut(w, 404, "not_found", "密钥不存在")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "message": "已吊销"})
}

// myKeysReveal GET /v1/my/keys/{id}/reveal → {key}
// 明文密钥仅创建时可见；这里回读 key_plain_enc（若存有明文），否则要求重建。
func (a *App) myKeysReveal(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	id := parseInt(r.PathValue("id"))
	var plain string
	if err := a.DB.QueryRow("SELECT key_plain_enc FROM api_keys WHERE id=? AND user_id=? AND revoked=0", id, actx.UserID).Scan(&plain); err != nil {
		errOut(w, 404, "not_found", "密钥不存在")
		return
	}
	if plain == "" {
		errOut(w, 404, "not_found", "该密钥明文未存档，请吊销后新建（安全设计：明文仅创建时展示）")
		return
	}
	jsonOut(w, 200, map[string]any{"key": plain})
}

// myUsage GET /v1/my/usage → {user_id, username, today:{calls,ok_rate,avg_latency_ms}, week:{...}, by_model, recent, retention}
// 形状与 Rust 版 handle_my_usage 一致；ok_rate 一位小数
func (a *App) myUsage(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var username string
	_ = a.DB.QueryRow("SELECT username FROM users WHERE id=?", actx.UserID).Scan(&username)
	now := time.Now().Unix()
	today0 := now - (now+8*3600)%86400 // 东八区当日零点
	stats := func(from int64) map[string]any {
		var total, okn int64
		var lat float64
		_ = a.DB.QueryRow(
			"SELECT COUNT(*), COALESCE(SUM(ok),0), COALESCE(AVG(latency_ms),0) FROM requests WHERE user_id=? AND ts>=?",
			actx.UserID, from).Scan(&total, &okn, &lat)
		rate := 100.0
		if total > 0 {
			rate = float64(int64(float64(okn)*1000.0/float64(total)+0.5)) / 10.0
		}
		return map[string]any{"calls": total, "ok_rate": rate, "avg_latency_ms": int64(lat)}
	}
	byModel := []map[string]any{}
	rows, err := a.DB.Query(
		`SELECT model, COUNT(*) c FROM requests WHERE user_id=? AND ts>=? GROUP BY model ORDER BY c DESC LIMIT 10`,
		actx.UserID, now-7*86400)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var m string
			var c int64
			_ = rows.Scan(&m, &c)
			byModel = append(byModel, map[string]any{"model": m, "calls": c})
		}
	}
	recent := []map[string]any{}
	rows2, err := a.DB.Query(
		`SELECT endpoint, model, ok, latency_ms, ts, bill_amount_micro FROM requests WHERE user_id=? ORDER BY ts DESC LIMIT 50`,
		actx.UserID)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var endpoint, model string
			var ok, lat, ts, bill int64
			_ = rows2.Scan(&endpoint, &model, &ok, &lat, &ts, &bill)
			recent = append(recent, map[string]any{
				"endpoint": endpoint, "model": model, "ok": ok == 1, "latency_ms": lat, "ts": ts,
				"bill_amount_micro": bill,
			})
		}
	}
	jsonOut(w, 200, map[string]any{
		"user_id": actx.UserID, "username": username,
		"today": stats(today0), "week": stats(now - 7*86400),
		"by_model": byModel, "recent": recent,
		"retention": "调用明细保留 90 天，自动清理",
	})
}

// myHistory GET /v1/my/history?page=&page_size= → {items,total}
// items 全 18 字段（endpoint/model/ok/tokens/tps/latency/status_code/error/usage_source/ts/billed/…）
// 形状与 Rust 版 handle_my_history 一致
func (a *App) myHistory(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	page, size := pageParams2(r)
	var total int64
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM requests WHERE user_id=?", actx.UserID).Scan(&total)
	rows, err := a.DB.Query(
		`SELECT endpoint, model, ok, prompt_tokens, completion_tokens, cached_tokens, total_tokens,
		        tps, latency_ms, status_code, error, usage_source, ts,
		        billed, bill_amount_micro, balance_after_micro, bill_state, stream_mode
		 FROM requests WHERE user_id=? ORDER BY ts DESC, rowid DESC LIMIT ? OFFSET ?`,
		actx.UserID, size, (page-1)*size)
	if err != nil {
		errOut(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var pt, ct, cacheT, totalT, lat, sc, ts, bill, balAfter int64
		var tps float64
		var endpoint, model, errMsg, usageSrc, billState, streamMode string
		var ok, billed int
		_ = rows.Scan(&endpoint, &model, &ok, &pt, &ct, &cacheT, &totalT,
			&tps, &lat, &sc, &errMsg, &usageSrc, &ts,
			&billed, &bill, &balAfter, &billState, &streamMode)
		items = append(items, map[string]any{
			"endpoint": endpoint, "model": model, "ok": ok == 1,
			"prompt_tokens": pt, "completion_tokens": ct, "cached_tokens": cacheT, "total_tokens": totalT,
			"tps": tps, "latency_ms": lat, "status_code": sc, "error": errMsg,
			"usage_source": usageSrc, "ts": ts, "billed": billed == 1,
			"bill_amount_micro": bill, "balance_after_micro": balAfter,
			"bill_state": billState, "stream_mode": streamMode,
		})
	}
	jsonOut(w, 200, map[string]any{"items": items, "total": total})
}

// myBilling GET /v1/my/billing?page=&page_size= → {page,page_size,total,items}
// items 含 operator/model（LEFT JOIN requests），与 Rust 版 my_billing 一致
func (a *App) myBilling(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	page, size := pageParams2(r)
	var total int64
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM balance_flows WHERE user_id=?", actx.UserID).Scan(&total)
	rows, err := a.DB.Query(
		`SELECT f.type, f.amount_micro, f.balance_after_micro, f.unit_price_micro, f.note, f.operator, f.ts, r.model
		 FROM balance_flows f LEFT JOIN requests r ON r.rowid = f.request_id AND f.request_id > 0
		 WHERE f.user_id=? ORDER BY f.id DESC LIMIT ? OFFSET ?`,
		actx.UserID, size, (page-1)*size)
	if err != nil {
		errOut(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var ty, note, operator, model string
		var amount, after, unit, ts int64
		var modelNull sql.NullString
		_ = rows.Scan(&ty, &amount, &after, &unit, &note, &operator, &ts, &modelNull)
		if modelNull.Valid {
			model = modelNull.String
		}
		items = append(items, map[string]any{
			"type": ty, "amount_micro": amount, "balance_after_micro": after,
			"unit_price_micro": unit, "note": note, "operator": operator,
			"ts": ts, "model": model,
		})
	}
	jsonOut(w, 200, map[string]any{"page": page, "page_size": size, "total": total, "items": items})
}

// myCheckup GET /v1/my/checkup → {ok, score, items:[{id,ok,level,title,detail,advice}], generated_ts}
// 6 项检查与 Rust 版 handle_checkup 一致（前端消费 level/detail/advice）
func (a *App) myCheckup(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	const maxKeysPerUser = 20
	now := time.Now().Unix()
	var email, avatarExt string
	var created, lastLogin int64
	_ = a.DB.QueryRow("SELECT email, avatar_ext, created_ts, last_login_ts FROM users WHERE id=?", actx.UserID).
		Scan(&email, &avatarExt, &created, &lastLogin)
	var keyN, legacyN, reqN int64
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM api_keys WHERE user_id=? AND revoked=0", actx.UserID).Scan(&keyN)
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM api_keys WHERE user_id=? AND revoked=0 AND key_plain_enc=''", actx.UserID).Scan(&legacyN)
	_ = a.DB.QueryRow("SELECT COUNT(*) FROM requests WHERE user_id=? AND ts>=?", actx.UserID, now-7*86400).Scan(&reqN)

	items := []map[string]any{}
	add := func(level, id, title, detail, advice string) {
		items = append(items, map[string]any{
			"id": id, "ok": level == "ok", "level": level,
			"title": title, "detail": detail, "advice": advice,
		})
	}
	// 1. 邮箱绑定
	if email == "" {
		add("bad", "email", "邮箱绑定", "尚未绑定邮箱", "绑定邮箱可用于找回账号与接收通知")
	} else {
		add("ok", "email", "邮箱绑定", "已绑定 "+email, "")
	}
	// 2. 头像设置
	if avatarExt == "" {
		add("warn", "avatar", "头像设置", "仍在使用默认头像", "上传头像后身份更易识别，去资料卡设置")
	} else {
		add("ok", "avatar", "头像设置", "已设置 "+strings.ToUpper(avatarExt)+" 头像", "")
	}
	// 3. 有效密钥
	if keyN == 0 {
		add("bad", "keys", "密钥状态", "没有可用密钥", "没有密钥将无法调用接口，去 API 密钥区创建")
	} else if keyN >= maxKeysPerUser-1 {
		add("warn", "keys", "密钥状态", fmt.Sprintf("有效密钥 %d/%d 把，接近上限", keyN, maxKeysPerUser), "建议吊销不用的旧密钥，为新建留余量")
	} else {
		add("ok", "keys", "密钥状态", fmt.Sprintf("有效密钥 %d/%d 把", keyN, maxKeysPerUser), "")
	}
	// 4. 密钥可复制性
	if legacyN > 0 {
		add("warn", "legacy_keys", "密钥健康", fmt.Sprintf("%d 把旧密钥无法查看原文", legacyN), "旧密钥创建于升级前，建议吊销后重建以支持随时复制")
	} else {
		add("ok", "legacy_keys", "密钥健康", "全部密钥支持随时复制查看", "")
	}
	// 5. 近 7 天活跃
	if reqN == 0 {
		add("warn", "activity", "近 7 天活跃", "没有调用记录", "去文档页试试模型调用，验证密钥可用")
	} else {
		add("ok", "activity", "近 7 天活跃", fmt.Sprintf("已发起 %d 次调用", reqN), "")
	}
	// 6. 登录情况
	daysAgo := (now - lastLogin) / 86400
	switch {
	case lastLogin == 0:
		add("warn", "login", "登录情况", "账号创建后从未再次登录", "定期登录可保持会话与账号安全")
	case daysAgo > 30:
		add("warn", "login", "登录情况", fmt.Sprintf("上次登录已是 %d 天前", daysAgo), "长时间未登录建议检查密钥是否仍在自己掌控")
	default:
		add("ok", "login", "登录情况", fmt.Sprintf("账号已陪伴 %d 天，最近登录正常", (now-created)/86400+1), "")
	}

	okN := 0
	for _, it := range items {
		if it["ok"].(bool) {
			okN++
		}
	}
	score := 0
	if len(items) > 0 {
		score = okN * 100 / len(items)
	}
	jsonOut(w, 200, map[string]any{"ok": true, "score": score, "items": items, "generated_ts": now})
}

// ---- /v1/pay/* ----

// pay 单笔限额（与 Rust 版一致：0.01 ~ 1000 元）
const (
	payMinMicro = 10_000
	payMaxMicro = 1_000_000_000
)

// payOrders GET /v1/pay/orders → {items}（最近 20 条，与 Rust 版一致）
func (a *App) payOrders(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	rows, err := a.DB.Query(
		`SELECT out_trade_no, amount_micro, channel, status, trade_no, created_ts, paid_ts
		 FROM payments WHERE user_id=? ORDER BY id DESC LIMIT 20`, actx.UserID)
	if err != nil {
		errOut(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var no, channel, status, tradeNo string
		var amount, created, paid int64
		_ = rows.Scan(&no, &amount, &channel, &status, &tradeNo, &created, &paid)
		items = append(items, map[string]any{
			"out_trade_no": no, "amount_micro": amount, "channel": channel,
			"status": status, "trade_no": tradeNo, "created_ts": created, "paid_ts": paid,
		})
	}
	jsonOut(w, 200, map[string]any{"items": items})
}

// payCreate POST /v1/pay/create {amount_micro, channel, product} → {out_trade_no, pay_url, ...}
// product：balance=个人余额充值（默认）/ pool=众筹池充值（净到手注入公共池，不可退不转个人余额）
//
// 双通道（20260922 站长定稿）：**微信 → 主通道（自挂，异常自动降级易支付）；支付宝 → 恒易支付**。
// 自挂通道下单前要抢**金额互斥锁**（平台靠「通道+金额」匹配到账，同金额并存会错配，且平台
// 没有取消订单接口 → 订单必然占用该金额满 5 分钟）。抢不到时**不阻塞用户**，返回
// {queued:true, retry_after_ms} 让前端轮询重试，放行后立即下单并跳转。
func (a *App) payCreate(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var req struct {
		AmountMicro int64  `json:"amount_micro"`
		Channel     string `json:"channel"` // alipay | wxpay
		Product     string `json:"product"` // balance | wallet2 | pool
	}
	// 先 Decode 再校验：Body 只能读一次，product 白名单必须基于解析后的真实值
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errOut(w, 400, "bad_request", "请求体格式错误")
		return
	}
	if req.Product == "" {
		req.Product = "balance"
	}
	// wallet2（20260924）：充值到 2 号折扣钱包。两钱包资金**完全独立、禁止互转**——
	// 折扣钱包只能靠这种独立充值入账（站长红线）。
	if req.Product != "balance" && req.Product != "wallet2" && req.Product != "pool" {
		errOut(w, 400, "bad_request", "product 须为 balance（余额充值）/ wallet2（折扣钱包充值）/ pool（众筹池充值）")
		return
	}
	if req.AmountMicro < payMinMicro || req.AmountMicro > payMaxMicro {
		errOut(w, 400, "bad_request", "单笔金额须在 0.01 ~ 1000 元之间")
		return
	}
	if req.Channel != "alipay" && req.Channel != "wxpay" {
		errOut(w, 400, "bad_request", "支付方式仅支持支付宝 / 微信")
		return
	}
	provider := a.providerForChannel(req.Channel)
	if !a.providerConfigured(provider) {
		errOut(w, 503, "service_unavailable", "在线充值暂未开放")
		return
	}
	// 频率限制：1 小时内最多 20 单（防刷单）
	var n int64
	_ = a.DB.QueryRow(
		"SELECT COUNT(*) FROM payments WHERE user_id=? AND created_ts>=?", actx.UserID, time.Now().Unix()-3600).Scan(&n)
	if n >= 20 {
		errOut(w, 429, "too_many", "创建订单过于频繁，请稍后再试")
		return
	}

	no := genTradeNo()
	now := time.Now().Unix()
	// 自挂：平台订单只有 5 分钟窗口；**先抢金额锁再建单**（抢不到直接返回排队，不落单）
	expireTs := int64(0)
	lockHeld := false
	if provider == payProviderXiaofeng {
		expireTs = now + xiaofengOrderWindowSecs
		if !a.payLockAcquire(req.AmountMicro, no, expireTs) {
			jsonOut(w, 200, map[string]any{
				"queued": true, "retry_after_ms": 2000, "wait_secs": a.payLockWaitSecs(req.AmountMicro),
				"message": "该金额正在被另一笔订单占用，正在为你排队，请稍候…",
			})
			return
		}
		lockHeld = true
	}
	if _, err := a.DB.Exec(
		"INSERT INTO payments (out_trade_no, user_id, amount_micro, channel, product, provider, expire_ts, status, ip, created_ts) VALUES (?,?,?,?,?,?,?, 'pending', ?, ?)",
		no, actx.UserID, req.AmountMicro, req.Channel, req.Product, provider, expireTs, clientIP(r), now); err != nil {
		if lockHeld {
			a.payLockRelease(req.AmountMicro, no)
		}
		errOut(w, 500, "internal_error", "订单创建失败")
		return
	}
	// 下单（按通道分派）
	payURL, tradeNo, perr := a.createProviderOrder(provider, no, req.AmountMicro, req.Product, req.Channel, clientIP(r))
	if perr != nil {
		log.Printf("[pay] 下单失败 provider=%s no=%s: %v", provider, no, perr)
		if lockHeld {
			a.payLockRelease(req.AmountMicro, no)
		}
		_, _ = a.DB.Exec("UPDATE payments SET status='failed' WHERE out_trade_no=?", no)
		if provider == payProviderXiaofeng {
			a.payProviderFail(payProviderXiaofeng, "create_failed")
		}
		errOut(w, 502, "upstream_error", "支付通道暂时不可用，请稍后重试")
		return
	}
	// trade_no 必须落库：自挂查单（/api/pay/result）与易支付 V2 查单都只认平台单号
	if tradeNo != "" {
		if _, err := a.DB.Exec("UPDATE payments SET trade_no=? WHERE out_trade_no=?", tradeNo, no); err != nil {
			log.Printf("[pay] 保存 trade_no 失败 no=%s: %v", no, err)
		}
	}
	if provider == payProviderXiaofeng {
		a.payProviderOK(payProviderXiaofeng)
	}
	// 微信是否走了**备用通道**（主通道配的是自挂，但本次降级到了易支付）——仅供前端提示。
	// ⚠️ 不能由前端自己按 provider=epay && channel=wxpay 推断：主通道没配自挂时（provider=epay）
	// 每笔微信单都会被误标成"备用通道"（20260922 修）。
	fallback := req.Channel == "wxpay" && provider == payProviderEpay &&
		strings.EqualFold(strings.TrimSpace(a.Cfg.Pay.Provider), payProviderXiaofeng)
	jsonOut(w, 200, map[string]any{
		"out_trade_no": no, "pay_url": payURL, "trade_no": tradeNo,
		"amount_micro": req.AmountMicro, "credit_micro": req.AmountMicro, "channel": req.Channel,
		"product": req.Product, "provider": provider, "fallback": fallback,
		// 平台侧订单失效时刻（自挂只有 5 分钟窗口）：前端据此显示倒计时 + 到期一键重下单
		"expire_ts": expireTs,
	})
}

// createProviderOrder 按通道下单，返回「跳转地址 + 平台单号」。
func (a *App) createProviderOrder(provider, outTradeNo string, amountMicro int64, product, channel, clientIP string) (string, string, error) {
	if provider == payProviderXiaofeng {
		return a.xiaofengCreate(outTradeNo, amountMicro, product)
	}
	return a.epayCreateOrder(outTradeNo, amountMicro, product, channel, clientIP)
}

// epayCreateOrder 易支付下单（V2 返回 pay_type/pay_info；V1 返回收银台 URL）。
//
// 跳转地址（20260922 站长定稿：本站不做站内扫码弹窗 / iframe 嵌收银台，一律**正常跳转**）：
//   - jump   → pay_info 本身就是平台收银台 URL，直接跳；
//   - qrcode → pay_info 是二维码内容（微信 weixin://wxpay/bizpayurl?pr=… 这类 scheme），
//     桌面浏览器直接跳转无意义 → 改用平台收银台页 /pay/submit/{trade_no}/，由平台自己渲染二维码
func (a *App) epayCreateOrder(outTradeNo string, amountMicro int64, product, channel, clientIP string) (string, string, error) {
	if a.epayV2On() {
		ord, err := a.epayV2Create(outTradeNo, amountMicro, channel, product, clientIP)
		if err != nil {
			return "", "", err
		}
		payURL := ord.PayInfo
		if ord.PayType == "qrcode" {
			payURL = strings.TrimRight(a.Cfg.EPay.Gateway, "/") + "/pay/submit/" + ord.TradeNo + "/"
		}
		return payURL, ord.TradeNo, nil
	}
	payURL, err := a.epaySubmitURL(outTradeNo, amountMicro, channel, product, clientIP)
	if err != nil {
		return "", "", err
	}
	return payURL, "", nil // V1 不收平台单号：对账用 out_trade_no 查
}

// payStatus GET /v1/pay/status?out_trade_no= → {out_trade_no, status, amount_micro, credit_micro, balance_micro}
// pending 超 10 秒时向上游查单补账（漏单自愈，与 Rust 版一致）
func (a *App) payStatus(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	no := r.URL.Query().Get("out_trade_no")
	if no == "" {
		errOut(w, 400, "bad_request", "缺少订单号")
		return
	}
	var amount, created, expireTs int64
	var status, provider string
	if err := a.DB.QueryRow(
		`SELECT amount_micro, status, created_ts, COALESCE(expire_ts,0), COALESCE(provider,'epay')
		 FROM payments WHERE out_trade_no=? AND user_id=?`, no, actx.UserID).
		Scan(&amount, &status, &created, &expireTs, &provider); err != nil {
		errOut(w, 404, "not_found", "订单不存在")
		return
	}
	// 漏单自愈：pending 且创建超 10 秒 → 主动向上游查单。
	// ⚠️ 对自挂通道这一步是**硬兜底**：实测平台回调只发一次、6 分钟零重试，回调一丢就永久漏单。
	if status == "pending" && time.Now().Unix()-created > 10 && a.providerConfigured(provider) {
		_ = a.payQueryAndSettle(provider, no, amount)
		_ = a.DB.QueryRow("SELECT status FROM payments WHERE out_trade_no=?", no).Scan(&status)
	}
	var bal, bal2 int64
	_ = a.DB.QueryRow("SELECT balance_micro, balance2_micro FROM users WHERE id=?", actx.UserID).Scan(&bal, &bal2)
	// pool 单：附带众筹池余额（前端充值弹窗轮询展示）
	var poolBal int64
	var product string
	_ = a.DB.QueryRow("SELECT COALESCE(product,'balance') FROM payments WHERE out_trade_no=?", no).Scan(&product)
	if product == "pool" {
		_ = a.DB.QueryRow("SELECT balance_micro FROM pool_wallet WHERE id=?", poolID).Scan(&poolBal)
	}
	// balance_micro 按**本单入账钱包**取值（20260924）：wallet2 单返回折扣钱包余额，
	// 否则前端轮询到账后会把折扣钱包余额显示成主钱包余额
	if product == "wallet2" {
		bal = bal2
	}
	jsonOut(w, 200, map[string]any{
		"out_trade_no": no, "status": status,
		"amount_micro": amount, "credit_micro": amount,
		// balance_micro 按**本单入账钱包**取值（20260924）：wallet2 单返回折扣钱包余额，
		// 否则前端轮询到账后会把折扣钱包余额显示成主钱包余额
		"balance_micro": bal,
		"product":       product, "pool_balance_micro": poolBal,
		// provider/expire_ts：前端据此显示"支付通道"与自挂 5 分钟倒计时（到期给一键重下单）
		"provider": provider, "expire_ts": expireTs,
	})
}

// payQueryAndSettle 按通道分派查单补账（对账任务与用户端「我已支付立即查询」共用）。
func (a *App) payQueryAndSettle(provider, outTradeNo string, amountMicro int64) bool {
	if provider == payProviderXiaofeng {
		return a.xiaofengQueryAndSettle(outTradeNo, amountMicro)
	}
	return a.epayQueryAndSettle(outTradeNo, amountMicro)
}

// epayQueryAndSettle 主动向上游查单（api.php?act=order），已支付则补账。
// 返回 true 表示本轮已补账（供对账任务统计）。
func (a *App) epayQueryAndSettle(outTradeNo string, amountMicro int64) bool {
	if a.epayV2On() {
		return a.epayV2QueryAndSettle(outTradeNo, amountMicro)
	}
	q := url.Values{}
	q.Set("act", "order")
	q.Set("pid", a.Cfg.EPay.PID)
	q.Set("key", a.Cfg.EPay.Key)
	q.Set("out_trade_no", outTradeNo)
	resp, err := http.Get(strings.TrimRight(a.Cfg.EPay.Gateway, "/") + "/api.php?" + q.Encode())
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false
	}
	var j struct {
		Code    int    `json:"code"`
		Status  int    `json:"status"`
		Money   string `json:"money"`
		TradeNo string `json:"trade_no"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return false
	}
	if j.Code == 1 && j.Status == 1 {
		if remote := yuanToMicro(j.Money); remote == amountMicro {
			a.epaySettle(outTradeNo, j.TradeNo)
			return true
		}
		log.Printf("[pay] 查单金额不符 no=%s remote=%d local=%d", outTradeNo, yuanToMicro(j.Money), amountMicro)
	}
	return false
}

// yuanToMicro "12.34" → 12_340_000（解析失败返回 -1）
func yuanToMicro(s string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 {
		return -1
	}
	return int64(f*1_000_000 + 0.5)
}

// payNotify GET/POST /v1/pay/notify（异步回调：验签 + pid/金额/通道三方校验 → 幂等入账）
// 20260919 加固：回调来源仅作参考（不依赖域名白名单，上游可能从任意出口回调），
// 安全性由验签 + pid + 金额三方比对保证——任何一环不符都不入账。
// 20260921：易支付验签按 api_version 分流（V2 RSA / V1 MD5）。
// 20260922 双通道：按 **pid** 判定是哪条通道（自挂/易支付），各自用对应密钥验签；
//
//	自挂实测是 **GET + query string**，成功标识 **trade_status=TRADE_SUCCESS**（易支付 V1 口径），
//	必须回纯文本 success。
func (a *App) payNotify(w http.ResponseWriter, r *http.Request) {
	// 同时兼容 form body 与 query string（自挂实测是 GET query string）
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(400)
		_, _ = w.Write([]byte("fail"))
		return
	}
	form := r.Form
	if !a.epayConfigured() && !a.xiaofengConfigured() {
		w.WriteHeader(503)
		_, _ = w.Write([]byte("fail"))
		return
	}
	// 按 pid 判定通道（两条通道 pid 不同）
	provider := ""
	switch {
	case a.xiaofengConfigured() && form.Get("pid") == a.Cfg.Pay.Xiaofeng.PID:
		provider = payProviderXiaofeng
	case a.epayConfigured() && form.Get("pid") == a.Cfg.EPay.PID:
		provider = payProviderEpay
	default:
		log.Printf("[pay] 回调 pid 不匹配 pid=%s from=%s", form.Get("pid"), clientIP(r))
		w.WriteHeader(400)
		_, _ = w.Write([]byte("fail"))
		return
	}
	verified, paid := false, false
	if provider == payProviderXiaofeng {
		verified, paid = a.xiaofengVerifyNotify(form), a.xiaofengNotifyPaid(form)
	} else {
		verified, paid = a.epayVerifyNotify(form), a.epayNotifyPaid(form)
	}
	if !verified {
		log.Printf("[pay] 回调验签失败 provider=%s out_trade_no=%s from=%s", provider, form.Get("out_trade_no"), clientIP(r))
		w.WriteHeader(400)
		_, _ = w.Write([]byte("fail"))
		return
	}
	if !paid {
		_, _ = w.Write([]byte("success")) // 非成功状态：确认收到但不入账
		return
	}
	outTradeNo := form.Get("out_trade_no")
	// 订单为准做三方比对：金额（防篡改）+ 通道（防串通道：自挂单不能被易支付回调核销，反之亦然）
	var orderMicro int64
	var orderProvider, orderStatus string
	err := a.DB.QueryRow("SELECT amount_micro, COALESCE(provider,'epay'), status FROM payments WHERE out_trade_no=?",
		outTradeNo).Scan(&orderMicro, &orderProvider, &orderStatus)
	notifyMicro := yuanToMicro(form.Get("money"))
	if err != nil || notifyMicro != orderMicro || orderProvider != provider {
		log.Printf("[pay] 回调与订单不符 provider=%s no=%s notify=%d order=%d orderProvider=%s err=%v",
			provider, outTradeNo, notifyMicro, orderMicro, orderProvider, err)
		w.WriteHeader(400)
		_, _ = w.Write([]byte("fail"))
		return
	}
	a.epaySettle(outTradeNo, form.Get("trade_no"))
	if provider == payProviderXiaofeng {
		// 到账即释放金额锁（该档位立刻可服务下一位）+ 记通道健康（清熔断）
		a.payLockRelease(orderMicro, outTradeNo)
		a.payProviderOK(payProviderXiaofeng)
	}
	_, _ = w.Write([]byte("success"))
}

// topupNote 充值流水备注（按产品区分，对账时一眼看出钱进了哪个钱包）
func topupNote(product string) string {
	if product == "wallet2" {
		return "折扣钱包充值"
	}
	return "在线充值"
}

// epaySettle 入账（幂等：仅 pending → paid 一次性入账，事务）。
// product 分流：balance → 主钱包；wallet2 → 2 号折扣钱包（20260924）；pool → 众筹池
func (a *App) epaySettle(outTradeNo, tradeNo string) {
	var uid, amount int64
	var status, product string
	err := a.DB.QueryRow("SELECT user_id, amount_micro, status, COALESCE(product,'balance') FROM payments WHERE out_trade_no=?", outTradeNo).
		Scan(&uid, &amount, &status, &product)
	if err != nil || status != "pending" {
		return
	}
	now := time.Now().Unix()
	tx, err := a.DB.DB.Begin()
	if err != nil {
		return
	}
	res, err := tx.Exec("UPDATE payments SET status='paid', trade_no=?, paid_ts=? WHERE out_trade_no=? AND status='pending'",
		tradeNo, now, outTradeNo)
	if err != nil {
		_ = tx.Rollback()
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_ = tx.Rollback()
		return
	}
	if product == "pool" {
		// 众筹池充值（20260921 恢复对外开放）：直接注入公共池，不进个人余额
		if err := poolChargeTx(tx, uid, amount, "众筹充值"); err != nil {
			_ = tx.Rollback()
			return
		}
		if err := tx.Commit(); err != nil {
			return
		}
		a.auditAppend("pool_recharge", uid, "amount="+strconv.FormatInt(amount, 10), "")
		return
	}
	// 入账钱包（20260924）：balance → 主钱包；wallet2 → 折扣钱包（独立充值，禁止互转）
	w := billing.WalletMain
	if product == "wallet2" {
		w = billing.WalletDiscount
	}
	col := billing.WalletColumn(w)
	if _, err := tx.Exec("UPDATE users SET "+col+"="+col+"+? WHERE id=?", amount, uid); err != nil {
		_ = tx.Rollback()
		return
	}
	var balance int64
	_ = tx.QueryRow("SELECT "+col+" FROM users WHERE id=?", uid).Scan(&balance)
	// 流水 wallet 列：折扣钱包充值必须记 wallet=2，否则对账时会把折扣余额当成主钱包余额
	if _, err := tx.Exec(
		`INSERT INTO balance_flows (user_id, request_id, type, amount_micro, balance_before_micro, balance_after_micro, unit_price_micro, note, operator, ts, wallet)
		 VALUES (?,0,'topup',?,?,?,0,?,'system',?,?)`,
		uid, amount, balance-amount, balance, topupNote(product), now, w); err != nil {
		_ = tx.Rollback()
		return
	}
	_ = tx.Commit()
	// 邀请返利钩子：被邀请人首充达标（≥¥5）→ 邀请人 ¥2 奖 + 被邀请人加赠 10%（幂等，invite.go）
	a.inviteOnFirstPay(uid, amount)
}

// epaySubmitURL 向易支付 mapi.php 建单（POST + clientip），返回用户侧收银台链接。
// 20260918 通道 xnoo：submit.php GET 跳转要求商户后台单独配置"支付接口商户"（未配置返回错误页），
// mapi.php + clientip 直连生效；仅回 payurl 的直接用，仅回 qrcode 的取平台收银台 /pay/submit/{trade_no}/。
func (a *App) epaySubmitURL(outTradeNo string, amountMicro int64, channel, product, clientIP string) (string, error) {
	money := fmt.Sprintf("%.2f", float64(amountMicro)/1_000_000)
	name := "余额充值"
	if product == "pool" {
		name = "众筹池充值"
	} else if product == "wallet2" {
		name = "折扣钱包充值" // 20260924：2 号折扣钱包独立充值
	}
	params := map[string]string{
		"pid":          a.Cfg.EPay.PID,
		"type":         channel,
		"out_trade_no": outTradeNo,
		"notify_url":   a.Cfg.EPay.NotifyBase + "/v1/pay/notify",
		"return_url":   a.Cfg.EPay.ReturnBase + "/pay/return",
		"name":         name,
		"money":        money,
		"clientip":     clientIP,
	}
	params["sign"] = a.epaySign(params)
	params["sign_type"] = "MD5"
	v := url.Values{}
	for k, val := range params {
		v.Set(k, val)
	}
	resp, err := http.PostForm(strings.TrimRight(a.Cfg.EPay.Gateway, "/")+"/mapi.php", v)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	var j struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		TradeNo string `json:"trade_no"`
		PayURL  string `json:"payurl"`
		QRCode  string `json:"qrcode"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return "", fmt.Errorf("mapi 响应异常: %w", err)
	}
	if j.Code != 1 {
		return "", fmt.Errorf("mapi code=%d msg=%s", j.Code, j.Msg)
	}
	if j.PayURL != "" {
		return j.PayURL, nil
	}
	if j.TradeNo != "" {
		return strings.TrimRight(a.Cfg.EPay.Gateway, "/") + "/pay/submit/" + j.TradeNo + "/", nil
	}
	return "", fmt.Errorf("mapi 未返回 payurl/trade_no")
}

// epaySign 易支付 MD5 签名。口径与自挂通道**完全一致**（实测逐字节验证），
// 故统一走 payMD5Sign（见 xiaofeng.go），避免两处实现漂移。
func (a *App) epaySign(params map[string]string) string {
	return payMD5Sign(a.Cfg.EPay.Key, params)
}

// epayVerifySign 回调验签（同签名算法）
func (a *App) epayVerifySign(form url.Values) bool {
	if form.Get("pid") != a.Cfg.EPay.PID {
		return false
	}
	got := form.Get("sign")
	if got == "" {
		return false
	}
	params := map[string]string{}
	for k := range form {
		params[k] = form.Get(k)
	}
	return a.epaySign(params) == got
}

// genTradeNo 商户单号：日期 + 随机
func genTradeNo() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102150405") + hex.EncodeToString(b)
}

// pageParams2 page/page_size 分页（前端 my/history 与 my/billing 口径）
func pageParams2(r *http.Request) (page, size int64) {
	page, size = 1, 20
	if v := parseInt(r.URL.Query().Get("page")); v > 0 {
		page = v
	}
	if v := parseInt(r.URL.Query().Get("page_size")); v > 0 && v <= 100 {
		size = v
	}
	return
}

// clientIP 客户端 IP（nginx X-Real-IP 优先）
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if i := strings.LastIndex(r.RemoteAddr, ":"); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}
