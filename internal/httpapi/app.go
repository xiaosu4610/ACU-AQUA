package httpapi

import (
	"sync"

	"acu-aqua/gateway/internal/config"
	"acu-aqua/gateway/internal/db"
	"acu-aqua/gateway/internal/upstream"
)

// App 全局上下文
type App struct {
	Cfg  *config.Cfg
	DB   *db.DBx

	clientsMu sync.Mutex
	clients   map[string]*upstream.Client
}

// New 构造
func New(c *config.Cfg, d *db.DBx) *App {
	return &App{Cfg: c, DB: d}
}
