package httpapi

// 扩展能力端点（与 Rust misc.rs/chat.rs 契约等价）：
// /v1/models/{id} 单模型详情 + embeddings / rerank / moderations / audio / videos 直转

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"acu-aqua/gateway/internal/auth"
	"acu-aqua/gateway/internal/billing"
	"acu-aqua/gateway/internal/config"
)

// ---------------------------------------------------------------------------
// /v1/models/{id} 单模型详情（OpenAI SDK 兼容）
// ---------------------------------------------------------------------------

// handleModelDetail GET /v1/models/{id}（收费模型形如 line-id/model，两段路径拼接）
func (a *App) handleModelDetail(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("id")
	if rest := r.PathValue("rest"); rest != "" {
		rawID = rawID + "/" + rest
	}
	actx := auth.Authenticate(a.DB.DB, r)
	created := time.Now().Unix()
	id := config.NormalizeModel(rawID)
	retired := a.retiredUpstreams()

	// auto 智能路由
	if config.IsAutoModel(id) {
		jsonOut(w, 200, map[string]any{
			"id": "auto", "object": "model", "created": created, "owned_by": "acu",
			"auto":        true,
			"description": "智能自动路由：每次请求实时选择当前成功率最高、响应最快的模型，快与稳优先，不保证每次命中同一模型",
		})
		return
	}

	// 收费模型（含线前缀）：从列表条目取（VIP 价目回退口径与列表一致）
	if lineID, siteID, hasPrefix := config.SplitModel(rawID); hasPrefix {
		if l := a.Cfg.LineByID(lineID); l != nil && l.Mode != "free" {
			for _, item := range a.modelListEntries(actx, created) {
				if item["id"] == lineID+"/"+siteID {
					jsonOut(w, 200, item)
					return
				}
			}
			errOut(w, 404, "model_not_found", "该模型已下架或暂不可用")
			return
		}
	}

	// 上游永久下线：详情返回 410 指引换模型
	if line, upID, ok := a.freeResolve(id); ok {
		if retired[upID] {
			errOut(w, 410, "model_retired",
				"模型 "+id+" 已在上游永久下线，请更换模型。GET /v1/models 可查全部可用模型")
			return
		}
		item := map[string]any{"id": id, "object": "model", "created": created, "owned_by": line.ID}
		if h, ok := a.computeHealth()[id]; ok {
			item["health"] = h
		}
		jsonOut(w, 200, item)
		return
	}
	errOut(w, 404, "model_not_found",
		"模型 "+id+" 不存在：目录与动态模型表均未收录，请 GET /v1/models 查看可用模型列表")
}

// ---------------------------------------------------------------------------
// 免费线直转 helper（embeddings / rerank / moderations / audio / videos）
// ---------------------------------------------------------------------------

