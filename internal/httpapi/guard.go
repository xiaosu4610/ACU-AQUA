package httpapi

// guard.go 通道防刷守卫：
//   - per-IP 滑窗限流（挡在打上游之前——上游 kabuai 被刷打满时众筹线与收费线一起遭殃）
//   - 高频触发自动封禁（窗口期内反复被限 → 临时封禁）
//   - crowd（众筹线）每用户在途并发上限（防单用户并发风暴拖垮上游）
// 纯内存实现，重启即清（封禁为临时惩罚，无需持久化）。

import (
	"net/http"
	"sync"
	"time"
)

const (
	ipWindow     = 60 * time.Second  // 滑窗时长
	ipLimit      = 60                // 每 IP 每窗口允许的上游转发请求数（正常用户远达不到）
	banThreshold = 6                 // 10 分钟内触发限流达到该次数 → 封禁
	banDuration  = 60 * time.Minute  // 封禁时长
	crowdUserCon = 3                 // crowd 每用户在途并发上限
	freeUserCon  = 1                 // 官方自营免费体验线（prefixed）每用户在途并发上限
	freeUserRPM  = 10                // 官方自营免费体验线每用户每分钟请求数（体验定位：满速走收费线）
)

type ipGuard struct {
	mu      sync.Mutex
	hits    map[string][]time.Time // 每 IP 窗口内请求时间戳
	rejects map[string][]time.Time // 每 IP 窗口内被拒时间戳
	banned  map[string]time.Time   // IP → 封禁到期时间
	crowd   map[int64]int32        // crowd 每用户在途并发计数
	freeCon map[int64]int32        // 免费体验线每用户在途并发计数
	freeRPM map[int64][]time.Time  // 免费体验线每用户窗口内请求时间戳
	lastGC  time.Time
}

var guard = &ipGuard{
	hits:    map[string][]time.Time{},
	rejects: map[string][]time.Time{},
	banned:  map[string]time.Time{},
	crowd:   map[int64]int32{},
	freeCon: map[int64]int32{},
	freeRPM: map[int64][]time.Time{},
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

// freeAllow 官方自营免费体验线每用户 RPM 闸（prefixed 免费线专用；uid=0 匿名不计——匿名走 IP 级滑窗）
func (g *ipGuard) freeAllow(uid int64) bool {
	if uid <= 0 || guardBypass {
		return true
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	arr := g.freeRPM[uid][:0]
	for _, t := range g.freeRPM[uid] {
		if now.Sub(t) < ipWindow {
			arr = append(arr, t)
		}
	}
	g.freeRPM[uid] = arr
	if len(arr) >= freeUserRPM {
		return false
	}
	g.freeRPM[uid] = append(arr, now)
	return true
}

// freeEnter 官方自营免费体验线每用户并发闸：放行返回释放函数；超限返回 false
func (g *ipGuard) freeEnter(uid int64) (func(), bool) {
	if uid <= 0 || guardBypass {
		return func() {}, true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.freeCon[uid] >= freeUserCon {
		return nil, false
	}
	g.freeCon[uid]++
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.freeCon[uid]--; g.freeCon[uid] <= 0 {
			delete(g.freeCon, uid)
		}
	}, true
}

// guardReject 统一 429 响应（站内口径，与上游限流响应区分）
func guardReject(w http.ResponseWriter) {
	errOut(w, 429, "rate_limited", "请求过于频繁，请稍后再试（每分钟最多 60 次；高频触发将临时限制访问）")
}
