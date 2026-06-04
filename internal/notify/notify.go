// Package notify delivers alert notifications to external channels (Slack,
// Teams, Telegram, generic webhook, email) and holds the pure decision logic
// (severity threshold + quiet hours) used by the dispatcher.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// Target is the decrypted per-channel configuration. It is the secret payload
// stored encrypted at rest; it must never be logged or returned by the API.
type Target struct {
	URL      string   `json:"url,omitempty"`      // slack / teams / webhook
	Token    string   `json:"token,omitempty"`    // telegram bot token
	ChatID   string   `json:"chat_id,omitempty"`  // telegram chat id
	Host     string   `json:"host,omitempty"`     // email: SMTP host
	Port     int      `json:"port,omitempty"`     // email: SMTP port
	Username string   `json:"username,omitempty"` // email: SMTP auth user
	Password string   `json:"password,omitempty"` // email: SMTP auth password
	From     string   `json:"from,omitempty"`     // email: From
	To       []string `json:"to,omitempty"`       // email: recipients
}

var httpClient = &http.Client{Timeout: 12 * time.Second}

// Send delivers a message via the given channel type. Real network I/O; returns
// a transport error (never containing the secret target) on failure.
func Send(ctx context.Context, chType string, t Target, subject, body string) error {
	switch chType {
	case "slack":
		return postJSON(ctx, t.URL, map[string]string{"text": subject + "\n" + body})
	case "teams":
		return postJSON(ctx, t.URL, map[string]any{
			"@type": "MessageCard", "@context": "http://schema.org/extensions",
			"summary": subject, "title": subject, "text": body,
		})
	case "webhook":
		return postJSON(ctx, t.URL, map[string]string{"subject": subject, "body": body})
	case "telegram":
		return sendTelegram(ctx, t, subject+"\n"+body)
	case "email":
		return sendEmail(t, subject, body)
	default:
		return fmt.Errorf("unsupported channel type %q", chType)
	}
}

func postJSON(ctx context.Context, endpoint string, payload any) error {
	if endpoint == "" {
		return fmt.Errorf("channel has no URL configured")
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook responded %d", resp.StatusCode)
	}
	return nil
}

func sendTelegram(ctx context.Context, t Target, text string) error {
	if t.Token == "" || t.ChatID == "" {
		return fmt.Errorf("telegram channel needs token + chat_id")
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.Token)
	form := url.Values{"chat_id": {t.ChatID}, "text": {text}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram responded %d", resp.StatusCode)
	}
	return nil
}

func sendEmail(t Target, subject, body string) error {
	if t.Host == "" || len(t.To) == 0 || t.From == "" {
		return fmt.Errorf("email channel needs host, from and at least one recipient")
	}
	port := t.Port
	if port == 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", t.Host, port)
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		t.From, strings.Join(t.To, ", "), subject, body))
	var auth smtp.Auth
	if t.Username != "" {
		auth = smtp.PlainAuth("", t.Username, t.Password, t.Host)
	}
	return smtp.SendMail(addr, auth, t.From, t.To, msg)
}

// ---- Decision logic (pure; unit-tested) -----------------------------------

func sevRank(s string) int {
	switch s {
	case "critical":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}

// InQuietHours reports whether nowMin (minute-of-day, 0..1439) falls inside the
// channel's quiet window [start,end), which may wrap past midnight. A nil bound
// or start==end means "no quiet hours".
func InQuietHours(startMin, endMin *int32, nowMin int) bool {
	if startMin == nil || endMin == nil {
		return false
	}
	s, e := int(*startMin), int(*endMin)
	if s == e {
		return false
	}
	if s < e {
		return nowMin >= s && nowMin < e
	}
	return nowMin >= s || nowMin < e // wraps midnight
}

// ShouldNotify decides whether an alert of alertSev should fire on a channel
// whose minimum severity is minSev, given whether the channel is in quiet
// hours. Critical alerts always pierce quiet hours.
func ShouldNotify(alertSev, minSev string, quiet bool) bool {
	if sevRank(alertSev) < sevRank(minSev) {
		return false
	}
	if quiet && alertSev != "critical" {
		return false
	}
	return true
}
