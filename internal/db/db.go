// Package db SQLite 接入与建表迁移。schema 与 Rust 版完全兼容：
// Go 版可直接打开现有生产库，用户 API Key / 管理会话无缝继续有效。
package db

import (
	"database/sql"
	"fmt"
	"log"
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
		// —— 众筹池（acu/ 公共算力池：与个人余额物理隔离）——
		`CREATE TABLE IF NOT EXISTS pool_wallet (
			id            TEXT PRIMARY KEY,
			balance_micro INTEGER NOT NULL DEFAULT 0,
			charged_micro INTEGER NOT NULL DEFAULT 0,
			used_micro    INTEGER NOT NULL DEFAULT 0,
			updated_ts    INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS pool_flows (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id       INTEGER NOT NULL DEFAULT 0,
			type          TEXT NOT NULL,
			amount_micro  INTEGER NOT NULL,
			balance_after INTEGER NOT NULL,
			revival       INTEGER NOT NULL DEFAULT 0,
			request_id    INTEGER NOT NULL DEFAULT 0,
			note          TEXT NOT NULL DEFAULT '',
			ts            INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS admin_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			action TEXT NOT NULL,
			target TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			prev_hash TEXT NOT NULL DEFAULT 'GENESIS',
			self_hash TEXT NOT NULL DEFAULT '',
			ts INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS payments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			amount_micro INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			out_trade_no TEXT NOT NULL DEFAULT '',
			channel TEXT NOT NULL DEFAULT '',
			trade_no TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			created_ts INTEGER NOT NULL,
			paid_ts INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS email_codes (
			email TEXT NOT NULL,
			purpose TEXT NOT NULL,
			code TEXT NOT NULL,
			fails INTEGER NOT NULL DEFAULT 0,
			expire_ts INTEGER NOT NULL,
			PRIMARY KEY (email, purpose))`,
		// —— 免费线三表（与 Rust 版同构，生产库已存在，此处仅兜底新建）——
		`CREATE TABLE IF NOT EXISTS model_health (
			model TEXT NOT NULL,
			ts INTEGER NOT NULL,
			ok INTEGER NOT NULL,
			err_type TEXT NOT NULL DEFAULT '',
			status_code INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0)`,
		`CREATE INDEX IF NOT EXISTS idx_mhealth_model_ts ON model_health(model, ts)`,
		`CREATE TABLE IF NOT EXISTS retired_models (
			model TEXT PRIMARY KEY,
			retired_ts INTEGER NOT NULL,
			hits INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS nvidia_models (
			id TEXT PRIMARY KEY,
			upstream_id TEXT NOT NULL DEFAULT '',
			ts INTEGER NOT NULL)`,
		// —— 工具/竞技场五表（与 Rust 版同构，生产库已存在，此处仅兜底新建）——
		`CREATE TABLE IF NOT EXISTS arena_battles (
			id TEXT PRIMARY KEY, model_a TEXT NOT NULL, model_b TEXT NOT NULL, ts INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS arena_votes (
			battle_id TEXT UNIQUE NOT NULL, model_a TEXT NOT NULL, model_b TEXT NOT NULL,
			winner TEXT NOT NULL, ts INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS shortlinks (
			code TEXT PRIMARY KEY, url TEXT NOT NULL, hits INTEGER NOT NULL DEFAULT 0,
			created_ts INTEGER NOT NULL, last_hit_ts INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS hooks (id TEXT PRIMARY KEY, created_ts INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS hook_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT, hook_id TEXT NOT NULL, method TEXT NOT NULL,
			path TEXT DEFAULT '', headers TEXT DEFAULT '{}', body BLOB, ts INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_hook_requests ON hook_requests(hook_id, ts)`,
		// —— 管理后台：上游额度池（stats/quota/supervision 数据源，与 Rust 版同构）——
		`CREATE TABLE IF NOT EXISTS upstream_quota (
			provider TEXT PRIMARY KEY,
			total_calls INTEGER NOT NULL DEFAULT 0,
			used_calls INTEGER NOT NULL DEFAULT 0,
			cost_per_call_micro INTEGER NOT NULL DEFAULT 0,
			circuit_open INTEGER NOT NULL DEFAULT 0,
			updated_ts INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS upstream_topups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider TEXT NOT NULL DEFAULT 'pool',
			amount_micro INTEGER NOT NULL DEFAULT 0,
			calls_added INTEGER NOT NULL DEFAULT 0,
			total_before INTEGER NOT NULL DEFAULT 0,
			total_after INTEGER NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT '',
			ts INTEGER NOT NULL)`,
		// —— 上游线路管理（管理后台在线 CRUD，DB 为唯一事实源；config toml 仅为首次引导种子）——
		`CREATE TABLE IF NOT EXISTS admin_lines (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT 'free',
			base_url TEXT NOT NULL DEFAULT '',
			auth_style TEXT NOT NULL DEFAULT '',
			proxy TEXT NOT NULL DEFAULT '',
			vip_num INTEGER NOT NULL DEFAULT 0,
			vip_den INTEGER NOT NULL DEFAULT 0,
			key_face_micro INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			dynamic INTEGER NOT NULL DEFAULT 0,
			updated_ts INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS admin_line_keys (
			line_id TEXT NOT NULL,
			idx INTEGER NOT NULL,
			key TEXT NOT NULL,
			dead INTEGER NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT '',
			updated_ts INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (line_id, idx))`,
		`CREATE TABLE IF NOT EXISTS admin_line_models (
			line_id TEXT NOT NULL,
			site_id TEXT NOT NULL,
			upstream_id TEXT NOT NULL DEFAULT '',
			image INTEGER NOT NULL DEFAULT 0,
			per_call_sell INTEGER NOT NULL DEFAULT 0,
			per_call_cost INTEGER NOT NULL DEFAULT 0,
			per_image_sell INTEGER NOT NULL DEFAULT 0,
			per_image_cost INTEGER NOT NULL DEFAULT 0,
			in_sell_rate10 INTEGER NOT NULL DEFAULT 0,
			cache_sell_rate10 INTEGER NOT NULL DEFAULT 0,
			out_sell_rate10 INTEGER NOT NULL DEFAULT 0,
			in_cost_rate10 INTEGER NOT NULL DEFAULT 0,
			cache_cost_rate10 INTEGER NOT NULL DEFAULT 0,
			out_cost_rate10 INTEGER NOT NULL DEFAULT 0,
			degraded INTEGER NOT NULL DEFAULT 0,
			updated_ts INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (line_id, site_id))`,
		// —— 错误事件表（结算/计费异常追踪，settle.go logError 落库用）——
		`CREATE TABLE IF NOT EXISTS error_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			user_id INTEGER NOT NULL DEFAULT 0,
			request_id INTEGER NOT NULL DEFAULT 0,
			detail TEXT NOT NULL DEFAULT '',
			ts INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_eevents_ts ON error_events(ts)`,
		`CREATE INDEX IF NOT EXISTS idx_eevents_kind ON error_events(kind, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_requests_ts ON requests(ts)`, // 模型实时状态 30 分钟窗口聚合
		`CREATE INDEX IF NOT EXISTS idx_requests_uid_billed_ts ON requests(user_id, billed, ts)`, // 财务中心消费统计
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
		{"payments", "channel", "TEXT NOT NULL DEFAULT ''"},
		{"payments", "trade_no", "TEXT NOT NULL DEFAULT ''"},
		{"payments", "ip", "TEXT NOT NULL DEFAULT ''"},
		{"payments", "fee_micro", "INTEGER NOT NULL DEFAULT 0"},
		{"requests", "tide_face_micro", "INTEGER NOT NULL DEFAULT 0"},
		{"admin_audit", "prev_hash", "TEXT NOT NULL DEFAULT 'GENESIS'"},
		{"admin_audit", "self_hash", "TEXT NOT NULL DEFAULT ''"},
		{"api_keys", "billing_grp", "TEXT NOT NULL DEFAULT ''"},   // 密钥计费分组：''|per_call|per_token
		{"requests", "resolved_line", "TEXT NOT NULL DEFAULT ''"}, // 统一前缀路由解析出的实际线（统计口径）
		{"admin_line_models", "degraded", "INTEGER NOT NULL DEFAULT 0"}, // 降级标记（诊断 D4：上游故障手动标记）
		{"admin_line_models", "key_idx", "INTEGER NOT NULL DEFAULT -1"}, // 模型专属密钥池序（-1=自动走粘性池；≥0 锁定非 dead 钥排序后的第 N 把，按模型分工钥）
		{"payments", "product", "TEXT NOT NULL DEFAULT 'balance'"},      // 支付产品分流：balance=个人余额充值 / pool=众筹池充值
		{"admin_lines", "dynamic", "INTEGER NOT NULL DEFAULT 0"},        // 免费线动态目录开关（NVIDIA 自动同步上游新模型到 nvidia_models）
		{"admin_lines", "auth_style", "TEXT NOT NULL DEFAULT ''"},       // 鉴权风格：''=bearer | x-api-key | codex（ChatGPT 账号池 RT→AT+协议转换）
		{"admin_lines", "proxy", "TEXT NOT NULL DEFAULT ''"},            // 线路级出站代理（http:// 或 socks5://，空=直连；gpt 线走本机 sing-box）
		{"requests", "key_idx", "INTEGER NOT NULL DEFAULT -1"},          // 实际使用的钥池序（codex 账号粒度用量/利润记账；-1=非钥池线或失败请求）
		{"admin_line_keys", "used_pct", "REAL NOT NULL DEFAULT -1"},     // codex 官方实时用量百分比（响应头 x-codex-primary-used-percent，-1=未知）
		{"admin_line_keys", "reset_at", "INTEGER NOT NULL DEFAULT 0"},   // codex 官方用量窗口重置时间（unix 秒）
		{"admin_line_keys", "plan_type", "TEXT NOT NULL DEFAULT ''"},    // codex 账号套餐（free/pro；响应头 x-codex-plan-type，周限号在此辨析）
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
	// admin_audit 列名统一：旧迁移库列名是 target_user，代码统一用 target（SQLite 3.25+ RENAME）
	{
		hasOld, hasNew := false, false
		rows, err := d.Query("PRAGMA table_info(admin_audit)")
		if err == nil {
			for rows.Next() {
				var cid int
				var name, ctype string
				var notnull, pk int
				var dflt sql.NullString
				if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil {
					switch name {
					case "target_user":
						hasOld = true
					case "target":
						hasNew = true
					}
				}
			}
			rows.Close()
		}
		if hasOld && !hasNew {
			if _, err := d.Exec("ALTER TABLE admin_audit RENAME COLUMN target_user TO target"); err != nil {
				return fmt.Errorf("admin_audit 列名统一失败: %w", err)
			}
			log.Printf("[db] admin_audit.target_user 已统一为 target")
		}
	}
	return nil
}
