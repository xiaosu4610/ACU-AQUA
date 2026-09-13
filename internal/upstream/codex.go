// ———————— Codex（ChatGPT 账号 OAuth）适配层 ————————
// auth_style="codex" 的线：keys 存各账号的 refresh_token（rt.1.xxx），
// base_url=https://chatgpt.com/backend-api/codex。
// 站内 chat/completions 请求在此转成 Responses API（内部统一走 SSE），
// 响应反向转换为 chat/completions（流式逐帧 / 非流式聚合），对上层
// （Do/DoKey/serveStreamChat/serveJSONChat）完全透明。
// 账号级语义映射：401/403 → 判死换号（账号封禁）；429 → 账号限流同钥退避。
package upstream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// codexClientID Codex CLI 官方 OAuth client_id（access_token JWT 内确认）
const codexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

// codexUserAgent 模拟 Codex CLI UA（chatgpt.com 的 Cloudflare 防护放行该指纹）
const codexUserAgent = "codex_cli_rs/0.55.0"

// codexTok 一个账号的令牌态
type codexTok struct {
	at  string    // access_token
	exp time.Time // 过期时刻（提前 5 分钟判过期）
	rt  string    // 当前 refresh_token（上游轮换后更新并持久化）
}

// codexToks 全局令牌表：key = lineID + "/" + keyIdx
var codexToks sync.Map

// codexRTMu 新 refresh_token 持久化文件锁
var codexRTMu sync.Mutex

// codexRTFile 轮换后的 refresh_token 持久化文件（DB 里的原 RT 保留不动，
// 文件里存最新 RT，重启后优先生效；env AQUA_CODEX_RT_FILE 可覆盖）
func codexRTFile() string {
	if p := os.Getenv("AQUA_CODEX_RT_FILE"); p != "" {
		return p
	}
	if st, err := os.Stat("/data/aqua/data"); err == nil && st.IsDir() {
		return "/data/aqua/data/codex-rt.json"
	}
	return "codex-rt.json"
}

// codexLoadSavedRT 读取轮换后的新 RT（无则空串）
func codexLoadSavedRT(lineID string, idx int) string {
	b, err := os.ReadFile(codexRTFile())
	if err != nil {
		return ""
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m[fmt.Sprintf("%s/%d", lineID, idx)]
}

// codexSaveRT 轮换后的 RT 覆盖持久化
func codexSaveRT(lineID string, idx int, rt string) {
	codexRTMu.Lock()
	defer codexRTMu.Unlock()
	p := codexRTFile()
	m := map[string]string{}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	m[fmt.Sprintf("%s/%d", lineID, idx)] = rt
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
		if b, err := json.MarshalIndent(m, "", "  "); err == nil {
			_ = os.WriteFile(p, b, 0o600)
		}
	}
}

// codexAuthClient 令牌刷新专用 client（短超时，走线代理）
func (c *Client) codexAuthClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, Transport: c.HTTP.Transport}
}

// refreshCodexToken 用 refresh_token 换新 access_token（auth.openai.com）
func refreshCodexToken(ctx context.Context, hc *http.Client, rt string) (*codexTok, error) {
	payload, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     codexClientID,
		"refresh_token": rt,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://auth.openai.com/oauth/token", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		ExpiresIn        int64  `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("刷新响应解析失败: %w", err)
	}
	if out.Error != "" || resp.StatusCode != 200 {
		return nil, fmt.Errorf("RT 刷新失败(%d): %s %s", resp.StatusCode, out.Error, out.ErrorDescription)
	}
	expSec := out.ExpiresIn
	if expSec <= 0 {
		expSec = 3600
	}
	newRT := out.RefreshToken
	if newRT == "" {
		newRT = rt // 上游未轮换时沿用
	}
	return &codexTok{at: out.AccessToken, exp: time.Now().Add(time.Duration(expSec-300) * time.Second), rt: newRT}, nil
}

