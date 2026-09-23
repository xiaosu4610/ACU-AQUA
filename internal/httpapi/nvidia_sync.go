package httpapi

// NVIDIA 动态目录同步（rc21）：
// 背景——Go 版切 DB 事实源（admin_lines）后 linesFromDB 不恢复 dynamic 字段、
// 且无任何同步任务，nvidia_models 表停留在 Rust 时代的旧数据，动态目录整体失效。
// 本文件补齐三件事：dynamic 字段持久化（db.go/adminlines.go 配套）+ 增量同步 + 后台定时。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"acu-aqua/gateway/internal/config"
)

// nvidiaSyncInterval 同步周期：每小时（上游 NIM 目录变动低频，足够灵敏）
const nvidiaSyncInterval = time.Hour

// catalogProbeMax 单轮目录探针上限（防目录异常膨胀时同步周期被拖长）
const catalogProbeMax = 160

// probeConcurrency 探针并发度（上游限流敏感，取保守值）
const probeConcurrency = 8

// probeModelStatus 对单个上游模型发一次最小探针（1 token），返回 HTTP 状态码；
// 传输层失败返回 0（不作结论）。语义由调用方解释：200=可用、404/410=上游确无此模型。
func (a *App) probeModelStatus(line *config.Line, upstreamID string) int {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"max_tokens":1}`, upstreamID)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(line.BaseURL, "/")+"/chat/completions", strings.NewReader(body))
	if err != nil {
		return 0
	}
	req.Header.Set("Content-Type", "application/json")
	if len(line.Keys) > 0 {
		req.Header.Set("Authorization", "Bearer "+line.Keys[0])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode
}

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
	// id 统一剥厂商前缀（裸名口径，与配置目录/免费分发 site_id 对齐）：
	// 原先直接插上游全名（如 z-ai/glm-5.3-flash），与存量裸名行构成双份，且 SplitModel
	// 会把全名误拆为线前缀——20260917 站长报告"可见不可调/目录重复"根因
	bareOf := func(id string) string {
		if i := strings.Index(id, "/"); i >= 0 {
			return id[i+1:]
		}
		return id
	}
	var toAdd []([2]string) // [裸名lower, 上游原始ID]
	for _, orig := range upstreamSet {
		bare := strings.ToLower(bareOf(orig))
		if bare == "" || configured[bare] {
			continue // 已在配置目录：不进动态表
		}
		if _, ok := existing[bare]; ok {
			continue // 表内已有（裸名口径）
		}
		toAdd = append(toAdd, [2]string{bare, orig})
	}
	var toRemove []string // lower(id)
	for low, up := range existing {
		key := strings.ToLower(up)
		if key == "" {
			// 存量裸名行 upstream_id 为空：裸名还在上游目录时补全 upstream_id（进 toAdd
			// 走 INSERT OR REPLACE），不删除——否则本轮"只删不补"（全名键查不到裸名），
			// 模型目录空窗一个同步周期（1 小时）
			found := ""
			for _, orig := range upstreamSet {
				if strings.ToLower(bareOf(orig)) == low {
					found = orig
					break
				}
			}
			if found != "" && !configured[low] {
				toAdd = append(toAdd, [2]string{low, found})
			} else {
				toRemove = append(toRemove, low) // 上游已无此模型 / 已进配置目录：照旧清理
			}
			continue
		}
		if strings.Contains(low, "/") {
			toRemove = append(toRemove, low) // 存量全名行：统一裸名口径清理
			continue
		}
		if _, ok := upstreamSet[key]; !ok {
			toRemove = append(toRemove, low) // 上游已无此模型：清理
		}
	}
	// —— 3.5 全目录探针：判活（自愈）/ 判死（隐藏幽灵模型）——
	// ⚠️ 20260922 事故：原自愈以"仍在上游 /models 目录里"为依据，但 NVIDIA 目录会长期列出
	// **实际推理已下架**的模型（实测 meta/llama2-70b、deepseek-ai/deepseek-coder-6.7b-instruct、
	// nvidia/llama-3.1-nemotron-70b-instruct 等 20+ 个：目录里有、/chat/completions 恒 404/410）。
	// 于是死循环：目录一直宣传 → 用户选中调用 410 → 标记 retired → 下一轮同步又被复活。
	// 生产实锤：每小时"复活 6~49 个"、近 2h 产生 992 次 410 model_retired。
	// 现改为每轮同步对目录发最小探针，把依据从"目录声称"换成"实测可用"：
	//   200     → 撤销 retired（覆盖瞬时抖动误标，原自愈的本意）
	//   404/410 → **直接判死**（hits 置 2，立即从 /v1/models 隐藏 + 调用端 410，不再打扰用户）
	//   其他     → 不作结论（429/5xx/网络抖动绝不误判为下线）
	{
		targets := map[string]bool{} // upstream_id 去重
		for _, orig := range upstreamSet {
			targets[orig] = true
		}
		for _, up := range existing {
			if up != "" {
				targets[up] = true
			}
		}
		probeList := make([]string, 0, len(targets))
		for up := range targets {
			probeList = append(probeList, up)
		}
		sort.Strings(probeList) // 稳定顺序，便于日志比对
		if len(probeList) > catalogProbeMax {
			probeList = probeList[:catalogProbeMax]
		}
		statuses := make([]int, len(probeList))
		var wg sync.WaitGroup
		sem := make(chan struct{}, probeConcurrency)
		for i, up := range probeList {
			wg.Add(1)
			go func(i int, up string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				statuses[i] = a.probeModelStatus(line, up)
			}(i, up)
		}
		wg.Wait() // 探针期间不持有 DB 连接（SQLite MaxOpenConns(1)）

		now := time.Now().Unix()
		var healed, killed int
		for i, up := range probeList {
			switch statuses[i] {
			case http.StatusOK:
				if res, err := a.DB.Exec("DELETE FROM retired_models WHERE model=?", up); err == nil {
					if n, _ := res.RowsAffected(); n > 0 {
						healed++
					}
				}
			case http.StatusNotFound, http.StatusGone:
				if _, err := a.DB.Exec(
					"INSERT INTO retired_models (model, retired_ts, hits) VALUES (?,?,2) "+
						"ON CONFLICT(model) DO UPDATE SET hits=MAX(hits,2), retired_ts=?",
					up, now, now); err == nil {
					killed++
				}
			}
		}
		if healed > 0 || killed > 0 {
			log.Printf("[nvidia] 目录探针（%d 个）：判活自愈 %d 个，判死隐藏 %d 个幽灵模型", len(probeList), healed, killed)
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
