// Codex 账号池运维：代理健康探测 + 自动换线 + 账号池看板（/v1/admin/codex）。
//
// 探测口径（应用层，第二道保活）：每 5 分钟经线代理请求
// GET https://chatgpt.com/backend-api/codex/responses（UA 伪装 codex_cli_rs）——
//   401 = 健康（请求已穿透地域风控到达 OpenAI 应用层，未带票被拒属预期）；
//   403 + body 含 unsupported_country = 出口 IP 被 OpenAI 地域拦截 → 换线；
//   超时/连接错 = 节点故障 → 换线。
// urltest（sing-box 内建）是第一道：节点彻底挂掉自动按延迟切；本探测专治
// 「节点活着但出口被风控」——urltest 对此无感。
// 换线动作：sing-box clash_api（127.0.0.1:9090）PUT /proxies/proxy 切 selector；
// 按节点表依次尝试 + 即时复探，成功即停；全部异常保持原节点标红待下轮。
// 恢复后不自动回切 auto-us（避免抖动），管理端可一键切回。
package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	codexProbeURL    = "https://chatgpt.com/backend-api/codex/responses"
	codexProbeUA     = "codex_cli_rs/0.55.0"
	codexQuotaTokens = int64(8000000) // 号商口径 luna 单号 8~10M，按 8M 保守记账（剩余=额度-已用）
)

func codexProbeInterval() time.Duration {
	if s := os.Getenv("AQUA_CODEX_PROBE_INTERVAL"); s != "" {
		if sec, err := strconv.Atoi(s); err == nil && sec >= 30 {
			return time.Duration(sec) * time.Second
		}
	}
	return 5 * time.Minute
}

func clashAPIBase() string {
	if p := os.Getenv("AQUA_CLASH_API"); p != "" {
		return p
	}
	return "http://127.0.0.1:9090"
}

// codexNodeHealth 单节点最近探测结果
type codexNodeHealth struct {
	Healthy bool   `json:"healthy"`
	LastTs  int64  `json:"last_ts"`
	Ms      int64  `json:"ms"`
	LastErr string `json:"last_err"`
}

// codexProxyHealth 代理健康状态（内存态，重启归零后首轮探测重建）
type codexProxyHealth struct {
	mu          sync.Mutex
	enabled     bool
	lastProbeTs int64
	lastOK      bool
	lastErr     string
	selectorNow string                        // selector 当前指向（auto-us 或具体节点）
	urltestNow  string                        // urltest 实际选中的出口节点
	nodes       map[string]*codexNodeHealth   // 探测过的节点健康度
	switches    []string                      // 最近 10 条切换/探测处置记录
	failStreak  int                           // 连续探测失败轮数
}

// codexProxy 当前线的出站代理地址（无 codex 线或线未配代理返回 ""）
func (a *App) codexProxyAddr() string {
	for _, l := range a.linesSnap() {
		if l.AuthStyle == "codex" {
			return l.Proxy
		}
	}
	return ""
}

// clashReq clash API 调用（本机 sing-box）
func clashReq(method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, clashAPIBase()+path, rd)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return buf, fmt.Errorf("clash api %s %s -> %d", method, path, resp.StatusCode)
	}
	return buf, nil
}

// clashSelectorNow 读 selector 当前指向与可选列表
func clashSelectorNow() (now string, all []string, err error) {
	b, err := clashReq("GET", "/proxies/proxy", nil)
	if err != nil {
		return "", nil, err
	}
	var out struct {
		Now  string   `json:"now"`
		All  []string `json:"all"`
		Type string   `json:"type"`
	}
	if json.Unmarshal(b, &out) != nil {
		return "", nil, fmt.Errorf("clash api 响应解析失败")
	}
	if out.Type != "Selector" {
		return "", nil, fmt.Errorf("proxy 不是 Selector（clash_api 未启用或配置变更）")
	}
	return out.Now, out.All, nil
}

