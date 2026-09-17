// Package upstream 上游抽象：Line（任意多条，配置驱动）+ 密钥池 + 转发客户端。
// 密钥策略：单钥用尽制——优先当前密钥用到面值耗尽/连续失败，再切换下一把；
// 错误重试：同钥重试 2 次仍失败才换钥，最多尝试 3 把；4xx 参数类错误不重试直接透传转译。
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"acu-aqua/gateway/internal/config"
)

// KeyState 密钥池中一把密钥的运行态
type KeyState struct {
	Idx          int
	Key          string
	InitialMicro int64
	UsedMicro    int64
	Dead         bool
	LastFailTs   int64
	Fails        int
	// AuthFails 连续鉴权失败（401/403）计数：达线阈值才判死（AuthFailsToKill，默认 3）。
	// 背景阶段 0 实测：上游 IP 级风控会把"原本有效的 key"临时 401（Forbidden code=16），
	// 立即判死会误杀全池；超过 1 小时的失败重新计数（风控通常临时）
	AuthFails  int
	AuthFailTs int64
	// CoolUntil 自适应冷却截止（unix 秒）：ReportFail 指数退避（300s 起 cap 30min）、
	// 429 insufficient_quota 长 30min 冷却、鉴权疑似风控长 15min 冷却共用
	CoolUntil int64
	// Recent 最近 60s 请求时间戳环（每钥 RPM 限速用）
	Recent []int64
}

// KeyPool 密钥池（单钥用尽制）：粘住当前钥直到死钥/冷却，再顺延到下一把。
type KeyPool struct {
	mu       sync.Mutex
	keys     []*KeyState
	cur      int
	CoolSecs int64 // 失败冷却秒数
	RPM      int   // 每钥每分钟请求上限（0=不限）
	AuthKill int   // 连续鉴权失败判死阈值（0=3）
}

func NewKeyPool(keys []string, faceMicro int64, coolSecs int64) *KeyPool {
	p := &KeyPool{CoolSecs: coolSecs}
	for i, k := range keys {
		if strings.TrimSpace(k) == "" {
			continue
		}
		p.keys = append(p.keys, &KeyState{Idx: i, Key: k, InitialMicro: faceMicro})
	}
	return p
}

// Len 池中密钥总数
func (p *KeyPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.keys)
}

// usable 当前时刻该钥是否可用（未死、不在冷却窗口、未超每钥 RPM）
func (p *KeyPool) usable(k *KeyState, now int64) bool {
	if k.Dead {
		return false
	}
	if k.CoolUntil > now {
		return false
	}
	if p.CoolSecs > 0 && k.Fails > 0 && now-k.LastFailTs < p.CoolSecs {
		return false
	}
	if p.RPM > 0 {
		cnt := 0
		for _, t := range k.Recent {
			if now-t < 60 {
				cnt++
			}
		}
		if cnt >= p.RPM {
			return false
		}
	}
	return true
}

// Acquire 取当前可用密钥（粘性：当前钥可用就一直用它，不轮询）
func (p *KeyPool) Acquire() (*KeyState, error) {
	return p.acquire(nil)
}

// AcquireSkip 同 Acquire，但跳过 skip 集合中的密钥（换钥遍历用）
func (p *KeyPool) AcquireSkip(skip map[int]bool) (*KeyState, error) {
	return p.acquire(skip)
}

func (p *KeyPool) acquire(skip map[int]bool) (*KeyState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.keys)
	if n == 0 {
		return nil, fmt.Errorf("KEY_POOL_EMPTY")
	}
	now := time.Now().Unix()
	for i := 0; i < n; i++ {
		k := p.keys[(p.cur+i)%n]
		if skip != nil && skip[k.Idx] {
			continue
		}
		if p.usable(k, now) {
			p.cur = (p.cur + i) % n
			return k, nil
		}
	}
	// 全池冷却（限流风暴/瞬态失败）：退化为取最久未失败的非死钥——
	// 宁可重试可能限流的钥，也不能让整线 502 瘫痪。
	var fallback *KeyState
	for i := 0; i < n; i++ {
		k := p.keys[(p.cur+i)%n]
		if k.Dead || (skip != nil && skip[k.Idx]) {
			continue
		}
		if fallback == nil || k.LastFailTs < fallback.LastFailTs {
			fallback = k
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("KEY_POOL_EXHAUSTED")
}

// Advance 切换到下一把密钥（当前钥连续失败重试无效后调用）
func (p *KeyPool) Advance() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.keys) == 0 {
		return
	}
	p.cur = (p.cur + 1) % len(p.keys)
}

