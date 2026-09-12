package upstream

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"acu-aqua/gateway/internal/config"
)

func testClient(up *httptest.Server, keys []string, face int64) *Client {
	l := &config.Line{ID: "t", BaseURL: up.URL, Keys: keys, KeyFaceMicro: face}
	return &Client{
		Line: l,
		Pool: NewKeyPool(l.Keys, face, 300),
		HTTP: &http.Client{Timeout: 10 * time.Second},
	}
}

// 单钥用尽制：连续两次请求都用同一把密钥（不轮询）
func TestStickyKey(t *testing.T) {
	var hits int64
	var k0, k1 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&hits, 1)
		if r.Header.Get("Authorization") == "Bearer sk-0" {
			atomic.AddInt64(&k0, 1)
		} else {
			atomic.AddInt64(&k1, 1)
		}
		_, _ = w.Write([]byte(`{}`))
		if n >= 2 {
			_ = r.Body.Close()
		}
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	body := []byte(`{}`)
	for i := 0; i < 5; i++ {
		resp, _, err := c.Do(context.Background(), body, false, "/chat/completions")
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("第 %d 次请求失败: %v", i+1, err)
		}
		_, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	if k0 != 5 || k1 != 0 {
		t.Fatalf("单钥用尽制失效：sk-0 用 %d 次，sk-1 用 %d 次（期望 5/0）", k0, k1)
	}
}

// 同钥重试：上游 5xx 两次后恢复 200，应重试成功且不换钥
func TestRetrySameKey(t *testing.T) {
	var hits, k0 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&hits, 1)
		if r.Header.Get("Authorization") == "Bearer sk-0" {
			atomic.AddInt64(&k0, 1)
		}
		if n <= 2 {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	resp, _, err := c.Do(context.Background(), []byte(`{}`), false, "/chat/completions")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("重试后应成功: err=%v", err)
	}
	resp.Body.Close()
	if k0 != 3 {
		t.Fatalf("应同钥重试 3 次全在 sk-0，实际 sk-0 命中 %d", k0)
	}
}

// 换钥重试：第一把钥持续 5xx，重试 2 次后应换第二把钥并成功
func TestFailoverToNextKey(t *testing.T) {
	var k0, k1 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer sk-0":
			atomic.AddInt64(&k0, 1)
			w.WriteHeader(500) // sk-0 永远失败
		case "Bearer sk-1":
			atomic.AddInt64(&k1, 1)
			_, _ = w.Write([]byte(`{}`)) // sk-1 成功
		}
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	resp, _, err := c.Do(context.Background(), []byte(`{}`), false, "/chat/completions")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("应换钥后成功: err=%v", err)
	}
	resp.Body.Close()
	if k0 != 3 || k1 != 1 {
		t.Fatalf("重试策略错误：sk-0 %d 次（期望 3），sk-1 %d 次（期望 1）", k0, k1)
	}
}

// 4xx 参数错误不重试：直接透传给上层转译
func TestNoRetryOn4xx(t *testing.T) {
	var hits int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"bad"}}`))
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	resp, _, err := c.Do(context.Background(), []byte(`{}`), false, "/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 || hits != 1 {
		t.Fatalf("4xx 不应重试：hits=%d", hits)
	}
}

// 模型级错误（model_not_found 且非渠道级故障）：不重试同钥，立即换钥成功
func TestModelLevelHop(t *testing.T) {
	var k0, k1 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer sk-0":
			atomic.AddInt64(&k0, 1)
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"Model not found"}}`))
		case "Bearer sk-1":
			atomic.AddInt64(&k1, 1)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	resp, _, err := c.Do(context.Background(), []byte(`{}`), false, "/chat/completions")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("模型级换钥应成功: err=%v", err)
	}
	resp.Body.Close()
	if k0 != 1 || k1 != 1 {
		t.Fatalf("模型级错误应立即换钥不重试：sk-0 %d 次（期望 1），sk-1 %d 次（期望 1）", k0, k1)
	}
}

