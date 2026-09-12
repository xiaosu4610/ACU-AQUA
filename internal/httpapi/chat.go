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
	"strings"
	"time"

	"acu-aqua/gateway/internal/auth"
	"acu-aqua/gateway/internal/billing"
	"acu-aqua/gateway/internal/config"
	"acu-aqua/gateway/internal/upstream"
)

// chatReq chat/completions 请求体（透传字段用 raw 保真）
type chatReq struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	MaxTokens json.Number    `json:"max_tokens,omitempty"`
	Raw      json.RawMessage `json:"-"`
}

// usage JSON（上游响应内）
type usageJSON struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// handleChat /v1/chat/completions 主入口：
// 鉴权 → 收费线路由（line-id/ 前缀且线存在）→ 免费模型（含 auto 路由/动态目录/旧 ID 兼容）
//  → 预扣→上游→结算（多退少补）
func (a *App) handleChat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求体读取失败")
		return
	}
	var req chatReq
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
		errOut(w, 400, "bad_request", "请求体格式错误或缺 model 字段")
		return
	}
	// 收费线（line-id/ 前缀且线存在且非 free）；其余全部走免费分发
	// （无前缀裸模型 / 旧厂商前缀 zhipu/glm-4-flash / auto 智能路由 / 动态目录）
	lineID, siteID, hasPrefix := config.SplitModel(req.Model)
	var line *config.Line
	if hasPrefix {
		line = a.Cfg.LineByID(lineID)
	}
	if line == nil || line.Mode == "free" {
		// 绞杀者模式：免费线仍由旧网关承接（免 Go 鉴权，旧网关自管）
		if legacyProxy != nil {
			a.proxyChat(w, r, body)
			return
		}
		// 纯 Go 模式：免费线 open 模式（与 Rust AUTH_MODE=open 等价）——
		// 免登录直接对话（体验中心/树洞/竞技场依赖）；带凭据则 usage 记到用户
		actx := auth.Authenticate(a.DB.DB, r)
		uid, kh := int64(0), ""
		if actx != nil {
			uid, kh = actx.UserID, actx.KeyHash
		}
		a.handleFreeChat(w, r, body, &req, uid, kh)
		return
	}
	// 鉴权（收费线强制登录/API 密钥）
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "invalid_api_key", "请先登录或提供有效的 API 密钥")
		return
	}
	var model *config.Model
	for i := range line.Models {
		if line.Models[i].SiteID == siteID {
			model = &line.Models[i]
			break
		}
	}
	if model == nil {
		errOut(w, 404, "model_not_found", "模型不存在：" + req.Model)
		return
	}

	// 上游模型名替换
	upBody, err := replaceModel(body, model.UpstreamID)
	if err != nil {
		errOut(w, 500, "internal_error", "请求处理失败")
		return
	}

	// 价格组与生效价目
	grp := billing.UserPriceGrp(a.DB.DB, actx.UserID, line.Mode)
	pricing, err := billing.CurrentPricing(a.DB.DB, req.Model, grp)
	if err != nil {
		errOut(w, 500, "internal_error", "计费查询失败")
		return
	}
	if pricing == nil {
		errOut(w, 404, "model_not_found", "该模型已下架或暂不可用")
		return
	}

	// 请求落库（拿 rowid 供面值回写）
	rid := a.insertRequest(actx.UserID, actx.KeyHash, "chat", req.Model, req.Stream)
	if rid == 0 {
		errOut(w, 500, "internal_error", "请求记录失败")
		return
	}

	// 预扣额：per_call = 单价；per_token = 输入估算 + max_tokens×输出价（保守上限），且 ≥ floor
	prehold := a.preholdAmount(model, pricing, upBody)

	if err := billing.Prehold(a.DB.DB, actx.UserID, prehold, rid); err != nil {
		if errors.Is(err, billing.ErrInsufficientBalance) {
			errOut(w, 429, "insufficient_quota", "余额不足，请先到控制台充值（先付后用，绝不透支）")
			return
		}
		errOut(w, 500, "internal_error", "预扣失败")
		return
	}

	// 上游转发
	client := a.clientFor(line.ID)
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	resp, key, err := client.Do(ctx, upBody, req.Stream, "/chat/completions")
	if err != nil {
		log.Printf("[chat] 上游失败 model=%s line=%s err=%v", req.Model, line.ID, err)
		_ = billing.Settle(a.DB.DB, actx.UserID, prehold, 0, rid, 0, "upstream_error")
		a.failRequest(rid, "upstream_error")
		errOut(w, 502, "upstream_error", "上游服务暂时不可用，请稍后重试")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		eb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = billing.Settle(a.DB.DB, actx.UserID, prehold, 0, rid, 0, "upstream_"+fmt.Sprint(resp.StatusCode))
		a.failRequest(rid, "upstream_status")
		// 信息隔离：上游报错转译为站点标准错误码，不透传原文
		upstreamErrOut(w, resp.StatusCode, eb)
		return
	}

	if req.Stream {
		a.serveStreamChat(w, r, resp, actx.UserID, prehold, rid, client, key, line, model)
		return
	}
	a.serveJSONChat(w, resp, actx.UserID, prehold, rid, client, key, line, model)
}

