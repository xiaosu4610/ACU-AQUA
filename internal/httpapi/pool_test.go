// 众筹池回归（20260921 恢复上线）：划拨是资金操作，锁死三条不变量——
// ① 余额不足绝不扣（条件更新，两侧流水都不产生）
// ② 个人出账与池子入账在同一事务（绝不出现只扣一边）
// ③ crowd 请求从**公共池**扣账、不扣个人余额
package httpapi

import (
	"testing"
	"time"

	"acu-aqua/gateway/internal/auth"
	"acu-aqua/gateway/internal/config"
)

// seedPoolUser 建测试用户并返回 (uid, 会话 token)
func seedPoolUser(t *testing.T, app *App, balance int64) (int64, string) {
	t.Helper()
	res, err := app.DB.Exec(`INSERT INTO users (username, email, password_hash, status, created_ts, balance_micro)
		VALUES (?,?,?,1,?,?)`, "pooltest", "pooltest@example.org", "x", time.Now().Unix(), balance)
	if err != nil {
		t.Fatalf("建测试用户失败: %v", err)
	}
	uid, _ := res.LastInsertId()
	tok, err := auth.CreateUserSession(app.DB.DB, uid)
	if err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	return uid, tok
}

func poolBalanceOf(t *testing.T, app *App) int64 {
	t.Helper()
	var b int64
	_ = app.DB.QueryRow("SELECT balance_micro FROM pool_wallet WHERE id=?", poolID).Scan(&b)
	return b
}

func userBalanceOf(t *testing.T, app *App, uid int64) int64 {
	t.Helper()
	var b int64
	_ = app.DB.QueryRow("SELECT balance_micro FROM users WHERE id=?", uid).Scan(&b)
	return b
}

func countRows(t *testing.T, app *App, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := app.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("计数查询失败: %v", err)
	}
	return n
}

// 划拨成功：个人余额等额减少、池子等额增加、双方流水各一条
func TestPoolTransfer_HappyPath(t *testing.T) {
	app, _, _ := newTestApp(t)
	uid, tok := seedPoolUser(t, app, 5_000_000)

	rec, out := doJSON(t, app.Routes(), "POST", "/v1/my/pool/transfer", tok, map[string]any{"amount_micro": 2_000_000})
	if rec.Code != 200 {
		t.Fatalf("划拨应 200，得 %d：%v", rec.Code, out)
	}
	if got := userBalanceOf(t, app, uid); got != 3_000_000 {
		t.Fatalf("个人余额应 3_000_000，得 %d", got)
	}
	if got := poolBalanceOf(t, app); got != 2_000_000 {
		t.Fatalf("池子余额应 2_000_000，得 %d", got)
	}
	if n := countRows(t, app, "SELECT COUNT(*) FROM balance_flows WHERE user_id=? AND type='pool_transfer' AND amount_micro=-2000000", uid); n != 1 {
		t.Fatalf("个人出账流水应有 1 条 -2000000，得 %d", n)
	}
	if n := countRows(t, app, "SELECT COUNT(*) FROM pool_flows WHERE user_id=? AND type='charge' AND amount_micro=2000000", uid); n != 1 {
		t.Fatalf("池子入账流水应有 1 条 +2000000，得 %d", n)
	}
}

// 余额不足：必须 429，且**两侧都不产生任何变动**（条件更新 + 回滚）
func TestPoolTransfer_InsufficientBalance(t *testing.T) {
	app, _, _ := newTestApp(t)
	uid, tok := seedPoolUser(t, app, 500_000)

	rec, _ := doJSON(t, app.Routes(), "POST", "/v1/my/pool/transfer", tok, map[string]any{"amount_micro": 1_000_000})
	if rec.Code != 429 {
		t.Fatalf("余额不足应 429，得 %d", rec.Code)
	}
	if got := userBalanceOf(t, app, uid); got != 500_000 {
		t.Fatalf("个人余额不应变动，得 %d", got)
	}
	if got := poolBalanceOf(t, app); got != 0 {
		t.Fatalf("池子不应变动，得 %d", got)
	}
	if n := countRows(t, app, "SELECT COUNT(*) FROM balance_flows WHERE user_id=? AND type='pool_transfer'", uid); n != 0 {
		t.Fatalf("不应产生个人流水，得 %d 条", n)
	}
	if n := countRows(t, app, "SELECT COUNT(*) FROM pool_flows WHERE user_id=? AND type='charge'", uid); n != 0 {
		t.Fatalf("不应产生池子流水，得 %d 条", n)
	}
}