// 渠道级不可用（OneAPI "no available channel"）：全钥共享同一渠道池，
// 换钥/重试注定失败 —— 第一跳即快速失败，原样透传响应（回归 2026-09-12
// 收费接口故障：渠道抖动时旧逻辑换钥重试 5 轮，用户等 10-15 秒收 404）
func TestChannelExhaustedFastFail(t *testing.T) {
	var k0, k1 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer sk-0":
			atomic.AddInt64(&k0, 1)
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"No available channel for model x"}}`))
		case "Bearer sk-1":
			atomic.AddInt64(&k1, 1)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	resp, _, err := c.Do(context.Background(), []byte(`{}`), false, "/chat/completions")
	if err != nil {
		t.Fatalf("应返回响应而非错误: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 503 || !bytes.Contains(body, []byte("No available channel")) {
		t.Fatalf("渠道级不可用应原样透传 503: %d %s", resp.StatusCode, body)
	}
	if k0 != 1 || k1 != 0 {
		t.Fatalf("渠道级不可用应立即快速失败不换钥：sk-0 %d 次（期望 1），sk-1 %d 次（期望 0）", k0, k1)
	}
}

// 全钥模型级错误：返回错误响应供上层转译（404 model_not_found）
func TestModelLevelAllKeys(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"No available channel"}}`))
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	resp, _, err := c.Do(context.Background(), []byte(`{}`), false, "/chat/completions")
	if err != nil {
		t.Fatalf("应返回响应而非错误: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 503 || !bytes.Contains(body, []byte("model_not_found")) {
		t.Fatalf("应透传模型级错误响应: %d %s", resp.StatusCode, body)
	}
}

// 客户端断开（ctx 取消）：不记密钥失败不冷却——
// 回归 2026-09-11 事故：一次客户端取消曾把全池拖入 300s 冷却，整线 502 瘫痪
func TestClientCancelNoCooldown(t *testing.T) {
	// 上游挂起直到 ctx 取消（上限 3s 兜底，防 Close 卡死）
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := c.Do(ctx, []byte(`{}`), false, "/chat/completions")
	if err == nil {
		t.Fatal("ctx 取消应返回错误")
	}
	// 密钥池不得被污染：无失败计数、仍可 Acquire
	for _, k := range c.Pool.keys {
		if k.Fails != 0 || k.Dead {
			t.Fatalf("客户端取消污染密钥池：key=%d Fails=%d Dead=%v", k.Idx, k.Fails, k.Dead)
		}
	}
	if _, err := c.Pool.Acquire(); err != nil {
		t.Fatalf("客户端取消后应仍能取钥: %v", err)
	}
}

// 全池冷却退化：所有钥都在冷却窗口时取最久未失败的钥，而非 502
func TestCooldownFallback(t *testing.T) {
	p := NewKeyPool([]string{"sk-0", "sk-1"}, 0, 300)
	now := time.Now().Unix()
	// 两把钥都在 300s 内失败过（sk-1 更早）
	p.keys[0].Fails, p.keys[0].LastFailTs = 1, now-10
	p.keys[1].Fails, p.keys[1].LastFailTs = 1, now-100
	k, err := p.Acquire()
	if err != nil {
		t.Fatalf("全池冷却应退化为可用钥而非报错: %v", err)
	}
	if k.Idx != 1 {
		t.Fatalf("应取最久未失败的钥 idx=1，得 idx=%d", k.Idx)
	}
	// 全部死钥才报 KEY_POOL_EXHAUSTED
	p.keys[0].Dead, p.keys[1].Dead = true, true
	if _, err := p.Acquire(); err == nil {
		t.Fatal("全死钥应报 KEY_POOL_EXHAUSTED")
	}
}

// 401 无效密钥：立即判死并换钥
func TestDeadKeyOn401(t *testing.T) {
	var k0, k1 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer sk-0":
			atomic.AddInt64(&k0, 1)
			w.WriteHeader(401)
		case "Bearer sk-1":
			atomic.AddInt64(&k1, 1)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	resp, _, err := c.Do(context.Background(), []byte(`{}`), false, "/chat/completions")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("应换钥成功: err=%v", err)
	}
	resp.Body.Close()
	if k0 != 1 || k1 != 1 {
		t.Fatalf("401 应立即换钥：sk-0 %d 次（期望 1），sk-1 %d 次（期望 1）", k0, k1)
	}
	// sk-0 已判死，后续请求只用 sk-1
	for i := 0; i < 3; i++ {
		resp, _, err := c.Do(context.Background(), []byte(`{}`), false, "/chat/completions")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if k0 != 1 {
		t.Fatalf("死钥不应再用：sk-0 %d 次（期望 1）", k0)
	}
}
