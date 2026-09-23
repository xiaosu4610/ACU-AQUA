// 免费线（mode="free"）：不计费不预扣，仅转发与用量记账。
// 健康评分 / 下线登记 / NVIDIA 动态目录 / auto 路由候选——口径与 Rust 版逐函数对齐。
package httpapi

import (
	"bytes"
	"context"
	"database/sql"
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

	"acu-aqua/gateway/internal/billing"
	"acu-aqua/gateway/internal/config"
)

// 健康记录滚动保留窗口与 auto 候选统计窗口（与 Rust 版一致）
const (
	healthRetentionSecs = 8 * 3600
	autoWindowSecs      = 6 * 3600
	retiredTTLSecs      = 12 * 3600
	autoCacheSecs       = 60
)

// retiredUpstreams 上游永久下线集合（按上游真实 id；hits>=2 两次确认制）。
// 顺带清理过期行：7 天自愈，防上游重新上架被永久误隐。
func (a *App) retiredUpstreams() map[string]bool {
	now := time.Now().Unix()
	_, _ = a.DB.Exec("DELETE FROM retired_models WHERE retired_ts < ?", now-retiredTTLSecs)
	out := map[string]bool{}
	rows, err := a.DB.Query("SELECT model FROM retired_models WHERE hits >= 2")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		if rows.Scan(&m) == nil {
			out[m] = true
		}
	}
	return out
}

// markRetired 上游 404/410 时登记（每次 +1 并刷新时间戳，累计 ≥2 次才真正隐藏）
func (a *App) markRetired(upstreamID string) {
	if upstreamID == "" {
		return
	}
	_, _ = a.DB.Exec(
		"INSERT INTO retired_models (model, retired_ts, hits) VALUES (?,?,1) "+
			"ON CONFLICT(model) DO UPDATE SET hits = hits + 1, retired_ts = ?",
		upstreamID, time.Now().Unix(), time.Now().Unix())
}

// recordHealth 免费模型调用健康记录（滚动清理，随身删除过期行）
func (a *App) recordHealth(model string, ok bool, errType string, code int, latencyMs int64) {
	now := time.Now().Unix()
	okI := 0
	if ok {
		okI = 1
	}
	_, _ = a.DB.Exec(
		"INSERT INTO model_health (model, ts, ok, err_type, status_code, latency_ms) VALUES (?,?,?,?,?,?)",
		model, now, okI, errType, code, latencyMs)
	_, _ = a.DB.Exec("DELETE FROM model_health WHERE ts < ?", now-healthRetentionSecs)
}

// computeHealth 批量健康评分：每模型最近 100 次调用（保留窗口内），
// score = 成功率×60 + 延迟分 + 稳定分（口径与 Rust compute_all_health 一致）
func (a *App) computeHealth() map[string]map[string]any {
	out := map[string]map[string]any{}
	rows, err := a.DB.Query(
		`SELECT model, COUNT(*), SUM(ok),
		        AVG(CASE WHEN latency_ms > 0 THEN latency_ms END),
		        SUM(CASE WHEN err_type='rate_limited' THEN 1 ELSE 0 END),
		        SUM(CASE WHEN err_type='network_error' THEN 1 ELSE 0 END),
		        SUM(CASE WHEN err_type='timeout' THEN 1 ELSE 0 END)
		 FROM (SELECT model, ok, latency_ms, err_type,
		              ROW_NUMBER() OVER (PARTITION BY model ORDER BY ts DESC) AS rn
		       FROM model_health WHERE ts >= ?)
		 WHERE rn <= 100 GROUP BY model`,
		time.Now().Unix()-healthRetentionSecs)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		var total, okCnt, rateCnt, netCnt, toCnt int64
		var avgLat sql.NullFloat64
		if err := rows.Scan(&m, &total, &okCnt, &avgLat, &rateCnt, &netCnt, &toCnt); err != nil {
			continue
		}
		lat := avgLat.Float64
		latencyScore := 4.0
		switch {
		case lat <= 0 || lat <= 3000:
			latencyScore = 30.0
		case lat <= 8000:
			latencyScore = 22.0
		case lat <= 15000:
			latencyScore = 12.0
		}
		t := float64(total)
		bad := float64(rateCnt + netCnt + toCnt)
		stability := 0.0
		success := 0.0
		if t > 0 {
			stability = 10.0 * (1.0 - minF(bad/t, 1.0))
			success = float64(okCnt) / t
		}
		score := success*60.0 + latencyScore + stability
		if score < 0 {
			score = 0
		}
		if score > 100 {
			score = 100
		}
		out[m] = map[string]any{
			"total": total, "ok": okCnt,
			"avg_latency_ms": int64(lat + 0.5), "score": int64(score + 0.5),
		}
	}
	return out
}

