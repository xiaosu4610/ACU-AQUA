package httpapi

// NVIDIA 动态目录同步（rc21）：
// 背景——Go 版切 DB 事实源（admin_lines）后 linesFromDB 不恢复 dynamic 字段、
// 且无任何同步任务，nvidia_models 表停留在 Rust 时代的旧数据，动态目录整体失效。
// 本文件补齐三件事：dynamic 字段持久化（db.go/adminlines.go 配套）+ 增量同步 + 后台定时。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// nvidiaSyncInterval 同步周期：每小时（上游 NIM 目录变动低频，足够灵敏）
const nvidiaSyncInterval = time.Hour

// startNvidiaSyncer 后台同步循环：启动 30s 后首跑，此后每 nvidiaSyncInterval 一次。
// 无 dynamic 免费线时静默不启动（未配置该功能的环境零开销）。
func (a *App) startNvidiaSyncer() {
	if a.dynamicLine() == nil {
		log.Printf("[nvidia] 未发现 dynamic 免费线，动态目录同步器未启动")
		return
	}
	go func() {
		time.Sleep(30 * time.Second)
		for {
			added, removed, err := a.syncNvidiaModels()
			if err != nil {
				log.Printf("[nvidia] 动态目录同步失败: %v", err)
			} else if added > 0 || removed > 0 {
				log.Printf("[nvidia] 动态目录同步完成: 新增 %d 个模型，下线清理 %d 个", added, removed)
			}
			time.Sleep(nvidiaSyncInterval)
		}
	}()
	log.Printf("[nvidia] 动态目录同步器已启动（每 %s 一次）", nvidiaSyncInterval)
}

// syncNvidiaModels 单次增量同步：
//  1. GET {base}/models（bearer 第一把钥；目录请求免费不烧面值，无钥则裸请求）
//  2. 差集入库：上游有 − 配置目录有 − 表内已有 → 新增；表内有 − 上游已无 → 清理
//
// 口径与 dynamicModels 展示层一致：configured 按 SiteID 小写匹配。
// SQLite 单连接纪律：先收集全部查询结果，再统一写入。
func (a *App) syncNvidiaModels() (added, removed int, err error) {
	line := a.dynamicLine()
	if line == nil {
		return 0, 0, errors.New("无 dynamic 免费线")
	}

	// —— 1. 拉上游目录 ——
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(line.BaseURL, "/")+"/models", nil)
	if err != nil {
		return 0, 0, err
	}
	if len(line.Keys) > 0 {
		req.Header.Set("Authorization", "Bearer "+line.Keys[0])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, 0, errors.New("上游目录请求失败 status=" + resp.Status + " body=" + string(body))
	}
	var listing struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&listing); err != nil {
		return 0, 0, err
	}
	upstreamSet := map[string]string{} // lower(id) → 原始 id
	for _, m := range listing.Data {
		if m.ID != "" {
			upstreamSet[strings.ToLower(m.ID)] = m.ID
		}
	}
	if len(upstreamSet) == 0 {
		return 0, 0, errors.New("上游目录为空，放弃同步（防误清）")
	}

	// —— 2. 收集本地状态（先读后写，单连接安全）——
	configured := map[string]bool{}
	{
		rows, err := a.DB.Query("SELECT site_id FROM admin_line_models WHERE line_id=?", line.ID)
		if err != nil {
			return 0, 0, err
		}
		for rows.Next() {
			var s string
			if rows.Scan(&s) == nil {
				configured[strings.ToLower(s)] = true
			}
		}
		rows.Close()
	}
	existing := map[string]string{} // lower(id) → upstream_id
	{
		rows, err := a.DB.Query("SELECT id, upstream_id FROM nvidia_models")
		if err != nil {
			return 0, 0, err
		}
		for rows.Next() {
			var id, up string
			if rows.Scan(&id, &up) == nil {
				existing[strings.ToLower(id)] = up
			}
		}
		rows.Close()
	}

	// —— 3. 差集计算 ——
	var toAdd []([2]string) // [lowerID, 原始ID]
	for low, orig := range upstreamSet {
		if configured[low] {
			continue // 已在配置目录：不进动态表
		}
		if _, ok := existing[low]; ok {
			continue // 表内已有
		}
		toAdd = append(toAdd, [2]string{low, orig})
	}
	var toRemove []string // lower(id)
	for low, up := range existing {
		key := strings.ToLower(up)
		if key == "" {
			key = low
		}
		if _, ok := upstreamSet[key]; !ok {
			toRemove = append(toRemove, low) // 上游已无此模型：清理
		}
	}
	if len(toAdd) == 0 && len(toRemove) == 0 {
		return 0, 0, nil
	}

	// —— 4. 事务写入 ——
	tx, err := a.DB.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	for _, pair := range toAdd {
		if _, err := tx.Exec("INSERT OR REPLACE INTO nvidia_models (id, upstream_id, ts) VALUES (?,?,?)",
			pair[0], pair[1], time.Now().Unix()); err != nil {
			return 0, 0, err
		}
	}
	for _, low := range toRemove {
		if _, err := tx.Exec("DELETE FROM nvidia_models WHERE lower(id)=?", low); err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	if len(toAdd) > 0 {
		names := make([]string, 0, len(toAdd))
		for _, p := range toAdd {
			names = append(names, p[1])
		}
		log.Printf("[nvidia] 新增动态模型: %s", strings.Join(names, ", "))
	}
	return len(toAdd), len(toRemove), nil
}

// handleAdminNvidiaSync 手动触发同步（管理员）：POST /v1/admin/nvidia/sync
func (a *App) handleAdminNvidiaSync(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	added, removed, err := a.syncNvidiaModels()
	if err != nil {
		errAdmin(w, 502, "upstream_error", "同步失败: "+err.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "added": added, "removed": removed})
}
