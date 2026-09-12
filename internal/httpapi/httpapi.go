// Package httpapi 路由与 handler。net/http 1.22+ 方法路由，零框架依赖。
package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"acu-aqua/gateway/internal/auth"
	"acu-aqua/gateway/internal/billing"
	"acu-aqua/gateway/internal/config"
	"acu-aqua/gateway/internal/upstream"
)

// Routes 路由表
func (a *App) Routes() http.Handler {
	initLegacy(a.Cfg.Server.LegacyUpstreamURL)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", a.handleStatus)
	mux.HandleFunc("GET /v1/meta", a.handleMeta)
	if legacyProxy == nil {
		// 纯 Go 单体模式：模型目录由本网关生成；绞杀者模式下 /v1/models 反代旧网关
		// （旧网关目录含免费模型 + 收费模型 + 活动价状态，与前端展示完全一致）
		mux.HandleFunc("GET /v1/models", a.handleModels)
	}
	mux.HandleFunc("GET /v1/models/{id}", a.handleModelDetail)
	mux.HandleFunc("GET /v1/models/{id}/{rest...}", a.handleModelDetail)
	mux.HandleFunc("POST /v1/chat/completions", a.handleChat)
	mux.HandleFunc("POST /v1/images/generations", a.handleImages)
	mux.HandleFunc("GET /v1/images/file/{id}", a.handleImageFile)

	// 扩展能力端点（embeddings / rerank / moderations / audio / videos）
	mux.HandleFunc("POST /v1/embeddings", a.handleEmbeddings)
	mux.HandleFunc("POST /v1/rerank", a.handleRerank)
	mux.HandleFunc("POST /v1/moderations", a.handleModerations)
	mux.HandleFunc("POST /v1/audio/speech", a.handleAudioSpeech)
	mux.HandleFunc("POST /v1/audio/transcriptions", a.handleAudioTranscriptions)
	mux.HandleFunc("POST /v1/videos/generations", a.handleVideos)

	// 公开数据端点
	mux.HandleFunc("GET /v1/stats", a.handleStats)

	// 竞技场（盲测对决 / 投票 / 排行榜）
	mux.HandleFunc("POST /v1/arena", a.handleArena)
	mux.HandleFunc("POST /v1/arena/vote", a.handleVote)
	mux.HandleFunc("GET /v1/arena/leaderboard", a.handleLeaderboard)

	// 工具箱（树洞 / 提示词工坊 / 站内 AI 工具通道 / 本地工具 / 短链 / Webhook）
	mux.HandleFunc("POST /v1/tools/treehole", a.handleTreehole)
	mux.HandleFunc("GET /v1/tools/treehole/prompt", a.handleTreeholePrompt)
	mux.HandleFunc("GET /v1/tools/prompts", a.handlePrompts)
	mux.HandleFunc("POST /v1/tools/chat", a.handleToolsChat)
	mux.HandleFunc("POST /v1/tools/translate", a.handleTranslate)
	mux.HandleFunc("POST /v1/tools/url-summary", a.handleURLSummary)
	mux.HandleFunc("POST /v1/tools/text-stats", a.handleToolTextStats)
	mux.HandleFunc("POST /v1/tools/token-count", a.handleToolTokenCount)
	mux.HandleFunc("POST /v1/tools/hash", a.handleToolHash)
	mux.HandleFunc("POST /v1/tools/password", a.handleToolPassword)
	mux.HandleFunc("POST /v1/tools/subnet", a.handleToolSubnet)
	mux.HandleFunc("POST /v1/tools/json", a.handleToolJSON)
	mux.HandleFunc("POST /v1/tools/regex", a.handleToolRegex)
	mux.HandleFunc("POST /v1/tools/color", a.handleToolColor)
	mux.HandleFunc("POST /v1/tools/url-code", a.handleToolURLCode)
	mux.HandleFunc("POST /v1/tools/base64", a.handleToolBase64)
	mux.HandleFunc("POST /v1/tools/dice", a.handleToolDice)
	mux.HandleFunc("POST /v1/tools/timestamp", a.handleToolTimestampPost)
	mux.HandleFunc("GET /v1/tools/timestamp", a.handleToolTimestampGet)
	mux.HandleFunc("GET /v1/tools/uuid", a.handleToolUUID)
	mux.HandleFunc("POST /v1/tools/uuid-bulk", a.handleToolUUIDBulk)
	mux.HandleFunc("POST /v1/tools/shorten", a.handleToolShorten)
	mux.HandleFunc("GET /s/{id}", a.handleShortRedirect)
	mux.HandleFunc("POST /v1/tools/webhook", a.handleToolWebhook)
	mux.HandleFunc("/hook/{id}", a.handleHookCollect)

	// IP 定位
	mux.HandleFunc("POST /v1/ip_location", a.handleIPLocation)

	// 用户认证（前端 SPA 契约路径 /v1/auth/*）
	mux.HandleFunc("POST /v1/auth/login", a.authLogin)
	mux.HandleFunc("POST /v1/auth/logout", a.authLogout)
	mux.HandleFunc("GET /v1/auth/me", a.authMe)
	mux.HandleFunc("POST /v1/auth/register", a.authRegister)
	mux.HandleFunc("POST /v1/auth/send-code", a.authSendCode)
	mux.HandleFunc("POST /v1/auth/forgot", a.authForgot)
	mux.HandleFunc("POST /v1/auth/reset", a.authReset)
	mux.HandleFunc("POST /v1/auth/password", a.authPassword)
	mux.HandleFunc("POST /v1/auth/profile", a.authProfile)
	mux.HandleFunc("GET /v1/auth/avatar/{id}", a.authAvatar)

	// 用户控制台数据（/v1/my/*）
	mux.HandleFunc("GET /v1/my/balance", a.myBalance)
	mux.HandleFunc("GET /v1/my/balance-alert", a.myBalanceAlertGet)
	mux.HandleFunc("POST /v1/my/balance-alert", a.myBalanceAlertSet)
	mux.HandleFunc("GET /v1/my/keys", a.myKeysGet)
	mux.HandleFunc("POST /v1/my/keys", a.myKeysCreate)
	mux.HandleFunc("DELETE /v1/my/keys/{id}", a.myKeysDelete)
	mux.HandleFunc("GET /v1/my/keys/{id}/reveal", a.myKeysReveal)
	mux.HandleFunc("GET /v1/my/usage", a.myUsage)
	mux.HandleFunc("GET /v1/my/history", a.myHistory)
	mux.HandleFunc("GET /v1/my/billing", a.myBilling)
	mux.HandleFunc("GET /v1/my/checkup", a.myCheckup)
	mux.HandleFunc("POST /v1/my/avatar", a.authAvatarUpload)

	// 充值（易支付）
	mux.HandleFunc("GET /v1/pay/orders", a.payOrders)
	mux.HandleFunc("POST /v1/pay/create", a.payCreate)
	mux.HandleFunc("GET /v1/pay/status", a.payStatus)
	mux.HandleFunc("POST /v1/pay/notify", a.payNotify)
	mux.HandleFunc("GET /v1/pay/notify", a.payNotify)

	// 用户面（Go 过渡期路径 /v1/user/*，保留兼容）
	mux.HandleFunc("POST /v1/user/register", a.handleRegister)
	mux.HandleFunc("POST /v1/user/login", a.handleLogin)
	mux.HandleFunc("POST /v1/user/logout", a.handleLogout)
	mux.HandleFunc("GET /v1/user/me", a.handleMe)
	mux.HandleFunc("GET /v1/user/flows", a.handleFlows)
	mux.HandleFunc("POST /v1/user/keys", a.handleCreateKey)
	mux.HandleFunc("GET /v1/user/keys", a.handleListKeys)
	mux.HandleFunc("DELETE /v1/user/keys/{id}", a.handleRevokeKey)

	// —— 管理后台 /v1/admin/*（Go 原生，响应形状对齐 Rust 版）——
	// 单密码模型 + adm_ 令牌独立会话 + 高危操作二次密码 + SHA-256 哈希链审计
	mux.HandleFunc("POST /v1/admin/login", a.handleAdminLogin)
	mux.HandleFunc("POST /v1/admin/logout", a.handleAdminLogout)
	mux.HandleFunc("GET /v1/admin/stats", a.handleAdminStats)
	mux.HandleFunc("GET /v1/admin/users", a.handleAdminUsers)
	mux.HandleFunc("GET /v1/admin/users/{uid}", func(w http.ResponseWriter, r *http.Request) {
		a.handleAdminUserDetail(w, r, r.PathValue("uid"))
	})
	mux.HandleFunc("POST /v1/admin/users/{uid}/balance", func(w http.ResponseWriter, r *http.Request) {
		a.handleAdminUserBalance(w, r, r.PathValue("uid"))
	})
	mux.HandleFunc("GET /v1/admin/quota", a.handleAdminQuota)
	mux.HandleFunc("POST /v1/admin/quota/topup", a.handleAdminQuotaTopup)
	mux.HandleFunc("POST /v1/admin/quota/circuit", a.handleAdminQuotaCircuit)
	mux.HandleFunc("POST /v1/admin/quota/sync", a.handleAdminQuotaSync)
	mux.HandleFunc("GET /v1/admin/audit", a.handleAdminAudit)
	mux.HandleFunc("GET /v1/admin/reconcile", a.handleAdminReconcile)
	mux.HandleFunc("GET /v1/admin/supervision", a.handleAdminSupervision)
	// —— 上游线路在线管理（DB 事实源 + 热重载；密钥/模型/线路 CRUD）——
	mux.HandleFunc("GET /v1/admin/lines", a.handleAdminLines)
	mux.HandleFunc("POST /v1/admin/lines/reload", a.handleAdminLinesReload)
	mux.HandleFunc("POST /v1/admin/lines", a.handleAdminLineCreate)
	mux.HandleFunc("POST /v1/admin/lines/{line}", a.handleAdminLineUpdate)
	mux.HandleFunc("DELETE /v1/admin/lines/{line}", a.handleAdminLineDelete)
	mux.HandleFunc("GET /v1/admin/lines/{line}/keys", a.handleAdminLineKeys)
	mux.HandleFunc("POST /v1/admin/lines/{line}/keys", a.handleAdminLineKeysAdd)
	mux.HandleFunc("DELETE /v1/admin/lines/{line}/keys/{idx}", a.handleAdminLineKeyDelete)
	mux.HandleFunc("POST /v1/admin/lines/{line}/keys/{idx}/dead", a.handleAdminLineKeyDead)
	mux.HandleFunc("GET /v1/admin/lines/{line}/models", a.handleAdminLineModels)
	mux.HandleFunc("POST /v1/admin/lines/{line}/models", a.handleAdminLineModelUpsert)
	mux.HandleFunc("DELETE /v1/admin/lines/{line}/models/{site}", a.handleAdminLineModelDelete)
	mux.HandleFunc("POST /v1/admin/lines/{line}/models/{site}/degraded", a.handleAdminLineModelDegraded)
	// —— 渠道测试与上游模型拉取（诊断 D5）——
	mux.HandleFunc("POST /v1/admin/lines/test-all", a.handleAdminLinesTestAll)
	mux.HandleFunc("POST /v1/admin/lines/{line}/test", a.handleAdminLineTest)
	mux.HandleFunc("GET /v1/admin/lines/{line}/upstream-models", a.handleAdminLineUpstreamModels)
	// —— 用户管理扩展（封禁/重置密码/价目组/踢下线/密钥管理/软删除）——
	mux.HandleFunc("POST /v1/admin/users/{uid}/status", func(w http.ResponseWriter, r *http.Request) {
		a.handleAdminUserStatus(w, r, r.PathValue("uid"))
	})
	mux.HandleFunc("POST /v1/admin/users/{uid}/password", func(w http.ResponseWriter, r *http.Request) {
		a.handleAdminUserPassword(w, r, r.PathValue("uid"))
	})
	mux.HandleFunc("POST /v1/admin/users/{uid}/price-grp", func(w http.ResponseWriter, r *http.Request) {
		a.handleAdminUserPriceGrp(w, r, r.PathValue("uid"))
	})
	mux.HandleFunc("POST /v1/admin/users/{uid}/kick", func(w http.ResponseWriter, r *http.Request) {
		a.handleAdminUserKick(w, r, r.PathValue("uid"))
	})
	mux.HandleFunc("GET /v1/admin/users/{uid}/keys", func(w http.ResponseWriter, r *http.Request) {
		a.handleAdminUserKeys(w, r, r.PathValue("uid"))
	})
	mux.HandleFunc("POST /v1/admin/users/{uid}/keys/{kid}/revoke", func(w http.ResponseWriter, r *http.Request) {
		a.handleAdminUserKeyRevoke(w, r, r.PathValue("uid"), r.PathValue("kid"))
	})
	mux.HandleFunc("DELETE /v1/admin/users/{uid}", func(w http.ResponseWriter, r *http.Request) {
		a.handleAdminUserDelete(w, r, r.PathValue("uid"))
	})
	// 系统更新（gitee 发行版源；config [update] enabled=true 才开放）
	mux.HandleFunc("GET /v1/admin/update/check", a.handleAdminUpdateCheck)
	mux.HandleFunc("POST /v1/admin/update/apply", a.handleAdminUpdateApply)

	// 其余全部反代旧网关（绞杀者迁移残留；纯 Go 单体模式下未注册路径 404）
	mux.HandleFunc("/", a.handleLegacy)
	return accessLog(corsGate(mux))
}

