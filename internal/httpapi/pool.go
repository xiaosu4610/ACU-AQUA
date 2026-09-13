package httpapi

// 众筹池（acu/ 公共算力池）：与个人余额物理隔离的共享钱包。
// 计费口径：acu/ 模型按五折（0.5 倍率）从池子扣账（pricing 表 mode='per_token'，grp 固定 normal）；
// 池子归零即熔断（403 crowd_pool_empty），用户充值（payments.product='pool'）或官方注入即复活；
// 每一笔充值/扣费落 pool_flows 全透明可查，榜单（充值/用量/荣誉/净贡献）全部由流水实时聚合。

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"acu-aqua/gateway/internal/auth"
)

const (
	poolID       = "acu"
	poolDailyCap = 1_000_000 // 单用户每日扣费上限（微元，¥1.00 五折口径）——防单人掏空
)

var (
	poolQuotaMu  sync.Mutex
	poolQuota    = map[int64]int64{} // uid → 当日已扣微元
	poolQuotaDay = ""                // 当日标记（跨天清零）
)

// poolToday 北京时间日期串（日配额按自然日）
func poolToday() string {
	return time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
}

// poolGate 请求前闸门：池子有余额 + 用户当日配额未超。返回 (status, code, msg)，status=0 即放行。
func (a *App) poolGate(uid int64) (int, string, string) {
	var balance int64
	err := a.DB.QueryRow("SELECT balance_micro FROM pool_wallet WHERE id=?", poolID).Scan(&balance)
	if err == sql.ErrNoRows {
		return 403, "crowd_pool_empty", "众筹池尚未启动，充值任意金额（低至 ¥5）即可点亮公共模型"
	}
	if err != nil {
		return 500, "internal_error", "众筹池查询失败"
	}
	if balance <= 0 {
		return 403, "crowd_pool_empty", "众筹池已被大家用完，正在等待充值复活——充值任意金额（低至 ¥5）立刻点亮，救场者将登上荣誉墙"
	}
	// 日配额检查（只查不扣：实际用量结算时累计）
	poolQuotaMu.Lock()
	if poolQuotaDay != poolToday() {
		poolQuotaDay = poolToday()
		poolQuota = map[int64]int64{}
	}
	used := poolQuota[uid]
	poolQuotaMu.Unlock()
	if used >= poolDailyCap {
		return 429, "crowd_quota_daily", "今日众筹模型额度已用完（每用户每日 ¥1.00 口径），明日再来；急需可用个人余额调用 aqua/ 或 tlk/ 模型"
	}
	return 0, "", ""
}

// poolQuotaAdd 结算后累计当日用量（超限只影响下一笔 gate，已发生用量照实入账）
func poolQuotaAdd(uid, amount int64) {
	poolQuotaMu.Lock()
	if poolQuotaDay != poolToday() {
		poolQuotaDay = poolToday()
		poolQuota = map[int64]int64{}
	}
	poolQuota[uid] += amount
	poolQuotaMu.Unlock()
}

// poolConsume 众筹池结算扣账（acu/ 请求响应后按实际 usage 五折扣账；final<=0 不扣）。
// 允许轻微透支至 0 以下（下一笔 gate 熔断），账实相符；失败重试 2 次后落错误中心。
func (a *App) poolConsume(uid, final, rid int64, note string) {
	if final <= 0 {
		return
	}
	poolQuotaAdd(uid, final)
	for attempt := 0; attempt < 3; attempt++ {
		if err := a.poolConsumeOnce(uid, final, rid, note); err == nil {
			return
		}
		if attempt < 2 {
			time.Sleep(time.Duration(50<<attempt) * time.Millisecond)
		}
	}
	a.logError("pool_settle_failed", "", uid, rid, "note="+note)
}

func (a *App) poolConsumeOnce(uid, final, rid int64, note string) error {
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE pool_wallet SET balance_micro=balance_micro-?, used_micro=used_micro+?, updated_ts=? WHERE id=?",
		final, final, time.Now().Unix(), poolID); err != nil {
		_ = tx.Rollback()
		return err
	}
	var balance int64
	if err := tx.QueryRow("SELECT balance_micro FROM pool_wallet WHERE id=?", poolID).Scan(&balance); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(
		"INSERT INTO pool_flows (user_id, type, amount_micro, balance_after, request_id, note, ts) VALUES (?, 'consume', ?, ?, ?, ?, ?)",
		uid, -final, balance, rid, note, time.Now().Unix()); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// poolChargeTx 众筹池充值入账（融入 epaySettle 外部事务）：净到手金额直接进池子；
