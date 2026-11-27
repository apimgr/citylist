# API reference

Citylist serves public, read-only geographic data. Lookup endpoints require no visitor account or API key. The default REST prefix is `/api/v1`; the configured version is available in `server.api_version`.

All successful JSON responses use the `ok`, `data`, and optional `count` envelope. Errors use `ok: false` with an `error` code and human-readable `message`. Requests are bounded and rate-limited to 120 requests per minute per client address by the current in-memory fixed-window limiter; limited responses include `Retry-After`.

Error codes stay stable across languages; the `message` is translated using the resolved request language. The server uses `BAD_REQUEST` and `VALIDATION_FAILED` for invalid input, `UNAUTHORIZED`, `TOKEN_EXPIRED`, and `TOKEN_INVALID` for authentication failures, `FORBIDDEN`, `CSRF_FAILED`, and `ACCOUNT_LOCKED` for denied requests, `NOT_FOUND`, `METHOD_NOT_ALLOWED`, `CONFLICT`, `RATE_LIMITED`, `MAINTENANCE`, and `SERVER_ERROR` for the corresponding HTTP outcomes.

## Cities

| Method and path | Query/body | Purpose and limits |
|---|---|---|
| `GET /api/v1/cities` | `page` (default 1, 1–1,000,000), `limit` (default 50, 1–100) | Paginated city records. |
| `GET /api/v1/cities/search` | `q` (2–100 characters), optional `country` (two-letter ISO code), `limit` (default 20, 1–100) | Search city names. |
| `GET /api/v1/cities/{id}` | GeoNames integer ID in the path | Look up one city. |
| `GET /api/v1/cities/country/{code}` | Two-letter ISO country code, optional `limit` (1–100) | List cities in a country. |
| `GET /api/v1/cities/coordinates` | `latitude` (-90–90), `longitude` (-180–180) | Find the nearest city. |
| `POST /api/v1/cities/coordinates` | JSON object with `latitude` and `longitude` | Same nearest-city query; request body is limited to 1 MiB. This route does not write data. |
| `GET /api/v1/cities/nearby` | `lat`, `lon`, `radius` (greater than 0 and at most 20,000 km), `limit` (1–100) | Find cities within a radius. Defaults: `radius=50`, `limit=10`. |
| `GET /api/v1/random` | — | Return one random city. |
| `GET /api/v1/random.txt` | — | Return one random city as plain text. |

## United States ZIP codes

| Method and path | Query/body | Purpose and limits |
|---|---|---|
| `GET /api/v1/zipcodes/{zip}` | Five-digit ZIP code | Look up one ZIP location. |
| `GET /api/v1/zipcodes/search` | `q` (2–100 characters), optional `state`, `limit` (default 20, 1–100) | Search ZIP locations by city and optional state. |
| `GET /api/v1/zipcodes/state/{state}` | Two-letter state abbreviation; `page` (default 1, 1–100,000), `limit` (default 100, 1–1,000) | Paginated ZIP locations for a state. |
| `GET /api/v1/zipcodes/nearby` | `lat`, `lon`, `radius_km`, `limit` (default 10, 1–100) | Find ZIP locations near coordinates. Latitude, longitude, and radius are range-validated. |

If the embedded postal dataset cannot be loaded, ZIP routes return `503` with the canonical `MAINTENANCE` error code.

## Dataset and health endpoints

| Method and path | Purpose |
|---|---|
| `GET /api/v1/` | API description and discovered API endpoints. |
| `GET /api/v1/stats` | Dataset statistics. |
| `GET /api/v1/stats.txt` | Explicit plain-text statistics endpoint. |
| `GET /api/v1/count` | City count. |
| `GET /api/v1/count.txt` | Explicit plain-text city count. |
| `GET /api/v1/server/healthz` | Public status, branding, build/runtime metadata, feature state, ordered component and dataset checks, and aggregate request statistics. |
| `GET /api/healthz` | Direct health alias. |
| `GET /healthz` | Optional root alias, disabled unless `server.healthz.root.enabled` is true. |
| `GET /api/autodiscover` | Public server details, configured `cli_min_version`, and the published CLI version/checksum map. The map is currently empty because the server has no defined CLI asset source. |