// corsGate CORS 门卫：
// 1. OPTIONS 预检全局应答（204 + CORS 头）——跨域 SPA 依赖预检放行。
// 2. 全部响应注入 CORS 头（单一来源）。
// ⚠️ 必须走 mux.ServeHTTP 而非 mux.Handler(r)+手动调用：后者绕过 Go 1.22
//    ServeMux 的路径参数注入（r.matches 私有字段仅 ServeMux.ServeHTTP 设置），
//    导致所有 {id} 路径参数为空——reveal/吊销/头像等带参数端点全线 404。
//    反代响应中的上游 CORS 头由 ReverseProxy ModifyResponse 剥除，避免重复。
func corsGate(mux *http.ServeMux) http.Handler {
	cors := func(w http.ResponseWriter) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, x-api-key")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			cors(w)
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		cors(w)
		mux.ServeHTTP(w, r)
	})
}

// statusWriter 记录状态码（Flush 透传，流式不受影响）
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// accessLog 访问日志：客户端IP 方法 路径 → 状态 耗时（stdout → journald 可查）
// 同时注入 X-Request-Id（OpenAI SDK 排障习惯：响应头可与服务端日志互查）
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusWriter{ResponseWriter: w, status: 200}
		start := time.Now()
		rid := newRequestID()
		w.Header().Set("X-Request-Id", rid)
		next.ServeHTTP(rec, r)
		ip := r.Header.Get("X-Real-Ip")
		if ip == "" {
			ip = r.RemoteAddr
		}
		log.Printf("[http] %s %s %s -> %d %s rid=%s", ip, r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond), rid)
	})
}

