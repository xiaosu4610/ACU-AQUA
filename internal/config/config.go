// Package config 实现四层配置加载与合并：
// 代码内置默认值 < config.toml < 环境变量(AQUA_*) < 命令行 flag。
// 红线：代码只含机制，不含事实——所有上游地址、密钥、成本率、站点信息
// 全部来自配置，代码与注释中不出现任何真实运营数据。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Site 站点信息（前端 /v1/meta 渲染源，全部可自定义）
type Site struct {
	Name       string `toml:"name"`
	Domain     string `toml:"domain"`
	DocsURL    string `toml:"docs_url"`
	QQGroup    string `toml:"qq_group"`
	QQGroupURL string `toml:"qq_group_url"`
}

// Database 数据库配置
type Database struct {
	Driver string `toml:"driver"` // sqlite | postgres（预留）
	Path   string `toml:"path"`
}

// Model 模型映射：站内名 ↔ 上游名 + 成本率 + 售价率（rate10 整数口径）
type Model struct {
	SiteID           string `toml:"site_id"`
	UpstreamID       string `toml:"upstream_id"`
	Image            bool   `toml:"image"` // 图片模型（按张计费）
	PerImageCost     int64  `toml:"per_image_cost"`      // 成本（微元/张，内部台账）
	PerImageSell     int64  `toml:"per_image_sell"`      // 售价（微元/张，普通组）
	InCostRate10     int64  `toml:"in_cost_rate10"`      // 上游成本率（内部台账，绝不外泄）
	CacheCostRate10  int64  `toml:"cache_cost_rate10"`
	OutCostRate10    int64  `toml:"out_cost_rate10"`
	InSellRate10     int64  `toml:"in_sell_rate10"`      // 站点售价率（普通组）
	CacheSellRate10  int64  `toml:"cache_sell_rate10"`
	OutSellRate10    int64  `toml:"out_sell_rate10"`
	PerCallCost      int64  `toml:"per_call_cost"`  // 按次线：成本（微元/次，保本线用）
	PerCallSell      int64  `toml:"per_call_sell"`  // 按次线：售价（微元/次，普通组）
}

// Line 一条上游线：独立的转发 + 计费体系。新增上游 = 追加一个 [[lines]] 块。
type Line struct {
	ID           string   `toml:"id"`   // 线标识（对应模型前缀 line-id/model）
	Name         string   `toml:"name"` // 展示名
	Mode         string   `toml:"mode"` // per_call | per_token
	BaseURL      string   `toml:"base_url"`
	Keys         []string `toml:"keys"`
	AuthStyle    string   `toml:"auth_style"`     // bearer | x-api-key
	KeyFaceMicro int64    `toml:"key_face_micro"` // 每把密钥面值（微元，面值台账用；0=不限）
	// vip 折扣 = 普通售价 × VipNum/VipDen（分数表达，整数可除零精度损失）
	VipNum  int64    `toml:"vip_multiplier_num"`
	VipDen  int64    `toml:"vip_multiplier_den"`
	Models  []Model  `toml:"models"`
}

// Server HTTP 服务配置
type Server struct {
	Listen string `toml:"listen"` // 如 0.0.0.0:8787
	// LegacyUpstreamURL 旧网关（Rust）地址：绞杀者迁移期免费线/未移植端点反代到此，
	// 共享同一 SQLite 库。置空 = 纯 Go 单体模式（非收费模型直接 404）。
	LegacyUpstreamURL string `toml:"legacy_upstream_url"`
}

// Cfg 顶层配置
type Cfg struct {
	Site     Site     `toml:"site"`
	Database Database `toml:"database"`
	Server   Server   `toml:"server"`
	Lines    []Line   `toml:"lines"`
}

// Default 内置默认值（全部为安全占位，不含任何运营事实）
func Default() *Cfg {
	return &Cfg{
		Site:     Site{Name: "AQUA Gateway", Domain: "", DocsURL: "", QQGroup: "", QQGroupURL: ""},
		Database: Database{Driver: "sqlite", Path: "data/aqua.db"},
		Server:   Server{Listen: "0.0.0.0:8787"},
		Lines:    nil, // 未配置任何上游 = 空壳站，UI 显示待配置
	}
}

