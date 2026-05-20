# Security policy

## Supported versions

Salience is in active development. Only the latest `main` branch receives
security fixes today. A formal LTS line will appear when the project tags
its first stable release.

## Threat model

Salience is **local-first** by default:

- The dashboard binds to `127.0.0.1` unless you explicitly pass
  `-bind 0.0.0.0`.
- Provider API keys are read from the environment (`OPENAI_API_KEY`,
  `ANTHROPIC_API_KEY`, `PERPLEXITY_API_KEY`) or a local `.env` file. They
  are never persisted to the SQLite store and never displayed in the UI.
- All data — runs, samples, mentions, explanations, advice — lives in a
  single SQLite file on disk. No cloud sync, no telemetry, no analytics
  beacons.

The dashboard has no built-in authentication. **Do not expose it to the
internet.** If you need remote access, put it behind a reverse proxy with
auth (e.g. Cloudflare Tunnel + Access, Tailscale, Caddy with basic auth).

## Reporting a vulnerability

If you believe you have found a security vulnerability, please **do not**
open a public GitHub issue. Instead:

1. Email `security@zistica.com` with details.
2. Include reproduction steps, version (`salience version`), and impact
   assessment.
3. Allow up to 7 days for an initial response and 30 days for a fix
   before public disclosure.

We will credit reporters who would like to be acknowledged.

## What counts as a vulnerability

- Path traversal / arbitrary file read or write via the dashboard server
- API endpoints that bypass intended read-only / write-only constraints
- Credential leakage in logs, files, or HTTP responses
- SQL injection in the store layer
- XSS / injection in the embedded dashboard UI
- Supply-chain issues with our pinned dependencies

## What is *not* a vulnerability

- The dashboard has no auth — that is by design when bound to localhost.
- The dashboard can spend API credits on behalf of the running user — that
  is the point. Use `-max-cost` and `-dry-run` to gate spending.
- The detection algorithm can return false positives — these are tunable
  via brand aliases, not security issues.
