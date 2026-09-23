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
	Idx int
	// RawIdx DB 原始 idx（admin_line_keys.idx）：模型专属钥定位与台账
	// （requests.key_idx / admin_line_keys）统一按原始 idx 口径——池内序在剔除
	// dead/超卖钥后与原始 idx 错位。静态配置线/测试无 DB idx 时 = Idx（池内序）
	RawIdx       int
	Key          string
	InitialMicro int64
	UsedMicro    int64
	Dead         bool
	// DeadUntil 死钥复活时刻（unix 秒；0=永久死）。20260919：判死此前是终态，
	// 但配额类判死（429 额度耗尽/面值耗尽）会随上游窗口刷新而恢复——
	// 生产实锤 codex 线 50 把钥死 10 把且永不复活，可用池持续萎缩至 97% 失败率。
	// 仅"确定性失效"（401/403 达阈值判死）保持永久，其余按窗口复活。
	DeadUntil  int64
	LastFailTs int64
	Fails      int
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
		p.keys = append(p.keys, &KeyState{Idx: i, RawIdx: i, Key: k, InitialMicro: faceMicro})
	}
	return p
}

// alignRawIdxs 把 DB 原始 idx（admin_line_keys.idx，与 keys 一一对应）对齐到池内钥：
// NewKeyPool 剔除空白钥后池内序会与原始 idx 错位（dead/超卖钥不进池）。
// 静态配置线（TOML，无 DB idx）或长度不一致时不处理（RawIdx 维持池内序）。
func alignRawIdxs(p *KeyPool, keys []string, idxs []int) {
	if len(keys) == 0 || len(keys) != len(idxs) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	j := 0
	for i, k := range keys {
		if strings.TrimSpace(k) == "" { // 与 NewKeyPool 同一剔除规则
			continue
		}
		if j < len(p.keys) {
			p.keys[j].RawIdx = idxs[i]
		}
		j++
	}
}

// Len 池中密钥总数
func (p *KeyPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.keys)
}