// dynamicModels 动态目录（nvidia_models 表）：返回未与配置目录重复且上游未下线的条目
func (a *App) dynamicModels(lineID string, configured map[string]bool, retired map[string]bool) []map[string]any {
	rows, err := a.DB.Query("SELECT id, upstream_id FROM nvidia_models")
	if err != nil {
		return nil
	}
	defer rows.Close()
	created := time.Now().Unix()
	var out []map[string]any
	for rows.Next() {
		var id, up string
		if rows.Scan(&id, &up) != nil {
			continue
		}
		if configured[strings.ToLower(id)] {
			continue // 已在配置目录：不重复
		}
		if up != "" && retired[up] {
			continue // 上游永久下线：隐藏
		}
		out = append(out, map[string]any{
			"id": id, "object": "model", "created": created,
			"owned_by": lineID, "dynamic": true,
		})
	}
	return out
}

// dynamicUpstreamID 动态目录反查上游真实 ID。
// 全名调用兼容：客户端可能用上游原始全名（如 z-ai/glm-5.3-flash，经 SplitModel 拆线失败后
// 原样进入免费分发）——先按原样查，不中再剥厂商前缀按裸名查（动态表统一裸名口径，20260917）。
func (a *App) dynamicUpstreamID(model string) (string, bool) {
	var up string
	err := a.DB.QueryRow("SELECT upstream_id FROM nvidia_models WHERE id = ?", model).Scan(&up)
	if err != nil || up == "" {
		if i := strings.Index(model, "/"); i >= 0 {
			bare := model[i+1:]
			err = a.DB.QueryRow("SELECT upstream_id FROM nvidia_models WHERE id = ?", bare).Scan(&up)
			if err != nil || up == "" {
				return "", false
			}
			return up, true
		}
		return "", false
	}
	return up, true
}

// 特殊端点模型（embed/rerank/tts/翻译等）不进 auto 候选池（口径同 Rust NVIDIA_DYNAMIC_SKIP）
var autoSkipKeywords = []string{
	"embed", "rerank", "tts", "asr", "speech", "translate", "ocr", "parse",
	"safety", "guard", "moderation", "clip", "yolo", "detr", "deplot",
	"super-resolution", "relighting", "animate", "cosmos", "ising", "image",
	"video", "audio", "diarizer", "vad", "riva", "reward", "cogview", "prover",
}

func isSpecialEndpointModel(id string) bool {
	l := strings.ToLower(id)
	for _, k := range autoSkipKeywords {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// auto 候选缓存（60s）
var (
	autoMu    sync.Mutex
	autoCache []string
	autoTS    int64
)

type autoCand struct {
	m    string
	succ float64
	lat  float64
}

// autoCandidates auto 路由候选：近 6h 健康数据，calls>=3 且成功率>=0.9，
// 按成功率降序、延迟升序；无数据时回退兜底模型。
func (a *App) autoCandidates() []string {
	autoMu.Lock()
	if autoCache != nil && time.Now().Unix()-autoTS < autoCacheSecs {
		out := autoCache
		autoMu.Unlock()
		return out
	}
	autoMu.Unlock()

	var cands []autoCand
	rows, err := a.DB.Query(
		`SELECT model, COUNT(*) AS calls,
		        SUM(CASE WHEN ok = 1 THEN 1 ELSE 0 END) * 1.0 / COUNT(*) AS succ,
		        AVG(CASE WHEN latency_ms > 0 THEN latency_ms END) AS lat
		 FROM model_health WHERE ts >= ? GROUP BY model`,
		time.Now().Unix()-autoWindowSecs)
	if err == nil {
		for rows.Next() {
			var m string
			var calls int64
			var succ float64
			var lat sql.NullFloat64
			if rows.Scan(&m, &calls, &succ, &lat) != nil {
				continue
			}
			if calls < 3 || succ < 0.9 {
				continue
			}
			m = normalizeFreeID(m)
			if isSpecialEndpointModel(m) {
				continue
			}
			l := lat.Float64
			if l <= 0 {
				l = 99999
			}
			if i := findCandIdx(cands, m); i >= 0 {
				// 同模型多记录合并：成功率取最优、延迟取最快
				if succ > cands[i].succ {
					cands[i].succ = succ
				}
				if l < cands[i].lat {
					cands[i].lat = l
				}
				continue
			}
			cands = append(cands, autoCand{m, succ, l})
		}
		rows.Close()
	}
	// 主窗口（6h 高标准）无候选时降级：近 30 分钟 succ>=0.5 的模型（网络局部故障自愈，
	// 例如上游某条线整体被墙、仅个别线存活时的场景）
	if len(cands) == 0 {
		rows2, err2 := a.DB.Query(
			`SELECT model, COUNT(*) AS calls,
			        SUM(CASE WHEN ok = 1 THEN 1 ELSE 0 END) * 1.0 / COUNT(*) AS succ,
			        AVG(CASE WHEN latency_ms > 0 THEN latency_ms END) AS lat
			 FROM model_health WHERE ts >= ? GROUP BY model HAVING calls >= 3 AND succ >= 0.5`,
			time.Now().Unix()-1800)
		if err2 == nil {
			for rows2.Next() {
				var m string
				var calls int64
				var succ float64
				var lat sql.NullFloat64
				if rows2.Scan(&m, &calls, &succ, &lat) != nil {
					continue
				}
				m = normalizeFreeID(m)
				if isSpecialEndpointModel(m) {
					continue
				}
				l := lat.Float64
				if l <= 0 {
					l = 99999
				}
				if i := findCandIdx(cands, m); i < 0 {
					cands = append(cands, autoCand{m, succ, l})
				}
			}
			rows2.Close()
		}
	}
	// 上游永久下线的模型不进候选池——仅拦动态目录模型；
	// 固定目录线（acu 商汤自营）模型绝不联动 retired（20260919 线路独立规矩）
	retired := a.retiredUpstreams()
	n := 0
	for _, c := range cands {
		if dynUp, isDyn := a.dynamicUpstreamID(c.m); isDyn && retired[dynUp] {
			continue
		}
		cands[n] = c
		n++
	}
	cands = cands[:n]
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].succ != cands[j].succ {
			return cands[i].succ > cands[j].succ
		}
		return cands[i].lat < cands[j].lat
	})
	out := make([]string, 0, len(cands)+len(a.linesSnap()))
	for _, c := range cands {
		out = append(out, c.m)
	}
	// 兜底：每条 free 线一个代表模型（健康候选为空/不足时仍可路由）
	for _, fb := range a.autoFallbacks() {
		if !containsStr(out, fb) {
			out = append(out, fb)
		}
	}
	autoMu.Lock()
	autoCache, autoTS = out, time.Now().Unix()
	autoMu.Unlock()
	return out
}

