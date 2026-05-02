// Package notify ships notifications (email today; webhook TBD) out of
// feedrig. Stdlib-only — no SDK dependencies.
package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Message is a small generic shape so multiple delivery channels (email
// today, Slack/webhook later) can share the digest renderer.
type Message struct {
	Subject  string
	HTMLBody string
	TextBody string
	To       []string // recipient list; required for email
}

// Mailer ships a Message via email.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Noop accepts any message and returns ErrUnavailable. Use as the default
// when SMTP isn't configured; callers downgrade gracefully.
type Noop struct{}

func (Noop) Send(_ context.Context, _ Message) error { return ErrUnavailable }

var ErrUnavailable = errors.New("mailer unavailable")

// SMTPMailer is a small wrapper around net/smtp. AuthPlain is used when
// User+Pass are non-empty; otherwise we go anonymous (useful for local
// MTAs / Mailpit-style dev).
type SMTPMailer struct {
	Host string // e.g. "smtp.fastmail.com"
	Port int    // 587 (STARTTLS) or 465 (implicit TLS)
	User string
	Pass string
	From string
	// UseImplicitTLS=true uses port-465-style TLS-from-the-start. Otherwise
	// STARTTLS is attempted on port 587.
	UseImplicitTLS bool
}

func (m SMTPMailer) Send(ctx context.Context, msg Message) error {
	if m.Host == "" || m.Port == 0 || m.From == "" {
		return fmt.Errorf("%w: SMTPMailer.Host/Port/From required", ErrUnavailable)
	}
	if len(msg.To) == 0 {
		return errors.New("no recipients")
	}

	addr := fmt.Sprintf("%s:%d", m.Host, m.Port)
	dialer := &net.Dialer{Timeout: 30 * time.Second}

	var conn net.Conn
	var err error
	if m.UseImplicitTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: m.Host})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		return fmt.Errorf("smtp newclient: %w", err)
	}
	defer c.Quit()

	if !m.UseImplicitTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: m.Host}); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}

	if m.User != "" {
		auth := smtp.PlainAuth("", m.User, m.Pass, m.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := c.Mail(m.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, rcpt := range msg.To {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write([]byte(buildMIME(m.From, msg))); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	return nil
}

// buildMIME assembles a multipart/alternative message with both the plain-
// text and HTML bodies. Most clients pick HTML; text is the fallback.
func buildMIME(from string, msg Message) string {
	boundary := "feedrig-mixed-boundary"
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(msg.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)

	if msg.TextBody != "" {
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\n\r\n")
		b.WriteString(msg.TextBody)
		b.WriteString("\r\n\r\n")
	}
	if msg.HTMLBody != "" {
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		fmt.Fprintf(&b, "Content-Type: text/html; charset=utf-8\r\n\r\n")
		b.WriteString(msg.HTMLBody)
		b.WriteString("\r\n\r\n")
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.String()
}
