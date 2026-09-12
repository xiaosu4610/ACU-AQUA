// Package billing 计费内核。红线：整数微元运算，全程 int64，零浮点。
// rate10 口径：微元/千token × 10（与 Rust 版完全一致，黄金用例可逐笔对账）。
package billing

import (
	"database/sql"
	"time"
)

// PricingInfo 当前生效价目（pricing 表按 (model, grp) 时间窗取行）
type PricingInfo struct {
	Mode        string // per_call | per_token
	PriceMicro  int64  // per_call=单次价；per_token=单次保底（预扣额与 402 口径）
	FloorMicro  int64  // per_token 单次保底
	InRate10    int64  // 微元/千token×10
	CacheRate10 int64
	OutRate10   int64
	EndsAt      int64 // 价目行截止时刻（0=永久生效；>0 为活动价，/v1/models 据此标限时补贴）
}

// Usage 三路 token（上游 usage 原值）
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
}

// CurrentPricing 按 (model, grp) 取当前生效行：
// starts_at <= now 且 (ends_at 为空或 > now) 的最新一条——活动价时间窗原生支持，
// 过期自动回退历史行，无需任何定时任务。
func CurrentPricing(d *sql.DB, model, grp string) (*PricingInfo, error) {
	now := time.Now().Unix()
	var p PricingInfo
	var ends sql.NullInt64
	err := d.QueryRow(
		`SELECT mode, price_micro, floor_micro, in_rate10, cache_rate10, out_rate10, ends_at
		 FROM pricing WHERE model=? AND grp=? AND starts_at <= ?
		 AND (ends_at IS NULL OR ends_at > ?)
		 ORDER BY starts_at DESC LIMIT 1`,
		model, grp, now, now,
	).Scan(&p.Mode, &p.PriceMicro, &p.FloorMicro, &p.InRate10, &p.CacheRate10, &p.OutRate10, &ends)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.EndsAt = ends.Int64
	return &p, nil
}

// MeterTokens 三段价精算：新输入×输入价 + 缓存×缓存价 + 输出×输出价。
// 整除截断口径与 Rust 版逐函数一致：tok_price = ni*in/10000 + ct*cache/10000 + cdt*out/10000，
// 最终 max(tok_price, floor)。
func MeterTokens(u Usage, p *PricingInfo) int64 {
	if p == nil || p.Mode != "per_token" {
		return 0
	}
	ni := u.PromptTokens - u.CachedTokens
	if ni < 0 {
		ni = 0
	}
	tokPrice := ni*p.InRate10/10000 + u.CachedTokens*p.CacheRate10/10000 + u.CompletionTokens*p.OutRate10/10000
	if p.FloorMicro > 0 && tokPrice < p.FloorMicro {
		return p.FloorMicro
	}
	return tokPrice
}

// FaceCostMicro 上游面值成本（内部台账口径，绝不外泄）：
// rate10 口径与售价一致（微元/万token），每段四舍五入：(tokens*rate10 + 5000) / 10000。
func FaceCostMicro(u Usage, inCostRate10, cacheCostRate10, outCostRate10 int64) int64 {
	roundM := func(tokens, rate10 int64) int64 {
		if tokens < 0 {
			tokens = 0
		}
		return (tokens*rate10 + 5_000) / 10_000
	}
	ni := u.PromptTokens - u.CachedTokens
	if ni < 0 {
		ni = 0
	}
	ct := u.CachedTokens
	if ct < 0 {
		ct = 0
	}
	return roundM(ni, inCostRate10) + roundM(ct, cacheCostRate10) + roundM(u.CompletionTokens, outCostRate10)
}

// UserPriceGrp 用户在某条线上的价格组（站长逐用户逐线授权）
func UserPriceGrp(d *sql.DB, userID int64, lineMode string) string {
	col := "price_grp_token"
	if lineMode == "per_call" {
		col = "price_grp_call"
	}
	var g string
	if err := d.QueryRow("SELECT "+col+" FROM users WHERE id=?", userID).Scan(&g); err != nil {
		return "normal"
	}
	if g == "" {
		return "normal"
	}
	return g
}

