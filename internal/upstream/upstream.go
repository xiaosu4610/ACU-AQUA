// Package upstream 上游抽象：Line（任意多条，配置驱动）+ 密钥池 + 转发客户端。
// 密钥策略：单钥用尽制——优先当前密钥用到面值耗尽/连续失败，再切换下一把；
// 错误重试：同钥重试 2 次仍失败才换钥，最多尝试 3 把；4xx 参数类错误不重试直接透传转译。
package upstream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
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
}

// KeyPool 密钥池（单钥用尽制）：粘住当前钥直到死钥/冷却，再顺延到下一把。
type KeyPool struct {
	mu       sync.Mutex
	keys     []*KeyState
	cur      int
	CoolSecs int64 // 失败冷却秒数
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

// usable 当前时刻该钥是否可用（未死、不在冷却窗口）
func (p *KeyPool) usable(k *KeyState, now int64) bool {
	if k.Dead {
		return false
	}
	if p.CoolSecs > 0 && k.Fails > 0 && now-k.LastFailTs < p.CoolSecs {
		return false
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

// ReportFail 记录失败（冷却计数）
func (p *KeyPool) ReportFail(k *KeyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k.Fails++
	k.LastFailTs = time.Now().Unix()
}

// ReportSuccess 成功清零失败计数
func (p *KeyPool) ReportSuccess(k *KeyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k.Fails = 0
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
	return &Client{
		Line: l,
		Pool: NewKeyPool(l.Keys, l.KeyFaceMicro, 300),
		HTTP: &http.Client{
			Timeout: 300 * time.Second,
			Transport: &http.Transport{
				DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				TLSHandshakeTimeout: 10 * time.Second,
				MaxIdleConns:        64,
				MaxIdleConnsPerHost: 16,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
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
				// 密钥无效/欠费：立即判死并换钥
				lastErr = fmt.Errorf("UPSTREAM_STATUS_%d", sc)
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
				_ = resp.Body.Close()
				c.Pool.ReportDead(k)
				log.Printf("[upstream] line=%s key=%d 状态%d，判死换钥", c.Line.ID, k.Idx, sc)
			case sc == 429 || sc >= 500:
				// 读错误体（小），判断是否模型级错误
				eb, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
				_ = resp.Body.Close()
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
	return nil, nil, lastErr
}

// DoKey 带模型专属钥发起请求：keyIdx<0 走通用 Do（粘性换钥池）；
// ≥0 锁定池序钥直发一次（不粘性、不判死、不换钥——专属钥仅服务对应模型，
// 避免与其他模型的钥在同一池内互判死；错误码原样透传给上层转译）。
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
	return resp, k, nil
}

// send 单次上游请求：注入密钥认证（bearer / x-api-key）
func (c *Client) send(ctx context.Context, k *KeyState, body []byte, stream bool, path string) (*http.Response, error) {
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
	return c.HTTP.Do(req)
}

// ReadAll 便捷读响应（上限 64MB）
func ReadAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, 64<<20))
}