// autoFallbacks 兜底候选：每条 free 线取第一个非特殊端点对话模型（线整体不可用时其余线仍可兜底）
func (a *App) autoFallbacks() []string {
	seen := map[string]bool{}
	var out []string
	ls := a.linesSnap()
	for i := range ls {
		l := &ls[i]
		if l.Mode != "free" {
			continue
		}
		for j := range l.Models {
			if isSpecialEndpointModel(l.Models[j].SiteID) {
				continue
			}
			id := l.Models[j].SiteID
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
			break
		}
	}
	return out
}

// normalizeFreeID 旧 ID 归一化（去首个「厂商/」前缀段 + 全小写）
func normalizeFreeID(m string) string {
	if i := strings.IndexByte(m, '/'); i >= 0 {
		return strings.ToLower(m[i+1:])
	}
	return strings.ToLower(m)
}

func findCandIdx(c []autoCand, m string) int {
	for i := range c {
		if c[i].m == m {
			return i
		}
	}
	return -1
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// logFreeErr 免费线转发失败日志
func logFreeErr(lineID, model string, err error) {
	log.Printf("[free] line=%s model=%s 转发失败: %v", lineID, model, err)
}

// dynamicLine 配置了 dynamic 的免费线（NVIDIA 动态目录承载线）
func (a *App) dynamicLine() *config.Line {
	ls := a.linesSnap()
	for i := range ls {
		if ls[i].Mode == "free" && ls[i].Dynamic {
			return &ls[i]
		}
	}
	return nil
}

// freeResolve 站点模型 → (线, 上游真实 ID)：配置目录优先，动态目录次之
func (a *App) freeResolve(siteID string) (*config.Line, string, bool) {
	if l, m := config.FindFreeModel(a.linesSnap(), siteID); l != nil {
		return l, m.UpstreamID, true
	}
	if up, ok := a.dynamicUpstreamID(siteID); ok {
		if l := a.dynamicLine(); l != nil {
			return l, up, true
		}
	}
	return nil, "", false
}

// handleFreeChat 免费模型转发主入口：
// auto 智能路由（候选前 3 依次尝试）→ 线客户端转发 → usage 记账 + 健康记录（不计费）
func (a *App) handleFreeChat(w http.ResponseWriter, r *http.Request, body []byte, req *chatReq, uid int64, keyHash string) {
	model := config.NormalizeModel(req.Model)
	start := time.Now()
	retired := a.retiredUpstreams()

	// 指定模型：目录/动态表反查（auto 由下方候选路由；先解析线供限速闸判定）
	var line *config.Line
	var upID string
	if model != "auto" {
		var ok bool
		line, upID, ok = a.freeResolve(model)
		if !ok {
			errOut(w, 404, "model_not_found",
				"模型不存在：目录与动态模型表均未收录，请 GET /v1/models 查看可用模型列表")
			return
		}
	}
	// 官方自营免费体验线（prefixed）限速：宽松闸门（防滥用）+ 低优先级排队（真实降级）。
	// 20260919 站长指令：闸门放宽（10→30 RPM、1→2 并发），"慢/卡"改由**真实降级**体现——
	// 免费请求转发前做一次低优先级等待（并发越高等待越久），高峰时自然排在付费请求之后；
	// 这是真实排队而非人为注入故障，不污染错误统计与模型健康度。
	if model == "auto" || line.Prefixed {
		if !guard.freeAllow(uid, clientIP(r)) {
			a.failRequest0(uid, keyHash, model, req.Stream, "free_rate_limited", 429)
			errOut(w, 429, "free_rate_limited",
				"免费通道当前请求过于密集（每用户每分钟上限 30 次）：请稍后重试；aqua/ 按量专线不限次、官方原版直连")
			return
		}
		rel, ok := guard.freeEnter(uid, clientIP(r))
		if !ok {
			a.failRequest0(uid, keyHash, model, req.Stream, "free_busy", 429)
			errOut(w, 429, "free_busy",
				"免费通道并发已满（每用户同时 2 路）：请等待当前请求完成；aqua/ 按量专线支持更高并发")
			return
		}
		defer rel() // 覆盖流式转发全程，函数返回即释放
		// 低优先级排队：让免费请求在拥挤时自然排在付费请求之后（真实降级，非人为故障）
		if d := guard.freePriorityDelay(uid, clientIP(r)); d > 0 {
			select {
			case <-time.After(d):
			case <-r.Context().Done():
				a.failRequest0(uid, keyHash, model, req.Stream, "client_cancel", 499)
				return
			}
		}
	}
	// 指定模型：retired 直接 410（不打上游烧密钥）
	if model != "auto" {
		// retired 仅作用于动态目录线（英伟达）；固定目录线（acu 商汤自营等）绝不联动——
		// 20260919 站长规矩：各线路完全独立，商汤 404 抖动不得隐藏 acu 自营目录模型
		if line.Dynamic && retired[upID] {
			a.failRequest0(uid, keyHash, model, req.Stream, "model_retired", 410) // 410 拒绝落 requests（此前盲区）
			errOut(w, 410, "model_retired",
				"模型 "+model+" 暂时不可用（上游异常或已下线），通常数小时内自动恢复；GET /v1/models 可查其他可用模型")
			return
		}
		resp, cancel, et := a.freeUpstreamChat(r, body, req, line, upID)
		if resp == nil {
			a.recordHealth(model, false, et, 0, time.Since(start).Milliseconds())
			// 客户端主动断开：499 client_cancel，不计故障（20260919 统计真实性）
			if et == "client_cancel" {
				a.failRequest0(uid, keyHash, model, req.Stream, "client_cancel", 499)
				return
			}
			// 注：timeout（上游过载/黑洞）不再计 retired——20260918 英伟达过载风暴
			// 期间 22 个健康模型因 45s 超时×2 被整批误隐；retired 仅收 404/410 明确下线，
			// 过载场景由 nvidia 同步自愈 + 短冷却调度兜底
			if et == "rate_limited" {
				// 全池限流：业务态繁忙（429），不当 502 故障处理
				a.failRequest0(uid, keyHash, model, req.Stream, "upstream_rate_limited", 429)
				errOut(w, 429, "line_busy", "官方自营线当前访问过于火爆，请稍后重试或改用 aqua/ 收费模型（更稳定）")
				return
			}
			a.failRequest0(uid, keyHash, model, req.Stream, "upstream_error", 502)
			errOut(w, 502, "upstream_error",
				"模型 "+model+" 这会儿没有响应（上游拥堵或维护中），已为您记录；请稍后重试或换个模型——GET /v1/models 可查可用列表")
			return
		}
		if resp.StatusCode == 404 || resp.StatusCode == 410 {
			_, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			resp.Body.Close()
			cancel()
			if line.Dynamic { // 仅动态目录登记 retired；固定目录线不登记（线路独立，20260919）
				a.markRetired(upID)
			}
			a.failRequest0(uid, keyHash, model, req.Stream, "model_retired", 410) // 410 拒绝落 requests（此前盲区）
			errOut(w, 410, "model_retired",
				"模型 "+model+" 暂时不可用（上游异常或已下线），通常数小时内自动恢复；GET /v1/models 可查其他可用模型")
			return
		}
		// 动态线 5xx 且为推理引擎崩溃（CUDA/TensorRT）：登记 retired 摘除（同钥/换钥重试均无意义，
		// 上游同步自愈 1h 复活；固定目录线不登记，20260919）
		if line.Dynamic && resp.StatusCode >= 500 && crashBodyPeek(resp.Body) {
			resp.Body.Close()
			cancel()
			a.markRetired(upID)
			a.recordHealth(model, false, "upstream_error", resp.StatusCode, time.Since(start).Milliseconds())
			a.failRequest0(uid, keyHash, model, req.Stream, "upstream_error", 502)
			errOut(w, 502, "upstream_error",
				"模型 "+model+" 上游推理引擎故障（已自动摘除，通常数小时内自动恢复），请稍后重试或换个模型")
			return
		}
		rid := a.insertRequest(uid, keyHash, "chat", model, req.Stream)
		w.Header().Set("X-AQUA-Model", model)
		w.Header().Set("X-AQUA-Line", line.ID)
		if req.Stream {
			a.serveFreeStreamChat(w, r, resp, cancel, rid, model, start, func() (*http.Response, context.CancelFunc, bool) {
				r2, c2, _ := a.freeUpstreamChat(r, body, req, line, upID)
				if r2 == nil || r2.StatusCode >= 400 {
					if r2 != nil {
						_, _ = io.Copy(io.Discard, io.LimitReader(r2.Body, 8<<10))
						r2.Body.Close()
						c2()
					}
					return nil, nil, false
				}
				return r2, c2, true
			})
		} else {
			a.serveFreeJSONChat(w, resp, rid, model, start)
		}
		return
	}

	// auto：健康候选前 6 依次尝试（前 3 全败时仍有回退余地），成功即用
	cands := a.autoCandidates()
	if len(cands) == 0 {
		errOut(w, 503, "auto_no_candidate", "当前没有可用的路由候选，请稍后重试或直接指定模型")
		return
	}
	if len(cands) > 6 {
		cands = cands[:6]
	}
	var (
		chosenModel string
		chosenLine  *config.Line
		chosenUpID  string
		resp        *http.Response
		cancel      context.CancelFunc
	)
	for _, m := range cands {
		line, upID, ok := a.freeResolve(m)
		if !ok {
			continue
		}
		// retired 仅拦动态目录候选；固定目录线（acu 商汤自营）绝不联动（20260919 线路独立规矩）
		if line.Dynamic && retired[upID] {
			continue
		}
		resp2, cancel2, et := a.freeUpstreamChat(r, body, req, line, upID)
		if resp2 == nil {
			a.recordHealth(m, false, et, 0, time.Since(start).Milliseconds())
			// timeout 不计 retired（过载风暴会整批误隐健康模型，20260918 实测）
			continue
		}
		if resp2.StatusCode == 404 || resp2.StatusCode == 410 {
			_, _ = io.ReadAll(io.LimitReader(resp2.Body, 8<<10))
			resp2.Body.Close()
			cancel2()
			if line.Dynamic { // 仅动态目录登记 retired；固定目录线不登记（线路独立，20260919）
				a.markRetired(upID)
			}
			continue
		}
		// 动态线 5xx 推理引擎崩溃：换候选（登记 retired 摘除，同步自愈复活）
		if line.Dynamic && resp2.StatusCode >= 500 && crashBodyPeek(resp2.Body) {
			resp2.Body.Close()
			cancel2()
			a.markRetired(upID)
			a.recordHealth(m, false, "upstream_error", resp2.StatusCode, time.Since(start).Milliseconds())
			continue
		}
		chosenModel, chosenLine, chosenUpID, resp, cancel = m, line, upID, resp2, cancel2
		if resp2.StatusCode < 400 {
			break
		}
		// ≥400：记录后换下一候选
		a.recordHealth(m, false, healthErrType(resp2.StatusCode), resp2.StatusCode, time.Since(start).Milliseconds())
		resp2.Body.Close()
		cancel2()
		resp = nil
	}
	if resp == nil {
		// 候选全败：业务态繁忙（503），不当 502 故障处理（20260918 紧急修复：auto 132×502/时）
		a.failRequest0(uid, keyHash, req.Model, req.Stream, "auto_exhausted", 503)
		errOut(w, 503, "auto_exhausted",
			"智能路由的候选模型这会儿全部繁忙（上游波动），请稍后重试，或直接指定一个模型——GET /v1/models 可查可用列表")
		return
	}
	defer cancel()
	rid := a.insertRequest(uid, keyHash, "chat", chosenModel, req.Stream)
	w.Header().Set("X-AQUA-Model", chosenModel)
	w.Header().Set("X-AQUA-Line", chosenLine.ID)
	if req.Stream {
		a.serveFreeStreamChat(w, r, resp, cancel, rid, chosenModel, start, func() (*http.Response, context.CancelFunc, bool) {
			r2, c2, _ := a.freeUpstreamChat(r, body, req, chosenLine, chosenUpID)
			if r2 == nil || r2.StatusCode >= 400 {
				if r2 != nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(r2.Body, 8<<10))
					r2.Body.Close()
					c2()
				}
				return nil, nil, false
			}
			return r2, c2, true
		})
	} else {
		a.serveFreeJSONChat(w, resp, rid, chosenModel, start)
	}
}

