package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "run healthcheck against running instance")
	flag.Parse()

	listen := envOr("PROXY_LISTEN", ":8787")

	if *healthcheck {
		resp, err := http.Get("http://127.0.0.1" + listen + "/healthz")
		if err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
			os.Exit(1)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
			os.Exit(1)
		}
		return
	}

	apiKey := os.Getenv("PROXY_API_KEY")
	if apiKey == "" {
		log.Fatal("PROXY_API_KEY is required")
	}

	smtpHost := os.Getenv("SMTP_HOST")
	if smtpHost == "" {
		log.Fatal("SMTP_HOST is required")
	}

	smtpPort, err := strconv.Atoi(envOr("SMTP_PORT", "587"))
	if err != nil {
		log.Fatalf("invalid SMTP_PORT: %v", err)
	}

	smtpTLS := envOr("SMTP_TLS", "starttls")
	if !validSMTPTLS(smtpTLS) {
		log.Fatalf("invalid SMTP_TLS %q: expected starttls, tls, or none", smtpTLS)
	}

	smtpTimeout, err := time.ParseDuration(envOr("SMTP_TIMEOUT", "30s"))
	if err != nil {
		log.Fatalf("invalid SMTP_TIMEOUT: %v", err)
	}

	fromOverride := os.Getenv("SMTP_FROM_OVERRIDE")
	if fromOverride != "" {
		_, fromOverride, err = parseAddressField("SMTP_FROM_OVERRIDE", fromOverride)
		if err != nil {
			log.Fatal(err)
		}
	}

	sender := &SMTPSender{Config: SMTPConfig{
		Host:         smtpHost,
		Port:         smtpPort,
		User:         os.Getenv("SMTP_USER"),
		Pass:         os.Getenv("SMTP_PASS"),
		TLS:          smtpTLS,
		FromOverride: fromOverride,
		Timeout:      smtpTimeout,
	}}

	mux := newMuxWithConfig(apiKey, sender, HandlerConfig{
		Debug: envBool("PROXY_DEBUG"),
	})

	server := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("listening on %s", listen)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func validSMTPTLS(mode string) bool {
	switch mode {
	case "starttls", "tls", "none":
		return true
	default:
		return false
	}
}
