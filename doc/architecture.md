# Architecture

## Overview

```
Ghost (newsletter send)
  │
  │  POST /v3/{domain}/messages  (multipart/form-data)
  │  Authorization: Basic api:{key}
  ▼
┌─────────────────────────┐
│  ghost-mailgun-proxy    │
│  (Go net/http)          │
│                         │
│  1. Validate auth token │
│  2. Parse form data     │
│  3. Parse recipient-    │
│     variables JSON      │
│  4. For each recipient: │
│     - Replace           │
│       %recipient.X%     │
│     - Send via SMTP     │
│  5. Return Mailgun-     │
│     compatible response │
└────────────┬────────────┘
             │
             │  SMTP (port 587/465/25)
             ▼
        SMTP Server
  (Mailpit dev / any prod)
```

## Components

| File | Responsibility |
|---|---|
| `main.go` | Entry point, config parsing, HTTP server setup, health check endpoint |
| `handler.go` | Mailgun API endpoint handlers: send, events, suppression delete |
| `smtp.go` | SMTP connection, MIME message construction, email delivery |
| `replace.go` | `%recipient.X%` template placeholder replacement |

## Request Flow

1. Ghost sends `POST /v3/{domain}/messages` with `multipart/form-data`.
2. The proxy validates the `Authorization: Basic` header (username must be `api`, key compared in constant time).
3. Form fields are parsed — `to`, `from`, `subject`, `html`, `text`, headers (`h:*`), and `recipient-variables` JSON.
4. For each recipient in `to`:
   - The `html`, `text`, and `subject` bodies are copied.
   - All `%recipient.X%` placeholders are replaced with values from the recipient's entry in `recipient-variables`.
   - Mailgun-specific `<%tag_unsubscribe_email%>` tokens are stripped from `List-Unsubscribe`.
   - A `multipart/alternative` MIME message is composed with both `text/plain` and `text/html` parts.
   - The message is sent via SMTP.
5. A Mailgun-compatible JSON response is returned: `{"id": "<uuid@domain>", "message": "Queued. Thank you."}`.
6. If any recipient fails, the error is logged but sending continues for remaining recipients. HTTP 500 is only returned if all recipients fail.

## Configuration

All configuration is via environment variables:

| Variable | Required | Default | Description |
|---|---|---|---|
| `PROXY_LISTEN` | no | `:8787` | HTTP listen address |
| `PROXY_API_KEY` | yes | — | API key Ghost sends (validated against `Authorization` header) |
| `SMTP_HOST` | yes | — | SMTP server hostname |
| `SMTP_PORT` | no | `587` | SMTP server port |
| `SMTP_USER` | no | — | SMTP auth username (skip auth if empty) |
| `SMTP_PASS` | no | — | SMTP auth password |
| `SMTP_TLS` | no | `starttls` | `starttls`, `tls`, or `none` |
| `SMTP_TIMEOUT` | no | `30s` | SMTP dial and command deadline |
| `SMTP_FROM_OVERRIDE` | no | — | Override `from` address for SMTP envelope sender |

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v3/{domain}/messages` | Receive email from Ghost, send via SMTP |
| `GET` | `/v3/{domain}/events` | Stub — returns empty events (Ghost polls for delivery stats) |
| `DELETE` | `/v3/{domain}/{type}/{email}` | Stub — returns 200 OK (Ghost suppression list management) |
| `GET` | `/healthz` | Health check (used by Docker healthcheck) |

## Deployment

The proxy runs as a Docker container alongside Ghost on a private Docker network (`ghost-tier`). It is not exposed to the host or the public internet.

```yaml
mailgun-proxy:
  image: ghcr.io/nogo/ghost-mailgun-proxy:latest
  restart: always
  environment:
    PROXY_API_KEY: "${MAILGUN_PROXY_API_KEY}"
    SMTP_HOST: "${SMTP_HOST}"
    SMTP_PORT: "${SMTP_PORT:-587}"
    SMTP_USER: "${SMTP_USER}"
    SMTP_PASS: "${SMTP_PASS}"
    SMTP_TLS: "${SMTP_TLS:-starttls}"
  expose:
    - 8787
  networks:
    - ghost-tier
```

Ghost is configured to point at the proxy via `mailgun_base_url` (set in `config.production.json` or Ghost Admin):

```json
{
  "bulkEmail": {
    "mailgun": {
      "apiKey": "your-proxy-api-key",
      "domain": "romanticbake.mrssoca.de",
      "baseUrl": "http://mailgun-proxy:8787"
    }
  }
}
```

## Security Boundaries

- **Network isolation.** The proxy is only reachable on the internal Docker network.
- **API key authentication.** Basic Auth with constant-time key comparison.
- **Header injection protection.** CR/LF characters are rejected in all header-bound values.
- **Payload limits.** Multipart parsing is capped at 32 MiB; HTTP read/header/idle timeouts are enforced.
- **SMTP deadlines.** Dial and command timeouts prevent hanging on slow SMTP servers.

## Why Go

- Single static binary, tiny Docker image (~10 MB on scratch).
- `net/http` and `net/smtp` are in the stdlib — no runtime dependencies.
- Matches the "set and forget" nature of this proxy.

## What's Not Implemented (and why)

| Feature | Status | Impact |
|---|---|---|
| Open tracking | Not implemented | No open rate stats in Ghost dashboard |
| Click tracking | Not implemented | No click rate stats |
| Bounce handling | Stubbed | Failed addresses won't auto-suppress |
| Delivery events | Stubbed | Email status stays "submitted" in Ghost |
| Batch scheduling | Ignored | Emails send immediately |
| Test mode | Ignored | No dry-run capability |

None of these affect actual email delivery. Newsletters send and arrive correctly for small subscriber lists.
