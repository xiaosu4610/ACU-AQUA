// 模型实时状态归一化回归（20260921）：免费模型卡片此前完全没有实时状态——
// 根因是 statusModelNorm 把 mode=free 的线整条跳过，且 SQL 用 resolved_line != '' 把免费流量滤掉了。
// 关键不变量：**同名模型在收费线与免费线必须落到不同站内 ID**
// （裸名 deepseek-v4-flash：收费线 → aqua/deepseek-v4-flash，免费线 → acu/deepseek-v4-flash），
// 二者靠 requests.resolved_line 是否为空区分，合并会互相串味。
package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"acu-aqua/gateway/internal/billing"
	"acu-aqua/gateway/internal/config"
)

// testLines 免费线（acu 带前缀 / 公益通道裸名）+ 收费线（与免费线同名模型）
func statusTestLines() []config.Line {
	return []config.Line{
		{ID: "acu", Mode: "free", Prefixed: true, Models: []config.Model{
			{SiteID: "deepseek-v4-flash", UpstreamID: "up-flash"},
		}},
		{ID: "nvidia", Mode: "free", Models: []config.Model{
			{SiteID: "nemotron-3-ultra-550b-a55b", UpstreamID: "up-nemotron"},
		}},
		{ID: "aqua", Mode: "per_call", Models: []config.Model{
			{SiteID: "deepseek-v4-flash", UpstreamID: "up-flash"},
		}},
	}
}

func TestStatusModelNorm_SplitsFreeAndPaidSameName(t *testing.T) {
	app, _, _ := newTestApp(t)
	app.Cfg.Billing.UnifiedPrefix = "aqua"
	app.Cfg.Lines = statusTestLines()

	free := app.statusModelNormFree()
	if got := free["deepseek-v4-flash"]; got != "acu/deepseek-v4-flash" {
		t.Fatalf("免费线裸名应归 acu/ 前缀，得 %q", got)
	}
	if got := free["nemotron-3-ultra-550b-a55b"]; got != "nemotron-3-ultra-550b-a55b" {
		t.Fatalf("非前缀免费线（公益通道）应保持裸名，得 %q", got)
	}
	paid := app.statusModelNorm()
	if got := paid["deepseek-v4-flash"]; got != "aqua/deepseek-v4-flash" {
		t.Fatalf("收费线裸名应归统一前缀，得 %q", got)
	}
	// 反向防串味：免费映射不得把收费模型名带进来
	if _, ok := free["aqua/deepseek-v4-flash"]; ok {
		t.Fatal("免费映射不应包含收费前缀 ID")
	}
}

func TestModelsStatus_IncludesFreeLineTraffic(t *testing.T) {
	app, _, _ := newTestApp(t)
	app.Cfg.Billing.UnifiedPrefix = "aqua"
	app.Cfg.Lines = statusTestLines()

	now := time.Now().Unix()
	insert := func(model, rline string) {
		t.Helper()
		if _, err := app.DB.Exec(`INSERT INTO requests
			(key_hash,endpoint,model,ok,latency_ms,first_ms,ts,tps,status_code,error,resolved_line)
			VALUES('h','chat',?,1,1000,300,?,50.0,200,'',?)`, model, now, rline); err != nil {
			t.Fatalf("插入 requests 失败: %v", err)
		}
	}
	insert("deepseek-v4-flash", "")     // 免费线（acu 自营）
	insert("deepseek-v4-flash", "aqua") // 收费线同名模型
	insert("nemotron-3-ultra-550b-a55b", "")

	rec := httptest.NewRecorder()
	app.handleModelsStatus(rec, httptest.NewRequest("GET", "/v1/models/status", nil))
	if rec.Code != 200 {
		t.Fatalf("状态接口应 200，得 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data []struct {
			Model      string  `json:"model"`
			Samples    int64   `json:"samples"`
			AvgFirstMs int64   `json:"avg_first_ms"`
			AvgTps     float64 `json:"avg_tps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	seen := map[string]int64{}
	for _, d := range out.Data {
		seen[d.Model] = d.Samples
	}
	for _, want := range []string{"acu/deepseek-v4-flash", "aqua/deepseek-v4-flash", "nemotron-3-ultra-550b-a55b"} {
		if seen[want] == 0 {
			t.Fatalf("状态列表应含 %q，实际：%v", want, seen)
		}
	}
}

// 免费线记账回归（20260921）：免费请求此前只写 tps，latency_ms / first_ms 恒为 0
// → /v1/models/status 里免费模型永远没有首字与总耗时，前端卡片只能显示 "--"。
// 这里锁住：流式免费请求要落总耗时 + 首字延迟（与收费路径 okRequestGen 同口径），
// 且必须保持免费语义（billed=0 / bill_amount_micro=0 / bill_state='free'）。
func TestOkFreeRequestGen_PersistsLatencyAndTTFT(t *testing.T) {
	app, _, _ := newTestApp(t)
	rid := app.insertRequestLine(1, "h", "chat", "free-m", true, "")

	// 总耗时 1500ms、生成阶段 900ms（首字之后）、首字延迟 600ms → tps = 100*1000/900
	app.okFreeRequestGen(rid, billing.Usage{PromptTokens: 10, CompletionTokens: 100}, 200, "actual", 1500, 900, 600)

	var lat, first, billed, amount int64
	var state string
	var tps float64
	if err := app.DB.QueryRow(
		"SELECT latency_ms, first_ms, tps, billed, bill_amount_micro, bill_state FROM requests WHERE rowid=?", rid,
	).Scan(&lat, &first, &tps, &billed, &amount, &state); err != nil {
		t.Fatalf("回读 requests 失败: %v", err)
	}
	if lat != 1500 {
		t.Fatalf("latency_ms 应落 1500，得 %d（免费线此前恒为 0）", lat)
	}
	if first != 600 {
		t.Fatalf("first_ms 应落 600，得 %d（免费线此前恒为 0）", first)
	}
	if tps < 110 || tps > 112 {
		t.Fatalf("tps 应按生成阶段 900ms 计 ≈111.1，得 %v", tps)
	}
	if billed != 0 || amount != 0 || state != "free" {
		t.Fatalf("免费语义被破坏：billed=%d amount=%d state=%q", billed, amount, state)
	}
}

// 非流式免费请求：无首帧概念，first_ms 记 0（与收费路径 okRequest 的 ttftMs=0 一致），
// 但 latency_ms 必须落库——否则非流式免费模型在状态卡上同样没有延迟。
func TestOkFreeRequest_NonStreamRecordsLatencyOnly(t *testing.T) {
	app, _, _ := newTestApp(t)
	rid := app.insertRequestLine(1, "h", "chat", "free-m", false, "")

	app.okFreeRequest(rid, billing.Usage{PromptTokens: 5, CompletionTokens: 50}, 200, "actual", 800)

	var lat, first int64
	if err := app.DB.QueryRow("SELECT latency_ms, first_ms FROM requests WHERE rowid=?", rid).Scan(&lat, &first); err != nil {
		t.Fatalf("回读 requests 失败: %v", err)
	}
	if lat != 800 {
		t.Fatalf("非流式 latency_ms 应落 800，得 %d", lat)
	}
	if first != 0 {
		t.Fatalf("非流式 first_ms 应为 0，得 %d", first)
	}
}
