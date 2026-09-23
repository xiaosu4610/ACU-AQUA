package httpapi

// guard.go 通道防刷守卫：
//   - per-IP 滑窗限流（挡在打上游之前——上游 kabuai 被刷打满时众筹线与收费线一起遭殃）
//   - 高频触发自动封禁（窗口期内反复被限 → 临时封禁）
//   - crowd（众筹线）每用户在途并发上限（防单用户并发风暴拖垮上游）
// 纯内存实现，重启即清（封禁为临时惩罚，无需持久化）。

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	ipWindow = 60 * time.Second // 滑窗时长
	// ipLimit 每 IP 每窗口允许的上游转发请求数。
	// 20260923 收紧→放宽：原 60/min 是按「聊天式」负载定的，但付费线主推 agentic
	// 编码场景（Claude Code / 编辑器插件），单用户轻易超过 60/min；且同一 NAT/CGNAT
	// 出口下的多个用户共用同一配额桶。生产实锤：日志中 170 个 (IP,分钟) 组合超过 60，
	// 峰值单 IP 单分钟 1115 次——其中既有滥用也有正常重度用户（无法区分，只能上调阈值）。
	// 滥用侧仍有三道闸：本闸算出上游的总量上限、storm 秒败断路器、免费线 30RPM/2并发。
	ipLimit = 180
	// banThreshold 10 分钟内触发限流达到该次数 → 封禁（6→12：原值过于敏感，
	// agentic 短时突发即可能凑够 6 次而被封 1 小时，属过重惩罚）
	banThreshold = 12
	banDuration  = 20 * time.Minute // 封禁时长（60→20 分钟：降低误伤代价，仍足以制止脚本滥用）
	crowdUserCon = 3                // crowd 每用户在途并发上限
	// 官方自营免费体验线（prefixed）闸门（20260919 站长指令：放宽闸门，靠**真实降级**促转化）：
	// 原 10 RPM / 单并发过于生硬（用户一撞 429 就以为是故障），改为宽松闸门——
	// 闸门只防"刷爆"（滥用），日常使用体感顺畅；真正的"慢/卡"来自免费线的
	// 低优先级与更短上游预算（见 freeUpstreamChat / freePriorityDelay）。
	freeUserCon = 2  // 免费体验线每用户在途并发上限（1→2：允许并行对话不互相阻塞）
	freeUserRPM = 30 // 免费体验线每用户每分钟请求数（10→30：正常使用不会撞墙）
)

// freePriorityDelay 免费线优先级延迟（"真实降级"的排队体现，20260919）。
//
// 原理：付费线请求不等待、立即打上游；免费线在转发前做一次极短等待，
// 使**并发拥挤时**免费请求自然排在付费请求之后（上游连接与带宽优先给付费用户）。
// 单请求额外延迟很小（基线 120ms），但密集并发时会显著叠加——这正是我们要的
// "免费在高峰会卡"的真实体验，而非人为注入故障（不污染错误统计与健康度）。
//
// 口径：并发越高等待越久（近似排队模型），上限 900ms 防极端堆积。
func (g *ipGuard) freePriorityDelay(uid int64, ip string) time.Duration {
	if guardBypass {
		return 0
	}
	key := g.freeKey(uid, ip)
	g.mu.Lock()
	inflight := g.freeCon[key]
	g.mu.Unlock()
	base := 120 * time.Millisecond
	// 每多一路在途请求 +200ms（同一用户或同 IP 的并发会被自然拉长）
	d := base + time.Duration(inflight)*200*time.Millisecond
	if d > 900*time.Millisecond {
		d = 900 * time.Millisecond
	}
	return d
}

type ipGuard struct {
	mu      sync.Mutex
	hits    map[string][]time.Time // 每 IP 窗口内请求时间戳
	rejects map[string][]time.Time // 每 IP 窗口内被拒时间戳
	banned  map[string]time.Time   // IP → 封禁到期时间
	crowd   map[int64]int32        // crowd 每用户在途并发计数
	freeCon map[string]int32       // 免费体验线在途并发计数（键：登录用户=uid 十进制；匿名="ip:"+IP）
	freeRPM map[string][]time.Time // 免费体验线窗口内请求时间戳（键规则同 freeCon）
	lastGC  time.Time
}

var guard = &ipGuard{
	hits:    map[string][]time.Time{},
	rejects: map[string][]time.Time{},
	banned:  map[string]time.Time{},
	crowd:   map[int64]int32{},
	freeCon: map[string]int32{},
	freeRPM: map[string][]time.Time{},
}