// AcquireFor 取指定池序的密钥（模型专属钥模式）：忽略粘性与冷却，dead 钥直接报错。
// 池序 = 非 dead 密钥按 idx 排序后的位置（0 起），与 linesFromDB 装载顺序一致。
func (p *KeyPool) AcquireFor(poolIdx int) (*KeyState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if poolIdx < 0 || poolIdx >= len(p.keys) {
		return nil, fmt.Errorf("KEY_IDX_OUT_OF_RANGE(%d/%d)", poolIdx, len(p.keys))
	}
	k := p.keys[poolIdx]
	if k.Dead {
		return nil, fmt.Errorf("KEY_IDX_DEAD(%d)", poolIdx)
	}
	return k, nil
}

// ReportFace 累计面值消耗；超初始面值标记死钥（额度耗得差不多自动摘除）
func (p *KeyPool) ReportFace(k *KeyState, faceMicro int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k.UsedMicro += faceMicro
	if k.InitialMicro > 0 && k.UsedMicro >= k.InitialMicro {
		k.Dead = true
	}
}

// ReportDead 立即标记死钥（上游判定密钥无效/欠费时）
func (p *KeyPool) ReportDead(k *KeyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k.Dead = true
}

// ReportFail 记录失败（自适应指数冷却：300s 起 ×2 递增，cap 30 分钟）
func (p *KeyPool) ReportFail(k *KeyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k.Fails++
	k.LastFailTs = time.Now().Unix()
	cool := int64(300)
	for i := 1; i < k.Fails && cool < 1800; i++ {
		cool *= 2
	}
	if cool > 1800 {
		cool = 1800
	}
	if until := k.LastFailTs + cool; until > k.CoolUntil {
		k.CoolUntil = until
	}
}

// ReportAuthFail 记录鉴权失败（401/403）：疑似风控走长冷却不判死；
// 连续失败达 AuthKill 阈值（>1h 重置计数）才返回 true 由调用方判死。
func (p *KeyPool) ReportAuthFail(k *KeyState) (kill bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().Unix()
	if k.AuthFailTs > 0 && now-k.AuthFailTs > 3600 {
		k.AuthFails = 0 // 距上次鉴权失败超 1h：风控多为临时，重新计数
	}
	k.AuthFails++
	k.AuthFailTs = now
	killAt := p.AuthKill
	if killAt <= 0 {
		killAt = 3
	}
	// 长冷却 15 分钟（远长于普通失败，避免风控期反复打上游延长封禁）
	k.CoolUntil = now + 900
	k.LastFailTs = now
	return k.AuthFails >= killAt
}

// ReportQuotaDead 配额耗尽（429 insufficient_quota）：长冷却 30 分钟等积分窗口刷新，
// 不判死（号还在、积分会回来）
func (p *KeyPool) ReportQuotaDead(k *KeyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if until := time.Now().Unix() + 1800; until > k.CoolUntil {
		k.CoolUntil = until
	}
	k.LastFailTs = time.Now().Unix()
}

// markUse 记录一次请求（每钥 RPM 滑窗）
func (p *KeyPool) markUse(k *KeyState) {
	if p.RPM <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().Unix()
	k.Recent = append(k.Recent, now)
	// 裁剪 60s 窗口外条目（从头裁即可，时间有序）
	cut := 0
	for cut < len(k.Recent) && now-k.Recent[cut] >= 60 {
		cut++
	}
	k.Recent = k.Recent[cut:]
}

// ReportSuccess 成功清零失败计数
func (p *KeyPool) ReportSuccess(k *KeyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k.Fails = 0
	k.CoolUntil = 0
}

// Stats 密钥池快照（管理后台台账）
func (p *KeyPool) Stats() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []map[string]any
	for _, k := range p.keys {
		out = append(out, map[string]any{
			"idx": k.Idx, "initial_micro": k.InitialMicro, "used_micro": k.UsedMicro,
			"remain_micro": k.InitialMicro - k.UsedMicro, "dead": k.Dead,
		})
	}
	return out
}