// clashSwitch 切换 selector 到指定节点
func clashSwitch(name string) error {
	_, err := clashReq("PUT", "/proxies/proxy", map[string]string{"name": name})
	return err
}

// codexProbeNode 探测指定出口是否可用（经线代理直探 OpenAI 应用层）
// 返回 nil=健康；错误=故障（地域风控/节点故障）
func codexProbeNode(proxyAddr string) (int64, error) {
	hc := &http.Client{Timeout: 20 * time.Second}
	if proxyAddr != "" {
		pu, err := url.Parse(proxyAddr)
		if err != nil {
			return 0, fmt.Errorf("代理地址解析失败: %w", err)
		}
		hc.Transport = &http.Transport{Proxy: http.ProxyURL(pu)}
	}
	start := time.Now()
	req, err := http.NewRequest("GET", codexProbeURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", codexProbeUA)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("连接失败: %v", err)
	}
	defer resp.Body.Close()
	body := make([]byte, 0, 4096)
	tmp := make([]byte, 2048)
	for {
		n, rerr := resp.Body.Read(tmp)
		body = append(body, tmp[:n]...)
		if rerr != nil || len(body) > 16384 {
			break
		}
	}
	ms := time.Since(start).Milliseconds()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 405:
		// 穿透风控到达应用层：健康（401=未带票被拒属预期；405=GET 不被该端点接受同样证明已到路由层）
		return ms, nil
	case resp.StatusCode == 403 && bytes.Contains(body, []byte("unsupported_country")):
		return ms, fmt.Errorf("出口 IP 被 OpenAI 地域拦截")
	default:
		return ms, fmt.Errorf("探测异常响应 %d", resp.StatusCode)
	}
}

// startCodexProbe 启动代理保活探测（周期 + 启动 15s 后首跑）
func (a *App) startCodexProbe() {
	a.codexProxy = &codexProxyHealth{nodes: map[string]*codexNodeHealth{}}
	go func() {
		time.Sleep(15 * time.Second)
		for {
			a.codexProbeOnce()
			time.Sleep(codexProbeInterval())
		}
	}()
}

// codexProbeOnce 单轮探测 + 异常自动换线
func (a *App) codexProbeOnce() {
	px := a.codexProxyAddr()
	h := a.codexProxy
	h.mu.Lock()
	h.enabled = px != ""
	h.mu.Unlock()
	if px == "" {
		return
	}

	ms, err := codexProbeNode(px)
	a.codexRefreshNow() // 先刷新当前指向，确保探测结果记录到正确节点
	h.mu.Lock()
	h.lastProbeTs = time.Now().Unix()
	h.lastOK = err == nil
	h.lastErr = ""
	if err != nil {
		h.lastErr = err.Error()
		h.failStreak++
	} else {
		h.failStreak = 0
	}
	h.mu.Unlock()
	a.codexRecordNode("", ms, err) // 当前生效节点（selectorNow/urltestNow 已刷新）

	if err == nil {
		return
	}
	log.Printf("[codex-ops] 代理探测异常: %v，尝试自动换线", err)

	// 自动换线：selector 可选列表里的具体节点逐个试（跳过当前 urltest 实际节点）
	now, all, serr := clashSelectorNow()
	if serr != nil {
		log.Printf("[codex-ops] clash API 不可达，无法自动换线: %v", serr)
		return
	}
	cur := h.currentNode()
	for _, name := range all {
		if name == now || name == "auto-us" || name == cur {
			continue
		}
		if swerr := clashSwitch(name); swerr != nil {
			continue
		}
		time.Sleep(2 * time.Second) // 出站链路切换生效
		ms2, perr := codexProbeNode(px)
		a.codexRecordNode(name, ms2, perr)
		h.mu.Lock()
		if perr == nil {
			h.switches = append(h.switches, fmt.Sprintf("%s %s→%s（%s 后恢复）",
				time.Now().Format("15:04:05"), cur, name, h.lastErr))
			h.switches = h.switches[max0(len(h.switches)-10):]
			h.lastOK = true
			h.lastErr = ""
		}
		h.mu.Unlock()
		if perr == nil {
			log.Printf("[codex-ops] 已切换出口 %s→%s，探测恢复", cur, name)
			return
		}
	}
	log.Printf("[codex-ops] 全部候选节点探测异常，保持当前出口待下轮重试")
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// codexRefreshNow 刷新 selector/urltest 当前指向
func (a *App) codexRefreshNow() {
	h := a.codexProxy
	now, _, err := clashSelectorNow()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.selectorNow = now
	}
	// urltest 实际出口
	if b, err2 := clashReq("GET", "/proxies/auto-us", nil); err2 == nil {
		var out struct {
			Now string `json:"now"`
		}
		if json.Unmarshal(b, &out) == nil {
			h.urltestNow = out.Now
		}
	}
}

