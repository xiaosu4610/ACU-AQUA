// 管理后台数据端点：/v1/admin/stats、/v1/admin/quota*、/v1/admin/supervision。
// 响应形状对齐 Rust 版 admin.rs；数据口径说明：
//   - 上游池（upstream_quota provider='pool'）以"微元"记账（total_calls 列存微元总额，沿用 Rust 字段名）
//   - 按次通道（acu/acu2）以"次"记账，cost_per_call_micro 为单次成本
//   - tide 面值台账复用 line_keys（line_id='tide'）
package httpapi

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const beijingOffset = int64(28800) // UTC+8

// paidLineCond 计费线归属 SQL 条件（机制化，线 ID 来自配置）：
// 统一前缀时代按 requests.resolved_line 归属实际线；旧数据（resolved_line=”）按模型前缀推断。
func (a *App) paidLineCond(mode string) string {
	id := ""
	if l := a.lineForMode(mode); l != nil {
		id = l.ID
	}
	return fmt.Sprintf("(resolved_line='%s' OR (COALESCE(resolved_line,'')='' AND model LIKE '%s/%%'))", id, id)
}

// paidLinePrefix 计费线模型前缀（Go 内存过滤用）
func (a *App) paidLinePrefix(mode string) string {
	if l := a.lineForMode(mode); l != nil {
		return l.ID + "/"
	}
	return "\x00none/"
}

func adminJSON(w http.ResponseWriter, v map[string]any) {
	jsonOut(w, 200, v)
}

// queryInt64 单值查询（失败/NULL 返回 def）
func (a *App) queryInt64(q string, args ...any) int64 {
	var v sql.NullInt64
	_ = a.DB.QueryRow(q, args...).Scan(&v)
	if !v.Valid {
		return 0
	}
	return v.Int64
}