// 池子此前余额 ≤ 0 时标记 revival（救场英雄）。
func poolChargeTx(tx *sql.Tx, uid, amount int64, note string) error {
	var prev int64
	if err := tx.QueryRow("SELECT balance_micro FROM pool_wallet WHERE id=?", poolID).Scan(&prev); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		if _, err := tx.Exec("INSERT INTO pool_wallet (id, balance_micro, charged_micro, used_micro, updated_ts) VALUES (?,0,0,0,?)", poolID, time.Now().Unix()); err != nil {
			return err
		}
		prev = 0
	}
	revival := 0
	if prev <= 0 && amount > 0 {
		revival = 1
	}
	if _, err := tx.Exec("UPDATE pool_wallet SET balance_micro=balance_micro+?, charged_micro=charged_micro+?, updated_ts=? WHERE id=?",
		amount, amount, time.Now().Unix(), poolID); err != nil {
		return err
	}
	var balance int64
	if err := tx.QueryRow("SELECT balance_micro FROM pool_wallet WHERE id=?", poolID).Scan(&balance); err != nil {
		return err
	}
	_, err := tx.Exec(
		"INSERT INTO pool_flows (user_id, type, amount_micro, balance_after, revival, note, ts) VALUES (?, 'charge', ?, ?, ?, ?, ?)",
		uid, amount, balance, revival, note, time.Now().Unix())
	return err
}

// handlePoolStatus GET /v1/pool/status（公开）：池子余额 + 累计 + 今日消耗 + 供血状态
func (a *App) handlePoolStatus(w http.ResponseWriter, r *http.Request) {
	var balance, charged, used int64
	err := a.DB.QueryRow("SELECT balance_micro, charged_micro, used_micro FROM pool_wallet WHERE id=?", poolID).
		Scan(&balance, &charged, &used)
	if err == sql.ErrNoRows {
		balance, charged, used = 0, 0, 0
	} else if err != nil {
		errOut(w, 500, "internal_error", "众筹池查询失败")
		return
	}
	var todayUsed int64
	dayStart := time.Now().In(time.FixedZone("CST", 8*3600))
	dayStart = time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), 0, 0, 0, 0, dayStart.Location())
	_ = a.DB.QueryRow(
		"SELECT COALESCE(SUM(-amount_micro),0) FROM pool_flows WHERE type='consume' AND ts>=?",
		dayStart.Unix()).Scan(&todayUsed)
	var consumers int64
	_ = a.DB.QueryRow("SELECT COUNT(DISTINCT user_id) FROM pool_flows WHERE type='consume'").Scan(&consumers)
	jsonOut(w, 200, map[string]any{
		"id": poolID, "balance_micro": balance, "charged_micro": charged, "used_micro": used,
		"today_used_micro": todayUsed, "consumers": consumers,
		"alive": balance > 0, "daily_cap_micro": poolDailyCap,
	})
}

// handlePoolFlows GET /v1/pool/flows?limit（公开）：全流水（脱敏，众筹透明账本）
func (a *App) handlePoolFlows(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	rows, err := a.DB.Query(`
		SELECT f.id, f.user_id, COALESCE(u.username,''), f.type, f.amount_micro, f.balance_after, f.revival, f.note, f.ts
		FROM pool_flows f LEFT JOIN users u ON u.id=f.user_id
		ORDER BY f.id DESC LIMIT ?`, limit)
	if err != nil {
		errOut(w, 500, "internal_error", "流水查询失败")
		return
	}
	defer rows.Close()
	type flow struct {
		ID          int64  `json:"id"`
		User        string `json:"user"`
		Type        string `json:"type"`
		AmountMicro int64  `json:"amount_micro"`
		Balance     int64  `json:"balance_after_micro"`
		Revival     int    `json:"revival"`
		Note        string `json:"note"`
		Ts          int64  `json:"ts"`
	}
	items := []flow{}
	for rows.Next() {
		var f flow
		var uid int64
		var name string
		if rows.Scan(&f.ID, &uid, &name, &f.Type, &f.AmountMicro, &f.Balance, &f.Revival, &f.Note, &f.Ts) == nil {
			f.User = maskPoolUser(uid, name)
			f.Note = "" // 全局账本不暴露请求细节，个人明细在控制台自查
			items = append(items, f)
		}
	}
	jsonOut(w, 200, map[string]any{"items": items})
}

