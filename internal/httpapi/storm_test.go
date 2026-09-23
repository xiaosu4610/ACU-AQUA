package httpapi

import (
	"errors"
	"testing"
	"time"
)

// stormClear 清空断路器状态（各用例隔离）
func stormClear() {
	stormMu.Lock()
	defer stormMu.Unlock()
	stormTab = map[string]*stormState{}
}

// stormExpire 把冷却截止时间拨到过去（模拟 60s 冷却到期，免真实 sleep）
func stormExpire(key string) {
	stormMu.Lock()
	defer stormMu.Unlock()
	if s := stormTab[key]; s != nil {
		s.until = time.Now().Unix() - 1
	}
}

// stormFails 读当前累计失败数
func stormFails(key string) int {
	stormMu.Lock()
	defer stormMu.Unlock()
	if s := stormTab[key]; s != nil {
		return s.fails
	}
	return 0
}

// TestStormPaidLineCooldownExpiryResetsCounter 付费线：冷却到期即清零计数。
// 这是 20260923 生产事故（443 次 storm_cooldown 全打在付费模型、24 个付费用户被误伤）的核心修复：
// 原实现 fails 停在阈值之上 → 冷却到期后**任何单次失败**立刻再锁 60s，用户在上游抖动期永远解不开。
func TestStormPaidLineCooldownExpiryResetsCounter(t *testing.T) {
	stormClear()
	const k = "key-paid"
	for i := 0; i < stormThreshold; i++ {
		stormFail(k, true)
	}
	if stormCheck(k) {
		t.Fatalf("达到阈值 %d 次后应进入冷却", stormThreshold)
	}
	stormExpire(k)
	if !stormCheck(k) {
		t.Fatal("冷却到期后应放行")
	}
	if got := stormFails(k); got != 0 {
		t.Fatalf("付费线冷却到期应清零计数，实际 fails=%d（粘性锁定未修复）", got)
	}
	// 关键回归点：到期后单次失败**不得**立即重新锁定
	stormFail(k, true)
	if !stormCheck(k) {
		t.Fatal("付费线：到期后单次失败不应立即重新冷却（粘性锁定）")
	}
	if got := stormFails(k); got != 1 {
		t.Fatalf("付费线到期后计数应重新从 1 开始，实际 %d", got)
	}
}

// TestStormFreeLineKeepsCounterAfterExpiry 免费/众筹线：保持原严格口径（到期不清零）。
// 那才是当初要防的滥用面（acu 线单用户 24h 6100+ 笔秒败重试循环），不得放宽。
func TestStormFreeLineKeepsCounterAfterExpiry(t *testing.T) {
	stormClear()
	const k = "key-free"
	for i := 0; i < stormThreshold; i++ {
		stormFail(k, false)
	}
	if stormCheck(k) {
		t.Fatal("达到阈值后应进入冷却")
	}
	stormExpire(k)
	if !stormCheck(k) {
		t.Fatal("冷却到期后应放行")
	}
	if got := stormFails(k); got != stormThreshold {
		t.Fatalf("免费线到期后计数应保留 %d，实际 %d", stormThreshold, got)
	}
	// 免费线：到期后单次失败立即重新锁定（保留原有防滥用强度）
	stormFail(k, false)
	if stormCheck(k) {
		t.Fatal("免费线：到期后单次失败应立即重新冷却（原防滥用口径不得放宽）")
	}
}

// TestStormStaleFailuresOutsideWindowDoNotAccumulate 窗口外的陈旧失败不计入，
// 防「数小时前几次抖动凑够阈值」（20260923 新增 stormWindowSecs）。
func TestStormStaleFailuresOutsideWindowDoNotAccumulate(t *testing.T) {
	stormClear()
	const k = "key-stale"
	for i := 0; i < stormThreshold-1; i++ {
		stormFail(k, true)
	}
	// 把最近失败时刻推到窗口之外
	stormMu.Lock()
	stormTab[k].lastFail = time.Now().Unix() - stormWindowSecs - 5
	stormMu.Unlock()
	stormFail(k, true) // 窗口外 → 应重新从 1 计
	if got := stormFails(k); got != 1 {
		t.Fatalf("窗口外陈旧失败应被丢弃、计数重置为 1，实际 %d", got)
	}
	if !stormCheck(k) {
		t.Fatal("不应因陈旧失败进入冷却")
	}
}

// TestStormResetClearsAll 上游成功后彻底清零
func TestStormResetClearsAll(t *testing.T) {
	stormClear()
	const k = "key-reset"
	for i := 0; i < stormThreshold; i++ {
		stormFail(k, false)
	}
	stormReset(k)
	if !stormCheck(k) {
		t.Fatal("reset 后应放行")
	}
	if got := stormFails(k); got != 0 {
		t.Fatalf("reset 后 fails 应为 0，实际 %d", got)
	}
}

// TestStormCountable 付费线豁免上游 5xx 状态错误（上游容量问题，非用户滥用）；
// 网络级失败仍计入；免费线一律计入。
func TestStormCountable(t *testing.T) {
	e503 := errors.New("UPSTREAM_STATUS_503")
	e500 := errors.New("UPSTREAM_STATUS_500")
	netErr := errors.New(`Post "https://api.kabuai.cn/v1/chat/completions": dial tcp: i/o timeout`)
	e429 := errors.New("UPSTREAM_STATUS_429")

	cases := []struct {
		name string
		mode string
		err  error
		want bool
	}{
		{"付费线-503 豁免", "per_call", e503, false},
		{"付费线-500 豁免", "per_token", e500, false},
		{"付费线-429 仍计入（上游限流属可用信号）", "per_call", e429, true},
		{"付费线-网络失败仍计入", "per_call", netErr, true},
		{"付费线-nil 错误计入（首帧超时场景）", "per_call", nil, true},
		{"免费线-503 仍计入（原防滥用口径）", "free", e503, true},
		{"众筹线-503 仍计入", "crowd", e503, true},
	}
	for _, c := range cases {
		if got := stormCountable(c.mode, c.err); got != c.want {
			t.Errorf("%s: stormCountable(%q, %v) = %v，期望 %v", c.name, c.mode, c.err, got, c.want)
		}
	}
}

// TestStormPaidModeMode 模式判定
func TestStormPaidMode(t *testing.T) {
	for _, m := range []string{"per_call", "per_token"} {
		if !stormPaidMode(m) {
			t.Errorf("%s 应判定为付费线", m)
		}
	}
	for _, m := range []string{"free", "crowd", "official", ""} {
		if stormPaidMode(m) {
			t.Errorf("%s 不应判定为付费线", m)
		}
	}
}