// Load 加载顺序：默认值 → toml 文件（若存在）→ 环境变量覆盖
func Load(path string) (*Cfg, error) {
	c := Default()
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, c); err != nil {
				return nil, fmt.Errorf("解析配置文件 %s: %w", path, err)
			}
		}
	}
	applyEnv(c)
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// applyEnv 环境变量覆盖（AQUA_ 前缀，机密类推荐只用 ENV 不落盘）
func applyEnv(c *Cfg) {
	if v := os.Getenv("AQUA_LISTEN"); v != "" {
		c.Server.Listen = v
	}
	if v := os.Getenv("AQUA_LEGACY_UPSTREAM_URL"); v != "" {
		c.Server.LegacyUpstreamURL = v
	}
	if v := os.Getenv("AQUA_DB_PATH"); v != "" {
		c.Database.Path = v
	}
	if v := os.Getenv("AQUA_SITE_NAME"); v != "" {
		c.Site.Name = v
	}
	if v := os.Getenv("AQUA_SITE_DOMAIN"); v != "" {
		c.Site.Domain = v
	}
	if v := os.Getenv("AQUA_SITE_DOCS_URL"); v != "" {
		c.Site.DocsURL = v
	}
	if v := os.Getenv("AQUA_SITE_QQ_GROUP"); v != "" {
		c.Site.QQGroup = v
	}
	if v := os.Getenv("AQUA_SITE_QQ_GROUP_URL"); v != "" {
		c.Site.QQGroupURL = v
	}
}

// validate 配置合法性校验
func (c *Cfg) validate() error {
	if c.Server.Listen == "" {
		return fmt.Errorf("server.listen 不能为空")
	}
	if c.Database.Driver != "sqlite" {
		return fmt.Errorf("database.driver 目前仅支持 sqlite（%s）", c.Database.Driver)
	}
	seen := map[string]bool{}
	for i := range c.Lines {
		l := &c.Lines[i]
		if l.ID == "" {
			return fmt.Errorf("lines[%d].id 不能为空", i)
		}
		if seen[l.ID] {
			return fmt.Errorf("lines[%d].id 重复：%s", i, l.ID)
		}
		seen[l.ID] = true
		if l.BaseURL == "" {
			return fmt.Errorf("line %s 缺少 base_url", l.ID)
		}
		if l.Mode != "per_call" && l.Mode != "per_token" {
			return fmt.Errorf("line %s mode 必须是 per_call 或 per_token", l.ID)
		}
		if l.AuthStyle == "" {
			l.AuthStyle = "bearer"
		}
		if l.Mode == "per_token" && l.VipDen == 0 {
			l.VipNum, l.VipDen = 1, 1 // 未配置 vip 折扣 = 不打折
		}
		for j := range l.Models {
			m := &l.Models[j]
			if m.SiteID == "" {
				return fmt.Errorf("line %s models[%d].site_id 不能为空", l.ID, j)
			}
			if m.UpstreamID == "" {
				m.UpstreamID = m.SiteID
			}
		}
	}
	return nil
}

// LineByID 按线 ID 查找
func (c *Cfg) LineByID(id string) *Line {
	for i := range c.Lines {
		if c.Lines[i].ID == id {
			return &c.Lines[i]
		}
	}
	return nil
}

// ModelFullName 站内完整模型名（line-id/site-id）
func ModelFullName(lineID, siteID string) string {
	return lineID + "/" + siteID
}

// SplitModel 拆解站内模型名 → (lineID, siteID)；非收费线模型返回 ok=false
func SplitModel(full string) (lineID, siteID string, ok bool) {
	i := strings.IndexByte(full, '/')
	if i <= 0 || i >= len(full)-1 {
		return "", "", false
	}
	return full[:i], full[i+1:], true
}

// envInt 环境变量读整数（内部工具）
func envInt(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
