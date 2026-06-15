package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"
)

type smtpCapture struct {
	MailFrom string
	RcptTo   string
	Data     string
}

func TestIntegration_GhostMailgunRequestToSMTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	captures := make(chan smtpCapture, 2)
	go captureSMTP(t, ln, 1, captures)

	addr := ln.Addr().(*net.TCPAddr)
	sender := &SMTPSender{Config: SMTPConfig{
		Host:    "127.0.0.1",
		Port:    addr.Port,
		TLS:     "none",
		Timeout: 2 * time.Second,
	}}

	req := newGhostMailgunRequest(t, "test-key")
	rr := httptest.NewRecorder()
	newMux("test-key", sender).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["message"] != "Queued. Thank you." {
		t.Fatalf("message = %q, want queued response", resp["message"])
	}
	if !strings.HasPrefix(resp["id"], "<") || !strings.HasSuffix(resp["id"], "@romanticbake.mrssoca.de>") {
		t.Fatalf("unexpected Mailgun response id: %q", resp["id"])
	}

	got := map[string]smtpCapture{}
	for range 2 {
		select {
		case capture := <-captures:
			got[capture.RcptTo] = capture
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for SMTP capture")
		}
	}

	assertCapturedGhostMessage(t, got["alice@example.com"], "alice@example.com", "Alice", "https://romanticbake.mrssoca.de/members/?token=alice", "https://romanticbake.mrssoca.de/unsubscribe/?uuid=member-alice")
	assertCapturedGhostMessage(t, got["bob@example.com"], "bob@example.com", "Bob", "https://romanticbake.mrssoca.de/members/?token=bob", "https://romanticbake.mrssoca.de/unsubscribe/?uuid=member-bob")
}

