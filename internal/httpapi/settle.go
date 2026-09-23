package httpapi

// 结算安全层 + 错误中心埋点 + 资金补偿任务（诊断报告 D1/D2）
// 背景：全站 16 处 `_ = billing.Settle(...)` 吞错，叠加进程中断，
// 产生悬空预扣与 orphan billed（详见 private/收费接口故障与对账不一致诊断报告及修复方案.md）

import (
	"log"
	"strconv"
	"time"

	"acu-aqua/gateway/internal/billing"
	"acu-aqua/gateway/internal/config"
)

// settleFor 结算分发（20260921 众筹线恢复）：
//   - crowd（acu/ 众筹线）→ 从**公共池**按次扣账，不扣个人余额（池子是共享钱包）
//   - 其余线 → 个人余额结算（多退少补）
func (a *App) settleFor(line *config.Line, uid, preheld, final, rid, unitPrice int64, note string) {
	if line != nil && line.Mode == "crowd" {
		// crowd 线不预扣个人余额；防御性处理：若历史遗留有预扣则原路退回，避免用户资金悬空
		if preheld > 0 {
			a.settleSafely(uid, preheld, 0, rid, 0, note+"|crowd_refund")
		}
		if final > 0 {
			// 池子按次扣账（失败重试 + 错误中心在 poolConsume 内）；note 记模型名，供个人众筹明细展示
			model := ""
			_ = a.DB.QueryRow("SELECT COALESCE(model,'') FROM requests WHERE rowid=?", rid).Scan(&model)
			a.poolConsume(uid, final, rid, model)
		}
		return
	}
	a.settleSafely(uid, preheld, final, rid, unitPrice, note)
}

// settleSafely 结算兜底：失败按 50/200/800ms 指数退避重试 3 次，
// 仍失败则落 error_events（错误中心）+ 标准日志，绝不再静默吞错。
func (a *App) settleSafely(uid, preheld, final, rid, unitPrice int64, note string) {
	if billing.Settle(a.DB.DB, uid, preheld, final, rid, unitPrice, note) == nil {
		return
	}
	for _, d := range []time.Duration{50 * time.Millisecond, 200 * time.Millisecond, 800 * time.Millisecond} {
		time.Sleep(d)
		if billing.Settle(a.DB.DB, uid, preheld, final, rid, unitPrice, note) == nil {
			return
		}
	}
	a.logError("settle_failed", "", uid, rid, "note="+note)
	log.Printf("[billing] 结算重试耗尽 uid=%d rid=%d note=%s（已落错误中心，等待补偿任务）", uid, rid, note)
}

// logError 错误中心埋点（error_events 只增不删；管理端可查，诊断 D2）
func (a *App) logError(kind, model string, uid, rid int64, detail string) {
	_, _ = a.DB.Exec(
		"INSERT INTO error_events (kind, model, user_id, request_id, detail, ts) VALUES (?,?,?,?,?,?)",
		kind, model, uid, rid, detail, time.Now().Unix())
}

// startCompensator 资金补偿任务：启动即扫一次，此后每小时一轮（诊断 D1）。
// ① 悬空预扣：prehold 流水后 2 小时仍无 billed/refunded 结算流 → 全额退款（防用户资金悬空）
// ② orphan billed：requests 记账 billed 但无对应结算流（历史存量）→ 补写台账（不动余额）
func (a *App) startCompensator() {
	a.ensureFlowUniqueIndex()
	go func() {
		time.Sleep(10 * time.Second) // 等服务就绪
		for {
			a.compensateOnce()
			time.Sleep(time.Hour)
		}
	}()
}

// ensureFlowUniqueIndex 去重后建局部唯一索引（request_id>0），保证补偿/结算幂等。
// 历史数据可能存在同 request 同 type 重复行，先清重再建；索引已存在则跳过。
// 邀请返利幂等索引：invite_rebate 按 request_id 防重放（request_id<=0 的历史行不参与）。
// 注意：索引创建失败时结算/补偿的应用层幂等仍是最后一道防线，绝不能删掉重试逻辑。
func (a *App) ensureFlowUniqueIndex() {
	_, _ = a.DB.Exec(`DELETE FROM balance_flows WHERE rowid NOT IN
		(SELECT MIN(rowid) FROM balance_flows WHERE request_id>0 GROUP BY request_id, type) AND request_id>0`)
	if _, err := a.DB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_bflows_req_type
		ON balance_flows(request_id, type) WHERE request_id>0`); err != nil {
		log.Printf("[billing] 警告：balance_flows 唯一索引创建失败，重放/补偿并发将失去库级幂等屏障（请尽快清理历史脏数据后重启）: %v", err)
	}
	if _, err := a.DB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_bflows_invite_rebate
		ON balance_flows(request_id) WHERE type='invite_rebate' AND request_id>0`); err != nil {
		log.Printf("[billing] 警告：invite_rebate 幂等索引创建失败: %v", err)
	}
}

