package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

// sendMail 可在测试中替换
var sendMail = smtpSend

func smtpSend(ctx context.Context, cfg pool.SMTPSettings, to string, m mailContent) error {
	if !cfg.Ready() {
		return errors.New("管理员尚未配置邮件服务")
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var conn net.Conn
	var err error
	if cfg.Security == "ssl" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("连接邮件服务器失败：%w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("邮件服务器握手失败：%w", err)
	}
	defer c.Close()
	if cfg.Security == "starttls" {
		if err = c.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("STARTTLS 失败：%w", err)
		}
	}
	if cfg.Username != "" {
		if err = c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("邮件服务器认证失败：%w", err)
		}
	}
	if err = c.Mail(cfg.From); err != nil {
		return fmt.Errorf("发件人被拒绝：%w", err)
	}
	if err = c.Rcpt(to); err != nil {
		return fmt.Errorf("收件人被拒绝：%w", err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(buildMessage(cfg, to, m)); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func buildMessage(cfg pool.SMTPSettings, to string, m mailContent) []byte {
	from := (&mail.Address{Name: cfg.FromName, Address: cfg.From}).String()
	var id [12]byte
	_, _ = rand.Read(id[:])
	domain := cfg.From[strings.LastIndex(cfg.From, "@")+1:]
	boundary := "np-" + hex.EncodeToString(id[:6])
	var b bytes.Buffer
	for _, h := range [][2]string{
		{"From", from}, {"To", to}, {"Subject", mime.BEncoding.Encode("UTF-8", m.Subject)},
		{"Date", time.Now().Format(time.RFC1123Z)}, {"Message-ID", "<" + hex.EncodeToString(id[:]) + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
	} {
		b.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	if m.HTML == "" {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		writeBase64(&b, m.Text)
		return b.Bytes()
	}
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
	for _, part := range [][2]string{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		b.WriteString("--" + boundary + "\r\nContent-Type: " + part[0] + "; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		writeBase64(&b, part[1])
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes()
}

func writeBase64(b *bytes.Buffer, body string) {
	raw := base64.StdEncoding.EncodeToString([]byte(body))
	for len(raw) > 76 {
		b.WriteString(raw[:76] + "\r\n")
		raw = raw[76:]
	}
	b.WriteString(raw + "\r\n")
}
