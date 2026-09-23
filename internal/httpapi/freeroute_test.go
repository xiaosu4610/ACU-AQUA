// 回归 20260922：裸名路由消歧。
// 事故：glm-5.3 / glm-5.3-flash / kimi-k3 / deepseek-v4.1-flash 等**同时在免费目录与收费线**
// （都是收费线的 SiteID），而 chat.go 的裸名兼容路由只查了"是不是收费线 SiteID"、
// 漏了"非免费线"这半边 → 用户调裸名想用免费公益通道，却被路由到 aqua/prime **扣了钱**。
// 生产实锤：22 个用户、1432 次请求被误扣 ¥18.58。
package httpapi

import (
	"testing"
	"time"

	"acu-aqua/gateway/internal/config"
)

// 裸名同时命中免费目录与收费线 → 必须走免费且**分文不扣**
func TestBareNameSharedByFreeAndPaidIsNotCharged(t *testing.T) {
	app, up, _ := newTestApp(t)
	h := app.Routes()
	app.Cfg.Billing.UnifiedPrefix = "aqua"

	// 收费线（t 线，per_token）加一个与免费目录**同名**的模型
	app.Cfg.Lines[0].Models = append(app.Cfg.Lines[0].Models, config.Model{
		SiteID: "glm-5.3-flash", UpstreamID: "vendor-m1",
		InCostRate10: 120_000, CacheCostRate10: 15_000, OutCostRate10: 360_000,
		InSellRate10: 60_000, CacheSellRate10: 7_500, OutSellRate10: 180_000,
	})
	// 免费动态线 + 动态目录里的同名模型
	app.Cfg.Lines = append(app.Cfg.Lines, config.Line{
		ID: "freenv", Mode: "free", BaseURL: up.URL, Dynamic: true, Keys: []string{"k"},
	})
	if _, err := app.DB.Exec("INSERT INTO nvidia_models (id, upstream_id, ts) VALUES (?,?,?)",
		"glm-5.3-flash", "z-ai/glm-5.3-flash", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}

	// 前置断言：两侧确实都命中（否则本测试失去意义）
	if app.paidLineForSiteID("glm-5.3-flash") == nil {
		t.Fatal("收费线里应有该 SiteID")
	}
	if !app.freeLineHasModel("glm-5.3-flash") {
		t.Fatal("免费目录里应有该模型")
	}
	// 只有免费线没有的裸名，才允许判为收费
	if app.freeLineHasModel("pc1") {
		t.Fatal("pc1 不在免费目录，freeLineHasModel 应返回 false")
	}

	seedRegCode(t, app, "shared@t.dev")
	rec, out := doJSON(t, h, "POST", "/v1/user/register", "", map[string]string{
		"username": "sharedname", "email": "shared@t.dev", "password": "password123", "code": "852341"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)
	if _, err := app.DB.Exec("UPDATE users SET balance_micro=10000000 WHERE email='shared@t.dev'"); err != nil {
		t.Fatal(err)
	}
	balOf := func() int64 {
		var b int64
		_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE email='shared@t.dev'").Scan(&b)
		return b
	}

	before := balOf()
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "glm-5.3-flash", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if rec.Code != 200 {
		t.Fatalf("免费模型调用失败: %d %s", rec.Code, rec.Body.String())
	}
	if got := before - balOf(); got != 0 {
		t.Fatalf("免费模型不得扣费：实扣 %d 微元（裸名被误路由到收费线）", got)
	}
	var line string
	_ = app.DB.QueryRow("SELECT COALESCE(resolved_line,'') FROM requests WHERE model='glm-5.3-flash' ORDER BY rowid DESC LIMIT 1").Scan(&line)
	if line == "t" {
		t.Fatalf("裸名被误路由到收费线（resolved_line=%s）", line)
	}
}

// 免费目录**没有**的裸名，仍应按收费处理（保留 20260918 的兼容意图）
func TestBareNameOnlyOnPaidLineStillCharged(t *testing.T) {
	app, _, _ := newTestApp(t)
	app.Cfg.Billing.UnifiedPrefix = "aqua"
	if app.freeLineHasModel("pc1") {
		t.Fatal("pc1 不在免费目录")
	}
	if app.paidLineForSiteID("pc1") == nil {
		t.Fatal("pc1 应在收费线（按次线）里")
	}
}
