package billing

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newSettleDB(t *testing.T, balance int64) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	schema := `
	CREATE TABLE users (id INTEGER PRIMARY KEY, status INTEGER NOT NULL DEFAULT 1, balance_micro INTEGER NOT NULL DEFAULT 0);
	CREATE TABLE balance_flows (user_id INTEGER, request_id INTEGER, type TEXT, amount_micro INTEGER,
		balance_before_micro INTEGER, balance_after_micro INTEGER, unit_price_micro INTEGER, note TEXT, operator TEXT, ts INTEGER);`
	if _, err := d.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO users (id, status, balance_micro) VALUES (1, 1, ?)", balance); err != nil {
		t.Fatal(err)
	}
	return d
}

func userBalance(t *testing.T, d *sql.DB) int64 {
	t.Helper()
	var b int64
	if err := d.QueryRow("SELECT balance_micro FROM users WHERE id=1").Scan(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSettle_RefundExactAndShortfall(t *testing.T) {
	// ① 退回：B0=10000，预扣 5000 → 5000；实扣 3000 → 退回 2000 → 7000（= 10000-3000，分毫不差）
	d := newSettleDB(t, 10_000)
	if err := Prehold(d, 1, 5_000, 1); err != nil {
		t.Fatal(err)
	}
	if b := userBalance(t, d); b != 5_000 {
		t.Fatalf("预扣后余额: 期望 5000, 实得 %d", b)
	}
	if err := Settle(d, 1, 5_000, 3_000, 1, 0, "billed"); err != nil {
		t.Fatal(err)
	}
	if b := userBalance(t, d); b != 7_000 {
		t.Fatalf("退回后余额: 期望 7000, 实得 %d", b)
	}

	// ② 补扣（余额充足）：预扣 3000（7000→4000），实扣 4800 → 追扣 1800 → 2200（= 7000-4800）
	if err := Prehold(d, 1, 3_000, 2); err != nil {
		t.Fatal(err)
	}
	if err := Settle(d, 1, 3_000, 4_800, 2, 0, "billed"); err != nil {
		t.Fatal(err)
	}
	if b := userBalance(t, d); b != 2_200 {
		t.Fatalf("补扣后余额: 期望 2200, 实得 %d", b)
	}

	// ③ 补扣（余额不足）：预扣 1000（2200→1200），实扣 9000 → 追扣 1200 扣光为止（实收 2200），
	//    差额 6800 由站方兜底并经 note shortfall 留痕——绝不产生负余额（政策红线）
	if err := Prehold(d, 1, 1_000, 3); err != nil {
		t.Fatal(err)
	}
	if err := Settle(d, 1, 1_000, 9_000, 3, 0, "billed"); err != nil {
		t.Fatal(err)
	}
	if b := userBalance(t, d); b != 0 {
		t.Fatalf("余额不足补扣后余额: 期望 0（绝不透支）, 实得 %d", b)
	}
	var note string
	if err := d.QueryRow("SELECT note FROM balance_flows WHERE request_id=3 AND type='billed'").Scan(&note); err != nil {
		t.Fatal(err)
	}
	if want := "billed|shortfall=6800"; note != want {
		t.Fatalf("shortfall 留痕: 期望 %q, 实得 %q", want, note)
	}

	// ④ 失败全额退回：充值 5000 → 预扣 2000 → final=0 全退 → 余额 5000
	if _, err := d.Exec("UPDATE users SET balance_micro=5000 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := Prehold(d, 1, 2_000, 4); err != nil {
		t.Fatal(err)
	}
	if err := Settle(d, 1, 2_000, 0, 4, 0, "stream_incomplete"); err != nil {
		t.Fatal(err)
	}
	if b := userBalance(t, d); b != 5_000 {
		t.Fatalf("失败退回后余额: 期望 5000, 实得 %d", b)
	}
}
