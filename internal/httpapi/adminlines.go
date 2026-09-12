// 上游线路在线管理：DB 为唯一事实源（config toml 仅作首次引导种子），
// 管理后台增删改线路/密钥/模型后热重载即时生效，全程审计哈希链留痕。
// 密钥明文只进不出（添加后仅脱敏展示），高危操作二次密码确认。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"acu-aqua/gateway/internal/config"
)

// ———————— 存储层：DB ↔ config.Line ————————

// initLinesFromStore 启动时调用：admin_lines 为空且 toml 有线 → 一次性种子导入；
// 随后 DB 有数据则覆盖 cfg.Lines（DB 优先，DB 清空可回退到 toml）。
func initLinesFromStore(c *config.Cfg, d *sql.DB) error {
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM admin_lines").Scan(&n); err != nil {
		return err
	}
	if n == 0 && len(c.Lines) > 0 {
		if err := seedLinesToDB(c.Lines, d); err != nil {
			return fmt.Errorf("上游线路种子导入: %w", err)
		}
		log.Printf("[lines] 已从 config 种子导入 %d 条上游线路到 admin_lines", len(c.Lines))
	}
	ls, err := linesFromDB(d)
	if err != nil {
		return err
	}
	if len(ls) > 0 {
		c.Lines = ls
	}
	return nil
}

