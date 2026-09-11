// Package db SQLite 接入与建表迁移。schema 与 Rust 版完全兼容：
// Go 版可直接打开现有生产库，用户 API Key / 管理会话无缝继续有效。
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open 打开 SQLite（WAL 模式，单写者纪律与 Rust 版一致）
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(0)", path)
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 单写者：限制为 1 个连接避免 SQLITE_BUSY 竞态
	d.SetMaxOpenConns(1)
	if err := d.Ping(); err != nil {
		return nil, err
	}
	return d, nil
}

// InitTables 建表 + 幂等迁移（与 Rust 版 init_tables 对齐）
func InitTables(d *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL DEFAULT '',
			avatar_ext TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL DEFAULT 1,
			created_ts INTEGER NOT NULL,
			last_login_ts INTEGER NOT NULL DEFAULT 0,
			balance_micro INTEGER NOT NULL DEFAULT 0,
			alert_threshold_micro INTEGER NOT NULL DEFAULT 0,
			alert_armed INTEGER NOT NULL DEFAULT 1,
			alert_last_ts INTEGER NOT NULL DEFAULT 0,
			price_grp TEXT NOT NULL DEFAULT 'normal',
			price_grp_call TEXT NOT NULL DEFAULT 'normal',
			price_grp_token TEXT NOT NULL DEFAULT 'normal')`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			key_hash TEXT NOT NULL UNIQUE,
			key_prefix TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			revoked INTEGER NOT NULL DEFAULT 0,
			created_ts INTEGER NOT NULL,
			key_plain_enc TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			created_ts INTEGER NOT NULL,
			last_seen_ts INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS admin_sessions (
			token TEXT PRIMARY KEY,
			created_ts INTEGER NOT NULL,
			last_seen_ts INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS balance_flows (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			request_id INTEGER NOT NULL DEFAULT 0,
			type TEXT NOT NULL,
			amount_micro INTEGER NOT NULL,
			balance_before_micro INTEGER NOT NULL,
			balance_after_micro INTEGER NOT NULL,
			unit_price_micro INTEGER NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT '',
			operator TEXT NOT NULL DEFAULT 'system',
			ts INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_bflows_uid_ts ON balance_flows(user_id, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_bflows_req ON balance_flows(request_id)`,
		`CREATE INDEX IF NOT EXISTS idx_bflows_type_ts ON balance_flows(type, ts)`,
		`CREATE TABLE IF NOT EXISTS requests (
			rowid_ INTEGER PRIMARY KEY AUTOINCREMENT,
			key_hash TEXT NOT NULL DEFAULT '',
			endpoint TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			ok INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			ts INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			tps REAL NOT NULL DEFAULT 0,
			status_code INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			usage_source TEXT NOT NULL DEFAULT '',
			billed INTEGER NOT NULL DEFAULT 0,
			bill_amount_micro INTEGER NOT NULL DEFAULT 0,
			unit_price_micro INTEGER NOT NULL DEFAULT 0,
			balance_after_micro INTEGER NOT NULL DEFAULT 0,
			bill_state TEXT NOT NULL DEFAULT '',
			stream_mode INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS pricing (
			model TEXT NOT NULL,
			price_micro INTEGER NOT NULL,
			starts_at INTEGER NOT NULL,
			ends_at INTEGER,
			note TEXT NOT NULL DEFAULT '',
			grp TEXT NOT NULL DEFAULT 'normal',
			mode TEXT NOT NULL DEFAULT 'per_call',
			floor_micro INTEGER NOT NULL DEFAULT 1000,
			in_rate10 INTEGER NOT NULL DEFAULT 0,
			cache_rate10 INTEGER NOT NULL DEFAULT 0,
			out_rate10 INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (model, grp, starts_at))`,
		`CREATE TABLE IF NOT EXISTS line_keys (
			line_id TEXT NOT NULL,
			idx INTEGER NOT NULL,
			initial_micro INTEGER NOT NULL DEFAULT 0,
			used_micro INTEGER NOT NULL DEFAULT 0,
			dead INTEGER NOT NULL DEFAULT 0,
			updated_ts INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (line_id, idx))`,
		`CREATE TABLE IF NOT EXISTS admin_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			action TEXT NOT NULL,
			target TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			ts INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS payments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			amount_micro INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			out_trade_no TEXT NOT NULL DEFAULT '',
			created_ts INTEGER NOT NULL,
			paid_ts INTEGER NOT NULL DEFAULT 0)`,
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return fmt.Errorf("建表失败: %w\nSQL: %s", err, s)
		}
	}
	// 幂等 ALTER：为旧库补列（Rust 版上线过程中逐步加过的列）
	alters := []struct{ table, col, def string }{
		{"requests", "bill_state", "TEXT NOT NULL DEFAULT ''"},
		{"requests", "stream_mode", "INTEGER NOT NULL DEFAULT 0"},
		{"requests", "face_cost_micro", "INTEGER NOT NULL DEFAULT 0"},
		{"users", "price_grp", "TEXT NOT NULL DEFAULT 'normal'"},
		{"users", "price_grp_call", "TEXT NOT NULL DEFAULT 'normal'"},
		{"users", "price_grp_token", "TEXT NOT NULL DEFAULT 'normal'"},
		{"sessions", "last_seen_ts", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, a := range alters {
		var n int
		d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?)`, a.table).Scan(&n)
		_ = n // pragma_table_info 传参在 sqlite 驱动里不可用，改用下方逐列探测
		has := false
		rows, err := d.Query(fmt.Sprintf("PRAGMA table_info(%s)", a.table))
		if err == nil {
			for rows.Next() {
				var cid int
				var name, ctype string
				var notnull, pk int
				var dflt sql.NullString
				if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil && name == a.col {
					has = true
				}
			}
			rows.Close()
		}
		if !has {
			if _, err := d.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", a.table, a.col, a.def)); err != nil {
				return fmt.Errorf("补列失败 %s.%s: %w", a.table, a.col, err)
			}
		}
	}
	return nil
}
