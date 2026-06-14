package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"strings"
)

// newMux returns an http.Handler with all routes wired up.
func newMux(apiKey string, sender Sender) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("POST /v3/{domain}/messages", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(r, apiKey) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		messagesHandler(w, r, sender)
	})

	mux.HandleFunc("GET /v3/{domain}/events", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(r, apiKey) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		eventsHandler(w, r)
	})

	mux.HandleFunc("DELETE /v3/{domain}/{type}/{email}", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(r, apiKey) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		suppressionHandler(w, r)
	})

	return mux
}

func checkAuth(r *http.Request, apiKey string) bool {
	user, pass, ok := r.BasicAuth()
	return ok && user == "api" && constantTimeEqual(pass, apiKey)
}

func constantTimeEqual(a, b string) bool {
	aHash := sha256.Sum256([]byte(a))
	bHash := sha256.Sum256([]byte(b))
	equal := subtle.ConstantTimeCompare(aHash[:], bHash[:]) == 1
	return equal && len(a) == len(b)
}

func messagesHandler(w http.ResponseWriter, r *http.Request, sender Sender) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	from := r.FormValue("from")
	subject := r.FormValue("subject")
	html := r.FormValue("html")
	text := r.FormValue("text")
	replyTo := r.FormValue("h:Reply-To")
	listUnsub := r.FormValue("h:List-Unsubscribe")
	listUnsubPost := r.FormValue("h:List-Unsubscribe-Post")
	recipients := r.Form["to"]
	domain := r.PathValue("domain")

	fromHeader, fromAddr, err := parseAddressField("from", from)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	replyToHeader := ""
	if replyTo != "" {
		var err error
		replyToHeader, _, err = parseAddressField("h:Reply-To", replyTo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	if subject == "" {
		http.Error(w, "subject is required", http.StatusBadRequest)
		return
	}
	if hasHeaderBreak(subject) {
		http.Error(w, "subject contains invalid line break", http.StatusBadRequest)
		return
	}
	if html == "" && text == "" {
		http.Error(w, "html or text is required", http.StatusBadRequest)
		return
	}
	if len(recipients) == 0 {
		http.Error(w, "at least one recipient is required", http.StatusBadRequest)
		return
	}
	if hasHeaderBreak(listUnsubPost) {
		http.Error(w, "h:List-Unsubscribe-Post contains invalid line break", http.StatusBadRequest)
		return
	}

	var recipientVars map[string]map[string]string
	if rv := r.FormValue("recipient-variables"); rv != "" {
		if err := json.Unmarshal([]byte(rv), &recipientVars); err != nil {
			http.Error(w, "invalid recipient-variables", http.StatusBadRequest)
			return
		}
	}

	var failCount int
	for _, to := range recipients {
		toHeader, toAddr, err := parseAddressField("to", to)
		if err != nil {
			log.Printf("error: invalid recipient %q: %v", to, err)
			failCount++
			continue
		}

		vars := recipientVars[to]

		pSubject := replaceRecipientVars(subject, vars)
		pHTML := replaceRecipientVars(html, vars)
		pText := replaceRecipientVars(text, vars)
		pListUnsub := cleanListUnsubscribe(replaceRecipientVars(listUnsub, vars))
		if hasHeaderBreak(pSubject) {
			log.Printf("error: subject for %s contains invalid line break", toAddr)
			failCount++
			continue
		}
		if hasHeaderBreak(pListUnsub) {
			log.Printf("error: List-Unsubscribe for %s contains invalid line break", toAddr)
			failCount++
			continue
		}

		headers := make(map[string]string)
		if pListUnsub != "" {
			headers["List-Unsubscribe"] = pListUnsub
		}
		if listUnsubPost != "" {
			headers["List-Unsubscribe-Post"] = listUnsubPost
		}

		email := &Email{
			From:    fromHeader,
			To:      toHeader,
			Subject: pSubject,
			HTML:    pHTML,
			Text:    pText,
			ReplyTo: replyToHeader,
			Headers: headers,
		}

		msg, err := buildMessage(email)
		if err != nil {
			log.Printf("error: build message for %s failed: %v", toAddr, err)
			failCount++
			continue
		}
		if err := sender.Send(fromAddr, toAddr, msg); err != nil {
			log.Printf("error: send to %s failed: %v", toAddr, err)
			failCount++
			continue
		}
		log.Printf("sent to %s", toAddr)
	}

	if failCount == len(recipients) {
		http.Error(w, "all recipients failed", http.StatusInternalServerError)
		return
	}

	id := fmt.Sprintf("<%s@%s>", randomID(), domain)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id":      id,
		"message": "Queued. Thank you.",
	})
}

func eventsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items": []any{},
		"pages": map[string]any{
			"next":     map[string]string{"page": ""},
			"previous": map[string]string{"page": ""},
		},
	})
}

func suppressionHandler(w http.ResponseWriter, r *http.Request) {
	email := r.PathValue("email")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Suppression removed",
		"address": email,
	})
}

func randomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func parseAddressField(field, value string) (headerValue, smtpAddress string, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", fmt.Errorf("%s is required", field)
	}
	if hasHeaderBreak(value) {
		return "", "", fmt.Errorf("%s contains invalid line break", field)
	}
	addr, err := mail.ParseAddress(value)
	if err != nil {
		return "", "", fmt.Errorf("invalid %s address", field)
	}
	return addr.String(), addr.Address, nil
}

func hasHeaderBreak(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}
