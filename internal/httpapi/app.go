package httpapi

import (
	"log"
	"path/filepath"
	"sync"

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
	return app
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