func newGhostMailgunRequest(t *testing.T, apiKey string) *http.Request {
	t.Helper()

	recipientVariables := map[string]map[string]string{
		"alice@example.com": {
			"name":             "Alice",
			"uuid":             "member-alice",
			"list_unsubscribe": "https://romanticbake.mrssoca.de/members/?token=alice",
			"unsubscribe_url":  "https://romanticbake.mrssoca.de/unsubscribe/?uuid=member-alice",
		},
		"bob@example.com": {
			"name":             "Bob",
			"uuid":             "member-bob",
			"list_unsubscribe": "https://romanticbake.mrssoca.de/members/?token=bob",
			"unsubscribe_url":  "https://romanticbake.mrssoca.de/unsubscribe/?uuid=member-bob",
		},
	}
	recipientJSON, err := json.Marshal(recipientVariables)
	if err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fields := map[string][]string{
		"to":                         {"alice@example.com", "bob@example.com"},
		"from":                       {"Romantic Bake <newsletter@romanticbake.mrssoca.de>"},
		"subject":                    {"New cake for %recipient.name%"},
		"html":                       {`<p>Hello %recipient.name%</p><p><a href="%recipient.unsubscribe_url%">Unsubscribe</a></p>`},
		"text":                       {"Hello %recipient.name%\nUnsubscribe: %recipient.unsubscribe_url%"},
		"h:Reply-To":                 {"newsletter@romanticbake.mrssoca.de"},
		"h:Sender":                   {"Romantic Bake <newsletter@romanticbake.mrssoca.de>"},
		"h:Auto-Submitted":           {"auto-generated"},
		"h:X-Auto-Response-Suppress": {"OOF, AutoReply"},
		"h:List-Unsubscribe":         {"<%recipient.list_unsubscribe%>, <%tag_unsubscribe_email%>"},
		"h:List-Unsubscribe-Post":    {"List-Unsubscribe=One-Click"},
		"recipient-variables":        {string(recipientJSON)},
		"o:tag":                      {"bulk-email", "ghost-email"},
		"o:tracking-opens":           {"true"},
		"v:email-id":                 {"email-123"},
	}
	for key, values := range fields {
		for _, value := range values {
			if err := mw.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v3/romanticbake.mrssoca.de/messages", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetBasicAuth("api", apiKey)
	return req
}

func captureSMTP(t *testing.T, ln net.Listener, count int, captures chan<- smtpCapture) {
	t.Helper()
	for range count {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleSMTPConnection(conn, captures)
	}
}

func handleSMTPConnection(conn net.Conn, captures chan<- smtpCapture) {
	defer conn.Close()

	write := func(s string) {
		conn.Write([]byte(s + "\r\n"))
	}
	write("220 localhost ESMTP mock")

	var capture smtpCapture
	var inData bool
	var dataLines []string
	lineReader := newSMTPLineReader(conn)

	for {
		line, err := lineReader()
		if err != nil {
			captures <- capture
			return
		}
		upper := strings.ToUpper(line)

		if inData {
			if line == "." {
				inData = false
				capture.Data = strings.Join(dataLines, "\r\n")
				captures <- capture
				capture = smtpCapture{}
				dataLines = nil
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
			capture.MailFrom = extractSMTPPath(line)
			write("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			capture.RcptTo = extractSMTPPath(line)
			write("250 OK")
		case upper == "DATA":
			write("354 Go ahead")
			inData = true
		case upper == "QUIT":
			write("221 Bye")
			return
		default:
			write("500 Unknown command")
		}
	}
}

func newSMTPLineReader(conn net.Conn) func() (string, error) {
	var buf bytes.Buffer
	return func() (string, error) {
		buf.Reset()
		tmp := make([]byte, 1)
		for {
			if _, err := conn.Read(tmp); err != nil {
				return "", err
			}
			if tmp[0] == '\n' {
				return strings.TrimRight(buf.String(), "\r"), nil
			}
			buf.WriteByte(tmp[0])
		}
	}
}

func extractSMTPPath(line string) string {
	start := strings.IndexByte(line, '<')
	end := strings.IndexByte(line, '>')
	if start >= 0 && end > start {
		return line[start+1 : end]
	}
	_, value, ok := strings.Cut(line, ":")
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func assertCapturedGhostMessage(t *testing.T, capture smtpCapture, wantTo, wantName, wantListUnsub, wantBodyUnsub string) {
	t.Helper()
	if capture.RcptTo != wantTo {
		t.Fatalf("RcptTo = %q, want %q", capture.RcptTo, wantTo)
	}
	if capture.MailFrom != "newsletter@romanticbake.mrssoca.de" {
		t.Fatalf("MailFrom = %q, want newsletter@romanticbake.mrssoca.de", capture.MailFrom)
	}

	msg, err := mail.ReadMessage(strings.NewReader(capture.Data))
	if err != nil {
		t.Fatalf("read MIME message: %v\n%s", err, capture.Data)
	}

	if from := msg.Header.Get("From"); !strings.Contains(from, "newsletter@romanticbake.mrssoca.de") {
		t.Fatalf("From header = %q", from)
	}
	toAddr, err := mail.ParseAddress(msg.Header.Get("To"))
	if err != nil {
		t.Fatalf("parse To header: %v", err)
	}
	if toAddr.Address != wantTo {
		t.Fatalf("To header address = %q, want %q", toAddr.Address, wantTo)
	}
	if subject := msg.Header.Get("Subject"); subject != "New cake for "+wantName {
		t.Fatalf("Subject = %q, want personalized subject", subject)
	}
	replyToAddr, err := mail.ParseAddress(msg.Header.Get("Reply-To"))
	if err != nil {
		t.Fatalf("parse Reply-To header: %v", err)
	}
	if replyToAddr.Address != "newsletter@romanticbake.mrssoca.de" {
		t.Fatalf("Reply-To address = %q", replyToAddr.Address)
	}
	if lu := msg.Header.Get("List-Unsubscribe"); lu != "<"+wantListUnsub+">" {
		t.Fatalf("List-Unsubscribe = %q, want personalized one-click URL", lu)
	}
	if lup := msg.Header.Get("List-Unsubscribe-Post"); lup != "List-Unsubscribe=One-Click" {
		t.Fatalf("List-Unsubscribe-Post = %q", lup)
	}
	if strings.Contains(capture.Data, "tag_unsubscribe_email") {
		t.Fatal("message still contains Mailgun tag_unsubscribe_email token")
	}
	if strings.Contains(capture.Data, "%recipient.") {
		t.Fatal("message still contains recipient placeholders")
	}

	parts := decodeMultipartAlternative(t, msg)
	if !strings.Contains(parts["text/plain"], "Hello "+wantName) {
		t.Fatalf("text part was not personalized:\n%s", parts["text/plain"])
	}
	if !strings.Contains(parts["text/plain"], wantBodyUnsub) {
		t.Fatalf("text part missing unsubscribe URL:\n%s", parts["text/plain"])
	}
	if !strings.Contains(parts["text/html"], "Hello "+wantName) {
		t.Fatalf("HTML part was not personalized:\n%s", parts["text/html"])
	}
	if !strings.Contains(parts["text/html"], wantBodyUnsub) {
		t.Fatalf("HTML part missing unsubscribe URL:\n%s", parts["text/html"])
	}
}

func decodeMultipartAlternative(t *testing.T, msg *mail.Message) map[string]string {
	t.Helper()

	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse Content-Type: %v", err)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q, want multipart/alternative", mediaType)
	}

	mr := multipart.NewReader(msg.Body, params["boundary"])
	parts := map[string]string{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read MIME part: %v", err)
		}

		partMediaType, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse part Content-Type: %v", err)
		}

		reader := io.Reader(part)
		if strings.EqualFold(part.Header.Get("Content-Transfer-Encoding"), "quoted-printable") {
			reader = quotedprintable.NewReader(part)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read part body: %v", err)
		}
		parts[partMediaType] = string(body)
	}
	return parts
}
