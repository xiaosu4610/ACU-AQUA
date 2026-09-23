// 计费审计回归（20260919）：无上游 usage 时不得按保底少记（站长红线：宁可多记不可少记）。
//
// 生产实锤：tide 线 202 笔 estimated 请求合计 78,789 输出 token 只收 113,140 微元，
// 旧实现落 FloorMicro(1000)，按价目应收约 60 万微元——少记约 5 倍。
package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"acu-aqua/gateway/internal/config"
	"acu-aqua/gateway/internal/db"
	"acu-aqua/gateway/internal/seeding"
)

// newNoUsageApp 构造"上游不报 usage"的测试站：
// 非流式返回长正文且无 usage 字段；流式返回长正文 SSE 且末帧 usage 为 null。
// 模型价目：in 48000 / out 168000（rate10），floor 1000 —— 长输出应收远超保底。
func newNoUsageApp(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	tmp := t.TempDir()
	longText := strings.Repeat("这是一段用于计费审计的测试正文内容。", 200) // 约 6400 字节
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			w.WriteHeader(404)
			return
		}
		// 按请求体 stream 判定（网关不转发自定义 header，只能看 body）
		rb, _ := io.ReadAll(r.Body)
		if strings.Contains(string(rb), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			// 分片吐正文，末帧 usage:null（模拟不报 usage 的上游）
			for i := 0; i < 4; i++ {
				frame := map[string]any{
					"choices": []any{map[string]any{"delta": map[string]any{"content": longText}}},
				}
				fb, _ := json.Marshal(frame)
				_, _ = w.Write([]byte("data: " + string(fb) + "\n\n"))
			}
			_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":null}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		resp := map[string]any{
			"id": "x", "object": "chat.completion",
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": longText}}},
			// 关键：不带 usage
		}
		ob, _ := json.Marshal(resp)
		_, _ = w.Write(ob)
	}))
	t.Cleanup(up.Close)

	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Lines = []config.Line{{
		ID: "nv", Name: "无usage线", Mode: "per_token", BaseURL: up.URL, Keys: []string{"sk-up-1"},
		VipNum: 1, VipDen: 1,
		Models: []config.Model{{
			SiteID: "longout", UpstreamID: "vendor-longout",
			InSellRate10: 48000, CacheSellRate10: 10000, OutSellRate10: 168000,
			InCostRate10: 30000, CacheCostRate10: 7500, OutCostRate10: 105000,
		}},
	}}

	d, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.InitTables(d); err != nil {
		t.Fatal(err)
	}
	if err := seeding.SeedAll(d, cfg); err != nil {
		t.Fatal(err)
	}
	return New(cfg, &db.DBx{DB: d}), up
}

