// Package httpapi 路由与 handler。net/http 1.22+ 方法路由，零框架依赖。
package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
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
	mux.HandleFunc("GET /v1/status", a.handleStatus) // 前端状态大屏调用路径（/status 为旧路径兼容）
	mux.HandleFunc("GET /v1/meta", a.handleMeta)
	if legacyProxy == nil {
		// 纯 Go 单体模式：模型目录由本网关生成；绞杀者模式下 /v1/models 反代旧网关
		// （旧网关目录含免费模型 + 收费模型 + 活动价状态，与前端展示完全一致）
		mux.HandleFunc("GET /v1/models", a.handleModels)
	}
	mux.HandleFunc("GET /v1/models/status", a.handleModelsStatus)
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

	// 众筹池（acu/ 公共算力池）：账本/榜单公开透明，个人明细须登录，官方注入须管理员
	mux.HandleFunc("GET /v1/pool/status", a.handlePoolStatus)
	mux.HandleFunc("GET /v1/pool/flows", a.handlePoolFlows)
	mux.HandleFunc("GET /v1/pool/ranks", a.handlePoolRanks)
	mux.HandleFunc("GET /v1/my/pool/flows", a.handleMyPoolFlows)
	mux.HandleFunc("POST /v1/admin/pool/seed", a.handleAdminPoolSeed)

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
	mux.HandleFunc("PATCH /v1/my/keys/{id}/group", a.myKeysGroup)
	mux.HandleFunc("DELETE /v1/my/keys/{id}", a.myKeysDelete)
	mux.HandleFunc("GET /v1/my/keys/{id}/reveal", a.myKeysReveal)
	mux.HandleFunc("GET /v1/my/finance", a.myFinance)
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
	// —— Codex 账号池运维（账号用量/存活/利润 + 代理探测换线）——
	mux.HandleFunc("GET /v1/admin/codex", a.handleAdminCodex)
	mux.HandleFunc("POST /v1/admin/codex/proxy/switch", a.handleAdminCodexSwitch)
	// —— 上游线路在线管理（DB 事实源 + 热重载；密钥/模型/线路 CRUD）——
	mux.HandleFunc("GET /v1/admin/lines", a.handleAdminLines)
	mux.HandleFunc("POST /v1/admin/nvidia/sync", a.handleAdminNvidiaSync) // NVIDIA 动态目录手动同步
	mux.HandleFunc("POST /v1/admin/lines/reload", a.handleAdminLinesReload)
	// —— 微软邮箱发信池（站长定稿 20260916：全域主线路）——
	mux.HandleFunc("POST /v1/admin/mailpool/import", a.handleAdminMailpoolImport)
	mux.HandleFunc("GET /v1/admin/mailpool", a.handleAdminMailpoolList)
	mux.HandleFunc("POST /v1/admin/mailpool/probe", a.handleAdminMailpoolProbe)
	mux.HandleFunc("POST /v1/admin/mailpool/test", a.handleAdminMailpoolTest)
	// —— 邀请返利（精算定稿 20260917：门槛¥5/奖¥2/返10%）——
	mux.HandleFunc("GET /v1/invite/me", a.handleInviteMe)
	mux.HandleFunc("POST /v1/invite/rotate", a.handleInviteRotate)
	mux.HandleFunc("GET /v1/admin/invites", a.handleAdminInvites)
	mux.HandleFunc("POST /v1/admin/lines", a.handleAdminLineCreate)
	mux.HandleFunc("POST /v1/admin/lines/{line}", a.handleAdminLineUpdate)
	mux.HandleFunc("DELETE /v1/admin/lines/{line}", a.handleAdminLineDelete)
	mux.HandleFunc("GET /v1/admin/lines/{line}/keys", a.handleAdminLineKeys)
	mux.HandleFunc("POST /v1/admin/lines/{line}/keys", a.handleAdminLineKeysAdd)
	mux.HandleFunc("POST /v1/admin/lines/{line}/keys/calibrate", a.handleAdminLineKeysCalibrate)
	mux.HandleFunc("POST /v1/admin/lines/{line}/keys/calibrate-auto", a.handleAdminLineKeysCalibrateAuto)
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
	// 全员邮件通知（计费变更公告等站点级通知；逐人单独发信 + 限速 + 进度查询）
	mux.HandleFunc("POST /v1/admin/notify/mail", a.handleAdminNotifyMail)
	mux.HandleFunc("GET /v1/admin/notify/mail/status", a.handleAdminNotifyMailStatus)
	mux.HandleFunc("GET /v1/admin/settings", a.handleAdminSettingsGet)
	mux.HandleFunc("POST /v1/admin/settings", a.handleAdminSettingsSave)

	// 其余全部反代旧网关（绞杀者迁移残留；纯 Go 单体模式下未注册路径 404）
	mux.HandleFunc("/", a.handleLegacy)
	return accessLog(corsGate(mux))
}

