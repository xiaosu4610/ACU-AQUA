// Package httpapi 路由与 handler。net/http 1.22+ 方法路由，零框架依赖。
package httpapi

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"acu-aqua/gateway/internal/auth"
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
	mux.HandleFunc("POST /v1/chat/completions", a.handleChat)
	mux.HandleFunc("POST /v1/images/generations", a.handleImages)
	mux.HandleFunc("GET /v1/images/file/{id}", a.handleImageFile)

	// 用户面
	mux.HandleFunc("POST /v1/user/register", a.handleRegister)
	mux.HandleFunc("POST /v1/user/login", a.handleLogin)
	mux.HandleFunc("POST /v1/user/logout", a.handleLogout)
	mux.HandleFunc("GET /v1/user/me", a.handleMe)
	mux.HandleFunc("GET /v1/user/flows", a.handleFlows)
	mux.HandleFunc("POST /v1/user/keys", a.handleCreateKey)
	mux.HandleFunc("GET /v1/user/keys", a.handleListKeys)
	mux.HandleFunc("DELETE /v1/user/keys/{id}", a.handleRevokeKey)

	// 其余全部反代旧网关（免费线/usage/hooks/pay/auth/my/admin 等，绞杀者迁移）。
	// 注意：管理后台（/v1/admin/*）整体由旧网关承接——前端管理页的响应形状
	// （core/ledger/summary、page 分页、quota/audit/supervision/reconcile）与
	// Rust 版强耦合，待按其真实响应形状精确移植后再接管，避免后台不可用。
	mux.HandleFunc("/", a.handleLegacy)
	return accessLog(corsGate(mux))
}

// corsGate CORS 门卫：Go 原生路由注入 CORS 头（与 Rust 版口径一致：
// allow-origin * / GET,POST,DELETE,OPTIONS / Authorization,Content-Type,x-api-key）。
// 反代路径由旧网关自带回 CORS 头，此处不注入，避免重复头被浏览器拒绝。
// OPTIONS 预检请求方法不匹配原生路由 → 落入 catch-all 反代旧网关应答（Rust 全局处理 OPTIONS）。
func corsGate(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "/" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, x-api-key")
		}
		h.ServeHTTP(w, r)
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
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusWriter{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(rec, r)
		ip := r.Header.Get("X-Real-Ip")
		if ip == "" {
			ip = r.RemoteAddr
		}
		log.Printf("[http] %s %s %s -> %d %s", ip, r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	})
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
	"insufficient_quota": "insufficient_quota",
	"rate_limit_exceeded": "rate_limit_error",
	"internal_error":     "api_error",
	"upstream_error":     "api_error",
	"service_unavailable": "api_error",
}

// errOut 站点统一错误（国际标准 OpenAI 风格；信息隔离：不透传上游原文）
func errOut(w http.ResponseWriter, code int, ecode, msg string) {
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

// handleStatus 存活探测
func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]any{"ok": true, "service": "aqua-gateway-go"})
}

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

// handleModels 模型列表（价格展示；成本率绝不出现）
func (a *App) handleModels(w http.ResponseWriter, r *http.Request) {
	actx := auth.Authenticate(a.DB.DB, r)
	var data []map[string]any
	for i := range a.Cfg.Lines {
		l := &a.Cfg.Lines[i]
		grp := "normal"
		if actx != nil {
			grp = a.userGrpFor(actx.UserID, l.Mode)
		}
		for j := range l.Models {
			m := &l.Models[j]
			full := config.ModelFullName(l.ID, m.SiteID)
			// 实时价格：仅列出当前生效价的模型——下架（pricing 行过期）即从列表消失
			pricing := a.pricingFor(full, grp)
			if pricing == nil {
				continue
			}
			item := map[string]any{
				"id": full, "object": "model", "owned_by": l.ID, "mode": l.Mode,
			}
			{
				if pricing.Mode == "per_token" {
					item["in_price"] = float64(pricing.InRate10) / 10000
					item["cache_price"] = float64(pricing.CacheRate10) / 10000
					item["out_price"] = float64(pricing.OutRate10) / 10000
					item["price_micro"] = pricing.PriceMicro
					item["floor_micro"] = pricing.FloorMicro
				} else {
					item["price_micro"] = pricing.PriceMicro
					if m.Image {
						item["image"] = true
						item["per_image"] = pricing.PriceMicro
					}
				}
			}
			data = append(data, item)
		}
	}
	jsonOut(w, 200, map[string]any{"object": "list", "data": data})
}

// bearerToken 提取 Authorization: Bearer xxx
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):])
	}
	return ""
}