// Client 上游 HTTP 客户端封装（OpenAI 兼容）
type Client struct {
	Line *config.Line
	Pool *KeyPool
	HTTP *http.Client
}

// NewClient 构造上游客户端
// 连接/TLS 快速失败：上游 CDN 拦截（TLS 握手挂死）场景 10 秒内报错进入重试/冷却，
// 而非拖满整段请求超时
func NewClient(l *config.Line) *Client {
	if l == nil {
		l = &config.Line{} // 防御：热重载竞态下线被删时调用方兜底，绝不 panic
	}
	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		// 上游已建连但迟迟不回响应头（黑洞/排队挂死）：120s 判死进重试/冷却。
		// 与 Client 整体超时解耦——流式长响应的 body 读取不受此限制
		ResponseHeaderTimeout: 120 * time.Second,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
	}
	// 线路级出站代理：gpt 线经本机 sing-box 出美国口（OpenAI 地域风控），其余线直连
	if l.Proxy != "" {
		if pu, err := url.Parse(l.Proxy); err == nil && pu.Scheme != "" {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	cl := &Client{
		Line: l,
		Pool: NewKeyPool(l.Keys, l.KeyFaceMicro, 300),
		HTTP: &http.Client{
			// 不设整体 Timeout：Client.Timeout 含响应体读取，长文流式生成会在
			// 300s 被硬切断（上游侧正常完成计费，本站表现为 stream_incomplete）。
			// 总时长由各请求 ctx（非流式 300s / 流式 600s）+ 上述分阶段超时兜底
			Transport: tr,
		},
	}
	// 线级调度参数注入：每钥 RPM 限速与鉴权判死阈值（商汤积分线用，其他线零值=关闭）
	cl.Pool.RPM = l.KeyRPM
	cl.Pool.AuthKill = l.AuthFailsToKill
	return cl
}

// 每钥重试次数与最多尝试密钥数
const (
	perKeyRetries = 3 // 同一把钥连续失败重试 2 次（共 3 次尝试）后换钥
	maxKeyHops    = 5 // 最多换 5 把钥（模型级错误换钥不计失败，上限放宽）
)

// IsModelUnavailable 上游错误体是否表明"模型不可用"（已下线/无可用通道）。
// 此类错误换钥有意义（不同钥可能属不同分组），但同钥重试无意义。
func IsModelUnavailable(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "no available channel") ||
		strings.Contains(s, "model_not_found") ||
		strings.Contains(s, "model not found") ||
		strings.Contains(s, "model_disabled") ||
		strings.Contains(s, "模型已关闭") ||
		strings.Contains(s, "does not exist")
}

// IsChannelExhausted 上游**渠道级**不可用（如 OneAPI 网关报 no available channel）：
// 同一上游的所有密钥共享同一渠道池，换钥/重试都注定失败 —— 必须快速失败，
// 避免渠道抖动时用户等十几秒才收到错误。
func IsChannelExhausted(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "no available channel")
}

// rebuildResp 用已读入的 body 重建可重复读的响应（错误体都很小，整读）
func rebuildResp(resp *http.Response, body []byte) *http.Response {
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp
}