// handlePoolRanks GET /v1/pool/ranks?type=charge|usage|honor|net&range=all|week（公开）：四大榜单
func (a *App) handlePoolRanks(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	rng := r.URL.Query().Get("range")
	since := int64(0)
	if rng == "week" {
		since = time.Now().Unix() - 7*86400
	}
	switch typ {
	case "charge":
		a.poolRankCharge(w, since)
	case "usage":
		a.poolRankUsage(w, since)
	case "honor":
		a.poolRankHonor(w)
	case "net":
		a.poolRankNet(w, since)
	default:
		errOut(w, 400, "bad_request", "type 须为 charge|usage|honor|net")
	}
}

// poolRankEntry 榜单条目（全部脱敏输出）
type poolRankEntry struct {
	Rank        int     `json:"rank"`
	User        string  `json:"user"`
	AmountMicro int64   `json:"amount_micro"`
	Extra       float64 `json:"extra,omitempty"`
	Note        string  `json:"note,omitempty"`
}

// poolRankCharge 充值榜：累计充值排序 + 占比
func (a *App) poolRankCharge(w http.ResponseWriter, since int64) {
	var total int64
	_ = a.DB.QueryRow("SELECT COALESCE(SUM(amount_micro),0) FROM pool_flows WHERE type='charge' AND user_id>0").Scan(&total)
	rows, err := a.DB.Query(`
		SELECT f.user_id, COALESCE(u.username,''), SUM(f.amount_micro) s
		FROM pool_flows f LEFT JOIN users u ON u.id=f.user_id
		WHERE f.type='charge' AND f.user_id>0 AND f.ts>=?
		GROUP BY f.user_id ORDER BY s DESC LIMIT 50`, since)
	if err != nil {
		errOut(w, 500, "internal_error", "榜单查询失败")
		return
	}
	defer rows.Close()
	items := []poolRankEntry{}
	rank := 0
	for rows.Next() {
		var uid, s int64
		var name string
		if rows.Scan(&uid, &name, &s) == nil {
			rank++
			e := poolRankEntry{Rank: rank, User: maskPoolUser(uid, name), AmountMicro: s}
			if total > 0 {
				e.Extra = float64(s) / float64(total) * 100 // 占池子总充值百分比
			}
			e.Note = poolBadge(s)
			items = append(items, e)
		}
	}
	jsonOut(w, 200, map[string]any{"items": items})
}

// poolBadge 充值段位徽章（¥5 铜 / ¥20 银 / ¥50 金 / ¥100 钻）
func poolBadge(chargedMicro int64) string {
	switch {
	case chargedMicro >= 100_000_000:
		return "钻石赞助"
	case chargedMicro >= 50_000_000:
		return "金牌赞助"
	case chargedMicro >= 20_000_000:
		return "银牌赞助"
	case chargedMicro >= 5_000_000:
		return "助力者"
	}
	return ""
}

// poolRankUsage 用量榜：近 7 天（since>0）或全期扣费 TOP，附调用笔数与主力模型
func (a *App) poolRankUsage(w http.ResponseWriter, since int64) {
	rows, err := a.DB.Query(`
		SELECT f.user_id, COALESCE(u.username,''), SUM(-f.amount_micro) s, COUNT(*) n
		FROM pool_flows f LEFT JOIN users u ON u.id=f.user_id
		WHERE f.type='consume' AND f.user_id>0 AND f.ts>=?
		GROUP BY f.user_id ORDER BY s DESC LIMIT 20`, since)
	if err != nil {
		errOut(w, 500, "internal_error", "榜单查询失败")
		return
	}
	defer rows.Close()
	type usageEntry struct {
		poolRankEntry
		Calls int64 `json:"calls"`
	}
	items := []usageEntry{}
	rank := 0
	for rows.Next() {
		var uid, s, n int64
		var name string
		if rows.Scan(&uid, &name, &s, &n) == nil {
			rank++
			e := usageEntry{poolRankEntry{Rank: rank, User: maskPoolUser(uid, name), AmountMicro: s}, n}
			var top string
			_ = a.DB.QueryRow(
				"SELECT note FROM pool_flows WHERE user_id=? AND type='consume' AND ts>=? GROUP BY note ORDER BY SUM(-amount_micro) DESC LIMIT 1",
				uid, since).Scan(&top)
			e.Note = top
			items = append(items, e)
		}
	}
	jsonOut(w, 200, map[string]any{"items": items})
}