func (a *App) compensateOnce() {
	cutoff := time.Now().Unix() - 2*3600 // 2 小时前（给长流式请求留足结算窗口）

	// ① 悬空预扣 → 全额退款（复用 Settle 语义：退回 preheld 并写 refunded 流水）
	// 守卫：只退「请求未成功」的悬空——ok=1 且 billed 的请求若结算流水缺失，
	// 说明服务已交付（结算重试耗尽），退款会白送，交由补偿②补写台账。
	rows, err := a.DB.Query(`
		SELECT f.user_id, f.request_id, -f.amount_micro
		FROM balance_flows f
		LEFT JOIN requests r ON r.rowid=f.request_id
		WHERE f.type='prehold' AND f.request_id>0 AND f.amount_micro<0 AND f.ts<?
		  AND (r.rowid IS NULL OR r.ok=0)
		  AND NOT EXISTS (SELECT 1 FROM balance_flows g
		                  WHERE g.request_id=f.request_id AND g.type IN ('billed','refunded'))
		LIMIT 500`, cutoff)
	if err != nil {
		log.Printf("[billing] 补偿任务①查询失败: %v", err)
	} else {
		type pend struct {
			uid, rid, amount int64
		}
		var list []pend
		for rows.Next() {
			var p pend
			if rows.Scan(&p.uid, &p.rid, &p.amount) == nil && p.amount > 0 {
				list = append(list, p)
			}
		}
		rows.Close()
		for _, p := range list {
			// 原子「检查+退款」：正常结算若在扫描与执行之间先落 billed，此处直接跳过，
			// 杜绝同一请求先 billed 后 refunded 的双重退款（TOCTOU）
			refunded, err := billing.RefundPreholdIfUnsettled(a.DB.DB, p.uid, p.amount, p.rid, "compensated_prehold")
			if err != nil {
				a.logError("compensate_failed", "", p.uid, p.rid, "refund_prehold: "+err.Error())
				continue
			}
			if !refunded {
				continue // 已有结算流（请求苏醒后正常结算），无需补偿
			}
			_, _ = a.DB.Exec("UPDATE requests SET ok=0, error='compensated_prehold', status_code=504, bill_state='refunded' WHERE rowid=? AND ok=0", p.rid)
			log.Printf("[billing] 补偿：悬空预扣已退款 uid=%d rid=%d amount=%d", p.uid, p.rid, p.amount)
		}
		if len(list) > 0 {
			a.logError("compensate_prehold", "", 0, 0, "refunded_count="+strconv.Itoa(len(list)))
		}
	}

	// ② orphan billed → 补结算/补台账（requests 已记账 billed 但个人流水缺失）
	// 排除 crowd/池子线请求：它们从不在个人 balance_flows 结算（走 pool_flows），
	// 按个人流水缺失判定会把每笔成功的 crowd 请求误补成假扣费流水。
	// 同时带出悬空 prehold：结算重试耗尽的成功请求，需走真实多退少补（Settle），
	// 而非仅补台账——否则用户按预扣额买单、与应收 final 的差额永久悬空。
	rows2, err := a.DB.Query(`
		SELECT r.user_id, r.rowid, r.bill_amount_micro, r.ts,
		       COALESCE((SELECT -f.amount_micro FROM balance_flows f
		                 WHERE f.request_id=r.rowid AND f.type='prehold' AND f.amount_micro<0
		                 LIMIT 1), 0)
		FROM requests r
		WHERE r.bill_state='billed' AND r.billed=1 AND r.bill_amount_micro>0 AND r.user_id>0 AND r.ts<?
		  AND NOT EXISTS (SELECT 1 FROM balance_flows g
		                  WHERE g.request_id=r.rowid AND g.type IN ('billed','refunded'))
		  AND NOT EXISTS (SELECT 1 FROM pool_flows pf WHERE pf.request_id=r.rowid)
		LIMIT 500`, cutoff)
	if err != nil {
		log.Printf("[billing] 补偿任务②查询失败: %v", err)
		return
	}
	type ob struct {
		uid, rid, amount, ts, preheld int64
	}
	var obs []ob
	for rows2.Next() {
		var o ob
		if rows2.Scan(&o.uid, &o.rid, &o.amount, &o.ts, &o.preheld) == nil {
			obs = append(obs, o)
		}
	}
	rows2.Close()
	backfilled := 0
	for _, o := range obs {
		if o.preheld > 0 {
			// 服务已交付、结算重试耗尽：真实多退少补（Settle 幂等由唯一索引+本查询的
			// NOT EXISTS 守卫双保险）。失败落错误中心，下一轮不再误入本分支（已有流水）。
			if err := billing.Settle(a.DB.DB, o.uid, o.preheld, o.amount, o.rid, o.amount, "compensated_settle"); err != nil {
				a.logError("compensate_failed", "", o.uid, o.rid, "settle_orphan: "+err.Error())
				continue
			}
			log.Printf("[billing] 补偿：orphan billed 已补结算 uid=%d rid=%d preheld=%d final=%d", o.uid, o.rid, o.preheld, o.amount)
			backfilled++
			continue
		}
		// 无 prehold（免费线残留/历史数据）：仅补台账。行内自洽：amount=0（不动余额，
		// 0+0=0 满足 balance_after=before+amount），应收价记 unit_price_micro 供收入统计，
		// 杜绝 before=0/after=0/amount=-x 的自相矛盾流水
		if _, err := a.DB.Exec(`INSERT INTO balance_flows
			(user_id, request_id, type, amount_micro, balance_before_micro, balance_after_micro, unit_price_micro, note, operator, ts)
			VALUES (?,?, 'billed', 0, 0, 0, ?, 'backfill', 'system', ?)`,
			o.uid, o.rid, o.amount, o.ts); err != nil {
			a.logError("compensate_failed", "", o.uid, o.rid, "backfill_billed: "+err.Error())
			continue
		}
		backfilled++
	}
	if backfilled > 0 {
		a.logError("compensate_backfill", "", 0, 0, "backfilled_count="+strconv.Itoa(backfilled))
		log.Printf("[billing] 补偿：orphan billed 已补结算/补台账 %d 笔", backfilled)
	}
}
