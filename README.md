# Ghost Mailgun SMTP Proxy

A lightweight HTTP service that implements the Mailgun API surface used by Ghost and forwards emails via SMTP. Built for small self-hosted Ghost blogs that don't need (or want) a Mailgun account.

## Quick Start

```bash
PROXY_API_KEY=my-secret-key \
SMTP_HOST=mail.example.com \
SMTP_PORT=587 \
SMTP_USER=user@example.com \
SMTP_PASS=password \
SMTP_TLS=starttls \
go run .
```

The proxy listens on `:8787` by default.

## Configuration

All settings are environment variables:

| Variable | Required | Default | Description |
|---|---|---|---|
| `PROXY_LISTEN` | no | `:8787` | HTTP listen address |
| `PROXY_API_KEY` | yes | — | API key (matched against Ghost's Mailgun API key) |
| `PROXY_DEBUG` | no | `false` | Log per-recipient email addresses for troubleshooting |
| `PROXY_MAX_RECIPIENTS` | no | `100` | Maximum recipients accepted in one Ghost batch |
| `SMTP_HOST` | yes | — | SMTP server hostname |
| `SMTP_PORT` | no | `587` | SMTP server port |
| `SMTP_USER` | no | — | SMTP username (skip auth if empty) |
| `SMTP_PASS` | no | — | SMTP password |
| `SMTP_TLS` | no | `starttls` | `starttls`, `tls`, or `none` |
| `SMTP_TIMEOUT` | no | `30s` | SMTP dial and command deadline |
| `SMTP_HELO` | no | — | Custom SMTP EHLO/HELO name |
| `SMTP_FROM_OVERRIDE` | no | — | Override SMTP envelope sender |

By default, logs contain only aggregate send counts and do not include subscriber email addresses. Set `PROXY_DEBUG=true` only while troubleshooting.

For each Ghost batch request, the proxy opens one SMTP connection and sends each personalized recipient message over that session. This avoids one SMTP login per subscriber and is friendlier to mail-server connection limits.

Requests with more than `PROXY_MAX_RECIPIENTS` recipients are rejected with `400`. For substantially larger lists, use a durable queue rather than raising this limit indefinitely.

## Ghost Configuration

In Ghost Admin (Settings > Email newsletter) or in `config.production.json`:

- **Mailgun domain**: Any string (e.g. your blog domain)
- **Mailgun API key**: Same value as `PROXY_API_KEY`
- **Mailgun base URL**: `http://mailgun-proxy:8787` (Docker network name)

Using `config.production.json` (recommended, survives database resets):

```json
{
  "bulkEmail": {
    "mailgun": {
      "apiKey": "your-proxy-api-key",
      "domain": "your-domain.example.com",
      "baseUrl": "http://mailgun-proxy:8787"
    }
  }
}
```

## Docker

Build and run:

```bash
docker build -t ghost-mailgun-proxy .
docker run -e PROXY_API_KEY=key -e SMTP_HOST=mail.example.com ghost-mailgun-proxy
```

Images are published to GitHub Container Registry by the `Publish container` workflow for version tags matching `v*` whose commit is on `main`, and by manual workflow dispatch:

```text
ghcr.io/nogo/ghost-mailgun-proxy
```

Published images include `linux/amd64` and `linux/arm64` platforms.

## Local Testing

### With Mailpit

```bash
PROXY_API_KEY=test-key \
SMTP_HOST=localhost \
SMTP_PORT=1025 \
SMTP_TLS=none \
go run .
```

### Manual curl test

```bash
curl -X POST http://localhost:8787/v3/test.com/messages \
  -u "api:test-key" \
  -F to='test@example.com' \
  -F from='blog@example.com' \
  -F subject='Test newsletter' \
  -F html='<h1>Hello %recipient.name%</h1>' \
  -F text='Hello %recipient.name%' \
  -F 'recipient-variables={"test@example.com":{"name":"Reader"}}'
```

### Unit tests

```bash
go test -race -count=1 ./...
```

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v3/{domain}/messages` | Send email via SMTP |
| `GET` | `/v3/{domain}/events` | Empty events (stub) |
| `DELETE` | `/v3/{domain}/{type}/{email}` | Suppression delete (stub) |
| `GET` | `/healthz` | Health check |

## Documentation

- [Project overview](doc/project.md) — Outcome, value, constraints, and non-goals
- [Architecture](doc/architecture.md) — Component diagram, request flow, deployment

## Limitations

Designed for small subscriber lists (<20). The following Mailgun features are not implemented:

- Open/click tracking
- Bounce auto-suppression
- Delivery event polling
- Batch scheduling

These don't affect email delivery — newsletters send and arrive correctly.
