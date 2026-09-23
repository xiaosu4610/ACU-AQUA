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

	// 众筹池（acu/ 公共算力池，20260921 站长定稿恢复上线）：
	// acu/ 由"纯免费"改回"众筹"——池子是公共共享钱包，用户充值/划拨注资，
	// acu/ 请求按次从池子扣账（pricing 表 acu/ 生效档），个人余额不动。
	mux.HandleFunc("GET /v1/pool/status", a.handlePoolStatus)
	mux.HandleFunc("GET /v1/pool/flows", a.handlePoolFlows)
	mux.HandleFunc("GET /v1/pool/ranks", a.handlePoolRanks)
	mux.HandleFunc("GET /v1/my/pool/flows", a.handleMyPoolFlows)
	mux.HandleFunc("POST /v1/my/pool/transfer", a.handleMyPoolTransfer)
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
	mux.HandleFunc("PATCH /v1/my/keys/{id}/quota", a.myKeysQuotaSet) // P4 密钥分发配额
	// P5 自定义域名与证书（用户侧）
	mux.HandleFunc("GET /v1/my/domains", a.myDomainsGet)
	mux.HandleFunc("POST /v1/my/domains", a.myDomainsCreate)
	mux.HandleFunc("POST /v1/my/domains/{id}/verify", a.myDomainsVerify)
	mux.HandleFunc("POST /v1/my/domains/{id}/cert/upload", a.myDomainsCertUpload)
	mux.HandleFunc("POST /v1/my/domains/{id}/cert/issue", a.myDomainsCertIssue)
	mux.HandleFunc("DELETE /v1/my/domains/{id}", a.myDomainsDelete)
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
	mux.HandleFunc("POST /v1/admin/lines/{line}/models/{site}/maintenance", a.handleAdminLineModelMaintenance)
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

	// P5 自定义域名管理（管理端：全量查看 + 违规停用）
	mux.HandleFunc("GET /v1/admin/domains", a.adminDomainsGet)
	mux.HandleFunc("POST /v1/admin/domains/{id}/disable", a.adminDomainDisable)

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
	"bad_request":              "invalid_request_error",
	"model_not_found":          "invalid_request_error",
	"model_retired":            "invalid_request_error",
	"not_found":                "invalid_request_error",
	"invalid_api_key":          "authentication_error",
	"unauthorized":             "authentication_error",
	"invalid_credentials":      "authentication_error",
	"admin_disabled":           "permission_error",
	"account_disabled":         "permission_error",
	"official_grp_scope":       "permission_error",
	"free_grp_restricted":      "permission_error",
	"per_call_suspended":       "permission_error",
	"official_line_restricted": "permission_error",
	"insufficient_quota":       "insufficient_quota",
	"rate_limited":             "rate_limit_error",
	"rate_limit_exceeded":      "rate_limit_error",
	"line_busy":                "rate_limit_error",
	"crowd_busy":               "rate_limit_error",
	"crowd_quota_daily":        "rate_limit_error",
	"crowd_pool_empty":         "rate_limit_error",
	"free_rate_limited":        "rate_limit_error",
	"free_busy":                "rate_limit_error",
	"storm_cooldown":           "rate_limit_error",
	"internal_error":           "api_error",
	"upstream_error":           "api_error",
	"upstream_auth":            "api_error",
	"upstream_auth_dead":       "api_error",
	"upstream_no_response":     "api_error",
	"service_unavailable":      "api_error",
	"overloaded_error":         "overloaded_error", // 上游渠道级不可用（OpenAI 官方 type）
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
	if upstream.IsModelUnavailable(body) {
		// 20260919：模型级不可用（上游该模型在本地分组无渠道）优先于渠道级判断——
		// 生产实锤 kabuai 返回 "No available channel for model X under group Y"，
		// 此前 IsChannelExhausted 先命中导致误报 503"渠道暂时没有可用节点"（用户以为等一会就好，
		// 实际是模型维度不可用，换模型才能恢复）。同时含两者措辞时按模型级处理更可行动。
		errOut(w, 404, "model_not_found", "该模型当前不可用（上游无可用渠道），请改用其他模型或联系站长")
		return
	}
	if upstream.IsChannelExhausted(body) {
		// 渠道级不可用是**瞬时故障**（与模型是否下线无关），必须与 404 区分开：
		// 返回 503 overloaded_error（OpenAI 官方 type），引导客户端稍后重试
		errOut(w, 503, "overloaded_error", "上游渠道暂时没有可用节点，请稍后重试；持续出现请联系站长")
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
					"avg_latency_ms": round1(lat),
				}
				if ttft > 0 {
					mm["avg_first_ms"] = round1(ttft) // 首字均延迟（FRT，用户感知口径，前端优先展示）
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

// statusModelNormFree 免费线模型名规范化映射：免费流量 resolved_line 为空且 model 记裸名，
// 需按免费线目录还原成 /v1/models 实际下发的 ID——acu 线 Prefixed=true → "acu/xxx"；
// 公益通道等非前缀免费线 → 保持裸名。与 statusModelNorm（收费线）**必须分开**：
// 裸名 deepseek-v4-flash 在收费线归 aqua/deepseek-v4-flash、在免费线归 acu/deepseek-v4-flash，
// 二者靠 requests.resolved_line 是否为空区分，合并会互相串味。
func (a *App) statusModelNormFree() map[string]string {
	m := map[string]string{}
	for _, l := range a.linesSnap() {
		if l.Mode != "free" {
			continue
		}
		for j := range l.Models {
			sid := l.Models[j].SiteID
			if l.Prefixed {
				m[sid] = l.ID + "/" + sid
			} else {
				m[sid] = sid
			}
		}
	}
	return m
}

// handleModelsStatus 模型实时状态（公开匿名，最近 200 次请求 + 近 6 小时窗口口径）：
// 每模型取最近 6 小时内最近 200 条真实请求，聚合请求数/成功率/平均时延/平均输出速度，供模型中心模型卡片内嵌展示。
// 平均输出速度（tok/s）为流式生成阶段口径（首字之后），并剔除 <3 tok/s 异常样本（短输出/保底估算/非流式总耗时口径）。
// 统计范围：收费线与免费线**都统计**，但按 requests.resolved_line 分流归一化——
// 非空 = 收费线（统一前缀 aqua/、独立前缀 tlk/、codex/）；为空 = 免费分发（acu 自营 → acu/xxx、
// 公益通道 → 裸名）。分流是硬要求：裸名 deepseek-v4-flash 在两条线是不同的站内 ID，
// 合在一起会让免费上游（公益通道 NIM）的抖动污染收费模型状态，反之亦然。
// 成功率剔除与模型健康无关的失败（仅作用 ok=0 行）：
// 400/404/422/429/402（调用方参数错/限流/面值耗尽）、413（请求体过大，上游拒绝）、
// 499/client_cancel（客户端等待期间主动断开）——用户侧问题；
// compensated_prehold（悬空预扣补偿退款，网关侧流水修补）；
// stream_incomplete（上游对长生成 300s 硬切：上游侧正常完成计费、内容多数已送达，渠道固有行为
// 而非模型故障——真正反映健康的是 upstream_error/5xx/网络错）；
// status_code=200 且 error=” 且 ok=0（在途未结算/进程重启被斩的僵尸行，非真实失败）。
// 6h 窗口（rc20）：低频模型的历史失败不再永久拖累状态——窗口外请求自然过期，无近期流量则不出现在列表（前端显示待命中）。
// 状态判定（rc20 放宽）：样本≥10 时 ≥99.5% 状态极佳 / ≥95% 正常 / ≥85% 部分异常 / 其余故障；
// 小样本有失败最多判"部分异常"，不下重判。
// 只输出聚合运行指标，不含成本/渠道/用户信息；模型名统一规范化后合并聚合。
func (a *App) handleModelsStatus(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`
		SELECT model, resolved_line, COUNT(*), COALESCE(SUM(ok),0),
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
			SELECT model, resolved_line, ok, latency_ms, tps, ts, status_code, error, first_ms,
			       ROW_NUMBER() OVER (PARTITION BY model, resolved_line ORDER BY rowid DESC) rn
			FROM (SELECT rowid, model, resolved_line, ok, latency_ms, tps, ts, status_code, error, first_ms
		      FROM requests WHERE endpoint IN ('chat','images')
		        AND ts > strftime('%s','now') - 21600
		      ORDER BY rowid DESC LIMIT 50000)
		) WHERE rn <= 200 GROUP BY model, resolved_line`)
	if err != nil {
		errOut(w, 500, "internal_error", "查询失败")
		return
	}
	defer rows.Close()
	normPaid := a.statusModelNorm()
	normFree := a.statusModelNormFree()
	type stAgg struct {
		total, okN, userErr, latN, tpsN, lastTs int64
		latSum, tpsSum                          float64
		firstSum, firstN                        int64
	}
	agg := map[string]*stAgg{}
	order := []string{}
	for rows.Next() {
		var model, rline string
		var total, okN, userErr, latN, tpsN, lastTs, firstSum, firstN int64
		var latSum, tpsSum float64
		if rows.Scan(&model, &rline, &total, &okN, &userErr, &latSum, &latN, &firstSum, &firstN, &tpsSum, &tpsN, &lastTs) != nil {
			continue
		}
		// 归一化按流量归属分流：resolved_line 非空 = 收费线（含 tlk/codex/统一前缀）；
		// 为空 = 免费分发（acu 自营 / 公益通道），二者同名模型必须落到不同站内 ID
		norm := normPaid
		if rline == "" {
			norm = normFree
		}
		name, ok := norm[model]
		if !ok {
			continue // 不在目录内的未知模型不输出
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
		// 20260919 站长定稿：对外只暴露**客观性能数据**（FRT 首字延迟 / TPS 每秒 tokens），
		// 不再下发健康评级（status）与成功率（ok_rate）——避免主观评分误导选型。
		// 注意：内部统计（model_health 表）保持不变，auto 智能路由仍按成功率筛候选
		// （见 free.go autoCandidates 直查 DB，不依赖本响应），此处仅收敛对外字段。
		it := map[string]any{
			"model": name, "samples": g.total, "last_ts": g.lastTs, "sample_size": 200,
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
// 20260919 修复：倍率走 siteOverride（settings 表优先，toml 兜底）——此前直读 toml，
// 后台改倍率只影响 /v1/meta 横幅、不影响划线折算，生产实测曾把 ¥1/M 的 5 折价标成 ¥10/M（放大 10 倍）。
func (a *App) promoBase(p *billing.PricingInfo, isVip bool) *billing.PricingInfo {
	if p == nil || p.EndsAt <= 0 {
		return nil
	}
	rs := a.siteOverride("rate_promo", a.Cfg.Site.RatePromo)
	if isVip {
		rs = a.siteOverride("rate_promo_vip", a.Cfg.Site.RatePromoVip)
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

// —— 展示价与实收价分离（20260923 站长指令）——

// chargeRates 常态扣费倍率（线 ID → 倍率）。命中时 /v1/models 额外下发：
//
//	charge_rate    实际扣费倍率（前端在卡片右上角展示，如 0.5×）
//	base_*_price   官方原价（= 实收价 / 倍率，供前端把主价显示为官方原价）
//
// 语义：**卡片展示官方原价、实收按倍率打折**——展示价与扣费价刻意不一致。
// 与 promoBase（限时促销，价目行带到期时间）的区别：本表是长期常态，无到期时间。
//
// 20260924 起**清空**：prime（TokenLinks 国模）已恢复官方原价，折扣不再走代码倍率，
// 改由「2 号钱包」价目组（pricing.grp='wallet2'）承载（见 wallet2 方案）。
var chargeRates = map[string]float64{}

// externalLineID 外模线 ID：其模型在前端「外模专线」板块单独展示。
// 仅影响展示分组，不动密钥分组（用户仍是「免费 + 按量计费」）与调用路由。
const externalLineID = "kiro"

// rateRatio 折扣倍率 = 实收价 / 官方原价（保留 3 位小数，如 0.5）。
// 用于 2 号折扣钱包模型：倍率由两行价目**算出来**，不再硬编码在 chargeRates。
// 实收 ≥ 原价或任一侧 ≤0 时返回 0（不展示倍率角标，避免"1×"这类无意义标注）。
func rateRatio(actual, base int64) float64 {
	if base <= 0 || actual <= 0 || actual >= base {
		return 0
	}
	return math.Round(float64(actual)/float64(base)*1000) / 1000
}

// modelListEntries 全量模型条目（/v1/models 与 /v1/models/{id} 共用）
func (a *App) modelListEntries(actx *auth.Ctx, created int64) []map[string]any {
	data := []map[string]any{{
		"id": "auto", "object": "model", "created": created, "owned_by": "acu",
		"auto":        true,
		"description": "智能自动路由：每次请求实时选择当前成功率最高、响应最快的模型，快与稳优先，不保证每次命中同一模型",
	}}

	// 20260919 站长定稿：不再下发健康评分（health）——前端只展示 FRT/TPS 客观指标。
	// 内部 model_health 表与 auto 路由候选筛选保持不变（直查 DB，与此无关）。
	retired := a.retiredUpstreams()
	// 纯免费分组密钥：收费模型对其不可见不可调（列表同步隐藏，调用端 403 拦截）
	freeOnly := actx != nil && actx.KeyGrp == "free"

	// 收费线：实时价目（DB pricing 表为准）；下架（normal 价过期）即从列表消失，VIP 回退 vip 组
	if a.Cfg.Billing.UnifiedPrefix != "" && !freeOnly {
		// 统一前缀模式：全部收费线模型合并为 前缀/site_id，groups 标注可用计费分组
		// （同 ID 在按次/按量线都存在 → 两条分组都列出；计费方式由密钥分组决定）
		type grpPrice struct {
			mode    string
			lineID  string // 来源线 ID（20260923：常态倍率与外模分组的判定依据）
			m       *config.Model
			p       *billing.PricingInfo // 用户实付价目（vip/agent 分组价；否则 normal 价）
			base    *billing.PricingInfo // 原价（normal）——仅 vip/agent 分组价存在时携带，供前端划线对比
			agent   bool                 // 代理拿货价视角（前端渲染「代理拿货价」徽标与注释）
			wallet2 bool                 // 2 号折扣钱包专用模型（20260924）：实收取 wallet2 价目行
		}
		merged := map[string][]grpPrice{}
		var order []string
		mergedLines := a.linesSnap()
		for i := range mergedLines {
			l := &mergedLines[i]
			// 20260919：per_token 线改为**参与统一前缀合并**（站长要求"按量付费也以 aqua/ 前缀展示"）——
			// 按量专线模型与按次线同名时合并出 groups=['per_call','per_token']，前端两个分组各取所需；
			// 仅按量的模型（如官方原版中转）groups=['per_token']，由按量分组密钥调用。
			// 仍排除：official（tlk 独占）/ crowd（acu 免费，前缀独立）/ codex（GPT 账号池，独立前缀）。
			if l.Mode == "free" || l.Mode == "official" || l.Mode == "crowd" || l.AuthStyle == "codex" {
				continue
			}
			ugrp := ""
			if actx != nil {
				ugrp = a.userGrpFor(actx.UserID, l.Mode)
			}
			vip := ugrp == "vip"
			agent := ugrp == "agent" // 代理拿货价视角（20260919 代理体系）
			for j := range l.Models {
				m := &l.Models[j]
				if m.Maintenance {
					continue // 软下架（20260923）：维护中模型不对外列出；DB 记录保留，清标记即回归
				}
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
					merged[m.SiteID] = append(merged[m.SiteID], grpPrice{mode: p.Mode, lineID: l.ID, m: m, p: p})
					continue
				}
				// —— 2 号折扣钱包专用模型（20260924 站长指令）——
				// 判据与 chat.go 完全同源：存在 pricing(model,'wallet2') 行 = 该模型属折扣钱包专用。
				// 实收价改取 wallet2 行（当前 = 官方原价 × 0.5），normal 行降级为「划线官方原价」；
				// 折扣倍率由**价目行对比**算出（不再硬编码 chargeRates），加一行价目即开通一个模型。
				// vip/agent 分组价对该类模型不适用（折扣钱包不区分分组），故此处直接 continue。
				if w2 := a.pricingFor(full, "wallet2"); w2 != nil {
					if _, ok := merged[m.SiteID]; !ok {
						order = append(order, m.SiteID)
					}
					merged[m.SiteID] = append(merged[m.SiteID], grpPrice{
						mode: w2.Mode, lineID: l.ID, m: m, p: w2, base: p, wallet2: true,
					})
					continue
				}
				if _, ok := merged[m.SiteID]; !ok {
					order = append(order, m.SiteID)
				}
				if agent {
					// 代理拿货价为主价 + normal 原价划线对比；agent 行缺失回退 normal（不 404）
					if ap := a.pricingFor(full, "agent"); ap != nil {
						merged[m.SiteID] = append(merged[m.SiteID], grpPrice{mode: ap.Mode, lineID: l.ID, m: m, p: ap, base: p, agent: true})
						continue
					}
				}
				if vip {
					if vp := a.pricingFor(full, "vip"); vp != nil {
						// VIP 拿货价为主 + 附原价（normal），前端底部展示对比
						merged[m.SiteID] = append(merged[m.SiteID], grpPrice{mode: vp.Mode, lineID: l.ID, m: m, p: vp, base: p})
						continue
					}
				}
				merged[m.SiteID] = append(merged[m.SiteID], grpPrice{mode: p.Mode, lineID: l.ID, m: m, p: p})
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
				// 原价对照三档（优先级从高到低）：
				// ① 常态扣费倍率（20260923 站长指令）：国模卡片**展示官方原价**、右上角标倍率，
				//    实收 = 官方价 × 倍率 —— 展示价与扣费价刻意不一致。官方原价由实收价反推
				//    （base = 实收 / 倍率），无需另存价目；前端据 charge_rate 把主价显示为官方原价。
				// ② 限时促销：价目行带截止时间 → 按 rate_promo 折算官方原价（所有用户下发，前端划线展示）
				// ③ VIP：附 normal 行原价供前端底部展示对比
				if pt.wallet2 && pt.base != nil {
					// 2 号折扣钱包专用（20260924）：实收 = wallet2 价目行；base_* = normal 官方原价。
					// 折扣倍率由**两行价目对比**算出（不再硬编码 chargeRates）：加一行 wallet2
					// 价目即开通一个折扣模型，倍率随价目自动变化。
					if rate := rateRatio(pt.p.InRate10, pt.base.InRate10); rate > 0 {
						item["charge_rate"] = rate
					}
					item["wallet2_only"] = true
					item["base_floor_micro"] = pt.base.FloorMicro
					item["base_in_price"] = float64(pt.base.InRate10) / 10000
					item["base_cache_price"] = float64(pt.base.CacheRate10) / 10000
					item["base_out_price"] = float64(pt.base.OutRate10) / 10000
				} else if rate := chargeRates[pt.lineID]; rate > 0 {
					div := func(v int64) int64 { return int64(math.Round(float64(v) / rate)) }
					item["charge_rate"] = rate
					item["base_in_price"] = float64(div(pt.p.InRate10)) / 10000
					item["base_cache_price"] = float64(div(pt.p.CacheRate10)) / 10000
					item["base_out_price"] = float64(div(pt.p.OutRate10)) / 10000
				} else if pb := a.promoBase(pt.p, pt.base != nil); pb != nil {
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
				item["description"] = pricingDescription(pc.m, pc.p, chargeRates[pc.lineID])
			} else if pt != nil {
				desc := pricingDescription(pt.m, pt.p, chargeRates[pt.lineID])
				if pt.wallet2 {
					// 折扣钱包专用：必须点明"要用 2 号钱包余额"，否则用户用主钱包调会直接 429
					desc += "；需使用「折扣钱包」（2 号钱包）余额调用——两钱包资金独立，不支持互转"
				}
				item["description"] = desc
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
			// 代理拿货价视角标记：前端据此渲染「代理拿货价」徽标 + 原价划线对比 + 利润注释
			for k := range gs {
				if gs[k].agent {
					item["price_view"] = "agent"
					break
				}
			}
			// 外模专线（20260923 站长指令）：外模（Kiro）模型在前端单独成「外模专线」板块。
			// 仅影响展示分组——密钥分组（用户仍是「免费 + 按量计费」）与调用路由完全不变。
			for k := range gs {
				if gs[k].lineID == externalLineID {
					item["section"] = "external"
					break
				}
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
				if m.Maintenance {
					continue // 软下架（20260923）：维护中模型不对外列出
				}
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
					"description": pricingDescription(m, p, chargeRates[l.ID]),
				})
			}
		}
		// codex（GPT 账号池）线：专属前缀（codex/xxx）单独输出——按量分段计价，任意登录密钥可调
		for i := range mergedLines {
			l := &mergedLines[i]
			if l.AuthStyle != "codex" {
				continue
			}
			// 20260919 自动下线：全部账号余量耗尽（used_pct>=99 或已判死）时，
			// 整个 codex 段跳过——模型从 /v1/models 消失，前端 section 自动不渲染。
			// 兜底：查询失败/无账号记录时保守**不隐藏**（避免误下线把可用服务摘掉）。
			if a.codexExhausted(l.ID) {
				log.Printf("[codex] line=%s 全部账号余量耗尽，已自动下线其模型（补充余量后自动恢复）", l.ID)
				continue
			}
			vip := actx != nil && a.userGrpFor(actx.UserID, l.Mode) == "vip"
			for j := range l.Models {
				m := &l.Models[j]
				if m.Maintenance {
					continue // 软下架（20260923）：维护中模型不对外列出
				}
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
				item["description"] = pricingDescription(m, p, chargeRates[l.ID])
				data = append(data, item)
			}
		}
		// per_token 按量专线：20260919 起已并入上方统一前缀合并（aqua/ 前缀），
		// 此处不再单独输出，避免同一模型双份下发（站长要求"按量也以 aqua/ 前缀展示"）。
		// 保留 codex 独立段（下方）与 official 段（上方）不变。
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
				if m.Maintenance {
					continue // 软下架（20260923）：维护中模型不对外列出；DB 记录保留，清标记即回归
				}
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
					"description": pricingDescription(m, p, chargeRates[l.ID]),
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
			// 固定目录模型默认一律列出——retired 只作用于动态目录，
			// 20260919 线路独立规矩：上游 404 抖动不得隐藏自营目录模型。
			// 例外（20260922）：**dynamic 免费线**（NVIDIA 公益通道）的"配置目录"同样会被上游
			// 悄悄下架（模型仍在 admin_line_models，但上游 /chat/completions 恒 404/410）；
			// 这些条目已由每轮目录探针**实测判死**（404/410 → hits>=2），故与动态目录同口径隐藏。
			// 生产实锤：deepseek-v4-flash-0731 在配置目录里、上游已无 → 目录列着但调用恒 410。
			if l.Dynamic && m.UpstreamID != "" && retired[m.UpstreamID] {
				continue
			}
			id := m.SiteID
			if l.Prefixed {
				// 专属前缀免费线（官方自营品牌框）：acu/ 形式透出；调用侧 NormalizeModel 剥前缀后仍走免费分发
				id = l.ID + "/" + m.SiteID
			}
			item := map[string]any{"id": id, "object": "model", "created": created, "owned_by": l.ID}
			if m.Image {
				item["type"] = "image" // 特殊计费类型下发（免费文生图等），前端零硬编码
			}
			data = append(data, item)
		}
		if l.Dynamic {
			for _, item := range a.dynamicModels(l.ID, configured, retired) {
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
			if m.Maintenance {
				continue // 软下架（20260923）：维护中模型不对外列出（众筹池扣费模型同样适用）
			}
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

// pricingDescription 价目描述（下发给客户端的 description；不含成本/通道等内部口径）。
//
// displayRate 展示倍率（0 或 ≥1 = 无倍率，按实收价描述）：
// 20260923 展示价与实收价分离——命中线级常态倍率（chargeRates）时，卡片主价显示的是
// **官方原价**（实收 ÷ 倍率），描述若仍按实收价写会出现「同一模型两个价」的自相矛盾。
// 故此处按 displayRate 反推官方原价来写，并显式注明实际结算折扣。
func pricingDescription(m *config.Model, p *billing.PricingInfo, displayRate float64) string {
	rate, discount := displayRate, ""
	if rate > 0 && rate < 1 {
		discount = fmt.Sprintf("；卡片展示官方原价，实际按官方原价 %s 倍结算", trimPrice(rate))
	} else {
		rate = 1
	}
	perM := func(v int64) string { return trimPrice(float64(v) / rate / 10000) }
	if p.Mode == "per_token" {
		return fmt.Sprintf(
			"按量计费：输入 %s / 缓存命中 %s / 输出 %s 元每百万 tokens（先付后用：余额充足方可调用，可在请求中调小 max_tokens 降低单次预扣）%s",
			perM(p.InRate10), perM(p.CacheRate10), perM(p.OutRate10), discount)
	}
	if m.Image {
		return fmt.Sprintf("%s 元/张，按张计费（先付后用，n 参数控制张数）%s", microToYuanStr(int64(float64(p.PriceMicro)/rate)), discount)
	}
	return fmt.Sprintf("预充值按次计费：%s 元/次（先付后用：余额充足方可调用）%s", microToYuanStr(int64(float64(p.PriceMicro)/rate)), discount)
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