// getAT 取账号 access_token：内存缓存 → 持久化新 RT → 原始 RT，逐级回退
func (c *Client) getAT(ctx context.Context, k *KeyState) (string, error) {
	cacheKey := fmt.Sprintf("%s/%d", c.Line.ID, k.Idx)
	if v, ok := codexToks.Load(cacheKey); ok {
		t := v.(*codexTok)
		if time.Now().Before(t.exp) {
			return t.at, nil
		}
	}
	rt := k.Key
	if saved := codexLoadSavedRT(c.Line.ID, k.Idx); saved != "" && saved != k.Key {
		if t, err := refreshCodexToken(ctx, c.codexAuthClient(), saved); err == nil {
			codexToks.Store(cacheKey, t)
			codexSaveRT(c.Line.ID, k.Idx, t.rt)
			return t.at, nil
		}
		log.Printf("[codex] line=%s key=%d 持久化 RT 失效，回退原始 RT", c.Line.ID, k.Idx)
	}
	t, err := refreshCodexToken(ctx, c.codexAuthClient(), rt)
	if err != nil {
		return "", err
	}
	codexToks.Store(cacheKey, t)
	codexSaveRT(c.Line.ID, k.Idx, t.rt)
	return t.at, nil
}

// codexInReq 站内 chat/completions 请求（只取需要的字段）
type codexInReq struct {
	Model       string          `json:"model"`
	Messages    []codexInMsg    `json:"messages"`
	MaxTokens   int64           `json:"max_tokens"`
	Temperature *float64        `json:"temperature"`
	Stream      bool            `json:"stream"`
	Stop        json.RawMessage `json:"stop"`
}

type codexInMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string 或 [{type,text}]
}

// codexMsgText 提取消息文本：string 直取；数组拼 text 项（image_url 等暂不支持）
func codexMsgText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var arr []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return "", fmt.Errorf("消息内容格式不支持")
	}
	var b strings.Builder
	for _, p := range arr {
		switch p.Type {
		case "text", "input_text", "output_text":
			b.WriteString(p.Text)
		case "image_url", "input_image":
			return "", fmt.Errorf("codex 线暂不支持图片输入")
		}
	}
	return b.String(), nil
}

// chatToResponses chat/completions → Responses API 请求体
// （内部统一 stream=true 请求上游；非流式在本地聚合）
func chatToResponses(body []byte) ([]byte, error) {
	var in codexInReq
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("请求体解析失败: %w", err)
	}
	var sys strings.Builder
	items := []map[string]any{}
	for _, m := range in.Messages {
		text, err := codexMsgText(m.Content)
		if err != nil {
			return nil, err
		}
		switch m.Role {
		case "system", "developer":
			if sys.Len() > 0 {
				sys.WriteString("\n\n")
			}
			sys.WriteString(text)
		case "assistant":
			items = append(items, map[string]any{
				"type": "message", "role": "assistant",
				"content": []map[string]any{{"type": "output_text", "text": text}},
			})
		default: // user / tool 等一律按 user 处理
			items = append(items, map[string]any{
				"type": "message", "role": "user",
				"content": []map[string]any{{"type": "input_text", "text": text}},
			})
		}
	}
	out := map[string]any{
		"model":        in.Model,
		"instructions": sys.String(),
		"input":        items,
		"stream":       true,
		"store":        false,
	}
	if in.MaxTokens > 0 {
		out["max_output_tokens"] = in.MaxTokens
	}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	return json.Marshal(out)
}

// codexChatChunk chat/completions 流式帧
func codexChatChunk(id, model, content string, role string, finish string, usage *codexUsage) map[string]any {
	d := map[string]any{}
	if role != "" {
		d["role"] = role
	}
	if content != "" {
		d["content"] = content
	}
	ch := map[string]any{"index": int64(0), "delta": d}
	if finish != "" {
		ch["finish_reason"] = finish
		ch["delta"] = map[string]any{}
	}
	m := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(),
		"model": model, "choices": []any{ch},
	}
	if usage != nil {
		m["usage"] = usage.toChat()
	}
	return m
}

// codexUsage Responses usage → chat/completions usage（缓存命中透传，三段计费用）
type codexUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func (u *codexUsage) toChat() map[string]any {
	return map[string]any{
		"prompt_tokens":     u.InputTokens,
		"completion_tokens": u.OutputTokens,
		"total_tokens":      u.TotalTokens,
		"prompt_tokens_details": map[string]any{
			"cached_tokens": u.InputTokensDetails.CachedTokens,
		},
	}
}

// sseLineScanner SSE 行扫描器：line 为当前行（无行尾），data 为 data: 行的 JSON payload。
// 手写 ReadBytes 循环不限行长（response.completed 帧含全文，可达数十 KB）。
type sseLineScanner struct {
	br   *bufio.Reader
	line []byte
	data []byte
	done bool
}