// usable 当前时刻该钥是否可用（未死、不在冷却窗口、未超每钥 RPM）。
// 20260919：死钥到期自动复活（DeadUntil>0 且已过期 → 清死标并重置失败计数，
// 给它一次重新证明的机会；若仍不可用会再次判死，成本可控）。
func (p *KeyPool) usable(k *KeyState, now int64) bool {
	if k.Dead {
		if k.DeadUntil == 0 || now < k.DeadUntil {
			return false // 永久死，或复活时刻未到
		}
		// 复活：清死标与失败历史，重新参与调度
		k.Dead = false
		k.DeadUntil = 0
		k.Fails = 0
		k.AuthFails = 0
		k.CoolUntil = 0
		k.UsedMicro = 0
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
	// 20260919 负载均衡增强：多候选时优先选「窗口内请求数最少」的钥（真正的
	// 负载均衡，而非总打同一把把刚冷却的钥再次打爆），请求数相同再比最久未失败。
	var fallback *KeyState
	fbCnt := 0
	for i := 0; i < n; i++ {
		k := p.keys[(p.cur+i)%n]
		if k.Dead || (skip != nil && skip[k.Idx]) {
			continue
		}
		cnt := 0
		for _, t := range k.Recent {
			if now-t < 60 {
				cnt++
			}
		}
		if fallback == nil || cnt < fbCnt || (cnt == fbCnt && k.LastFailTs < fallback.LastFailTs) {
			fallback, fbCnt = k, cnt
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

// AcquireFor 取指定原始 idx 的密钥（模型专属钥模式）：忽略粘性与冷却，dead 钥直接报错。
// 按原始 idx（admin_line_keys.idx / 模型 key_idx 的 DB 口径）匹配——池内序在剔除
// dead/超卖钥后与原始 idx 错位；无匹配（该原始 idx 未进池）报 KEY_IDX_OUT_OF_RANGE。
// 20260919：死钥先尝试复活（配额窗口刷新后回归），仍死才报错。
func (p *KeyPool) AcquireFor(poolIdx int) (*KeyState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().Unix()
	for _, k := range p.keys {
		if k.RawIdx != poolIdx {
			continue
		}
		if k.Dead && p.usable(k, now) {
			return k, nil // 复活成功（usable 已清死标）
		}
		if k.Dead {
			return nil, fmt.Errorf("KEY_IDX_DEAD(%d)", poolIdx)
		}
		return k, nil
	}
	return nil, fmt.Errorf("KEY_IDX_OUT_OF_RANGE(%d/%d)", poolIdx, len(p.keys))
}

// ReportFace 累计面值消耗；超初始面值标记死钥（额度耗得差不多自动摘除）。
// 20260919：改为可复活——面值/额度会随上游充值或窗口刷新恢复，30 分钟后自动重试；
// 若仍耗尽会再次判死（自适应），避免死钥永久占用池位。
func (p *KeyPool) ReportFace(k *KeyState, faceMicro int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k.UsedMicro += faceMicro
	if k.InitialMicro > 0 && k.UsedMicro >= k.InitialMicro {
		k.Dead = true
		k.DeadUntil = time.Now().Unix() + 1800
	}
}

// ReportDead 立即标记死钥（上游判定密钥无效/欠费时）。
// 20260919：配额类判死带复活时刻（30 分钟）——上游窗口/充值后自动回归可用池；
// 若为确定性失效（鉴权连续失败）则永久死（DeadUntil=0）。
func (p *KeyPool) ReportDead(k *KeyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k.Dead = true
	if k.DeadUntil == 0 {
		k.DeadUntil = time.Now().Unix() + 1800
	}
}

// ReportDeadPermanent 永久判死（确定性失效：鉴权连续失败达阈值等），永不复活
func (p *KeyPool) ReportDeadPermanent(k *KeyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k.Dead = true
	k.DeadUntil = 0
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

// rateLimitCoolSecs 429 限流后该钥的短冷却秒数（上游限流窗口刷新即恢复）。
// 用变量而非常量：测试需缩短等待时间（生产恒为 20s）。
var rateLimitCoolSecs int64 = 20

// ReportRateLimit 限流（429 tpm/rpm）短冷却 20s：窗口期过后该钥自动恢复可用。
// 429 是限流不是功能失败：同步清零 Fails——否则一次旧失败残留会让 usable() 的
// 300s (Fails>0) 冷却覆盖本函数的 20s 短冷却语义，好钥被锁死 5 分钟
func (p *KeyPool) ReportRateLimit(k *KeyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if until := time.Now().Unix() + rateLimitCoolSecs; until > k.CoolUntil {
		k.CoolUntil = until
	}
	k.Fails = 0
	k.LastFailTs = time.Now().Unix()
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

// NextRateLimitRecovery 池中最短的"冷却剩余时长"（0 = 已有可用钥，无需等待）。
// 供 429 网关内部重试估算"等待多久再重跑全池"——等足池内最早恢复的那把钥，
// 比拍脑袋固定休眠更贴合上游限流窗口（20260924）。
// 永久死钥（DeadUntil==0）不参与估算：它们不会恢复。
func (p *KeyPool) NextRateLimitRecovery() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().Unix()
	minWait := int64(-1)
	for _, k := range p.keys {
		if k.Dead && k.DeadUntil == 0 {
			continue // 永久判死：永不恢复，不参与等待估算
		}
		if k.CoolUntil <= now {
			return 0 // 存在已过冷却的钥：立即可重跑
		}
		if w := k.CoolUntil - now; minWait < 0 || w < minWait {
			minWait = w
		}
	}
	if minWait < 0 {
		return 0
	}
	return time.Duration(minWait) * time.Second
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
		// 20260919 连接池扩容：流式高并发下 MaxIdleConnsPerHost=16 会导致
		// 频繁新建 TCP+TLS（TTFT 抖动 + 上游连接数打满触发限流）；
		// 上游多为 HTTP/1.1，放宽到 64 显著减少握手开销
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     90 * time.Second,
		// 显式启用 HTTP/2（自定义 DialContext 时 Go 默认不尝试 h2）：
		// 支持 h2 的上游可多路复用，进一步降连接压力
		ForceAttemptHTTP2: true,
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
	// DB 装载线：池内钥对齐 admin_line_keys 原始 idx（专属钥定位与台账统一口径）
	alignRawIdxs(cl.Pool, l.Keys, l.KeyIdxs)
	// DB 死标注入：dead=1 的钥进池但标死（30 分钟复活窗口）——
	// 20260919：原 SQL 层直接剔除，导致配额窗口刷新后也无法回归，可用池只减不增
	if len(l.DeadKeyIdxs) > 0 {
		markDeadIdxs(cl.Pool, l.DeadKeyIdxs)
	}
	return cl
}

// markDeadIdxs 把 DB 死标应用到池内对应原始 idx 的钥（带 30 分钟复活窗口）
func markDeadIdxs(p *KeyPool, idxs []int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	until := time.Now().Unix() + 1800
	for _, idx := range idxs {
		for _, k := range p.keys {
			if k.RawIdx == idx {
				k.Dead = true
				k.DeadUntil = until
				break
			}
		}
	}
}

// 每钥重试次数与最多尝试密钥数
const (
	perKeyRetries = 2 // 同一把钥失败重试 1 次（共 2 次尝试）后换钥——
	// 20260919：原为 3 次，但限流类错误同钥重试无意义（实测 449 次/天纯浪费等待），
	// 且 IsRateLimited 命中后本就 break 换钥，此值主要影响真实 5xx 的退避深度
	maxKeyHops = 8 // 最多换 8 把钥（20260919：原 5 把，池大时过少会过早放弃；
	// 模型级错误换钥不计失败，上限放宽成本低）
)

// 429 全池限流的网关内部重试（20260924 站长指令："出现 429 自动网关重试，不要直接返回客户端"）。
//
// 语义：上游限流窗口（tpm/rpm）是**时间性**的——短冷却过后同一批钥即可复用。
// 此前一旦连续 6 把钥限流就立刻向客户端抛 429「当前访问过于火爆」，把可自愈的
// 抖动直接变成用户失败。现改为等限流窗口刷新后重跑全池，预算耗尽才交上层转译 429。
const (
	rateLimitRoundMax = 3                // 总轮次（含首轮）：首轮失败后最多再内部重试 2 轮
	rateLimitBudget   = 30 * time.Second // 内部重试总预算上限（等待 + 重试耗时）
	rateLimitWaitCap  = 20 * time.Second // 单次等待上限（对齐密钥 429 短冷却 20s）
)

// rateLimitRetryBudget 内部重试预算：取 rateLimitBudget，但不超过 ctx 剩余时间的一半
// （等待不能吃光上游/客户端预算，否则重试成功也会因超时失败），下限 5s——
// 保证短超时链路（如免费线非流式 45s）也能顶一次短等待。
func rateLimitRetryBudget(ctx context.Context) time.Duration {
	b := rateLimitBudget
	if dl, ok := ctx.Deadline(); ok {
		if half := time.Until(dl) / 2; half < b {
			b = half
		}
	}
	if b < 5*time.Second {
		b = 5 * time.Second
	}
	return b
}

// IsModelUnavailable 上游错误体是否表明"模型不可用"（已下线/无可用通道）。
// 此类错误换钥有意义（不同钥可能属不同分组），但同钥重试无意义。
func IsModelUnavailable(body []byte) bool {
	s := strings.ToLower(string(body))
	// 20260919：新增 "for model"/"under group" 措辞识别——kabuai 生产返回
	// "No available channel for model deepseek-v4-flash under group pro代理 (distributor)"，
	// 语义是"该模型在当前分组无渠道"（模型维度，换钥可能命中不同分组），
	// 与纯渠道级故障（整池无节点，换钥无意义）严格区分。
	return strings.Contains(s, "for model") ||
		strings.Contains(s, "under group") ||
		strings.Contains(s, "model_not_found") ||
		strings.Contains(s, "model not found") ||
		strings.Contains(s, "model_disabled") ||
		strings.Contains(s, "模型已关闭") ||
		strings.Contains(s, "does not exist")
}

// IsRateLimited 上游错误体是否为"限流"（tpm/rpm 速率限制）。
// 此类错误同钥重试无意义（窗口内继续打只会继续 429），应立即换钥；
// 钥本身没死，短冷却（秒级窗口过后即恢复）而非 300s 指数退避。
// 20260919 生产修复：kabuai 实际返回 {"message":"rpm exhausted","type":"quota_exceeded_error"}
// —— 此前措辞不匹配导致 449 次/天误走"同钥重试"（3 次打同一把已限流的钥，纯浪费且拖长用户等待）。
// 注意 "quota_exceeded_error" 是速率配额（窗口刷新即恢复），与账号级欠费
// （insufficient_quota，需换号）语义不同，故归入限流分支。
func IsRateLimited(body []byte) bool {
	s := strings.ToLower(string(body))
	return strings.Contains(s, "rate_limit_error") ||
		strings.Contains(s, "429003") ||
		strings.Contains(s, "rate limit") ||
		strings.Contains(s, "tpm limit") ||
		strings.Contains(s, "rpm limit") ||
		strings.Contains(s, "rpm exhausted") ||
		strings.Contains(s, "tpm exhausted") ||
		strings.Contains(s, "quota_exceeded_error") ||
		strings.Contains(s, "rate_exceeded") ||
		strings.Contains(s, "too many requests") ||
		strings.Contains(s, "请求过于频繁") ||
		strings.Contains(s, "限流")
}

// IsChannelExhausted 上游**渠道级**不可用：同一上游的所有密钥共享同一渠道池，
// 换钥/重试都注定失败 —— 必须快速失败，避免渠道抖动时用户等十几秒才收到错误。
// 20260919：排除模型级措辞（"for model"/"under group"）——kabuai 的
// "No available channel for model X under group Y" 是模型维度问题（换钥可能命中
// 不同分组），若误判为渠道级会直接放弃重试；此处只认纯渠道级措辞。
func IsChannelExhausted(body []byte) bool {
	s := strings.ToLower(string(body))
	if !strings.Contains(s, "no available channel") {
		return false
	}
	return !strings.Contains(s, "for model") && !strings.Contains(s, "under group")
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
	consecRL := 0                                      // 连续限流换钥计数：≥6 视为全池饱和，提前止损（逐钥再试纯属浪费时长）
	channelDown := false                               // 渠道级不可用：换钥无意义，立即终止
	quotaHits, nonQuotaFails, rateLimitHits := 0, 0, 0 // 429 分型计数：全池纯配额耗尽/纯限流 → 上层转译为业务态
	poolSaturated := false                             // 本轮"连续限流全池饱和"标记 → 触发网关内部重试
	retryBudget := rateLimitRetryBudget(ctx)           // 429 内部重试预算（受 ctx 剩余约束）
	waitUsed := time.Duration(0)                       // 已消耗的内部重试等待
	for round := 0; ; round++ {
		// 每轮重置遍历状态：上一轮已把钥打进 20s 短冷却，窗口过后需重新轮询全池
		tried = map[int]bool{}
		acquireTries = 0
		consecRL = 0
		poolSaturated = false
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
						c.Pool.ReportDeadPermanent(k) // 鉴权连续失败=确定性失效，永久判死不复活
						log.Printf("[upstream] line=%s key=%d 状态%d 鉴权连续失败达阈值，永久判死换钥", c.Line.ID, k.Idx, sc)
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
					if sc == 503 && strings.Contains(string(eb), "codex_auth_dead") {
						// codex 账号 RT 链已死（invalid_grant 类确定性失败）：该账号永远
						// 不会再成功——立即判死换号，绝不原地重试 15 次（20260919 生产
						// "同钥 3 次 × 5 账号" 无效轮询 → 502 的根因修复）
						nonQuotaFails++
						c.Pool.ReportDead(k)
						log.Printf("[upstream] line=%s key=%d codex 账号令牌链已失效，判死换号", c.Line.ID, k.Idx)
						break
					}
					if IsModelUnavailable(eb) {
						// 模型在该钥的分组不可用：换钥有意义，同钥重试无意义。
						// 20260919：模型级判断提到渠道级之前——kabuai 实际返回
						// "No available channel for model X under group Y" 同时含两种措辞，
						// 若先判渠道级会直接终止（错失"其他钥分组不同可能可用"的机会）
						modelLevel = true
						modelResp = rebuildResp(resp, eb)
						log.Printf("[upstream] line=%s key=%d 状态%d 模型级错误，直接换钥", c.Line.ID, k.Idx, sc)
					} else if IsChannelExhausted(eb) {
						// 渠道级不可用：全钥共享同一渠道池，重试/换钥注定失败 → 立即返回
						modelLevel = true
						channelDown = true
						modelResp = rebuildResp(resp, eb)
						log.Printf("[upstream] line=%s key=%d 状态%d 渠道级不可用，快速失败", c.Line.ID, k.Idx, sc)
					} else if sc == 429 && IsRateLimited(eb) {
						// 限流（tpm/rpm）：同钥重试无意义（窗口内继续打只会继续 429），
						// 短冷却 20s（窗口过后自动恢复，绝不长冷）并立即换钥——
						// 旧逻辑在此走"同钥重试+300s 冷却"，50 钥池被热钥拖累后全池冷却 → 整线 502
						lastErr = fmt.Errorf("UPSTREAM_RATE_LIMITED")
						rateLimitHits++
						consecRL++
						c.Pool.ReportRateLimit(k)
						log.Printf("[upstream] line=%s key=%d 429 限流(tpm/rpm)，短冷却20s换钥 body=%s", c.Line.ID, k.Idx, eb)
						// 20260919：阈值 3→6——小池线（aqua 仅 2 把钥）连续 3 把即触发会让
						// 整线过早止损；放宽后仍能在真正饱和时快速失败。
						// 20260924：饱和不再直接抛 429 给客户端，改为标记后进入网关内部重试。
						if consecRL >= 6 {
							log.Printf("[upstream] line=%s 连续%d钥限流，全池饱和 → 网关内部重试", c.Line.ID, consecRL)
							poolSaturated = true
						}
						break
					} else if sc == 429 {
						// 无标记 429：仍是限流语义（负载/配额边缘）——同钥重试只会继续 429，
						// 短冷却 20s + 立即换钥（与带标记限流同策略），避免把整池拖进 300s 长冷却
						// 后全池 EXHAUSTED → 502（20260919 生产 kabuai 429×499 的 cascade 修复）
						lastErr = fmt.Errorf("UPSTREAM_RATE_LIMITED")
						rateLimitHits++
						consecRL++
						c.Pool.ReportRateLimit(k)
						log.Printf("[upstream] line=%s key=%d 429 限流(无标记)，短冷却20s换钥 body=%s", c.Line.ID, k.Idx, eb)
						// 20260919：阈值 3→6——小池线（aqua 仅 2 把钥）连续 3 把即触发会让
						// 整线过早止损（458 次/天）；放宽后仍能在真正饱和时快速失败，
						// 但给足换钥机会（池大时更明显）
						// 20260924：饱和不再直接抛 429 给客户端，改为标记后进入网关内部重试。
						if consecRL >= 6 {
							log.Printf("[upstream] line=%s 连续%d钥限流，全池饱和 → 网关内部重试", c.Line.ID, consecRL)
							poolSaturated = true
						}
						break
					} else {
						nonQuotaFails++
						consecRL = 0
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
			if poolSaturated {
				break // 全池饱和：直接进入内部重试判定，不再空耗 acquire 退避
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
		if channelDown {
			break
		}
		// —— 429 全池限流：网关内部重试（20260924 站长指令）——
		// 触发条件：本轮连续饱和 或 纯限流（无其他类型失败）且尚无模型级结果。
		// 处理：等限流窗口刷新（NextRateLimitRecovery 估算）后重跑全池；
		// 预算（rateLimitRetryBudget，受 ctx 剩余一半约束）耗尽才交上层转译 429。
		rlOnly := rateLimitHits > 0 && nonQuotaFails == 0 && quotaHits == 0
		if (poolSaturated || rlOnly) && modelResp == nil && round < rateLimitRoundMax-1 &&
			waitUsed < retryBudget && ctx.Err() == nil {
			w := c.Pool.NextRateLimitRecovery()
			if w < time.Second {
				w = time.Second // 已有非限流钥可用：短停顿即可重跑（避免瞬时风控再命中）
			}
			if w > rateLimitWaitCap {
				w = rateLimitWaitCap
			}
			if rem := retryBudget - waitUsed; w > rem {
				w = rem
			}
			waitUsed += w
			log.Printf("[upstream] line=%s 429 全池限流：网关内部重试第%d轮，等待%s后重跑全池（已用预算%s/%s）",
				c.Line.ID, round+1, w, waitUsed, retryBudget)
			backoff(w)
			if ctx.Err() != nil {
				break // 预算/客户端已超时：按原语义交上层（不伪造响应）
			}
			// 新轮次重新判定失败类型；rateLimitHits 累计保留，供最终 429 转译
			nonQuotaFails, quotaHits = 0, 0
			continue
		}
		break
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
	// 全池纯限流（无其他类型失败）：上层转译为 429 繁忙业务态而非 502 故障
	if rateLimitHits > 0 && nonQuotaFails == 0 && quotaHits == 0 {
		lastErr = fmt.Errorf("UPSTREAM_RATE_LIMITED")
	}
	return nil, nil, lastErr
}

// DoKey 带模型专属钥发起请求：keyIdx<0 走通用 Do（粘性换钥池）；
// ≥0 锁定原始 idx 钥直发一次（不粘性、不换钥——专属钥仅服务对应模型，
// 避免与其他模型的钥在同一池内互判死；错误码原样透传给上层转译）。
// 上游状态码照常记账（与 Do 同口径，冷却后本模型自动恢复，不反复打上游）：
//   - 402（面值耗尽）判死：专属钥耗尽后 AcquireFor 直接报 KEY_IDX_DEAD，
//     上层转译为明确的"专属通道余额耗尽"
//   - 401/403 鉴权失败分型（达阈值判死，未达阈值长冷却 15 分钟）
//   - 429 限流短冷却 20s；5xx 记失败走指数冷却
func (c *Client) DoKey(ctx context.Context, body []byte, stream bool, path string, keyIdx int64) (*http.Response, *KeyState, error) {
	if keyIdx < 0 {
		return c.Do(ctx, body, stream, path)
	}
	k, err := c.Pool.AcquireFor(int(keyIdx))
	if err != nil {
		// StrictKeyIdx（分组隔离线）：专属钥不可用时**绝不降级**——同线其他钥属于
		// 不同上游分组，调本模型必然失败（No available channel for model X under group Y）。
		// 直接返回明确错误，让上层转译为"专属通道暂不可用"，避免无谓的一次错钥请求。
		if c.Line.StrictKeyIdx {
			log.Printf("[upstream] line=%s 严格专属钥 idx=%d 不可用(%v)，不降级（分组隔离）", c.Line.ID, keyIdx, err)
			return nil, nil, fmt.Errorf("KEY_IDX_UNAVAILABLE")
		}
		// 20260919 负载均衡修复：专属钥不可用（判死/未进池）不再直接 502——
		// 降级到通用池（粘性轮换其余健康钥）。生产实锤：aqua 线仅 2 把钥、
		// 4 个模型钉死 key_idx=1，单钥判死即整线瘫痪。
		log.Printf("[upstream] line=%s 专属钥 idx=%d 不可用(%v)，降级通用池", c.Line.ID, keyIdx, err)
		return c.Do(ctx, body, stream, path)
	}
	// 网络层单发抖动重试一次（20260919：专属钥单发无重试，一次瞬态拨号/TLS 抖动
	// 即 502"线路繁忙"——TCP/TLS 握手类失败与钥无关，同钥立即重试一次成本极低）
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		resp, err = c.send(ctx, k, body, stream, path)
		if err == nil || ctx.Err() != nil {
			break
		}
		if attempt >= 1 {
			break
		}
		log.Printf("[upstream] line=%s key=%d 专属钥网络错误(第1次): %v，立即重试", c.Line.ID, k.RawIdx, err)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if err != nil {
		// 网络层彻底失败（未达上游）：换钥不改变连接目标（同一 base_url），
		// 分组隔离线直接返回错误，避免无谓的错钥请求。
		if c.Line.StrictKeyIdx {
			log.Printf("[upstream] line=%s key=%d 专属钥网络失败(%v)，严格模式不降级", c.Line.ID, k.RawIdx, err)
			return nil, nil, err
		}
		log.Printf("[upstream] line=%s key=%d 专属钥网络失败(%v)，降级通用池重试", c.Line.ID, k.RawIdx, err)
		return c.Do(ctx, body, stream, path)
	}
	// 非 200 分型记账（原逻辑保留）；其中「可换钥恢复」的类别额外降级通用池——
	// 专属钥限流/欠费/故障时，同线其他钥可能完全健康（20260919 负载均衡）
	fallback := false
	switch {
	case resp.StatusCode == 402:
		// 上游面值耗尽：该钥当前不可用（可复活——上游充值/窗口刷新后回归）
		c.Pool.ReportDead(k)
		log.Printf("[upstream] line=%s key=%d 专属钥状态402 面值耗尽，判死(30min后可复活)", c.Line.ID, k.RawIdx)
		fallback = true
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		// 鉴权失败分型（与 Do 同口径）：达阈值永久判死，未达阈值长冷却不判死
		if c.Pool.ReportAuthFail(k) {
			c.Pool.ReportDeadPermanent(k)
			log.Printf("[upstream] line=%s key=%d 专属钥状态%d 鉴权连续失败达阈值，永久判死", c.Line.ID, k.RawIdx, resp.StatusCode)
		} else {
			log.Printf("[upstream] line=%s key=%d 专属钥状态%d 疑似上游风控，长冷却15min未判死", c.Line.ID, k.RawIdx, resp.StatusCode)
		}
		fallback = true
	case resp.StatusCode == 429:
		// 限流：短冷却 20s（429 是限流不是功能失败），立即换钥降级
		c.Pool.ReportRateLimit(k)
		log.Printf("[upstream] line=%s key=%d 专属钥状态429 限流，短冷却20s并降级通用池", c.Line.ID, k.RawIdx)
		fallback = true
	case resp.StatusCode >= 500:
		// 上游 5xx：记失败走指数冷却；换钥/换连接可能恢复
		c.Pool.ReportFail(k)
		log.Printf("[upstream] line=%s key=%d 专属钥状态%d 记失败指数冷却并降级通用池", c.Line.ID, k.RawIdx, resp.StatusCode)
		fallback = true
	}
	if fallback {
		// 释放当前响应体（避免连接泄漏）后走通用池
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
		_ = resp.Body.Close()
		// 分组隔离线：不降级——其他钥属于不同上游分组，调本模型必然失败。
		// 保留上游真实状态码语义（429/5xx）交上层转译，避免伪造响应。
		if c.Line.StrictKeyIdx {
			// 429 限流例外（20260924 站长指令"出现 429 自动网关重试，不要直接返回客户端"）：
			// 限流是**时间性**的（窗口刷新即恢复），直接抛错等于把可自愈抖动转成用户失败。
			// 专属钥不能换钥，只能等窗口刷新后重打本钥；预算耗尽才透传真实状态码。
			if resp.StatusCode == 429 {
				if r3, ok := c.retryExclusiveRateLimited(ctx, k, body, stream, path); ok {
					return r3, k, nil
				}
			}
			log.Printf("[upstream] line=%s key=%d 状态%d 严格模式不降级（分组隔离）", c.Line.ID, k.RawIdx, resp.StatusCode)
			return nil, nil, fmt.Errorf("UPSTREAM_STATUS_%d", resp.StatusCode)
		}
		if r2, k2, err2 := c.Do(ctx, body, stream, path); err2 == nil {
			return r2, k2, nil
		} else if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		// 通用池也失败：返回上游真实错误码（上层按 429/5xx 分型转译），
		// 而非伪造响应——保住原始状态语义
		return nil, nil, fmt.Errorf("UPSTREAM_STATUS_%d", resp.StatusCode)
	}
	return resp, k, nil
}

// retryExclusiveRateLimited 专属钥 429 限流后的网关内部重试（20260924 站长指令）：
// 按限流窗口等待后**重打同一把钥**——分组隔离线不能换钥（同线其他钥属不同上游分组，
// 调本模型必然失败），只能等窗口刷新。预算受 ctx 约束（见 rateLimitRetryBudget）。
// 返回 (resp,true) = 已拿到 200；false = 未成功，交调用方按原 429 状态码转译。
func (c *Client) retryExclusiveRateLimited(ctx context.Context, k *KeyState, body []byte, stream bool, path string) (*http.Response, bool) {
	budget := rateLimitRetryBudget(ctx)
	used := time.Duration(0)
	for round := 1; round < rateLimitRoundMax; round++ {
		if used >= budget || ctx.Err() != nil {
			return nil, false
		}
		w := rateLimitWaitCap
		if rem := budget - used; w > rem {
			w = rem
		}
		used += w
		log.Printf("[upstream] line=%s key=%d 429 限流：网关内部重试第%d轮，等待%s后重打本钥", c.Line.ID, k.RawIdx, round, w)
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(w):
		}
		resp, err := c.send(ctx, k, body, stream, path)
		if err != nil {
			if ctx.Err() != nil {
				return nil, false
			}
			continue // 网络抖动：窗口等待后重试成本低，下一轮再试
		}
		if resp.StatusCode == 200 {
			c.Pool.ReportSuccess(k)
			return resp, true
		}
		if resp.StatusCode == 429 {
			// 仍限流：续期短冷却（窗口重置）后继续下一轮
			c.Pool.ReportRateLimit(k)
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
			continue
		}
		// 其他状态码：不伪造响应，交调用方按原 429 语义转译
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
		_ = resp.Body.Close()
		return nil, false
	}
	return nil, false
}

// send 单次上游请求：注入密钥认证（bearer / x-api-key / codex OAuth）。
// StrictClean 线先清洗请求体（剥上游白名单外字段）；成功获得 HTTP 响应（不论状态码）
// 都记入每钥 RPM 滑窗——网络层失败（未达上游）不计。
func (c *Client) send(ctx context.Context, k *KeyState, body []byte, stream bool, path string) (*http.Response, error) {
	if c.Line.AuthStyle == "codex" {
		// Codex（ChatGPT 账号）线：RT→AT 换票 + chat/completions↔Responses 协议转换（见 codex.go）
		return c.codexSend(ctx, k, body, stream, path)
	}
	if c.Line.AuthStyle == "kiro" {
		// Kiro（AWS CodeWhisperer 账号）线：OIDC 换票 + chat/completions↔AWS Event Stream 协议转换（见 kiro.go）
		return c.kiroSend(ctx, k, body, stream, path)
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