`GET /server/healthz` is the browser-facing health page and negotiates HTML, JSON, or text. Health checks are cached and ordered as database, cache, disk, scheduler, enabled overlays, then registered datasets. Tor and I2P report `disabled`, `starting`, `healthy`, or `unavailable`; an unavailable active overlay marks the overall service `degraded`. `healthy`, `degraded`, and `restart_required` return HTTP 200; `unhealthy`, `maintenance`, and `shutting_down` return HTTP 503. Health responses do not expose manager error strings, local paths, or credentials.

## Informational pages and discovery

The public server-rendered pages include `/server/about`, `/server/help`, `/server/help/mcp`, `/server/contact`, `/server/security`, `/server/security/policy`, `/server/security/thanks`, `/server/privacy`, `/server/dpo`, `/server/terms`, and `/server/preferences`. JSON mirrors are available under `/api/v1/server/` for the informational pages, including the policy and acknowledgments pages; the report-status page is deliberately GET-only, allows one token-authorized view per day, and has no API mirror. `/server/preferences/tokens` is the public, credential-gated token management page.

| Surface | Route |
|---|---|
| OpenAPI JSON | `/api/v1/server/swagger`, `/api/swagger` |
| Swagger documentation page | `/server/docs/swagger` |
| GraphQL | `GET` or `POST /api/graphql`, `/api/v1/server/graphql` |
| GraphQL query explorer | `/server/docs/graphql` |
| MCP Streamable HTTP | `POST /api/v1/server/mcp`, with `/api/mcp` and `/mcp` aliases |
| Locale catalog JSON | `/locales/{en,es,fr,de,it,ar}.json` |
| API discovery for language models | `/.well-known/llms.txt`, `/llms.txt` |
| Security reporting metadata | `/.well-known/security.txt` |
| Project OpenPGP key | `/.well-known/pgp-key.asc` (only when a key exists and publication is enabled) |
| Browser manifest and service worker | `/manifest.json`, `/sw.js` |

GraphQL accepts POST JSON request bodies and form-encoded `query` submissions. GET accepts `query`, optional JSON-encoded `variables`, and optional `operationName`; it only executes the read-only query schema. A GET without `query` returns a short usage message. Request bodies and GET parameter values are capped at 1 MiB. The server-rendered explorer links directly to the GET endpoint for no-JavaScript clients. MCP uses Streamable HTTP revision `2026-07-28`, requires `Authorization: Bearer <token>` on every request, and ignores cookies. Issue an owner token using `POST /api/v1/server/preferences/tokens/primary`, then create a linked token for each MCP client on `/server/preferences/tokens`. Connection instructions are at `/server/help/mcp`.

The `/server/docs/swagger` and `/server/docs/graphql` pages load their pinned Swagger UI and GraphiQL libraries from the server's own static assets. Each page includes a `<noscript>` card linking to the corresponding plain JSON endpoint.

### Server APIs

The following server routes are JSON APIs under the default `/api/v1` prefix. The versioned prefix follows `server.api_version`; aliases listed here are served directly by the same handler, without a redirect.

| Method and path | Access | Purpose |
|---|---|---|
| `GET /api/v1/server/about`, `/api/v1/server/help`, `/api/v1/server/help/mcp`, `/api/v1/server/security`, `/api/v1/server/privacy`, `/api/v1/server/dpo`, `/api/v1/server/terms` | Public | JSON mirrors of the informational pages; supported text negotiation follows the server response rules. |
| `GET /api/v1/server/security/policy`, `/api/v1/server/security/thanks` | Public | Coordinated-disclosure policy and researcher acknowledgments. |
| `GET`, `POST /api/v1/server/contact` | Public | Contact form metadata and bounded report submission. The form is for security reports, terms violations, DMCA notices, and privacy requests. |
| `GET`, `POST /api/v1/server/preferences` | Public | Read or update the caller's guest preference cookies. The API route does not use browser CSRF tokens. |
| `GET /api/v1/server/preferences/sync` | Public | Export supported guest preferences, a canonical URL, and a short code. |
| `GET /api/v1/server/preferences/sync/import` | Public | Import query or code values and return the applied and ignored settings. |
| `POST /api/v1/server/preferences/tokens/primary` | Public, rate-limited | Issue an anonymous primary owner token. |
| `GET /api/v1/server/preferences/tokens` | Primary owner Bearer token | List owner and linked tokens with values masked. |
| `POST /api/v1/server/preferences/tokens` | Primary owner Bearer token | Create a linked token. |
| `POST /api/v1/server/preferences/tokens/primary/{action}` | Primary owner Bearer token | Reveal or rotate the primary token. |
| `POST`, `PATCH`, `DELETE /api/v1/server/preferences/tokens/{id}[/{action}]` | Primary owner Bearer token | Reveal, rotate, update, or revoke a linked token. |
| `GET /api/v1/server/auth/check` | Bearer token | Validate an operator or owner token without returning its value. |
| `GET /api/v1/server/swagger`, `/api/swagger` | Public | OpenAPI JSON. |
| `GET /api/v1/server/healthz`, `/api/healthz` | Public | Machine-readable health response. |
| `GET`, `POST /api/v1/server/graphql`, `/api/graphql` | Public | Read-only GraphQL queries; GET uses `query`, optional `variables`, and `operationName` parameters. |
| `POST /api/v1/server/mcp`, `/api/mcp`, `/mcp` | Owner or operator Bearer token | MCP Streamable HTTP lookup tools. |

