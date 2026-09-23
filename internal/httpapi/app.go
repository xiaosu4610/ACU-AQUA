package httpapi

import (
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"acu-aqua/gateway/internal/config"
	"acu-aqua/gateway/internal/db"
	"acu-aqua/gateway/internal/mail"
	"acu-aqua/gateway/internal/upstream"
)

// App 全局上下文
type App struct {
	Cfg  *config.Cfg
	DB   *db.DBx
	Mail *mail.Sender // 阿里云 SMTP（兜底通道：试探/全灭接管）

	MailPool *MailPool // 微软邮箱发信池（站长定稿 20260916：全域主线路）

	AvatarsDir string // 用户头像目录（<db目录>/avatars，文件名 <uid>.<ext>）

	// linesMu 保护 Cfg.Lines 的热重载替换（上游管理）：
	// reload 整体替换 slice（新底层数组，从不原地改元素），读方拿到的快照/元素指针锁外使用安全。
	linesMu sync.RWMutex

	clientsMu sync.Mutex
	clients   map[string]*upstream.Client

	codexProxy *codexProxyHealth // codex 线代理健康状态（探测/换线，codexops.go）
}

// New 构造（上游线路 DB 优先：admin_lines 有数据则以 DB 为事实源）
func New(c *config.Cfg, d *db.DBx) *App {
	avatars := ""
	if c.Database.Path != "" {
		avatars = filepath.Join(filepath.Dir(c.Database.Path), "avatars")
	}
	if err := initLinesFromStore(c, d.DB); err != nil {
		// 种子/加载失败不阻断启动：保留 toml 配置继续服务
		log.Printf("[lines] 上游线路 DB 加载失败（回退 toml 配置）: %v", err)
	}
	app := &App{Cfg: c, DB: d, Mail: mail.New(c.SMTP), AvatarsDir: avatars, MailPool: newMailPool()}
	// 诊断 D2：站点 5xx 统一落错误中心（error_events）
	errSink = func(kind, detail string) { app.logError("api_"+kind, "", 0, 0, detail) }
	// 诊断 D1：资金补偿任务（悬空预扣退款 / orphan billed 补台账）
	app.startCompensator()
	// NVIDIA 动态目录同步器（启动 30s 后首跑 + 每小时，nvidia_sync.go）
	app.startNvidiaSyncer()
	// Codex 代理保活探测 + 自动换线（启动 15s 后首跑 + 每 5 分钟，codexops.go）
	app.startCodexProbe()
	// 微软发信池活体巡检（启动 1min 后首跑 + 每 6h，mailpool.go）
	app.StartMailProbe()
	// 邀请返利结算器（消费计费成功队列，invite.go）
	app.StartInviteRebater()
	// 数据保留清理（管理端承诺 90 天自动清理，admin_stats.go 文案）：requests/error_events 留 90 天，arena_battles 留 7 天
	app.startRetentionCleaner()
	// 支付订单对账（漏单自愈）：回调丢失/回调域名不可达时主动向上游查单补账，20260919
	app.startPayReconciler()
	return app
}

// startPayReconciler 支付订单对账：启动 2 分钟后首跑，此后每 10 分钟一轮。
// 背景（20260919 生产实锤）：notify_base 曾配置到不可达域名，回调收不到 → 订单滞留 pending，
// 用户已付款但余额不到账（仅靠前端轮询 /pay/status 的 10 秒查单自愈，用户关页面即漏单）。
func (a *App) startPayReconciler() {
	// 任一通道配好就要跑对账：自挂平台回调**只发一次不重试**，对账是硬兜底
	if (a.Cfg.EPay.Gateway == "" || a.Cfg.EPay.PID == "" || a.Cfg.EPay.Key == "") &&
		(a.Cfg.Pay.Xiaofeng.Gateway == "" || a.Cfg.Pay.Xiaofeng.PID == "" || a.Cfg.Pay.Xiaofeng.Key == "") {
		return // 两条通道都没配置，无需对账
	}
	go func() {
		time.Sleep(2 * time.Minute)
		for {
			a.payReconcileOnce()
			time.Sleep(10 * time.Minute)
		}
	}()
}

