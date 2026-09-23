package httpapi

// 秒败风暴断路器（20260917 站长指令；20260923 修「粘性锁定」误伤付费线）。
// 单站点密钥连续上游失败（upstream_error）≥5 笔 → 对该密钥 429 冷却 60s。
// 背景：acu 线单用户 24h 6100+ 笔毫秒级秒败重试循环（客户端无退避），打满上游单 key
// 频率配额，挤掉其他用户请求并污染模型健康统计。冷却只作用于风暴密钥本身，不影响他人。
//
// 20260923 修复（生产实锤：24h 内 443 次 storm_cooldown **全部**打在付费模型上，
// 24 个付费用户被误伤；用户时序为「上游 502 → 紧接着连环 429」）：
//
//	① 粘性锁定：原 stormCheck 在冷却到期后直接放行，但 fails 仍停在 ≥5 未清零 →
//	   之后**任何单次失败**都立即再次置 60s 冷却，用户在上游抖动期永远解不开。
//	   现按线路模式分流（见 ③），付费线改为「冷却到期即清零」。
//	② 陈旧失败无限累计：原「连续累计不判时长」让数小时前的失败也能凑够阈值。
//	   现加 stormWindowSecs 窗口（全局生效）：只有窗口内的失败才累计，真正的秒败风暴本就发生在数秒内。
//	③ 付费线不计上游 5xx：DoKey 对非 200 返回 UPSTREAM_STATUS_5xx 错误（upstream.go），
//	   原逻辑把它与网络级秒败同等计入 → 上游容量不足时付费用户被 429 锁死。
//	   5xx 是上游侧问题且 DoKey 内部已换钥重试过，故对 per_call/per_token 线豁免计数。
//	   免费/众筹线保持原口径——那才是当初要防的滥用面，不得放宽。
//
// 行为：成功一笔即清零；冷却到期后付费线清零计数、免费/众筹线保持累计（防"到期瞬间又打一波"）。
// 不按时长区分快慢失败：DoKey 内部含换钥重试，秒败风暴的实际调用耗时也可能 >2s，
// 按耗时判定会漏放。
import (
	"strings"
	"sync"
	"time"
)

type stormState struct {
	fails    int
	lastFail int64 // 最近一次失败时刻（窗口判定用）
	until    int64 // 冷却截止 unix 秒（0=未冷却）
	lenient  bool  // 是否付费线（per_call/per_token）：决定冷却到期后是否清零计数
}

var (
	stormMu  sync.Mutex
	stormTab = map[string]*stormState{} // key: 站点密钥 keyHash
)

const (
	stormThreshold  = 5   // 连续上游失败阈值
	stormCooldown   = 60  // 冷却秒数
	stormWindowSecs = 120 // 失败累计窗口：仅窗口内的失败才计入连续数
)

// stormPaidMode 该线路模式是否属付费线（风暴口径放宽对象）
func stormPaidMode(mode string) bool {
	return mode == "per_call" || mode == "per_token"
}

// stormCountable 该次失败是否计入风暴计数。
// 付费线豁免上游 5xx 状态错误：DoKey 已换钥重试过，5xx 属上游容量/渠道问题而非用户滥用，
// 再叠一层 429 会把上游故障放大成「付费被限速」。网络级失败（拨号/超时）仍然计入。
func stormCountable(mode string, err error) bool {
	if !stormPaidMode(mode) || err == nil {
		return true
	}
	return !strings.HasPrefix(err.Error(), "UPSTREAM_STATUS_5")
}

// stormCheck 入口检查：冷却中的密钥返回 false（调用方 429 拦截）。
// 冷却到期即放行；**支付线**同时清零计数——否则 fails 停在阈值之上，
// 之后任一单次失败都会立即再锁 60s（粘性锁定，用户在上游抖动期永远解不开）。
func stormCheck(keyHash string) bool {
	stormMu.Lock()
	defer stormMu.Unlock()
	s := stormTab[keyHash]
	if s == nil {
		return true
	}
	now := time.Now().Unix()
	if s.until > 0 && s.until <= now {
		if s.lenient {
			s.fails = 0
			s.lastFail = 0
		}
		s.until = 0
	}
	return s.until <= now
}

// stormFail 上游失败记录；达阈值置冷却。paid 标记该密钥是否付费线（一旦为真即持续放宽）
func stormFail(keyHash string, paid bool) {
	stormMu.Lock()
	defer stormMu.Unlock()
	now := time.Now().Unix()
	s := stormTab[keyHash]
	if s == nil {
		s = &stormState{}
		stormTab[keyHash] = s
	}
	if paid {
		s.lenient = true
	}
	// 窗口外（或从未失败过）的陈旧失败不计入：防"数小时前几次抖动凑够阈值"
	if s.lastFail == 0 || now-s.lastFail > stormWindowSecs {
		s.fails = 0
	}
	s.fails++
	s.lastFail = now
	if s.fails >= stormThreshold {
		s.until = now + stormCooldown
	}
	if len(stormTab) > 8192 { // 表收缩：防长期运行膨胀
		for k, v := range stormTab {
			// 过期条目（含已达阈值者）一律删除：阈值判定在写入时已做，过期条目无保留价值
			//（旧条件 v.fails < stormThreshold 会让冷却到期的风暴密钥永不清除，只能等整表清零）
			if v.until < now {
				delete(stormTab, k)
			}
		}
		if len(stormTab) > 16384 {
			stormTab = map[string]*stormState{}
		}
	}
}

// stormReset 上游成功清零
func stormReset(keyHash string) {
	stormMu.Lock()
	defer stormMu.Unlock()
	if s := stormTab[keyHash]; s != nil {
		s.fails = 0
		s.lastFail = 0
		s.until = 0
	}
}
