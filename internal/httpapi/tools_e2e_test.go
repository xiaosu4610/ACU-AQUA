package httpapi

// 工具箱 / 竞技场 / stats / models/{id} 端点回归测试（本地工具零上游依赖）

import (
	"strings"
	"testing"
)

func TestLocalToolsEndpoints(t *testing.T) {
	app, _, _ := newTestApp(t)
	h := app.Routes()

	// hash：sha256("abc") 已知值
	_, out := doJSON(t, h, "POST", "/v1/tools/hash", "", map[string]any{"text": "abc", "algo": "sha256"})
	if out["digest"] != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("hash sha256 错误: %v", out)
	}
	_, out = doJSON(t, h, "POST", "/v1/tools/hash", "", map[string]any{"text": "abc", "algo": "md5"})
	if out["digest"] != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatalf("hash md5 错误: %v", out)
	}
	_, out = doJSON(t, h, "POST", "/v1/tools/hash", "", map[string]any{"text": "x", "algo": "bogus"})
	if len(out["error"].(map[string]any)["message"].(string)) == 0 {
		t.Fatalf("hash 非法算法应报错: %v", out)
	}

	// subnet
	_, out = doJSON(t, h, "POST", "/v1/tools/subnet", "", map[string]any{"cidr": "192.168.1.0/24"})
	if out["network"] != "192.168.1.0" || out["broadcast"] != "192.168.1.255" ||
		out["mask"] != "255.255.255.0" || out["usable_hosts"] != float64(254) {
		t.Fatalf("subnet 错误: %v", out)
	}

	// password：长度与数量约束
	_, out = doJSON(t, h, "POST", "/v1/tools/password", "", map[string]any{"length": 16, "count": 5})
	pws := out["passwords"].([]any)
	if len(pws) != 5 || len(pws[0].(string)) != 16 {
		t.Fatalf("password 错误: %v", out)
	}

	// token-count：中文 0.6 字/token
	_, out = doJSON(t, h, "POST", "/v1/tools/token-count", "", map[string]any{"text": "你好世界"})
	if out["cjk_chars"] != float64(4) {
		t.Fatalf("token-count 错误: %v", out)
	}

	// text-stats
	_, out = doJSON(t, h, "POST", "/v1/tools/text-stats", "", map[string]any{"text": "hello 世界。"})
	if out["cjk_chars"] != float64(2) || out["chars"] != float64(9) {
		t.Fatalf("text-stats 错误: %v", out)
	}

	// json 校验
	_, out = doJSON(t, h, "POST", "/v1/tools/json", "", map[string]any{"text": `{"a":1}`})
	if out["valid"] != true || out["type"] != "object" {
		t.Fatalf("json 校验错误: %v", out)
	}
	_, out = doJSON(t, h, "POST", "/v1/tools/json", "", map[string]any{"text": `{bad`})
	if out["valid"] != false {
		t.Fatalf("json 非法输入应为 valid=false: %v", out)
	}

	// regex
	_, out = doJSON(t, h, "POST", "/v1/tools/regex", "", map[string]any{"pattern": `\d+`, "text": "a1b22c333"})
	if out["count"] != float64(3) {
		t.Fatalf("regex 错误: %v", out)
	}

	// color：#3b82f6
	_, out = doJSON(t, h, "POST", "/v1/tools/color", "", map[string]any{"input": "#3b82f6"})
	if out["hex"] != "#3b82f6" {
		t.Fatalf("color hex 错误: %v", out)
	}

	// url-code
	_, out = doJSON(t, h, "POST", "/v1/tools/url-code", "", map[string]any{"text": "a b&c", "mode": "encode"})
	if out["result"] != "a%20b%26c" {
		t.Fatalf("url-code encode 错误: %v", out)
	}
	_, out = doJSON(t, h, "POST", "/v1/tools/url-code", "", map[string]any{"text": "a%20b%26c", "mode": "decode"})
	if out["result"] != "a b&c" {
		t.Fatalf("url-code decode 错误: %v", out)
	}

	// base64 双向
	_, out = doJSON(t, h, "POST", "/v1/tools/base64", "", map[string]any{"text": "AQUA"})
	if out["encoded"] != "QVFVQQ==" {
		t.Fatalf("base64 encode 错误: %v", out)
	}
	_, out = doJSON(t, h, "POST", "/v1/tools/base64", "", map[string]any{"encoded": "QVFVQQ=="})
	if out["decoded"] != "AQUA" {
		t.Fatalf("base64 decode 错误: %v", out)
	}

	// dice / uuid / uuid-bulk / timestamp
	_, out = doJSON(t, h, "POST", "/v1/tools/dice", "", map[string]any{"sides": 6, "count": 3})
	if len(out["rolls"].([]any)) != 3 {
		t.Fatalf("dice 错误: %v", out)
	}
	_, out = doJSON(t, h, "GET", "/v1/tools/uuid", "", nil)
	if len(out["uuid"].(string)) != 36 {
		t.Fatalf("uuid 错误: %v", out)
	}
	_, out = doJSON(t, h, "POST", "/v1/tools/uuid-bulk", "", map[string]any{"count": 5})
	if len(out["uuids"].([]any)) != 5 {
		t.Fatalf("uuid-bulk 错误: %v", out)
	}
	_, out = doJSON(t, h, "POST", "/v1/tools/timestamp", "", map[string]any{"timestamp": float64(1700000000)})
	if out["datetime_utc8"] != "2023-11-15 06:13:20" {
		t.Fatalf("timestamp 转 datetime 错误: %v", out)
	}
	// datetime → timestamp：与 Rust NaiveDateTime.and_utc() 语义一致（按 UTC 解析）
	_, out = doJSON(t, h, "POST", "/v1/tools/timestamp", "", map[string]any{"datetime": "2023-11-15 06:13:20"})
	if out["timestamp"] != float64(1700028800) {
		t.Fatalf("datetime 转 timestamp 错误: %v", out)
	}
}

