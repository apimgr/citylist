# Security and privacy

Citylist serves public, read-only geographic data. Visitors do not create accounts or log in. Public lookup routes are anonymous. The `POST /api/v1/cities/coordinates` route accepts a query body but does not mutate data. On first server startup, Citylist generates an operator token and a 32-byte encryption key in `secrets.yml` with mode `0600`; the operator token is printed once to standard output. Anonymous visitors can issue owner tokens for token-gated capabilities such as MCP and manage linked tokens. `GET /api/v1/server/auth/check` validates operator and owner Bearer tokens without echoing them. CORS defaults to `*` and does not enable credentialed cross-origin requests.

The HTTP server applies request timeouts, response security headers, input validation, GraphQL POST-body and GET-parameter limits, and an in-memory fixed-window limit of 120 requests per minute per client address. It returns `429` and `Retry-After` when a client exceeds the limit. Forwarded client-IP headers are accepted only when the immediate peer is a private, loopback, link-local, or unspecified IP address. CORS is controlled by `server.cors.allowed_origins`, `server.cors.allow_credentials`, and `server.cors.max_age`; the default origin is `*`, and wildcard origins are not returned with credential permission. Health responses contain public version/status information and no detailed internal state.

Server-rendered forms use a double-submit CSRF token. A page render issues a random 32-byte token in both a `csrf_token` cookie and the form. Frontend POST requests require the matching value in the `csrf_token` field or `X-CSRF-Token` header; malformed or missing tokens receive `403`. The cookie is scoped to `/`, expires after 12 hours, uses `SameSite=Strict`, is Secure on resolved HTTPS, and is not HttpOnly. Frontend POST request bodies are capped at 1 MiB. API routes are outside this CSRF check and use explicit Bearer authentication where required. The owner-token cookie is HttpOnly, Secure on HTTPS, and SameSite=Strict.

Contact and security-report forms accept untrusted personal information and attachments. Their submissions use bounded fields, CAPTCHA and anti-abuse checks. Security-report contents and contact details retained for report follow-up are encrypted in the local database; report status links require a token usable once per day and are GET-only. The consent banner is server-rendered across pages; its accept, decline, and granular-save actions POST to `/server/consent` with CSRF protection. Preference cookies such as theme and language are written only when the visitor grants preference consent. Analytics consent is recorded, but Citylist currently has no configured analytics provider or tracker. `/server/ccpa` records a sale opt-out only when `server.privacy.data.sold` is enabled. Configured SMTP recipients may receive submitted contact and report details; webhook destinations receive summaries only and never receive security-report bodies or submitter contact details. For onion or I2P requests, clearnet webhook delivery is skipped while the submission remains stored. External CAPTCHA verification is used only when reCAPTCHA or hCaptcha is configured; public geographic lookups do not call external providers.

Global Privacy Control is honored by default. A request with `Sec-GPC: 1` sets a request-scoped opt-out, suppresses behavioral analytics and non-essential preference/consent cookies, records `compliance.gpc_honored` in the audit log, and displays a translated notice on the privacy and preferences pages. Set `web.headers.honor_sec_gpc: false` only when the deployment deliberately disables this behavior.

Do Not Track is disabled by default. Set `web.headers.honor_dnt: true` to honor `DNT: 1` for a request by suppressing behavioral analytics and preference/analytics cookies, recording `compliance.dnt_honored`, and showing a translated notice. DNT does not change the visitor's separate data sale or sharing choice.

Citylist does not store user profiles. Search terms and coordinates are processed to answer the request; the application has no user profile or analytics store. Operators control the deployment, network-level telemetry, and access-log retention. Security reporting points to GitHub's private vulnerability reporting, the contact form, and the configured security email. When no email override is set, the generated `security.txt` uses `security@{fqdn}`.

## Public security and discovery endpoints

| Path | Status | Purpose |
|---|---|---|
| `/.well-known/security.txt` | Enabled | RFC 9116 security contact file. This is the canonical path. |
| `/server/security` | Enabled | Human-readable security policy and private-reporting link. |
| `/server/security/policy` | Enabled | Coordinated-disclosure policy. Default policy text can be replaced with `web.security.policy` Markdown. |
| `/server/security/thanks` | Enabled | Researcher acknowledgments for reports disclosed with credit. |
| `/server/security/report/{trackingID}` | Enabled | GET-only report status with a token query value usable once per day; no API mirror. |
| `/.well-known/pgp-key.asc` | Conditional | ASCII-armored project OpenPGP public key; available when a keypair exists and `web.security.publish_pgp_key` is true. |
| `/.well-known/llms.txt` | Enabled | Plain-text API and feature discovery for automated clients. |
| `/llms.txt` | Enabled alias | Same discovery document as `/.well-known/llms.txt`. |
| `/api/v1/server/auth/check` | Enabled | Machine-only operator/owner bearer-token validation; returns the token kind and never echoes credentials. |
| `/server/preferences/tokens` | Enabled | Create or restore an owner token; management actions require the primary owner token and CSRF protection. |
| `/server/healthz` | Enabled | Public service health page; no authentication. |
| `/api/v1/server/healthz` | Enabled | API health response. |
| `/api/healthz` | Enabled | Direct machine-friendly alias of the API health response. |
| `/healthz` | Disabled by default | Optional direct alias, enabled with `server.healthz.root.enabled: true`. |

The non-canonical `/security.txt` path returns `404`. Unknown `/.well-known/*` paths return `404`; the namespace does not expose a directory listing or arbitrary files. OpenPGP key publication is conditional as described above. ACME challenges, WebFinger, OIDC metadata, app associations, and MTA-STS are not served.

## Reporting a vulnerability

Use the private vulnerability reporting link in `/.well-known/security.txt` or the human-readable `/server/security` page. The security page also links to `/server/security/policy` and `/server/security/thanks`. Do not include credentials or other secrets in a report. When the project OpenPGP key is published at `/.well-known/pgp-key.asc`, reports can include a researcher public key for encrypted acknowledgment; report bodies are encrypted at rest with the project key when available, otherwise with the server encryption key.

## Security configuration and key maintenance

`web.security.report_url`, `web.security.contact`, and `web.security.expires` supply the disclosure link, contact, and RFC 9116 expiry. `web.security.policy` replaces the built-in coordinated-disclosure policy with sanitized Markdown. `web.security.keyservers` lists OpenPGP keyservers, and `web.security.publish_pgp_key` controls public-key publication. `server.contact.security.email` is the fallback security contact and the project key identity address. CORS is configured separately under `server.cors`.

OpenPGP key storage and the `citylist --maintenance pgp` command support generation, rotation, publication, public/private export, import, and deletion. Sensitive operations require root or operator-token authorization, interactive confirmations where destructive, and audit logging. Private-key exports are rate-limited and written with private file permissions. The public key route remains conditional on a keypair existing and `web.security.publish_pgp_key` being enabled. Key publication sends JSON to keys.openpgp.org and form data to HKP servers such as keyserver.ubuntu.com. Network errors, HTTP 429, and server errors are retried with persisted exponential backoff, up to eight attempts; other HTTP client errors are recorded as final. Retry state is stored privately in `config/security/keyservers.state` and processed by the built-in scheduler. See the command's `help` action for its current usage.
