// myapi.go 用户控制台数据端点（/v1/my/*）与充值（/v1/pay/*）。
// 响应形状按前端 SPA 消费契约实现（keys → {keys}, history/billing → {items,total},
// usage → {today,week,by_model}, pay/create → {out_trade_no,pay_url}）。
package httpapi

import (
	"crypto/md5"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"acu-aqua/gateway/internal/auth"
	"acu-aqua/gateway/internal/config"
)

// ---- /v1/my/* ----

// myBalance GET /v1/my/balance → {balance_micro, today_cost_micro, total_cost_micro, price_micro, prices, promo_ends_at, promo_active}
// 形状与 Rust 版 admin::my_balance 一致（前端控制台余额卡消费）
func (a *App) myBalance(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var bal int64
	_ = a.DB.QueryRow("SELECT balance_micro FROM users WHERE id=?", actx.UserID).Scan(&bal)
	now := time.Now().Unix()
	today0 := now - (now+8*3600)%86400 // 东八区当日零点
	var todayCost, totalCost int64
	_ = a.DB.QueryRow(
		"SELECT COALESCE(SUM(unit_price_micro),0) FROM balance_flows WHERE user_id=? AND type='billed' AND ts>=?",
		actx.UserID, today0).Scan(&todayCost)
	_ = a.DB.QueryRow(
		"SELECT COALESCE(SUM(unit_price_micro),0) FROM balance_flows WHERE user_id=? AND type='billed'",
		actx.UserID).Scan(&totalCost)
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
		"balance_micro":    bal,
		"today_cost_micro": todayCost,
		"total_cost_micro": totalCost,
		"price_micro":      priceMicro,
		"prices":           prices,
		"promo_ends_at":    0,
		"promo_active":     false,
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

// myKeysGet GET /v1/my/keys → {keys:[{id,prefix,name,revoked,created_ts,can_reveal}]}
// 字段名与 Rust 版一致（前端消费 k.prefix / k.can_reveal）
func (a *App) myKeysGet(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	rows, err := a.DB.Query(
		"SELECT id, key_prefix, name, revoked, created_ts, key_plain_enc, COALESCE(billing_grp,'') FROM api_keys WHERE user_id=? ORDER BY created_ts DESC",
		actx.UserID)
	if err != nil {
		errOut(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, revoked, created int64
		var prefix, name, enc, grp string
		_ = rows.Scan(&id, &prefix, &name, &revoked, &created, &enc, &grp)
		items = append(items, map[string]any{
			"id": id, "prefix": prefix, "name": name, "revoked": revoked != 0,
			"created_ts": created, "can_reveal": enc != "", "billing_grp": grp,
		})
	}
	jsonOut(w, 200, map[string]any{"keys": items})
}

// myKeysCreate POST /v1/my/keys {name, billing_grp}
// billing_grp：密钥计费分组 per_call（免费+按次）| per_token（免费+按量）| 空（旧式未分组）
func (a *App) myKeysCreate(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	var req struct {
		Name       string `json:"name"`
		BillingGrp string `json:"billing_grp"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Name) == "" {
		req.Name = "默认密钥"
	}
	grp := config.NormalizeBillingGrp(req.BillingGrp)
	plain, err := auth.CreateAPIKey(a.DB.DB, actx.UserID, req.Name, grp)
	if err != nil {
		errOut(w, 500, "internal_error", "密钥创建失败")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "key": plain, "billing_grp": grp, "message": "密钥已创建"})
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
		`SELECT endpoint, model, ok, latency_ms, ts FROM requests WHERE user_id=? ORDER BY ts DESC LIMIT 50`,
		actx.UserID)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var endpoint, model string
			var ok, lat, ts int64
			_ = rows2.Scan(&endpoint, &model, &ok, &lat, &ts)
			recent = append(recent, map[string]any{
				"endpoint": endpoint, "model": model, "ok": ok == 1, "latency_ms": lat, "ts": ts,
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

// payCreate POST /v1/pay/create {amount_micro, channel} → {out_trade_no, pay_url, amount_micro, credit_micro, channel}
func (a *App) payCreate(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	if a.Cfg.EPay.Gateway == "" || a.Cfg.EPay.PID == "" || a.Cfg.EPay.Key == "" {
		errOut(w, 503, "service_unavailable", "在线充值暂未开放")
		return
	}
	var req struct {
		AmountMicro int64  `json:"amount_micro"`
		Channel     string `json:"channel"` // alipay | wxpay
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AmountMicro < payMinMicro || req.AmountMicro > payMaxMicro {
		errOut(w, 400, "bad_request", "单笔金额须在 0.01 ~ 1000 元之间")
		return
	}
	if req.Channel != "alipay" && req.Channel != "wxpay" {
		errOut(w, 400, "bad_request", "支付方式仅支持支付宝 / 微信")
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
	if _, err := a.DB.Exec(
		"INSERT INTO payments (out_trade_no, user_id, amount_micro, channel, status, ip, created_ts) VALUES (?,?,?,?, 'pending', ?, ?)",
		no, actx.UserID, req.AmountMicro, req.Channel, clientIP(r), now); err != nil {
		errOut(w, 500, "internal_error", "订单创建失败")
		return
	}
	payURL := a.epaySubmitURL(no, req.AmountMicro, req.Channel)
	jsonOut(w, 200, map[string]any{
		"out_trade_no": no, "pay_url": payURL,
		"amount_micro": req.AmountMicro, "credit_micro": req.AmountMicro, "channel": req.Channel,
	})
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
	var amount, created int64
	var status string
	if err := a.DB.QueryRow(
		"SELECT amount_micro, status, created_ts FROM payments WHERE out_trade_no=? AND user_id=?", no, actx.UserID).
		Scan(&amount, &status, &created); err != nil {
		errOut(w, 404, "not_found", "订单不存在")
		return
	}
	// 漏单自愈：pending 且创建超 10 秒 → 主动向上游查单
	if status == "pending" && time.Now().Unix()-created > 10 && a.Cfg.EPay.Gateway != "" {
		a.epayQueryAndSettle(no, amount)
		_ = a.DB.QueryRow("SELECT status FROM payments WHERE out_trade_no=?", no).Scan(&status)
	}
	var bal int64
	_ = a.DB.QueryRow("SELECT balance_micro FROM users WHERE id=?", actx.UserID).Scan(&bal)
	jsonOut(w, 200, map[string]any{
		"out_trade_no": no, "status": status,
		"amount_micro": amount, "credit_micro": amount, "balance_micro": bal,
	})
}

// epayQueryAndSettle 主动向上游查单（api.php?act=order），已支付则补账
func (a *App) epayQueryAndSettle(outTradeNo string, amountMicro int64) {
	q := url.Values{}
	q.Set("act", "order")
	q.Set("pid", a.Cfg.EPay.PID)
	q.Set("key", a.Cfg.EPay.Key)
	q.Set("out_trade_no", outTradeNo)
	resp, err := http.Get(strings.TrimRight(a.Cfg.EPay.Gateway, "/") + "/api.php?" + q.Encode())
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return
	}
	var j struct {
		Code     int    `json:"code"`
		Status   int    `json:"status"`
		Money    string `json:"money"`
		TradeNo  string `json:"trade_no"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return
	}
	if j.Code == 1 && j.Status == 1 {
		if remote := yuanToMicro(j.Money); remote == amountMicro {
			a.epaySettle(outTradeNo, j.TradeNo)
		}
	}
}

// yuanToMicro "12.34" → 12_340_000（解析失败返回 -1）
func yuanToMicro(s string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 {
		return -1
	}
	return int64(f*1_000_000 + 0.5)
}

// payNotify GET/POST /v1/pay/notify（EPay 异步回调，MD5 验签 + pid/金额校验 → 幂等入账）
func (a *App) payNotify(w http.ResponseWriter, r *http.Request) {
	if a.Cfg.EPay.Gateway == "" || a.Cfg.EPay.PID == "" || a.Cfg.EPay.Key == "" {
		w.WriteHeader(503)
		_, _ = w.Write([]byte("fail"))
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(400)
		_, _ = w.Write([]byte("fail"))
		return
	}
	form := r.Form
	// EPay 回调字段：pid/type/out_trade_no/trade_no/name/money/trade_status/sign/sign_type
	if form.Get("pid") != a.Cfg.EPay.PID {
		w.WriteHeader(400)
		_, _ = w.Write([]byte("fail"))
		return
	}
	if !a.epayVerifySign(form) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte("fail"))
		return
	}
	if form.Get("trade_status") != "TRADE_SUCCESS" {
		_, _ = w.Write([]byte("success")) // 非成功状态确认收到但不入账
		return
	}
	outTradeNo := form.Get("out_trade_no")
	// 金额比对（订单为准，防篡改）
	notifyMicro := yuanToMicro(form.Get("money"))
	var orderMicro int64
	err := a.DB.QueryRow("SELECT amount_micro FROM payments WHERE out_trade_no=?", outTradeNo).Scan(&orderMicro)
	if err != nil || notifyMicro != orderMicro {
		w.WriteHeader(400)
		_, _ = w.Write([]byte("fail"))
		return
	}
	a.epaySettle(outTradeNo, form.Get("trade_no"))
	_, _ = w.Write([]byte("success"))
}