// 低于下限（¥1）拒绝，且不动任何余额
func TestPoolTransfer_BelowMinimum(t *testing.T) {
	app, _, _ := newTestApp(t)
	uid, tok := seedPoolUser(t, app, 5_000_000)

	rec, _ := doJSON(t, app.Routes(), "POST", "/v1/my/pool/transfer", tok, map[string]any{"amount_micro": 100_000})
	if rec.Code != 400 {
		t.Fatalf("低于下限应 400，得 %d", rec.Code)
	}
	if got := userBalanceOf(t, app, uid); got != 5_000_000 {
		t.Fatalf("个人余额不应变动，得 %d", got)
	}
}

// 未登录必须 401（资金接口绝不能匿名可达）
func TestPoolTransfer_RequiresAuth(t *testing.T) {
	app, _, _ := newTestApp(t)
	rec, _ := doJSON(t, app.Routes(), "POST", "/v1/my/pool/transfer", "", map[string]any{"amount_micro": 2_000_000})
	if rec.Code != 401 {
		t.Fatalf("未登录应 401，得 %d", rec.Code)
	}
}

// 池子闸门：空池拒绝（403 crowd_pool_empty）
func TestPoolGate_EmptyPool(t *testing.T) {
	app, _, _ := newTestApp(t)
	st, code, _ := app.poolGate(1)
	if st != 403 || code != "crowd_pool_empty" {
		t.Fatalf("空池应 403/crowd_pool_empty，得 %d/%s", st, code)
	}
	// 注资后放行
	tx, err := app.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := poolChargeTx(tx, 1, 10_000_000, "余额划拨"); err != nil {
		t.Fatalf("池子入账失败: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if st, code, _ := app.poolGate(1); st != 0 {
		t.Fatalf("注资后应放行，得 %d/%s", st, code)
	}
}

// crowd 结算：从池子扣账，个人余额分文不动
func TestSettleFor_CrowdChargesPoolNotPersonal(t *testing.T) {
	app, _, _ := newTestApp(t)
	uid, _ := seedPoolUser(t, app, 9_000_000)

	tx, err := app.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := poolChargeTx(tx, uid, 10_000_000, "余额划拨"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rid := app.insertRequestLine(uid, "h", "chat", "acu/m1", false, "acu")

	line := &config.Line{ID: "acu", Mode: "crowd"}
	app.settleFor(line, uid, 0, 5800, rid, 5800, "billed")

	if got := userBalanceOf(t, app, uid); got != 9_000_000 {
		t.Fatalf("crowd 线不得扣个人余额，应 9_000_000，得 %d", got)
	}
	if got := poolBalanceOf(t, app); got != 10_000_000-5800 {
		t.Fatalf("池子应扣 5800，得 %d", got)
	}
	if n := countRows(t, app, "SELECT COUNT(*) FROM pool_flows WHERE request_id=? AND type='consume' AND amount_micro=-5800", rid); n != 1 {
		t.Fatalf("池子应有 1 条 -5800 消费流水，得 %d", n)
	}
}

// poolConsume 正常路径：扣池一次、流水一条、不落错误中心
func TestPoolConsume_ChargesOnceWithoutError(t *testing.T) {
	app, _, _ := newTestApp(t)
	uid, _ := seedPoolUser(t, app, 0)
	tx, err := app.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := poolChargeTx(tx, uid, 1_000_000, "余额划拨"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rid := app.insertRequestLine(uid, "h", "chat", "acu/m1", false, "acu")
	app.poolConsume(uid, 1000, rid, "acu/m1")

	if n := countRows(t, app, "SELECT COUNT(*) FROM error_events WHERE kind='pool_settle_failed'"); n != 0 {
		t.Fatalf("正常路径不应落错误中心，得 %d 条", n)
	}
	if got := poolBalanceOf(t, app); got != 1_000_000-1000 {
		t.Fatalf("池子应扣 1000，得 %d", got)
	}
	if n := countRows(t, app, "SELECT COUNT(*) FROM pool_flows WHERE request_id=? AND type='consume'", rid); n != 1 {
		t.Fatalf("应有且仅有 1 条消费流水，得 %d", n)
	}
}