// guardBypass 测试旁路（仅 guard_test.go 的 init 置 true，生产恒 false）
var guardBypass bool

// allow IP 放行判定：false = 调用方直接回 429（不打上游）
func (g *ipGuard) allow(ip string) bool {
	if guardBypass {
		return true
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gcLocked(now)
	// 封禁期检查
	if until, ok := g.banned[ip]; ok {
		if now.Before(until) {
			return false
		}
		delete(g.banned, ip) // 期满自动解封
	}
	// 请求滑窗
	arr := g.hits[ip][:0]
	for _, t := range g.hits[ip] {
		if now.Sub(t) < ipWindow {
			arr = append(arr, t)
		}
	}
	g.hits[ip] = arr
	if len(arr) >= ipLimit {
		g.rejectLocked(ip, now)
		return false
	}
	g.hits[ip] = append(arr, now)
	return true
}

// rejectLocked 记一次被拒；10 分钟内累计达到阈值 → 封禁
func (g *ipGuard) rejectLocked(ip string, now time.Time) {
	arr := g.rejects[ip][:0]
	for _, t := range g.rejects[ip] {
		if now.Sub(t) < 10*time.Minute {
			arr = append(arr, t)
		}
	}
	arr = append(arr, now)
	g.rejects[ip] = arr
	if len(arr) >= banThreshold {
		g.banned[ip] = now.Add(banDuration)
		delete(g.rejects, ip)
	}
}

// gcLocked 惰性清理：条目过多时全量清扫过期数据（防长尾 IP 内存膨胀）
func (g *ipGuard) gcLocked(now time.Time) {
	if len(g.hits)+len(g.rejects) <= 8192 || now.Sub(g.lastGC) < 5*time.Minute {
		return
	}
	g.lastGC = now
	for ip, arr := range g.hits {
		if len(arr) == 0 || now.Sub(arr[len(arr)-1]) >= ipWindow {
			delete(g.hits, ip)
		}
	}
	for ip, arr := range g.rejects {
		if len(arr) == 0 || now.Sub(arr[len(arr)-1]) >= 10*time.Minute {
			delete(g.rejects, ip)
		}
	}
}

// crowdEnter crowd 每用户并发闸门：放行返回释放函数；超限返回 false
func (g *ipGuard) crowdEnter(uid int64) (func(), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.crowd[uid] >= crowdUserCon {
		return nil, false
	}
	g.crowd[uid]++
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.crowd[uid]--; g.crowd[uid] <= 0 {
			delete(g.crowd, uid)
		}
	}, true
}

// freeKey 体验线闸键：登录用户按 uid 计；匿名（uid<=0）按 "ip:"+clientIP 计
// （20260919 堵匿名绕过 prefixed 免费线限速的口子——此前 uid=0 直接放行）
func (g *ipGuard) freeKey(uid int64, ip string) string {
	if uid > 0 {
		return strconv.FormatInt(uid, 10)
	}
	return "ip:" + ip
}

// freeAllow 官方自营免费体验线 RPM 闸（prefixed 免费线专用）：
// 登录用户按 uid、匿名按 IP 维度计数（匿名不再绕过）
func (g *ipGuard) freeAllow(uid int64, ip string) bool {
	if guardBypass {
		return true
	}
	key := g.freeKey(uid, ip)
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	arr := g.freeRPM[key][:0]
	for _, t := range g.freeRPM[key] {
		if now.Sub(t) < ipWindow {
			arr = append(arr, t)
		}
	}
	g.freeRPM[key] = arr
	if len(arr) >= freeUserRPM {
		return false
	}
	g.freeRPM[key] = append(arr, now)
	return true
}

// freeEnter 官方自营免费体验线并发闸：放行返回释放函数；超限返回 false（键规则同 freeAllow）
func (g *ipGuard) freeEnter(uid int64, ip string) (func(), bool) {
	if guardBypass {
		return func() {}, true
	}
	key := g.freeKey(uid, ip)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.freeCon[key] >= freeUserCon {
		return nil, false
	}
	g.freeCon[key]++
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.freeCon[key]--; g.freeCon[key] <= 0 {
			delete(g.freeCon, key)
		}
	}, true
}

// guardReject 统一 429 响应（站内口径，与上游限流响应区分）
func guardReject(w http.ResponseWriter) {
	errOut(w, 429, "rate_limited", "请求过于频繁，请稍后再试（每分钟最多 180 次；高频触发将临时限制访问）")
}
