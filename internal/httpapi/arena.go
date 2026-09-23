package httpapi

// 模型竞技场：同一问题双模型盲测对比 → 用户投票 → 胜率排行榜（与 Rust arena.rs 契约等价）

import (
	"crypto/rand"
	"math"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// arenaPool 竞技场常驻模型池：免费、对话型（避开 acu 并发限制、非特殊能力模型）
var arenaPool = []string{
	"glm-4-flash", "qwen3-8b", "glm-4-9b-0414", "qwen2-7b-instruct",
	"qwen3-4b", "qwen3-0.6b", "gpt-oss-20b", "gpt-oss-120b",
	"minimax-m3", "mistral-nemotron",
	"deepseek-r1-distill-qwen-7b", "deepseek-r1-distill-qwen-14b",
}

// pickTwoArena 挑两个不同的候选模型：健康优先（近 6h 失败模型排后），过滤已下线
func (a *App) pickTwoArena() (string, string, bool) {
	retired := a.retiredUpstreams()
	health := a.computeHealth()
	var healthy, dead []string
	for _, m := range arenaPool {
		if _, up, ok := a.freeResolve(m); ok && !retired[up] {
			dead = append(dead, m) // 先全放 dead，健康样本好的再挪走
			if h, ok := health[m]; ok {
				total, _ := h["total"].(int64)
				okCnt, _ := h["ok"].(int64)
				if total >= 3 && float64(okCnt)/float64(total) >= 0.5 {
					healthy = append(healthy, m)
					dead = dead[:len(dead)-1]
				}
			}
		}
	}
	// 健康组内洗牌取前二；不足则用无健康数据/失败组补足
	shuffle := func(list []string) {
		for i := len(list) - 1; i > 0; i-- {
			x, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
			j := 0
			if err == nil {
				j = int(x.Int64())
			}
			list[i], list[j] = list[j], list[i]
		}
	}
	shuffle(healthy)
	shuffle(dead)
	pool := append(healthy, dead...)
	if len(pool) < 2 {
		return "", "", false
	}
	return pool[0], pool[1], true
}

// battleOneArena 调单个模型并提取回答（非流式）；返回 (实际命中模型, 回答)
func (a *App) battleOneArena(model, prompt string, maxTokens int64) (string, map[string]any) {
	messages := []map[string]any{{"role": "user", "content": prompt}}
	start := time.Now()
	code, j, used := a.freeDispatchJSON(model, messages, 1.0, maxTokens)
	lat := time.Since(start).Milliseconds()
	if lat < 1 {
		lat = 1
	}
	if code == 0 || code >= 400 || j == nil {
		return "", map[string]any{"content": nil, "latency_ms": lat, "ok": false, "status": code}
	}
	content := jsonPointerStr(j, "choices", "0", "message", "content")
	if content == "" {
		return "", map[string]any{"content": nil, "latency_ms": lat, "ok": false, "status": code}
	}
	return used, map[string]any{"content": content, "latency_ms": lat, "ok": true, "status": code}
}

// handleArena POST /v1/arena —— 发起盲测对决
func (a *App) handleArena(w http.ResponseWriter, r *http.Request) {
	// 无鉴权公开接口：写操作按 IP 限频（与 chat/images 共享防刷滑窗；uid 不可得，投票另有 battle_id 唯一约束兜底）
	if !guard.allow(clientIP(r)) {
		guardReject(w)
		return
	}
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	prompt := strings.TrimSpace(strOf(v, "prompt"))
	if prompt == "" {
		errOut(w, 400, "bad_request", "缺少 prompt 字段：请提供要对比测试的问题文本")
		return
	}
	if len(prompt) > 8000 {
		errOut(w, 400, "bad_request", "prompt 过长（上限 8000 字节），请精简后重试")
		return
	}
	maxTokens := int64(512)
	if f, ok := v["max_tokens"].(float64); ok {
		maxTokens = int64(f)
	}
	maxTokens = clampInt(maxTokens, 16, 2048)
	ma0, mb0, ok := a.pickTwoArena()
	if !ok {
		errOut(w, 503, "service_unavailable", "竞技场暂无可用模型，请稍后重试")
		return
	}
	var ma, mb string
	var ra, rb map[string]any
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ma, ra = a.battleOneArena(ma0, prompt, maxTokens) }()
	go func() { defer wg.Done(); mb, rb = a.battleOneArena(mb0, prompt, maxTokens) }()
	wg.Wait()

	if !boolOf(ra, "ok", false) && !boolOf(rb, "ok", false) {
		errOut(w, 502, "upstream_error", "两位选手都未能返回结果（上游临时故障），请稍后重试")
		return
	}
	// 兜底命中的实际模型写回战场映射（投票揭晓真实身份）
	if ma == "" {
		ma = ma0
	}
	if mb == "" {
		mb = mb0
	}
	battleID := strings.ReplaceAll(newUUID(), "-", "")[:12]
	_, _ = a.DB.Exec(
		"INSERT OR REPLACE INTO arena_battles (id, model_a, model_b, ts) VALUES (?,?,?,?)",
		battleID, ma, mb, time.Now().Unix())
	jsonOut(w, 200, map[string]any{
		"battle_id": battleID,
		"blind":     true,
		"a":         ra,
		"b":         rb,
		"hint":      "对比 A / B 回答后在 /v1/arena/vote 投票（winner: a|b|tie），投票后揭晓模型身份",
	})
}

