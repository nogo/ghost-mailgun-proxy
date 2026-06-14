package main

import "testing"

func TestReplaceRecipientVars(t *testing.T) {
	tests := []struct {
		name    string
		content string
		vars    map[string]string
		want    string
	}{
		{
			name:    "single placeholder",
			content: "Hello %recipient.name%",
			vars:    map[string]string{"name": "Alice"},
			want:    "Hello Alice",
		},
		{
			name:    "multiple placeholders",
			content: "Hi %recipient.name%, click %recipient.list_unsubscribe%",
			vars:    map[string]string{"name": "Bob", "list_unsubscribe": "https://example.com/unsub"},
			want:    "Hi Bob, click https://example.com/unsub",
		},
		{
			name:    "repeated placeholder",
			content: "%recipient.name% and %recipient.name%",
			vars:    map[string]string{"name": "Eve"},
			want:    "Eve and Eve",
		},
		{
			name:    "missing key left as-is",
			content: "Hello %recipient.unknown%",
			vars:    map[string]string{"name": "Alice"},
			want:    "Hello %recipient.unknown%",
		},
		{
			name:    "empty vars",
			content: "Hello %recipient.name%",
			vars:    map[string]string{},
			want:    "Hello %recipient.name%",
		},
		{
			name:    "nil vars",
			content: "Hello %recipient.name%",
			vars:    nil,
			want:    "Hello %recipient.name%",
		},
		{
			name:    "no placeholders",
			content: "Plain text with no placeholders",
			vars:    map[string]string{"name": "Alice"},
			want:    "Plain text with no placeholders",
		},
		{
			name:    "empty content",
			content: "",
			vars:    map[string]string{"name": "Alice"},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceRecipientVars(tt.content, tt.vars)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanListUnsubscribe(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "strip tag_unsubscribe_email with leading comma",
			header: "<https://example.com/unsub>, <%tag_unsubscribe_email%>",
			want:   "<https://example.com/unsub>",
		},
		{
			name:   "strip tag_unsubscribe_email alone",
			header: "<%tag_unsubscribe_email%>",
			want:   "",
		},
		{
			name:   "no tag_unsubscribe_email",
			header: "<https://example.com/unsub>",
			want:   "<https://example.com/unsub>",
		},
		{
			name:   "empty header",
			header: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanListUnsubscribe(tt.header)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
