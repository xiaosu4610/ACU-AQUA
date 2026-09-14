// 免费线（mode="free"）：不计费不预扣，仅转发与用量记账。
// 健康评分 / 下线登记 / NVIDIA 动态目录 / auto 路由候选——口径与 Rust 版逐函数对齐。
package httpapi

import (
	"bytes"
	"context"
	"database/sql"
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

// dynamicUpstreamID 动态目录反查上游真实 ID
func (a *App) dynamicUpstreamID(model string) (string, bool) {
	var up string
	err := a.DB.QueryRow("SELECT upstream_id FROM nvidia_models WHERE id = ?", model).Scan(&up)
	if err != nil || up == "" {
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
	// 上游永久下线的模型不进候选池
	retired := a.retiredUpstreams()
	n := 0
	for _, c := range cands {
		if !retired[upstreamIDOf(a, c.m)] {
			cands[n] = c
			n++
		}
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

// upstreamIDOf 站点模型 → 上游真实 ID（配置目录优先，动态目录次之）
func upstreamIDOf(a *App, siteID string) string {
	if _, m := a.Cfg.FindFreeModel(siteID); m != nil {
		return m.UpstreamID
	}
	if up, ok := a.dynamicUpstreamID(siteID); ok {
		return up
	}
	return ""
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
	if l, m := a.Cfg.FindFreeModel(siteID); l != nil {
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

	// 指定模型：目录/动态表反查，retired 直接 410（不打上游烧密钥）
	if model != "auto" {
		line, upID, ok := a.freeResolve(model)
		if !ok {
			errOut(w, 404, "model_not_found",
				"模型不存在：目录与动态模型表均未收录，请 GET /v1/models 查看可用模型列表")
			return
		}
		if retired[upID] {
			errOut(w, 410, "model_retired",
				"模型 "+model+" 暂时不可用（上游异常或已下线），通常数小时内自动恢复；GET /v1/models 可查其他可用模型")
			return
		}
		resp, cancel, et := a.freeUpstreamChat(r, body, req, line, upID)
		if resp == nil {
			a.recordHealth(model, false, et, 0, time.Since(start).Milliseconds())
			if et == "timeout" {
				// 黑洞型故障（请求被上游静默挂起）：计入 retired，两次确认后自动摘除
				a.markRetired(upID)
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
			a.markRetired(upID)
			errOut(w, 410, "model_retired",
				"模型 "+model+" 暂时不可用（上游异常或已下线），通常数小时内自动恢复；GET /v1/models 可查其他可用模型")
			return
		}
		rid := a.insertRequest(uid, keyHash, "chat", model, req.Stream)
		w.Header().Set("X-AQUA-Model", model)
		w.Header().Set("X-AQUA-Line", line.ID)
		if req.Stream {
			a.serveFreeStreamChat(w, r, resp, rid, model, start)
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
		resp        *http.Response
		cancel      context.CancelFunc
	)
	for _, m := range cands {
		line, upID, ok := a.freeResolve(m)
		if !ok || retired[upID] {
			continue
		}
		resp2, cancel2, et := a.freeUpstreamChat(r, body, req, line, upID)
		if resp2 == nil {
			a.recordHealth(m, false, et, 0, time.Since(start).Milliseconds())
			if et == "timeout" {
				a.markRetired(upID)
			}
			continue
		}
		if resp2.StatusCode == 404 || resp2.StatusCode == 410 {
			_, _ = io.ReadAll(io.LimitReader(resp2.Body, 8<<10))
			resp2.Body.Close()
			cancel2()
			a.markRetired(upID)
			continue
		}
		chosenModel, chosenLine, resp, cancel = m, line, resp2, cancel2
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
		errOut(w, 502, "upstream_error",
			"智能路由的候选模型这会儿全部没有响应（上游波动），请稍后重试，或直接指定一个模型——GET /v1/models 可查可用列表")
		return
	}
	defer cancel()
	rid := a.insertRequest(uid, keyHash, "chat", chosenModel, req.Stream)
	w.Header().Set("X-AQUA-Model", chosenModel)
	w.Header().Set("X-AQUA-Line", chosenLine.ID)
	if req.Stream {
		a.serveFreeStreamChat(w, r, resp, rid, chosenModel, start)
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
	client := a.clientFor(line.ID)
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
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, func() {}, "timeout"
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

// serveFreeStreamChat SSE 流式：逐行转发 + usage 末帧捕获 + 流结束落库
func (a *App) serveFreeStreamChat(w http.ResponseWriter, r *http.Request, resp *http.Response, rid int64, model string, start time.Time) {
	defer resp.Body.Close()
	flusher, okF := w.(http.Flusher)
	if !okF {
		a.failRequest(rid, "no_flusher", 500)
		errOut(w, 500, "internal_error", "流式不可用")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)

	var u billing.Usage
	var firstByte time.Time // 首帧到达时间：tps 按生成阶段（首字之后）计
	buf := make([]byte, 0, 32<<10)
	tmp := make([]byte, 16<<10)
	for {
		select {
		case <-r.Context().Done():
			a.finishFreeStream(rid, model, u, start, firstByte)
			return
		default:
		}
		n, rerr := resp.Body.Read(tmp)
		if n > 0 {
			if firstByte.IsZero() {
				firstByte = time.Now()
			}
			buf = append(buf, tmp[:n]...)
			for {
				i := bytes.IndexByte(buf, '\n')
				if i < 0 {
					break
				}
				lineBytes := buf[:i]
				buf = buf[i+1:]
				out := sanitizeStreamLine(lineBytes, &u, "")
				_, _ = w.Write(out)
				_, _ = w.Write([]byte("\n"))
				flusher.Flush()
			}
		}
		if rerr != nil {
			a.finishFreeStream(rid, model, u, start, firstByte)
			return
		}
	}
}

// finishFreeStream 流结束：健康记录 + usage 落库（免费口径：billed=0）
func (a *App) finishFreeStream(rid int64, model string, u billing.Usage, start time.Time, firstByte time.Time) {
	lat := time.Since(start).Milliseconds()
	ok := u.PromptTokens > 0 || u.CompletionTokens > 0
	a.recordHealth(model, ok, healthResultType(ok, 200), 200, lat)
	if ok {
		a.okFreeRequestGen(rid, u, 200, "actual", lat, genMs(firstByte))
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
	a.okFreeRequestGen(rid, u, statusCode, src, latMs, latMs)
}

// okFreeRequestGen 免费模型成功回写（生成阶段口径）：genMs=生成阶段耗时（≤0 回退总耗时）
func (a *App) okFreeRequestGen(rid int64, u billing.Usage, statusCode int, src string, latMs int64, genMs int64) {
	tpsMs := genMs
	if tpsMs <= 0 {
		tpsMs = latMs
	}
	tps := 0.0
	if tpsMs > 0 {
		tps = float64(u.CompletionTokens) * 1000 / float64(tpsMs)
	}
	_, _ = a.DB.Exec(
		"UPDATE requests SET ok=1, status_code=?, prompt_tokens=?, completion_tokens=?, cached_tokens=?, total_tokens=?, billed=0, bill_amount_micro=0, bill_state='free', usage_source=?, tps=? WHERE rowid=?",
		statusCode, u.PromptTokens, u.CompletionTokens, u.CachedTokens, u.PromptTokens+u.CompletionTokens, src, tps, rid)
}
