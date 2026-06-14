# Project: Ghost Mailgun SMTP Proxy

## Outcome

A lightweight Go HTTP service that implements the subset of the Mailgun REST API used by Ghost and forwards emails via SMTP. Ghost believes it is talking to Mailgun, but the proxy translates Mailgun API calls into SMTP deliveries to any SMTP server.

## Value

- **No Mailgun account needed.** Small Ghost blogs can use newsletter email without signing up for a third-party email provider.
- **Full control.** Send via your own SMTP server, local mail server, or any relay you trust.
- **Minimal footprint.** Single static binary, ~10 MB Docker image, no runtime dependencies.
- **Self-contained.** Drops into the existing Ghost Docker Compose stack with one service entry.

## Problem

Ghost hardcodes Mailgun as the only newsletter email provider. It uses the `mailgun.js` SDK (v10.4.0) to call Mailgun's REST API. While Ghost exposes a `mailgun_base_url` override, there was no community proxy that translates these calls to SMTP for small self-hosted deployments.

## Constraints

- **Go stdlib only.** No external HTTP framework or SMTP library — `net/http`, `net/smtp`, `mime/multipart`, and friends.
- **Small audience target.** Designed for <20 subscribers. Sequential per-recipient sending, no batching, no delivery tracking.
- **Mailgun API compatibility.** Must respond with the exact shape Ghost's `mailgun-client.js` expects, including the `id` / `message` JSON envelope for sends and empty events for polling.
- **Docker-native.** Built and deployed as a container alongside Ghost. No systemd, no host-level installs.
- **Internal network only.** The proxy is never exposed to the public internet; it's reachable only by Ghost on the Docker network.

## Non-Goals

- **Not a general Mailgun replacement.** We implement only the endpoints Ghost calls: send email, fetch events, and delete suppression.
- **Not a full email platform.** No open/click tracking, no bounce auto-suppression, no delivery event logging, no batch scheduling.
- **Not a high-throughput proxy.** One SMTP connection per recipient, no pooling, no concurrency. Good enough for small lists, not designed for scale.
- **Not a drop-in for all Mailgun integrations.** This proxy is specifically shaped around what Ghost sends.