// newRequestID 短随机请求 ID（req_ + 12 hex）
func newRequestID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "req_" + hex.EncodeToString(b)
}

// jsonOut JSON 响应
func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// errTypes 标准错误码 → 错误类型（OpenAI 兼容口径：invalid_request_error /
// authentication_error / rate_limit_error / insufficient_quota / api_error）
var errTypes = map[string]string{
	"bad_request":        "invalid_request_error",
	"model_not_found":    "invalid_request_error",
	"not_found":          "invalid_request_error",
	"invalid_api_key":    "authentication_error",
	"unauthorized":       "authentication_error",
	"invalid_credentials": "authentication_error",
	"admin_disabled":     "permission_error",
	"insufficient_quota":  "insufficient_quota",
	"rate_limit_exceeded": "rate_limit_error",
	"internal_error":      "api_error",
	"upstream_error":      "api_error",
	"service_unavailable": "api_error",
	"overloaded_error":    "overloaded_error", // 上游渠道级不可用（OpenAI 官方 type）
}

// errSink 5xx 错误中心埋点钩子（诊断 D2：App 构造时注入，5xx 统一落 error_events）
var errSink func(kind, detail string)

// errOut 站点统一错误（国际标准 OpenAI 风格；信息隔离：不透传上游原文）
func errOut(w http.ResponseWriter, code int, ecode, msg string) {
	if code >= 500 && errSink != nil {
		errSink(ecode, msg)
	}
	t, ok := errTypes[ecode]
	if !ok {
		if code >= 500 {
			t = "api_error"
		} else if code == 401 || code == 403 {
			t = "authentication_error"
		} else {
			t = "invalid_request_error"
		}
	}
	jsonOut(w, code, map[string]any{
		"error": map[string]any{"message": msg, "type": t, "code": ecode, "param": nil},
	})
}