// freeUpstreamChat 单次上游转发（返回 nil = 网络/池错误；调用方负责 Body/Cancel 生命周期与健康记录）。
// 第三返回值为错误类型："timeout"（黑洞/排队超时，调用方计入 retired 摘除）| "network_error" | ""
func (a *App) freeUpstreamChat(r *http.Request, body []byte, req *chatReq, line *config.Line, upID string) (*http.Response, context.CancelFunc, string) {
	upBody, err := replaceModel(body, upID)
	if err != nil {
		return nil, func() {}, "network_error"
	}
	client, cerr := a.clientFor(line.ID)
	if cerr != nil {
		logFreeErr(line.ID, upID, cerr)
		return nil, func() {}, "network_error"
	}
	// 非流式 45s：上游黑洞（请求被静默挂起）时让用户尽快拿到明确报错，而非干等
	// 流式 600s：长生成合理，黑洞场景由 retired 摘除兜底
	timeout := 45 * time.Second
	if req.Stream {
		timeout = 600 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	resp, _, err := client.Do(ctx, upBody, req.Stream, "/chat/completions")
	if err != nil {
		cancel()
		logFreeErr(line.ID, upID, err)
		// 客户端主动断开（9s 超时脚本/用户取消）：记 499 不污染 502 统计（20260919）
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			return nil, func() {}, "client_cancel"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, func() {}, "timeout"
		}
		// 全池纯限流（429 tpm/rpm）：业务态繁忙而非故障，上层转译 429
		if strings.Contains(err.Error(), "UPSTREAM_RATE_LIMITED") {
			return nil, func() {}, "rate_limited"
		}
		return nil, func() {}, "network_error"
	}
	return resp, cancel, ""
}