// poolRankHonor 荣誉榜：救场英雄时间线（revival=1）+ 开服元老（最早 10 笔充值）
func (a *App) poolRankHonor(w http.ResponseWriter) {
	rows, err := a.DB.Query(`
		SELECT f.user_id, COALESCE(u.username,''), f.amount_micro, f.ts
		FROM pool_flows f LEFT JOIN users u ON u.id=f.user_id
		WHERE f.revival=1 AND f.user_id>0 ORDER BY f.ts DESC LIMIT 50`)
	if err != nil {
		errOut(w, 500, "internal_error", "榜单查询失败")
		return
	}
	defer rows.Close()
	type honor struct {
		User        string `json:"user"`
		AmountMicro int64  `json:"amount_micro"`
		Ts          int64  `json:"ts"`
		Saves       int64  `json:"saves"`
	}
	heroes := []honor{}
	for rows.Next() {
		var uid, amount, ts int64
		var name string
		if rows.Scan(&uid, &name, &amount, &ts) == nil {
			h := honor{User: maskPoolUser(uid, name), AmountMicro: amount, Ts: ts}
			_ = a.DB.QueryRow("SELECT COUNT(*) FROM pool_flows WHERE revival=1 AND user_id=?", uid).Scan(&h.Saves)
			heroes = append(heroes, h)
		}
	}
	elders := []poolRankEntry{}
	rows2, err := a.DB.Query(`
		SELECT f.user_id, COALESCE(u.username,''), MIN(f.ts), SUM(f.amount_micro) s
		FROM pool_flows f LEFT JOIN users u ON u.id=f.user_id
		WHERE f.type='charge' AND f.user_id>0
		GROUP BY f.user_id ORDER BY MIN(f.ts) ASC LIMIT 10`)
	if err == nil {
		defer rows2.Close()
		rank := 0
		for rows2.Next() {
			var uid, first, s int64
			var name string
			if rows2.Scan(&uid, &name, &first, &s) == nil {
				rank++
				elders = append(elders, poolRankEntry{Rank: rank, User: maskPoolUser(uid, name), AmountMicro: s, Note: "开服元老"})
			}
		}
	}
	jsonOut(w, 200, map[string]any{"heroes": heroes, "elders": elders})
}

// poolRankNet 净贡献榜：充值−扣费 只展示正值（不羞辱），附评语
func (a *App) poolRankNet(w http.ResponseWriter, since int64) {
	rows, err := a.DB.Query(`
		SELECT f.user_id, COALESCE(u.username,''), SUM(f.amount_micro) s
		FROM pool_flows f LEFT JOIN users u ON u.id=f.user_id
		WHERE f.user_id>0 AND f.type IN ('charge','consume') AND f.ts>=?
		GROUP BY f.user_id HAVING s>0 ORDER BY s DESC LIMIT 30`, since)
	if err != nil {
		errOut(w, 500, "internal_error", "榜单查询失败")
		return
	}
	defer rows.Close()
	items := []poolRankEntry{}
	rank := 0
	for rows.Next() {
		var uid, s int64
		var name string
		if rows.Scan(&uid, &name, &s) == nil {
			rank++
			e := poolRankEntry{Rank: rank, User: maskPoolUser(uid, name), AmountMicro: s}
			e.Note = poolBadge(s) // 净贡献达段位线同样授徽
			items = append(items, e)
		}
	}
	jsonOut(w, 200, map[string]any{"items": items})
}

// maskPoolUser 榜单脱敏：昵称打码（保首尾各 1 字符）+ UID 尾三位
func maskPoolUser(uid int64, name string) string {
	if name == "" {
		name = "用户"
	}
	r := []rune(name)
	if len(r) <= 2 {
		name = string(r) + "**"
	} else {
		name = string(r[:1]) + "**" + string(r[len(r)-1:])
	}
	ids := strconv.FormatInt(uid, 10)
	if len(ids) > 3 {
		ids = ids[len(ids)-3:]
	}
	return name + " · " + ids
}

