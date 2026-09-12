package httpapi

// 结算安全层 + 错误中心埋点 + 资金补偿任务（诊断报告 D1/D2）
// 背景：全站 16 处 `_ = billing.Settle(...)` 吞错，叠加进程中断，
// 产生悬空预扣与 orphan billed（详见 private/收费接口故障与对账不一致诊断报告及修复方案.md）

import (
	"log"
	"strconv"
	"time"

	"acu-aqua/gateway/internal/billing"
)

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
func (a *App) ensureFlowUniqueIndex() {
	_, _ = a.DB.Exec(`DELETE FROM balance_flows WHERE rowid NOT IN
		(SELECT MIN(rowid) FROM balance_flows WHERE request_id>0 GROUP BY request_id, type) AND request_id>0`)
	if _, err := a.DB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_bflows_req_type
		ON balance_flows(request_id, type) WHERE request_id>0`); err != nil {
		log.Printf("[billing] balance_flows 唯一索引创建失败（历史脏数据，补偿任务仍可用）: %v", err)
	}
}

func (a *App) compensateOnce() {
	cutoff := time.Now().Unix() - 2*3600 // 2 小时前（给长流式请求留足结算窗口）

	// ① 悬空预扣 → 全额退款（复用 Settle 语义：退回 preheld 并写 refunded 流水）
	rows, err := a.DB.Query(`
		SELECT f.user_id, f.request_id, -f.amount_micro
		FROM balance_flows f
		WHERE f.type='prehold' AND f.request_id>0 AND f.amount_micro<0 AND f.ts<?
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
			if err := billing.Settle(a.DB.DB, p.uid, p.amount, 0, p.rid, 0, "compensated_prehold"); err != nil {
				a.logError("compensate_failed", "", p.uid, p.rid, "refund_prehold: "+err.Error())
				continue
			}
			_, _ = a.DB.Exec("UPDATE requests SET ok=0, error='compensated_prehold', status_code=504, bill_state='refunded' WHERE rowid=? AND ok=0", p.rid)
			log.Printf("[billing] 补偿：悬空预扣已退款 uid=%d rid=%d amount=%d", p.uid, p.rid, p.amount)
		}
		if len(list) > 0 {
			a.logError("compensate_prehold", "", 0, 0, "refunded_count="+strconv.Itoa(len(list)))
		}
	}

	// ② orphan billed → 补写台账（不动余额：requests 已记账，仅流水缺失）
	rows2, err := a.DB.Query(`
		SELECT r.user_id, r.rowid, r.bill_amount_micro, r.ts
		FROM requests r
		WHERE r.bill_state='billed' AND r.billed=1 AND r.bill_amount_micro>0 AND r.user_id>0 AND r.ts<?
		  AND NOT EXISTS (SELECT 1 FROM balance_flows g
		                  WHERE g.request_id=r.rowid AND g.type IN ('billed','refunded'))
		LIMIT 500`, cutoff)
	if err != nil {
		log.Printf("[billing] 补偿任务②查询失败: %v", err)
		return
	}
	type ob struct {
		uid, rid, amount, ts int64
	}
	var obs []ob
	for rows2.Next() {
		var o ob
		if rows2.Scan(&o.uid, &o.rid, &o.amount, &o.ts) == nil {
			obs = append(obs, o)
		}
	}
	rows2.Close()
	for _, o := range obs {
		_, err := a.DB.Exec(`INSERT INTO balance_flows
			(user_id, request_id, type, amount_micro, balance_before_micro, balance_after_micro, unit_price_micro, note, operator, ts)
			VALUES (?,?, 'billed', ?, 0, 0, ?, 'backfill', 'system', ?)`,
			o.uid, o.rid, -o.amount, o.amount, o.ts)
		if err != nil {
			a.logError("compensate_failed", "", o.uid, o.rid, "backfill_billed: "+err.Error())
			continue
		}
	}
	if len(obs) > 0 {
		a.logError("compensate_backfill", "", 0, 0, "backfilled_count="+strconv.Itoa(len(obs)))
		log.Printf("[billing] 补偿：orphan billed 已补写台账 %d 笔", len(obs))
	}
}
