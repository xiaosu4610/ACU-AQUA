package httpapi

// 秒败风暴断路器（20260917 站长指令）：
// 单站点密钥连续上游失败（upstream_error）≥5 笔 → 对该密钥 429 冷却 60s。
// 背景：acu 线单用户 24h 6100+ 笔毫秒级秒败重试循环（客户端无退避），打满上游单 key
// 频率配额，挤掉其他用户请求并污染模型健康统计。冷却只作用于风暴密钥本身，不影响他人。
// 行为：成功一笔即清零；冷却到期后再败继续累计并重置 60s（防"到期瞬间又打一波"）。
// 不按时长区分快慢失败：DoKey 内部含换钥重试，秒败风暴的实际调用耗时也可能 >2s，
// 按耗时判定会漏放；上游整体故障时被冷却 60s 也无实际损失（那 60s 内上游同样不可用）。

import (
	"sync"
	"time"
)

type stormState struct {
	fails int
	until int64 // 冷却截止 unix 秒（0=未冷却）
}

var (
	stormMu  sync.Mutex
	stormTab = map[string]*stormState{} // key: 站点密钥 keyHash
)

const (
	stormThreshold = 5  // 连续上游失败阈值
	stormCooldown  = 60 // 冷却秒数
)

// stormCheck 入口检查：冷却中的密钥返回 false（调用方 429 拦截）
func stormCheck(keyHash string) bool {
	stormMu.Lock()
	defer stormMu.Unlock()
	s := stormTab[keyHash]
	if s == nil {
		return true
	}
	return s.until <= time.Now().Unix()
}

// stormFail 上游失败记录（连续累计，不判时长）；达阈值置冷却
func stormFail(keyHash string) {
	stormMu.Lock()
	defer stormMu.Unlock()
	s := stormTab[keyHash]
	if s == nil {
		s = &stormState{}
		stormTab[keyHash] = s
	}
	s.fails++
	if s.fails >= stormThreshold {
		s.until = time.Now().Unix() + stormCooldown
	}
	if len(stormTab) > 8192 { // 表收缩：防长期运行膨胀
		now := time.Now().Unix()
		for k, v := range stormTab {
			if v.until < now && v.fails < stormThreshold {
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
		s.until = 0
	}
}