// —— GET /v1/admin/stats ——
func (a *App) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	now := time.Now().Unix()
	today0 := (now+beijingOffset)/86400*86400 - beijingOffset
	week0 := today0 - 6*86400
	month0 := today0 - 29*86400

	// 收入（billed 流水）
	incomeAll := a.queryInt64(`SELECT COALESCE(SUM(unit_price_micro),0) FROM balance_flows WHERE type='billed'`)
	incomeToday := a.queryInt64(`SELECT COALESCE(SUM(unit_price_micro),0) FROM balance_flows WHERE type='billed' AND ts>=?`, today0)
	incomeWeek := a.queryInt64(`SELECT COALESCE(SUM(unit_price_micro),0) FROM balance_flows WHERE type='billed' AND ts>=?`, week0)
	incomeMonth := a.queryInt64(`SELECT COALESCE(SUM(unit_price_micro),0) FROM balance_flows WHERE type='billed' AND ts>=?`, month0)

	// 上游池（微元口径）
	poolTotal := a.queryInt64(`SELECT total_calls FROM upstream_quota WHERE provider='pool'`)
	poolUsed := a.queryInt64(`SELECT used_calls FROM upstream_quota WHERE provider='pool'`)
	circuitOpen := a.queryInt64(`SELECT circuit_open FROM upstream_quota WHERE provider='pool'`) != 0
	poolRemain := poolTotal - poolUsed
	costPerCall := a.queryInt64(`SELECT cost_per_call_micro FROM upstream_quota WHERE provider='acu'`)
	if costPerCall == 0 {
		costPerCall = a.perCallCostDefault()
	}
	topupTotal := a.queryInt64(`SELECT COALESCE(SUM(amount_micro),0) FROM upstream_topups`)

	// 近 7 日按次成本（按模型单价 × 调用量，价格取配置成本台账）
	cost7d := int64(0)
	if rows, err := a.DB.Query(
		`SELECT model, COUNT(*) FROM requests WHERE `+a.paidLineCond("per_call")+` AND bill_state='billed' AND ts>=? GROUP BY model`, week0); err == nil {
		for rows.Next() {
			var model string
			var n int64
			if rows.Scan(&model, &n) == nil {
				cost7d += a.perCallCostFor(model) * n
			}
		}
		rows.Close()
	}
	// 全期按次成本（同口径去掉时间条件）：利润必须全期收入对全期成本，7 日成本只用于日均燃烧
	costAll := int64(0)
	if rows, err := a.DB.Query(
		`SELECT model, COUNT(*) FROM requests WHERE ` + a.paidLineCond("per_call") + ` AND bill_state='billed' GROUP BY model`); err == nil {
		for rows.Next() {
			var model string
			var n int64
			if rows.Scan(&model, &n) == nil {
				costAll += a.perCallCostFor(model) * n
			}
		}
		rows.Close()
	}
	costBilled := cost7d // 已计费口径成本（近 7 日）
	dailyCost := cost7d / 7
	daysLeft := int64(-1)
	if dailyCost > 0 {
		daysLeft = poolRemain / dailyCost
	}

	// 手续费（支付通道费）
	feeSum := a.queryInt64(`SELECT COALESCE(SUM(fee_micro),0) FROM payments WHERE status='paid'`)
	amountSum := a.queryInt64(`SELECT COALESCE(SUM(amount_micro),0) FROM payments WHERE status='paid'`)
	feeRate := 0.0
	if amountSum > 0 {
		feeRate = float64(feeSum) / float64(amountSum)
	}
	feeEst := int64(float64(incomeAll) * feeRate)

	income := map[string]any{
		"all_micro": incomeAll, "today_micro": incomeToday,
		"week_micro": incomeWeek, "month_micro": incomeMonth,
		"promo_micro": 0, "normal_micro": incomeAll,
	}
	upstream := map[string]any{
		"total_micro": poolTotal, "used_micro": poolUsed, "remain_micro": poolRemain,
		"cost_per_call_micro": costPerCall, "topup_total_micro": topupTotal,
		"cost_micro": cost7d, "cost_billed_micro": costBilled,
		"fee_micro": feeSum, "fee_rate": feeRate, "fee_est_micro": feeEst,
		"profit_micro":     incomeAll - costAll,
		"profit_net_micro": incomeAll - costAll - feeEst,
		"circuit_open":     circuitOpen, "days_left": daysLeft,
	}

	// 调用量
	callWindows := func(from int64) (total, ok int64) {
		_ = a.DB.QueryRow(
			"SELECT COUNT(*), COALESCE(SUM(ok),0) FROM requests WHERE bill_state!='' AND ts>=?", from,
		).Scan(&total, &ok)
		return
	}
	var allT, allOK int64
	_ = a.DB.QueryRow("SELECT COUNT(*), COALESCE(SUM(ok),0) FROM requests WHERE bill_state!=''").Scan(&allT, &allOK)
	todayT, todayOK := callWindows(today0)
	weekT, weekOK := callWindows(week0)
	monthT, monthOK := callWindows(month0)
	failDist := []map[string]any{}
	if rows, err := a.DB.Query(
		`SELECT COALESCE(NULLIF(error,''),'unknown'), COUNT(*) FROM requests WHERE bill_state!='' AND ok=0 GROUP BY 1 ORDER BY 2 DESC LIMIT 10`); err == nil {
		for rows.Next() {
			var reason string
			var n int64
			if rows.Scan(&reason, &n) == nil {
				failDist = append(failDist, map[string]any{"reason": reason, "count": n})
			}
		}
		rows.Close()
	}

	// 用户负债（20260924：含 2 号折扣钱包——两钱包都是站方对用户的真实负债）
	// 注意：分母口径仍是"有哪些钱包"，只要任一钱包有余额就计入持仓人数
	liability := a.queryInt64(`SELECT COALESCE(SUM(balance_micro + balance2_micro),0) FROM users WHERE balance_micro>0 OR balance2_micro>0`)
	withBalance := a.queryInt64(`SELECT COUNT(*) FROM users WHERE balance_micro>0 OR balance2_micro>0`)

	// tide 专线（按量口径：resolved_line 归属实际按量线，旧数据按前缀推断）
	tideIncome := a.queryInt64(`SELECT COALESCE(SUM(b.unit_price_micro),0) FROM balance_flows b JOIN requests r ON r.rowid=b.request_id WHERE b.type='billed' AND r.` + a.paidLineCond("per_token"))
	tideIncomeToday := a.queryInt64(`SELECT COALESCE(SUM(b.unit_price_micro),0) FROM balance_flows b JOIN requests r ON r.rowid=b.request_id WHERE b.type='billed' AND r.`+a.paidLineCond("per_token")+` AND b.ts>=?`, today0)
	tideCalls := a.queryInt64(`SELECT COUNT(*) FROM requests WHERE ` + a.paidLineCond("per_token") + ` AND bill_state='billed'`)
	tideIn := a.queryInt64(`SELECT COALESCE(SUM(prompt_tokens),0) FROM requests WHERE ` + a.paidLineCond("per_token") + ` AND bill_state='billed'`)
	tideCached := a.queryInt64(`SELECT COALESCE(SUM(cached_tokens),0) FROM requests WHERE ` + a.paidLineCond("per_token") + ` AND bill_state='billed'`)
	tideOut := a.queryInt64(`SELECT COALESCE(SUM(completion_tokens),0) FROM requests WHERE ` + a.paidLineCond("per_token") + ` AND bill_state='billed'`)
	faceToday := a.queryInt64(`SELECT COALESCE(SUM(tide_face_micro),0) FROM requests WHERE `+a.paidLineCond("per_token")+` AND bill_state='billed' AND ts>=?`, today0)
	face7d := a.queryInt64(`SELECT COALESCE(SUM(tide_face_micro),0) FROM requests WHERE `+a.paidLineCond("per_token")+` AND bill_state='billed' AND ts>=?`, week0)
	faceTotal := a.queryInt64(`SELECT COALESCE(SUM(initial_micro),0) FROM line_keys WHERE line_id='tide'`)
	faceUsed := a.queryInt64(`SELECT COALESCE(SUM(used_micro),0) FROM line_keys WHERE line_id='tide'`)
	if faceUsed > faceTotal {
		faceTotal = faceUsed
	}
	tideKeys := []map[string]any{}
	if rows, err := a.DB.Query(
		"SELECT idx, initial_micro, used_micro, dead, updated_ts FROM line_keys WHERE line_id='tide' ORDER BY idx"); err == nil {
		for rows.Next() {
			var idx, init, used, updated int64
			var dead int64
			if rows.Scan(&idx, &init, &used, &dead, &updated) == nil {
				tideKeys = append(tideKeys, map[string]any{
					"idx": idx, "initial_micro": init, "used_micro": used,
					"remain_micro": init - used, "dead": dead != 0, "updated_ts": updated,
				})
			}
		}
		rows.Close()
	}
	tide := map[string]any{
		"income_micro": tideIncome, "income_today_micro": tideIncomeToday,
		"face_total_micro": faceTotal, "face_today_micro": faceToday, "face_7d_micro": face7d,
		"face_cap_micro": a.tideFaceCap(), "face_remain_micro": faceTotal - faceUsed,
		"profit_micro": tideIncome - faceUsed, "calls": tideCalls,
		"tokens_in": tideIn, "tokens_cached": tideCached, "tokens_out": tideOut,
		"keys": tideKeys,
	}

	// 近 14 天趋势（北京时间按日分组）
	trend := []map[string]any{}
	if rows, err := a.DB.Query(
		`SELECT (ts+?)/86400 AS day, COUNT(*), COALESCE(SUM(CASE WHEN bill_state='billed' THEN unit_price_micro END),0)
		 FROM requests WHERE bill_state!='' AND ts>=? GROUP BY day ORDER BY day`, beijingOffset, today0-13*86400); err == nil {
		for rows.Next() {
			var day, calls, inc int64
			if rows.Scan(&day, &calls, &inc) == nil {
				trend = append(trend, map[string]any{
					"date":  strconv.FormatInt(day*86400-beijingOffset, 10),
					"calls": calls, "income_micro": inc,
				})
			}
		}
		rows.Close()
	}

	// 消费 TOP10
	top := []map[string]any{}
	if rows, err := a.DB.Query(
		`SELECT b.user_id, COALESCE(u.username,''), COALESCE(SUM(b.unit_price_micro),0), COUNT(*)
		 FROM balance_flows b LEFT JOIN users u ON u.id=b.user_id
		 WHERE b.type='billed' GROUP BY b.user_id ORDER BY 3 DESC LIMIT 10`); err == nil {
		for rows.Next() {
			var uid, cost, calls int64
			var username string
			if rows.Scan(&uid, &username, &cost, &calls) == nil {
				top = append(top, map[string]any{"user_id": uid, "username": username, "cost_micro": cost, "calls": calls})
			}
		}
		rows.Close()
	}

	prices, priceMicro := a.currentPrices()
	floorSafety := a.floorSafety()

	// billing_modes 在役收费线的计费模式（20260923 修：管理台"计费模式"KPI 此前由前端**硬编码**
	// "按次计费"，站长报"明明是按量计费却显示按次计费"）。
	// 口径：**排除全模型维护（maintenance）的线**——那种线对外不可用，计入会让管理台展示的
	// 模式与实际可调能力不符；免费/官方中转/众筹/codex 走独立前缀，不属"收费线计费模式"。
	billingModes := []string{}
	seenMode := map[string]bool{}
	{
		ls := a.linesSnap()
		for i := range ls {
			l := &ls[i]
			if l.Mode == "free" || l.Mode == "official" || l.Mode == "crowd" || l.AuthStyle == "codex" {
				continue
			}
			live := false
			for j := range l.Models {
				if !l.Models[j].Maintenance {
					live = true
					break
				}
			}
			if !live || seenMode[l.Mode] {
				continue
			}
			seenMode[l.Mode] = true
			billingModes = append(billingModes, l.Mode)
		}
	}

	adminJSON(w, map[string]any{
		"income": income, "upstream": upstream,
		"billing_modes": billingModes,
		"calls": map[string]any{
			"all": allT, "all_ok": allOK, "today": todayT, "today_ok": todayOK,
			"week": weekT, "week_ok": weekOK, "month": monthT, "month_ok": monthOK,
			"fail_dist": failDist,
		},
		"users":  map[string]any{"liability_micro": liability, "with_balance": withBalance},
		"tide":   tide,
		"trend":  trend,
		"top":    top,
		"prices": prices, "price_micro": priceMicro,
		"floor_safety":  floorSafety,
		"promo_ends_at": 0, "billing_switch_at": 0,
		"now": now,
	})
}

