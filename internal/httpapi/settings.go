// settings.go 站点配置外置化：settings kv 表（DB 优先，config 兜底）。
// 渠道/模型已在 admin_lines/nvidia_models/pricing 表 DB 化（后台可管），
// 本文件补齐站点展示层配置（站名/QQ/费率/公告），后台改完即时生效、无需改代码发版。
package httpapi

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// settingsSchema kv 配置表
const settingsSchema = `CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL DEFAULT '',
	updated_ts INTEGER NOT NULL DEFAULT 0)`

var settingsOnce sync.Once

func (a *App) ensureSettings() {
	settingsOnce.Do(func() {
		if _, err := a.DB.Exec(settingsSchema); err != nil {
			log.Printf("[settings] 建表失败: %v", err)
		}
	})
}

// settingsGet 读单键（无行/出错=空串）
func (a *App) settingsGet(key string) string {
	a.ensureSettings()
	var v string
	_ = a.DB.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	return v
}

// siteOverride DB 覆盖值：settings 有非空值则用之，否则回退 config（env/toml）兜底
func (a *App) siteOverride(key, fallback string) string {
	if v := a.settingsGet(key); v != "" {
		return v
	}
	return fallback
}

// settingsKeys 可后台修改的白名单键
var settingsKeys = map[string]bool{
	"site_name": true, "qq_group": true, "qq_group_url": true,
	"qq_group2": true, "qq_group_url2": true,
	"rate_promo": true, "rate_normal": true, "rate_promo_vip": true,
	"announcement": true, "announcement_enabled": true,
	"docs_url": true,
	// 在线充值总开关（20260924）：显式 "0" = 停售（新单 503），空/其他值 = 正常开放。
	// 语义上用"仅 0 关闭"而非"仅 1 开启"——保证历史库无此键时行为不变（默认开放），
	// 避免"加开关"这个动作本身把支付关掉。**已下单待支付的回调入账不受影响**。
	"pay_enabled": true,
	// 合作推广位（20260919：秘塔 AI 等广告后台可管，空=不展示）
	"ad_image": true, "ad_link": true, "ad_label": true,
}

// payEnabled 在线充值是否开放（settings 显式 "0" 才关闭；无键=开放，保证默认行为不变）
func (a *App) payEnabled() bool { return a.settingsGet("pay_enabled") != "0" }

// handleMeta 公开元信息：site 字段支持 DB 覆盖 + 公告直达前端（免再发请求）
func (a *App) handleMeta(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]any{
		"name":                 a.siteOverride("site_name", a.Cfg.Site.Name),
		"domain":               a.Cfg.Site.Domain,
		"docs_url":             a.siteOverride("docs_url", a.Cfg.Site.DocsURL),
		"qq_group":             a.siteOverride("qq_group", a.Cfg.Site.QQGroup),
		"qq_group_url":         a.siteOverride("qq_group_url", a.Cfg.Site.QQGroupURL),
		"qq_group2":            a.siteOverride("qq_group2", a.Cfg.Site.QQGroup2),
		"qq_group_url2":        a.siteOverride("qq_group_url2", a.Cfg.Site.QQGroupURL2),
		"rate_promo":           a.siteOverride("rate_promo", a.Cfg.Site.RatePromo),
		"rate_normal":          a.siteOverride("rate_normal", a.Cfg.Site.RateNormal),
		"rate_promo_vip":       a.siteOverride("rate_promo_vip", a.Cfg.Site.RatePromoVip),
		"announcement":         a.settingsGet("announcement"),
		"announcement_enabled": a.settingsGet("announcement_enabled") == "1",
		// 在线充值开关（20260924）：前端据此把「去支付」置灰并显示停售文案，
		// 而不是让用户填完金额点下去才吃 503
		"pay_enabled": a.payEnabled(),
		// 合作推广位（后台 settings 可改；链接/图/文案三键齐备才展示）
		"ad_image": a.settingsGet("ad_image"),
		"ad_link":  a.settingsGet("ad_link"),
		"ad_label": a.settingsGet("ad_label"),
	})
}

// handleAdminSettingsGet GET /v1/admin/settings → 全量白名单键值
func (a *App) handleAdminSettingsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	a.ensureSettings()
	out := map[string]string{}
	for k := range settingsKeys {
		out[k] = a.settingsGet(k)
	}
	jsonOut(w, 200, map[string]any{"settings": out})
}

// handleAdminSettingsSave POST /v1/admin/settings {key1:val1,...}（白名单外拒绝；空值=清除回退 config）
func (a *App) handleAdminSettingsSave(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var req map[string]string
	if err := adminBody(r, 16<<10, &req); err != nil {
		errOut(w, 400, "bad_request", "请求体格式错误")
		return
	}
	a.ensureSettings()
	now := time.Now().Unix()
	n := 0
	for k, v := range req {
		if !settingsKeys[k] {
			continue // 白名单外静默忽略（防误写敏感键）
		}
		if _, err := a.DB.Exec(
			`INSERT INTO settings (key, value, updated_ts) VALUES (?,?,?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_ts=excluded.updated_ts`,
			k, v, now); err != nil {
			errOut(w, 500, "internal_error", "保存失败")
			return
		}
		n++
	}
	a.auditAppend("settings_save", 0, fmt.Sprintf("keys=%d", n), clientIP(r))
	jsonOut(w, 200, map[string]any{"ok": true, "saved": n, "message": "站点配置已保存，即时生效"})
}
