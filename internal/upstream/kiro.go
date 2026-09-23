// ———————— Kiro（AWS Kiro / CodeWhisperer 账号池）适配层 ————————
// auth_style="kiro" 的线：keys 存账号凭证 JSON（admin_line_keys.key）：
//
//	{"client_id":"...","client_secret":"...","refresh_token":"...","region":"us-east-1"}
//
// base_url = https://q.us-east-1.amazonaws.com
//
// 站内 chat/completions 请求在此转成 AWS CodeWhisperer Streaming 的
// GenerateAssistantResponse，上游返回 **AWS Event Stream（二进制分帧）**，
// 再反向转成 chat/completions（流式逐帧 / 非流式聚合），对上层
// （Do/DoKey/serveStreamChat/serveJSONChat）完全透明。
//
// 与 codex 线的两点关键差异（20260922 实测确认）：
//  1. 上游**不返回 token 用量**，只回 meteringEvent{unit:credit, usage:<积分>}。
//     故本层按「请求正文/4、响应正文/4」合成 usage 帧——计费口径与其它线的
//     字节估算一致（billing.MeterObserved 同口径），成本侧另有精确台账。
//  2. 上游**不支持 OpenAI 的 image_url**（实测 400），故本线模型一律 no_vision。
package upstream

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"acu-aqua/gateway/internal/billing"
)

// kiroMaxHistory 转发的历史轮数上限（防超长上下文被上游拒绝；超出丢最旧）
const kiroMaxHistory = 40

// kiroUserAgent 上游请求 UA（Go 默认的 "Go-http-client/2.0" 会被部分 CDN/WAF 按非预期指纹处置）
const kiroUserAgent = "kiro-cli/1.0.0"

// briefBody 截断响应体（错误信息/日志用；非 JSON 响应时定位根因）
func briefBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// kiroCred 一个 Kiro 账号的凭证
type kiroCred struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	Region       string `json:"region"`
}

// parseKiroCred 解析 admin_line_keys.key 里的凭证 JSON
func parseKiroCred(s string) (*kiroCred, error) {
	var c kiroCred
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &c); err != nil {
		return nil, fmt.Errorf("kiro 凭证不是合法 JSON")
	}
	if c.ClientID == "" || c.ClientSecret == "" || c.RefreshToken == "" {
		return nil, fmt.Errorf("kiro 凭证缺少 client_id / client_secret / refresh_token")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	return &c, nil
}

// errKiroAuthDead 账号令牌链确定性失效（invalid_grant 等）：必须判死换号，绝不原地重试
var errKiroAuthDead = errors.New("kiro 令牌链已失效")

// kiroTok 一个账号的令牌态
type kiroTok struct {
	at     string
	exp    time.Time
	rt     string // 上游轮换后的最新 RT
	origin string // 令牌链锚点 = 取号时的 DB RT（管理台换钥后缓存即刻失效）
}

// kiroToks 全局令牌表：key = lineID + "/" + keyIdx
var kiroToks sync.Map

// ResetKiroTokens 清空全部 Kiro 令牌缓存（线路热重载时调用）
func ResetKiroTokens() {
	kiroToks.Range(func(k, v any) bool {
		kiroToks.Delete(k)
		return true
	})
}

// kiroRTMu RT 持久化文件锁
var kiroRTMu sync.Mutex

type kiroSavedEntry struct {
	RT     string `json:"rt"`
	Origin string `json:"origin,omitempty"`
}

// kiroRTFile 轮换后 RT 的持久化文件（DB 原 RT 保留不动；重启后优先生效）
func kiroRTFile() string {
	if p := os.Getenv("AQUA_KIRO_RT_FILE"); p != "" {
		return p
	}
	if st, err := os.Stat("/data/aqua/data"); err == nil && st.IsDir() {
		return "/data/aqua/data/kiro-rt.json"
	}
	return "kiro-rt.json"
}

func kiroLoadSavedRT(lineID string, idx int) (rt, origin string) {
	b, err := os.ReadFile(kiroRTFile())
	if err != nil {
		return "", ""
	}
	m := map[string]kiroSavedEntry{}
	if json.Unmarshal(b, &m) != nil {
		return "", ""
	}
	if e, ok := m[fmt.Sprintf("%s/%d", lineID, idx)]; ok {
		return e.RT, e.Origin
	}
	return "", ""
}