// upstreamErrOut 上游错误转译：上游任何报错 → 站点标准错误码（不透传原文，保护上游信息）
// body 为上游错误响应体（用于识别"模型已下线/无可用通道"，翻译为 model_not_found）
func upstreamErrOut(w http.ResponseWriter, upstreamStatus int, body []byte) {
	if upstream.IsChannelExhausted(body) {
		// 渠道级不可用是**瞬时故障**（与模型是否下线无关），必须与 404 区分开：
		// 返回 503 overloaded_error（OpenAI 官方 type），引导客户端稍后重试
		errOut(w, 503, "overloaded_error", "上游渠道暂时没有可用节点，请稍后重试；持续出现请联系站长")
		return
	}
	if upstream.IsModelUnavailable(body) {
		errOut(w, 404, "model_not_found", "该模型已下线或上游通道不可用，请联系站长")
		return
	}
	switch {
	case upstreamStatus == 400 || upstreamStatus == 422:
		errOut(w, 400, "bad_request", "请求参数不被上游接受，请检查请求体")
	case upstreamStatus == 404:
		errOut(w, 404, "model_not_found", "上游不存在该模型，请联系站长核对模型映射")
	case upstreamStatus == 401 || upstreamStatus == 403:
		errOut(w, 502, "upstream_error", "上游鉴权异常，已通知站长处理")
	case upstreamStatus == 429:
		errOut(w, 429, "rate_limit_exceeded", "上游繁忙，请稍后重试")
	case upstreamStatus >= 500:
		errOut(w, 502, "upstream_error", "上游服务暂时不可用，请稍后重试")
	default:
		errOut(w, 502, "upstream_error", "上游服务错误，请稍后重试")
	}
}