// epaySettle 入账（幂等：仅 pending → paid 一次性入账，事务）
func (a *App) epaySettle(outTradeNo, tradeNo string) {
	var uid, amount int64
	var status string
	err := a.DB.QueryRow("SELECT user_id, amount_micro, status FROM payments WHERE out_trade_no=?", outTradeNo).
		Scan(&uid, &amount, &status)
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
	if _, err := tx.Exec("UPDATE users SET balance_micro=balance_micro+? WHERE id=?", amount, uid); err != nil {
		_ = tx.Rollback()
		return
	}
	var balance int64
	_ = tx.QueryRow("SELECT balance_micro FROM users WHERE id=?", uid).Scan(&balance)
	if _, err := tx.Exec(
		`INSERT INTO balance_flows (user_id, request_id, type, amount_micro, balance_before_micro, balance_after_micro, unit_price_micro, note, operator, ts)
		 VALUES (?,0,'topup',?,?,?,0,'在线充值','system',?)`,
		uid, amount, balance-amount, balance, now); err != nil {
		_ = tx.Rollback()
		return
	}
	_ = tx.Commit()
}

// epaySubmitURL 构造易支付跳转链接（MD5 签名，参数排序拼接）
func (a *App) epaySubmitURL(outTradeNo string, amountMicro int64, channel string) string {
	money := fmt.Sprintf("%.2f", float64(amountMicro)/1_000_000)
	params := map[string]string{
		"pid":          a.Cfg.EPay.PID,
		"type":         channel,
		"out_trade_no": outTradeNo,
		"notify_url":   a.Cfg.EPay.NotifyBase + "/v1/pay/notify",
		"return_url":   a.Cfg.EPay.ReturnBase + "/pay/return",
		"name":         "余额充值",
		"money":        money,
	}
	params["sign"] = a.epaySign(params)
	params["sign_type"] = "MD5"
	v := url.Values{}
	for k, val := range params {
		v.Set(k, val)
	}
	return strings.TrimRight(a.Cfg.EPay.Gateway, "/") + "/submit.php?" + v.Encode()
}

// epaySign 易支付 MD5 签名：参数名 ASCII 升序 k=v& 连接（跳过 sign/sign_type/空值）+ 密钥
func (a *App) epaySign(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sign" || k == "sign_type" || params[k] == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+params[k])
	}
	sum := md5.Sum([]byte(strings.Join(pairs, "&") + a.Cfg.EPay.Key))
	return hex.EncodeToString(sum[:])
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
