package main

import (
	"crypto/tls"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// Sender sends an email message.
type Sender interface {
	Send(from, to string, msg []byte) error
}

type SendRequest struct {
	From string
	To   string
	Msg  []byte
}

type BatchSender interface {
	SendBatch(requests []SendRequest) []error
}

// SMTPConfig holds SMTP connection parameters.
type SMTPConfig struct {
	Host         string
	Port         int
	User         string
	Pass         string
	TLS          string // "starttls", "tls", or "none"
	FromOverride string
	Timeout      time.Duration
	HELO         string
}

// SMTPSender sends email via SMTP.
type SMTPSender struct {
	Config SMTPConfig
}

func (s *SMTPSender) Send(from, to string, msg []byte) error {
	errs := s.SendBatch([]SendRequest{{From: from, To: to, Msg: msg}})
	if len(errs) == 0 {
		return nil
	}
	return errs[0]
}

func (s *SMTPSender) SendBatch(requests []SendRequest) []error {
	errs := make([]error, len(requests))
	if len(requests) == 0 {
		return errs
	}

	c, conn, timeout, err := s.connect()
	if err != nil {
		for i := range errs {
			errs[i] = err
		}
		return errs
	}
	defer c.Close()

	for i, req := range requests {
		if err := s.sendWithClient(c, conn, timeout, req); err != nil {
			errs[i] = err
			if resetErr := resetClient(c, conn, timeout); resetErr != nil {
				for j := i + 1; j < len(errs); j++ {
					errs[j] = fmt.Errorf("smtp reset after failure: %w", resetErr)
				}
				return errs
			}
		}
	}

	_ = setDeadline(conn, timeout)
	_ = c.Quit()
	return errs
}

func (s *SMTPSender) connect() (*smtp.Client, net.Conn, time.Duration, error) {
	timeout := s.Config.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	addr := net.JoinHostPort(s.Config.Host, fmt.Sprintf("%d", s.Config.Port))

	var c *smtp.Client
	var conn net.Conn
	var err error

	switch s.Config.TLS {
	case "tls":
		dialer := &net.Dialer{Timeout: timeout}
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: s.Config.Host})
		if err != nil {
			return nil, nil, 0, fmt.Errorf("tls dial: %w", err)
		}
		if err := setDeadline(conn, timeout); err != nil {
			conn.Close()
			return nil, nil, 0, err
		}
		c, err = smtp.NewClient(conn, s.Config.Host)
		if err != nil {
			conn.Close()
			return nil, nil, 0, fmt.Errorf("smtp client: %w", err)
		}
	case "none", "starttls":
		dialer := &net.Dialer{Timeout: timeout}
		conn, err = dialer.Dial("tcp", addr)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("smtp dial: %w", err)
		}
		if err := setDeadline(conn, timeout); err != nil {
			conn.Close()
			return nil, nil, 0, err
		}
		c, err = smtp.NewClient(conn, s.Config.Host)
		if err != nil {
			conn.Close()
			return nil, nil, 0, fmt.Errorf("smtp client: %w", err)
		}
		if s.Config.TLS == "starttls" {
			if s.Config.HELO != "" {
				if err := setDeadline(conn, timeout); err != nil {
					c.Close()
					return nil, nil, 0, err
				}
				if err := c.Hello(s.Config.HELO); err != nil {
					c.Close()
					return nil, nil, 0, fmt.Errorf("smtp hello: %w", err)
				}
			}
			if err := setDeadline(conn, timeout); err != nil {
				c.Close()
				return nil, nil, 0, err
			}
			if err := c.StartTLS(&tls.Config{ServerName: s.Config.Host}); err != nil {
				c.Close()
				return nil, nil, 0, fmt.Errorf("starttls: %w", err)
			}
		}
	default:
		return nil, nil, 0, fmt.Errorf("invalid SMTP TLS mode %q", s.Config.TLS)
	}

	if s.Config.HELO != "" && s.Config.TLS != "starttls" {
		if err := setDeadline(conn, timeout); err != nil {
			c.Close()
			return nil, nil, 0, err
		}
		if err := c.Hello(s.Config.HELO); err != nil {
			c.Close()
			return nil, nil, 0, fmt.Errorf("smtp hello: %w", err)
		}
	}

	if s.Config.User != "" {
		if err := setDeadline(conn, timeout); err != nil {
			c.Close()
			return nil, nil, 0, err
		}
		auth := smtp.PlainAuth("", s.Config.User, s.Config.Pass, s.Config.Host)
		if err := c.Auth(auth); err != nil {
			c.Close()
			return nil, nil, 0, fmt.Errorf("smtp auth: %w", err)
		}
	}

	return c, conn, timeout, nil
}