// failRequest0 免费转发前置失败（未拿到响应）：记一条失败请求（0 计费）
func (a *App) failRequest0(uid int64, keyHash, model string, stream bool, reason string, statusCode int) {
	rid := a.insertRequest(uid, keyHash, "chat", model, stream)
	if rid != 0 {
		a.failRequest(rid, reason, statusCode)
	}
}

// crashBodyPeek 探测 5xx 响应体是否为推理引擎崩溃（CUDA/TensorRT，换钥重试无意义）。
// 读限 32KB，读完即丢弃。
func crashBodyPeek(body io.Reader) bool {
	b, _ := io.ReadAll(io.LimitReader(body, 32<<10))
	s := string(b)
	return strings.Contains(s, "TensorRT-LLM") ||
		strings.Contains(s, "CUDA runtime error") ||
		strings.Contains(s, "illegal memory access")
}

// healthErrType 状态码 → 健康记录错误类型（口径同 Rust）
func healthErrType(code int) string {
	switch {
	case code == 429:
		return "rate_limited"
	case code >= 500:
		return "upstream_error"
	default:
		return "client_error"
	}
}

// serveFreeJSONChat 非流式：读全量 → usage 记账 + 健康记录 → 透传
func (a *App) serveFreeJSONChat(w http.ResponseWriter, resp *http.Response, rid int64, model string, start time.Time) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	lat := time.Since(start).Milliseconds()
	if err != nil {
		a.recordHealth(model, false, "network_error", 0, lat)
		a.failRequest(rid, "read_error", 502)
		errOut(w, 502, "upstream_error", "上游响应读取失败")
		return
	}
	ok := resp.StatusCode < 400
	a.recordHealth(model, ok, healthResultType(ok, resp.StatusCode), resp.StatusCode, lat)
	var jr struct {
		Usage *usageJSON `json:"usage"`
	}
	_ = jsonUnmarshal(raw, &jr)
	u := usageFromJSON(jr.Usage)
	if ok {
		src := "estimated"
		if jr.Usage != nil {
			src = "actual"
		}
		a.okFreeRequest(rid, u, resp.StatusCode, src, lat)
	} else {
		a.failRequest(rid, fmt.Sprintf("upstream_%d", resp.StatusCode), resp.StatusCode)
	}
	// 状态/头透传（信息隔离：剥除成本/追踪类字段）
	for _, k := range []string{"Content-Type", "X-Request-Id"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(stripSensitive(raw))
}