The unversioned `/api/autodiscover` endpoint returns server details, the configured minimum CLI version, and the published CLI version/checksum map. No public API mirrors the emailed report-status page. Owner-token routes ignore cookies and require explicit Bearer authentication, except public primary-token issuance.

## Browser preferences

`GET` or `HEAD /server/preferences` renders guest controls for theme, language, cookie-consent categories, and (when `server.privacy.data.sold` is true) CCPA opt-out. Save and reset are `POST /server/preferences`; reset removes non-credential cookies and clears browser storage. The More preferences card links token management and preference sync. Choosing `lang=auto` removes the `lang` cookie; an explicit supported language saves it for one year when preference-cookie consent is enabled. A valid `?lang=CODE` query selects the language for that request and saves it in the one-year `lang` cookie only when that consent is enabled. Preference cookies use `Path=/`, `SameSite=Lax`, and `Secure` when the request is resolved as HTTPS. The browser preferences JSON mirror is `GET` and `POST /api/v1/server/preferences`; POST accepts JSON or form data, does not require CSRF, applies valid values, and returns `400 VALIDATION_FAILED` if none validate.

Preference sync is available at `GET /server/preferences/sync`, `POST /server/preferences/sync`, and `GET /server/preferences/sync/import`, with JSON mirrors `GET /api/v1/server/preferences/sync` and `GET /api/v1/server/preferences/sync/import`. The web page exports a canonical import URL and a base64url code, and accepts a URL, query string, or code in its CSRF-protected form. Import is additive. Only `theme`, `lang`, and valid `citylist_pref_*` values are exported or applied; consent, CCPA, build, CSRF, and credential cookies are excluded. Sync imports set preference cookies for one year and never require an account.

Owner token management is available at `/server/preferences/tokens` and `/api/v1/server/preferences/tokens`. Token `id` values are opaque UUID v7 identifiers used by linked-token routes; the database stores that same public identifier without an integer key. The primary token can be created or restored in the browser; a primary bearer token is required for API token listing and management. See [Integrations](integrations.md) for owner-token use with MCP.

The browser token page also provides CSRF-protected POST actions: `/server/preferences/token/create`, `/server/preferences/token/restore`, `/server/preferences/token/forget`, `/server/preferences/token/reveal`, `/server/preferences/token/rotate`, and `/server/preferences/token/revoke`. Linked-token creation uses `POST /server/preferences/tokens`; reveal, rotate, update, and revoke use `POST /server/preferences/tokens/{id}/{action}`. Owner-cookie authentication is limited to these web forms. Token issue, reveal, rotate, and linked-token creation redirect with `303`; a scoped, one-use cookie makes the new or revealed token available on the next token-page load. The API uses Bearer authentication and returns raw tokens only from issue, reveal, and rotate operations.

