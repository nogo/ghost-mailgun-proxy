package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSender records calls and optionally returns errors per recipient.
type fakeSender struct {
	calls  []fakeSendCall
	errors map[string]error // to-address → error
}

type fakeSendCall struct {
	From string
	To   string
	Msg  []byte
}

func (f *fakeSender) Send(from, to string, msg []byte) error {
	f.calls = append(f.calls, fakeSendCall{From: from, To: to, Msg: msg})
	if f.errors != nil {
		if err, ok := f.errors[to]; ok {
			return err
		}
	}
	return nil
}

func newGhostRequest(t *testing.T, apiKey string, fields map[string][]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for key, vals := range fields {
		for _, v := range vals {
			mw.WriteField(key, v)
		}
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v3/example.com/messages", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if apiKey != "" {
		req.SetBasicAuth("api", apiKey)
	}
	return req
}

func ghostFields() map[string][]string {
	return map[string][]string{
		"to":                  {"alice@example.com", "bob@example.com"},
		"from":                {"blog@example.com"},
		"subject":             {"Hello %recipient.name%"},
		"html":                {"<h1>Hi %recipient.name%</h1>"},
		"text":                {"Hi %recipient.name%"},
		"h:Reply-To":          {"reply@example.com"},
		"h:List-Unsubscribe":  {"<%recipient.list_unsubscribe%>, <%tag_unsubscribe_email%>"},
		"o:tag":               {"bulk-email", "ghost-email"},
		"recipient-variables": {`{"alice@example.com":{"name":"Alice","list_unsubscribe":"https://example.com/unsub/a"},"bob@example.com":{"name":"Bob","list_unsubscribe":"https://example.com/unsub/b"}}`},
	}
}

func TestMessagesHandler_Auth(t *testing.T) {
	sender := &fakeSender{}
	h := newMux("test-key", sender)

	t.Run("missing auth", func(t *testing.T) {
		req := newGhostRequest(t, "", ghostFields())
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", rr.Code)
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		req := newGhostRequest(t, "wrong-key", ghostFields())
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", rr.Code)
		}
	})

	t.Run("wrong username", func(t *testing.T) {
		req := newGhostRequest(t, "", ghostFields())
		req.SetBasicAuth("user", "test-key")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", rr.Code)
		}
	})

	t.Run("correct key", func(t *testing.T) {
		req := newGhostRequest(t, "test-key", ghostFields())
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("got %d, want 200", rr.Code)
		}
	})
}

func TestMessagesHandler_PerRecipientSend(t *testing.T) {
	sender := &fakeSender{}
	h := newMux("test-key", sender)

	req := newGhostRequest(t, "test-key", ghostFields())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rr.Code)
	}

	if len(sender.calls) != 2 {
		t.Fatalf("expected 2 sends, got %d", len(sender.calls))
	}

	// Check Alice's email
	aliceMsg := string(sender.calls[0].Msg)
	if sender.calls[0].To != "alice@example.com" {
		t.Errorf("call[0].To = %q, want alice@example.com", sender.calls[0].To)
	}
	if !strings.Contains(aliceMsg, "Hi Alice") {
		t.Error("Alice's email missing personalized text")
	}
	if !strings.Contains(aliceMsg, "https://example.com/unsub/a") {
		t.Error("Alice's email missing unsubscribe link")
	}
	if strings.Contains(aliceMsg, "tag_unsubscribe_email") {
		t.Error("Alice's email still contains tag_unsubscribe_email token")
	}

	// Check Bob's email
	bobMsg := string(sender.calls[1].Msg)
	if sender.calls[1].To != "bob@example.com" {
		t.Errorf("call[1].To = %q, want bob@example.com", sender.calls[1].To)
	}
	if !strings.Contains(bobMsg, "Hi Bob") {
		t.Error("Bob's email missing personalized text")
	}

	// Check response JSON
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["message"] != "Queued. Thank you." {
		t.Errorf("message = %q", resp["message"])
	}
	if !strings.Contains(resp["id"], "@") {
		t.Errorf("id missing @: %q", resp["id"])
	}
}

