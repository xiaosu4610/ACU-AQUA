package billing

import (
	"bytes"
	"testing"
)

// 20260919 计费审计：无上游 usage 时的保守估算（亏本防线）
// 背景：生产实测 tide 线 202 笔 estimated 请求 78,789 输出 token 只收 113,140 微元，
// 旧实现按 FloorMicro(1000) 计费，按价目应收约 60 万微元——少记约 5 倍。

func TestMeterObserved_LongOutputNotFloor(t *testing.T) {
	// glm-5.3 六折价：in 48000 / out 168000（rate10）
	// 20260923 标准化：估算由调用方完成（EstTokens / EstPromptTokens），
	// 本函数只做「token 数 × 单价」换算——此处直接传 token 数。
	p := &PricingInfo{Mode: "per_token", InRate10: 48000, OutRate10: 168000, FloorMicro: 1000}
	got := MeterObserved(p, 1000, 15000)
	// 1000 tok → 1000*48000/10000 = 4800
	// 15000 tok → 15000*168000/10000 = 252000
	want := int64(4800 + 252000)
	if got != want {
		t.Errorf("观测估算: 期望 %d, 实得 %d（旧实现会落 1000 保底，少记 %d 倍）",
			want, got, want/1000)
	}
	if got <= p.FloorMicro {
		t.Errorf("长输出不得落保底：实得 %d 应远大于保底 %d", got, p.FloorMicro)
	}
}

func TestMeterObserved_FloorStillApplies(t *testing.T) {
	// 极短输出：观测估算低于保底时必须抬到保底（口径与 MeterTokens 一致）
	p := &PricingInfo{Mode: "per_token", InRate10: 1000, OutRate10: 2000, FloorMicro: 1000}
	got := MeterObserved(p, 20, 10)
	if got != 1000 {
		t.Errorf("低于保底应取保底: 期望 1000, 实得 %d", got)
	}
}

func TestMeterObserved_NonTokenModeZero(t *testing.T) {
	// per_call 不走观测估算（按次收单价，与 usage 无关）
	p := &PricingInfo{Mode: "per_call", PriceMicro: 5000}
	if got := MeterObserved(p, 100000, 100000); got != 0 {
		t.Errorf("per_call 应返回 0（由调用方收单价）: 实得 %d", got)
	}
	if got := MeterObserved(nil, 1000, 1000); got != 0 {
		t.Errorf("nil 价目应返回 0: 实得 %d", got)
	}
}

func TestMeterObserved_NegativeClamp(t *testing.T) {
	// 负 token（异常输入）必须 clamp 到 0，不得翻转记账方向
	p := &PricingInfo{Mode: "per_token", InRate10: 48000, OutRate10: 168000, FloorMicro: 0}
	if got := MeterObserved(p, -100, -100); got != 0 {
		t.Errorf("负 token 应 clamp 为 0: 实得 %d", got)
	}
}

func TestFaceCostObserved_NotZero(t *testing.T) {
	// 无 usage 时成本必须留痕（旧实现 face=0 → 利润统计虚高，掩盖真实亏损）
	// deepseek-v4-pro 成本：in 33750 / out 101250（入参为 token 数）
	got := FaceCostObserved(33750, 101250, 1000, 15000)
	// roundM(1000, 33750) = 3375; roundM(15000, 101250) = 151875
	if want := int64(3375 + 151875); got != want {
		t.Errorf("成本估算: 期望 %d, 实得 %d", want, got)
	}
	if got == 0 {
		t.Error("成本估算不得为 0（会掩盖亏损）")
	}
}

// —— 20260923 标准化：统一 token 估算 ——

func TestEstTokens_AsciiAndCJK(t *testing.T) {
	// 纯 ASCII：8 字符 → 8/4 = 2 token
	if got := EstTokens("abcdefgh"); got != 2 {
		t.Errorf("8 个 ASCII 应估 2 token: 实得 %d", got)
	}
	// 纯中文：4 个汉字 → 4 token（旧口径 字节/4 = 12/4 = 3，低估 25%）
	if got := EstTokens("你好世界"); got != 4 {
		t.Errorf("4 个汉字应估 4 token（CJK 1 token/字）: 实得 %d", got)
	}
	// 空文本下限 1（防 0 计费）
	if got := EstTokens(""); got != 1 {
		t.Errorf("空文本应估 1: 实得 %d", got)
	}
}

func TestEstPromptTokens_StripsJSONStructure(t *testing.T) {
	// 核心回归：旧口径按 messages 原文 JSON 字节 /4，把 role/content 字段名与引号括号
	// 都算成 token——`{"role":"user","content":"hi"}` 30 字节 → 旧估 7 token。
	// 新口径 = 正文 token（"hi"→1）+ 每消息框架开销 4 = 5。
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`)
	if got := EstPromptTokens(body); got != 5 {
		t.Errorf("单条 hi 应估 5 token（正文 1 + 框架 4）: 实得 %d", got)
	}
	// 关键性质：**结构开销不随消息条数按字节虚增**。
	// 10 条 hi：正文 7 + 框架 40 = 47；旧口径按原文 JSON 字节/4 ≈ 70，
	// 且随消息变长会持续膨胀（旧口径把每个字段名都算一次 token）。
	var sb []byte
	sb = append(sb, []byte(`{"messages":[`)...)
	for i := 0; i < 10; i++ {
		if i > 0 {
			sb = append(sb, ',')
		}
		sb = append(sb, []byte(`{"role":"user","content":"hi"}`)...)
	}
	sb = append(sb, []byte(`]}`)...)
	if got := EstPromptTokens(sb); got != 47 {
		t.Errorf("10 条 hi 应估 47（正文 7 + 框架 40，旧口径约 70）: 实得 %d", got)
	}
	// 长正文场景：结构开销占比应可忽略（旧口径在这里反而接近正确，
	// 真正的虚增发生在短消息密集会话）——正文 4000 字符 → 1000 + 4 = 1004
	long := bytes.Repeat([]byte("a"), 4000)
	if got := EstPromptTokens(append(append([]byte(`{"messages":[{"role":"user","content":"`), long...), []byte(`"}]}`)...)); got != 1004 {
		t.Errorf("4000 字符正文应估 1004: 实得 %d", got)
	}
}

func TestEstPromptTokens_MultimodalAndPrompt(t *testing.T) {
	// 多模态数组：只取 text 部分（图片不折算），正文 "abcd"→1 + 框架 4 = 5
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"abcd"},{"type":"image_url","image_url":{"url":"x"}}]}]}`)
	if got := EstPromptTokens(body); got != 5 {
		t.Errorf("多模态 text 部分 abcd 应估 5（正文 1 + 框架 4）: 实得 %d", got)
	}
	// completions 风格 prompt 字段（无 messages，无框架开销）
	if got := EstPromptTokens([]byte(`{"prompt":"abcdefgh"}`)); got != 2 {
		t.Errorf("prompt 字段 8 字符应估 2 token: 实得 %d", got)
	}
	// 空请求体下限 1
	if got := EstPromptTokens([]byte(`{}`)); got != 1 {
		t.Errorf("空 messages 应估 1: 实得 %d", got)
	}
}