// payReconcileOnce 扫描 2 分钟~7 天内仍 pending 的订单，逐笔向上游查单：
// 已支付则补账（epaySettle 幂等），未支付保持 pending。单轮上限 50 笔（防上游限流）。
// ⚠️ 排序必须 ASC（20260922 修）：原为 `ORDER BY id DESC` 只取**最新** 50 笔，而 pending 长期
// 有数百笔 → 新订单不断挤占窗口，**旧 pending 永远轮不到**，回调一旦丢失即永久漏账。
// 本函数只是**兜底**（正常路径靠回调 + 用户点"我已支付立即查询"），故取最旧的先补，
// 保证任何 pending 订单都在有限轮次内被覆盖，不会饿死。
func (a *App) payReconcileOnce() {
	now := time.Now().Unix()
	rows, err := a.DB.Query(`SELECT out_trade_no, amount_micro, COALESCE(provider,'epay') FROM payments
		WHERE status='pending' AND created_ts < ? AND created_ts > ? ORDER BY created_ts ASC LIMIT 50`,
		now-120, now-7*86400)
	if err != nil {
		log.Printf("[pay] 对账查询失败: %v", err)
		return
	}
	type item struct {
		no       string
		amount   int64
		provider string
	}
	var list []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.no, &it.amount, &it.provider) == nil {
			list = append(list, it)
		}
	}
	rows.Close()
	settled := 0
	for _, it := range list {
		if a.payQueryAndSettle(it.provider, it.no, it.amount) {
			settled++
		}
	}
	if settled > 0 {
		log.Printf("[pay] 对账补账完成：扫描 %d 笔，补账 %d 笔", len(list), settled)
		a.auditAppend("pay_reconcile", 0, fmt.Sprintf("scanned=%d settled=%d", len(list), settled), "")
	}
}

// startRetentionCleaner 数据保留清理：启动即跑一次，此后每 24h 一轮
func (a *App) startRetentionCleaner() {
	go func() {
		time.Sleep(time.Minute) // 等服务就绪
		for {
			a.retentionCleanOnce()
			time.Sleep(24 * time.Hour)
		}
	}()
}

// retentionCleanOnce 按 retention 承诺清理过期台账（清理失败仅记日志，不阻断其余表）
func (a *App) retentionCleanOnce() {
	now := time.Now().Unix()
	for _, j := range []struct {
		table  string
		cutoff int64
	}{
		{"requests", now - 90*86400},
		{"error_events", now - 90*86400},
		{"arena_battles", now - 7*86400},
	} {
		res, err := a.DB.Exec("DELETE FROM "+j.table+" WHERE ts < ?", j.cutoff)
		if err != nil {
			log.Printf("[retention] %s 清理失败: %v", j.table, err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("[retention] %s 已清理 %d 行（%d 天前）", j.table, n, (now-j.cutoff)/86400)
		}
	}
}

// lineByID 线查找（读锁）
func (a *App) lineByID(id string) *config.Line {
	a.linesMu.RLock()
	defer a.linesMu.RUnlock()
	return a.Cfg.LineByID(id)
}

// lineForMode 按计费模式取第一条收费线（读锁）
func (a *App) lineForMode(mode string) *config.Line {
	a.linesMu.RLock()
	defer a.linesMu.RUnlock()
	return a.Cfg.LineForMode(mode)
}

// lineForModel 统一前缀模型感知选线：优先计费分组默认线持有该模型；
// 默认线没有时在同模式其他线里定位（同模式多线并存：如 tide/gpt 同为
// per_token，模型映射在哪个线就路由到哪个线，分组计费语义不变）。
// 全局都没有该模型时回退默认线（上层按「模型不在分组可用列表」404，保持原语义）
func (a *App) lineForModel(grp, siteID string) *config.Line {
	def := a.lineForMode(grp)
	if def != nil {
		for i := range def.Models {
			if def.Models[i].SiteID == siteID {
				return def
			}
		}
	}
	a.linesMu.RLock()
	defer a.linesMu.RUnlock()
	for i := range a.Cfg.Lines {
		l := &a.Cfg.Lines[i]
		if l.Mode != grp || l.AuthStyle == "codex" { // codex 线走专属前缀，不参与统一前缀路由
			continue
		}
		for j := range l.Models {
			if l.Models[j].SiteID == siteID {
				return l
			}
		}
	}
	return def
}

// linesSnap 线路快照（读锁内取 slice 头；reload 只整体替换，快照元素不可变）
func (a *App) linesSnap() []config.Line {
	a.linesMu.RLock()
	defer a.linesMu.RUnlock()
	return a.Cfg.Lines
}