// handleStatus 存活探测 + 状态透明页（全模型近 1 小时成功率 / 延迟）
func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Unix() - 3600
	models := []map[string]any{}
	rows, err := a.DB.Query(
		`SELECT model, COUNT(*) calls, SUM(ok)*1.0/COUNT(*) succ, COALESCE(AVG(latency_ms),0) lat
		 FROM model_health WHERE ts>=? GROUP BY model ORDER BY calls DESC`, since)
	if err == nil {
		for rows.Next() {
			var model string
			var calls int64
			var succ, lat float64
			if rows.Scan(&model, &calls, &succ, &lat) == nil {
				models = append(models, map[string]any{
					"model": model, "calls_1h": calls,
					"success_rate":   round1(succ * 100),
					"avg_latency_ms": round1(lat),
				})
			}
		}
		rows.Close()
	}
	jsonOut(w, 200, map[string]any{
		"ok":         true,
		"service":    "aqua-gateway-go",
		"version":    gatewayVersion,
		"uptime_sec": time.Now().Unix() - startTime.Unix(),
		"window":     "1h",
		"models":     models,
	})
}

// gatewayVersion 网关版本（/status 展示；构建时可注入：
// go build -ldflags "-X acu-aqua/gateway/internal/httpapi.gatewayVersion=v2026.09.12"）
var gatewayVersion = "go-2026.09"