func newSSEScanner(r io.Reader) *sseLineScanner {
	return &sseLineScanner{br: bufio.NewReaderSize(r, 64<<10)}
}

func (s *sseLineScanner) next() bool {
	if s.done {
		return false
	}
	line, err := s.br.ReadBytes('\n')
	if len(line) == 0 && err != nil {
		s.done = true
		return false
	}
	s.line = bytes.TrimRight(line, "\r\n")
	s.data = nil
	if bytes.HasPrefix(s.line, []byte("data: ")) {
		s.data = bytes.TrimSpace(bytes.TrimPrefix(s.line, []byte("data: ")))
	} else if bytes.Equal(bytes.TrimSpace(s.line), []byte("data:[DONE]")) {
		s.data = []byte("[DONE]")
	}
	if err != nil {
		s.done = true // EOF 前最后一行也返回
	}
	return true
}

// codexPipeStream 读取上游 Responses SSE → 输出 chat/completions SSE（含 [DONE]）。
// 返回聚合文本与 usage（供非流式聚合/日志）。
func codexPipeStream(r io.Reader, w io.Writer) (string, *codexUsage, error) {
	var text strings.Builder
	var usage *codexUsage
	var respID, model string
	sentRole := false
	emit := func(v map[string]any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	finish := func(ferr error) (string, *codexUsage, error) {
		if ferr == nil {
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
		return text.String(), usage, ferr
	}
	sc := newSSEScanner(r)
	for sc.next() {
		data := sc.data
		if data == nil || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.created", "response.in_progress":
			if respID == "" {
				var full struct {
					Response struct {
						ID    string `json:"id"`
						Model string `json:"model"`
					} `json:"response"`
				}
				if json.Unmarshal(data, &full) == nil {
					respID, model = full.Response.ID, full.Response.Model
				}
			}
		case "response.output_text.delta":
			var d struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal(data, &d) != nil || d.Delta == "" {
				continue
			}
			if !sentRole {
				sentRole = true
				emit(codexChatChunk(respID, model, "", "assistant", "", nil))
			}
			text.WriteString(d.Delta)
			emit(codexChatChunk(respID, model, d.Delta, "", "", nil))
		case "response.completed", "response.incomplete":
			var full struct {
				Response struct {
					ID    string      `json:"id"`
					Model string      `json:"model"`
					Usage *codexUsage `json:"usage"`
				} `json:"response"`
			}
			if json.Unmarshal(data, &full) == nil && full.Response.Usage != nil {
				usage = full.Response.Usage
			}
			if respID == "" {
				respID, model = full.Response.ID, full.Response.Model
			}
			emit(codexChatChunk(respID, model, "", "", "stop", usage))
			return finish(nil)
		case "response.failed", "error":
			var e struct {
				Response *struct {
					Error *struct {
						Message string `json:"message"`
						Code    string `json:"code"`
					} `json:"error"`
				} `json:"response"`
				Message string `json:"message"`
				Code    string `json:"code"`
			}
			_ = json.Unmarshal(data, &e)
			msg := e.Message
			code := e.Code
			if msg == "" && e.Response != nil && e.Response.Error != nil {
				msg, code = e.Response.Error.Message, e.Response.Error.Code
			}
			if msg == "" {
				msg = "codex 上游返回失败"
			}
			emit(map[string]any{"error": map[string]any{
				"message": msg, "type": "api_error", "code": code, "param": nil,
			}})
			return finish(fmt.Errorf("codex_error: %s", msg))
		}
	}
	// 上游断流未正常收尾
	if !sentRole {
		emit(map[string]any{"error": map[string]any{
			"message": "codex 上游流式响应中断", "type": "api_error", "code": "stream_incomplete", "param": nil,
		}})
		return finish(fmt.Errorf("codex_stream_incomplete"))
	}
	// 已有部分内容但没收到 completed：补 finish 帧 + [DONE]
	emit(codexChatChunk(respID, model, "", "", "stop", usage))
	return finish(nil)
}

// codexAggregate 非流式：直接解析上游 Responses SSE，聚合为 chat.completion JSON
func codexAggregate(r io.Reader, model string) ([]byte, error) {
	var sb strings.Builder
	var usage *codexUsage
	var failed error
	sc := newSSEScanner(r)
	for sc.next() {
		if sc.data == nil || bytes.Equal(sc.data, []byte("[DONE]")) {
			continue
		}
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(sc.data, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			var d struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal(sc.data, &d) == nil {
				sb.WriteString(d.Delta)
			}
		case "response.completed", "response.incomplete":
			var full struct {
				Response struct {
					Usage *codexUsage `json:"usage"`
				} `json:"response"`
			}
			if json.Unmarshal(sc.data, &full) == nil && full.Response.Usage != nil {
				usage = full.Response.Usage
			}
		case "response.failed", "error":
			failed = fmt.Errorf("codex 上游返回失败")
		}
	}
	if failed != nil && sb.Len() == 0 {
		return nil, failed
	}
	out := map[string]any{
		"id": "chatcmpl-codex", "object": "chat.completion", "created": time.Now().Unix(),
		"model": model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": sb.String()},
			"finish_reason": "stop",
		}},
	}
	if usage != nil {
		out["usage"] = usage.toChat()
	}
	return json.Marshal(out)
}