// TestNoUsageBilling_NotFloor 无 usage 的按量请求必须按观测内容计费，不得落保底。
func TestNoUsageBilling_NotFloor(t *testing.T) {
	app, _ := newNoUsageApp(t)
	h := app.Routes()

	seedRegCode(t, app, "nou@t.dev")
	rec, out := doJSON(t, h, "POST", "/v1/user/register", "", map[string]string{
		"username": "nouusage", "email": "nou@t.dev", "password": "password123", "code": "852341"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)
	if _, err := app.DB.Exec("UPDATE users SET balance_micro=10000000 WHERE email='nou@t.dev'"); err != nil {
		t.Fatal(err)
	}
	balOf := func() int64 {
		var b int64
		_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE email='nou@t.dev'").Scan(&b)
		return b
	}

	// —— 非流式：上游无 usage，正文约 6400 字节 ——
	b0 := balOf()
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "nv/longout", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 3000})
	if rec.Code != 200 {
		t.Fatalf("非流式请求失败: %d %s", rec.Code, rec.Body.String())
	}
	charged := b0 - balOf()
	if charged <= 1000 {
		t.Fatalf("无 usage 落保底（漏记）：实扣 %d 微元，应远大于保底 1000", charged)
	}
	// 6400 字节正文 → estOut=1600 tok → 1600*168000/10000 = 26880；再加输入侧
	if charged < 26000 {
		t.Fatalf("保守估算偏低：实扣 %d，期望 ≥26000（观测 6400 字节正文）", charged)
	}
	var state, src string
	var amt int64
	_ = app.DB.QueryRow(`SELECT bill_state, usage_source, bill_amount_micro FROM requests
		WHERE model='nv/longout' AND stream_mode=0 ORDER BY rowid DESC LIMIT 1`).Scan(&state, &src, &amt)
	if state != "billed" || src != "estimated" {
		t.Fatalf("账目口径错误：state=%s src=%s（应 billed/estimated）", state, src)
	}
	if amt != charged {
		t.Fatalf("请求账与余额扣减不一致：amount=%d charged=%d", amt, charged)
	}

	// —— 流式：同样无 usage，按已转发正文字节估算 ——
	b1 := balOf()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(
		`{"model":"nv/longout","messages":[{"role":"user","content":"hi"}],"max_tokens":3000,"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("流式请求失败: %d %s", w.Code, w.Body.String())
	}
	streamCharged := b1 - balOf()
	if streamCharged <= 1000 {
		t.Fatalf("流式无 usage 落保底（漏记）：实扣 %d 微元，应远大于保底 1000", streamCharged)
	}

	// —— 面值成本必须留痕（旧实现 face=0 → 利润统计虚高） ——
	var face int64
	_ = app.DB.QueryRow(`SELECT face_cost_micro FROM requests
		WHERE model='nv/longout' AND stream_mode=0 ORDER BY rowid DESC LIMIT 1`).Scan(&face)
	if face <= 0 {
		t.Fatalf("无 usage 时面值成本未留痕（face=%d）：成本台账少记会掩盖真实亏损", face)
	}
}

// TestPerCallCostRecorded 按次请求的成本台账必须记账（20260919 计费审计）。
//
// 生产实锤：aqua 线 33,754 笔 billed 中 32,763 笔 face_cost_micro=0（97% 漏记）——
// 按次成本配在 PerCallCost，而结算统一走 FaceCostMicro（三段 rate10），按次模型
// 不配 rate10 成本字段 → 恒返回 0，收入 2.73 亿微元对应的成本只记了 0.85 亿。
func TestPerCallCostRecorded(t *testing.T) {
	app, _, _ := newTestApp(t)
	h := app.Routes()

	seedRegCode(t, app, "pcc@t.dev")
	rec, out := doJSON(t, h, "POST", "/v1/user/register", "", map[string]string{
		"username": "pccuser", "email": "pcc@t.dev", "password": "password123", "code": "852341"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)
	_, _ = app.DB.Exec("UPDATE users SET balance_micro=1000000 WHERE email='pcc@t.dev'")

	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 100})
	if rec.Code != 200 {
		t.Fatalf("按次请求失败: %d", rec.Code)
	}
	var amt, face int64
	_ = app.DB.QueryRow(`SELECT bill_amount_micro, face_cost_micro FROM requests
		WHERE model='c/pc1' ORDER BY rowid DESC LIMIT 1`).Scan(&amt, &face)
	if amt != 2000 {
		t.Fatalf("按次应实收单价 2000，得 %d", amt)
	}
	// 测试线 pc1 未配 per_call_cost（=0），此时成本台账为 0 属正常（无成本数据）；
	// 关键回归点是**配了成本的按次模型必须记账** —— 用一条带成本的按次模型验证。
	// 直接改库模拟管理台配置成本后重跑。
	if _, err := app.DB.Exec("UPDATE admin_line_models SET per_call_cost=1500 WHERE line_id='c' AND site_id='pc1'"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB.Exec("UPDATE pricing SET price_micro=2000 WHERE model='c/pc1'"); err != nil {
		t.Fatal(err)
	}
	if err := app.reloadLines(); err != nil {
		t.Fatal(err)
	}
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "c/pc1", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 100})
	if rec.Code != 200 {
		t.Fatalf("按次请求（带成本）失败: %d", rec.Code)
	}
	_ = app.DB.QueryRow(`SELECT bill_amount_micro, face_cost_micro FROM requests
		WHERE model='c/pc1' ORDER BY rowid DESC LIMIT 1`).Scan(&amt, &face)
	if face != 1500 {
		t.Fatalf("按次成本台账漏记：期望 1500（per_call_cost），得 %d（修复前恒为 0）", face)
	}
}

// TestNoUsageBilling_CostCovered 售价必须覆盖成本（保本红线，含 3% 通道费）。
func TestNoUsageBilling_CostCovered(t *testing.T) {
	app, _ := newNoUsageApp(t)
	h := app.Routes()

	seedRegCode(t, app, "cov@t.dev")
	rec, out := doJSON(t, h, "POST", "/v1/user/register", "", map[string]string{
		"username": "covuser", "email": "cov@t.dev", "password": "password123", "code": "852341"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)
	_, _ = app.DB.Exec("UPDATE users SET balance_micro=10000000 WHERE email='cov@t.dev'")

	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "nv/longout", "messages": []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 3000})
	if rec.Code != 200 {
		t.Fatalf("请求失败: %d", rec.Code)
	}
	var amt, face int64
	_ = app.DB.QueryRow(`SELECT bill_amount_micro, face_cost_micro FROM requests
		WHERE model='nv/longout' ORDER BY rowid DESC LIMIT 1`).Scan(&amt, &face)
	if face <= 0 {
		t.Fatalf("成本台账缺失: face=%d", face)
	}
	// 实收须覆盖成本（含 3% 通道费 → amt*0.97 >= face）
	if amt*97 < face*100 {
		t.Fatalf("亏本：实收 %d 微元未覆盖成本 %d 微元（含 3%% 通道费需 ≥%d）", amt, face, (face*100+96)/97)
	}
}

// TestWallet2_ForcedModelWalletSplit 2 号折扣钱包强制模型-钱包划分（20260924 站长指令）。
//
// 规则：存在 pricing(model,'wallet2') 行 → 该模型**只能**用 2 号钱包 + 折扣价；
// 主钱包余额再多也调不了（**绝不静默回落**——回落会按原价扣主钱包，用户以为在打折）。
//
// 覆盖两个真实缺陷场景：
//  1. 预扣与结算必须**同一价格组**：曾出现"预扣按 wallet2 折扣价、结算按 normal 原价"
//     → 折扣钱包被多扣一倍（生产实测 500 预扣 → 1000 结算）。
//  2. 余额不足时主钱包不得被扣（两钱包资金完全独立）。
func TestWallet2_ForcedModelWalletSplit(t *testing.T) {
	app, _ := newNoUsageApp(t)
	h := app.Routes()

	// wallet2 价目 = normal 的一半（官方原价 × 0.5）；floor 也减半
	if _, err := app.DB.Exec(`INSERT INTO pricing
		(model, price_micro, starts_at, ends_at, note, mode, floor_micro, in_rate10, cache_rate10, out_rate10, grp)
		VALUES ('nv/longout', 0, 0, NULL, 'test wallet2', 'per_token', 500, 24000, 5000, 84000, 'wallet2')`); err != nil {
		t.Fatal(err)
	}

	seedRegCode(t, app, "w2@t.dev")
	rec, out := doJSON(t, h, "POST", "/v1/user/register", "", map[string]string{
		"username": "w2user", "email": "w2@t.dev", "password": "password123", "code": "852341"})
	if rec.Code != 200 {
		t.Fatalf("注册失败: %d %v", rec.Code, out)
	}
	tok := out["token"].(string)

	// ① 只有主钱包有钱 → 必须拒绝（折扣钱包余额不足），且**主钱包分文不动**
	_, _ = app.DB.Exec("UPDATE users SET balance_micro=10000000, balance2_micro=0 WHERE email='w2@t.dev'")
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "nv/longout", "messages": []map[string]string{{"role": "user", "content": "hi"}}, "max_tokens": 64})
	if rec.Code != 429 {
		t.Fatalf("折扣钱包无余额应 429 拒绝（不回落主钱包），实得 %d %s", rec.Code, rec.Body.String())
	}
	var b1, b2 int64
	_ = app.DB.QueryRow("SELECT balance_micro, balance2_micro FROM users WHERE email='w2@t.dev'").Scan(&b1, &b2)
	if b1 != 10000000 || b2 != 0 {
		t.Fatalf("拒绝路径不应改动任何钱包：主 %d（期望 10000000），折扣 %d（期望 0）", b1, b2)
	}

	// ② 折扣钱包有钱 → 200，且扣的是折扣钱包；主钱包保持 0（分文不扣）
	_, _ = app.DB.Exec("UPDATE users SET balance_micro=0, balance2_micro=10000000 WHERE email='w2@t.dev'")
	rec, _ = doJSON(t, h, "POST", "/v1/chat/completions", tok, map[string]any{
		"model": "nv/longout", "messages": []map[string]string{{"role": "user", "content": "hi"}}, "max_tokens": 3000})
	if rec.Code != 200 {
		t.Fatalf("折扣钱包有余额应放行，实得 %d %s", rec.Code, rec.Body.String())
	}
	_ = app.DB.QueryRow("SELECT balance_micro, balance2_micro FROM users WHERE email='w2@t.dev'").Scan(&b1, &b2)
	if b1 != 0 {
		t.Fatalf("主钱包被误扣：%d（期望 0）——两钱包必须资金独立", b1)
	}
	charged := int64(10000000) - b2
	if charged <= 0 {
		t.Fatalf("折扣钱包未被扣费：余额 %d", b2)
	}

	// ③ 预扣与结算必须同口径：结算金额须按 **wallet2 折扣价** 而非 normal 原价。
	//    上游正文 = 18 字 × 200 = 3600 token（CJK 按 1 token/字）。
	//      wallet2 out=84000 → 3600×8.4 + 输入 ≈ 30252 微元
	//      normal  out=168000 → 3600×16.8 + 输入 ≈ 60504 微元（恰为两倍）
	//    实扣若接近后者即为"结算走错价目组"缺陷（用户被多扣一倍）。
	var billed int64
	_ = app.DB.QueryRow(`SELECT bill_amount_micro FROM requests
		WHERE model='nv/longout' ORDER BY rowid DESC LIMIT 1`).Scan(&billed)
	if billed > 45000 {
		t.Fatalf("结算未按 wallet2 折扣价：实扣 %d 微元（折扣价约 30252，normal 原价约 60504）", billed)
	}
	if billed != charged {
		t.Fatalf("请求账与余额扣减不一致：amount=%d charged=%d", billed, charged)
	}

	// ④ 流水 wallet 列必须记 2（对账/补偿任务据此回退到原钱包）
	var w int
	if err := app.DB.QueryRow(`SELECT wallet FROM balance_flows
		WHERE user_id=(SELECT id FROM users WHERE email='w2@t.dev') AND type='billed'
		ORDER BY rowid DESC LIMIT 1`).Scan(&w); err != nil {
		t.Fatal(err)
	}
	if w != 2 {
		t.Fatalf("结算流水 wallet 期望 2（折扣钱包），实得 %d", w)
	}
}