func TestShortenAndWebhookFlow(t *testing.T) {
	app, _, _ := newTestApp(t)
	h := app.Routes()

	// 短链：创建 → /s/{code} 302
	_, out := doJSON(t, h, "POST", "/v1/tools/shorten", "", map[string]any{"url": "https://example.com/a?q=1"})
	short := out["short"].(string)
	if !strings.HasPrefix(short, "https://aqua.zhuafs.com/s/") {
		t.Fatalf("shorten 错误: %v", out)
	}
	code := strings.TrimPrefix(short, "https://aqua.zhuafs.com/s/")
	rec, _ := doJSON(t, h, "GET", "/s/"+code, "", nil)
	if rec.Code != 302 || rec.Header().Get("Location") != "https://example.com/a?q=1" {
		t.Fatalf("短链 302 错误: %d %v", rec.Code, rec.Header().Get("Location"))
	}

	// webhook：create → 收集 → list → clear
	_, out = doJSON(t, h, "POST", "/v1/tools/webhook", "", map[string]any{"action": "create"})
	hid := out["id"].(string)
	if len(hid) != 10 {
		t.Fatalf("webhook create 错误: %v", out)
	}
	rec, _ = doJSON(t, h, "POST", "/hook/"+hid, "", map[string]any{"ping": 1})
	if rec.Code != 200 {
		t.Fatalf("hook collect 错误: %d", rec.Code)
	}
	_, out = doJSON(t, h, "POST", "/v1/tools/webhook", "", map[string]any{"action": "list", "id": hid})
	if out["total"] != float64(1) {
		t.Fatalf("webhook list 应有 1 条: %v", out)
	}
	_, out = doJSON(t, h, "POST", "/v1/tools/webhook", "", map[string]any{"action": "clear", "id": hid})
	if out["cleared"] != float64(1) {
		t.Fatalf("webhook clear 错误: %v", out)
	}
}

func TestPublicPagesAndGuards(t *testing.T) {
	app, _, _ := newTestApp(t)
	h := app.Routes()

	// prompts 工坊：15 条（含 2 官方树洞人格）
	_, out := doJSON(t, h, "GET", "/v1/tools/prompts", "", nil)
	if out["count"] != float64(15) {
		t.Fatalf("prompts 数量错误: %v", out["count"])
	}
	if len(out["prompts"].([]any)) != 15 {
		t.Fatalf("prompts 数组错误")
	}

	// 树洞模式元信息
	_, out = doJSON(t, h, "GET", "/v1/tools/treehole/prompt", "", nil)
	modes := out["modes"].([]any)
	if len(modes) != 2 {
		t.Fatalf("treehole/prompt 模式错误: %v", out)
	}

	// stats 空数据 200 且形状完整
	_, out = doJSON(t, h, "GET", "/v1/stats", "", nil)
	if _, ok := out["today"].(map[string]any); !ok {
		t.Fatalf("stats today 缺失: %v", out)
	}
	if _, ok := out["hourly"].([]any); !ok {
		t.Fatalf("stats hourly 缺失")
	}

	// arena leaderboard 空数据 200
	_, out = doJSON(t, h, "GET", "/v1/arena/leaderboard", "", nil)
	if out["total_votes"] != float64(0) || out["ranking"] == nil {
		t.Fatalf("arena leaderboard 形状错误: %v", out)
	}

	// 模型详情：auto / 收费 / 不存在
	rec, out := doJSON(t, h, "GET", "/v1/models/auto", "", nil)
	if out["auto"] != true {
		t.Fatalf("models/auto 错误: %v", out)
	}
	_, out = doJSON(t, h, "GET", "/v1/models/t/m1", "", nil)
	if out["paid"] != true {
		t.Fatalf("models/t/m1 应为收费模型: %v", out)
	}
	rec, out = doJSON(t, h, "GET", "/v1/models/nonexistent-model", "", nil)
	if rec.Code != 404 {
		t.Fatalf("models/不存在 应 404: %d %v", rec.Code, out)
	}

	// ip_location：内网 IP 拒绝
	rec, _ = doJSON(t, h, "POST", "/v1/ip_location", "", map[string]any{"ip": "192.168.1.1"})
	if rec.Code != 400 {
		t.Fatalf("ip_location 内网 IP 应 400: %d", rec.Code)
	}

	// tools/chat 未登录 401
	rec, _ = doJSON(t, h, "POST", "/v1/tools/chat", "", map[string]any{
		"model": "deepseek-chat", "messages": []map[string]any{{"role": "user", "content": "hi"}}})
	if rec.Code != 401 {
		t.Fatalf("tools/chat 未登录应 401: %d", rec.Code)
	}
}
