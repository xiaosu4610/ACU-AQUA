// 模型能力口径回归（20260920）：纯文本模型拒收图片输入 + 上游 max_tokens 上限钳制。
//
// 背景：TokenLink（按量线）把 deepseek-v4-flash / deepseek-v4-pro / glm-5.3-flash 的
// 上游指向统一改到 deepseek-v4.1-flash，前端价格与展示不变（偷偷发福利）。但"名义模型"
// 与"上游真实模型"的能力口径必须对齐：
//   - DeepSeek V4 Flash / V4 Pro 为纯文本 → 网关侧拒收图片（400 vision_not_supported）
//   - GLM-5.3-Flash 官方输出上限 128K → 上游请求 max_tokens 钳到 131072
package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// capUpstream 记录最后一次收到的上游请求体（用于断言"网关实际转发了什么"）
type capUpstream struct {
	mu   sync.Mutex
	body []byte
}

func (c *capUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.body = b
	c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"x","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":0}}}`))
}

func (c *capUpstream) last() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body
}

func TestHasImageContent(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"纯文本字符串", `{"messages":[{"role":"user","content":"你好"}]}`, false},
		{"文本块数组", `{"messages":[{"role":"user","content":[{"type":"text","text":"你好"}]}]}`, false},
		{"image_url 块", `{"messages":[{"role":"user","content":[{"type":"text","text":"看图"},{"type":"image_url","image_url":{"url":"https://x/a.png"}}]}]}`, true},
		{"input_image 块（Responses 口径）", `{"messages":[{"role":"user","content":[{"type":"input_image","image_url":"https://x/a.png"}]}]}`, true},
		{"image 块（部分厂商口径）", `{"messages":[{"role":"user","content":[{"type":"image","source":{}}]}]}`, true},
		{"多轮中任一轮含图", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"ok"},{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://x/a.png"}}]}]}`, true},
		{"无 messages", `{"model":"m"}`, false},
		{"非法 JSON", `{`, false},
	}
	for _, c := range cases {
		if got := hasImageContent([]byte(c.body)); got != c.want {
			t.Errorf("%s：期望 %v，得 %v", c.name, c.want, got)
		}
	}
}

func TestClampMaxTokens(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		limit int64
		want  int64 // 期望上游看到的 max_tokens；-1 表示字段不存在
		keep  string
	}{
		{"limit=0 不限", `{"max_tokens":384000}`, 0, 384000, ""},
		{"未超限不重编码", `{"max_tokens":1000}`, 131072, 1000, ""},
		{"超限钳制", `{"max_tokens":384000}`, 131072, 131072, ""},
		{"max_completion_tokens 同样钳制", `{"max_completion_tokens":384000}`, 131072, -1, ""},
		{"两字段同时钳制", `{"max_tokens":200000,"max_completion_tokens":300000}`, 131072, 131072, ""},
		{"无字段不动", `{"model":"m"}`, 131072, -1, ""},
		{"非数字不动", `{"max_tokens":"384000"}`, 131072, -1, ""},
	}
	for _, c := range cases {
		out, err := clampMaxTokens([]byte(c.body), c.limit)
		if err != nil {
			t.Fatalf("%s：%v", c.name, err)
		}
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("%s：输出非法 JSON %s", c.name, out)
		}
		got := int64(-1)
		if v, ok := m["max_tokens"].(float64); ok {
			got = int64(v)
		}
		if got != c.want {
			t.Errorf("%s：max_tokens 期望 %d，得 %d（输出 %s）", c.name, c.want, got, out)
		}
	}
}

// TestNoVisionRejectsImage 纯文本模型（no_vision=1）收到图片必须 400，且不转发上游
func TestNoVisionRejectsImage(t *testing.T) {
	app, h, tok := newQuotaApp(t)
	if _, err := app.DB.Exec("UPDATE admin_line_models SET no_vision=1 WHERE line_id='c' AND site_id='pc1'"); err != nil {
		t.Fatal(err)
	}
	if err := app.reloadLines(); err != nil {
		t.Fatal(err)
	}
	withImage := map[string]any{
		"model": "c/pc1",
		"messages": []map[string]any{{"role": "user", "content": []map[string]any{
			{"type": "text", "text": "这是什么"},
			{"type": "image_url", "image_url": map[string]string{"url": "https://x/a.png"}},
		}}},
	}
	rec, out := doJSON(t, h, "POST", "/v1/chat/completions", tok, withImage)
	msg := ""
	if e, ok := out["error"].(map[string]any); ok {
		msg, _ = e["code"].(string)
	}
	if rec.Code != 400 || msg != "vision_not_supported" {
		t.Fatalf("含图片应 400 vision_not_supported，得 %d (%s)", rec.Code, msg)
	}
	// 纯文本请求不受影响
	rec2, out2 := doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec2.Code != 200 {
		t.Fatalf("纯文本请求应放行，得 %d %v", rec2.Code, out2)
	}
}

// TestMaxOutputTokensClampedUpstream 上游 max_tokens 上限：用户传 384K，上游只应收到 128K
func TestMaxOutputTokensClampedUpstream(t *testing.T) {
	app, h, tok := newQuotaApp(t)
	cap := &capUpstream{}
	srv := httptest.NewServer(cap)
	t.Cleanup(srv.Close)
	// 把按次线 c 的上游指向捕获服务器
	if _, err := app.DB.Exec("UPDATE admin_lines SET base_url=? WHERE id='c'", srv.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB.Exec("UPDATE admin_line_models SET max_output_tokens=131072 WHERE line_id='c' AND site_id='pc1'"); err != nil {
		t.Fatal(err)
	}
	if err := app.reloadLines(); err != nil {
		t.Fatal(err)
	}
	rec, out := doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 384000})
	if rec.Code != 200 {
		t.Fatalf("应放行，得 %d %v", rec.Code, out)
	}
	var sent struct {
		MaxTokens int64  `json:"max_tokens"`
		Model     string `json:"model"`
	}
	if err := json.Unmarshal(cap.last(), &sent); err != nil {
		t.Fatalf("上游请求体解析失败：%v（原文 %s）", err, cap.last())
	}
	if sent.MaxTokens != 131072 {
		t.Fatalf("上游 max_tokens 应被钳到 131072，得 %d（原文 %s）", sent.MaxTokens, cap.last())
	}
	if sent.Model != "vendor-pc1" {
		t.Fatalf("上游模型名应为 upstream_id，得 %q", sent.Model)
	}
}
