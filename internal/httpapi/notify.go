package httpapi

// 全员邮件通知（管理后台）：按量计费切换公告等站点级通知。
// 逐用户单独发信（To 头只含收件人本人，防地址泄露）；后台协程限速发送，
// 进度可查询；并发互斥（同一时刻仅一个发送任务）。

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// mailJob 群发任务运行态（内存态，进程重启即清——群发为一次性运营动作）
type mailJob struct {
	mu      sync.Mutex
	running atomic.Bool
	subject string
	total   int64
	sent    int64
	failed  int64
	started time.Time
	errs    []string // 前 20 条失败样本
}

var notifyJob mailJob

// —— POST /v1/admin/notify/mail {subject, body, confirm_password, throttle_ms?} ——
func (a *App) handleAdminNotifyMail(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if a.Mail == nil && (a.MailPool == nil || a.MailPool.activeCount(a.DB.DB) == 0) {
		errAdmin(w, 503, "mail_unconfigured", "SMTP 未配置，无法发送邮件")
		return
	}
	var req struct {
		Subject         string `json:"subject"`
		Body            string `json:"body"`
		ConfirmPassword string `json:"confirm_password"`
		ThrottleMs      int64  `json:"throttle_ms"`
		// UserIDs 定向收件人（可空=全员）。用于事故补偿等**只通知受影响用户**的场景——
		// 20260922 免费模型误扣退款只涉及 22 人，不该为少数人给全站 1500+ 用户发信。
		UserIDs []int64 `json:"user_ids"`
	}
	if err := adminBody(r, 64<<10, &req); err != nil {
		errAdmin(w, 400, "bad_request", "请求体格式错误")
		return
	}
	req.Subject = strings.TrimSpace(req.Subject)
	if req.Subject == "" || len(req.Body) < 10 {
		errAdmin(w, 400, "bad_request", "subject 与 body（≥10 字符）必填")
		return
	}
	if len(req.Subject) > 200 || len(req.Body) > 20<<10 {
		errAdmin(w, 400, "bad_request", "subject ≤200 字符，body ≤20KB")
		return
	}
	if !a.confirmOK(w, r, "notify_mail", req.ConfirmPassword) {
		return
	}
	if !notifyJob.running.CompareAndSwap(false, true) {
		errAdmin(w, 409, "already_running", "已有发送任务进行中，请先查询进度")
		return
	}

	// 收件人：默认全部正常状态用户；传了 user_ids 则只发给指定用户
	// （email 唯一非空列，双重过滤防御）
	rcptSQL := "SELECT email FROM users WHERE status=1 AND email LIKE '%@%'"
	var rcptArgs []any
	if len(req.UserIDs) > 0 {
		if len(req.UserIDs) > 5000 {
			notifyJob.running.Store(false)
			errAdmin(w, 400, "bad_request", "user_ids 上限 5000 个")
			return
		}
		ph := make([]string, 0, len(req.UserIDs))
		for _, id := range req.UserIDs {
			ph = append(ph, "?")
			rcptArgs = append(rcptArgs, id)
		}
		rcptSQL += " AND id IN (" + strings.Join(ph, ",") + ")"
	}
	rows, err := a.DB.Query(rcptSQL, rcptArgs...)
	if err != nil {
		notifyJob.running.Store(false)
		errAdmin(w, 500, "internal_error", "收件人查询失败")
		return
	}
	var rcpts []string
	for rows.Next() {
		var e string
		if rows.Scan(&e) == nil && strings.Contains(e, "@") {
			rcpts = append(rcpts, e)
		}
	}
	rows.Close()
	if len(rcpts) == 0 {
		notifyJob.running.Store(false)
		errAdmin(w, 400, "no_recipients", "无可用收件人")
		return
	}

	throttle := req.ThrottleMs
	if throttle <= 0 {
		throttle = 1500 // 默认 1.5s/封：微软池单号 60s 冷却 × 轮询的全局安全吞吐（1337 封约 33 分钟）
	}

	notifyJob.mu.Lock()
	notifyJob.subject = req.Subject
	notifyJob.total = int64(len(rcpts))
	notifyJob.sent = 0
	notifyJob.failed = 0
	notifyJob.errs = notifyJob.errs[:0]
	notifyJob.started = time.Now()
	notifyJob.mu.Unlock()

	body := req.Body
	subject := req.Subject
	// 微软池轮询摊量（站长定稿）：群发全走 SendAny（选择器 last_ok_ts 最早优先=轮询；每号 60s 冷却+日限 40 天然摊量）
	sendOne := func(to, subj, body string) error {
		_, _, err := a.SendAny(to, subj, body)
		return err
	}
	go func() {
		defer notifyJob.running.Store(false)
		for i, to := range rcpts {
			if err := sendOne(to, subject, body); err != nil {
				notifyJob.mu.Lock()
				notifyJob.failed++
				if len(notifyJob.errs) < 20 {
					notifyJob.errs = append(notifyJob.errs, fmt.Sprintf("%s: %v", to, err))
				}
				notifyJob.mu.Unlock()
				log.Printf("[notify] 邮件发送失败 (%d/%d) to=%s: %v", i+1, len(rcpts), to, err)
			} else {
				notifyJob.sent++
			}
			if throttle > 0 && i < len(rcpts)-1 {
				time.Sleep(time.Duration(throttle) * time.Millisecond)
			}
		}
		log.Printf("[notify] 全员邮件发送完成：成功 %d / 失败 %d / 总数 %d（%s）",
			notifyJob.sent, notifyJob.failed, notifyJob.total, subject)
	}()

	a.auditAppend("notify_mail", 0, fmt.Sprintf("subject=%q recipients=%d", subject, len(rcpts)), clientIP(r))
	adminJSON(w, map[string]any{"ok": true, "total": len(rcpts), "throttle_ms": throttle,
		"message": "发送任务已启动，GET /v1/admin/notify/mail/status 查询进度"})
}

// —— GET /v1/admin/notify/mail/status ——
func (a *App) handleAdminNotifyMailStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	notifyJob.mu.Lock()
	defer notifyJob.mu.Unlock()
	adminJSON(w, map[string]any{
		"running": notifyJob.running.Load(), "subject": notifyJob.subject,
		"total": notifyJob.total, "sent": notifyJob.sent, "failed": notifyJob.failed,
		"started_at": notifyJob.started.Unix(),
		"eta_sec": func() int64 {
			done := notifyJob.sent + notifyJob.failed
			if done == 0 || !notifyJob.running.Load() {
				return 0
			}
			elapsed := time.Since(notifyJob.started).Seconds()
			return int64(elapsed / float64(done) * float64(notifyJob.total-done))
		}(),
		"errors": notifyJob.errs,
	})
}