// Do 向上游发起请求（含错误重试）：
//   - 200：直接返回
//   - 429 / 5xx / 网络错误：同钥退避重试 perKeyRetries 次，仍失败换下一把钥
//   - 模型级错误（无可用通道/模型下线）：不重试同钥，立即换钥（不同钥分组可能不同）
//   - 401 / 403：该钥判定无效，标记死钥并立即换钥
//   - 其余 4xx：请求本身问题，不重试，返回给上层转译
func (c *Client) Do(ctx context.Context, body []byte, stream bool, path string) (*http.Response, *KeyState, error) {
	var lastErr error
	var modelResp *http.Response // 模型级错误的最后一帧（全钥耗尽时返回给上层转译）
	// 可中断等待（请求取消/超时时立即返回）
	backoff := func(d time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
	}

	tried := map[int]bool{}
	acquireTries := 0
	channelDown := false // 渠道级不可用：换钥无意义，立即终止
	quotaHits, nonQuotaFails := 0, 0 // 429 分型计数：全池纯配额耗尽 → 上层转译为业务态
	for hop := 0; hop < maxKeyHops; hop++ {
		k, err := c.Pool.AcquireSkip(tried)
		if err != nil {
			// 密钥全在冷却：稍等再取（限流场景几秒即恢复）
			if acquireTries < 2 {
				acquireTries++
				hop--
				backoff(2 * time.Second)
				continue
			}
			break
		}
		acquireTries = 0
		tried[k.Idx] = true
		modelLevel := false
		for try := 0; try < perKeyRetries; try++ {
			if try > 0 {
				backoff(time.Duration(try) * 500 * time.Millisecond) // 退避：500ms / 1s
			}
			resp, err := c.send(ctx, k, body, stream, path)
			if err != nil {
				lastErr = err
				if ctx.Err() != nil {
					// 客户端断开/请求超时：失败与密钥无关，不记失败不冷却，
					// 立即终止（重试与换钥都已无意义——ctx 已死，上游必失败）。
					// 否则一次客户端取消会把全池密钥拖入冷却，整线瘫痪 5 分钟。
					return nil, nil, err
				}
				nonQuotaFails++
				c.Pool.ReportFail(k)
				log.Printf("[upstream] line=%s key=%d 网络错误(第%d次): %v", c.Line.ID, k.Idx, try+1, err)
				continue // 同钥重试
			}
			sc := resp.StatusCode
			switch {
			case sc == 200:
				c.Pool.ReportSuccess(k)
				return resp, k, nil
			case sc == 401 || sc == 403:
				// 鉴权失败分型（阶段 0 实测：上游 IP 级风控会把有效 key 临时 401，
				// 立即判死会误杀全池）——达阈值才判死，未达阈值长冷却 15 分钟。
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
				_ = resp.Body.Close()
				lastErr = fmt.Errorf("UPSTREAM_STATUS_%d", sc)
				nonQuotaFails++
				if c.Pool.ReportAuthFail(k) {
					c.Pool.ReportDead(k)
					log.Printf("[upstream] line=%s key=%d 状态%d 鉴权连续失败达阈值，判死换钥", c.Line.ID, k.Idx, sc)
				} else {
					log.Printf("[upstream] line=%s key=%d 状态%d 疑似上游风控，长冷却15min未判死", c.Line.ID, k.Idx, sc)
				}
			case sc == 402:
				// 上游面值耗尽：该钥永远不会再成功，立即判死换钥
				lastErr = fmt.Errorf("UPSTREAM_STATUS_402")
				nonQuotaFails++
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
				_ = resp.Body.Close()
				c.Pool.ReportDead(k)
				log.Printf("[upstream] line=%s key=%d 状态402（面值耗尽），判死换钥", c.Line.ID, k.Idx)
			case sc == 429 || sc >= 500:
				// 读错误体（小），分型处理
				eb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
				_ = resp.Body.Close()
				if sc == 429 && strings.Contains(strings.ToLower(string(eb)), "insufficient_quota") {
					// 配额耗尽（5h 积分窗口/周额度打完）：号还在、积分会回来，
					// 长冷却 30 分钟自动复活，绝不判死；换钥继续
					lastErr = fmt.Errorf("UPSTREAM_QUOTA_EXHAUSTED")
					quotaHits++
					c.Pool.ReportQuotaDead(k)
					log.Printf("[upstream] line=%s key=%d 429 配额耗尽，长冷却30min换钥", c.Line.ID, k.Idx)
					break
				}
				lastErr = fmt.Errorf("UPSTREAM_STATUS_%d", sc)
				if IsChannelExhausted(eb) {
					// 渠道级不可用：全钥共享同一渠道池，重试/换钥注定失败 → 立即返回
					modelLevel = true
					channelDown = true
					modelResp = rebuildResp(resp, eb)
					log.Printf("[upstream] line=%s key=%d 状态%d 渠道级不可用，快速失败", c.Line.ID, k.Idx, sc)
				} else if IsModelUnavailable(eb) {
					// 模型在该钥的分组不可用：换钥有意义，同钥重试无意义
					modelLevel = true
					modelResp = rebuildResp(resp, eb)
					log.Printf("[upstream] line=%s key=%d 状态%d 模型级错误，直接换钥", c.Line.ID, k.Idx, sc)
				} else {
					nonQuotaFails++
					c.Pool.ReportFail(k)
					log.Printf("[upstream] line=%s key=%d 状态%d(第%d次) body=%s，同钥重试", c.Line.ID, k.Idx, sc, try+1, eb)
					continue // 同钥退避重试
				}
			default:
				// 400/404/422 等：请求本身问题，不重试
				c.Pool.ReportSuccess(k)
				eb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
				_ = resp.Body.Close()
				return rebuildResp(resp, eb), k, nil
			}
			break
		}
		if modelLevel {
			if channelDown {
				break // 渠道级不可用：终止全部重试
			}
			// 立即换下一把钥（该钥本身没问题，不计失败不冷却）
			c.Pool.Advance()
			backoff(300 * time.Millisecond)
			continue
		}
		if channelDown {
			break
		}
		// 当前钥重试无效，换下一把
		c.Pool.Advance()
		backoff(time.Second)
	}
	if modelResp != nil {
		return modelResp, nil, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("KEY_POOL_EXHAUSTED")
	}
	// 全池纯配额耗尽（无其他类型失败）：上层转译为"本时段额度用完"业务态而非故障
	if quotaHits > 0 && nonQuotaFails == 0 {
		lastErr = fmt.Errorf("UPSTREAM_QUOTA_EXHAUSTED")
	}
	return nil, nil, lastErr
}

