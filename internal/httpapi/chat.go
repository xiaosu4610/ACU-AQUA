package httpapi

import (
	"bufio"
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

// readerBody 把 bufio.Reader 包装成 ReadCloser（首帧探测后缓冲数据自然衔接后续读取）
type readerBody struct {
	io.Reader
	io.Closer
}

// chatReq chat/completions 请求体（透传字段用 raw 保真）
type chatReq struct {
	Model         string      `json:"model"`
	Stream        bool        `json:"stream"`
	MaxTokens     json.Number `json:"max_tokens,omitempty"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	Raw json.RawMessage `json:"-"`
}

// usage JSON（上游响应内）
type usageJSON struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	// 缓存命中兼容字段：部分上游不用 OpenAI 标准结构（缺失时缓存 token 按全价输入
	// 计费——用户被超收）。DeepSeek 旧口径 / Anthropic 网关透传口径
	PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
	CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
}

// handleChat /v1/chat/completions 主入口：
// 鉴权 → 收费线路由（统一前缀按密钥分组选线 / 线前缀显式直连）→ 免费模型（含 auto 路由/动态目录/旧 ID 兼容）
//
//	→ 预扣→上游→结算（多退少补）
func (a *App) handleChat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	// 通道防刷：per-IP 滑窗限流（挡在打上游之前，保 kabuai 上游不被刷满殃及正常计费线）
	if !guard.allow(clientIP(r)) {
		guardReject(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		errOut(w, 400, "bad_request", "请求体读取失败")
		return
	}
	var req chatReq
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
		errOut(w, 400, "bad_request", "请求体格式错误或缺 model 字段")
		return
	}
	// 收费路由判定：统一前缀（按密钥分组）/ 线前缀（显式直连）；其余走免费分发
	// （无前缀裸模型 / 旧厂商前缀 zhipu/glm-4-flash / auto 智能路由 / 动态目录）
	lineID, siteID, hasPrefix := config.SplitModel(req.Model)
	unified := hasPrefix && a.Cfg.Billing.UnifiedPrefix != "" && lineID == a.Cfg.Billing.UnifiedPrefix
	var line *config.Line
	if hasPrefix && !unified {
		line = a.lineByID(lineID)
	}
	if !hasPrefix || (line == nil && !unified) || (line != nil && line.Mode == "free") {
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
	// 鉴权（收费线强制登录/API 密钥；统一前缀分组路由需要密钥分组）
	actx := auth.Authenticate(a.DB.DB, r)
	if actx == nil {
		errOut(w, 401, "invalid_api_key", "请先登录或提供有效的 API 密钥")
		return
	}
	// 秒败风暴断路器：该密钥连续快速上游失败已触发冷却 → 429 拦截（保护上游配额，逼迫客户端退避）
	if !stormCheck(actx.KeyHash) {
		a.failRequest0(actx.UserID, actx.KeyHash, req.Model, req.Stream, "storm_cooldown", 429)
		errOut(w, 429, "rate_limited", "请求频率过高：你的客户端在连续快速失败后仍在重试，已临时限流 60 秒——请为程序增加失败退避（如指数重试）后再试")
		return
	}
	if line != nil && line.Mode == "official" {
		// tlk 官方中转线（线前缀直连）：仅官方中转分组密钥可调（独占高速模型，官方原价 6 折计费）
		kg := actx.KeyGrp
		if kg == "" {
			kg = a.Cfg.Billing.DefaultGrp
		}
		if kg != "official" {
			errOut(w, 403, "official_line_restricted", "tlk/ 官方中转模型仅限官方中转分组密钥调用——请在控制台创建或切换为「官方中转」分组密钥")
			return
		}
	}
	if line != nil && line.Mode == "crowd" {
		// acu/ 众筹专线：所有分组密钥可调（含纯免费），计费走众筹池（不碰个人余额）
		// 闸门 = 池子有余额 + 用户当日配额未超；池子归零即熔断，充值即复活
		if sc, code, msg := a.poolGate(actx.UserID); sc != 0 {
			errOut(w, sc, code, msg)
			return
		}
		// 防刷：每用户在途并发上限（失败请求不占配额，无并发闸会让单用户无限打上游）
		rel, ok := guard.crowdEnter(actx.UserID)
		if !ok {
			a.failRequest0(actx.UserID, actx.KeyHash, req.Model, req.Stream, "crowd_busy", 429)
			errOut(w, 429, "crowd_busy", "当前调用过于频繁（众筹模型每用户同时最多 3 路请求），请等待在途请求完成后再试")
			return
		}
		defer rel()
	}
	if unified {
		// 统一前缀：按密钥计费分组选线；未分组旧密钥按配置默认分组
		grp := actx.KeyGrp
		if grp == "" {
			grp = a.Cfg.Billing.DefaultGrp
		}
		if grp == "free" {
			// 纯免费分组密钥：收费模型全部拦截（免费模型走裸名不经此分支）
			errOut(w, 403, "free_grp_restricted", "当前密钥为纯免费分组，仅可调用免费模型（不带 aqua/ 前缀的裸模型名）；收费模型请在控制台将该密钥切换为按次/按量分组")
			return
		}
		if grp == "official" {
			// 官方中转分组密钥：官方中转通道已并入众筹池（tlk 线下架），引导直调众筹模型
			errOut(w, 403, "official_grp_scope", "官方中转通道已并入众筹池：任意密钥可直接调用众筹模型（acu/ 前缀，如 acu/glm-5.3，按次扣站点额度）；按量模型请使用按量分组密钥")
			return
		}
		line = a.lineForModel(grp, siteID) // 模型感知：分组默认线无此模型时在同模式其他线定位（如 gpt 线）
		if line == nil {
			if grp == "per_call" {
				// 按次线下架并入众筹池（admin_lines.enabled=0）：引导众筹池/按量分组
				errOut(w, 403, "per_call_suspended", "按次计费已并入众筹池：任意密钥可直接调用众筹模型（acu/ 前缀，按次扣站点额度）；按量模型请切换「按量计费」分组（免费模型不受影响）")
				return
			}
			errOut(w, 503, "service_unavailable", "未配置 "+grp+" 计费线，请联系站长")
			return
		}
	}
	var model *config.Model
	for i := range line.Models {
		if line.Models[i].SiteID == siteID {
			model = &line.Models[i]
			break
		}
	}
	if model == nil {
		if unified {
			// 统一前缀分组路由下目标线没有该模型：明确告知换分组（按次/按量）可见
			errOut(w, 404, "model_not_found", "模型不存在："+req.Model+"（该模型不在当前密钥计费分组的可用列表，按量分组模型更全，可在控制台创建按量分组密钥）")
			return
		}
		errOut(w, 404, "model_not_found", "模型不存在："+req.Model)
		return
	}
	fullID := config.ModelFullName(line.ID, siteID)

	// 上游模型名替换
	upBody, err := replaceModel(body, model.UpstreamID)
	if err != nil {
		errOut(w, 500, "internal_error", "请求处理失败")
		return
	}

	// 伪流式（20260916 站长指令）：上游非流式链路快且稳（免 300s 流式硬切/首帧前断流），
	// 客户端要流式时向上游发非流式请求，完整返回后转换为标准 OpenAI SSE 帧序列——
	// 对客户端完全透明。codex 协议线（RT→AT 转换依赖上游 SSE 事件流）不适用；
	// settings fake_stream=1 总开关 + fake_stream_models 裸名白名单
	// （20260917 站长指令：仅 V4 Flash / V4 Pro 上游非流式，其余模型真流式透传）。
	fake := false
	includeUsage := false
	if req.Stream && line.AuthStyle != "codex" && a.fakeStreamFor(req.Model) {
		fake = true
		includeUsage = req.StreamOptions != nil && req.StreamOptions.IncludeUsage
		if upBody, err = forceNonStream(upBody); err != nil {
			errOut(w, 500, "internal_error", "请求处理失败")
			return
		}
	}

	// 价格组与生效价目（pricing 键 = 目标线全名，与密钥分组解耦）
	grp := billing.UserPriceGrp(a.DB.DB, actx.UserID, line.Mode)
	pricing, err := billing.CurrentPricing(a.DB.DB, fullID, grp)
	if err != nil {
		errOut(w, 500, "internal_error", "计费查询失败")
		return
	}
	if pricing == nil {
		errOut(w, 404, "model_not_found", "该模型已下架或暂不可用")
		return
	}

	// 请求落库（拿 rowid 供面值回写；resolved_line 记实际线，统计口径）
	rid := a.insertRequestLine(actx.UserID, actx.KeyHash, "chat", req.Model, req.Stream, line.ID)
	if rid == 0 {
		errOut(w, 500, "internal_error", "请求记录失败")
		return
	}

	// 预扣额：per_call = 单价；per_token = 输入估算 + max_tokens×输出价（保守上限），且 ≥ floor。
	// crowd（acu/ 众筹池）不预扣个人余额：池子共享钱包，请求前已过 poolGate 闸门
	prehold := int64(0)
	if line.Mode != "crowd" {
		prehold = a.preholdAmount(model, pricing, upBody)

		if err := billing.Prehold(a.DB.DB, actx.UserID, prehold, rid); err != nil {
			if errors.Is(err, billing.ErrInsufficientBalance) {
				a.failRequest(rid, "insufficient_quota", 429) // 诊断 D2：Prehold 失败路径必须回写，不留 (empty) 盲区
				errOut(w, 429, "insufficient_quota", "余额不足：使用收费模型须保持账户 0 元以上余额，请先到控制台充值（先付后用，绝不透支）")
				return
			}
			a.failRequest(rid, "prehold_error", 500)
			errOut(w, 500, "internal_error", "预扣失败")
			return
		}
	}

	// 上游转发
	// 预算：非流式 300s；流式 600s（长文生成合理时长——旧口径 300s 会把仍在正常
	// 生成的长响应硬切断，上游侧正常完成计费，本站却表现为 stream_incomplete）
	client, cerr := a.clientFor(line.ID)
	if cerr != nil {
		// 线刚被热重载停用/删除：预扣全额退回，503 快速失败（旧实现此处 panic）
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "line_removed")
		a.failRequest(rid, "line_removed", 503)
		errOut(w, 503, "service_unavailable", "线路配置刚刚更新，请稍后重试")
		return
	}
	budget := 300 * time.Second
	if req.Stream {
		budget = 600 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	if fake {
		// 伪流式：网关内自管拨号/重拨/转帧/结算（上游非流式），不走下方真流式管道
		a.serveFakeStreamChat(w, r, ctx, actx.UserID, prehold, rid, client, line, model, start, req, upBody, includeUsage, actx.KeyHash)
		return
	}
	resp, key, err := client.DoKey(ctx, upBody, req.Stream, "/chat/completions", model.KeyIdx)
	if err != nil {
		log.Printf("[chat] 上游失败 model=%s line=%s err=%v", req.Model, line.ID, err)
		if ctxDone(r.Context()) || errors.Is(err, context.Canceled) {
			// 客户端在等待上游响应期间主动断开（刷新/取消/SDK 超时）：与模型健康无关，
			// 记 client_cancel（499），状态口径剔除，不再误判为模型故障
			a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "client_cancel")
			a.failRequest(rid, "client_cancel", 499)
			return
		}
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_error")
		stormFail(actx.KeyHash)
		a.failRequest(rid, "upstream_error", 502)
		errOut(w, 502, "upstream_error", "线路繁忙：已自动换线重试仍失败，请稍后重试")
		return
	}
	defer resp.Body.Close()
	stormReset(actx.KeyHash) // 上游拨号成功：清零秒败计数
	if key != nil {
		a.setRequestKeyIdx(rid, key.Idx) // codex 账号粒度记账：记实际使用的钥池序（换号后以最终为准）
	}
	a.recordCodexUsage(line, key, resp) // 官方实时用量头落库（429 满额头也有价值）

	if resp.StatusCode != 200 {
		eb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_"+fmt.Sprint(resp.StatusCode))
		a.failRequest(rid, "upstream_status", resp.StatusCode)
		// 信息隔离：上游报错转译为站点标准错误码，不透传原文
		upstreamErrOut(w, resp.StatusCode, eb)
		return
	}

	if req.Stream {
		// 首帧探测 + 断流换钥重试：上游 200 后、首字节前瞬断（排队失败/渠道抖动）时，
		// 客户端尚未收到任何内容 → 透明换钥重试，用户无感；首帧后断流属生成中断，无法重试
		for probe := 0; probe < 3; probe++ {
			br := bufio.NewReader(resp.Body)
			if _, perr := br.Peek(1); perr == nil {
				resp.Body = readerBody{Reader: br, Closer: resp.Body}
				break
			}
			resp.Body.Close()
			if probe == 2 {
				log.Printf("[chat] 首帧前断流 model=%s line=%s，重试耗尽", req.Model, line.ID)
				a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_error")
				stormFail(actx.KeyHash)
				a.failRequest(rid, "upstream_error", 502)
				errOut(w, 502, "upstream_error", "线路繁忙：已自动换线重试仍失败，请稍后重试")
				return
			}
			log.Printf("[chat] 首帧前断流 model=%s line=%s，换钥重试(%d/2)", req.Model, line.ID, probe+1)
			client.Pool.Advance()
			time.Sleep(300 * time.Millisecond)
			resp, key, err = client.DoKey(ctx, upBody, true, "/chat/completions", model.KeyIdx)
			if err != nil {
				log.Printf("[chat] 重试仍失败 model=%s line=%s err=%v", req.Model, line.ID, err)
				if ctxDone(r.Context()) || errors.Is(err, context.Canceled) {
					// 客户端在重试等待期间主动断开：与模型健康无关
					a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "client_cancel")
					a.failRequest(rid, "client_cancel", 499)
					return
				}
				a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_error")
				stormFail(actx.KeyHash)
				a.failRequest(rid, "upstream_error", 502)
				errOut(w, 502, "upstream_error", "线路繁忙：已自动换线重试仍失败，请稍后重试")
				return
			}
			if key != nil {
				a.setRequestKeyIdx(rid, key.Idx)
				a.recordCodexUsage(line, key, resp)
			}
			if resp.StatusCode != 200 {
				eb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
				_ = resp.Body.Close()
				a.settleFor(line, actx.UserID, prehold, 0, rid, 0, "upstream_"+fmt.Sprint(resp.StatusCode))
				a.failRequest(rid, "upstream_status", resp.StatusCode)
				upstreamErrOut(w, resp.StatusCode, eb)
				return
			}
		}
		a.serveStreamChat(w, r, resp, actx.UserID, prehold, rid, client, key, line, model, start)
		return
	}
	a.serveJSONChat(w, resp, actx.UserID, prehold, rid, client, key, line, model, start)
}

// ---------------------------------------------------------------------------
// 伪流式：上游非流式 → 客户端流式（20260916）
// ---------------------------------------------------------------------------

// fakeStreamOn 全站伪流式总开关（settings 表 fake_stream=1；默认关。codex 协议线代码级排除）
func (a *App) fakeStreamOn() bool { return a.settingsGet("fake_stream") == "1" }

// fakeStreamFor 模型级伪流式判定（20260917 站长指令：仅 V4 Flash / V4 Pro 上游非流式，
// 其余模型流式正常透传）。总开关 fake_stream=1 不变；fake_stream_models=逗号分隔裸名白名单，
// 空 = 全部模型（兼容 20260916 全站行为）。匹配按去线前缀（aqua/ acu/ 等）后的裸名精确比对。
func (a *App) fakeStreamFor(siteModel string) bool {
	if !a.fakeStreamOn() {
		return false
	}
	list := strings.TrimSpace(a.settingsGet("fake_stream_models"))
	if list == "" {
		return true
	}
	m := strings.ToLower(strings.TrimSpace(siteModel))
	if i := strings.IndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	for _, s := range strings.Split(list, ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" && m == s {
			return true
		}
	}
	return false
}

// forceNonStream 伪流式前置：请求体 stream 置 false 并剥除 stream_options
// （部分上游在 stream=false 时携带 stream_options 会拒绝请求）
func forceNonStream(body []byte) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m["stream"] = []byte("false")
	delete(m, "stream_options")
	return json.Marshal(m)
}

// serveFakeStreamChat 伪流式主路径：向上游发非流式请求，完整响应到手后转换为
// 标准 OpenAI chat.completion.chunk SSE 流回放给客户端（首帧 role → reasoning →
// content 分片 → tool_calls → finish_reason → [可选 usage 帧] → [DONE]）。
// 稳定性：拨号失败/畸形响应自动换钥重拨一次（Do 内部已含同钥退避+换钥重试与
// 渠道级快速失败，此处只兜 200-但-内容畸形的尾部的尾部）；客户端在等待期断开记
// client_cancel（499），与模型健康无关；计费与真流式同口径（usage actual 优先，
// 缺失按保底/单价，面值台账照记）。首字（first_ms）= 完整响应到手时刻——伪流式下
// 用户真实等待时长，如实记录。
func (a *App) serveFakeStreamChat(w http.ResponseWriter, r *http.Request, ctx context.Context, uid, prehold, rid int64, c *upstream.Client, line *config.Line, m *config.Model, start time.Time, req chatReq, upBody []byte, includeUsage bool, keyHash string) {
	var raw []byte
	var key *upstream.KeyState
	for attempt := 0; ; attempt++ {
		rp, k, derr := c.DoKey(ctx, upBody, false, "/chat/completions", m.KeyIdx)
		if derr != nil {
			log.Printf("[chat] 伪流式上游失败 model=%s line=%s err=%v", req.Model, line.ID, derr)
			if ctxDone(r.Context()) || errors.Is(derr, context.Canceled) {
				// 客户端在等待上游响应期间主动断开：与模型健康无关
				a.settleFor(line, uid, prehold, 0, rid, 0, "client_cancel")
				a.failRequest(rid, "client_cancel", 499)
				return
			}
			a.settleFor(line, uid, prehold, 0, rid, 0, "upstream_error")
			stormFail(keyHash)
			a.failRequest(rid, "upstream_error", 502)
			errOut(w, 502, "upstream_error", "线路繁忙：已自动换线重试仍失败，请稍后重试")
			return
		}
		stormReset(keyHash) // 上游拨号成功：清零秒败计数
		if k != nil {
			key = k
			a.setRequestKeyIdx(rid, k.Idx)
		}
		if rp.StatusCode != 200 {
			// 非最终 200（Do 已完成同钥退避/换钥/渠道级快速失败）：错误转译透传
			eb, _ := io.ReadAll(io.LimitReader(rp.Body, 64<<10))
			_ = rp.Body.Close()
			a.settleFor(line, uid, prehold, 0, rid, 0, "upstream_"+fmt.Sprint(rp.StatusCode))
			a.failRequest(rid, "upstream_status", rp.StatusCode)
			upstreamErrOut(w, rp.StatusCode, eb)
			return
		}
		rw, rerr := io.ReadAll(io.LimitReader(rp.Body, 32<<20))
		_ = rp.Body.Close()
		if rerr == nil && fakeParse(rw) {
			raw = rw
			break
		}
		// 畸形响应（200 但非合法 JSON / 缺 choices）：换钥重拨一次
		if attempt == 0 && !ctxDone(r.Context()) {
			log.Printf("[chat] 伪流式响应畸形 model=%s line=%s，换钥重拨", req.Model, line.ID)
			c.Pool.Advance()
			select {
			case <-ctx.Done():
			case <-time.After(300 * time.Millisecond):
			}
			continue
		}
		a.settleFor(line, uid, prehold, 0, rid, 0, "upstream_malformed")
		a.failRequest(rid, "upstream_malformed", 502)
		errOut(w, 502, "upstream_error", "上游返回了格式异常的响应，请稍后重试")
		return
	}

	// —— 解析完整响应 ——
	var jr struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role             string           `json:"role"`
				Content          json.RawMessage  `json:"content"`
				ReasoningContent string           `json:"reasoning_content"`
				ToolCalls        []map[string]any `json:"tool_calls"`
			} `json:"message"`
			FinishReason json.RawMessage `json:"finish_reason"`
		} `json:"choices"`
		Usage *usageJSON `json:"usage"`
	}
	_ = json.Unmarshal(raw, &jr)

	// —— 计费（与 serveJSONChat 同口径）——
	u := usageFromJSON(jr.Usage)
	p, _ := billing.CurrentPricing(a.DB.DB, line.ID+"/"+m.SiteID, billing.UserPriceGrp(a.DB.DB, uid, line.Mode))
	final, face := int64(0), int64(0)
	src := "estimated"
	if jr.Usage != nil {
		src = "actual"
		if p != nil && p.Mode == "per_call" {
			final = p.PriceMicro
		} else {
			final = billing.MeterTokens(u, p)
		}
		face = billing.FaceCostMicro(u, m.InCostRate10, m.CacheCostRate10, m.OutCostRate10)
	} else if p != nil {
		if p.Mode == "per_call" {
			final = p.PriceMicro
		} else {
			final = p.FloorMicro
		}
	}
	a.settleFor(line, uid, prehold, final, rid, final, "billed")
	if face > 0 && key != nil {
		c.Pool.ReportFace(key, face)
		_ = billing.LineKeyReport(a.DB.DB, line.ID, key.Idx, key.InitialMicro, face, rid)
	}
	waitMs := time.Since(start).Milliseconds()
	a.okRequestGen(rid, u, final, face, src, waitMs, 200, waitMs, waitMs)

	// —— 转换为 SSE 帧序列 ——
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)

	id := jr.ID
	if id == "" {
		id = fmt.Sprintf("chatcmpl-fake-%d", rid)
	}
	created := jr.Created
	if created == 0 {
		created = time.Now().Unix()
	}
	mdl := jr.Model
	if mdl == "" {
		mdl = req.Model
	}
	chunk := func(delta map[string]any, finish any, usage map[string]any) {
		frame := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": mdl,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		if usage != nil {
			frame["usage"] = usage
			frame["choices"] = []any{} // OpenAI 口径：usage 终帧 choices 为空数组
		}
		b, _ := json.Marshal(frame)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	var msg *struct {
		Role             string           `json:"role"`
		Content          json.RawMessage  `json:"content"`
		ReasoningContent string           `json:"reasoning_content"`
		ToolCalls        []map[string]any `json:"tool_calls"`
	}
	if len(jr.Choices) > 0 {
		msg = &jr.Choices[0].Message
	}
	chunk(map[string]any{"role": "assistant", "content": ""}, nil, nil) // 首帧 role（SDK 兼容基线）
	if msg != nil {
		if rc := msg.ReasoningContent; rc != "" {
			for _, s := range chunkText(rc, 48) {
				chunk(map[string]any{"reasoning_content": s}, nil, nil)
			}
		}
		if content := fakeContentText(msg.Content); content != "" {
			for _, s := range chunkText(content, 48) {
				chunk(map[string]any{"content": s}, nil, nil)
			}
		}
		if len(msg.ToolCalls) > 0 {
			// 完整 tool_calls 单帧下发（补 index 供客户端按位聚合）
			for i := range msg.ToolCalls {
				msg.ToolCalls[i]["index"] = i
			}
			chunk(map[string]any{"tool_calls": msg.ToolCalls}, nil, nil)
		}
	}
	finish := any(nil)
	if len(jr.Choices) > 0 {
		var fr string
		if json.Unmarshal(jr.Choices[0].FinishReason, &fr) == nil && fr != "" {
			finish = fr
		}
	}
	chunk(map[string]any{}, finish, nil)
	if includeUsage {
		uf := map[string]any{
			"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens,
			"total_tokens": u.PromptTokens + u.CompletionTokens,
		}
		if u.CachedTokens > 0 {
			uf["prompt_tokens_details"] = map[string]any{"cached_tokens": u.CachedTokens}
		}
		chunk(nil, nil, uf)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// fakeParse 伪流式形状探测：合法 JSON 且带 choices（兼容上游错误体带 error 字段的场景交由上层转译，这里只认 200+choices）
func fakeParse(raw []byte) bool {
	var probe struct {
		Choices []json.RawMessage `json:"choices"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return false
	}
	return len(probe.Choices) > 0
}

