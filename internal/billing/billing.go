// Package billing 计费内核。红线：整数微元运算，全程 int64，零浮点。
// rate10 口径：微元/千token × 10（与 Rust 版完全一致，黄金用例可逐笔对账）。
package billing

import (
	"database/sql"
	"encoding/json"
	"log"
	"strconv"
	"strings"
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
		 ORDER BY starts_at DESC, rowid DESC LIMIT 1`,
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
	// 三段全部 clamp 非负（与 FaceCostMicro 同口径）：上游 usage 异常为负时
	// 绝不允许穿透到结算并翻转记账方向
	ni := u.PromptTokens - u.CachedTokens
	if ni < 0 {
		ni = 0
	}
	ct := u.CachedTokens
	if ct < 0 {
		ct = 0
	}
	cdt := u.CompletionTokens
	if cdt < 0 {
		cdt = 0
	}
	tokPrice := ni*p.InRate10/10000 + ct*p.CacheRate10/10000 + cdt*p.OutRate10/10000
	if p.FloorMicro > 0 && tokPrice < p.FloorMicro {
		return p.FloorMicro
	}
	return tokPrice
}

// MeterObserved 无上游 usage 时的保守计费（亏本防线，站长红线：宁可多记不可少记）。
//
// 背景（20260919 生产实测）：部分上游（渠道抖动/中断/非标准实现）流式末帧不带 usage，
// 旧实现落 FloorMicro（保底 1000 微元）——而实际输出可能上万 token。
// 生产实锤：tide 线 202 笔 estimated 请求合计 78,789 输出 token 只收 113,140 微元，
// 按价目应收约 60 万微元，**少记约 5 倍**；用户余额充足时这部分即站方净亏。
//
// 口径（20260923 标准化）：入参改为 **token 数**而非字节数，由调用方用
// EstPromptTokens（输入）/ EstTokens（输出正文）统一估算——不再在此处做 `字节 / 4`，
// 避免各调用点各自为政，也避免把 JSON 结构开销算成 token。
// 结果仍与 floor 取大值，绝不低于保底。
func MeterObserved(p *PricingInfo, inTokens, outTokens int64) int64 {
	if p == nil || p.Mode != "per_token" {
		return 0
	}
	if inTokens < 0 {
		inTokens = 0
	}
	if outTokens < 0 {
		outTokens = 0
	}
	tokPrice := inTokens*p.InRate10/10000 + outTokens*p.OutRate10/10000
	if p.FloorMicro > 0 && tokPrice < p.FloorMicro {
		return p.FloorMicro
	}
	return tokPrice
}

// FaceCostObserved 无上游 usage 时的面值成本估算（台账口径，与 MeterObserved 同源）。
// 用于「上游实际消耗了但未报 usage」场景的成本留痕——成本台账少记会让利润统计虚高，
// 掩盖真实亏损（站长红线：宁可多记不可少记）。入参同 MeterObserved 为 token 数。
func FaceCostObserved(inCostRate10, outCostRate10, inTokens, outTokens int64) int64 {
	roundM := func(tokens, rate10 int64) int64 {
		if tokens < 0 {
			tokens = 0
		}
		return (tokens*rate10 + 5_000) / 10_000
	}
	return roundM(inTokens, inCostRate10) + roundM(outTokens, outCostRate10)
}

// —— 统一 token 估算（20260923 标准化，全站唯一口径）——
//
// 背景：此前全站**三套估算口径并存，且都把 JSON 结构开销（字段名 / 引号 / 括号）
// 当成了 token 计数**：
//   - preholdAmount：len(messages 原文 JSON) / 4 × 1.2 + 16
//   - inBytes → MeterObserved：len(messages 原文 JSON) / 4
//   - kiro：len(**整个请求体**) / 4（连 model / stream / max_tokens 都算进去，最离谱）
//
// 生产实证虚增幅度：`{"role":"user","content":"hi"}` = 30 字节 → 估 7 token，
// 而真实约 1 token —— **高估约 7 倍**。短消息密集的会话（agentic 编码场景的常态）
// 输入侧被系统性超收，且随消息条数线性放大（每条消息的结构开销约 30 字节 ≈ 7 token）。
//
// 现统一为**只统计正文文本**：token ≈ ASCII 字符数 / 4 + CJK 字符数 × 1

// EstTokens 文本 token 估算（全站唯一口径）。
//
// CJK 按 1 token/字符：中文 UTF-8 占 3 字节，若沿用「字节 / 4」只算 0.75 token/字，
// 会**低估 25%**——与「宁可多记不可少记」红线相悖。ASCII 仍按 4 字符/token（英文经验值）。
func EstTokens(text string) int64 {
	var ascii, cjk int64
	for _, r := range text {
		switch {
		case r < 0x80:
			ascii++
		case (r >= 0x2E80 && r <= 0x9FFF) || // CJK 部首 / 汉字
			(r >= 0x3000 && r <= 0x303F) || // CJK 标点
			(r >= 0xAC00 && r <= 0xD7AF) || // 韩文
			(r >= 0x3040 && r <= 0x30FF): // 日文假名
			cjk++
		default:
			ascii += 2 // 其它非 ASCII（拉丁扩展 / emoji 等）：按 2 字符当量
		}
	}
	n := ascii/4 + cjk
	if n < 1 {
		n = 1
	}
	return n
}

// EstTokensFromBytes 字节序列的 token 估算（输出侧用：SSE 正文增量累计的是字节）。
func EstTokensFromBytes(b []byte) int64 {
	return EstTokens(string(b))
}

// EstPromptTokens 从 chat/completions（或 completions）请求体估算**输入** token。
// 只统计 messages[].content / prompt 的正文（剥离 JSON 结构开销，旧口径的主要虚增来源），
// 并为**每条消息加 msgFramingTokens** —— 对齐主流 tokenizer 的每消息框架开销
// （role 标记 + 分隔符，OpenAI/Claude 实际tokenizer 均约 3~4 token/消息）。
// 不加这一项会低估多轮会话的输入（与「宁可多记不可少记」红线相悖）。
func EstPromptTokens(body []byte) int64 {
	var req struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Prompt json.RawMessage `json:"prompt"`
	}
	if json.Unmarshal(body, &req) != nil {
		return 1
	}
	if len(req.Messages) == 0 {
		return EstTokens(rawTextOf(req.Prompt))
	}
	var sb strings.Builder
	for i := range req.Messages {
		sb.WriteString(rawTextOf(req.Messages[i].Content))
		sb.WriteByte('\n')
	}
	return EstTokens(sb.String()) + int64(len(req.Messages))*msgFramingTokens
}

// msgFramingTokens 每条消息的框架开销（role 标记 + 分隔符），对齐主流 tokenizer 经验值。
const msgFramingTokens = 4

// rawTextOf 取 content 字段的文本，兼容两种合法形态：
//   - 字符串："hello"
//   - 多模态数组：[{"type":"text","text":"hello"}, {"type":"image_url",...}]（只取 text 部分）
//
// 其它形态（null / 对象 / 非法）按空串处理——图片不折算 token（上游按图计费另行处理）。
func rawTextOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var sb strings.Builder
		for i := range parts {
			sb.WriteString(parts[i].Text)
		}
		return sb.String()
	}
	return ""
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
	if lineMode == "crowd" {
		return "normal" // 众筹池（acu/）不分 VIP：全体统一按普通渠道零售价扣池，与个人价格组无关
	}
	col := "price_grp_token"
	if lineMode == "per_call" {
		col = "price_grp_call"
	}
	var g string
	if err := d.QueryRow("SELECT "+col+" FROM users WHERE id=?", userID).Scan(&g); err != nil {
		// 查询失败（瞬时 DB 抖动）静默回退 normal 会把高折扣组按原价多扣——必须留痕
		log.Printf("[billing] UserPriceGrp 查询失败 uid=%d（回退 normal）: %v", userID, err)
		return "normal"
	}
	if g == "" {
		return "normal"
	}
	return g
}

// —— 2 号折扣子钱包（20260924 站长指令）——
//
// 定位：折扣专用钱包，与主钱包**完全独立**、各记各的账。
//   - 主钱包（wallet=1）→ users.balance_micro，全部模型按常规价
//   - 折扣钱包（wallet=2）→ users.balance2_micro，仅"存在 pricing(model,'wallet2')
//     价目行"的模型可用（当前 = prime/TokenLinks 国模 8 个），按折扣价
//
// 站长红线：
//  1. **禁止两钱包互转**（绝不能从 1 号钱包划余额到 2 号钱包）——只允许独立充值；
//  2. 失败退款**必须退回原钱包**——故钱包随预扣流水落库（balance_flows.wallet），
//     结算/退款一律从流水读回，绝不按"当前默认钱包"猜（否则折扣余额会漏进主钱包）。
const (
	WalletMain     = 1 // 主钱包
	WalletDiscount = 2 // 2 号折扣钱包
)

// WalletColumn 钱包 → 余额列名。集中一处，避免 if wallet==2 分支散落各处
// （充值入账、管理台调账、余额查询都复用，防止列名写错导致扣错钱包）。
func WalletColumn(wallet int) string {
	if wallet == WalletDiscount {
		return "balance2_micro"
	}
	return "balance_micro"
}

// WalletBalances 读用户两个钱包余额（主钱包, 折扣钱包）。供控制台展示与对账。
func WalletBalances(d *sql.DB, userID int64) (int64, int64, error) {
	var b1, b2 int64
	err := d.QueryRow("SELECT balance_micro, balance2_micro FROM users WHERE id=?", userID).Scan(&b1, &b2)
	return b1, b2, err
}

// Prehold 发起请求前预扣（先付后用）：余额不足返回错误（429 insufficient_quota 口径）。
// 政策硬门槛：使用收费模型须保持账户 0 元以上余额（balance_micro>0 显式政策位，
// 预扣额恒正时与 >= 等价；防未来 amount=0 路径绕过，绝不透支、绝无事后追缴）。
// 原子条件 UPDATE（与 Rust 版双进程并发访问同一生产库时无竞态：扣不满足即失败）。
// wallet：1=主钱包 2=折扣钱包（见上文红线；钱包随预扣流水落库，供结算/退款原路回退）。
func Prehold(d *sql.DB, userID, amount int64, requestID int64, wallet int) error {
	if amount < 0 {
		amount = 0
	}
	col := WalletColumn(wallet)
	return tx(d, func(tx *sql.Tx) error {
		res, err := tx.Exec(
			"UPDATE users SET "+col+"="+col+"-? WHERE id=? AND status=1 AND "+col+">=? AND "+col+">0",
			amount, userID, amount)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			// 用户存在但余额不足（或用户不存在/被禁用）——禁用伪装成余额不足会把
			// 用户引导去充值、充了也调不了，必须区分口径
			var st int64
			if e := tx.QueryRow("SELECT status FROM users WHERE id=?", userID).Scan(&st); e != nil {
				return e
			}
			if st != 1 {
				return ErrAccountDisabled
			}
			return ErrInsufficientBalance
		}
		var balance int64
		if err := tx.QueryRow("SELECT "+col+" FROM users WHERE id=?", userID).Scan(&balance); err != nil {
			return err
		}
		return insertFlow(tx, userID, requestID, "prehold", -amount, balance, 0, "", wallet)
	})
}

// preholdWallet 取某请求预扣所属的钱包（1=主钱包 2=折扣钱包）。
// 结算/退款一律按**预扣时的钱包**回退（站长红线 2）；无预扣流水（免费/众筹线、
// 历史存量数据）一律按主钱包口径兜底。
func preholdWallet(tx *sql.Tx, requestID int64) int {
	if requestID <= 0 {
		return WalletMain
	}
	var w int
	if err := tx.QueryRow(
		"SELECT wallet FROM balance_flows WHERE request_id=? AND type='prehold' ORDER BY rowid LIMIT 1",
		requestID).Scan(&w); err != nil || w != WalletDiscount {
		return WalletMain
	}
	return WalletDiscount
}

// Settle 完成后结算（多退少补，对齐 one-api/new-api 精准口径）：
// 应扣 = final（按实际 usage 精算）；已预扣 = preheld；delta = preheld - final。
// delta>0 退回；delta<0 补扣（长输出/估算偏差）——追扣 min(超支额, 当前余额)，余额扣到 0 为止、
// 绝不产生负余额（政策红线：先付后用绝不透支）；仍不足的极端差额由站方兜底并经流水留痕。
// 失败请求 final=0 → 全额退回。
// 钱包口径：从本请求的预扣流水读回（**不是**入参）——确保多退少补都发生在同一钱包。
func Settle(d *sql.DB, userID, preheld, final int64, requestID int64, unitPrice int64, note string) error {
	return tx(d, func(tx *sql.Tx) error {
		wallet := preholdWallet(tx, requestID)
		col := WalletColumn(wallet)
		var balance int64
		if err := tx.QueryRow("SELECT "+col+" FROM users WHERE id=?", userID).Scan(&balance); err != nil {
			return err
		}
		var charged int64
		switch {
		case final == 0:
			// 全额退回：余额已含预扣，无需变动
			charged = 0
			if preheld > 0 {
				if _, err := tx.Exec("UPDATE users SET "+col+"="+col+"+? WHERE id=?", preheld, userID); err != nil {
					return err
				}
				balance += preheld
			}
		case final > preheld:
			// 补扣：以上游实际 usage 为准，追扣到余额上限（不产生负余额）
			owe := final - preheld
			take := owe
			if take > balance {
				take = balance
			}
			if take > 0 {
				if _, err := tx.Exec("UPDATE users SET "+col+"="+col+"-? WHERE id=?", take, userID); err != nil {
					return err
				}
				balance -= take
			}
			charged = preheld + take
			if charged < final {
				note = note + "|shortfall=" + strconv.FormatInt(final-charged, 10)
			}
		default:
			// 退回多预扣部分
			if preheld > final {
				if _, err := tx.Exec("UPDATE users SET "+col+"="+col+"+? WHERE id=?", preheld-final, userID); err != nil {
					return err
				}
				balance += preheld - final
			}
			charged = final
		}
		flowType := "billed"
		amount := -charged
		if final == 0 {
			// 全额退回 flow amount 固定记 0（对账公式口径：prehold 扣款行不计入重放，
			// 退回资金不得重复计入；退回事实由 balance_after 与 requests.error 留痕）
			flowType = "refunded"
			amount = 0
		}
		return insertFlow(tx, userID, requestID, flowType, amount, balance, unitPrice, note, wallet)
	})
}

// insertFlow 计费流水（只增不删）。wallet 记流水归属钱包，供退款原路回退与按钱包对账。
func insertFlow(tx *sql.Tx, userID, requestID int64, flowType string, amount, balanceAfter, unitPrice int64, note string, wallet int) error {
	_, err := tx.Exec(
		`INSERT INTO balance_flows (user_id, request_id, type, amount_micro, balance_before_micro, balance_after_micro, unit_price_micro, note, operator, ts, wallet)
		 VALUES (?,?,?,?,?,?,?,?, 'system', ?, ?)`,
		userID, requestID, flowType, amount, balanceAfter-amount, balanceAfter, unitPrice, note, time.Now().Unix(), wallet)
	return err
}

// RefundPreholdIfUnsettled 补偿任务专用：单事务内原子完成「检查悬空 + 全额退款」。
// 若该请求在同一事务内已存在 billed/refunded 结算流（正常结算先到），返回 false 不退款，
// 彻底消除「先扫描快照、后逐条退款」窗口内请求苏醒结算导致的二次退款（TOCTOU）。
// 钱包同 Settle：从预扣流水读回，退回原钱包。
func RefundPreholdIfUnsettled(d *sql.DB, userID, preheld, requestID int64, note string) (bool, error) {
	done := false
	err := tx(d, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM balance_flows
			WHERE request_id=? AND type IN ('billed','refunded')`, requestID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return nil // 已有结算流：不退款（done=false）
		}
		wallet := preholdWallet(tx, requestID)
		col := WalletColumn(wallet)
		var balance int64
		if err := tx.QueryRow("SELECT "+col+" FROM users WHERE id=?", userID).Scan(&balance); err != nil {
			return err
		}
		if preheld > 0 {
			if _, err := tx.Exec("UPDATE users SET "+col+"="+col+"+? WHERE id=?", preheld, userID); err != nil {
				return err
			}
			balance += preheld
		}
		if err := insertFlow(tx, userID, requestID, "refunded", 0, balance, 0, note, wallet); err != nil {
			return err
		}
		done = true
		return nil
	})
	return done, err
}