// Prehold 发起请求前预扣（先付后用）：余额不足返回错误（429 insufficient_quota 口径）。
// 政策硬门槛：使用收费模型须保持账户 0 元以上余额（balance_micro>0 显式政策位，
// 预扣额恒正时与 >= 等价；防未来 amount=0 路径绕过，绝不透支、绝无事后追缴）。
// 原子条件 UPDATE（与 Rust 版双进程并发访问同一生产库时无竞态：扣不满足即失败）。
func Prehold(d *sql.DB, userID, amount int64, requestID int64) error {
	if amount < 0 {
		amount = 0
	}
	return tx(d, func(tx *sql.Tx) error {
		res, err := tx.Exec(
			"UPDATE users SET balance_micro=balance_micro-? WHERE id=? AND status=1 AND balance_micro>=? AND balance_micro>0",
			amount, userID, amount)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			// 用户存在但余额不足（或用户不存在/被禁用）
			var st int64
			if e := tx.QueryRow("SELECT status FROM users WHERE id=?", userID).Scan(&st); e != nil {
				return e
			}
			return ErrInsufficientBalance
		}
		var balance int64
		if err := tx.QueryRow("SELECT balance_micro FROM users WHERE id=?", userID).Scan(&balance); err != nil {
			return err
		}
		return insertFlow(tx, userID, requestID, "prehold", -amount, balance, 0, "")
	})
}

// Settle 完成后结算（多退少补）：
// 实际应扣 = final；已预扣 = preheld；delta = preheld - final（正=退回，负=补扣）。
// 失败请求 final=0 → 全额退回。
func Settle(d *sql.DB, userID, preheld, final int64, requestID int64, unitPrice int64, note string) error {
	delta := preheld - final
	if delta < 0 {
		delta = 0 // 极端防御：final > preheld 时不追扣（预扣已按保守上限估）
	}
	return tx(d, func(tx *sql.Tx) error {
		var balance int64
		if err := tx.QueryRow("SELECT balance_micro FROM users WHERE id=?", userID).Scan(&balance); err != nil {
			return err
		}
		if delta > 0 {
			if _, err := tx.Exec("UPDATE users SET balance_micro=balance_micro+? WHERE id=?", delta, userID); err != nil {
				return err
			}
		}
		flowType := "billed"
		amount := -final
		if final == 0 {
			// 全额退回 flow amount 固定记 0（对账公式口径：prehold 扣款行不计入重放，
			// 退回资金不得重复计入；退回事实由 balance_after 与 requests.error 留痕）
			flowType = "refunded"
		}
		return insertFlow(tx, userID, requestID, flowType, amount, balance+delta, unitPrice, note)
	})
}

// insertFlow 计费流水（只增不删）
func insertFlow(tx *sql.Tx, userID, requestID int64, flowType string, amount, balanceAfter, unitPrice int64, note string) error {
	_, err := tx.Exec(
		`INSERT INTO balance_flows (user_id, request_id, type, amount_micro, balance_before_micro, balance_after_micro, unit_price_micro, note, operator, ts)
		 VALUES (?,?,?,?,?,?,?,?, 'system', ?)`,
		userID, requestID, flowType, amount, balanceAfter-amount, balanceAfter, unitPrice, note, time.Now().Unix())
	return err
}

// BreakevenFloor 保本线（亏损防线）：per_call = ceil(cost / 0.93)；
// per_token 成本随 token 线性、售价比例恒定，返回 0（防线由播种价目本身保证）。
func BreakevenFloor(costMicro int64, mode string) int64 {
	if mode == "per_token" {
		return 0
	}
	if costMicro <= 0 {
		return 0
	}
	return (costMicro*100 + 92) / 93
}

// LineKeyReport 面值台账：逐把密钥累计（内存 + DB 双写一致）
func LineKeyReport(d *sql.DB, lineID string, idx int, initialMicro, faceCostMicro int64, requestID int64) error {
	now := time.Now().Unix()
	if _, err := d.Exec(
		`INSERT INTO line_keys (line_id, idx, initial_micro, used_micro, dead, updated_ts)
		 VALUES (?,?,?, ?,0,?)
		 ON CONFLICT(line_id, idx) DO UPDATE SET used_micro=used_micro+?, updated_ts=?`,
		lineID, idx, initialMicro, faceCostMicro, now, faceCostMicro, now); err != nil {
		return err
	}
	if requestID > 0 {
		if _, err := d.Exec("UPDATE requests SET face_cost_micro=? WHERE rowid=?", faceCostMicro, requestID); err != nil {
			return err
		}
	}
	return nil
}