| Method and path | Authorization | Purpose |
|---|---|---|
| `POST /api/v1/server/preferences/tokens/primary` | No token; public issuance is rate-limited | Issue a primary owner token. |
| `GET /api/v1/server/preferences/tokens` | Primary owner Bearer token | List the primary and linked tokens, with token values masked. |
| `POST /api/v1/server/preferences/tokens` | Primary owner Bearer token | Create a linked token. |
| `POST /api/v1/server/preferences/tokens/primary/{action}` | Primary owner Bearer token | `reveal` or `rotate` the primary token. |
| `DELETE /api/v1/server/preferences/tokens/primary` | Primary owner Bearer token and JSON `confirm` value | Revoke the primary token and delete its owner and linked tokens. The response reports deleted token and resource counts. |
| `POST /api/v1/server/preferences/tokens/{id}/{action}` | Primary owner Bearer token | `reveal`, `rotate`, `update`, or `revoke` a linked token. |
| `PATCH /api/v1/server/preferences/tokens/{id}` | Primary owner Bearer token | Update linked-token name or expiry. |
| `DELETE /api/v1/server/preferences/tokens/{id}` | Primary owner Bearer token | Revoke a linked token. |

The owner-token cookie name is `citylist_owner_token_lgqdhqtw`, the stable value recorded in `IDEA.md`. A visitor can issue a primary owner token with `POST /server/preferences/token/create` or `POST /api/v1/server/preferences/tokens/primary`, and manage linked tokens from the token page. Revoking a primary token requires the last four characters as the confirmation value and deletes the owner plus its linked tokens; Citylist currently has no owner-owned resource records. REST lookup endpoints remain public; MCP requires a valid Bearer token. See the route tables below for the owner-token API actions.

Server-rendered forms issue a `csrf_token` cookie and include the same random 32-byte base64url value in the form. Browser POST requests require a matching value in the `csrf_token` field or `X-CSRF-Token` header; missing or mismatched tokens receive `403`. The cookie is `Path=/`, lasts 12 hours, uses `SameSite=Strict`, and is Secure on HTTPS; it is not HttpOnly. Frontend POST bodies are capped at 1 MiB. Preference redirects are restricted to safe local paths.

`GET /api/v1/server/auth/check` is a machine-only check for a Bearer token. A valid operator token returns `data.kind: "operator"`; a valid primary or linked owner token returns `data.kind: "owner"`. Invalid or missing credentials return `401`. The response never echoes a credential. This check does not authenticate public data lookups.

The server-rendered consent banner POSTs choices to `/server/consent` with the page CSRF token. Choices are accept, decline, or granular `save` with `preferences` and `analytics` fields. The server stores the consent categories and timestamp in the `cookie_consent` cookie. Preference cookies are written only when preference consent is granted. Analytics tracking is off unless a supported provider is configured in `server.tracking` and analytics consent is granted. Tracking is also suppressed for Global Privacy Control (`Sec-GPC: 1`), a CCPA opt-out, and Tor/I2P requests. Configured tracking may load provider resources; see [Configuration](configuration.md). The tracking bootstrap script is served at `/server/tracking.js` only when tracking is allowed. When `server.privacy.data.sold` is true, the privacy page can POST an opt-out choice to `/server/ccpa`; this route is absent when data is not sold. Both browser POST routes use CSRF protection and safe local redirects.

## Content negotiation and text responses

- JSON is the default API representation.
- `Accept: text/plain` converts supported JSON or HTML responses to readable text.
- Append `.txt` to API routes for plain-text output, except the explicit `/stats.txt` and `/count.txt` routes.
- Browser pages remain HTML unless text is explicitly requested.
- `/robots.txt`, `/.well-known/security.txt`, and the LLM discovery documents are plain text.

## Examples

```sh
curl -q -LSsf -H 'Accept: application/json' 'http://YOUR_SERVER/api/v1/cities/search?q=London&country=GB'
curl -q -LSsf 'http://YOUR_SERVER/api/v1/cities/coordinates?latitude=51.5&longitude=-0.1'
curl -q -LSsf 'http://YOUR_SERVER/api/v1/zipcodes/10001'
curl -q -LSsf -H 'Accept: text/plain' 'http://YOUR_SERVER/api/v1/stats'
curl -q -LSsf 'http://YOUR_SERVER/api/v1/stats.txt'
curl -q -LSsf 'http://YOUR_SERVER/.well-known/llms.txt'
```

For GraphQL examples and MCP client setup, use `/server/docs/graphql` and `/server/help/mcp` on a running Citylist instance.