func TestMessagesHandler_Validation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string][]string)
	}{
		{
			name: "missing recipient",
			mutate: func(fields map[string][]string) {
				delete(fields, "to")
			},
		},
		{
			name: "missing subject",
			mutate: func(fields map[string][]string) {
				delete(fields, "subject")
			},
		},
		{
			name: "missing body",
			mutate: func(fields map[string][]string) {
				delete(fields, "html")
				delete(fields, "text")
			},
		},
		{
			name: "invalid from address",
			mutate: func(fields map[string][]string) {
				fields["from"] = []string{"not an address"}
			},
		},
		{
			name: "invalid recipient variables JSON",
			mutate: func(fields map[string][]string) {
				fields["recipient-variables"] = []string{"{"}
			},
		},
		{
			name: "subject header injection",
			mutate: func(fields map[string][]string) {
				fields["subject"] = []string{"Hello\r\nBcc: attacker@example.com"}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := ghostFields()
			tt.mutate(fields)
			h := newMux("test-key", &fakeSender{})
			req := newGhostRequest(t, "test-key", fields)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", rr.Code)
			}
		})
	}
}

func TestMessagesHandler_RecipientVariableHeaderInjectionFailsRecipient(t *testing.T) {
	sender := &fakeSender{}
	h := newMux("test-key", sender)

	fields := ghostFields()
	fields["to"] = []string{"alice@example.com"}
	fields["recipient-variables"] = []string{`{"alice@example.com":{"name":"Alice\r\nBcc: attacker@example.com","list_unsubscribe":"https://example.com/unsub/a"}}`}

	req := newGhostRequest(t, "test-key", fields)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500 when all recipients fail validation", rr.Code)
	}
	if len(sender.calls) != 0 {
		t.Fatalf("expected no send attempts, got %d", len(sender.calls))
	}
}

func TestMessagesHandler_PartialFailure(t *testing.T) {
	sender := &fakeSender{
		errors: map[string]error{
			"alice@example.com": fmt.Errorf("connection refused"),
		},
	}
	h := newMux("test-key", sender)

	req := newGhostRequest(t, "test-key", ghostFields())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Partial failure: some succeeded → 200
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200 on partial failure", rr.Code)
	}
	if len(sender.calls) != 2 {
		t.Errorf("expected 2 send attempts, got %d", len(sender.calls))
	}
}

func TestMessagesHandler_RedactsRecipientLogsByDefault(t *testing.T) {
	var logs bytes.Buffer
	captureLogs(t, &logs)

	sender := &fakeSender{
		errors: map[string]error{
			"alice@example.com": fmt.Errorf("connection refused"),
		},
	}
	h := newMux("test-key", sender)

	req := newGhostRequest(t, "test-key", ghostFields())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 on partial failure", rr.Code)
	}
	got := logs.String()
	if strings.Contains(got, "alice@example.com") || strings.Contains(got, "bob@example.com") {
		t.Fatalf("default logs leaked recipient address:\n%s", got)
	}
	if !strings.Contains(got, "recipients=2 sent=1 failed=1") {
		t.Fatalf("default logs missing aggregate counts:\n%s", got)
	}
}

func TestMessagesHandler_DebugLogsRecipients(t *testing.T) {
	var logs bytes.Buffer
	captureLogs(t, &logs)

	sender := &fakeSender{}
	h := newMuxWithConfig("test-key", sender, HandlerConfig{Debug: true})

	req := newGhostRequest(t, "test-key", ghostFields())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rr.Code)
	}
	got := logs.String()
	if !strings.Contains(got, "sent to alice@example.com") || !strings.Contains(got, "sent to bob@example.com") {
		t.Fatalf("debug logs did not include recipient addresses:\n%s", got)
	}
}

func TestMessagesHandler_AllFail(t *testing.T) {
	sender := &fakeSender{
		errors: map[string]error{
			"alice@example.com": fmt.Errorf("fail"),
			"bob@example.com":   fmt.Errorf("fail"),
		},
	}
	h := newMux("test-key", sender)

	req := newGhostRequest(t, "test-key", ghostFields())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500 when all fail", rr.Code)
	}
}

func TestEventsHandler(t *testing.T) {
	h := newMux("test-key", &fakeSender{})

	req := httptest.NewRequest(http.MethodGet, "/v3/example.com/events?event=delivered", nil)
	req.SetBasicAuth("api", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rr.Code)
	}

	body, _ := io.ReadAll(rr.Body)
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items, ok := resp["items"].([]any)
	if !ok || len(items) != 0 {
		t.Errorf("expected empty items array, got %v", resp["items"])
	}
}

func TestSuppressionHandler(t *testing.T) {
	h := newMux("test-key", &fakeSender{})

	req := httptest.NewRequest(http.MethodDelete, "/v3/example.com/bounces/test@example.com", nil)
	req.SetBasicAuth("api", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rr.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["address"] != "test@example.com" {
		t.Errorf("address = %q", resp["address"])
	}
}

func TestHealthz(t *testing.T) {
	h := newMux("test-key", &fakeSender{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
}

func captureLogs(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})
}