// perCallCostFor 模型单次成本（配置台账；未知模型取线默认）
func (a *App) perCallCostFor(fullModel string) int64 {
	lineID, siteID, ok := splitLineModel(fullModel)
	if !ok {
		return 0
	}
	if l := a.lineByID(lineID); l != nil {
		for i := range l.Models {
			if l.Models[i].SiteID == siteID && l.Models[i].PerCallCost > 0 {
				return l.Models[i].PerCallCost
			}
		}
		for i := range l.Models {
			if l.Models[i].PerCallCost > 0 {
				return l.Models[i].PerCallCost
			}
		}
	}
	return 0
}

func (a *App) perCallCostDefault() int64 {
	ls := a.linesSnap()
	for i := range ls {
		if ls[i].Mode == "per_call" {
			for j := range ls[i].Models {
				if ls[i].Models[j].PerCallCost > 0 {
					return ls[i].Models[j].PerCallCost
				}
			}
		}
	}
	return 0
}

// tideFaceCap 单钥面值（tide 线 key_face_micro 配置）
func (a *App) tideFaceCap() int64 {
	if l := a.lineByID("tide"); l != nil {
		return l.KeyFaceMicro
	}
	return 0
}

// splitLineModel 完整模型名拆线（复用 config；此处局部实现避免循环依赖导入面扩大）
func splitLineModel(full string) (lineID, siteID string, ok bool) {
	i := strings.IndexByte(full, '/')
	if i <= 0 || i >= len(full)-1 {
		return "", "", false
	}
	return full[:i], full[i+1:], true
}

