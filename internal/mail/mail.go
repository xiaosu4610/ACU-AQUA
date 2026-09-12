// Package mail SMTP 发信（隐式 TLS，如 smtpdm.aliyun.com:465）。
// 用于注册/找回密码验证码。零依赖标准库实现。
package mail

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strconv"

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

// Send 发送纯文本邮件（to 支持逗号分隔多地址）
func (s *Sender) Send(to, subject, body string) error {
	addr := s.Cfg.Host + ":" + strconv.Itoa(s.Cfg.Port)
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: s.Cfg.Host})
	if err != nil {
		return fmt.Errorf("SMTP 连接失败: %w", err)
	}
	cli, err := smtp.NewClient(conn, s.Cfg.Host)
	if err != nil {
		return fmt.Errorf("SMTP 客户端失败: %w", err)
	}
	defer cli.Close()
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

// buildMessage RFC 5322 报文（主题与正文 UTF-8 Base64，中文不乱码）
func buildMessage(from, to, subject, body string) string {
	return "From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: =?UTF-8?B?" + b64(subject) + "?=\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64(body) + "\r\n"
}