// BreakevenFloor 保本线（亏损防线）：per_call = ceil(cost / 0.97)（支付通道统一 3% 费率口径，2026-09-13 起）。
// per_token 返回 0：按量成本随 token 线性、售价比例恒定，单点 floor 无意义——
// 按量线的保本校验由 IsBelowCost 逐段比对（20260919 计费审计补上，此前按量线无任何防线）。
func BreakevenFloor(costMicro int64, mode string) int64 {
	if mode == "per_token" {
		return 0
	}
	if costMicro <= 0 {
		return 0
	}
	return (costMicro*100 + 96) / 97
}

// IsBelowCost 逐段保本校验（**按量线专用**，20260919 计费审计新增）。
//
// 背景：per_token 线的 BreakevenFloor 恒返回 0，播种与管理台都没有任何"售价 ≥ 成本"的
// 逐段校验——生产实测已出现亏本配置（tide 线 qwen-image-2.0 售价 100000 微元 < 成本 200000 微元，
// 每卖一张亏 0.1 元）。按量线同样是"卖一次亏一次"。
//
// 口径：三段价各自不得低于成本段（含 3% 支付通道费 → 成本需放大到 /0.97）。
// 任一段低于成本即判定亏本。返回 (是否亏本, 说明)。
// 注意：这是**告警口径**，不阻断启动（项目铁律：启动期防线只许告警+跳过，绝不返回错误）。
func IsBelowCost(mode string, sellIn, sellCache, sellOut, costIn, costCache, costOut int64) (bool, string) {
	if mode != "per_token" {
		return false, ""
	}
	// 成本为 0 表示未配置成本台账 → 无法判定，不告警（避免误报刷屏）
	if costIn == 0 && costCache == 0 && costOut == 0 {
		return false, ""
	}
	need := func(cost int64) int64 {
		if cost <= 0 {
			return 0
		}
		return (cost*100 + 96) / 97 // 含 3% 通道费的保本下限
	}
	type seg struct {
		name       string
		sell, cost int64
	}
	segs := []seg{
		{"输入", sellIn, costIn},
		{"缓存", sellCache, costCache},
		{"输出", sellOut, costOut},
	}
	for _, s := range segs {
		if s.cost <= 0 {
			continue // 该段未配置成本：跳过（不误报）
		}
		if s.sell < need(s.cost) {
			return true, s.name + "段售价 " + itoa(s.sell) + " 低于保本线 " + itoa(need(s.cost)) +
				"（成本 " + itoa(s.cost) + " × 1/0.97）"
		}
	}
	return false, ""
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

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