// corsGate CORS 门卫：
// 1. OPTIONS 预检全局应答（204 + CORS 头）——跨域 SPA 依赖预检放行。
// 2. 全部响应注入 CORS 头（单一来源）。
// ⚠️ 必须走 mux.ServeHTTP 而非 mux.Handler(r)+手动调用：后者绕过 Go 1.22
//
//	ServeMux 的路径参数注入（r.matches 私有字段仅 ServeMux.ServeHTTP 设置），
//	导致所有 {id} 路径参数为空——reveal/吊销/头像等带参数端点全线 404。
//	反代响应中的上游 CORS 头由 ReverseProxy ModifyResponse 剥除，避免重复。
func corsGate(mux *http.ServeMux) http.Handler {
	cors := func(w http.ResponseWriter) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
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
	"bad_request":         "invalid_request_error",
	"model_not_found":     "invalid_request_error",
	"not_found":           "invalid_request_error",
	"invalid_api_key":     "authentication_error",
	"unauthorized":        "authentication_error",
	"invalid_credentials": "authentication_error",
	"admin_disabled":      "permission_error",
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
	case upstreamStatus == 413:
		// 上游对请求体大小有硬限制（超长上下文/图片场景）：给用户明确指引而非笼统报错
		errOut(w, 413, "context_too_long", "请求内容过长：请缩短对话上下文或减小附件大小后重试")
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
	// 口径（与 handleModelsStatus 对齐）：
	//   - 数据源用 requests 表（全线路真实请求；model_health 仅免费线写入，会导致收费模型缺席/误显 0%）
	//   - 分母剔除 4xx（400~499：参数错/未鉴权/限流 429/面值 402/请求体过大 413/客户端断开 499/防刷拦截等
	//     用户侧与网关侧拒绝——模型本身健康与否与此类失败无关），仅 5xx/网络错（status_code=0）计为真实失败；
	//     另剔除 compensated_prehold（网关补偿流水）、stream_incomplete（上游长生成 300s 硬切，内容多数已送达）、
	//     client_cancel（客户端等待期间主动断开）、在途未结算行（ok=0 且 status_code=200 且 error=''）——均非模型健康信号
	//   - 平均时延只取成功请求（失败请求的耗时反映的是熔断/拒绝速度，不是模型速度）
	rows, err := a.DB.Query(
		`SELECT model, COUNT(*) calls, SUM(ok)*1.0/COUNT(*) succ,
		        COALESCE(AVG(CASE WHEN ok=1 AND latency_ms>0 THEN latency_ms END),0) lat,
		        COALESCE(AVG(CASE WHEN ok=1 AND first_ms>0 THEN first_ms END),0) ttft
		 FROM requests
		 WHERE ts>=? AND endpoint IN ('chat','images')
		   AND status_code NOT BETWEEN 400 AND 499
		   AND NOT (ok=0 AND error IN ('stream_incomplete','compensated_prehold','client_cancel'))
		   AND NOT (ok=0 AND status_code=200 AND error='')
		 GROUP BY model HAVING calls>0 ORDER BY calls DESC`, since)
	if err == nil {
		for rows.Next() {
			var model string
			var calls int64
			var succ, lat, ttft float64
			if rows.Scan(&model, &calls, &succ, &lat, &ttft) == nil {
				mm := map[string]any{
					"model": model, "calls_1h": calls,
					"success_rate":   round1(succ * 100),
					"avg_latency_ms": round1(lat),
				}
				if ttft > 0 {
					mm["avg_first_ms"] = round1(ttft) // 首字均延迟（用户感知口径，前端优先展示）
				}
				models = append(models, mm)
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

// gatewayVersion 网关版本（/status 与管理后台展示，遵循 SemVer 2.0.0；
// 构建时可注入：go build -ldflags "-X acu-aqua/gateway/internal/httpapi.gatewayVersion=v1.0.0"）
var gatewayVersion = "v1.0.0"

// handleMeta 站点信息（前端渲染源，全部来自配置——代码零运营事实）
// handleMeta 已迁 settings.go（站点配置 DB 外置化：settings 表优先，config 兜底）

// statusModelNorm 模型名规范化映射：站内统一 ID（前缀/site_id）。
// requests.model 历史存在三种记录口径并存：aqua/xxx（统一前缀）、tide/xxx（线前缀直连）、
// 裸名 site_id（旧网关遗留流量）——全部归并到统一前缀全名，保证模型卡片实时指标能对上。
func (a *App) statusModelNorm() map[string]string {
	m := map[string]string{}
	up := a.Cfg.Billing.UnifiedPrefix
	if up == "" {
		return m
	}
	for _, l := range a.linesSnap() {
		if l.Mode == "free" {
			continue
		}
		if l.Mode == "official" || l.Mode == "crowd" || l.AuthStyle == "codex" {
			// tlk 官方中转线 / acu 众筹专线 / codex GPT 专线：独立前缀（tlk/xxx、acu/xxx、codex/xxx），目录/状态/请求三方同 ID 恒等映射
			for j := range l.Models {
				full := config.ModelFullName(l.ID, l.Models[j].SiteID)
				m[full] = full
			}
			continue
		}
		for j := range l.Models {
			u := up + "/" + l.Models[j].SiteID
			m[up+"/"+l.Models[j].SiteID] = u                      // 统一前缀原文（aqua 线下架后 tide 承接，仍需恒等映射）
			m[config.ModelFullName(l.ID, l.Models[j].SiteID)] = u // 线前缀名 tide/xxx → aqua/xxx
			m[l.Models[j].SiteID] = u                             // 裸名（旧网关口径）→ aqua/xxx
		}
	}
	return m
}

// handleModelsStatus 模型实时状态（公开匿名，最近 200 次请求 + 近 6 小时窗口口径）：
// 每模型取最近 6 小时内最近 200 条真实请求，聚合请求数/成功率/平均时延/平均输出速度，供模型中心模型卡片内嵌展示。
// 平均输出速度（tok/s）为流式生成阶段口径（首字之后），并剔除 <3 tok/s 异常样本（短输出/保底估算/非流式总耗时口径）。
// 只统计收费线流量（resolved_line 非空）：免费分发流量（裸名走免费上游、resolved_line 为空）
// 的上游故障与收费模型健康无关，不得计入（避免免费上游 NIM 故障污染收费模型状态）。
// 成功率剔除与模型健康无关的失败（仅作用 ok=0 行）：
// 400/404/422/429/402（调用方参数错/限流/面值耗尽）、413（请求体过大，上游拒绝）、
// 499/client_cancel（客户端等待期间主动断开）——用户侧问题；
// compensated_prehold（悬空预扣补偿退款，网关侧流水修补）；
// stream_incomplete（上游对长生成 300s 硬切：上游侧正常完成计费、内容多数已送达，渠道固有行为
// 而非模型故障——真正反映健康的是 upstream_error/5xx/网络错）；
// status_code=200 且 error='' 且 ok=0（在途未结算/进程重启被斩的僵尸行，非真实失败）。
// 6h 窗口（rc20）：低频模型的历史失败不再永久拖累状态——窗口外请求自然过期，无近期流量则不出现在列表（前端显示待命中）。
// 状态判定（rc20 放宽）：样本≥10 时 ≥99.5% 状态极佳 / ≥95% 正常 / ≥85% 部分异常 / 其余故障；
// 小样本有失败最多判"部分异常"，不下重判。
// 只输出聚合运行指标，不含成本/渠道/用户信息；模型名统一规范化后合并聚合。
func (a *App) handleModelsStatus(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`
		SELECT model, COUNT(*), COALESCE(SUM(ok),0),
		       COALESCE(SUM(CASE WHEN ok=0 AND (status_code IN (400,404,422,429,402,413,499)
		           OR error IN ('compensated_prehold','stream_incomplete','client_cancel')
		           OR (status_code=200 AND error='')) THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN ok=1 AND latency_ms>0 THEN latency_ms ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN ok=1 AND latency_ms>0 THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN ok=1 AND first_ms>0 THEN first_ms ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN ok=1 AND first_ms>0 THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN ok=1 AND tps>=3 THEN tps ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN ok=1 AND tps>=3 THEN 1 ELSE 0 END),0),
		       MAX(ts)
		FROM (
			SELECT model, ok, latency_ms, tps, ts, status_code, error, first_ms,
			       ROW_NUMBER() OVER (PARTITION BY model ORDER BY rowid DESC) rn
			FROM (SELECT rowid, model, ok, latency_ms, tps, ts, status_code, error, first_ms
		      FROM requests WHERE endpoint IN ('chat','images') AND resolved_line != ''
		        AND ts > strftime('%s','now') - 21600
		      ORDER BY rowid DESC LIMIT 50000)
		) WHERE rn <= 200 GROUP BY model`)
	if err != nil {
		errOut(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	norm := a.statusModelNorm()
	type stAgg struct {
		total, okN, userErr, latN, tpsN, lastTs int64
		latSum, tpsSum                          float64
		firstSum, firstN                        int64
	}
	agg := map[string]*stAgg{}
	order := []string{}
	for rows.Next() {
		var model string
		var total, okN, userErr, latN, tpsN, lastTs, firstSum, firstN int64
		var latSum, tpsSum float64
		if rows.Scan(&model, &total, &okN, &userErr, &latSum, &latN, &firstSum, &firstN, &tpsSum, &tpsN, &lastTs) != nil {
			continue
		}
		name, ok := norm[model]
		if !ok {
			continue // 免费线与未知模型不输出
		}
		g := agg[name]
		if g == nil {
			g = &stAgg{}
			agg[name] = g
			order = append(order, name)
		}
		g.total += total
		g.okN += okN
		g.userErr += userErr
		g.latSum += latSum
		g.latN += latN
		g.firstSum += firstSum
		g.firstN += firstN
		g.tpsSum += tpsSum
		g.tpsN += tpsN
		if lastTs > g.lastTs {
			g.lastTs = lastTs
		}
	}
	items := []map[string]any{}
	for _, name := range order {
		g := agg[name]
		denom := g.total - g.userErr // 分母剔除用户参数类错误
		rate := 1.0
		if denom > 0 {
			rate = float64(g.okN) / float64(denom)
		}
		status := "ok"
		if denom >= 10 {
			switch {
			case rate < 0.85:
				status = "down" // 小样本（<30）不下重判：真实证据不足，最多"部分异常"
				if denom < 30 {
					status = "degraded"
				}
			case rate < 0.95:
				status = "degraded"
			case rate >= 0.995 && denom >= 30:
				status = "great" // 近期表现近乎完美：状态极佳
			}
		} else if rate < 1 {
			status = "degraded" // 小样本：有失败最多"部分异常"，绝不误判故障
		}
		it := map[string]any{
			"model": name, "samples": g.total, "ok": g.okN, "ok_rate": rate,
			"status": status, "last_ts": g.lastTs, "sample_size": 200,
		}
		if g.latN > 0 {
			it["avg_latency_ms"] = int64(math.Round(g.latSum / float64(g.latN)))
		}
		if g.firstN > 0 {
			it["avg_first_ms"] = int64(math.Round(float64(g.firstSum) / float64(g.firstN)))
		}
		if g.tpsN > 0 {
			it["avg_tps"] = g.tpsSum / float64(g.tpsN)
		}
		items = append(items, it)
	}
	jsonOut(w, 200, map[string]any{"object": "list", "sample_size": 200, "generated_ts": time.Now().Unix(), "data": items})
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

// promoBase 促销期"官方原价"折算：原价 = 用户实付价目 ÷ 促销倍率（[site] rate_promo / rate_promo_vip）。
// 仅当生效价目行是活动价（EndsAt>0）且配置了对应倍率时折算；p 为用户实付行，isVip 选 VIP 倍率。
// 折算结果与上游官方标价一致（如 qwen3.8-27b 输入 0.60/0.2=¥3.00、VIP 0.39/0.13=¥3.00），供前端划线对照。
func (a *App) promoBase(p *billing.PricingInfo, isVip bool) *billing.PricingInfo {
	if p == nil || p.EndsAt <= 0 {
		return nil
	}
	rs := a.Cfg.Site.RatePromo
	if isVip {
		rs = a.Cfg.Site.RatePromoVip
	}
	rate, err := strconv.ParseFloat(strings.TrimSpace(rs), 64)
	if err != nil || rate <= 0 || rate >= 1 {
		return nil
	}
	div := func(v int64) int64 { return int64(math.Round(float64(v) / rate)) }
	return &billing.PricingInfo{
		Mode: p.Mode, PriceMicro: div(p.PriceMicro), FloorMicro: div(p.FloorMicro),
		InRate10: div(p.InRate10), CacheRate10: div(p.CacheRate10), OutRate10: div(p.OutRate10),
		EndsAt: p.EndsAt,
	}
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
	// 纯免费分组密钥：收费模型对其不可见不可调（列表同步隐藏，调用端 403 拦截）
	freeOnly := actx != nil && actx.KeyGrp == "free"

	// 收费线：实时价目（DB pricing 表为准）；下架（normal 价过期）即从列表消失，VIP 回退 vip 组
	if a.Cfg.Billing.UnifiedPrefix != "" && !freeOnly {
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
			if l.Mode == "free" || l.Mode == "official" || l.Mode == "per_token" || l.Mode == "crowd" || l.AuthStyle == "codex" {
				// official（tlk 官方中转）/ codex（GPT 账号池）/ per_token（按量专线 tlinks 等）/
				// crowd（acu 众筹专线）线不参与统一前缀合并：均有专属前缀（线 id 即前缀）单独输出（下方）。
				// crowd 曾漏排除——其 6 模型与 aqua/ 全重叠且价目同为 per_token，合并出 groups 重复双份
				// （['per_token','per_token']，20260917 站长报告"未正确展示按量计费"根因之一）
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
					merged[m.SiteID] = append(merged[m.SiteID], grpPrice{p.Mode, m, p, nil})
					continue
				}
				if _, ok := merged[m.SiteID]; !ok {
					order = append(order, m.SiteID)
				}
				if vip {
					if vp := a.pricingFor(full, "vip"); vp != nil {
						// VIP 拿货价为主 + 附原价（normal），前端底部展示对比
						merged[m.SiteID] = append(merged[m.SiteID], grpPrice{vp.Mode, m, vp, p})
						continue
					}
				}
				merged[m.SiteID] = append(merged[m.SiteID], grpPrice{p.Mode, m, p, nil})
			}
		}
		for _, site := range order {
			gs := merged[site]
			item := map[string]any{
				"id": a.Cfg.Billing.UnifiedPrefix + "/" + site, "object": "model",
				// owned_by 跟前缀品牌走（aqua/→"aqua"）：原硬编码 "acu" 与众筹线品牌混淆（20260917 站长报告错标）
				"created": created, "owned_by": a.Cfg.Billing.UnifiedPrefix, "paid": true,
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
				// floor_micro 不对外下发（单次保底属计费兜底机制，前端不得展示——20260916 站长指示）
				item["in_price"] = float64(pt.p.InRate10) / 10000
				item["cache_price"] = float64(pt.p.CacheRate10) / 10000
				item["out_price"] = float64(pt.p.OutRate10) / 10000
				// 原价对照：促销生效时按倍率折算官方原价（所有用户下发，前端划线展示）；非促销期仅 VIP 附 normal 行
				if pb := a.promoBase(pt.p, pt.base != nil); pb != nil {
					item["base_floor_micro"] = pb.FloorMicro
					item["base_in_price"] = float64(pb.InRate10) / 10000
					item["base_cache_price"] = float64(pb.CacheRate10) / 10000
					item["base_out_price"] = float64(pb.OutRate10) / 10000
				} else if pt.base != nil {
					// VIP 用户：附原价（normal）供前端底部展示对比
					item["base_floor_micro"] = pt.base.FloorMicro
					item["base_in_price"] = float64(pt.base.InRate10) / 10000
					item["base_cache_price"] = float64(pt.base.CacheRate10) / 10000
					item["base_out_price"] = float64(pt.base.OutRate10) / 10000
				}
			}
			if pc != nil {
				item["price_micro"] = pc.p.PriceMicro
				if pb := a.promoBase(pc.p, pc.base != nil); pb != nil {
					item["base_price_micro"] = pb.PriceMicro
				} else if pc.base != nil {
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
		// tlk 官方中转线：独立前缀（tlk/xxx）单独输出——官方原价 6 折、仅官方中转分组密钥可调
		for i := range mergedLines {
			l := &mergedLines[i]
			if l.Mode != "official" {
				continue
			}
			vip := actx != nil && a.userGrpFor(actx.UserID, l.Mode) == "vip"
			for j := range l.Models {
				m := &l.Models[j]
				full := config.ModelFullName(l.ID, m.SiteID)
				p := a.pricingFor(full, "normal")
				if p == nil {
					continue // 无生效 normal 价：不下发（与统一线"下架即消失"口径一致）
				}
				if vip {
					if vp := a.pricingFor(full, "vip"); vp != nil {
						p = vp
					}
				}
				data = append(data, map[string]any{
					"id": full, "object": "model",
					"created": created, "owned_by": l.ID, "paid": true,
					"type":   "chat",
					"groups": []string{"official"}, "mode": "official",
					"in_price":    float64(p.InRate10) / 10000,
					"cache_price": float64(p.CacheRate10) / 10000,
					"out_price":   float64(p.OutRate10) / 10000,
					"description": pricingDescription(m, p),
				})
			}
		}
		// codex（GPT 账号池）线：专属前缀（codex/xxx）单独输出——按量分段计价，任意登录密钥可调
		for i := range mergedLines {
			l := &mergedLines[i]
			if l.AuthStyle != "codex" {
				continue
			}
			vip := actx != nil && a.userGrpFor(actx.UserID, l.Mode) == "vip"
			for j := range l.Models {
				m := &l.Models[j]
				full := config.ModelFullName(l.ID, m.SiteID)
				p := a.pricingFor(full, "normal")
				if p == nil {
					continue
				}
				if vip {
					if vp := a.pricingFor(full, "vip"); vp != nil {
						p = vp
					}
				}
				// mode/groups 跟价目行走（线内混挂 image 等按次模型时价目 mode=per_call，
				// 标线 mode=per_token 会让前端按量卡错误展示 0 价三段——20260917 实测修复）
				item := map[string]any{
					"id": full, "object": "model",
					"created": created, "owned_by": l.ID, "paid": true,
					"type":   "chat",
					"groups": []string{p.Mode}, "mode": p.Mode,
				}
				if p.Mode == "per_token" {
					item["in_price"] = float64(p.InRate10) / 10000
					item["cache_price"] = float64(p.CacheRate10) / 10000
					item["out_price"] = float64(p.OutRate10) / 10000
				} else if p.Mode == "per_call" {
					item["price_micro"] = p.PriceMicro
				}
				if m.Image {
					item["type"] = "image"
					item["image"] = true
				}
				item["description"] = pricingDescription(m, p)
				data = append(data, item)
			}
		}
		// per_token 按量专线（tlinks 等，codex 已单独处理）：专属前缀（线 id/xxx）单独输出——
		// 按量三段计价，任意登录密钥可调（线前缀显式直连，不经统一分组路由）
		for i := range mergedLines {
			l := &mergedLines[i]
			if l.Mode != "per_token" || l.AuthStyle == "codex" {
				continue
			}
			vip := actx != nil && a.userGrpFor(actx.UserID, l.Mode) == "vip"
			for j := range l.Models {
				m := &l.Models[j]
				full := config.ModelFullName(l.ID, m.SiteID)
				p := a.pricingFor(full, "normal")
				if p == nil {
					continue
				}
				if vip {
					if vp := a.pricingFor(full, "vip"); vp != nil {
						p = vp
					}
				}
				// mode/groups 跟价目行走（线内混挂 image 等按次模型时价目 mode=per_call，
				// 标线 mode=per_token 会让前端按量卡错误展示 0 价三段——20260917 实测修复）
				item := map[string]any{
					"id": full, "object": "model",
					"created": created, "owned_by": l.ID, "paid": true,
					"type":   "chat",
					"groups": []string{p.Mode}, "mode": p.Mode,
				}
				if p.Mode == "per_token" {
					item["in_price"] = float64(p.InRate10) / 10000
					item["cache_price"] = float64(p.CacheRate10) / 10000
					item["out_price"] = float64(p.OutRate10) / 10000
				} else if p.Mode == "per_call" {
					item["price_micro"] = p.PriceMicro
				}
				if m.Image {
					item["type"] = "image"
					item["image"] = true
				}
				item["description"] = pricingDescription(m, p)
				data = append(data, item)
			}
		}
	} else {
		// 线前缀直连模式（统一路由未启用）：按线逐条输出
		directLines := a.linesSnap()
		for i := range directLines {
			l := &directLines[i]
			if l.Mode == "free" || freeOnly {
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
					"id": full, "object": "model", "created": created, "owned_by": l.ID,
					"paid":        true,
					"type":        "chat",
					"mode":        p.Mode,
					"price_micro": p.PriceMicro,
					"description": pricingDescription(m, p),
				}
				if p.Mode == "per_token" {
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
			// 固定目录模型一律列出——retired 仅作用于下方动态目录（Dynamic），
			// 20260919 线路独立规矩：商汤 404 抖动不得隐藏 acu 自营目录模型
			id := m.SiteID
			if l.Prefixed {
				// 专属前缀免费线（官方自营品牌框）：acu/ 形式透出；调用侧 NormalizeModel 剥前缀后仍走免费分发
				id = l.ID + "/" + m.SiteID
			}
			item := map[string]any{"id": id, "object": "model", "created": created, "owned_by": l.ID}
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

	// acu/ 众筹专线：独立前缀输出——按官方原版定价从众筹池扣费，所有分组密钥（含纯免费）可见可调；
	// 放在免费模型列表语义下（groups 含 free），池子归零时条目仍透出（调用端 403 引导充值）
	crowdLines := a.linesSnap()
	for i := range crowdLines {
		l := &crowdLines[i]
		if l.Mode != "crowd" {
			continue
		}
		for j := range l.Models {
			m := &l.Models[j]
			full := config.ModelFullName(l.ID, m.SiteID)
			p := a.pricingFor(full, "normal")
			if p == nil {
				continue
			}
			item := map[string]any{
				"id": full, "object": "model",
				"created": created, "owned_by": l.ID, "paid": false,
				"type":   "chat",
				"groups": []string{"free", "crowd"}, "mode": "crowd",
				"pool_billed": true,
			}
			if p.Mode == "per_token" {
				item["in_price"] = float64(p.InRate10) / 10000
				item["cache_price"] = float64(p.CacheRate10) / 10000
				item["out_price"] = float64(p.OutRate10) / 10000
				item["description"] = "众筹按量计费：按 tokens 从公共站点额度扣费（缓存命中价更低），个人余额分文不动"
			} else {
				// 20260917 站长指示：众筹转按次计费后，前端真实展示每次扣费单价（覆盖旧 v5 §3.3 隐藏价目制度）
				item["price_micro"] = p.PriceMicro
				item["description"] = fmt.Sprintf("众筹按次计费：%s 元/次（每次成功请求从站点公共额度扣一次，与生成长度无关，个人余额分文不动）", microToYuanStr(p.PriceMicro))
			}
			data = append(data, item)
		}
	}
	return data
}

// pricingDescription 收费模型对外计费说明（不含成本/通道/折扣率字样）
func pricingDescription(m *config.Model, p *billing.PricingInfo) string {
	if p.Mode == "per_token" {
		return fmt.Sprintf(
			"按量计费：输入 %s / 缓存命中 %s / 输出 %s 元每百万 tokens（先付后用：余额充足方可调用，可在请求中调小 max_tokens 降低单次预扣）",
			trimPrice(float64(p.InRate10)/10000), trimPrice(float64(p.CacheRate10)/10000),
			trimPrice(float64(p.OutRate10)/10000))
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
