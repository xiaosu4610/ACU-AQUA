package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"acu-aqua/gateway/internal/config"
)

// chatToResponses：system 并入 instructions，user/assistant 转 input items，stream 强制 true
func TestChatToResponses(t *testing.T) {
	in := `{"model":"gpt-5.6-luna","messages":[
		{"role":"system","content":"你是助手"},
		{"role":"user","content":"你好"},
		{"role":"assistant","content":"你好！"},
		{"role":"user","content":[{"type":"text","text":"数组消息"}]}
	],"max_tokens":100,"temperature":0.7}`
	out, err := chatToResponses([]byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	var m struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Stream       bool   `json:"stream"`
		Store        bool   `json:"store"`
		Input        []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
		MaxOutputTokens int64 `json:"max_output_tokens"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("输出解析失败: %v", err)
	}
	if m.Model != "gpt-5.6-luna" || !m.Stream || m.Store {
		t.Fatalf("model/stream/store 错误: %v", m)
	}
	if m.Instructions != "你是助手" {
		t.Fatalf("system 未并入 instructions: %q", m.Instructions)
	}
	if len(m.Input) != 3 || m.MaxOutputTokens != 100 {
		t.Fatalf("input 数量/max_output_tokens 错误: n=%d max=%d", len(m.Input), m.MaxOutputTokens)
	}
	if m.Input[0].Content[0].Type != "input_text" || m.Input[1].Content[0].Type != "output_text" {
		t.Fatalf("content type 错误: %+v", m.Input)
	}
	if m.Input[2].Content[0].Text != "数组消息" {
		t.Fatalf("数组消息文本错误: %+v", m.Input[2])
	}
}

// 构造一段最小 Responses API SSE 流
const codexFakeSSE = "event: response.created\n" +
	`data: {"type":"response.created","response":{"id":"resp_x","model":"gpt-5.6-luna"}}` + "\n\n" +
	"event: response.output_text.delta\n" +
	`data: {"type":"response.output_text.delta","delta":"你"}` + "\n\n" +
	"event: response.output_text.delta\n" +
	`data: {"type":"response.output_text.delta","delta":"好"}` + "\n\n" +
	"event: response.completed\n" +
	`data: {"type":"response.completed","response":{"id":"resp_x","model":"gpt-5.6-luna","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}` + "\n\n"

// codexPipeStream：SSE→chat chunks，文本/usage/[DONE] 逐项断言
func TestCodexPipeStream(t *testing.T) {
	var sb strings.Builder
	text, usage, err := codexPipeStream(strings.NewReader(codexFakeSSE), &sb)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if text != "你好" {
		t.Fatalf("聚合文本错误: %q", text)
	}
	if usage == nil || usage.InputTokens != 10 || usage.OutputTokens != 5 {
		t.Fatalf("usage 错误: %+v", usage)
	}
	out := sb.String()
	if !strings.Contains(out, `"role":"assistant"`) {
		t.Fatalf("缺首帧 role")
	}
	if !strings.Contains(out, `"content":"你"`) || !strings.Contains(out, `"content":"好"`) {
		t.Fatalf("缺 delta 帧")
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) || !strings.Contains(out, `"prompt_tokens":10`) {
		t.Fatalf("缺尾帧/usage: %s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("缺 [DONE]")
	}
}

// codexAggregate：非流式聚合出标准 chat.completion
func TestCodexAggregate(t *testing.T) {
	b, err := codexAggregate(strings.NewReader(codexFakeSSE), "gpt-5.6-luna")
	if err != nil {
		t.Fatalf("聚合失败: %v", err)
	}
	var m struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if m.Object != "chat.completion" || m.Choices[0].Message.Content != "你好" {
		t.Fatalf("聚合结果错误: %s", b)
	}
	if m.Usage.PromptTokens != 10 || m.Usage.CompletionTokens != 5 {
		t.Fatalf("usage 错误: %+v", m.Usage)
	}
}

// 端到端：假 auth 换票 + 假 responses 上游，Do() 全链路走通（bearer 认证头校验）
func TestCodexEndToEnd(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["grant_type"] != "refresh_token" || in["client_id"] != codexClientID || !strings.HasPrefix(in["refresh_token"], "rt.1.") {
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "AT_TEST", "refresh_token": "rt.1.ROTATED", "expires_in": 3600,
		})
	}))
	defer authSrv.Close()

	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer AT_TEST" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexFakeSSE))
	}))
	defer upSrv.Close()

	// 换票路径依赖 auth.openai.com 常量端点，单测里直接预置 AT 缓存绕过；
	// 缓存校验按令牌链锚点（origin = DB 钥）匹配，预置时须带上真实钥
	// RT 刷新逻辑由服务器端真实凭据验证
	t.Setenv("AQUA_CODEX_RT_FILE", t.TempDir()+"/rt.json")
	codexToks.Store("gpt-test/0", &codexTok{at: "AT_TEST", exp: time.Now().Add(time.Hour), rt: "rt.1.x", origin: "rt.1.AAA"})

	l := &config.Line{
		ID: "gpt-test", AuthStyle: "codex",
		BaseURL: upSrv.URL,
		Keys:    []string{"rt.1.AAA"},
	}
	c := NewClient(l)
	body := `{"model":"gpt-5.6-luna","messages":[{"role":"user","content":"hi"}],"stream":false}`
	resp, _, err := c.Do(context.Background(), []byte(body), false, "/chat/completions")
	if err != nil {
		t.Fatalf("Do 失败: %v", err)
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), `"content":"你好"`) {
		t.Fatalf("端到端内容错误: %s", buf[:n])
	}
}