// handleMeta 站点信息（前端渲染源，全部来自配置——代码零运营事实）
func (a *App) handleMeta(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]any{
		"name":         a.Cfg.Site.Name,
		"domain":       a.Cfg.Site.Domain,
		"docs_url":     a.Cfg.Site.DocsURL,
		"qq_group":     a.Cfg.Site.QQGroup,
		"qq_group_url": a.Cfg.Site.QQGroupURL,
	})
}

// handleModels 模型列表（价格展示；成本率绝不出现）。
// 组成：auto 置顶 + 收费线（paid:true + 实时价目）+ 免费线（无 paid → 前端标免费）+ 动态目录。
// 用户组差异化：VIP 用户（price_grp_call/token='vip'）在 normal 价已下架时回退 vip 组价目——
// 已下架模型对 VIP 仍可见可用；普通用户仅见当前生效价的模型。
func (a *App) handleModels(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r) // 公开端点：无凭据时按普通口径
	created := time.Now().Unix()
	data := a.modelListEntries(actx, created)
	jsonOut(w, 200, map[string]any{"object": "list", "created": created, "data": data})
}

// modelListEntries 全量模型条目（/v1/models 与 /v1/models/{id} 共用）
func (a *App) modelListEntries(actx *auth.Ctx, created int64) []map[string]any {
	data := []map[string]any{{
		"id": "auto", "object": "model", "created": created, "owned_by": "acu",
		"auto":        true,
		"description": "智能自动路由：每次请求实时选择当前成功率最高、响应最快的模型，快与稳优先，不保证每次命中同一模型",
	}}

	health := a.computeHealth()
	retired := a.retiredUpstreams()

	// 收费线：实时价目（DB pricing 表为准）；下架（normal 价过期）即从列表消失，VIP 回退 vip 组
	if a.Cfg.Billing.UnifiedPrefix != "" {
		// 统一前缀模式：全部收费线模型合并为 前缀/site_id，groups 标注可用计费分组
		// （同 ID 在按次/按量线都存在 → 两条分组都列出；计费方式由密钥分组决定）
		type grpPrice struct {
			mode string
			m    *config.Model
			p    *billing.PricingInfo // 用户实付价目（VIP 有 vip 价目 → vip 价；否则 normal 价）
			base *billing.PricingInfo // 原价（normal）——仅 VIP 且存在 vip 价目时携带，供前端底部展示
		}
		merged := map[string][]grpPrice{}
		var order []string
		mergedLines := a.linesSnap()
		for i := range mergedLines {
			l := &mergedLines[i]
			if l.Mode == "free" {
				continue
			}
			vip := actx != nil && a.userGrpFor(actx.UserID, l.Mode) == "vip"
			for j := range l.Models {
				m := &l.Models[j]
				full := config.ModelFullName(l.ID, m.SiteID)
				p := a.pricingFor(full, "normal")
				if p == nil {
					// normal 价过期（下架）：VIP 回退 vip 组（保持原语义）
					if vip {
						p = a.pricingFor(full, "vip")
					}
					if p == nil {
						continue
					}
					if _, ok := merged[m.SiteID]; !ok {
						order = append(order, m.SiteID)
					}
					merged[m.SiteID] = append(merged[m.SiteID], grpPrice{l.Mode, m, p, nil})
					continue
				}
				if _, ok := merged[m.SiteID]; !ok {
					order = append(order, m.SiteID)
				}
				if vip {
					if vp := a.pricingFor(full, "vip"); vp != nil {
						// VIP 拿货价为主 + 附原价（normal），前端底部展示对比
						merged[m.SiteID] = append(merged[m.SiteID], grpPrice{l.Mode, m, vp, p})
						continue
					}
				}
				merged[m.SiteID] = append(merged[m.SiteID], grpPrice{l.Mode, m, p, nil})
			}
		}
		for _, site := range order {
			gs := merged[site]
			item := map[string]any{
				"id": a.Cfg.Billing.UnifiedPrefix + "/" + site, "object": "model",
				"created": created, "owned_by": "acu", "paid": true,
				"type": "chat", // 特殊计费模型类型下发（image/video…），前端零硬编码；来自线路模型配置
			}
			modes := []string{}
			var pc, pt *grpPrice
			for k := range gs {
				modes = append(modes, gs[k].mode)
				if gs[k].mode == "per_call" && pc == nil {
					pc = &gs[k]
				}
				if gs[k].mode == "per_token" && pt == nil {
					pt = &gs[k]
				}
			}
			item["groups"] = modes
			item["mode"] = modes[0] // 兼容旧读法：首个可用计费模式
			if pt != nil {
				item["floor_micro"] = pt.p.FloorMicro
				item["in_price"] = float64(pt.p.InRate10) / 10000
				item["cache_price"] = float64(pt.p.CacheRate10) / 10000
				item["out_price"] = float64(pt.p.OutRate10) / 10000
				if pt.base != nil {
					// VIP 用户：附原价（normal）供前端底部展示对比
					item["base_floor_micro"] = pt.base.FloorMicro
					item["base_in_price"] = float64(pt.base.InRate10) / 10000
					item["base_cache_price"] = float64(pt.base.CacheRate10) / 10000
					item["base_out_price"] = float64(pt.base.OutRate10) / 10000
				}
			}
			if pc != nil {
				item["price_micro"] = pc.p.PriceMicro
				if pc.base != nil {
					item["base_price_micro"] = pc.base.PriceMicro
				}
				// 描述跟随价格来源：双线同模（按次+按量并存）时 price_micro 取按次价，描述也必须按次，
				// 避免"按次价 + 按量描述"混搭误导；纯按量模型（pc==nil）在下方回落按量描述
				item["description"] = pricingDescription(pc.m, pc.p)
			} else if pt != nil {
				item["description"] = pricingDescription(pt.m, pt.p)
			}
			for k := range gs {
				if gs[k].m.Image {
					item["image"] = true
					item["type"] = "image"
					item["per_image"] = gs[k].p.PriceMicro
					if gs[k].base != nil {
						item["base_per_image"] = gs[k].base.PriceMicro
					}
				}
			}
			// 限时补贴：生效价目行带截止时间（活动价时间窗）→ 透出角标与到期时刻
			var promoEnds int64
			if pt != nil && pt.p.EndsAt > promoEnds {
				promoEnds = pt.p.EndsAt
			}
			if pc != nil && pc.p.EndsAt > promoEnds {
				promoEnds = pc.p.EndsAt
			}
			for k := range gs {
				if gs[k].p.EndsAt > promoEnds {
					promoEnds = gs[k].p.EndsAt
				}
			}
			if promoEnds > 0 {
				item["subsidized"] = true
				item["promo_ends_at"] = promoEnds
			}
			// 诊断 D4：该模型所有可用分组都标记降级 → /v1/models 透出（客户端提示换模型）
			allDeg := true
			for k := range gs {
				if !gs[k].m.Degraded {
					allDeg = false
					break
				}
			}
			if allDeg && len(gs) > 0 {
				item["degraded"] = true
			}
			data = append(data, item)
		}
	} else {
		// 线前缀直连模式（统一路由未启用）：按线逐条输出
		directLines := a.linesSnap()
		for i := range directLines {
			l := &directLines[i]
			if l.Mode == "free" {
				continue
			}
			vip := actx != nil && a.userGrpFor(actx.UserID, l.Mode) == "vip"
			for j := range l.Models {
				m := &l.Models[j]
				full := config.ModelFullName(l.ID, m.SiteID)
				p := a.pricingFor(full, "normal")
				if p == nil && vip {
					// VIP：normal 价已下架 → 回退 vip 组价目（vip 价目永久生效）
					p = a.pricingFor(full, "vip")
				}
				if p == nil {
					continue
				}
				item := map[string]any{
					"id": full, "object": "model", "created": created, "owned_by": "acu",
					"paid":        true,
					"type":        "chat",
					"mode":        p.Mode,
					"price_micro": p.PriceMicro,
					"description": pricingDescription(m, p),
				}
				if p.Mode == "per_token" {
					item["floor_micro"] = p.FloorMicro
					item["in_price"] = float64(p.InRate10) / 10000
					item["cache_price"] = float64(p.CacheRate10) / 10000
					item["out_price"] = float64(p.OutRate10) / 10000
				}
				if m.Image {
					item["image"] = true
					item["type"] = "image"
					item["per_image"] = p.PriceMicro
				}
				if m.Degraded {
					item["degraded"] = true // 诊断 D4：降级标记透出
				}
				data = append(data, item)
			}
		}
	}

	// 免费线：全部模型列出（上游永久下线的隐藏）+ 动态目录
	freeLines := a.linesSnap()
	for i := range freeLines {
		l := &freeLines[i]
		if l.Mode != "free" {
			continue
		}
		configured := map[string]bool{}
		for j := range l.Models {
			m := &l.Models[j]
			configured[strings.ToLower(m.SiteID)] = true
			if retired[m.UpstreamID] {
				continue // 上游永久下线（404/410 两次确认）：隐藏
			}
			item := map[string]any{"id": m.SiteID, "object": "model", "created": created, "owned_by": l.ID}
			if m.Image {
				item["type"] = "image" // 特殊计费类型下发（免费文生图等），前端零硬编码
			}
			if h, ok := health[m.SiteID]; ok {
				item["health"] = h
			}
			data = append(data, item)
		}
		if l.Dynamic {
			for _, item := range a.dynamicModels(l.ID, configured, retired) {
				if h, ok := health[item["id"].(string)]; ok {
					item["health"] = h
				}
				data = append(data, item)
			}
		}
	}
	return data
}