// serveJSONChat 非流式：读全量 → 剥层 → 结算 → 回写
func (a *App) serveJSONChat(w http.ResponseWriter, resp *http.Response, uid, prehold, rid int64, c *upstream.Client, key *upstream.KeyState, line *config.Line, m *config.Model) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		_ = billing.Settle(a.DB.DB, uid, prehold, 0, rid, 0, "read_error")
		a.failRequest(rid, "read_error")
		errOut(w, 502, "upstream_error", "上游响应读取失败")
		return
	}
	var jr struct {
		Usage *usageJSON `json:"usage"`
	}
	_ = json.Unmarshal(raw, &jr)
	u := usageFromJSON(jr.Usage)
	final := int64(0)
	face := int64(0)
	if jr.Usage != nil {
		p, _ := billing.CurrentPricing(a.DB.DB, line.ID+"/"+m.SiteID, billing.UserPriceGrp(a.DB.DB, uid, line.Mode))
		// per_call：不看 usage，收单价；per_token：三段精算
		if p != nil && p.Mode == "per_call" {
			final = p.PriceMicro
		} else {
			final = billing.MeterTokens(u, p)
		}
		face = billing.FaceCostMicro(u, m.InCostRate10, m.CacheCostRate10, m.OutCostRate10)
	} else {
		// 无 usage：按保底/单价收
		p, _ := billing.CurrentPricing(a.DB.DB, line.ID+"/"+m.SiteID, billing.UserPriceGrp(a.DB.DB, uid, line.Mode))
		if p != nil {
			if p.Mode == "per_call" {
				final = p.PriceMicro
			} else {
				final = p.FloorMicro
			}
		}
		face = 0
	}
	// 结算（多退少补）+ 面值台账 + 请求回写
	_ = billing.Settle(a.DB.DB, uid, prehold, final, rid, final, "billed")
	if face > 0 && key != nil {
		c.Pool.ReportFace(key, face)
		_ = billing.LineKeyReport(a.DB.DB, line.ID, key.Idx, key.InitialMicro, face, rid)
	}
	a.okRequest(rid, u, final, face)
	// 剥层后透传（信息隔离：移除成本/追踪类字段）
	out := stripSensitive(raw)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(out)
}

