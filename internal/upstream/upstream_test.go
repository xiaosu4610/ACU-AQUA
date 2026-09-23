package upstream

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	// 20260919：同钥重试次数 3→2（perKeyRetries）——限流类错误同钥重试无意义，
	// 减少无谓等待；500 场景下 2 次同钥失败后仍能成功（服务端前 2 次返回 500）
	if k0 != 2 {
		t.Fatalf("应同钥重试 2 次全在 sk-0，实际 sk-0 命中 %d", k0)
	}
}

// 换钥重试：第一把钥持续 5xx，同钥重试耗尽后应换第二把钥并成功
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
	if k0 != 2 || k1 != 1 {
		t.Fatalf("重试策略错误：sk-0 %d 次（期望 2），sk-1 %d 次（期望 1）", k0, k1)
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

// 模型级不可用（含 "for model"/"under group" 措辞）：换钥有意义 → 应换钥成功。
// 20260919：kabuai 生产返回 "No available channel for model X under group Y"，
// 此前被 IsChannelExhausted 抢先命中而误判为渠道级（直接放弃、不换钥）。
func TestModelLevelUnavailableFailover(t *testing.T) {
	var k0, k1 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer sk-0":
			atomic.AddInt64(&k0, 1)
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"No available channel for model x under group g"}}`))
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
	if resp.StatusCode != 200 {
		t.Fatalf("模型级错误应换钥后成功: %d %s", resp.StatusCode, body)
	}
	if k0 != 1 || k1 != 1 {
		t.Fatalf("模型级错误应换钥：sk-0 %d 次（期望 1），sk-1 %d 次（期望 1）", k0, k1)
	}
}

// 纯渠道级不可用（仅含 no available channel，无模型级措辞）：立即快速失败不换钥
func TestPureChannelDownFastFail(t *testing.T) {
	var k0, k1 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer sk-0":
			atomic.AddInt64(&k0, 1)
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"message":"no available channel"}}`))
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
	if resp.StatusCode != 503 || !bytes.Contains(body, []byte("no available channel")) {
		t.Fatalf("渠道级不可用应原样透传 503: %d %s", resp.StatusCode, body)
	}
	if k0 != 1 || k1 != 0 {
		t.Fatalf("纯渠道级不可用应立即快速失败不换钥：sk-0 %d 次（期望 1），sk-1 %d 次（期望 0）", k0, k1)
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

// 429 全池限流：网关内部重试（20260924 站长指令"出现 429 自动网关重试，不要直接返回客户端"）。
// 上游首轮对 6 把钥全部返回限流 429，等待窗口后内部重跑全池应拿到 200，
// 而不是把 429 抛给调用方（旧行为：连续 6 钥限流即止损返回 UPSTREAM_RATE_LIMITED）。
func TestRateLimitInternalRetry(t *testing.T) {
	// 缩短限流短冷却（生产 20s），否则本测试要等 20s 才重试——只改等待时长，不改逻辑
	oldCool := rateLimitCoolSecs
	rateLimitCoolSecs = 1
	defer func() { rateLimitCoolSecs = oldCool }()

	var hits int64
	const rlHits = 6 // 6 把钥各限流一次 → 触发"全池饱和"提前止损并进入内部重试
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&hits, 1) <= rlHits {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"message":"rpm exhausted","type":"quota_exceeded_error"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1", "sk-2", "sk-3", "sk-4", "sk-5"}, 0)
	// ctx 预算决定内部重试上限（rateLimitRetryBudget = min(30s, ctx 剩余一半)）
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, _, err := c.Do(ctx, []byte(`{}`), false, "/chat/completions")
	if err != nil {
		t.Fatalf("429 内部重试未生效，仍把失败抛给调用方: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("期望内部重试后拿到 200，实得 %d", resp.StatusCode)
	}
	if got := atomic.LoadInt64(&hits); got <= rlHits {
		t.Fatalf("上游请求数 %d 未超过首轮限流次数 %d，说明没有真正重试", got, rlHits)
	}
}

// 429 配额耗尽（insufficient_quota）不参与内部重试：该钥 30min 长冷却，
// 等待重试毫无意义——应立即交上层转译"本时段额度用完"业务态。
func TestQuotaExhaustedNoInternalRetry(t *testing.T) {
	var hits int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"message":"insufficient_quota"}}`))
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0"}, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := c.Do(ctx, []byte(`{}`), false, "/chat/completions"); err == nil ||
		!strings.Contains(err.Error(), "UPSTREAM_QUOTA_EXHAUSTED") {
		t.Fatalf("配额耗尽应立即返回 UPSTREAM_QUOTA_EXHAUSTED（不内部重试），实得 %v", err)
	}
	// 1 把钥 × 2 次同钥重试 = 2；若有内部重试会远超此数
	if got := atomic.LoadInt64(&hits); got > 4 {
		t.Fatalf("配额耗尽不应触发内部重试，上游请求数 %d（期望 ≤4）", got)
	}
}

// NextRateLimitRecovery：全池限流后返回最早恢复的冷却剩余；有可用钥时为 0
func TestNextRateLimitRecovery(t *testing.T) {
	p := NewKeyPool([]string{"a", "b"}, 0, 300)
	if w := p.NextRateLimitRecovery(); w != 0 {
		t.Fatalf("池内无冷却钥时应为 0，实得 %s", w)
	}
	for i := 0; i < 2; i++ {
		k, err := p.Acquire()
		if err != nil {
			t.Fatalf("Acquire 第%d次失败: %v", i+1, err)
		}
		p.ReportRateLimit(k)
	}
	// 两把钥都进 20s 短冷却：等待估算应落在 (0, 20s]
	if w := p.NextRateLimitRecovery(); w <= 0 || w > 20*time.Second {
		t.Fatalf("全池限流后恢复估算应在 (0,20s]，实得 %s", w)
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

// TestKeyPoolAdvanceRotatesAccounts 账号级轮询（20260923 修复的回归测试）。
//
// 背景：KeyPool.acquire 把游标停在**刚返回的那把钥**上（粘性，见 TestStickyKey），
// 而 markUse 在 RPM<=0 时直接 return → 调用方不显式 Advance() 就永远只用首钥。
// Kiro 每个账号额度独立（50 积分/月），必须靠 Advance() 轮换分摊额度——
// 生产实锤：不轮换时 idx=0 用掉 48.44/50（仅剩 4 次），其余 19 号用量近 0。
func TestKeyPoolAdvanceRotatesAccounts(t *testing.T) {
	p := NewKeyPool([]string{"k0", "k1", "k2"}, 0, 300)
	// 粘性前提：不 Advance 时始终取同一把
	a1, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	a2, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if a1.Idx != 0 || a2.Idx != 0 {
		t.Fatalf("未 Advance 时应粘住首钥 idx=0，实际 %d,%d", a1.Idx, a2.Idx)
	}
	// 每取一把即 Advance：应呈 0→1→2→0 轮询
	var got []int
	for i := 0; i < 4; i++ {
		k, err := p.Acquire()
		if err != nil {
			t.Fatalf("第 %d 次 Acquire: %v", i, err)
		}
		got = append(got, k.Idx)
		p.Advance()
	}
	want := []int{0, 1, 2, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("账号轮询序列应为 %v，实际 %v", want, got)
		}
	}
}