// serveFreeStreamChat SSE 流式：逐行转发 + usage 末帧捕获 + 流结束落库。
// 稳态保障（20260918 紧急修复，"老是断开连接"主诉）：
//   - 响应头延迟到首帧到达才写——首帧未到前任何失败都能整体重来；
//   - 首帧未到即断流（上游建连即掐，sensenova 过载常见）：refetch 换钥重试一次，
//     重试仍失败则以 JSON 业务态报错（而非半截 SSE）；
//   - refetch 为 nil 时保持旧行为（如 tools 场景）。
func (a *App) serveFreeStreamChat(w http.ResponseWriter, r *http.Request, resp *http.Response, cancel context.CancelFunc, rid int64, model string, start time.Time, refetch func() (*http.Response, context.CancelFunc, bool)) {
	curResp, curCancel := resp, cancel
	defer func() {
		curResp.Body.Close()
		curCancel()
	}()
	flusher, okF := w.(http.Flusher)
	if !okF {
		a.failRequest(rid, "no_flusher", 500)
		errOut(w, 500, "internal_error", "流式不可用")
		return
	}

	var u billing.Usage
	var firstByte time.Time // 首帧到达时间：tps 按生成阶段（首字之后）计
	headersSent := false
	retried := false
	sawDone := false // 上游是否已发 [DONE]（精确判定；正常完成依据——usage 缺失不再误判 stream_incomplete）
	sendHeaders := func() {
		if headersSent {
			return
		}
		headersSent = true
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(curResp.StatusCode)
	}
	// 初始响应即为错误码：直接 JSON 报错（头未发，可安全改写）
	if curResp.StatusCode >= 400 {
		_, _ = io.Copy(io.Discard, io.LimitReader(curResp.Body, 64<<10))
		a.failRequest(rid, fmt.Sprintf("upstream_%d", curResp.StatusCode), curResp.StatusCode)
		errOut(w, curResp.StatusCode, "upstream_error", "上游返回错误（"+fmt.Sprintf("%d", curResp.StatusCode)+"），请稍后重试")
		return
	}

	buf := make([]byte, 0, 32<<10)
	tmp := make([]byte, 16<<10)
	for {
		select {
		case <-r.Context().Done():
			a.finishFreeStream(rid, model, u, start, firstByte, true, sawDone)
			return
		default:
		}
		n, rerr := curResp.Body.Read(tmp)
		if n > 0 {
			if firstByte.IsZero() {
				firstByte = time.Now()
			}
			buf = append(buf, tmp[:n]...)
			if len(buf) > 8<<20 {
				// 上游持续发无换行数据：熔断防内存撑爆（恶意/异常上游），与收费线同口径
				a.recordHealth(model, false, "upstream_error", 0, time.Since(start).Milliseconds())
				curResp.Body.Close()
				curCancel()
				a.finishFreeStream(rid, model, u, start, firstByte, true, sawDone)
				return
			}
			if !headersSent {
				// 首帧闸门（20260919）：响应头延迟到首个真实内容帧才发——
				// ①上游秒断（0 字节内容）→ 整体重试一次；②首个 data 帧即错误事件
				// （商汤 429-in-stream 等）→ 不向客户端漏传错误帧，换钥重试/转业务态
				held := make([][]byte, 0, 8)
				sawErr := ""
				gate := func() bool { // true=继续读上游，false=退出
					for {
						i := bytes.IndexByte(buf, '\n')
						if i < 0 {
							return true
						}
						lineBytes := buf[:i]
						buf = buf[i+1:]
						if d, isErr := sseErrorEvent(lineBytes); isErr {
							sawErr = d
							return false
						}
						if isSSEDone(lineBytes) {
							sawDone = true
						}
						held = append(held, lineBytes) // 非错误行整行暂扣（含空行）：发头后补发
					}
				}
				if !gate() {
					a.recordHealth(model, false, "upstream_error", 0, time.Since(start).Milliseconds())
					curResp.Body.Close()
					curCancel()
					if !retried && refetch != nil && r.Context().Err() == nil {
						retried = true
						if rateLimitedPayload(sawErr) {
							a.failRequest(rid, "upstream_rate_limited", 429)
							errOut(w, 429, "line_busy", "官方自营线当前访问过于火爆，请稍后重试或改用 aqua/ 收费模型（更稳定）")
							return
						}
						if r2, c2, ok2 := refetch(); ok2 {
							curResp, curCancel = r2, c2
							firstByte = time.Time{}
							buf = buf[:0]
							sawDone = false // 重试重新计数：旧响应的暂扣行已整体废弃
							continue
						}
					}
					a.failRequest(rid, "stream_incomplete", 503)
					errOut(w, 503, "line_busy", "上游连接刚建立即中断，请稍后重试")
					return
				}
				if len(held) > 0 {
					sendHeaders()
					for _, hb := range held {
						if len(bytes.TrimSpace(hb)) == 0 {
							_, _ = w.Write([]byte("\n")) // 空行=事件边界，原样还原（防相邻事件重放被合并成坏帧）
							continue
						}
						out := sanitizeStreamLine(hb, &u)
						_, _ = w.Write(out)
						_, _ = w.Write([]byte("\n"))
					}
					flusher.Flush()
				}
			}
			if headersSent {
				for {
					i := bytes.IndexByte(buf, '\n')
					if i < 0 {
						break
					}
					lineBytes := buf[:i]
					buf = buf[i+1:]
					if isSSEDone(lineBytes) {
						sawDone = true
					}
					out := sanitizeStreamLine(lineBytes, &u)
					_, _ = w.Write(out)
					_, _ = w.Write([]byte("\n"))
					flusher.Flush()
				}
			}
		}
		if rerr != nil {
			// 首帧未到即断流：尚未向客户端写过任何字节 → 换钥整体重试一次
			if !headersSent && !retried && refetch != nil && r.Context().Err() == nil {
				retried = true
				a.recordHealth(model, false, "network_error", 0, time.Since(start).Milliseconds())
				curResp.Body.Close()
				curCancel()
				if r2, c2, ok2 := refetch(); ok2 {
					curResp, curCancel = r2, c2
					continue
				}
				a.failRequest(rid, "stream_incomplete", 503)
				errOut(w, 503, "line_busy", "上游连接刚建立即中断（已自动重试仍失败），请稍后重试")
				return
			}
			a.finishFreeStream(rid, model, u, start, firstByte, r.Context().Err() != nil, sawDone)
			return
		}
	}
}