// serveStreamChat SSE 流式：逐行转发 + 首尾事件计费
func (a *App) serveStreamChat(w http.ResponseWriter, r *http.Request, resp *http.Response, uid, prehold, rid int64, c *upstream.Client, key *upstream.KeyState, line *config.Line, m *config.Model) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = billing.Settle(a.DB.DB, uid, prehold, 0, rid, 0, "no_flusher")
		errOut(w, 500, "internal_error", "流式不可用")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)

	var u billing.Usage
	final := int64(0)
	faceTotal := int64(0)
	settled := false
	settle := func() {
		if settled {
			return
		}
		settled = true
		p, _ := billing.CurrentPricing(a.DB.DB, line.ID+"/"+m.SiteID, billing.UserPriceGrp(a.DB.DB, uid, line.Mode))
		if u.PromptTokens > 0 || u.CompletionTokens > 0 {
			if p != nil && p.Mode == "per_call" {
				// per_call：不看 usage，收单价
				final = p.PriceMicro
			} else {
				final = billing.MeterTokens(u, p)
			}
			faceTotal = billing.FaceCostMicro(u, m.InCostRate10, m.CacheCostRate10, m.OutCostRate10)
			if faceTotal > 0 && key != nil {
				c.Pool.ReportFace(key, faceTotal)
				_ = billing.LineKeyReport(a.DB.DB, line.ID, key.Idx, key.InitialMicro, faceTotal, rid)
			}
		} else if p != nil {
			if p.Mode == "per_call" {
				final = p.PriceMicro
			} else {
				final = p.FloorMicro
			}
		}
		_ = billing.Settle(a.DB.DB, uid, prehold, final, rid, final, "billed")
		a.okRequest(rid, u, final, faceTotal)
	}
	defer settle()

	ctx := r.Context()
	buf := make([]byte, 0, 32<<10)
	tmp := make([]byte, 16<<10)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			// 按 SSE 行处理：完整行才转发（便于剥层与 usage 抓取）
			for {
				i := bytes.IndexByte(buf, '\n')
				if i < 0 {
					break
				}
				lineBytes := buf[:i]
				buf = buf[i+1:]
				out := sanitizeStreamLine(lineBytes, &u)
				_, _ = w.Write(out)
				_, _ = w.Write([]byte("\n"))
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// sanitizeStreamLine SSE data 行处理：抓 usage（末帧）+ 剥除成本/计费/追踪类字段（信息隔离）
func sanitizeStreamLine(line []byte, u *billing.Usage) []byte {
	s := strings.TrimSpace(string(line))
	if !strings.HasPrefix(s, "data:") {
		return line
	}
	payload := strings.TrimSpace(s[5:])
	if payload == "" || payload == "[DONE]" {
		return line
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &m) != nil {
		return line
	}
	// usage 抓取（多个帧带 usage 时取最后一个非零值）
	if ur, ok := m["usage"]; ok && ur != nil && string(ur) != "null" {
		var uj usageJSON
		if json.Unmarshal(ur, &uj) == nil && uj.PromptTokens > 0 {
			*u = usageFromJSON(&uj)
		}
	}
	// 剥层：绝不让上游成本/计费/追踪字段到达下游
	changed := false
	for _, k := range []string{"cost_cny", "billing_pending", "trace_id"} {
		if _, ok := m[k]; ok {
			delete(m, k)
			changed = true
		}
	}
	if !changed {
		return line
	}
	out, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return []byte("data: " + string(out))
}

// usageFromJSON JSON usage → billing.Usage（缓存命中兼容两种字段）
func usageFromJSON(j *usageJSON) billing.Usage {
	if j == nil {
		return billing.Usage{}
	}
	cached := int64(0)
	if j.PromptTokensDetails != nil {
		cached = j.PromptTokensDetails.CachedTokens
	}
	return billing.Usage{PromptTokens: j.PromptTokens, CompletionTokens: j.CompletionTokens, CachedTokens: cached}
}

// replaceModel 替换请求体中的 model 字段（保持其余字段原样）
func replaceModel(body []byte, newModel string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	mb, _ := json.Marshal(newModel)
	m["model"] = mb
	return json.Marshal(m)
}

// stripSensitive 信息隔离：剥除上游响应中的成本/计费/追踪字段
func stripSensitive(raw []byte) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	changed := false
	for _, k := range []string{"cost_cny", "billing_pending", "trace_id", "usage.cost_cny"} {
		if _, ok := m[k]; ok {
			delete(m, k)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// preholdAmount 预扣额：per_call=单次价；per_token=输入估算+max_tokens×输出价，≥floor
func (a *App) preholdAmount(m *config.Model, p *billing.PricingInfo, body []byte) int64 {
	if p == nil {
		return 1000
	}
	if p.Mode == "per_call" {
		return p.PriceMicro
	}
	// 输入估算：消息体长度/4 × 1.2 保守余量（与 Rust 版口径一致）
	var req struct {
		Messages json.RawMessage `json:"messages"`
		MaxTokens int64          `json:"max_tokens"`
	}
	_ = json.Unmarshal(body, &req)
	est := int64(float64(len(req.Messages))/4*1.2) + 16
	maxOut := req.MaxTokens
	if maxOut <= 0 {
		maxOut = 4096
	}
	estCost := est*p.InRate10/10000 + maxOut*p.OutRate10/10000
	if estCost < p.FloorMicro {
		estCost = p.FloorMicro
	}
	return estCost
}

// insertRequest 请求落库，返回 rowid
func (a *App) insertRequest(uid int64, keyHash, endpoint, model string, stream bool) int64 {
	res, err := a.DB.Exec(
		"INSERT INTO requests (key_hash, endpoint, model, user_id, ok, ts, bill_state, stream_mode) VALUES (?,?,?,?,0,?,?,?)",
		keyHash, endpoint, model, uid, time.Now().Unix(), "", boolToInt(stream))
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

// okRequest 成功回写：usage + 金额 + 面值成本 + bill_state='billed'
func (a *App) okRequest(rid int64, u billing.Usage, amount int64, face int64) {
	_, _ = a.DB.Exec(
		"UPDATE requests SET ok=1, prompt_tokens=?, completion_tokens=?, cached_tokens=?, total_tokens=?, billed=1, bill_amount_micro=?, unit_price_micro=?, face_cost_micro=?, bill_state='billed' WHERE rowid=?",
		u.PromptTokens, u.CompletionTokens, u.CachedTokens, u.PromptTokens+u.CompletionTokens, amount, amount, face, rid)
}

// failRequest 失败回写
func (a *App) failRequest(rid int64, reason string) {
	_, _ = a.DB.Exec("UPDATE requests SET ok=0, error=?, bill_state='refunded' WHERE rowid=?", reason, rid)
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// clientFor 线客户端缓存（每线一个 KeyPool）
func (a *App) clientFor(lineID string) *upstream.Client {
	a.clientsMu.Lock()
	defer a.clientsMu.Unlock()
	if a.clients == nil {
		a.clients = map[string]*upstream.Client{}
	}
	if c, ok := a.clients[lineID]; ok {
		return c
	}
	l := a.Cfg.LineByID(lineID)
	c := upstream.NewClient(l)
	a.clients[lineID] = c
	return c
}

// dbErr 检查（占位：统一错误检查）
func dbErr(err error) bool {
	return err != nil && err != sql.ErrNoRows
}