// handleMyPoolFlows GET /v1/my/pool/flows（登录）：我的众筹池充值与扣费明细
func (a *App) handleMyPoolFlows(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "unauthorized", "请先登录")
		return
	}
	rows, err := a.DB.Query(`
		SELECT f.type, f.amount_micro, f.balance_after, f.revival, f.note, f.ts,
		       COALESCE(r.prompt_tokens,0), COALESCE(r.completion_tokens,0), COALESCE(r.cached_tokens,0)
		FROM pool_flows f
		LEFT JOIN requests r ON r.rowid=f.request_id
		WHERE f.user_id=? ORDER BY f.id DESC LIMIT 200`, actx.UserID)
	if err != nil {
		errOut(w, 500, "internal_error", "明细查询失败")
		return
	}
	defer rows.Close()
	type myFlow struct {
		Type        string `json:"type"`
		AmountMicro int64  `json:"amount_micro"`
		Balance     int64  `json:"balance_after_micro"`
		Revival     int    `json:"revival"`
		Model       string `json:"model,omitempty"`
		Ts          int64  `json:"ts"`
		Prompt      int64  `json:"prompt_tokens"`
		Completion  int64  `json:"completion_tokens"`
		Cached      int64  `json:"cached_tokens"`
	}
	items := []myFlow{}
	var charged, used int64
	for rows.Next() {
		var f myFlow
		var note string
		if rows.Scan(&f.Type, &f.AmountMicro, &f.Balance, &f.Revival, &note, &f.Ts,
			&f.Prompt, &f.Completion, &f.Cached) == nil {
			switch f.Type {
			case "consume":
				f.Model = note
				used += -f.AmountMicro
			case "charge":
				f.Model = "众筹池充值"
				charged += f.AmountMicro
			default:
				f.Model = note // seed=官方注入 / adjust=调整
			}
			items = append(items, f)
		}
	}
	var balance int64
	_ = a.DB.QueryRow("SELECT balance_micro FROM pool_wallet WHERE id=?", poolID).Scan(&balance)
	jsonOut(w, 200, map[string]any{
		"items": items, "balance_micro": balance,
		"my_charged_micro": charged, "my_used_micro": used, "net_micro": charged - used,
	})
}

// handleAdminPoolSeed POST /v1/admin/pool/seed {amount_micro, confirm_password}（管理员）：官方注入
func (a *App) handleAdminPoolSeed(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var req struct {
		AmountMicro     int64  `json:"amount_micro"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil || req.AmountMicro <= 0 || req.AmountMicro > 1000_000_000 {
		errAdmin(w, 400, "bad_request", "注入金额须在 0 ~ 1000 元之间")
		return
	}
	if !adminPasswordOk(req.ConfirmPassword) {
		errAdmin(w, 403, "confirm_failed", "二次密码确认失败")
		return
	}
	tx, err := a.DB.Begin()
	if err != nil {
		errAdmin(w, 500, "internal_error", "注入失败")
		return
	}
	var prev int64
	if err := tx.QueryRow("SELECT balance_micro FROM pool_wallet WHERE id=?", poolID).Scan(&prev); err != nil {
		if _, err := tx.Exec("INSERT INTO pool_wallet (id, balance_micro, charged_micro, used_micro, updated_ts) VALUES (?,0,0,0,?)", poolID, time.Now().Unix()); err != nil {
			_ = tx.Rollback()
			errAdmin(w, 500, "internal_error", "注入失败")
			return
		}
	}
	if _, err := tx.Exec("UPDATE pool_wallet SET balance_micro=balance_micro+?, charged_micro=charged_micro+?, updated_ts=? WHERE id=?",
		req.AmountMicro, req.AmountMicro, time.Now().Unix(), poolID); err != nil {
		_ = tx.Rollback()
		errAdmin(w, 500, "internal_error", "注入失败")
		return
	}
	var balance int64
	_ = tx.QueryRow("SELECT balance_micro FROM pool_wallet WHERE id=?", poolID).Scan(&balance)
	if _, err := tx.Exec(
		"INSERT INTO pool_flows (user_id, type, amount_micro, balance_after, note, ts) VALUES (0, 'seed', ?, ?, '官方注入', ?)",
		req.AmountMicro, balance, time.Now().Unix()); err != nil {
		_ = tx.Rollback()
		errAdmin(w, 500, "internal_error", "注入失败")
		return
	}
	if err := tx.Commit(); err != nil {
		errAdmin(w, 500, "internal_error", "注入失败")
		return
	}
	a.auditAppend("pool_seed", 0, "amount="+strconv.FormatInt(req.AmountMicro, 10), clientIP(r))
	jsonOut(w, 200, map[string]any{"ok": true, "balance_micro": balance})
}