// sseErrorEvent 判断 SSE 行是否为「错误事件而非内容帧」（仅首帧闸门用）：
// data: JSON 含 error 字段，或含 code+message 且无 choices（商汤/各家中断流错误形态）。
// 返回 payload 原文（限流判定用）；非错误行第二返回值 false。
func sseErrorEvent(line []byte) (string, bool) {
	s := strings.TrimSpace(string(line))
	if !strings.HasPrefix(s, "data:") {
		return "", false
	}
	payload := strings.TrimSpace(s[5:])
	if payload == "" || payload == "[DONE]" {
		return "", false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &m) != nil {
		return "", false
	}
	if _, hasErr := m["error"]; hasErr {
		return payload, true
	}
	_, hasCode := m["code"]
	_, hasMsg := m["message"]
	_, hasChoices := m["choices"]
	if hasCode && hasMsg && !hasChoices {
		return payload, true
	}
	return "", false
}

// rateLimitedPayload 错误事件是否为限流（tpm/rpm/quota 口径 → 429 业务态）
func rateLimitedPayload(payload string) bool {
	l := strings.ToLower(payload)
	return strings.Contains(l, "rate") || strings.Contains(l, "429003") ||
		strings.Contains(l, "tpm") || strings.Contains(l, "rpm") ||
		strings.Contains(l, "quota")
}

