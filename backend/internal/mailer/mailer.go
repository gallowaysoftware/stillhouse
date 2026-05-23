// Package mailer abstracts transactional email behind a small interface so
// the rest of the codebase doesn't depend on a specific provider. Two
// implementations: Resend (production) and Console (dev — prints what would
// have been sent to stdout). Picked via STILLHOUSE_MAILER env.
package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// Mailer is the surface every site of email-sending in the app talks to.
// Methods are intentionally named per the call site (SendWelcome,
// SendPasswordReset) instead of a generic Send, so the implementation
// owns the template and the call site just supplies data — no per-caller
// template duplication.
type Mailer interface {
	SendWelcome(ctx context.Context, to, displayName, tenantName string) error
	SendPasswordReset(ctx context.Context, to, displayName, resetURL string) error
}

// FromEnv constructs whichever Mailer the env says. Defaults to console so
// missing config never silently drops mail in dev — it gets printed.
func FromEnv(logger *slog.Logger) Mailer {
	switch os.Getenv("STILLHOUSE_MAILER") {
	case "resend":
		key := os.Getenv("RESEND_API_KEY")
		from := os.Getenv("MAIL_FROM")
		if key == "" || from == "" {
			logger.Warn("STILLHOUSE_MAILER=resend but RESEND_API_KEY or MAIL_FROM is empty; falling back to console")
			return &Console{logger: logger}
		}
		return &Resend{apiKey: key, from: from, logger: logger, http: &http.Client{Timeout: 10 * time.Second}}
	default:
		return &Console{logger: logger}
	}
}

// Console prints what WOULD have been sent — useful in dev to read
// password-reset links from `docker logs` without an SMTP setup.
type Console struct{ logger *slog.Logger }

func (c *Console) SendWelcome(_ context.Context, to, displayName, tenantName string) error {
	c.logger.Info("WELCOME EMAIL", "to", to, "name", displayName, "tenant", tenantName)
	return nil
}

func (c *Console) SendPasswordReset(_ context.Context, to, displayName, resetURL string) error {
	c.logger.Info("PASSWORD RESET EMAIL", "to", to, "name", displayName, "url", resetURL)
	return nil
}

// Resend talks to https://resend.com via their POST /emails endpoint. Tiny
// hand-rolled client to avoid pulling their SDK (and its transitive deps).
type Resend struct {
	apiKey string
	from   string
	http   *http.Client
	logger *slog.Logger
}

type resendReq struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

func (r *Resend) send(ctx context.Context, req resendReq) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("resend: status %d: %s", resp.StatusCode, string(buf))
	}
	return nil
}

func (r *Resend) SendWelcome(ctx context.Context, to, displayName, tenantName string) error {
	subject := "Welcome to Stillhouse"
	html := fmt.Sprintf(`<p>Hi %s,</p>
<p>Your Stillhouse tenant <strong>%s</strong> is live. Sign in to start tracking your distillery.</p>
<p>If you didn't sign up, ignore this email.</p>`, htmlEscape(displayName), htmlEscape(tenantName))
	return r.send(ctx, resendReq{From: r.from, To: to, Subject: subject, HTML: html})
}

func (r *Resend) SendPasswordReset(ctx context.Context, to, displayName, resetURL string) error {
	subject := "Stillhouse password reset"
	html := fmt.Sprintf(`<p>Hi %s,</p>
<p>Use this link to reset your Stillhouse password (expires in 1 hour):</p>
<p><a href="%s">%s</a></p>
<p>If you didn't ask for this, ignore the email — your password stays unchanged.</p>`,
		htmlEscape(displayName), htmlEscape(resetURL), htmlEscape(resetURL))
	return r.send(ctx, resendReq{From: r.from, To: to, Subject: subject, HTML: html})
}

// htmlEscape is the bare minimum to keep displayed names from breaking the
// HTML body or sneaking in script tags. Templates here are tiny enough that
// pulling html/template is overkill; this stays inline.
func htmlEscape(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			b = append(b, '&', 'l', 't', ';')
		case '>':
			b = append(b, '&', 'g', 't', ';')
		case '&':
			b = append(b, '&', 'a', 'm', 'p', ';')
		case '"':
			b = append(b, '&', 'q', 'u', 'o', 't', ';')
		case '\'':
			b = append(b, '&', '#', '3', '9', ';')
		default:
			b = append(b, s[i])
		}
	}
	return string(b)
}
