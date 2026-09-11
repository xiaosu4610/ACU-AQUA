package billing

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrInsufficientBalance 余额不足（429 insufficient_quota 口径）
var ErrInsufficientBalance = errors.New("INSUFFICIENT_BALANCE")

// tx 事务包装：SQLite busy/locked（与 Rust 版双进程共库时偶发）自动退避重试
func tx(d *sql.DB, fn func(*sql.Tx) error) error {
	var err error
	for i := 0; i < 5; i++ {
		t, e := d.Begin()
		if e != nil {
			err = e
		} else {
			e = fn(t)
			if e != nil {
				_ = t.Rollback()
				err = e
			} else {
				err = t.Commit()
			}
		}
		if err == nil {
			return nil
		}
		s := strings.ToLower(err.Error())
		if strings.Contains(s, "busy") || strings.Contains(s, "locked") {
			time.Sleep(time.Duration(30*(i+1)) * time.Millisecond)
			continue
		}
		return err
	}
	return err
}
