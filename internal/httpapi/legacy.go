// Package httpapi legacy 反向代理（绞杀者迁移模式）：
// 收费线（line-id/ 前缀模型）由 Go 原生处理；免费模型与其余端点反代到 Rust 旧网关，
// 两个进程共享同一 SQLite 生产库——用户、余额、密钥、流水天然无缝衔接。
// 待免费线移植完成后，去掉 legacy_upstream_url 即可整体下线 Rust。
package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// legacyProxy 旧网关反向代理（SSE 即时 flush）
var legacyProxy *httputil.ReverseProxy

func initLegacy(base string) {
	if base == "" || legacyProxy != nil {
		return
	}
	u, err := url.Parse(base)
	if err != nil {
		return
	}
	legacyProxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(u)
			pr.Out.Host = u.Host
		},
		FlushInterval: -1, // 流式响应逐写即时下发
		Transport: &http.Transport{
			MaxIdleConns:        64,
			MaxIdleConnsPerHost: 64,
			IdleConnTimeout:     90 * time.Second,
			ResponseHeaderTimeout: 660 * time.Second,
		},
	}
}

// handleLegacy catch-all：Go 未注册的路径全部反代旧网关（usage/hooks/pay 等）
func (a *App) handleLegacy(w http.ResponseWriter, r *http.Request) {
	if legacyProxy == nil {
		errOut(w, 404, "not_found", "接口不存在")
		return
	}
	legacyProxy.ServeHTTP(w, r)
}

// proxyChat 非收费模型（无 line-id/ 前缀）整请求转发旧网关（免费线由 Rust 继续承接）
func (a *App) proxyChat(w http.ResponseWriter, r *http.Request, body []byte) {
	if legacyProxy == nil {
		errOut(w, 404, "model_not_found", "模型不存在：" + readModelName(body))
		return
	}
	u, _ := url.Parse(a.Cfg.Server.LegacyUpstreamURL)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, u.String()+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		errOut(w, 502, "upstream_error", "转发失败，请稍后重试")
		return
	}
	req.Header = r.Header.Clone()
	resp, err := legacyProxy.Transport.RoundTrip(req)
	if err != nil {
		errOut(w, 502, "upstream_error", "免费通道暂时不可用，请稍后重试")
		return
	}
	defer resp.Body.Close()
	// 复制响应头，但剥离 CORS 头——原生路由已由 corsGate 注入，
	// Rust 也回一套，重复头会被浏览器判 CORS 违规（ERR_FAILED）
	for k, vs := range resp.Header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	// SSE/流式必须逐块下发：io.Copy 会被 4KB 缓冲拖住，小帧积压导致
	// 客户端长时间收不到数据（表现为"流式卡死"）。每块写后立即 Flush。
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}

// readModelName 从请求体读模型名（仅错误提示用，失败返回空）
func readModelName(body []byte) string {
	var m struct {
		Model string `json:"model"`
	}
	_ = jsonUnmarshal(body, &m)
	return m.Model
}
