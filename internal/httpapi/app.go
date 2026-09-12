package httpapi

import (
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
	Mail *mail.Sender // 验证码发信（未配置为 nil，注册/找回不可用）

	AvatarsDir string // 用户头像目录（<db目录>/avatars，文件名 <uid>.<ext>）

	clientsMu sync.Mutex
	clients   map[string]*upstream.Client
}

// New 构造
func New(c *config.Cfg, d *db.DBx) *App {
	avatars := ""
	if c.Database.Path != "" {
		avatars = filepath.Join(filepath.Dir(c.Database.Path), "avatars")
	}
	return &App{Cfg: c, DB: d, Mail: mail.New(c.SMTP), AvatarsDir: avatars}
}
