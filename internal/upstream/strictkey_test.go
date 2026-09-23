package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// 严格专属钥模式（StrictKeyIdx）回归 —— 20260919 分组隔离线。
//
// 背景：aqua 线两把钥分属上游不同分组（V4F代理 / V4.1代理），能力不重叠：
//   - 钥0（V4F代理）   → deepseek-v4-flash / deepseek-v4-pro
//   - 钥1（V4.1代理）  → deepseek-v4-1-flash / glm-5.3 / glm-5.3-flash / kimi-k3
// 未开严格模式时，网关会"专属钥不可用 → 降级通用池"，即打到另一把钥——
// 而该分组根本不含此模型，必然 503（No available channel for model X under group Y），
// 白白浪费一次请求并拖长等待。生产实锤：glm-5.3 成功率仅 14.3%。

// TestStrictKeyIdx_NoFallbackWhenDedicatedKeyDead 严格模式：专属钥判死时**绝不降级**
func TestStrictKeyIdx_NoFallbackWhenDedicatedKeyDead(t *testing.T) {
	var hits0, hits1 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer sk-0" {
			atomic.AddInt64(&hits0, 1)
		} else {
			atomic.AddInt64(&hits1, 1)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	c.Line.StrictKeyIdx = true

	// 把 idx=1（V4.1代理那把）永久判死，模拟专属钥不可用
	k1, err := c.Pool.AcquireFor(1)
	if err != nil {
		t.Fatalf("取 idx=1 失败: %v", err)
	}
	c.Pool.ReportDeadPermanent(k1)

	// 绑定 idx=1 的模型：应明确报错，且**一次都不许打到 sk-0**
	_, _, err = c.DoKey(context.Background(), []byte(`{}`), false, "/chat/completions", 1)
	if err == nil || !strings.Contains(err.Error(), "KEY_IDX_UNAVAILABLE") {
		t.Fatalf("严格模式应返回 KEY_IDX_UNAVAILABLE，得 %v", err)
	}
	if hits0 != 0 {
		t.Fatalf("严格模式不得降级到其他钥（分组不含该模型），sk-0 被打了 %d 次", hits0)
	}
	if hits1 != 0 {
		t.Fatalf("专属钥已判死，不应再被请求，sk-1 被打了 %d 次", hits1)
	}
}

// TestStrictKeyIdx_OffStillFallsBack 非严格模式：保持原降级容错行为（同分组多钥线）
func TestStrictKeyIdx_OffStillFallsBack(t *testing.T) {
	var hits0, hits1 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer sk-0" {
			atomic.AddInt64(&hits0, 1)
		} else {
			atomic.AddInt64(&hits1, 1)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0) // StrictKeyIdx 默认 false
	k1, err := c.Pool.AcquireFor(1)
	if err != nil {
		t.Fatalf("取 idx=1 失败: %v", err)
	}
	c.Pool.ReportDeadPermanent(k1)

	resp, k, err := c.DoKey(context.Background(), []byte(`{}`), false, "/chat/completions", 1)
	if err != nil {
		t.Fatalf("非严格模式应降级成功，得错误 %v", err)
	}
	if resp == nil || resp.StatusCode != 200 {
		t.Fatalf("非严格模式降级后应 200，得 %v", resp)
	}
	if k == nil || k.RawIdx != 0 {
		t.Fatalf("降级后应使用 sk-0（idx=0），得 %+v", k)
	}
	if hits0 == 0 {
		t.Fatal("非严格模式应降级打到 sk-0，实际 0 次")
	}
}

// TestStrictKeyIdx_DedicatedKeyUsed 严格模式：专属钥健康时正常走专属钥（不轮询）
func TestStrictKeyIdx_DedicatedKeyUsed(t *testing.T) {
	var hits0, hits1 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer sk-0" {
			atomic.AddInt64(&hits0, 1)
		} else {
			atomic.AddInt64(&hits1, 1)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	c.Line.StrictKeyIdx = true

	// 连续 5 次绑定 idx=1 的请求：必须全部走 sk-1，绝不轮询到 sk-0
	for i := 0; i < 5; i++ {
		resp, k, err := c.DoKey(context.Background(), []byte(`{}`), false, "/chat/completions", 1)
		if err != nil {
			t.Fatalf("第 %d 次失败: %v", i+1, err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("第 %d 次非 200: %d", i+1, resp.StatusCode)
		}
		if k.RawIdx != 1 {
			t.Fatalf("第 %d 次未走专属钥 idx=1，得 idx=%d", i+1, k.RawIdx)
		}
		_ = resp.Body.Close()
	}
	if hits0 != 0 {
		t.Fatalf("严格模式不得轮询到 sk-0，实际被打了 %d 次", hits0)
	}
	if hits1 != 5 {
		t.Fatalf("sk-1 应被使用 5 次，实际 %d 次", hits1)
	}
}

// TestStrictKeyIdx_ModelUnavailableNoFallback 严格模式：上游返回"分组无此模型"时不降级
//
// 对应真实场景：模型绑定的钥被误配到不含该模型的分组 → 上游 503
// "No available channel for model X under group Y"。严格模式下不再换钥（换也白换），
// 直接把上游真实状态码交上层转译。
func TestStrictKeyIdx_ModelUnavailableNoFallback(t *testing.T) {
	var hits0, hits1 int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer sk-0" {
			atomic.AddInt64(&hits0, 1)
		} else {
			atomic.AddInt64(&hits1, 1)
		}
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"message":"No available channel for model glm-5.3 under group v4f代理 (distributor)"}}`))
	}))
	defer up.Close()

	c := testClient(up, []string{"sk-0", "sk-1"}, 0)
	c.Line.StrictKeyIdx = true

	_, _, err := c.DoKey(context.Background(), []byte(`{}`), false, "/chat/completions", 1)
	if err == nil {
		t.Fatal("严格模式下上游 503 应返回错误")
	}
	if hits0 != 0 {
		t.Fatalf("严格模式不得换钥，sk-0 被打了 %d 次", hits0)
	}
	if hits1 != 1 {
		t.Fatalf("应只打专属钥一次（不重试不换钥），实际 %d 次", hits1)
	}
}