// seedLinesToDB toml 线路全量导入（幂等：仅空表时调用）
func seedLinesToDB(lines []config.Line, d *sql.DB) error {
	now := time.Now().Unix()
	for i := range lines {
		l := &lines[i]
		if _, err := d.Exec(
			`INSERT INTO admin_lines (id, name, mode, base_url, vip_num, vip_den, key_face_micro, enabled, updated_ts)
			 VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
			l.ID, l.Name, l.Mode, l.BaseURL, l.VipNum, l.VipDen, l.KeyFaceMicro, 1, now); err != nil {
			return err
		}
		for k := range l.Keys {
			if l.Keys[k] == "" {
				continue
			}
			if _, err := d.Exec(
				`INSERT INTO admin_line_keys (line_id, idx, key, dead, note, updated_ts)
				 VALUES (?,?,?,0,'',?) ON CONFLICT(line_id, idx) DO NOTHING`,
				l.ID, k, l.Keys[k], now); err != nil {
				return err
			}
		}
		for j := range l.Models {
			m := l.Models[j]
			if _, err := d.Exec(
				`INSERT INTO admin_line_models (line_id, site_id, upstream_id, image, per_call_sell, per_call_cost,
				 per_image_sell, per_image_cost, in_sell_rate10, cache_sell_rate10, out_sell_rate10,
				 in_cost_rate10, cache_cost_rate10, out_cost_rate10, updated_ts)
				 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(line_id, site_id) DO NOTHING`,
				l.ID, m.SiteID, m.UpstreamID, b2i(m.Image), m.PerCallSell, m.PerCallCost,
				m.PerImageSell, m.PerImageCost, m.InSellRate10, m.CacheSellRate10, m.OutSellRate10,
				m.InCostRate10, m.CacheCostRate10, m.OutCostRate10, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// linesFromDB 三表 → []config.Line（enabled 线；keys 按 idx 排序剔除 dead）
func linesFromDB(d *sql.DB) ([]config.Line, error) {
	rows, err := d.Query(
		`SELECT id, name, mode, base_url, vip_num, vip_den, key_face_micro, enabled
		 FROM admin_lines ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []config.Line{}
	for rows.Next() {
		var l config.Line
		var enabled int
		if err := rows.Scan(&l.ID, &l.Name, &l.Mode, &l.BaseURL, &l.VipNum, &l.VipDen, &l.KeyFaceMicro, &enabled); err != nil {
			return nil, err
		}
		if enabled == 0 {
			continue // 停用线不参与路由
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// keys（剔除 dead 钥；台账 used>=initial 的超卖钥也不进池——上游面值已耗尽，
	// 留在池里只会反复 402/502，直到管理台恢复 dead 标记或补台账）
	krows, err := d.Query(
		`SELECT k.line_id, k.idx, k.key FROM admin_line_keys k
		 LEFT JOIN line_keys f ON f.line_id=k.line_id AND f.idx=k.idx
		 WHERE k.dead=0 AND NOT (COALESCE(f.initial_micro,0)>0 AND COALESCE(f.used_micro,0)>=COALESCE(f.initial_micro,0))
		 ORDER BY k.line_id, k.idx`)
	if err != nil {
		return nil, err
	}
	defer krows.Close()
	for krows.Next() {
		var lineID, key string
		var idx int
		if err := krows.Scan(&lineID, &idx, &key); err != nil {
			return nil, err
		}
		for i := range out {
			if out[i].ID == lineID {
				out[i].Keys = append(out[i].Keys, key)
			}
		}
	}
	if err := krows.Err(); err != nil {
		return nil, err
	}
	// models
	mrows, err := d.Query(
		`SELECT line_id, site_id, upstream_id, image, per_call_sell, per_call_cost,
		        per_image_sell, per_image_cost, in_sell_rate10, cache_sell_rate10, out_sell_rate10,
		        in_cost_rate10, cache_cost_rate10, out_cost_rate10, COALESCE(degraded,0), COALESCE(key_idx,-1)
		 FROM admin_line_models ORDER BY line_id, site_id`)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var lineID, siteID, upID string
		var image, perCall, perCallCost, perImgSell, perImgCost, inR, cacheR, outR, inC, cacheC, outC, degraded, keyIdx int64
		if err := mrows.Scan(&lineID, &siteID, &upID, &image, &perCall, &perCallCost,
			&perImgSell, &perImgCost, &inR, &cacheR, &outR, &inC, &cacheC, &outC, &degraded, &keyIdx); err != nil {
			return nil, err
		}
		for i := range out {
			if out[i].ID == lineID {
				out[i].Models = append(out[i].Models, config.Model{
					SiteID: siteID, UpstreamID: upID, Image: image != 0,
					PerCallSell: perCall, PerCallCost: perCallCost,
					PerImageSell: perImgSell, PerImageCost: perImgCost,
					InSellRate10: inR, CacheSellRate10: cacheR, OutSellRate10: outR,
					InCostRate10: inC, CacheCostRate10: cacheC, OutCostRate10: outC,
					Degraded: degraded != 0, KeyIdx: keyIdx,
				})
			}
		}
	}
	if err := mrows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// reloadLines 热重载：DB → 替换 Lines + 清空客户端池（下次请求按新配置重建）。
// 锁序固定 clientsMu → linesMu（与 clientFor 一致），全程原子替换，读快照安全。
func (a *App) reloadLines() error {
	ls, err := linesFromDB(a.DB.DB)
	if err != nil {
		return err
	}
	a.clientsMu.Lock()
	a.linesMu.Lock()
	a.Cfg.Lines = ls
	a.clients = nil
	a.linesMu.Unlock()
	a.clientsMu.Unlock()
	return nil
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// ———————— 管理端点 ————————

// GET /v1/admin/lines → 线路列表（含密钥统计/模型数/近 1h 健康/面值台账）
func (a *App) handleAdminLines(w http.ResponseWriter, r *http.Request) { //nolint:revive
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	// 先整体读出线路（SQLite 单连接：rows 遍历中不得再发起查询）
	type lineRow struct {
		id, name, mode, baseURL string
		vipNum, vipDen, face    int64
		enabled                 int64
	}
	rows, err := a.DB.Query(`SELECT id, name, mode, base_url, vip_num, vip_den, key_face_micro, enabled FROM admin_lines ORDER BY rowid`)
	if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	ls := []lineRow{}
	for rows.Next() {
		var x lineRow
		if err := rows.Scan(&x.id, &x.name, &x.mode, &x.baseURL, &x.vipNum, &x.vipDen, &x.face, &x.enabled); err == nil {
			ls = append(ls, x)
		}
	}
	rows.Close()
	items := []map[string]any{}
	for _, x := range ls {
		var kTotal, kDead, mTotal int64
		_ = a.DB.QueryRow("SELECT COUNT(*), COALESCE(SUM(dead),0) FROM admin_line_keys WHERE line_id=?", x.id).Scan(&kTotal, &kDead)
		_ = a.DB.QueryRow("SELECT COUNT(*) FROM admin_line_models WHERE line_id=?", x.id).Scan(&mTotal)
		// 近 1h 健康统计（requests 落库口径）
		var calls, oks int64
		_ = a.DB.QueryRow(
			"SELECT COUNT(*), COALESCE(SUM(ok),0) FROM requests WHERE resolved_line=? AND ts>? AND endpoint IN ('chat','images')",
			x.id, time.Now().Unix()-3600).Scan(&calls, &oks)
		var used, initial int64
		_ = a.DB.QueryRow(
			"SELECT COALESCE(SUM(used_micro),0), COALESCE(SUM(initial_micro),0) FROM line_keys WHERE line_id=?", x.id).Scan(&used, &initial)
		items = append(items, map[string]any{
			"id": x.id, "name": x.name, "mode": x.mode, "base_url": x.baseURL,
			"vip_num": x.vipNum, "vip_den": x.vipDen, "key_face_micro": x.face,
			"enabled":    x.enabled != 0,
			"keys_total": kTotal, "keys_dead": kDead, "models_total": mTotal,
			"calls_1h": calls, "ok_1h": oks,
			"face_used_micro": used, "face_initial_micro": initial,
		})
	}
	jsonOut(w, 200, map[string]any{"lines": items})
}

// POST /v1/admin/lines {id,name,mode,base_url,vip_num,vip_den,key_face_micro,keys} 新增线路
func (a *App) handleAdminLineCreate(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var req struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Mode            string `json:"mode"`
		BaseURL         string `json:"base_url"`
		VipNum          int64  `json:"vip_num"`
		VipDen          int64  `json:"vip_den"`
		KeyFaceMicro    int64  `json:"key_face_micro"`
		Keys            string `json:"keys"` // 批量粘贴（逗号/换行分隔）
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 1<<20, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	req.ID = strings.ToLower(strings.TrimSpace(req.ID))
	req.Mode = strings.TrimSpace(req.Mode)
	ip := clientIP(r)
	if req.ID == "" || !lineIDOk(req.ID) {
		errAdmin(w, 400, "bad_request", "线 ID 仅允许小写字母/数字/连字符")
		return
	}
	if req.Mode != "free" && req.Mode != "per_call" && req.Mode != "per_token" {
		errAdmin(w, 400, "bad_request", "mode 仅允许 free/per_call/per_token")
		return
	}
	if req.Mode != "free" && !adminPasswordOk(req.ConfirmPassword) {
		a.auditAppend("line_add_fail", 0, req.ID, ip)
		errAdmin(w, 403, "invalid_credentials", "确认密码错误")
		return
	}
	var dup int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM admin_lines WHERE id=?", req.ID).Scan(&dup); err == nil && dup > 0 {
		errAdmin(w, 409, "conflict", "线 ID 已存在")
		return
	}
	now := time.Now().Unix()
	if _, err := a.DB.Exec(
		`INSERT INTO admin_lines (id, name, mode, base_url, vip_num, vip_den, key_face_micro, enabled, updated_ts)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		req.ID, req.Name, req.Mode, req.BaseURL, req.VipNum, req.VipDen, req.KeyFaceMicro, 1, now); err != nil {
		errAdmin(w, 500, "internal_error", "创建失败")
		return
	}
	added, _ := a.addLineKeys(req.ID, req.Keys, now)
	a.auditAppend("line_add", 0, fmt.Sprintf("线=%s 模式=%s 密钥=%d 把", req.ID, req.Mode, added), ip)
	_ = a.reloadLines()
	jsonOut(w, 200, map[string]any{"ok": true, "keys_added": added})
}

// POST /v1/admin/lines/{line} {name,base_url,vip_num,vip_den,key_face_micro,enabled} 修改线路
func (a *App) handleAdminLineUpdate(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	lineID := r.PathValue("line")
	var req struct {
		Name            string `json:"name"`
		BaseURL         string `json:"base_url"`
		VipNum          int64  `json:"vip_num"`
		VipDen          int64  `json:"vip_den"`
		KeyFaceMicro    int64  `json:"key_face_micro"`
		Enabled         *bool  `json:"enabled"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 8192, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	ip := clientIP(r)
	if !adminPasswordOk(req.ConfirmPassword) {
		a.auditAppend("line_update_fail", 0, lineID, ip)
		errAdmin(w, 403, "invalid_credentials", "确认密码错误")
		return
	}
	var name, mode, baseURL string
	if err := a.DB.QueryRow("SELECT name, mode, base_url FROM admin_lines WHERE id=?", lineID).Scan(&name, &mode, &baseURL); err == sql.ErrNoRows {
		errAdmin(w, 404, "not_found", "线路不存在")
		return
	} else if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	if req.Name != "" {
		name = req.Name
	}
	if req.BaseURL != "" {
		baseURL = req.BaseURL
	}
	enabled := 1
	if req.Enabled != nil {
		if *req.Enabled {
			enabled = 1
		} else {
			enabled = 0
		}
	} else {
		_ = a.DB.QueryRow("SELECT enabled FROM admin_lines WHERE id=?", lineID).Scan(&enabled)
	}
	if _, err := a.DB.Exec(
		`UPDATE admin_lines SET name=?, base_url=?, vip_num=?, vip_den=?, key_face_micro=?, enabled=?, updated_ts=? WHERE id=?`,
		name, baseURL, req.VipNum, req.VipDen, req.KeyFaceMicro, enabled, time.Now().Unix(), lineID); err != nil {
		errAdmin(w, 500, "internal_error", "更新失败")
		return
	}
	a.auditAppend("line_update", 0, fmt.Sprintf("线=%s enabled=%d base=%s", lineID, enabled, baseURL), ip)
	_ = a.reloadLines()
	jsonOut(w, 200, map[string]any{"ok": true})
}

// DELETE /v1/admin/lines/{line} 删除线路（须先停用；二次密码）
func (a *App) handleAdminLineDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	lineID := r.PathValue("line")
	var req struct {
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	ip := clientIP(r)
	if !adminPasswordOk(req.ConfirmPassword) {
		a.auditAppend("line_delete_fail", 0, lineID, ip)
		errAdmin(w, 403, "invalid_credentials", "确认密码错误")
		return
	}
	var enabled int
	if err := a.DB.QueryRow("SELECT enabled FROM admin_lines WHERE id=?", lineID).Scan(&enabled); err == sql.ErrNoRows {
		errAdmin(w, 404, "not_found", "线路不存在")
		return
	} else if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	if enabled != 0 {
		errAdmin(w, 400, "bad_request", "请先停用线路再删除")
		return
	}
	for _, q := range []string{
		"DELETE FROM admin_lines WHERE id=?", "DELETE FROM admin_line_keys WHERE line_id=?", "DELETE FROM admin_line_models WHERE line_id=?",
	} {
		if _, err := a.DB.Exec(q, lineID); err != nil {
			errAdmin(w, 500, "internal_error", "删除失败")
			return
		}
	}
	a.auditAppend("line_delete", 0, "线="+lineID, ip)
	_ = a.reloadLines()
	jsonOut(w, 200, map[string]any{"ok": true})
}

// GET /v1/admin/lines/{line}/keys → 密钥池（脱敏 + dead 标记）
func (a *App) handleAdminLineKeys(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	lineID := r.PathValue("line")
	rows, err := a.DB.Query(
		`SELECT k.idx, k.key, k.dead, k.note, COALESCE(f.initial_micro,0), COALESCE(f.used_micro,0)
		 FROM admin_line_keys k LEFT JOIN line_keys f ON f.line_id=k.line_id AND f.idx=k.idx
		 WHERE k.line_id=? ORDER BY k.idx`, lineID)
	if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idx, dead, initial, used int64
		var key, note string
		if rows.Scan(&idx, &key, &dead, &note, &initial, &used) == nil {
			items = append(items, map[string]any{
				"idx": idx, "masked": maskKey(key), "dead": dead != 0, "note": note,
				"face_initial_micro": initial, "face_used_micro": used,
			})
		}
	}
	jsonOut(w, 200, map[string]any{"line": lineID, "keys": items})
}

// POST /v1/admin/lines/{line}/keys {keys:"sk-a,sk-b\nsk-c"} 批量添加
func (a *App) handleAdminLineKeysAdd(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	lineID := r.PathValue("line")
	var req struct {
		Keys            string `json:"keys"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 1<<20, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	if strings.TrimSpace(req.Keys) == "" {
		errAdmin(w, 400, "bad_request", "密钥内容为空")
		return
	}
	var exist int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM admin_lines WHERE id=?", lineID).Scan(&exist); err == nil && exist == 0 {
		errAdmin(w, 404, "not_found", "线路不存在")
		return
	}
	if !adminPasswordOk(req.ConfirmPassword) {
		a.auditAppend("line_key_add_fail", 0, lineID, clientIP(r))
		errAdmin(w, 403, "invalid_credentials", "确认密码错误")
		return
	}
	added, dups := a.addLineKeys(lineID, req.Keys, time.Now().Unix())
	a.auditAppend("line_key_add", 0, fmt.Sprintf("线=%s 新增 %d 把（重复 %d）", lineID, added, dups), clientIP(r))
	_ = a.reloadLines()
	jsonOut(w, 200, map[string]any{"ok": true, "added": added, "duplicates": dups})
}

// addLineKeys 批量写入密钥（逗号/换行/空白分隔；已有同钥跳过）。返回 (新增, 重复)
func (a *App) addLineKeys(lineID, raw string, now int64) (int, int) {
	var maxIdx sql.NullInt64
	_ = a.DB.QueryRow("SELECT MAX(idx) FROM admin_line_keys WHERE line_id=?", lineID).Scan(&maxIdx)
	next := 0 // 空表从 0 起（与 toml 种子导入索引对齐）
	if maxIdx.Valid {
		next = int(maxIdx.Int64) + 1
	}
	added, dups := 0, 0
	seen := map[string]bool{}
	fields := strings.FieldsFunc(raw, func(rn rune) bool { return rn == ',' || rn == '\n' || rn == '\r' || rn == ' ' || rn == '\t' })
	for _, k := range fields {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		var dup int
		_ = a.DB.QueryRow("SELECT COUNT(*) FROM admin_line_keys WHERE line_id=? AND key=?", lineID, k).Scan(&dup)
		if dup > 0 {
			dups++
			continue
		}
		if _, err := a.DB.Exec(
			`INSERT INTO admin_line_keys (line_id, idx, key, dead, note, updated_ts) VALUES (?,?,?,0,'',?)`,
			lineID, next, k, now); err != nil {
			continue
		}
		next++
		added++
	}
	return added, dups
}

// DELETE /v1/admin/lines/{line}/keys/{idx}（二次密码）
func (a *App) handleAdminLineKeyDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	lineID := r.PathValue("line")
	idx, _ := strconv.ParseInt(r.PathValue("idx"), 10, 64)
	var req struct {
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	ip := clientIP(r)
	if !adminPasswordOk(req.ConfirmPassword) {
		a.auditAppend("line_key_delete_fail", 0, lineID, ip)
		errAdmin(w, 403, "invalid_credentials", "确认密码错误")
		return
	}
	if _, err := a.DB.Exec("DELETE FROM admin_line_keys WHERE line_id=? AND idx=?", lineID, idx); err != nil {
		errAdmin(w, 500, "internal_error", "删除失败")
		return
	}
	a.auditAppend("line_key_delete", 0, fmt.Sprintf("线=%s idx=%d", lineID, idx), ip)
	_ = a.reloadLines()
	jsonOut(w, 200, map[string]any{"ok": true})
}

// POST /v1/admin/lines/{line}/keys/{idx}/dead {dead:true|false} 停用/恢复
func (a *App) handleAdminLineKeyDead(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	lineID := r.PathValue("line")
	idx, _ := strconv.ParseInt(r.PathValue("idx"), 10, 64)
	var req struct {
		Dead            bool   `json:"dead"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	ip := clientIP(r)
	if !adminPasswordOk(req.ConfirmPassword) {
		a.auditAppend("line_key_dead_fail", 0, lineID, ip)
		errAdmin(w, 403, "invalid_credentials", "确认密码错误")
		return
	}
	if _, err := a.DB.Exec("UPDATE admin_line_keys SET dead=?, updated_ts=? WHERE line_id=? AND idx=?",
		b2i(req.Dead), time.Now().Unix(), lineID, idx); err != nil {
		errAdmin(w, 500, "internal_error", "更新失败")
		return
	}
	act := "line_key_disable"
	if !req.Dead {
		act = "line_key_enable"
	}
	a.auditAppend(act, 0, fmt.Sprintf("线=%s idx=%d", lineID, idx), ip)
	_ = a.reloadLines()
	jsonOut(w, 200, map[string]any{"ok": true})
}

// GET /v1/admin/lines/{line}/models → 模型映射清单
func (a *App) handleAdminLineModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	lineID := r.PathValue("line")
	rows, err := a.DB.Query(
		`SELECT site_id, upstream_id, image, per_call_sell, per_call_cost, per_image_sell, per_image_cost,
		        in_sell_rate10, cache_sell_rate10, out_sell_rate10, in_cost_rate10, cache_cost_rate10, out_cost_rate10, COALESCE(degraded,0), COALESCE(key_idx,-1)
		 FROM admin_line_models WHERE line_id=? ORDER BY site_id`, lineID)
	if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var siteID, upID string
		var image, perCall, perCallCost, perImgSell, perImgCost, inR, cacheR, outR, inC, cacheC, outC, degraded, keyIdx int64
		if rows.Scan(&siteID, &upID, &image, &perCall, &perCallCost, &perImgSell, &perImgCost,
			&inR, &cacheR, &outR, &inC, &cacheC, &outC, &degraded, &keyIdx) == nil {
			items = append(items, map[string]any{
				"site_id": siteID, "upstream_id": upID, "image": image != 0,
				"per_call_sell": perCall, "per_call_cost": perCallCost,
				"per_image_sell": perImgSell, "per_image_cost": perImgCost,
				"in_sell_rate10": inR, "cache_sell_rate10": cacheR, "out_sell_rate10": outR,
				"in_cost_rate10": inC, "cache_cost_rate10": cacheC, "out_cost_rate10": outC,
				"degraded": degraded != 0, "key_idx": keyIdx,
			})
		}
	}
	jsonOut(w, 200, map[string]any{"line": lineID, "models": items})
}

// POST /v1/admin/lines/{line}/models {site_id,upstream_id,image,per_call_sell,in/cache/out_sell_rate10}
// 新增收费线模型时按参数播种 pricing（normal+vip，幂等不覆盖已调价）
func (a *App) handleAdminLineModelUpsert(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	lineID := r.PathValue("line")
	var req struct {
		SiteID          string `json:"site_id"`
		UpstreamID      string `json:"upstream_id"`
		Image           bool   `json:"image"`
		PerCallSell     int64  `json:"per_call_sell"`
		PerCallCost     int64  `json:"per_call_cost"`
		PerImageSell    int64  `json:"per_image_sell"`
		PerImageCost    int64  `json:"per_image_cost"`
		InSellRate10    int64  `json:"in_sell_rate10"`
		CacheSellRate10 int64  `json:"cache_sell_rate10"`
		OutSellRate10   int64  `json:"out_sell_rate10"`
		InCostRate10    int64  `json:"in_cost_rate10"`
		CacheCostRate10 int64  `json:"cache_cost_rate10"`
		OutCostRate10   int64  `json:"out_cost_rate10"`
		// KeyIdx 模型专属钥池序：nil/-1=自动；≥0 锁定非 dead 钥排序后的第 N 把。指针防 JSON 零值误绑 0
		KeyIdx          *int64 `json:"key_idx"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 8192, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	req.SiteID = strings.TrimSpace(req.SiteID)
	ip := clientIP(r)
	if req.SiteID == "" || strings.ContainsAny(req.SiteID, " /") {
		errAdmin(w, 400, "bad_request", "site_id 不能为空且不含空格/斜杠")
		return
	}
	var mode string
	if err := a.DB.QueryRow("SELECT mode FROM admin_lines WHERE id=?", lineID).Scan(&mode); err == sql.ErrNoRows {
		errAdmin(w, 404, "not_found", "线路不存在")
		return
	} else if err != nil {
		errAdmin(w, 500, "internal_error", "查询失败")
		return
	}
	if mode != "free" && !adminPasswordOk(req.ConfirmPassword) {
		a.auditAppend("line_model_upsert_fail", 0, lineID, ip)
		errAdmin(w, 403, "invalid_credentials", "确认密码错误")
		return
	}
	if req.UpstreamID == "" {
		req.UpstreamID = req.SiteID
	}
	keyIdx := int64(-1) // 缺省自动（粘性池）；nil 时归 -1，显式传值才锁定专属钥
	if req.KeyIdx != nil {
		keyIdx = *req.KeyIdx
	}
	now := time.Now().Unix()
	if _, err := a.DB.Exec(
		`INSERT INTO admin_line_models (line_id, site_id, upstream_id, image, per_call_sell, per_call_cost,
		 per_image_sell, per_image_cost, in_sell_rate10, cache_sell_rate10, out_sell_rate10,
		 in_cost_rate10, cache_cost_rate10, out_cost_rate10, key_idx, updated_ts)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(line_id, site_id) DO UPDATE SET upstream_id=excluded.upstream_id, image=excluded.image,
		   per_call_sell=excluded.per_call_sell, per_call_cost=excluded.per_call_cost,
		   per_image_sell=excluded.per_image_sell, per_image_cost=excluded.per_image_cost,
		   in_sell_rate10=excluded.in_sell_rate10, cache_sell_rate10=excluded.cache_sell_rate10,
		   out_sell_rate10=excluded.out_sell_rate10, in_cost_rate10=excluded.in_cost_rate10,
		   cache_cost_rate10=excluded.cache_cost_rate10, out_cost_rate10=excluded.out_cost_rate10,
		   key_idx=excluded.key_idx, updated_ts=excluded.updated_ts`,
		lineID, req.SiteID, req.UpstreamID, b2i(req.Image), req.PerCallSell, req.PerCallCost,
		req.PerImageSell, req.PerImageCost, req.InSellRate10, req.CacheSellRate10, req.OutSellRate10,
		req.InCostRate10, req.CacheCostRate10, req.OutCostRate10, keyIdx, now); err != nil {
		errAdmin(w, 500, "internal_error", "保存失败")
		return
	}
	seeded := a.seedModelPricing(lineID, mode, req.SiteID, req.PerCallSell, req.InSellRate10, req.CacheSellRate10, req.OutSellRate10)
	a.auditAppend("line_model_upsert", 0, fmt.Sprintf("线=%s 模型=%s 播种价 %d 组", lineID, req.SiteID, seeded), ip)
	_ = a.reloadLines()
	jsonOut(w, 200, map[string]any{"ok": true, "pricing_seeded": seeded})
}

// seedModelPricing 新模型价格播种（幂等；vip = normal × vipnum/vipden）。返回播种组数。
func (a *App) seedModelPricing(lineID, mode, siteID string, perCallSell, inR, cacheR, outR int64) int {
	var vipNum, vipDen int64
	_ = a.DB.QueryRow("SELECT vip_num, vip_den FROM admin_lines WHERE id=?", lineID).Scan(&vipNum, &vipDen)
	full := lineID + "/" + siteID
	seeded := 0
	seed := func(grp, pm string, price, floor, sIn, sCache, sOut int64) {
		var n int
		if err := a.DB.QueryRow("SELECT COUNT(*) FROM pricing WHERE model=? AND grp=? AND starts_at=0", full, grp).Scan(&n); err != nil {
			log.Printf("[seed] 查价失败 model=%s grp=%s: %v", full, grp, err)
		}
		if n > 0 {
			return
		}
		if _, err := a.DB.Exec(
			`INSERT INTO pricing (model, price_micro, starts_at, ends_at, note, grp, mode, floor_micro, in_rate10, cache_rate10, out_rate10)
			 VALUES (?,?,0,NULL,'管理后台播种',?,?,?,?,?,?)`, full, price, grp, pm, floor, sIn, sCache, sOut); err != nil {
			log.Printf("[seed] 播种失败 model=%s grp=%s: %v", full, grp, err)
			return
		}
		seeded++
	}
	vipRate := func(p int64) int64 {
		if vipDen <= 0 {
			return p
		}
		return p * vipNum / vipDen
	}
	if mode == "per_token" {
		seed("normal", "per_token", 1000, 1000, inR, cacheR, outR)
		seed("vip", "per_token", 1000, 1000, vipRate(inR), vipRate(cacheR), vipRate(outR))
	} else {
		seed("normal", "per_call", perCallSell, 0, 0, 0, 0)
		seed("vip", "per_call", vipRate(perCallSell), 0, 0, 0, 0)
	}
	return seeded
}

// DELETE /v1/admin/lines/{line}/models/{site}（二次密码）
func (a *App) handleAdminLineModelDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	lineID := r.PathValue("line")
	siteID := r.PathValue("site")
	var req struct {
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 4096, &req); err != nil && err.Error() != "EOF" {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	ip := clientIP(r)
	if !adminPasswordOk(req.ConfirmPassword) {
		a.auditAppend("line_model_delete_fail", 0, lineID, ip)
		errAdmin(w, 403, "invalid_credentials", "确认密码错误")
		return
	}
	if _, err := a.DB.Exec("DELETE FROM admin_line_models WHERE line_id=? AND site_id=?", lineID, siteID); err != nil {
		errAdmin(w, 500, "internal_error", "删除失败")
		return
	}
	a.auditAppend("line_model_delete", 0, fmt.Sprintf("线=%s 模型=%s（pricing 历史价保留）", lineID, siteID), ip)
	_ = a.reloadLines()
	jsonOut(w, 200, map[string]any{"ok": true})
}

// POST /v1/admin/lines/{line}/models/{site}/degraded {degraded, confirm_password}（二次密码）
// 诊断 D4：上游故障时手动标记降级，/v1/models 透出提示用户换模型；标记不参与计费与路由
func (a *App) handleAdminLineModelDegraded(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	lineID := r.PathValue("line")
	siteID := r.PathValue("site")
	var req struct {
		Degraded        bool   `json:"degraded"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := adminBody(r, 4096, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	ip := clientIP(r)
	if !adminPasswordOk(req.ConfirmPassword) {
		a.auditAppend("line_model_degraded_fail", 0, lineID, ip)
		errAdmin(w, 403, "invalid_credentials", "确认密码错误")
		return
	}
	res, err := a.DB.Exec("UPDATE admin_line_models SET degraded=?, updated_ts=? WHERE line_id=? AND site_id=?",
		b2i(req.Degraded), time.Now().Unix(), lineID, siteID)
	if err != nil {
		errAdmin(w, 500, "internal_error", "更新失败")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		errAdmin(w, 404, "not_found", "模型不存在")
		return
	}
	state := "恢复"
	if req.Degraded {
		state = "标记降级"
	}
	a.auditAppend("line_model_degraded", 0, fmt.Sprintf("%s：线=%s 模型=%s", state, lineID, siteID), ip)
	_ = a.reloadLines()
	jsonOut(w, 200, map[string]any{"ok": true, "degraded": req.Degraded})
}

// POST /v1/admin/lines/reload 手动全量热重载
func (a *App) handleAdminLinesReload(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if err := a.reloadLines(); err != nil {
		errAdmin(w, 500, "internal_error", "热重载失败: "+err.Error())
		return
	}
	a.auditAppend("lines_reload", 0, "手动热重载", clientIP(r))
	ls := a.linesSnap()
	jsonOut(w, 200, map[string]any{"ok": true, "lines": len(ls)})
}

// ———————— 渠道测试与上游模型拉取（诊断 D5）———————

// lineTestHTTP 渠道测试专用 HTTP 客户端（独立于转发 KeyPool，轻量短超时）
var lineTestHTTP = &http.Client{Timeout: 15 * time.Second}

// testLineUpstream 单线测试：GET {base}/models（上游标准端点，零计费），
// 返回 状态码/延迟/上游模型数；key 取线内第一把活钥（免费线无钥不带鉴权头）。
func testLineUpstream(l *config.Line) map[string]any {
	start := time.Now()
	url := strings.TrimRight(l.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return map[string]any{"line": l.ID, "ok": false, "error": "url_invalid"}
	}
	if len(l.Keys) > 0 && l.Keys[0] != "" {
		switch l.AuthStyle {
		case "x-api-key":
			req.Header.Set("x-api-key", l.Keys[0])
		default:
			req.Header.Set("Authorization", "Bearer "+l.Keys[0])
		}
	}
	resp, err := lineTestHTTP.Do(req)
	if err != nil {
		return map[string]any{"line": l.ID, "name": l.Name, "ok": false,
			"latency_ms": time.Since(start).Milliseconds(), "error": "network_error"}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	lat := time.Since(start).Milliseconds()
	n := 0
	if resp.StatusCode == 200 {
		var jr struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &jr) == nil {
			n = len(jr.Data)
		}
	}
	return map[string]any{"line": l.ID, "name": l.Name, "ok": resp.StatusCode == 200,
		"status_code": resp.StatusCode, "latency_ms": lat, "upstream_models": n}
}

// POST /v1/admin/lines/{line}/test 单线测试
func (a *App) handleAdminLineTest(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	l := a.lineByID(r.PathValue("line"))
	if l == nil {
		errAdmin(w, 404, "not_found", "线路不存在")
		return
	}
	res := testLineUpstream(l)
	a.auditAppend("line_test", 0, fmt.Sprintf("线=%s 测试结果=%v", l.ID, res["ok"]), clientIP(r))
	jsonOut(w, 200, res)
}

// POST /v1/admin/lines/test-all 全量并发测试（诊断 D5：上游故障一屏定位）
func (a *App) handleAdminLinesTestAll(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	ls := a.linesSnap()
	out := make([]map[string]any, len(ls))
	var wg sync.WaitGroup
	for i := range ls {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i] = testLineUpstream(&ls[i])
		}(i)
	}
	wg.Wait()
	a.auditAppend("lines_test_all", 0, fmt.Sprintf("全量测试 %d 条线", len(ls)), clientIP(r))
	jsonOut(w, 200, map[string]any{"items": out})
}

// GET /v1/admin/lines/{line}/upstream-models 拉取上游模型列表（添加映射时直选上游 ID）
func (a *App) handleAdminLineUpstreamModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	l := a.lineByID(r.PathValue("line"))
	if l == nil {
		errAdmin(w, 404, "not_found", "线路不存在")
		return
	}
	url := strings.TrimRight(l.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		errAdmin(w, 500, "internal_error", "请求构造失败")
		return
	}
	if len(l.Keys) > 0 && l.Keys[0] != "" {
		switch l.AuthStyle {
		case "x-api-key":
			req.Header.Set("x-api-key", l.Keys[0])
		default:
			req.Header.Set("Authorization", "Bearer "+l.Keys[0])
		}
	}
	resp, err := lineTestHTTP.Do(req)
	if err != nil {
		errAdmin(w, 502, "upstream_error", "上游不可达: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		errAdmin(w, 502, "upstream_error", fmt.Sprintf("上游返回 %d", resp.StatusCode))
		return
	}
	var jr struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &jr); err != nil {
		errAdmin(w, 502, "upstream_error", "上游响应不是标准模型列表")
		return
	}
	ids := make([]string, 0, len(jr.Data))
	for _, m := range jr.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	jsonOut(w, 200, map[string]any{"line": l.ID, "models": ids})
}

// lineIDOk 线 ID 合法性：小写字母/数字/连字符，1-32 位
func lineIDOk(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// maskKey 密钥脱敏：前 6 + … + 后 4
func maskKey(k string) string {
	if len(k) <= 10 {
		return strings.Repeat("*", len(k))
	}
	return k[:6] + "…" + k[len(k)-4:]
}