// currentPrices 当前生效价目（pricing 表）→ prices[] + 最低价
func (a *App) currentPrices() ([]map[string]any, int64) {
	now := time.Now().Unix()
	rows, err := a.DB.Query(
		`SELECT model, mode, price_micro, floor_micro, in_rate10, cache_rate10, out_rate10
		 FROM pricing WHERE grp='normal' AND starts_at<=? AND (ends_at IS NULL OR ends_at>?)`, now, now)
	if err != nil {
		return []map[string]any{}, 0
	}
	defer rows.Close()
	out := []map[string]any{}
	minPrice := int64(-1)
	for rows.Next() {
		var model, mode string
		var price, floor, in10, cache10, out10 int64
		if rows.Scan(&model, &mode, &price, &floor, &in10, &cache10, &out10) == nil {
			item := map[string]any{
				"model": model, "mode": mode, "price_micro": price, "floor_micro": floor,
				"in_price": float64(in10) / 10, "cache_price": float64(cache10) / 10,
				"out_price": float64(out10) / 10, "subsidized": false,
			}
			out = append(out, item)
			if strings.HasPrefix(model, a.paidLinePrefix("per_call")) && (minPrice < 0 || price < minPrice) {
				minPrice = price
			}
		}
	}
	if minPrice < 0 {
		minPrice = 0
	}
	return out, minPrice
}

