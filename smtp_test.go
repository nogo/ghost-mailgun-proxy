package main

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// mockSMTPServer accepts one connection and records the DATA payload.
// It speaks just enough SMTP to satisfy net/smtp.
func mockSMTPServer(t *testing.T, ln net.Listener, result chan<- string) {
	t.Helper()
	conn, err := ln.Accept()
	if err != nil {
		result <- ""
		return
	}
	defer conn.Close()

	write := func(s string) {
		conn.Write([]byte(s + "\r\n"))
	}
	scanner := bufio.NewScanner(conn)

	write("220 localhost ESMTP mock")

	var inData bool
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()
		upper := strings.ToUpper(line)

		if inData {
			if line == "." {
				inData = false
				write("250 OK")
				continue
			}
			dataLines = append(dataLines, line)
			continue
		}

		switch {
		case strings.HasPrefix(upper, "EHLO"):
			write("250-localhost")
			write("250 OK")
		case strings.HasPrefix(upper, "HELO"):
			write("250 OK")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			write("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			write("250 OK")
		case upper == "DATA":
			write("354 Go ahead")
			inData = true
		case upper == "QUIT":
			write("221 Bye")
			result <- strings.Join(dataLines, "\r\n")
			return
		default:
			write("500 Unknown command")
		}
	}
	result <- strings.Join(dataLines, "\r\n")
}

func TestSMTPSender_Send(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	result := make(chan string, 1)
	go mockSMTPServer(t, ln, result)

	addr := ln.Addr().(*net.TCPAddr)
	sender := &SMTPSender{Config: SMTPConfig{
		Host: "127.0.0.1",
		Port: addr.Port,
		TLS:  "none",
	}}

	msg := []byte("From: sender@example.com\r\nTo: rcpt@example.com\r\nSubject: Test\r\n\r\nHello")
	if err := sender.Send("sender@example.com", "rcpt@example.com", msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	data := <-result
	if !strings.Contains(data, "Subject: Test") {
		t.Errorf("expected Subject header in DATA, got:\n%s", data)
	}
	if !strings.Contains(data, "Hello") {
		t.Errorf("expected body in DATA, got:\n%s", data)
	}
}

func TestBuildMessage(t *testing.T) {
	email := &Email{
		From:    "blog@example.com",
		To:      "reader@example.com",
		Subject: "New Post",
		HTML:    "<h1>Hello</h1>",
		Text:    "Hello",
		ReplyTo: "reply@example.com",
		Headers: map[string]string{
			"List-Unsubscribe":      "<https://example.com/unsub>",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}

	msg, err := buildMessage(email)
	if err != nil {
		t.Fatalf("buildMessage failed: %v", err)
	}
	s := string(msg)

	checks := []struct {
		name string
		want string
	}{
		{"From header", "From: blog@example.com"},
		{"To header", "To: reader@example.com"},
		{"Subject header", "Subject: New Post"},
		{"Reply-To header", "Reply-To: reply@example.com"},
		{"List-Unsubscribe header", "List-Unsubscribe: <https://example.com/unsub>"},
		{"List-Unsubscribe-Post header", "List-Unsubscribe-Post: List-Unsubscribe=One-Click"},
		{"MIME version", "MIME-Version: 1.0"},
		{"multipart/alternative", "multipart/alternative"},
		{"text/plain part", "text/plain"},
		{"text/html part", "text/html"},
		{"text body", "Hello"},
		{"html body", "<h1>Hello</h1>"},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(s, c.want) {
				t.Errorf("missing %q in message:\n%s", c.want, s)
			}
		})
	}
}

func TestBuildMessage_AutoHeaders(t *testing.T) {
	email := &Email{
		From:    "blog@example.com",
		To:      "reader@example.com",
		Subject: "Test",
		HTML:    "<p>Hi</p>",
		Text:    "Hi",
	}

	msg, err := buildMessage(email)
	if err != nil {
		t.Fatalf("buildMessage failed: %v", err)
	}
	s := string(msg)

	if !strings.Contains(s, "Auto-Submitted: auto-generated") {
		t.Error("missing Auto-Submitted header")
	}
	if !strings.Contains(s, "X-Auto-Response-Suppress: OOF, AutoReply") {
		t.Error("missing X-Auto-Response-Suppress header")
	}
}

func TestBuildMessage_RejectsHeaderInjection(t *testing.T) {
	email := &Email{
		From:    "blog@example.com",
		To:      "reader@example.com",
		Subject: "Test\r\nBcc: attacker@example.com",
		HTML:    "<p>Hi</p>",
		Text:    "Hi",
	}

	if _, err := buildMessage(email); err == nil {
		t.Fatal("expected header injection error")
	}
}

func TestBuildMessage_EncodesNonASCIISubject(t *testing.T) {
	email := &Email{
		From:    "blog@example.com",
		To:      "reader@example.com",
		Subject: "Käsekuchen",
		Text:    "Hi",
	}

	msg, err := buildMessage(email)
	if err != nil {
		t.Fatalf("buildMessage failed: %v", err)
	}
	s := string(msg)
	if !strings.Contains(s, "Subject: =?utf-8?") {
		t.Fatalf("subject was not RFC 2047 encoded:\n%s", s)
	}
	if strings.Contains(s, "Subject: Käsekuchen") {
		t.Fatalf("subject leaked raw non-ASCII:\n%s", s)
	}
}