// handleVote POST /v1/arena/vote —— 盲测投票并揭晓身份
func (a *App) handleVote(w http.ResponseWriter, r *http.Request) {
	// 无鉴权公开接口：写操作按 IP 限频（battle_id 唯一约束只防重复票，防不了刷票脚本）
	if !guard.allow(clientIP(r)) {
		guardReject(w)
		return
	}
	v, ok := readToolJSON(w, r)
	if !ok {
		return
	}
	bid := strings.TrimSpace(strOf(v, "battle_id"))
	if bid == "" {
		errOut(w, 400, "bad_request", "缺少 battle_id 字段（/v1/arena 响应中返回）")
		return
	}
	winner := strOf(v, "winner")
	switch winner {
	case "a", "b", "tie":
	default:
		errOut(w, 400, "bad_request", "winner 取值只能为 a / b / tie（收到："+winner+"）")
		return
	}
	var ma, mb string
	err := a.DB.QueryRow("SELECT model_a, model_b FROM arena_battles WHERE id = ?", bid).Scan(&ma, &mb)
	if err != nil {
		errOut(w, 404, "not_found", "battle_id 不存在或已过期（战场映射保留 7 天），请重新发起对决")
		return
	}
	res, err := a.DB.Exec(
		"INSERT OR IGNORE INTO arena_votes (battle_id, model_a, model_b, winner, ts) VALUES (?,?,?,?,?)",
		bid, ma, mb, winner, time.Now().Unix())
	if err != nil {
		errOut(w, 500, "internal_error", "投票保存失败")
		return
	}
	revealed := map[string]string{"a": ma, "b": mb}
	if n, _ := res.RowsAffected(); n == 1 {
		jsonOut(w, 200, map[string]any{"counted": true, "winner": winner, "revealed": revealed})
		return
	}
	jsonOut(w, 200, map[string]any{
		"counted": false, "msg": "该 battle_id 已投过票，一票一场", "revealed": revealed,
	})
}

// handleLeaderboard GET /v1/arena/leaderboard —— 胜率排行榜
func (a *App) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query("SELECT model_a, model_b, winner FROM arena_votes")
	if err != nil {
		errOut(w, 503, "service_unavailable", "数据库繁忙，请稍后重试")
		return
	}
	type st struct{ w, l, t int64 }
	standings := map[string]*st{}
	totalVotes := int64(0)
	for rows.Next() {
		var ma, mb, winner string
		if err := rows.Scan(&ma, &mb, &winner); err != nil {
			continue
		}
		totalVotes++
		for _, m := range []string{ma, mb} {
			if standings[m] == nil {
				standings[m] = &st{}
			}
		}
		switch winner {
		case "a":
			standings[ma].w++
			standings[mb].l++
		case "b":
			standings[mb].w++
			standings[ma].l++
		default:
			standings[ma].t++
			standings[mb].t++
		}
	}
	rows.Close()

	type rank struct {
		model              string
		wins, losses, ties int64
		battles            int64
		winRate            float64
	}
	list := make([]rank, 0, len(standings))
	for m, s := range standings {
		battles := s.w + s.l + s.t
		score := float64(s.w) + float64(s.t)*0.5
		wr := 0.0
		if battles > 0 {
			wr = math.Round(score*100.0/float64(battles)*10) / 10
		}
		list = append(list, rank{m, s.w, s.l, s.t, battles, wr})
	}
	sort.Slice(list, func(i, j int) bool {
		si := float64(list[i].wins) + float64(list[i].ties)*0.5
		sj := float64(list[j].wins) + float64(list[j].ties)*0.5
		if si != sj {
			return si > sj
		}
		return list[i].battles > list[j].battles
	})
	ranking := make([]map[string]any, 0, len(list))
	for _, it := range list {
		ranking = append(ranking, map[string]any{
			"model": it.model, "wins": it.wins, "losses": it.losses, "ties": it.ties,
			"battles": it.battles, "win_rate": it.winRate,
		})
	}
	jsonOut(w, 200, map[string]any{
		"total_votes": totalVotes,
		"ranking":     ranking,
		"rule":        "胜 1 分 / 平 0.5 分 / 负 0 分，按积分排序",
	})
}