// floorSafety 保本自检：breakeven = ceil(成本/0.97)（支付通道统一 3% 手续费口径，2026-09-13 起；
// 旧口径为支付宝 5%/微信 6% 按更严的 ceil(cost/0.94)，切换 pay.faka08.com 双通道统一 3% 后更新）
func (a *App) floorSafety() []map[string]any {
	out := []map[string]any{}
	now := time.Now().Unix()
	ls := a.linesSnap()
	for i := range ls {
		l := &ls[i]
		if l.Mode != "per_call" {
			continue
		}
		for j := range l.Models {
			m := &l.Models[j]
			cost := m.PerCallCost
			sell := m.PerCallSell
			if sell == 0 {
				// 售价以 pricing 当前价为口径
				_ = a.DB.QueryRow(
					`SELECT price_micro FROM pricing WHERE model=? AND grp='normal' AND starts_at<=? AND (ends_at IS NULL OR ends_at>?) ORDER BY starts_at DESC`,
					l.ID+"/"+m.SiteID, now, now).Scan(&sell)
			}
			if cost <= 0 {
				continue
			}
			breakeven := (cost*100 + 96) / 97 // ceil(cost/0.97)：通道 3% 费率后到手 97%
			margin := 0.0
			if sell > 0 {
				margin = float64(sell-breakeven) / float64(sell) * 100
			}
			out = append(out, map[string]any{
				"model": l.ID + "/" + m.SiteID, "mode": "per_call",
				"floor_micro": breakeven, "breakeven_micro": breakeven,
				"margin_pct": math.Round(margin*100) / 100, "safe": sell >= breakeven,
			})
		}
	}
	return out
}

// —— GET /v1/admin/quota ——
func (a *App) handleAdminQuota(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var poolTotal, poolUsed int64
	var circuit int64
	_ = a.DB.QueryRow("SELECT total_calls, used_calls, circuit_open FROM upstream_quota WHERE provider='pool'").Scan(&poolTotal, &poolUsed, &circuit)
	channels := []map[string]any{}
	if rows, err := a.DB.Query(
		"SELECT provider, total_calls, used_calls, cost_per_call_micro FROM upstream_quota WHERE provider IN ('acu','acu2') ORDER BY provider"); err == nil {
		for rows.Next() {
			var p string
			var total, used, cost int64
			if rows.Scan(&p, &total, &used, &cost) == nil {
				channels = append(channels, map[string]any{
					"provider": p, "total_calls": total, "used_calls": used, "cost_per_call_micro": cost,
				})
			}
		}
		rows.Close()
	}
	topups := []map[string]any{}
	if rows, err := a.DB.Query(
		"SELECT provider, amount_micro, calls_added, total_before, total_after, note, ts FROM upstream_topups ORDER BY id DESC LIMIT 30"); err == nil {
		for rows.Next() {
			var provider string
			var amount, added, before, after, ts int64
			var note string
			if rows.Scan(&provider, &amount, &added, &before, &after, &note, &ts) == nil {
				topups = append(topups, map[string]any{
					"provider": provider, "amount_micro": amount, "calls_added": added,
					"total_before": before, "total_after": after, "note": note, "ts": ts,
				})
			}
		}
		rows.Close()
	}
	adminJSON(w, map[string]any{
		"pool": map[string]any{
			"total_micro": poolTotal, "used_micro": poolUsed,
			"remain_micro": poolTotal - poolUsed, "circuit_open": circuit != 0,
		},
		"channels": channels, "topups": topups,
	})
}