// currentNode 当前实际生效节点（selector 手动指定节点 or urltest 选中节点）
func (h *codexProxyHealth) currentNode() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.selectorNow != "" && h.selectorNow != "auto-us" {
		return h.selectorNow
	}
	return h.urltestNow
}

// codexRecordNode 记录节点探测结果（name 空=当前生效节点）
func (a *App) codexRecordNode(name string, ms int64, err error) {
	h := a.codexProxy
	if name == "" {
		name = h.currentNode()
	}
	if name == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	nh := h.nodes[name]
	if nh == nil {
		nh = &codexNodeHealth{}
		h.nodes[name] = nh
	}
	nh.Healthy = err == nil
	nh.LastTs = time.Now().Unix()
	nh.Ms = ms
	nh.LastErr = ""
	if err != nil {
		nh.LastErr = err.Error()
	}
}

// —— GET /v1/admin/codex —— 账号池看板：账号粒度用量/存活/利润 + 代理健康度
func (a *App) handleAdminCodex(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	// 账号粒度聚合（requests.key_idx 由网关按实际用钥回写；-1=失败请求/无钥请求不计入任何账号）
	type acc struct {
		Idx    int64
		Calls  int64
		Ok     int64
		LastTs int64
		OkTs   int64
		Prompt int64
		Compl  int64
		Cached int64
		Total  int64
		Income int64
		Face   int64
	}
	agg := map[int64]*acc{}
	order := []int64{}
	rows, err := a.DB.Query(`
		SELECT COALESCE(key_idx,-1), COUNT(*), COALESCE(SUM(ok),0), MAX(ts),
		       COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0),
		       COALESCE(SUM(cached_tokens),0), COALESCE(SUM(total_tokens),0),
		       COALESCE(SUM(CASE WHEN ok=1 THEN bill_amount_micro ELSE 0 END),0),
		       COALESCE(SUM(face_cost_micro),0),
		       COALESCE(MAX(CASE WHEN ok=1 THEN ts ELSE 0 END),0)
		FROM requests WHERE resolved_line='codex' GROUP BY COALESCE(key_idx,-1)`)
	if err == nil {
		for rows.Next() {
			var i int64
			x := &acc{}
			if rows.Scan(&i, &x.Calls, &x.Ok, &x.LastTs, &x.Prompt, &x.Compl, &x.Cached, &x.Total, &x.Income, &x.Face, &x.OkTs) == nil {
				agg[i] = x
				order = append(order, i)
			}
		}
		rows.Close()
	}

	// 账号清单（admin_line_keys 为事实源：note=邮箱标识，dead=判死）
	type acctOut struct {
		Idx          int64  `json:"idx"`
		Note         string `json:"note"`
		Dead         bool   `json:"dead"`
		Calls        int64  `json:"calls"`
		OkCalls      int64  `json:"ok_calls"`
		PromptTokens int64  `json:"prompt_tokens"`
		CompletionT  int64  `json:"completion_tokens"`
		CachedTokens int64  `json:"cached_tokens"`
		TotalTokens  int64  `json:"total_tokens"`
		QuotaTokens  int64  `json:"quota_tokens"`
		RemainRatio  float64 `json:"remain_ratio"`
		IncomeMicro  int64  `json:"income_micro"`
		CostMicro    int64  `json:"cost_micro"`
		ProfitMicro  int64  `json:"profit_micro"`
		LastOKTs     int64  `json:"last_ok_ts"`
		LastTs       int64  `json:"last_ts"`
	}
	accounts := []acctOut{}
	krows, kerr := a.DB.Query(`SELECT idx, COALESCE(note,''), dead FROM admin_line_keys WHERE line_id='codex' ORDER BY idx`)
	if kerr == nil {
		for krows.Next() {
			var idx int64
			var note string
			var dead int64
			if krows.Scan(&idx, &note, &dead) != nil {
				continue
			}
			x := agg[idx]
			o := acctOut{Idx: idx, Note: note, Dead: dead != 0, QuotaTokens: codexQuotaTokens}
			if x != nil {
				o.Calls, o.OkCalls, o.LastTs, o.LastOKTs = x.Calls, x.Ok, x.LastTs, x.OkTs
				o.PromptTokens, o.CompletionT, o.CachedTokens, o.TotalTokens = x.Prompt, x.Compl, x.Cached, x.Total
				o.IncomeMicro, o.CostMicro = x.Income, x.Face
				o.ProfitMicro = x.Income - x.Face
			}
			if o.TotalTokens < o.QuotaTokens {
				o.RemainRatio = float64(o.QuotaTokens-o.TotalTokens) / float64(o.QuotaTokens)
			}
			accounts = append(accounts, o)
		}
		krows.Close()
	}

	// 代理健康度
	h := a.codexProxy
	a.codexRefreshNow()
	h.mu.Lock()
	proxy := map[string]any{
		"enabled": h.enabled, "last_probe_ts": h.lastProbeTs, "probe_ok": h.lastOK,
		"last_err": h.lastErr, "selector_now": h.selectorNow, "urltest_now": h.urltestNow,
		"fail_streak": h.failStreak, "switches": h.switches,
		"nodes": h.nodes, "interval_sec": int(codexProbeInterval().Seconds()),
	}
	h.mu.Unlock()

	adminJSON(w, map[string]any{"accounts": accounts, "proxy": proxy, "quota_tokens": codexQuotaTokens})
}

