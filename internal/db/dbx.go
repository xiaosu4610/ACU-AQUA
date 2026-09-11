package db

import "database/sql"

// DBx 包装（后续可扩展为 postgres 实现同接口）
type DBx struct {
	*sql.DB
}
