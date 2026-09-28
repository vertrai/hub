package manager

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"time"
)

func sendResourceAlert(ctx context.Context, cfg resourceAlertSettings, channel, body string) error {
	if channel == "telegram" {
		payload, _ := json.Marshal(map[string]string{"chat_id": cfg.TelegramChatID, "text": body})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+cfg.TelegramToken+"/sendMessage", bytes.NewReader(payload))
		if err != nil {
			return errors.New("telegram request failed")
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			return errors.New("telegram connection failed")
		}
		defer resp.Body.Close()
		var result struct {
			OK bool `json:"ok"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&result) != nil || resp.StatusCode != 200 || !result.OK {
			return errors.New("telegram delivery rejected")
		}
		return nil
	}
	if channel != "email" {
		return errors.New("unknown notification channel")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", smtpAddress(cfg))
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(25 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	tlsConfig := &tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12}
	if cfg.SMTPMode == "tls" {
		secure := tls.Client(conn, tlsConfig)
		if err = secure.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()
	if cfg.SMTPMode == "starttls" {
		if err = client.StartTLS(tlsConfig); err != nil {
			return err
		}
	} else if cfg.SMTPMode != "tls" {
		return errors.New("TLS required")
	}
	if cfg.SMTPUser != "" {
		if err = client.Auth(smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPHost)); err != nil {
			return err
		}
	}
	if err = client.Mail(cfg.EmailFrom); err != nil {
		return err
	}
	if err = client.Rcpt(cfg.EmailTo); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	message := "From: " + cfg.EmailFrom + "\r\nTo: " + cfg.EmailTo + "\r\nSubject: " + mime.QEncoding.Encode("UTF-8", "Hub 资源库存告警") + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + body + "\r\n"
	if _, err = io.WriteString(writer, message); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	// DATA acceptance is the delivery acknowledgement; QUIT failure must not retry it.
	_ = client.Quit()
	return nil
}