// pricingDescription 收费模型对外计费说明（不含成本/通道/折扣率字样）
func pricingDescription(m *config.Model, p *billing.PricingInfo) string {
	if p.Mode == "per_token" {
		return fmt.Sprintf(
			"按量计费：输入 %s / 缓存命中 %s / 输出 %s 元每百万 tokens（先付后用：余额充足方可调用，可在请求中调小 max_tokens 降低单次预扣，单次保底 %s 元）",
			trimPrice(float64(p.InRate10)/10000), trimPrice(float64(p.CacheRate10)/10000),
			trimPrice(float64(p.OutRate10)/10000), microToYuanStr(p.FloorMicro))
	}
	if m.Image {
		return fmt.Sprintf("%s 元/张，按张计费（先付后用，n 参数控制张数）", microToYuanStr(p.PriceMicro))
	}
	return fmt.Sprintf("预充值按次计费：%s 元/次（先付后用：余额充足方可调用）", microToYuanStr(p.PriceMicro))
}

// trimPrice 价格显示：0.0500 → "0.05"（去尾零）
func trimPrice(v float64) string {
	s := fmt.Sprintf("%.4f", v)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// microToYuanStr 微元 → 元字符串（去尾零）
func microToYuanStr(micro int64) string {
	return trimPrice(float64(micro) / 1_000_000)
}

// bearerToken 提取 Authorization: Bearer xxx
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):])
	}
	return ""
}