// fakeContentText message.content 归一化为文本（string | null | [{type:text,text}] 数组）
func fakeContentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, pt := range parts {
			b.WriteString(pt.Text)
		}
		return b.String()
	}
	return ""
}

// chunkText 按 rune 分片（伪流式回放粒度，48 字符/帧——渐进可见且帧数可控）
func chunkText(s string, n int) []string {
	rs := []rune(s)
	if len(rs) <= n {
		return []string{s}
	}
	out := []string{}
	for i := 0; i < len(rs); i += n {
		e := i + n
		if e > len(rs) {
			e = len(rs)
		}
		out = append(out, string(rs[i:e]))
	}
	return out
}

// serveJSONChat 非流式：读全量 → 校验形状 → 剥层 → 结算 → 回写
func (a *App) serveJSONChat(w http.ResponseWriter, resp *http.Response, uid, prehold, rid int64, c *upstream.Client, key *upstream.KeyState, line *config.Line, m *config.Model, start time.Time) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		a.settleFor(line, uid, prehold, 0, rid, 0, "read_error")
		a.failRequest(rid, "read_error", 502)
		errOut(w, 502, "upstream_error", "上游响应读取失败")
		return
	}
	// OpenAI 形状校验：上游 200 但响应不是合法 JSON 或缺 choices →
	// 视为上游畸形响应（透传会让客户端 SDK 报"格式错误"），转为 502 并全额退款
	var probe struct {
		Choices []json.RawMessage `json:"choices"`
		Usage   *usageJSON        `json:"usage"`
		Error   json.RawMessage   `json:"error"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || (len(probe.Choices) == 0 && len(probe.Error) == 0) {
		a.settleFor(line, uid, prehold, 0, rid, 0, "upstream_malformed")
		a.failRequest(rid, "upstream_malformed", 502)
		errOut(w, 502, "upstream_error", "上游返回了格式异常的响应，请稍后重试")
		return
	}
	u := usageFromJSON(probe.Usage)
	final := int64(0)
	face := int64(0)
	if probe.Usage != nil {
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
	a.settleFor(line, uid, prehold, final, rid, final, "billed")
	if face > 0 && key != nil {
		c.Pool.ReportFace(key, face)
		_ = billing.LineKeyReport(a.DB.DB, line.ID, key.Idx, key.InitialMicro, face, rid)
	}
	src := "estimated"
	if probe.Usage != nil {
		src = "actual"
	}
	a.okRequest(rid, u, final, face, src, time.Since(start).Milliseconds(), 200)
	// 剥层后透传（信息隔离：移除成本/追踪类字段）
	out := stripSensitive(raw)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(out)
}

// serveStreamChat SSE 流式：逐行转发 + 首尾事件计费
func (a *App) serveStreamChat(w http.ResponseWriter, r *http.Request, resp *http.Response, uid, prehold, rid int64, c *upstream.Client, key *upstream.KeyState, line *config.Line, m *config.Model, start time.Time) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.settleFor(line, uid, prehold, 0, rid, 0, "no_flusher")
		a.failRequest(rid, "no_flusher", 500)
		errOut(w, 500, "internal_error", "流式不可用")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)

	var u billing.Usage
	var firstByte time.Time // 首帧到达时间：tps 按生成阶段（首字之后）计，等待不计入生成速度
	final := int64(0)
	faceTotal := int64(0)
	settled := false
	sawDone := false     // 上游是否已发 [DONE]（未发即中断 → 客户端拿到的是截断流）
	interrupted := false // 上游异常中断（区别于客户端主动断开）
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
			a.settleFor(line, uid, prehold, final, rid, final, "billed")
			a.okRequestGen(rid, u, final, faceTotal, "actual", time.Since(start).Milliseconds(), 200, genMs(firstByte), ttftOf(firstByte, start))
		} else if interrupted {
			// 一帧有效内容都没有且上游异常中断：全额退回
			a.settleFor(line, uid, prehold, 0, rid, 0, "stream_incomplete")
			a.failRequest(rid, "stream_incomplete", 502)
		} else if p != nil {
			// 上游正常收尾但未发 usage：按保底/单价收（与原口径一致，防薅羊毛）
			if p.Mode == "per_call" {
				final = p.PriceMicro
			} else {
				final = p.FloorMicro
			}
			a.settleFor(line, uid, prehold, final, rid, final, "billed")
			a.okRequestGen(rid, u, final, faceTotal, "estimated", time.Since(start).Milliseconds(), 200, genMs(firstByte), ttftOf(firstByte, start))
		} else {
			a.settleFor(line, uid, prehold, 0, rid, 0, "stream_incomplete")
			a.failRequest(rid, "stream_incomplete", 502)
		}
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
			if firstByte.IsZero() {
				firstByte = time.Now()
			}
			buf = append(buf, tmp[:n]...)
			// 按 SSE 行处理：完整行才转发（便于剥层与 usage 抓取）
			for {
				i := bytes.IndexByte(buf, '\n')
				if i < 0 {
					break
				}
				lineBytes := buf[:i]
				buf = buf[i+1:]
				if bytes.Contains(lineBytes, []byte("[DONE]")) {
					sawDone = true
				}
				out := sanitizeStreamLine(lineBytes, &u)
				_, _ = w.Write(out)
				_, _ = w.Write([]byte("\n"))
				flusher.Flush()
			}
		}
		if err != nil {
			// 上游中断且从未发过 [DONE]：补发一帧 OpenAI 错误事件再正常收尾，
			// 客户端 SDK 才能感知截断（否则表现为静默缺内容）
			if !sawDone && !ctxDone(r.Context()) {
				interrupted = true
				errFrame := map[string]any{"error": map[string]any{
					"message": "上游流式响应中断，内容可能不完整，请重试",
					"type":    "api_error", "code": "stream_incomplete", "param": nil,
				}}
				eb, _ := json.Marshal(errFrame)
				_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", eb)
				flusher.Flush()
			}
			return
		}
	}
}

// ctxDone 请求上下文是否已取消（客户端断开/超时：无需再写响应）
func ctxDone(ctx context.Context) bool {
	return ctx.Err() != nil
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

// usageFromJSON JSON usage → billing.Usage（缓存命中兼容三种字段：OpenAI 标准结构 /
// DeepSeek 旧口径 prompt_cache_hit_tokens / Anthropic 口径 cache_read_input_tokens）
func usageFromJSON(j *usageJSON) billing.Usage {
	if j == nil {
		return billing.Usage{}
	}
	cached := int64(0)
	if j.PromptTokensDetails != nil {
		cached = j.PromptTokensDetails.CachedTokens
	}
	if cached == 0 {
		cached = j.PromptCacheHitTokens
	}
	if cached == 0 {
		cached = j.CacheReadInputTokens
	}
	if cached > j.PromptTokens {
		cached = j.PromptTokens // 防御：缓存命中不可能超过 prompt 总量
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

// preholdAmount 预扣额（one-api/new-api 同款门槛哲学：预扣只做防白嫖门槛，超支由 Settle 补扣兜底）：
// per_call=单次价；per_token=输入估算+max_tokens×输出价，≥floor。
// maxOut 未传默认 4096（保持存量体验，实际输出超出部分由结算补扣收回）；上限 100000 对齐 new-api maxTokensLimit 防溢出
func (a *App) preholdAmount(m *config.Model, p *billing.PricingInfo, body []byte) int64 {
	if p == nil {
		return 1000
	}
	if p.Mode == "per_call" {
		return p.PriceMicro
	}
	// 输入估算：消息体长度/4 × 1.2 保守余量（与 Rust 版口径一致）
	var req struct {
		Messages  json.RawMessage `json:"messages"`
		MaxTokens int64           `json:"max_tokens"`
	}
	_ = json.Unmarshal(body, &req)
	est := int64(float64(len(req.Messages))/4*1.2) + 16
	maxOut := req.MaxTokens
	if maxOut <= 0 {
		maxOut = 4096
	}
	if maxOut > 100000 {
		maxOut = 100000
	}
	estCost := est*p.InRate10/10000 + maxOut*p.OutRate10/10000
	if estCost < p.FloorMicro {
		estCost = p.FloorMicro
	}
	return estCost
}

// insertRequest 请求落库，返回 rowid
func (a *App) insertRequest(uid int64, keyHash, endpoint, model string, stream bool) int64 {
	return a.insertRequestLine(uid, keyHash, endpoint, model, stream, "")
}

// insertRequestLine 请求落库（resolved_line 记实际计费线，统一前缀路由统计口径）
func (a *App) insertRequestLine(uid int64, keyHash, endpoint, model string, stream bool, resolvedLine string) int64 {
	res, err := a.DB.Exec(
		"INSERT INTO requests (key_hash, endpoint, model, user_id, ok, ts, bill_state, stream_mode, resolved_line) VALUES (?,?,?,?,0,?,?,?,?)",
		keyHash, endpoint, model, uid, time.Now().Unix(), "", boolToInt(stream), resolvedLine)
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

// setRequestKeyIdx 回写实际使用的钥池序（codex 账号粒度用量/利润记账，换号后以最终为准）
func (a *App) setRequestKeyIdx(rid int64, idx int) {
	if rid == 0 || idx < 0 {
		return
	}
	_, _ = a.DB.Exec("UPDATE requests SET key_idx=? WHERE rowid=?", idx, rid)
}

// okRequest 成功回写：usage + 金额 + 面值成本 + bill_state='billed'
// src=usage 来源口径（actual=上游实发 / estimated=保底估算）；latMs/sc 回写观测口径，
// tps=输出 tokens/秒（生成速度，非流式为总耗时口径）；消除生产旧表列默认值造成的统计失真
func (a *App) okRequest(rid int64, u billing.Usage, amount int64, face int64, src string, latMs int64, sc int) {
	a.okRequestGen(rid, u, amount, face, src, latMs, sc, latMs, 0)
}

// okRequestGen 成功回写（生成阶段口径）：genMs=生成阶段耗时（流式为首字之后；≤0 时回退总耗时）
// tps 按 genMs 计——等待（建连/prompt 处理/首字）不计入生成速度，latency_ms 仍按总耗时口径，
// ttftMs=首字延迟（请求起点→首帧到达；用户感知的"响应速度"，≤0 记 0，状态卡优先展示它）
func (a *App) okRequestGen(rid int64, u billing.Usage, amount int64, face int64, src string, latMs int64, sc int, genMs int64, ttftMs int64) {
	tpsMs := genMs
	if tpsMs <= 0 {
		tpsMs = latMs
	}
	tps := 0.0
	if tpsMs > 0 {
		tps = float64(u.CompletionTokens) * 1000 / float64(tpsMs)
	}
	_, _ = a.DB.Exec(
		"UPDATE requests SET ok=1, prompt_tokens=?, completion_tokens=?, cached_tokens=?, total_tokens=?, billed=1, bill_amount_micro=?, unit_price_micro=?, face_cost_micro=?, bill_state='billed', usage_source=?, latency_ms=?, status_code=?, tps=?, first_ms=? WHERE rowid=?",
		u.PromptTokens, u.CompletionTokens, u.CachedTokens, u.PromptTokens+u.CompletionTokens, amount, amount, face, src, latMs, sc, tps, ttftMs, rid)
	// 邀请返利：异步队列结算（热路径零阻塞；acu/crowd 等范围过滤在结算器内，invite.go）
	a.inviteEnqueue(rid)
}

// genMs 生成阶段耗时（毫秒）：首帧未到达（无有效输出）返回 0，由调用方回退总耗时
func genMs(firstByte time.Time) int64 {
	if firstByte.IsZero() {
		return 0
	}
	return time.Since(firstByte).Milliseconds()
}

// ttftMs 首字延迟（毫秒）：请求起点→首帧到达；首帧未到达返回 0（非流式/无输出）
func ttftOf(firstByte, start time.Time) int64 {
	if firstByte.IsZero() {
		return 0
	}
	return firstByte.Sub(start).Milliseconds()
}

// failRequest 失败回写（诊断 D2：reason 必填区分失败原因，status_code 回写真实状态码供统计）
func (a *App) failRequest(rid int64, reason string, statusCode int) {
	_, _ = a.DB.Exec("UPDATE requests SET ok=0, error=?, status_code=?, bill_state='refunded' WHERE rowid=?", reason, statusCode, rid)
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// clientFor 线客户端缓存（每线一个 KeyPool）。
// 线已被热重载停用/删除（调用方持有旧快照）时返回错误：调用方应 503 快速失败——
// 旧实现拿 nil 线构造客户端会直接 panic（请求连接被重置且上游无任何日志）。
func (a *App) clientFor(lineID string) (*upstream.Client, error) {
	a.clientsMu.Lock()
	defer a.clientsMu.Unlock()
	if a.clients == nil {
		a.clients = map[string]*upstream.Client{}
	}
	if c, ok := a.clients[lineID]; ok {
		return c, nil
	}
	l := a.lineByID(lineID)
	if l == nil {
		return nil, fmt.Errorf("line_removed(%s)", lineID)
	}
	c := upstream.NewClient(l)
	a.clients[lineID] = c
	return c, nil
}

// dbErr 检查（占位：统一错误检查）
func dbErr(err error) bool {
	return err != nil && err != sql.ErrNoRows
}