func kiroSaveRT(lineID string, idx int, rt, origin string) {
	kiroRTMu.Lock()
	defer kiroRTMu.Unlock()
	p := kiroRTFile()
	m := map[string]kiroSavedEntry{}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	m[fmt.Sprintf("%s/%d", lineID, idx)] = kiroSavedEntry{RT: rt, Origin: origin}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
		if b, err := json.MarshalIndent(m, "", "  "); err == nil {
			_ = os.WriteFile(p, b, 0o600)
		}
	}
}

// kiroAuthClient 令牌刷新专用 client。
// ⚠️ 必须用**独立 Transport 且不启用 HTTP/2**（20260922 实测）：走线的默认 Transport
// （ForceAttemptHTTP2=true）访问 AWS OIDC 换票端点时，上游边缘对 h2 + 5KB 级请求体
// 返回**空体 413**（curl/Python 同请求同路径均 200，仅 Go h2 复现）。
// 换票流量极低（每账号约 1 次/小时），独占连接池无成本。
func (c *Client) kiroAuthClient() *http.Client {
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       60 * time.Second,
	}
	if c.Line.Proxy != "" { // 线级出站代理（与主 Transport 同口径）
		if pu, err := url.Parse(c.Line.Proxy); err == nil && pu.Scheme != "" {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	return &http.Client{Timeout: 25 * time.Second, Transport: tr}
}

// refreshKiroToken 用 refresh_token 换新 access_token（AWS IAM Identity Center OIDC）
func refreshKiroToken(ctx context.Context, hc *http.Client, cred *kiroCred, rt string) (*kiroTok, error) {
	payload, _ := json.Marshal(map[string]string{
		"clientId":     cred.ClientID,
		"clientSecret": cred.ClientSecret,
		"refreshToken": rt,
		"grantType":    "refresh_token",
	})
	url := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", cred.Region)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// UA 显式设置：Go 默认的 "Go-http-client/2.0" 会被部分 CDN/WAF 按非浏览器指纹处置
	req.Header.Set("User-Agent", kiroUserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// 注意：AWS 用 camelCase（accessToken / refreshToken / expiresIn），
	// 与 OpenAI 的 snake_case 不同——这里不能照抄 codex 的字段名。
	var out struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Error        string `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(body, &out); err != nil {
		// 带上请求与响应细节：非 JSON 响应（CDN/WAF 错误页）时仅凭状态码无法定位
		return nil, fmt.Errorf("刷新响应解析失败(状态%d url=%s reqLen=%d): %s",
			resp.StatusCode, url, len(payload), briefBody(body))
	}
	if resp.StatusCode != 200 || out.AccessToken == "" {
		err := fmt.Errorf("AT 刷新失败(%d): %s", resp.StatusCode, out.Error)
		// 确定性失效分型：令牌链已死（换号是唯一解）
		switch out.Error {
		case "invalid_grant", "invalid_client", "unauthorized_client", "invalid_request":
			return nil, fmt.Errorf("%w: %v", errKiroAuthDead, err)
		}
		if resp.StatusCode == 400 || resp.StatusCode == 401 {
			return nil, fmt.Errorf("%w: %v", errKiroAuthDead, err)
		}
		return nil, err
	}
	exp := out.ExpiresIn
	if exp <= 0 {
		exp = 3600
	}
	newRT := out.RefreshToken
	if newRT == "" {
		newRT = rt
	}
	return &kiroTok{at: out.AccessToken, exp: time.Now().Add(time.Duration(exp-300) * time.Second), rt: newRT}, nil
}

// kiroRefreshMu 并发刷新互斥（RT 一次性轮换，同账号并发刷新必有一个失败）
var kiroRefreshMu sync.Mutex

// getKiroAT 取账号 access_token：内存缓存 → 持久化新 RT（仅同链轮换）→ DB 原始 RT，逐级回退。
//
// ⚠️ 令牌链锚点是 **cred.RefreshToken**（DB 里的 RT 原文），**不是 k.Key**——
// 与 codex 线不同：codex 的 k.Key 本身就是 RT，而 Kiro 的 k.Key 是整份凭证 JSON。
// 20260922 踩坑：误把 k.Key 当 RT 传入换票请求，请求体从 5KB 膨胀到 10KB，
// 被 AWS 边缘以**空体 413** 拒绝（curl/Python 同请求同路径均 200，极难定位）。
func (c *Client) getKiroAT(ctx context.Context, k *KeyState) (string, error) {
	cred, err := parseKiroCred(k.Key)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errKiroAuthDead, err) // 凭证格式错=永久不可用
	}
	anchor := cred.RefreshToken // 令牌链锚点：DB 原始 RT
	cacheKey := fmt.Sprintf("%s/%d", c.Line.ID, k.Idx)
	if v, ok := kiroToks.Load(cacheKey); ok {
		t := v.(*kiroTok)
		if t.origin == anchor && time.Now().Before(t.exp) {
			return t.at, nil
		}
		kiroToks.Delete(cacheKey)
	}
	kiroRefreshMu.Lock()
	defer kiroRefreshMu.Unlock()
	if v, ok := kiroToks.Load(cacheKey); ok { // double-check
		t := v.(*kiroTok)
		if t.origin == anchor && time.Now().Before(t.exp) {
			return t.at, nil
		}
	}
	if saved, origin := kiroLoadSavedRT(c.Line.ID, k.Idx); saved != "" && saved != anchor && origin == anchor {
		if t, err := refreshKiroToken(ctx, c.kiroAuthClient(), cred, saved); err == nil {
			t.origin = anchor
			kiroToks.Store(cacheKey, t)
			kiroSaveRT(c.Line.ID, k.Idx, t.rt, anchor)
			return t.at, nil
		}
		log.Printf("[kiro] line=%s key=%d 持久化 RT 失效，回退 DB 原始 RT", c.Line.ID, k.Idx)
	}
	t, err := refreshKiroToken(ctx, c.kiroAuthClient(), cred, anchor)
	if err != nil {
		return "", err
	}
	t.origin = anchor
	kiroToks.Store(cacheKey, t)
	kiroSaveRT(c.Line.ID, k.Idx, t.rt, anchor)
	return t.at, nil
}

// ———————— AWS Event Stream 解析 ————————
// 帧格式（全大端）：
//
//	[total_length:4][headers_length:4][prelude_crc:4][headers][payload][message_crc:4]
//
// headers 为 (name_len:1)(name)(value_type:1)(value) 序列；value_type=7 为 string（带 2 字节长度）。
// 我们只关心 :event-type / :message-type 两个 string 头与 payload（JSON）。

// awsESFrame 一帧
type awsESFrame struct {
	EventType   string
	MessageType string
	Payload     []byte
}

// awsESReader AWS Event Stream 帧读取器（不限行长，payload 可达数十 KB）
type awsESReader struct {
	br   *bufio.Reader
	done bool
}

func newAWSESReader(r io.Reader) *awsESReader {
	return &awsESReader{br: bufio.NewReaderSize(r, 64<<10)}
}

// next 读下一帧；返回 io.EOF 表示正常结束
func (s *awsESReader) next() (*awsESFrame, error) {
	if s.done {
		return nil, io.EOF
	}
	pre := make([]byte, 12)
	if _, err := io.ReadFull(s.br, pre); err != nil {
		s.done = true
		return nil, err
	}
	total := binary.BigEndian.Uint32(pre[0:4])
	hlen := binary.BigEndian.Uint32(pre[4:8])
	if total < 16 || hlen > total-16 {
		s.done = true
		return nil, fmt.Errorf("kiro 事件流帧头非法(total=%d headers=%d)", total, hlen)
	}
	rest := make([]byte, total-12)
	if _, err := io.ReadFull(s.br, rest); err != nil {
		s.done = true
		return nil, err
	}
	hdrs := rest[:hlen]
	payload := rest[hlen : len(rest)-4] // 末尾 4 字节为 message CRC
	f := &awsESFrame{Payload: payload}
	p := 0
	for p < len(hdrs) {
		nl := int(hdrs[p])
		p++
		if p+nl > len(hdrs) {
			break
		}
		name := string(hdrs[p : p+nl])
		p += nl
		if p >= len(hdrs) {
			break
		}
		vt := hdrs[p]
		p++
		if vt == 7 { // string：2 字节长度 + 值
			if p+2 > len(hdrs) {
				break
			}
			vl := int(binary.BigEndian.Uint16(hdrs[p : p+2]))
			p += 2
			if p+vl > len(hdrs) {
				break
			}
			val := string(hdrs[p : p+vl])
			p += vl
			switch name {
			case ":event-type":
				f.EventType = val
			case ":message-type":
				f.MessageType = val
			}
		} else { // 其它类型（bool/int/long...）按 1/2/4/8 字节跳过
			switch vt {
			case 0, 1:
				p++
			case 2:
				p += 2
			case 4, 8:
				p += 4
			case 5, 9:
				p += 8
			}
		}
	}
	return f, nil
}

// ———————— 请求转换 ————————

// kiroInReq 站内 chat/completions 请求（只取需要的字段）
type kiroInReq struct {
	Model    string          `json:"model"`
	Messages []kiroInMsg     `json:"messages"`
	Stream   bool            `json:"stream"`
	System   json.RawMessage `json:"system,omitempty"`
}

type kiroInMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// kiroMsgText 提取消息文本；遇图片块返回错误（本线模型一律 no_vision，上层已拦）
func kiroMsgText(raw json.RawMessage) (string, error) {
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
		case "image_url", "input_image", "image":
			return "", fmt.Errorf("kiro 线暂不支持图片输入")
		}
	}
	return b.String(), nil
}

// kiroTurn 一轮对话
type kiroTurn struct {
	user string
	asst string
}

// chatToKiro chat/completions → GenerateAssistantResponse 请求体。
// 语义映射：system/developer → 并入首个 user 消息头部（Kiro 无独立 system 位）；
// 其余按 role 交替填入 history，最后一条 user 作为 currentMessage。
func chatToKiro(body []byte, convID string) ([]byte, error) {
	var in kiroInReq
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("请求体解析失败: %w", err)
	}
	if in.Model == "" {
		return nil, fmt.Errorf("缺少 model")
	}
	var sysParts []string
	type turn struct{ role, text string }
	var turns []turn
	for _, m := range in.Messages {
		text, err := kiroMsgText(m.Content)
		if err != nil {
			return nil, err
		}
		switch m.Role {
		case "system", "developer":
			if text != "" {
				sysParts = append(sysParts, text)
			}
		case "assistant":
			turns = append(turns, turn{"assistant", text})
		default: // user / tool 等一律按 user
			turns = append(turns, turn{"user", text})
		}
	}
	// 最后一条 user 作为 currentMessage；其前的内容进 history
	cur := ""
	cut := len(turns)
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].role == "user" {
			cur = turns[i].text
			cut = i
			break
		}
	}
	if cur == "" {
		// 没有 user 消息（异常请求）：把全部内容塞进 currentMessage，避免空请求
		var all []string
		for _, t := range turns {
			all = append(all, t.text)
		}
		cur = strings.Join(all, "\n")
		cut = len(turns)
	}
	if len(sysParts) > 0 {
		cur = strings.Join(sysParts, "\n\n") + "\n\n" + cur
	}
	hist := turns[:cut]
	if len(hist) > kiroMaxHistory {
		hist = hist[len(hist)-kiroMaxHistory:]
	}
	// history 首项必须是 user（Kiro 要求 user/assistant 交替，以 user 开头）
	for len(hist) > 0 && hist[0].role != "user" {
		hist = hist[1:]
	}
	histOut := make([]map[string]any, 0, len(hist))
	for _, t := range hist {
		if t.role == "user" {
			histOut = append(histOut, map[string]any{"userInputMessage": map[string]any{
				"content": t.text, "modelId": in.Model, "origin": "KIRO_CLI"}})
		} else {
			histOut = append(histOut, map[string]any{"assistantResponseMessage": map[string]any{
				"content": t.text}})
		}
	}
	cs := map[string]any{
		"chatTriggerType": "MANUAL",
		"conversationId":  convID,
		"currentMessage": map[string]any{"userInputMessage": map[string]any{
			"content": cur, "modelId": in.Model, "origin": "KIRO_CLI"}},
	}
	if len(histOut) > 0 {
		cs["history"] = histOut
	}
	return json.Marshal(map[string]any{"conversationState": cs})
}

// kiroPromptTokens 输入 token（20260923 标准化：委托 billing.EstPromptTokens，只数正文文本）。
//
// 旧实现是 len(**整个请求体**) / 4 —— 把 model / stream / max_tokens 等所有字段
// 连同 JSON 结构（字段名/引号/括号）都算成 token，是全站三套口径里最离谱的一个，
// 输入侧被系统性超收。现统一到全站唯一口径。
func kiroPromptTokens(body []byte) int64 {
	return billing.EstPromptTokens(body)
}

// kiroEstTokens 文本 token 估算（20260923 标准化：委托 billing.EstTokens，CJK 感知）。
// 输出侧是纯正文无结构开销，旧口径字节/4 对中文会低估 25%，统一后按字符计。
func kiroEstTokens(s string) int64 {
	return billing.EstTokens(s)
}

// kiroChatChunk chat/completions 流式帧
func kiroChatChunk(id, model, content, role, finish string, usage map[string]any) map[string]any {
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
		m["usage"] = usage
	}
	return m
}

// kiroUsage 合成 usage（上游不返回 token 数，按正文长度估算）
func kiroUsage(prompt, completion int64) map[string]any {
	return map[string]any{
		"prompt_tokens":         prompt,
		"completion_tokens":     completion,
		"total_tokens":          prompt + completion,
		"prompt_tokens_details": map[string]any{"cached_tokens": int64(0)},
	}
}

// kiroPipeStream 读上游 AWS Event Stream → 输出 chat/completions SSE（含 [DONE]）。
// 返回聚合文本与 finish 原因（供非流式聚合/日志）。
func kiroPipeStream(r io.Reader, w io.Writer, promptTok int64, model string) (string, string, error) {
	var text strings.Builder
	var convID string
	sentRole := false
	emit := func(v map[string]any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	done := func(ferr error) (string, string, error) {
		if ferr == nil {
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
		return text.String(), "stop", ferr
	}
	er := newAWSESReader(r)
	for {
		f, err := er.next()
		if err != nil {
			if err == io.EOF {
				break
			}
			if !sentRole {
				emit(map[string]any{"error": map[string]any{
					"message": "kiro 上游流式响应中断", "type": "api_error",
					"code": "stream_incomplete", "param": nil}})
				return text.String(), "error", fmt.Errorf("kiro_stream_incomplete")
			}
			// 已有部分内容：按截断收尾（不伪装成功，交上层按保底计费）
			emit(kiroChatChunk(convID, model, "", "", "length", kiroUsage(promptTok, kiroEstTokens(text.String()))))
			return text.String(), "length", errKiroTruncated
		}
		if f.MessageType == "exception" || f.MessageType == "error" {
			msg := strings.TrimSpace(string(f.Payload))
			if msg == "" {
				msg = "kiro 上游返回异常"
			}
			emit(map[string]any{"error": map[string]any{
				"message": msg, "type": "api_error", "code": f.EventType, "param": nil}})
			return text.String(), "error", fmt.Errorf("kiro_error: %s", msg)
		}
		switch f.EventType {
		case "initial-response":
			var v struct {
				ConversationID string `json:"conversationId"`
			}
			if json.Unmarshal(f.Payload, &v) == nil {
				convID = v.ConversationID
			}
		case "assistantResponseEvent":
			var v struct {
				Content string `json:"content"`
			}
			if json.Unmarshal(f.Payload, &v) != nil || v.Content == "" {
				continue
			}
			if !sentRole {
				sentRole = true
				emit(kiroChatChunk(convID, model, "", "assistant", "", nil))
			}
			text.WriteString(v.Content)
			emit(kiroChatChunk(convID, model, v.Content, "", "", nil))
		case "meteringEvent":
			// 上游真实积分消耗：留作成本台账的对照（当前按 rate 台账计，此处仅记录异常值）
			var v struct {
				Usage float64 `json:"usage"`
			}
			if json.Unmarshal(f.Payload, &v) == nil && v.Usage < 0 {
				log.Printf("[kiro] 上游返回负积分用量: %v", v.Usage)
			}
		}
	}
	if !sentRole {
		emit(map[string]any{"error": map[string]any{
			"message": "kiro 上游未返回任何内容", "type": "api_error",
			"code": "empty_response", "param": nil}})
		return "", "error", fmt.Errorf("kiro_empty_response")
	}
	emit(kiroChatChunk(convID, model, "", "", "stop", kiroUsage(promptTok, kiroEstTokens(text.String()))))
	return done(nil)
}

// errKiroTruncated 上游事件流中断但已产出部分内容（不重试、不退款，按保底计费）
var errKiroTruncated = errors.New("kiro stream truncated")

// kiroAggregate 非流式：解析上游事件流，聚合为 chat.completion JSON
func kiroAggregate(r io.Reader, model string, promptTok int64) ([]byte, error) {
	var sb strings.Builder
	var failed error
	er := newAWSESReader(r)
	for {
		f, err := er.next()
		if err != nil {
			break
		}
		if f.MessageType == "exception" || f.MessageType == "error" {
			failed = fmt.Errorf("kiro 上游返回异常: %s", strings.TrimSpace(string(f.Payload)))
			continue
		}
		if f.EventType == "assistantResponseEvent" {
			var v struct {
				Content string `json:"content"`
			}
			if json.Unmarshal(f.Payload, &v) == nil {
				sb.WriteString(v.Content)
			}
		}
	}
	if failed != nil && sb.Len() == 0 {
		return nil, failed
	}
	out := map[string]any{
		"id": "chatcmpl-kiro", "object": "chat.completion", "created": time.Now().Unix(),
		"model": model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": sb.String()},
			"finish_reason": "stop",
		}},
		"usage": kiroUsage(promptTok, kiroEstTokens(sb.String())),
	}
	return json.Marshal(out)
}

// randReader UUID 随机源
var randReader io.Reader = rand.Reader

// kiroUUIDv4 生成 conversationId（crypto/rand，不引第三方依赖）
func kiroUUIDv4() string {
	var b [16]byte
	if _, err := io.ReadFull(randReader, b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// kiroSend Kiro 线的转发：换 AT → 转 GenerateAssistantResponse → 转回 chat/completions。
// 非 200 状态原样返回（上层按 401/403 判死换号、429 同钥退避的既有语义处理）。
func (c *Client) kiroSend(ctx context.Context, k *KeyState, body []byte, stream bool, path string) (*http.Response, error) {
	at, err := c.getKiroAT(ctx, k)
	if err != nil {
		log.Printf("[kiro] line=%s key=%d 取 AT 失败: %v", c.Line.ID, k.Idx, err)
		if errors.Is(err, errKiroAuthDead) {
			// 确定性失效：401 让上层立即判死换号（绝不原地重试）
			return kiroFakeResp(401, `{"error":{"message":"kiro 账号令牌已失效","type":"upstream_error","code":"kiro_auth_dead"}}`), nil
		}
		return kiroFakeResp(503, `{"error":{"message":"kiro 账号令牌刷新失败，请稍后重试","type":"upstream_error","code":"kiro_auth"}}`), nil
	}
	var in kiroInReq
	_ = json.Unmarshal(body, &in)
	upBody, err := chatToKiro(body, kiroUUIDv4())
	if err != nil {
		return kiroFakeResp(400, fmt.Sprintf(`{"error":{"message":%q,"type":"invalid_request_error","code":null}}`, err.Error())), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.Line.BaseURL, "/")+"/", bytes.NewReader(upBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonCodeWhispererStreamingService.GenerateAssistantResponse")
	req.Header.Set("Authorization", "Bearer "+at)
	req.Header.Set("Accept", "application/vnd.amazon.eventstream")
	req.Header.Set("User-Agent", kiroUserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return resp, nil // 上层既有语义处理（401/403 判死换号、429/5xx 退避）
	}
	// 账号轮换（20260923 修）：KeyPool.acquire 把游标停在**刚返回的那把钥**上
	// （p.cur = (p.cur+i)%n），而 markUse 在 RPM<=0 时直接 return → key_rpm=0 的线
	// 完全不轮换。Kiro 每个账号额度独立（50 积分/月），不轮换会把流量全压在首号——
	// 生产实锤：idx=0 用掉 48.44/50（仅剩 4 次），其余 19 号用量近 0。
	// 这里在**成功响应后**显式推进游标，实现账号级轮询分摊额度。
	c.Pool.Advance()
	promptTok := kiroPromptTokens(body)
	model := in.Model
	if stream {
		pr, pw := io.Pipe()
		go func() {
			_, _, perr := kiroPipeStream(resp.Body, pw, promptTok, model)
			_ = resp.Body.Close()
			pw.CloseWithError(perr)
		}()
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       pr,
		}, nil
	}
	agg, aerr := kiroAggregate(resp.Body, model, promptTok)
	_ = resp.Body.Close()
	if aerr != nil {
		return kiroFakeResp(502, fmt.Sprintf(`{"error":{"message":%q,"type":"upstream_error","code":"kiro_stream"}}`, aerr.Error())), nil
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(agg)),
	}, nil
}

// kiroFakeResp 构造伪错误响应（走上层既有错误转译链路）
func kiroFakeResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
