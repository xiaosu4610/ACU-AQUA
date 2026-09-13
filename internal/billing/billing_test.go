package billing

import "testing"

// 黄金用例：与 Rust 版生产实测逐笔对账（2026-09-10 生产 e2e 记录）
// 用例口径：tok_price = ni*in/10000 + ct*cache/10000 + cdt*out/10000（整数截断），max(保底)

func TestMeterTokens_GoldenQwen38Max(t *testing.T) {
	// 生产实测：qwen3.8-max normal 福利价（in 30000 / out 90000）
	// 72 in + 120 out → 1296 微元（Rust 版实扣 1296，分毫不差）
	p := &PricingInfo{Mode: "per_token", InRate10: 30000, CacheRate10: 3750, OutRate10: 90000, FloorMicro: 1000}
	got := MeterTokens(Usage{PromptTokens: 72, CompletionTokens: 120}, p)
	if got != 1296 {
		t.Errorf("qwen3.8-max 福利价: 期望 1296, 实得 %d", got)
	}
}

func TestMeterTokens_GoldenQwen38MaxNormal(t *testing.T) {
	// 同一笔 tokens 在原 5 折价（in 60000 / out 180000）→ 2592
	p := &PricingInfo{Mode: "per_token", InRate10: 60000, CacheRate10: 7500, OutRate10: 180000, FloorMicro: 1000}
	got := MeterTokens(Usage{PromptTokens: 72, CompletionTokens: 120}, p)
	if got != 2592 {
		t.Errorf("qwen3.8-max 五折价: 期望 2592, 实得 %d", got)
	}
}

func TestMeterTokens_GoldenVip(t *testing.T) {
	// 生产实测：qwen3.8-max vip 4.5 折（in 54000 / out 162000）
	// 74 in + 178 out → 3282 微元（Rust 版实扣 3282）
	p := &PricingInfo{Mode: "per_token", InRate10: 54000, CacheRate10: 6750, OutRate10: 162000, FloorMicro: 1000}
	got := MeterTokens(Usage{PromptTokens: 74, CompletionTokens: 178}, p)
	if got != 3282 {
		t.Errorf("qwen3.8-max vip: 期望 3282, 实得 %d", got)
	}
}

func TestMeterTokens_Floor(t *testing.T) {
	// 生产实测：glm-5.3-flash 用量 42 微元 < 保底 1000 → 实扣 1000（floor 兜底）
	p := &PricingInfo{Mode: "per_token", InRate10: 3600, CacheRate10: 1035, OutRate10: 12600, FloorMicro: 1000}
	got := MeterTokens(Usage{PromptTokens: 16, CompletionTokens: 30}, p)
	if got != 1000 {
		t.Errorf("保底兜底: 期望 1000, 实得 %d", got)
	}
}

func TestMeterTokens_Cache(t *testing.T) {
	// 缓存命中走 cache 价：1000 prompt（600 cached）+ 500 out
	// tok 价 = 80+34+350 = 464，但 < 保底 1000 → floor 兜底取 1000
	p := &PricingInfo{Mode: "per_token", InRate10: 2000, CacheRate10: 575, OutRate10: 7000, FloorMicro: 1000}
	got := MeterTokens(Usage{PromptTokens: 1000, CachedTokens: 600, CompletionTokens: 500}, p)
	if got != 1000 {
		t.Errorf("缓存命中+保底: 期望 1000, 实得 %d", got)
	}
	// 无保底（FloorMicro=0）场景：纯三段价 464
	p.FloorMicro = 0
	got = MeterTokens(Usage{PromptTokens: 1000, CachedTokens: 600, CompletionTokens: 500}, p)
	if got != 464 {
		t.Errorf("缓存命中无保底: 期望 464, 实得 %d", got)
	}
}

func TestFaceCostMicro_Golden(t *testing.T) {
	// 生产实测：qwen3.8-max 面价（in 120000 / cache 15000 / out 360000 每万token，rate10 口径）
	// 74 in + 178 out → 7296 微元（Rust 版四舍五入精确命中）
	got := FaceCostMicro(Usage{PromptTokens: 74, CompletionTokens: 178}, 120_000, 15_000, 360_000)
	if got != 7296 {
		t.Errorf("面值成本: 期望 7296, 实得 %d", got)
	}
	// glm-5.3-flash 面价（in 8000 / cache 2300 / out 28000 每万token）
	// 16 in + 30 out → round(16*0.8)+round(30*2.8)=13+84=97（Rust 版实落 97）
	got = FaceCostMicro(Usage{PromptTokens: 16, CompletionTokens: 30}, 8_000, 2_300, 28_000)
	if got != 97 {
		t.Errorf("glm 面值成本: 期望 97, 实得 %d", got)
	}
}

func TestBreakevenFloor(t *testing.T) {
	// 按次线：cost 1000 → (1000*100+96)/97 = 1031（ceil(1000/0.97)，3% 通道费口径）
	if got := BreakevenFloor(1000, "per_call"); got != 1031 {
		t.Errorf("保本线: 期望 1031, 实得 %d", got)
	}
	// 按量线返回 0（防线由播种价目保证）
	if got := BreakevenFloor(1000, "per_token"); got != 0 {
		t.Errorf("按量保本线: 期望 0, 实得 %d", got)
	}
}