// confirmOK 高危操作二次密码校验（失败写审计）
func (a *App) confirmOK(w http.ResponseWriter, r *http.Request, action string, pw string) bool {
	if adminPasswordOk(pw) {
		return true
	}
	a.auditAppend(action+"_confirm_fail", 0, "", clientIP(r))
	errAdmin(w, 403, "invalid_credentials", "确认密码错误")
	return false
}

// —— POST /v1/admin/quota/topup ——（池增量充值）
func (a *App) handleAdminQuotaTopup(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var req struct {
		AmountMicro     int64  `json:"amount_micro"`
		Note            string `json:"note"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 8192, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	if req.AmountMicro <= 0 {
		errAdmin(w, 400, "bad_request", "充值金额必须大于 0")
		return
	}
	if strings.TrimSpace(req.Note) == "" {
		errAdmin(w, 400, "bad_request", "备注必填（审计留痕）")
		return
	}
	if !a.confirmOK(w, r, "upstream_topup", req.ConfirmPassword) {
		return
	}
	before := a.queryInt64("SELECT total_calls FROM upstream_quota WHERE provider='pool'")
	after := before + req.AmountMicro
	if _, err := a.DB.Exec("UPDATE upstream_quota SET total_calls=?, updated_ts=? WHERE provider='pool'", after, time.Now().Unix()); err != nil {
		errAdmin(w, 500, "internal_error", "更新失败")
		return
	}
	cost := a.queryInt64("SELECT cost_per_call_micro FROM upstream_quota WHERE provider='acu'")
	added := int64(0)
	if cost > 0 {
		added = req.AmountMicro / cost
	}
	ts := time.Now().Unix()
	_, _ = a.DB.Exec(
		"INSERT INTO upstream_topups (provider, amount_micro, calls_added, total_before, total_after, note, ts) VALUES ('pool',?,?,?,?,?,?)",
		req.AmountMicro, added, before, after, req.Note, ts)
	a.auditAppend("upstream_topup", 0, req.Note+"：+"+strconv.FormatInt(req.AmountMicro, 10)+" 微元", clientIP(r))
	adminJSON(w, map[string]any{"ok": true, "amount_micro": req.AmountMicro, "total_before": before, "total_after": after})
}

// —— POST /v1/admin/quota/circuit ——（手动解除熔断）
func (a *App) handleAdminQuotaCircuit(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	_, _ = a.DB.Exec("UPDATE upstream_quota SET circuit_open=0, updated_ts=? WHERE provider='pool'", time.Now().Unix())
	a.auditAppend("circuit_reset", 0, "手动解除熔断", clientIP(r))
	adminJSON(w, map[string]any{"ok": true, "provider": "pool"})
}

// —— POST /v1/admin/quota/sync ——（上游真实剩余绝对值对齐）
func (a *App) handleAdminQuotaSync(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var req struct {
		RemainMicro     int64  `json:"remain_micro"`
		Note            string `json:"note"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 8192, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	if req.RemainMicro < 0 {
		errAdmin(w, 400, "bad_request", "剩余额度不能为负")
		return
	}
	if strings.TrimSpace(req.Note) == "" {
		errAdmin(w, 400, "bad_request", "备注必填（审计留痕）")
		return
	}
	if !a.confirmOK(w, r, "quota_sync", req.ConfirmPassword) {
		return
	}
	var used int64
	_ = a.DB.QueryRow("SELECT used_calls FROM upstream_quota WHERE provider='pool'").Scan(&used)
	after := used + req.RemainMicro
	if _, err := a.DB.Exec("UPDATE upstream_quota SET total_calls=?, updated_ts=? WHERE provider='pool'", after, time.Now().Unix()); err != nil {
		errAdmin(w, 500, "internal_error", "更新失败")
		return
	}
	ts := time.Now().Unix()
	_, _ = a.DB.Exec(
		"INSERT INTO upstream_topups (provider, amount_micro, calls_added, total_before, total_after, note, ts) VALUES ('pool',0,0,?,?,?,?)",
		after, after, "sync："+req.Note, ts)
	a.auditAppend("quota_sync", 0, req.Note+"：剩余对齐 "+strconv.FormatInt(req.RemainMicro, 10)+" 微元", clientIP(r))
	adminJSON(w, map[string]any{"ok": true, "remain_micro": req.RemainMicro, "total_before": after, "total_after": after})
}

// —— GET /v1/admin/supervision?page&q ——
func (a *App) handleAdminSupervision(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	now := time.Now().Unix()
	week0 := (now+beijingOffset)/86400*86400 - beijingOffset - 6*86400
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := atoiDefault(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	const pageSize = 20

	poolTotal := a.queryInt64(`SELECT total_calls FROM upstream_quota WHERE provider='pool'`)
	poolUsed := a.queryInt64(`SELECT used_calls FROM upstream_quota WHERE provider='pool'`)
	poolRemain := poolTotal - poolUsed
	cost := a.queryInt64(`SELECT cost_per_call_micro FROM upstream_quota WHERE provider='acu'`)
	if cost == 0 {
		cost = a.perCallCostDefault()
	}
	aRemainCalls := int64(0)
	if cost > 0 {
		aRemainCalls = poolRemain / cost
	}
	priceNow := a.queryInt64(`SELECT COALESCE(MIN(price_micro),0) FROM pricing WHERE model LIKE ? AND grp='normal' AND starts_at<=? AND (ends_at IS NULL OR ends_at>?)`, a.paidLinePrefix("per_call")+"%", now, now)
	if priceNow == 0 {
		priceNow = 2000
	}
	// 用户负债（20260924：含 2 号折扣钱包）
	liability := a.queryInt64(`SELECT COALESCE(SUM(balance_micro + balance2_micro),0) FROM users WHERE balance_micro>0 OR balance2_micro>0`)
	holders := a.queryInt64(`SELECT COUNT(*) FROM users WHERE balance_micro>0 OR balance2_micro>0`)
	bNow := int64(0)
	if priceNow > 0 {
		bNow = (liability + priceNow - 1) / priceNow
	}
	gapCalls := aRemainCalls - bNow
	overissue := 0.0
	if aRemainCalls > 0 {
		overissue = float64(bNow) / float64(aRemainCalls) * 100
	}
	level, levelLabel := "normal", "正常"
	switch {
	case overissue >= 100:
		level, levelLabel = "critical", "已超发"
	case overissue >= 80:
		level, levelLabel = "danger", "高危"
	case overissue >= 60:
		level, levelLabel = "warn", "预警"
	}
	gapTopup := int64(0)
	if gapCalls < 0 {
		gapTopup = -gapCalls * cost
	}
	used7d := int64(0)
	if rows, err := a.DB.Query(
		`SELECT model, COUNT(*) FROM requests WHERE `+a.paidLineCond("per_call")+` AND bill_state='billed' AND ts>=? GROUP BY model`, week0); err == nil {
		for rows.Next() {
			var model string
			var n int64
			if rows.Scan(&model, &n) == nil {
				used7d += a.perCallCostFor(model) * n
			}
		}
		rows.Close()
	}
	daysLeft := int64(-1)
	if used7d > 0 {
		daysLeft = poolRemain / (used7d / 7)
	}

	adminTopup := a.queryInt64(`SELECT COALESCE(SUM(CASE WHEN type='topup' THEN amount_micro WHEN type='deduct' THEN amount_micro END),0) FROM balance_flows WHERE operator='admin' AND type IN ('topup','deduct')`)
	epayTopup := a.queryInt64(`SELECT COALESCE(SUM(amount_micro),0) FROM balance_flows WHERE operator='epay' AND type='topup'`)
	cumTopup := a.queryInt64(`SELECT COALESCE(SUM(amount_micro),0) FROM balance_flows WHERE type='topup'`)
	cumDeduct := a.queryInt64(`SELECT COALESCE(ABS(SUM(amount_micro)),0) FROM balance_flows WHERE type='deduct'`)

	// 发放台账
	where := "f.type IN ('topup','deduct')"
	var args []any
	if q != "" {
		where += " AND (u.username LIKE ? OR u.email LIKE ? OR CAST(u.id AS TEXT)=?)"
		args = append(args, "%"+q+"%", "%"+q+"%", q)
	}
	var ledgerTotal int64
	_ = a.DB.QueryRow(
		`SELECT COUNT(*) FROM balance_flows f LEFT JOIN users u ON u.id=f.user_id WHERE `+where, args...).Scan(&ledgerTotal)
	ledger := []map[string]any{}
	lrows, err := a.DB.Query(
		`SELECT f.id, f.user_id, COALESCE(u.username,''), COALESCE(u.email,''), f.type, f.amount_micro, f.note, f.ts, f.operator
		 FROM balance_flows f LEFT JOIN users u ON u.id=f.user_id WHERE `+where+" ORDER BY f.id DESC LIMIT ? OFFSET ?",
		append(args, pageSize, (page-1)*pageSize)...)
	if err == nil {
		for lrows.Next() {
			var id, uid, amount, ts int64
			var username, email, fType, note, operator string
			if lrows.Scan(&id, &uid, &username, &email, &fType, &amount, &note, &ts, &operator) == nil {
				ledger = append(ledger, map[string]any{
					"id": id, "user_id": uid, "username": username, "email": email,
					"type": fType, "amount_micro": amount, "note": note, "ts": ts, "operator": operator,
				})
			}
		}
		lrows.Close()
	}

	// 余额持有 TOP20（20260924：主钱包 + 折扣钱包合计排序）
	top := []map[string]any{}
	if rows, err := a.DB.Query(
		`SELECT u.id, u.username, u.email, u.balance_micro, u.balance2_micro,
		        COALESCE((SELECT SUM(amount_micro) FROM balance_flows f WHERE f.user_id=u.id AND f.type='topup'),0),
		        0,
		        COALESCE((SELECT SUM(unit_price_micro) FROM balance_flows f WHERE f.user_id=u.id AND f.type='billed'),0),
		        COALESCE((SELECT COUNT(*) FROM requests rq WHERE rq.user_id=u.id AND rq.bill_state='billed'),0)
		 FROM users u WHERE u.balance_micro>0 OR u.balance2_micro>0
		 ORDER BY (u.balance_micro + u.balance2_micro) DESC LIMIT 20`); err == nil {
		for rows.Next() {
			var id, balance, balance2, topup, deduct, spent, calls int64
			var username, email string
			if rows.Scan(&id, &username, &email, &balance, &balance2, &topup, &deduct, &spent, &calls) == nil {
				top = append(top, map[string]any{
					"id": id, "username": username, "email": email,
					"balance_micro": balance, "balance2_micro": balance2,
					"topup_micro": topup, "deduct_micro": deduct,
					"spent_micro": spent, "billed_calls": calls,
				})
			}
		}
		rows.Close()
	}

	adminJSON(w, map[string]any{
		"now": now,
		"core": map[string]any{
			"a_remain_calls": aRemainCalls, "pool_total_micro": poolTotal, "pool_used_micro": poolUsed,
			"b_now_calls": bNow, "b_after_calls": bNow,
			"price_now_micro": priceNow, "price_after_micro": priceNow,
			"liability_micro": liability, "holders": holders,
			"gap_calls": gapCalls, "overissue_pct": math.Round(overissue*100) / 100,
			"level": level, "level_label": levelLabel,
			"gap_topup_micro": gapTopup, "cost_per_call_micro": cost,
			"days_left": daysLeft, "used_7d_money": used7d,
		},
		"summary": map[string]any{
			"admin_topup_micro": adminTopup, "epay_topup_micro": epayTopup,
			"cum_topup_micro": cumTopup, "cum_deduct_micro": cumDeduct,
			"net_issued_micro": cumTopup - cumDeduct,
		},
		"ledger": map[string]any{
			"page": page, "page_size": pageSize, "total": ledgerTotal, "q": q, "items": ledger,
		},
		"top": top,
	})
}