// codexSend Codex 线的转发：换 AT → 转 Responses → 转回 chat/completions。
// 非 200 状态原样返回（上层按 401/403 判死换号、429 同钥退避的既有语义处理）。
func (c *Client) codexSend(ctx context.Context, k *KeyState, body []byte, stream bool, path string) (*http.Response, error) {
	at, err := c.getAT(ctx, k)
	if err != nil {
		log.Printf("[codex] line=%s key=%d 取 AT 失败: %v", c.Line.ID, k.Idx, err)
		return codexFakeResp(503, `{"error":{"message":"codex 账号令牌刷新失败，请稍后重试","type":"upstream_error","code":"codex_auth"}}`), nil
	}
	upBody, err := chatToResponses(body)
	if err != nil {
		return codexFakeResp(400, fmt.Sprintf(`{"error":{"message":%q,"type":"invalid_request_error","code":null}}`, err.Error())), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.Line.BaseURL, "/")+"/responses", bytes.NewReader(upBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+at)
	req.Header.Set("User-Agent", codexUserAgent)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	// 官方用量头透传（x-codex-primary-used-percent / reset-at / plan-type）：
	// httpapi 层按请求落库 admin_line_keys，看板显示官方实时余量，零额外请求
	codexHeaders := http.Header{}
	for hk, hv := range resp.Header {
		if lk := strings.ToLower(hk); strings.HasPrefix(lk, "x-codex-") {
			codexHeaders[hk] = hv
		}
	}
	if resp.StatusCode != 200 {
		resp.Header = mergeCodexHeaders(resp.Header, codexHeaders)
		return resp, nil // 上层既有语义处理（401/403 判死换号、429/5xx 退避）
	}
	// 内部统一走上游 SSE：按用户 stream 要求实时转换或聚合
	model := codexModelOf(body)
	if stream {
		pr, pw := io.Pipe()
		go func() {
			_, _, perr := codexPipeStream(resp.Body, pw)
			_ = resp.Body.Close()
			pw.CloseWithError(perr)
		}()
		return &http.Response{
			StatusCode: 200,
			Header:     mergeCodexHeaders(http.Header{"Content-Type": []string{"text/event-stream"}}, codexHeaders),
			Body:       pr,
		}, nil
	}
	agg, aerr := codexAggregate(resp.Body, model)
	_ = resp.Body.Close()
	if aerr != nil {
		return codexFakeResp(502, fmt.Sprintf(`{"error":{"message":%q,"type":"upstream_error","code":"codex_stream"}}`, aerr.Error())), nil
	}
	return &http.Response{
		StatusCode: 200,
		Header:     mergeCodexHeaders(http.Header{"Content-Type": []string{"application/json"}}, codexHeaders),
		Body:       io.NopCloser(bytes.NewReader(agg)),
	}, nil
}

// mergeCodexHeaders 把官方 x-codex-* 用量头并进返回头（dst 优先，官方头不覆盖）
func mergeCodexHeaders(dst, codex http.Header) http.Header {
	for k, vs := range codex {
		if _, ok := dst[k]; !ok {
			dst[k] = vs
		}
	}
	return dst
}

// codexModelOf 从站内请求体取模型名（响应回显用）
func codexModelOf(body []byte) string {
	var m struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &m)
	return m.Model
}

// codexFakeResp 构造伪错误响应（走上层既有错误转译链路）
func codexFakeResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
