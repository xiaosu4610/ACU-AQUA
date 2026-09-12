package httpapi

// /v1/stats 全站公开数据大屏 + /status 状态透明页（与 Rust usage.rs 契约等价）

import (
	"net/http"
	"time"
)

// startTime 进程启动时刻（/status 用）
var startTime = time.Now()

// handleStats GET /v1/stats —— 全站今日数据大屏（聚合，不含任何密钥信息）
func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Unix()
	utc8 := time.Now().In(time.FixedZone("UTC+8", 8*3600))
	today0 := time.Date(utc8.Year(), utc8.Month(), utc8.Day(), 0, 0, 0, 0, utc8.Location()).Unix()

	// 今日总览
	var total, okCnt int64
	var avgLat float64
	_ = a.DB.QueryRow(
		"SELECT COUNT(*), COALESCE(SUM(ok),0), COALESCE(AVG(latency_ms),0) FROM requests WHERE ts>=?",
		today0).Scan(&total, &okCnt, &avgLat)
	avgLatI := int64(avgLat)

	// 热门模型 TOP10
	topModels := []map[string]any{}
	rows, err := a.DB.Query(
		"SELECT model, COUNT(*) c FROM requests WHERE ts>=? AND model != '' GROUP BY model ORDER BY c DESC LIMIT 10", today0)
	if err == nil {
		for rows.Next() {
			var m string
			var c int64
			if rows.Scan(&m, &c) == nil {
				topModels = append(topModels, map[string]any{"model": m, "calls": c})
			}
		}
		rows.Close()
	}

	// 今日活跃（去重 key）
	activeKeys := int64(0)
	_ = a.DB.QueryRow(
		"SELECT COUNT(DISTINCT key_hash) FROM requests WHERE ts>=? AND key_hash != ''", today0).Scan(&activeKeys)

	// 24h 按小时趋势（补齐空桶）
	hour0 := (now - 23*3600) / 3600 * 3600
	hourlyMap := map[int64][2]int64{}
	rows2, err := a.DB.Query(
		"SELECT (ts/3600)*3600 AS hr, COUNT(*), COALESCE(SUM(ok),0) FROM requests WHERE ts>=? GROUP BY hr", hour0)
	if err == nil {
		for rows2.Next() {
			var hr, c, ok2 int64
			if rows2.Scan(&hr, &c, &ok2) == nil {
				hourlyMap[hr] = [2]int64{c, ok2}
			}
		}
		rows2.Close()
	}
	hourly := make([]map[string]any, 0, 24)
	for i := 0; i < 24; i++ {
		hr := hour0 + int64(i)*3600
		v := hourlyMap[hr]
		c := v[0]
		var okRate any
		if c > 0 {
			okRate = round1(float64(v[1]) * 100 / float64(c))
		}
		hourly = append(hourly, map[string]any{"hour": hr, "calls": c, "ok_rate": okRate})
	}

	// 全模型 24h 流量表
	modelStats := []map[string]any{}
	rows3, err := a.DB.Query(
		`SELECT model, COUNT(*) calls, SUM(ok)*1.0/COUNT(*) succ, COALESCE(AVG(latency_ms),0) lat, COALESCE(SUM(total_tokens),0) tokens
		 FROM requests WHERE ts>=? AND model != '' GROUP BY model ORDER BY calls DESC`, now-24*3600)
	if err == nil {
		for rows3.Next() {
			var model string
			var calls, tokens int64
			var succ, lat float64
			if rows3.Scan(&model, &calls, &succ, &lat, &tokens) == nil {
				modelStats = append(modelStats, map[string]any{
					"model": model, "calls_24h": calls,
					"success_rate":   round1(succ * 100),
					"avg_latency_ms": round1(lat),
					"total_tokens":   tokens,
				})
			}
		}
		rows3.Close()
	}

	okRate := 100.0
	if total > 0 {
		okRate = round1(float64(okCnt) * 100 / float64(total))
	}
	jsonOut(w, 200, map[string]any{
		"today": map[string]any{
			"total_calls":    total,
			"ok_rate":        okRate,
			"avg_latency_ms": avgLatI,
			"active_keys":    activeKeys,
		},
		"top_models":  topModels,
		"hourly":      hourly,
		"model_stats": modelStats,
	})
}

func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}