// finishFreeStream 流结束：健康记录 + usage 落库（免费口径：billed=0）；
// clientGone=客户端主动断开 → 499 client_cancel（不污染 502 统计，20260919）；
// sawDone=上游已发 [DONE]（精确判定）→ 视为正常完成：recordHealth 成功 + 正常入账
// （usage 缺失按保底口径 estimated 落库），不再误判 stream_incomplete 502
func (a *App) finishFreeStream(rid int64, model string, u billing.Usage, start time.Time, firstByte time.Time, clientGone bool, sawDone bool) {
	lat := time.Since(start).Milliseconds()
	// 首字延迟（TTFT）：请求起点 → 首帧到达；无首帧（未产出/客户端提前断开）记 0
	// ——与收费路径 okRequestGen 的 ttftMs 同口径，保证两条线的"首字"可比
	ttft := int64(0)
	if !firstByte.IsZero() {
		if d := firstByte.Sub(start).Milliseconds(); d > 0 {
			ttft = d
		}
	}
	ok := sawDone || u.PromptTokens > 0 || u.CompletionTokens > 0
	a.recordHealth(model, ok, healthResultType(ok, 200), 200, lat)
	if ok {
		src := "estimated"
		if u.PromptTokens > 0 || u.CompletionTokens > 0 {
			src = "actual"
		}
		a.okFreeRequestGen(rid, u, 200, src, lat, genMs(firstByte), ttft)
	} else if clientGone {
		a.failRequest(rid, "client_cancel", 499)
	} else {
		a.failRequest(rid, "stream_incomplete", 502)
	}
}

// healthResultType ok + 状态码 → 健康记录类型
func healthResultType(ok bool, code int) string {
	if ok {
		return "success"
	}
	return healthErrType(code)
}

// okFreeRequest 免费模型成功回写（usage 照记，不计费；src=usage 来源口径；tps=输出 tokens/秒）
func (a *App) okFreeRequest(rid int64, u billing.Usage, statusCode int, src string, latMs int64) {
	// 非流式：无首帧概念，ttft 记 0（与收费路径 okRequest → okRequestGen(..., ttftMs=0) 同口径）
	a.okFreeRequestGen(rid, u, statusCode, src, latMs, latMs, 0)
}

// okFreeRequestGen 免费模型成功回写（生成阶段口径）：genMs=生成阶段耗时（≤0 回退总耗时）；
// ttftMs=首字延迟（请求起点→首帧到达，非流式/无首帧记 0）。
// 20260921 补：此前只写 tps，**latency_ms / first_ms 恒为 0** → 免费模型在 /v1/models/status
// 里永远没有首字与总耗时（前端模型卡片只能显示"--"）。补齐后免费模型与收费模型同口径可比。
func (a *App) okFreeRequestGen(rid int64, u billing.Usage, statusCode int, src string, latMs int64, genMs int64, ttftMs int64) {
	tpsMs := genMs
	if tpsMs <= 0 {
		tpsMs = latMs
	}
	tps := 0.0
	if tpsMs > 0 {
		tps = float64(u.CompletionTokens) * 1000 / float64(tpsMs)
	}
	_, _ = a.DB.Exec(
		"UPDATE requests SET ok=1, status_code=?, prompt_tokens=?, completion_tokens=?, cached_tokens=?, total_tokens=?, billed=0, bill_amount_micro=0, bill_state='free', usage_source=?, tps=?, latency_ms=?, first_ms=? WHERE rowid=?",
		statusCode, u.PromptTokens, u.CompletionTokens, u.CachedTokens, u.PromptTokens+u.CompletionTokens, src, tps, latMs, ttftMs, rid)
}
