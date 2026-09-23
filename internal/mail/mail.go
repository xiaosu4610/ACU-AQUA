// Package mail SMTP 发信（隐式 TLS，如 smtpdm.aliyun.com:465）。
// 用于注册/找回密码验证码。零依赖标准库实现。
package mail

import (
	crand "crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"acu-aqua/gateway/internal/config"
)

// Sender SMTP 发信器
type Sender struct {
	Cfg config.SMTP
}

// New 构造（未配置返回 nil，调用方需判空）
func New(c config.SMTP) *Sender {
	if c.Host == "" || c.User == "" || c.Pass == "" {
		return nil
	}
	if c.Port <= 0 {
		c.Port = 465
	}
	if c.From == "" {
		c.From = c.User
	}
	return &Sender{Cfg: c}
}

// Send 发送纯文本邮件（to 支持逗号分隔多地址）。
// 超时护栏：dial 10s + 会话整体 45s——兜底通道绝不允许无限挂起拖死调用方 handler。
func (s *Sender) Send(to, subject, body string) error {
	addr := s.Cfg.Host + ":" + strconv.Itoa(s.Cfg.Port)
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: s.Cfg.Host})
	if err != nil {
		return fmt.Errorf("SMTP 连接失败: %w", err)
	}
	cli, err := smtp.NewClient(conn, s.Cfg.Host)
	if err != nil {
		return fmt.Errorf("SMTP 客户端失败: %w", err)
	}
	defer cli.Close()
	_ = conn.SetDeadline(time.Now().Add(45 * time.Second))
	auth := smtp.PlainAuth("", s.Cfg.User, s.Cfg.Pass, s.Cfg.Host)
	if err := cli.Auth(auth); err != nil {
		return fmt.Errorf("SMTP 认证失败: %w", err)
	}
	if err := cli.Mail(s.Cfg.User); err != nil {
		return err
	}
	for _, t := range splitAddrs(to) {
		if err := cli.Rcpt(t); err != nil {
			return err
		}
	}
	w, err := cli.Data()
	if err != nil {
		return err
	}
	msg := buildMessage(s.Cfg.From, to, subject, body)
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cli.Quit()
}

func splitAddrs(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' || r == ';' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// buildMessage RFC 5322 报文（20260919 反垃圾改造）：
//   - multipart/alternative（text + branded HTML）——纯单部分文本是垃圾分大头
//   - 补齐 Date / Message-ID / X-Mailer 规范头——缺 Message-ID 会被多数反垃圾引擎扣分
//   - HTML 部分表格布局 + 内联样式（邮件客户端兼容，无外部资源、无脚本）
func buildMessage(from, to, subject, body string) string {
	domain := "localhost"
	if i := strings.LastIndex(from, "@"); i >= 0 && i+1 < len(from) {
		domain = from[i+1:]
	}
	msgID := fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), randHex(12), domain)
	boundary := "=_aqua_" + randHex(16)

	textPart := "Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		chunk64(b64(body))
	htmlPart := "Content-Type: text/html; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		chunk64(b64(renderHTML(body)))

	return "From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: =?UTF-8?B?" + b64(subject) + "?=\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
		"Message-ID: " + msgID + "\r\n" +
		"X-Mailer: AQUA-api-Mailer\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n" +
		"This is a multi-part message in MIME format.\r\n" +
		"--" + boundary + "\r\n" + textPart + "\r\n" +
		"--" + boundary + "\r\n" + htmlPart + "\r\n" +
		"--" + boundary + "--\r\n"
}

// renderHTML 品牌化 HTML 正文（内联样式；正文整体 HTML 转义，防注入）
func renderHTML(body string) string {
	inner := htmlEsc(body)
	return `<!DOCTYPE html>
<html lang="zh-CN"><body style="margin:0;padding:0;background:#f4f7fa;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#f4f7fa;padding:28px 12px;">
<tr><td align="center">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;width:100%;background:#ffffff;border-radius:14px;border:1px solid #e3ebf2;overflow:hidden;font-family:'PingFang SC','Microsoft YaHei','Segoe UI',Arial,sans-serif;">
<tr><td style="background:linear-gradient(115deg,#08796e,#126e9e);padding:18px 28px;">
  <span style="color:#ffffff;font-size:17px;font-weight:700;letter-spacing:.02em;">AQUA api</span>
  <span style="color:rgba(255,255,255,.75);font-size:12px;margin-left:10px;">公益模型服务平台</span>
</td></tr>
<tr><td style="padding:30px 30px 10px;color:#14283c;font-size:15px;line-height:1.9;">
  ` + inner + `
</td></tr>
<tr><td style="padding:6px 30px 30px;color:#536b80;font-size:12.5px;line-height:1.9;">
  本邮件由 AQUA api 系统发出（验证码 / 账号安全通知）。如果你没有进行过相关操作，请忽略本邮件，你的账号不会受影响。<br>
  为保障服务长期公益运行，站点对滥用行为有限流措施；如有疑问，欢迎加入站点 QQ 交流群联系管理员。
</td></tr>
<tr><td style="border-top:1px solid #e3ebf2;padding:14px 30px;color:#8aa0b3;font-size:11px;">
  本邮件由系统自动发送，请勿直接回复。© AQUA api
</td></tr>
</table>
</td></tr></table>
</body></html>`
}

// htmlEsc 最小 HTML 转义（标准库 html 为外部内容设计，此处自持更稳）
func htmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;")
	return r.Replace(s)
}

// randHex n 字节随机 hex（Message-ID / boundary 用）
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}

// chunk64 base64 按 76 字符折行（RFC 2045）
func chunk64(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i += 76 {
		end := i + 76
		if end > len(s) {
			end = len(s)
		}
		sb.WriteString(s[i:end])
		sb.WriteString("\r\n")
	}
	return sb.String()
}