func (s *SMTPSender) sendWithClient(c *smtp.Client, conn net.Conn, timeout time.Duration, req SendRequest) error {
	from := req.From
	if s.Config.FromOverride != "" {
		from = s.Config.FromOverride
	}

	if err := setDeadline(conn, timeout); err != nil {
		return err
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := setDeadline(conn, timeout); err != nil {
		return err
	}
	if err := c.Rcpt(req.To); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}

	if err := setDeadline(conn, timeout); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if err := setDeadline(conn, timeout); err != nil {
		w.Close()
		return err
	}
	if _, err := w.Write(req.Msg); err != nil {
		return fmt.Errorf("write data: %w", err)
	}
	if err := setDeadline(conn, timeout); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close data: %w", err)
	}

	return nil
}

func resetClient(c *smtp.Client, conn net.Conn, timeout time.Duration) error {
	if err := setDeadline(conn, timeout); err != nil {
		return err
	}
	return c.Reset()
}

func setDeadline(conn net.Conn, timeout time.Duration) error {
	if conn == nil {
		return nil
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}
	return nil
}

// Email holds the fields needed to compose a MIME message.
type Email struct {
	From    string
	To      string
	Subject string
	HTML    string
	Text    string
	ReplyTo string
	Headers map[string]string // extra headers (List-Unsubscribe, etc.)
}

// buildMessage composes a multipart/alternative MIME message.
func buildMessage(e *Email) ([]byte, error) {
	var buf strings.Builder

	if err := writeHeader(&buf, "MIME-Version", "1.0"); err != nil {
		return nil, err
	}
	if err := writeHeader(&buf, "From", e.From); err != nil {
		return nil, err
	}
	if err := writeHeader(&buf, "To", e.To); err != nil {
		return nil, err
	}
	if err := writeHeader(&buf, "Subject", encodeHeaderValue(e.Subject)); err != nil {
		return nil, err
	}
	if e.ReplyTo != "" {
		if err := writeHeader(&buf, "Reply-To", e.ReplyTo); err != nil {
			return nil, err
		}
	}
	if err := writeHeader(&buf, "Auto-Submitted", "auto-generated"); err != nil {
		return nil, err
	}
	if err := writeHeader(&buf, "X-Auto-Response-Suppress", "OOF, AutoReply"); err != nil {
		return nil, err
	}

	for key, val := range e.Headers {
		if val != "" {
			if err := writeHeader(&buf, key, val); err != nil {
				return nil, err
			}
		}
	}

	boundary := "==boundary_ghost_mailgun_proxy=="
	if err := writeHeader(&buf, "Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary)); err != nil {
		return nil, err
	}
	buf.WriteString("\r\n")

	mw := multipart.NewWriter(&buf)
	if err := mw.SetBoundary(boundary); err != nil {
		return nil, fmt.Errorf("set MIME boundary: %w", err)
	}

	if e.Text != "" {
		textHeader := make(textproto.MIMEHeader)
		textHeader.Set("Content-Type", "text/plain; charset=utf-8")
		textHeader.Set("Content-Transfer-Encoding", "quoted-printable")
		pw, err := mw.CreatePart(textHeader)
		if err != nil {
			return nil, fmt.Errorf("create text part: %w", err)
		}
		if err := writeQuotedPrintable(pw, e.Text); err != nil {
			return nil, fmt.Errorf("write text part: %w", err)
		}
	}

	if e.HTML != "" {
		htmlHeader := make(textproto.MIMEHeader)
		htmlHeader.Set("Content-Type", "text/html; charset=utf-8")
		htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
		pw, err := mw.CreatePart(htmlHeader)
		if err != nil {
			return nil, fmt.Errorf("create HTML part: %w", err)
		}
		if err := writeQuotedPrintable(pw, e.HTML); err != nil {
			return nil, fmt.Errorf("write HTML part: %w", err)
		}
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close MIME writer: %w", err)
	}

	return []byte(buf.String()), nil
}

func writeHeader(buf *strings.Builder, key, value string) error {
	if key == "" || strings.ContainsAny(key, ":\r\n") {
		return fmt.Errorf("invalid header key %q", key)
	}
	if hasHeaderBreak(value) {
		return fmt.Errorf("header %s contains invalid line break", key)
	}
	buf.WriteString(key)
	buf.WriteString(": ")
	buf.WriteString(value)
	buf.WriteString("\r\n")
	return nil
}

func encodeHeaderValue(value string) string {
	if isASCII(value) {
		return value
	}
	return mime.QEncoding.Encode("utf-8", value)
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] > 0x7f {
			return false
		}
	}
	return true
}

func writeQuotedPrintable(w interface{ Write([]byte) (int, error) }, s string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(s)); err != nil {
		qp.Close()
		return err
	}
	return qp.Close()
}