// —— POST /v1/admin/codex/proxy/switch —— 手动换线：{"node":"us-f"}（node="auto" 切回自动测优）
func (a *App) handleAdminCodexSwitch(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var req struct {
		Node string `json:"node"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Node == "" {
		errOut(w, 400, "bad_request", "缺少 node")
		return
	}
	// 合法节点校验：clash API selector 的真实可选列表（防注入 clash API）
	_, all, err := clashSelectorNow()
	if err != nil {
		errOut(w, 502, "clash_unreachable", "clash API 不可达（sing-box 未启用 clash_api）")
		return
	}
	valid := false
	for _, n := range all {
		if n == req.Node {
			valid = true
			break
		}
	}
	if !valid {
		errOut(w, 400, "bad_node", "未知节点: "+req.Node)
		return
	}
	if err := clashSwitch(req.Node); err != nil {
		errOut(w, 502, "switch_failed", "切换失败: "+err.Error())
		return
	}
	a.codexProxy.mu.Lock()
	a.codexProxy.switches = append(a.codexProxy.switches, fmt.Sprintf("%s 手动切换→%s", time.Now().Format("15:04:05"), req.Node))
	a.codexProxy.switches = a.codexProxy.switches[max0(len(a.codexProxy.switches)-10):]
	a.codexProxy.mu.Unlock()
	// 立即复探给出结论
	px := a.codexProxyAddr()
	ms, perr := codexProbeNode(px)
	a.codexRecordNode(req.Node, ms, perr)
	if perr != nil {
		errOut(w, 502, "probe_failed", "已切换但探测未恢复: "+perr.Error())
		return
	}
	adminJSON(w, map[string]any{"ok": true, "node": req.Node, "probe_ms": ms})
}