// freeForwardPath 免费线直转：model 替换为上游 ID 后原样转发到 <base><path>，
// 响应透传 + usage 记账 + 健康记录（免费口径，不计费）
func (a *App) freeForwardPath(w http.ResponseWriter, r *http.Request, body []byte, model, path string, timeout time.Duration, endpoint string) {
	actx := auth.Authenticate(a.DB.DB, r)
	uid, keyHash := int64(0), ""
	if actx != nil {
		uid, keyHash = actx.UserID, actx.KeyHash
	}
	norm := config.NormalizeModel(model)
	line, upID, ok := a.freeResolve(norm)
	if !ok {
		errOut(w, 404, "model_not_found",
			"模型 "+norm+" 不存在：目录与动态模型表均未收录，请 GET /v1/models 查看可用模型列表")
		return
	}
	retired := a.retiredUpstreams()
	if retired[upID] {
		errOut(w, 410, "model_retired",
			"模型 "+norm+" 已在上游永久下线，请更换模型。GET /v1/models 可查全部可用模型")
		return
	}

	// model 字段替换为上游真实 ID
	var v map[string]any
	if err := json.Unmarshal(body, &v); err == nil {
		v["model"] = upID
		body, _ = json.Marshal(v)
	}

	start := time.Now()
	client := a.clientFor(line.ID)
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	resp, _, err := client.Do(ctx, body, false, path)
	if err != nil {
		a.recordHealth(norm, false, "network_error", 0, time.Since(start).Milliseconds())
		rid := a.insertRequest(uid, keyHash, endpoint, norm, false)
		if rid != 0 {
			a.failRequest(rid, "upstream_error")
		}
		errOut(w, 502, "upstream_error", "上游服务暂时不可用，请稍后重试")
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	lat := time.Since(start).Milliseconds()
	okCall := resp.StatusCode < 400
	a.recordHealth(norm, okCall, healthResultType(okCall, resp.StatusCode), resp.StatusCode, lat)
	rid := a.insertRequest(uid, keyHash, endpoint, norm, false)
	if okCall {
		var j map[string]any
		if json.Unmarshal(raw, &j) == nil {
			a.okFreeRequest(rid, usageFromUpstreamJSON(j), resp.StatusCode)
		} else if rid != 0 {
			a.okFreeRequest(rid, billing.Usage{}, resp.StatusCode)
		}
	} else if rid != 0 {
		a.failRequest(rid, fmt.Sprintf("upstream_%d", resp.StatusCode))
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(stripSensitive(raw))
}

// modelOf 请求体取 model 字段（缺失时给 400）
func modelOf(w http.ResponseWriter, body []byte) (string, bool) {
	var v struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &v); err != nil || v.Model == "" {
		errOut(w, 400, "bad_request", "请求体必须包含 \"model\"（模型 ID），可先 GET /v1/models 查看可用模型")
		return "", false
	}
	return v.Model, true
}

// ---------------------------------------------------------------------------
// POST /v1/embeddings
// ---------------------------------------------------------------------------

func (a *App) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求体读取失败，请检查网络后重试")
		return
	}
	model, ok := modelOf(w, body)
	if !ok {
		return
	}
	a.freeForwardPath(w, r, body, model, "/embeddings", 60*time.Second, "embeddings")
}

// ---------------------------------------------------------------------------
// POST /v1/moderations
// ---------------------------------------------------------------------------

func (a *App) handleModerations(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求体读取失败，请检查网络后重试")
		return
	}
	model, ok := modelOf(w, body)
	if !ok {
		return
	}
	a.freeForwardPath(w, r, body, model, "/moderations", 60*time.Second, "moderations")
}

// ---------------------------------------------------------------------------
// POST /v1/rerank
// ---------------------------------------------------------------------------

func (a *App) handleRerank(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求体读取失败，请检查网络后重试")
		return
	}
	model, ok := modelOf(w, body)
	if !ok {
		return
	}
	a.freeForwardPath(w, r, body, model, "/rerank", 60*time.Second, "rerank")
}

// ---------------------------------------------------------------------------
// POST /v1/audio/speech | /v1/audio/transcriptions | /v1/videos/generations
// ---------------------------------------------------------------------------

func (a *App) handleAudioSpeech(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求体读取失败，请检查网络后重试")
		return
	}
	model, ok := modelOf(w, body)
	if !ok {
		return
	}
	a.freeForwardPath(w, r, body, model, "/audio/speech", 120*time.Second, "audio.speech")
}

func (a *App) handleAudioTranscriptions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 20<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求体读取失败，请检查网络后重试")
		return
	}
	model, ok := modelOf(w, body)
	if !ok {
		return
	}
	a.freeForwardPath(w, r, body, model, "/audio/transcriptions", 60*time.Second, "audio.transcriptions")
}

func (a *App) handleVideos(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求体读取失败，请检查网络后重试")
		return
	}
	model, ok := modelOf(w, body)
	if !ok {
		return
	}
	a.freeForwardPath(w, r, body, model, "/videos/generations", 300*time.Second, "videos")
}