// DoKey 带模型专属钥发起请求：keyIdx<0 走通用 Do（粘性换钥池）；
// ≥0 锁定池序钥直发一次（不粘性、不换钥——专属钥仅服务对应模型，
// 避免与其他模型的钥在同一池内互判死；错误码原样透传给上层转译）。
// 上游 402（面值耗尽）仍判死该钥：专属钥耗尽后 AcquireFor 直接报 KEY_IDX_DEAD，
// 上层转译为明确的"专属通道余额耗尽"，不再反复打上游。
func (c *Client) DoKey(ctx context.Context, body []byte, stream bool, path string, keyIdx int64) (*http.Response, *KeyState, error) {
	if keyIdx < 0 {
		return c.Do(ctx, body, stream, path)
	}
	k, err := c.Pool.AcquireFor(int(keyIdx))
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.send(ctx, k, body, stream, path)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == 402 {
		c.Pool.ReportDead(k)
		log.Printf("[upstream] line=%s key=%d 专属钥状态402 面值耗尽，判死", c.Line.ID, k.Idx)
	}
	return resp, k, nil
}

// send 单次上游请求：注入密钥认证（bearer / x-api-key / codex OAuth）。
// StrictClean 线先清洗请求体（剥上游白名单外字段）；成功获得 HTTP 响应（不论状态码）
// 都记入每钥 RPM 滑窗——网络层失败（未达上游）不计。
func (c *Client) send(ctx context.Context, k *KeyState, body []byte, stream bool, path string) (*http.Response, error) {
	if c.Line.AuthStyle == "codex" {
		// Codex（ChatGPT 账号）线：RT→AT 换票 + chat/completions↔Responses 协议转换（见 codex.go）
		return c.codexSend(ctx, k, body, stream, path)
	}
	if c.Line.StrictClean {
		body = cleanStrictBody(body)
	}
	url := strings.TrimRight(c.Line.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	switch c.Line.AuthStyle {
	case "x-api-key":
		req.Header.Set("x-api-key", k.Key)
	default:
		req.Header.Set("Authorization", "Bearer "+k.Key)
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := c.HTTP.Do(req)
	if err == nil {
		c.Pool.markUse(k)
	}
	return resp, err
}

// cleanStrictBody 请求清洗（StrictClean 线）：递归剥离 tools[].function.strict 等
// 上游请求体白名单外的非标准字段（阶段 0 实测某些积分制网关对未列出字段整单拒绝）。
// 清洗失败（非合法 JSON）时原样返回——绝不让清洗本身弄坏请求。
func cleanStrictBody(body []byte) []byte {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return body
	}
	stripStrict(v)
	nb, err := json.Marshal(v)
	if err != nil {
		return body
	}
	return nb
}

func stripStrict(v any) {
	switch t := v.(type) {
	case map[string]any:
		delete(t, "strict")
		for _, vv := range t {
			stripStrict(vv)
		}
	case []any:
		for _, vv := range t {
			stripStrict(vv)
		}
	}
}

// ReadAll 便捷读响应（上限 64MB）
func ReadAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, 64<<20))
}
