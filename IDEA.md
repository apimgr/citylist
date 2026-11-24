## Project description

Citylist provides a global city and United States ZIP-code reference service through a browser application, REST and GraphQL APIs, MCP, and a companion command-line client. City records retain GeoNames identifiers, names, country codes, and coordinates; ZIP records provide postal codes, city, state, and coordinates. Lookups are public and read-only. Guest preferences and anonymous owner tokens support browser customization and token-gated integrations without visitor accounts.

## Project variables

project_name: citylist
project_org: apimgr
internal_name: citylist
internal_org: apimgr
app_name: CityList API
repo: https://github.com/apimgr/citylist
license: MIT
binary: citylist
client_binary: citylist-cli
api_version: v1
owner_token: citylist_owner_token_lgqdhqtw

## Business logic

### Product scope & non-goals

**In scope:**
- Global city list with GeoNames IDs (compatible with OpenWeatherMap city IDs)
- US zipcode dataset with city, state, and coordinates
- Search/filter cities by name and country code
- Lookup city by GeoNames ID; lookup US zipcode by postal code
- Browser application for city search, coordinate search, and ZIP lookups, with guest theme, language, consent, and conditional CCPA preferences
- Scheduled site-wide announcements rendered as accessible server-side banners, with optional visitor dismissal
- Six-language interface catalogs shared by the server and companion client
- Public pages: `/server/about`, `/server/help`, `/server/help/mcp`, `/server/contact`, `/server/security`, `/server/security/policy`, `/server/security/thanks`, `/server/security/report/{trackingID}`, `/server/healthz`, `/server/privacy`, `/server/dpo`, `/server/terms`, `/server/preferences`, `/server/preferences/tokens`, and `/server/preferences/sync`
- Anonymous owner and linked API tokens, including browser-based token management
- CLI client (`citylist-cli`) for queries from the terminal and graphical interface
- OpenAPI/Swagger at `/api/{api_version}/server/swagger` and `/server/docs/swagger`; GraphQL at `/api/graphql` and `/api/{api_version}/server/graphql` with a browser explorer at `/server/docs/graphql`; machine discovery at `/.well-known/security.txt`, `/.well-known/pgp-key.asc` (when a key exists and publication is enabled), and `/.well-known/llms.txt`
- MCP lookup tools for clients using owner or operator bearer tokens
- Cross-device export/import of theme, language, and supported guest preferences
- Operator CLI for release checks, checksum-verified manual updates, and stable/beta/devel channel selection

**Non-goals:**
- No visitor accounts, registration, or login
- No admin web panel (operator settings use `server.yml`, `yaml.d/` area files, and `secrets.yml`)
- No mutation API for the embedded city and ZIP datasets (they remain read-only)
- No real-time population, weather, or timezone data beyond what is in the embedded dataset
- No paid tiers or differentiated rate-limit tiers

### Roles & permissions

There are no visitor accounts, passwords, or login flow. Anonymous visitors may issue a primary owner token and linked tokens for token-gated integrations such as MCP. Public lookup and informational endpoints remain available without a token. Owner-token management requires the owner's primary token; operator-only maintenance uses the operator token or local-root authorization and is not exposed as a web administration interface.

| Actor | Access |
|-------|--------|
| **Anonymous visitor (browser)** | Read access to public pages and lookup features; guest preference controls; contact and security-report forms; may issue or restore an owner token |
| **Anonymous API client (curl/CLI)** | Read access to public data endpoints and contact-form APIs; may update only its guest preference cookies |
| **Owner token holder** | Manage the owner's primary and linked tokens and access token-gated integrations such as MCP |
| **Server operator** | Configures the server through `server.yml`, `yaml.d/` area files, and `secrets.yml`; privileged maintenance uses the operator token or local root |

### Data model & sensitivity

**City record** (embedded at build time, no PII):

| Field | Type | Sensitivity |
|-------|------|-------------|
| `id` | integer — GeoNames city ID | Public |
| `name` | string — city name | Public |
| `country` | string — ISO 3166-1 alpha-2 code | Public |
| `coord.lat` | float — latitude | Public |
| `coord.lon` | float — longitude | Public |

**US Zipcode record** (embedded at build time, no PII):

| Field | Type | Sensitivity |
|-------|------|-------------|
| `zip` | string — 5-digit postal code | Public |
| `city` | string | Public |
| `state` | string — abbreviation | Public |
| `lat` | float | Public |
| `lon` | float | Public |

City and ZIP lookup records contain no PII. Contact submissions may contain names, email addresses, and message content and are delivered to configured recipients. Security reports and any contact details are stored encrypted for follow-up; tracking credentials are stored as hashes. Owner and linked tokens are sensitive credentials: hashes support authentication and encrypted values support authorized reveal/rotation flows. Audit records may include client IP addresses and token-action metadata.

### Trust boundaries & external services

| Boundary | Trust level | Notes |
|----------|-------------|-------|
| City and zipcode datasets (embedded at build) | Fully trusted | Static, compiled into binary |
| Incoming HTTP requests | **Untrusted** | All query parameters validated |
| Contact and security-report submitters | **Untrusted** | Submitted fields and attachments are bounded and validated before delivery or storage |
| Configured SMTP relay and CAPTCHA provider | External | SMTP delivers contact/report notices; reCAPTCHA or hCaptcha is contacted only when that provider is configured |

City and ZIP lookup requests use only the embedded datasets and never call upstream data providers. Contact and report handling may send submitted data through the configured SMTP relay; CAPTCHA verification contacts the selected external provider only when configured. These services do not supply lookup results.

### Threat model & abuse cases

**Primary assets:** service availability, operator and owner credentials, and the confidentiality of submitted security reports and contact data.

**Attacker/abuser goals:**
- DoS via high-rate requests or expensive city name searches
- Bulk scraping of the full dataset
- Owner-token theft or unauthorized token-management actions
- Contact-form spam, malicious submissions, or disclosure of security-report contents

**Defenses:**
- Rate limiting on all endpoints
- Request size limits on all inputs
- Contact-specific anti-abuse checks, CAPTCHA validation, CSRF protection, and bounded uploads
- Hashed and encrypted token storage, encrypted security reports, and token checks for report-status access
- Anonymous access removes visitor account enumeration and credential-stuffing surfaces; privileged operator actions remain protected by the server operator token

### Security decisions & exceptions

- **No user accounts or login:** intentional. Public city and ZIP lookup routes do not require credentials. Operator-only management endpoints use the configured operator token and are not part of the public data API.
- **CORS is configurable under `server.cors`**: the default allowed origin is `*`; operators can restrict allowed origins, credentials, and preflight max age. Wildcard origins are not combined with credentialed CORS responses.

### API routes

The following routes are part of Citylist's user-facing product. `{api_version}` is the configured version segment (default `v1`). Public geographic lookups do not require a token and never mutate city or ZIP data. Browser preference and contact forms use CSRF protection. API preference operations affect only the caller's guest preference cookies; owner-token API operations require a primary owner Bearer token unless otherwise noted. JSON uses the `ok`/`data` envelope where applicable; supported pages and APIs negotiate text when requested.

| Method | Path | Auth | Purpose and request/response summary |
|--------|------|------|--------------------------------------|
| GET | `/` | Public | City lookup home page. |
| GET | `/search` | Public | City and ZIP search interface. |
| GET | `/coordinates` | Public | Coordinate-based city/ZIP lookup interface. |
| GET | `/server/about`, `/server/help`, `/server/help/mcp`, `/server/security`, `/server/privacy`, `/server/dpo`, `/server/terms` | Public | Informational pages, help, and privacy contact. |
| GET, POST | `/server/contact` | Public | Contact form and submission; bounded anti-abuse checks apply. |
| GET | `/server/security/policy`, `/server/security/thanks` | Public | Disclosure policy and public acknowledgments for researchers who consented to credit. |
| GET | `/server/security/report/{trackingID}` | `token` query value, usable once per day | View report status using the emailed link; this is GET-only and has no API mirror. |
| POST | `/server/consent` | Public, CSRF-protected | Save accepted, declined, or granular preference and analytics consent. |
| POST | `/server/ccpa` | Public, CSRF-protected; only when `server.privacy.data.sold` is true | Record or clear the CCPA sale opt-out. |
| GET | `/server/healthz` | Public | Public health/build status page; negotiates HTML, JSON, or text. |
| GET, HEAD | `/server/preferences` | Public | Guest preferences form. |
| POST | `/server/preferences` | Public, CSRF-protected | Save theme, language, consent, conditional CCPA opt-out, or reset; redirects to a safe local path. |
| GET | `/server/preferences/tokens` | Public page; owner actions credential-gated | Issue/restore primary owner token and manage primary/linked tokens in the browser. |
| POST | `/server/preferences/token/create`, `/server/preferences/token/restore`, `/server/preferences/token/forget`, `/server/preferences/token/reveal`, `/server/preferences/token/rotate`, `/server/preferences/token/revoke` | CSRF-protected; primary owner token required for reveal, rotate, and revoke | Browser primary-token create, restore, forget-this-browser, reveal, rotate, and confirmed revoke actions. |
| POST | `/server/preferences/tokens` | CSRF-protected; owner token required | Create a linked token. |
| POST | `/server/preferences/tokens/{id}/{action}` | CSRF-protected; owner token required | Reveal, rotate, update, or revoke a linked token. |
| GET | `/server/preferences/sync` | Public | Export current guest preferences as a URL and short code; HTML, text, or JSON by negotiation. |
| POST | `/server/preferences/sync` | Public, CSRF-protected | Import preferences from a URL, query string, or short code and redirect safely. |
| GET | `/server/preferences/sync/import` | Public | Import preferences from URL query parameters; redirects safely. |
| GET | `/server/docs/swagger`, `/server/docs/graphql` | Public | OpenAPI/Swagger and GraphQL browser documentation. |
| POST | `/api/graphql`, `/api/{api_version}/server/graphql` | Public | Read-only GraphQL query endpoint. |
| POST | `/api/{api_version}/server/mcp`, `/api/mcp`, `/mcp` | Bearer token required | MCP Streamable HTTP lookup tools; accepts owner or operator credentials. |
| GET | `/locales/{lang}.json`, `/static/*` | Public | Embedded locale catalog and static web assets. |
| GET | `/api/{api_version}/` | Public | API description and endpoint discovery. |
| GET | `/api/{api_version}/cities` | Public | Paginated city list (`page`, `limit`). |
| GET | `/api/{api_version}/cities/search` | Public | Search by `q`, optional country code and limit. |
| GET | `/api/{api_version}/cities/{id}` | Public | Look up a GeoNames city ID. |
| GET | `/api/{api_version}/cities/country/{code}` | Public | List cities by ISO country code. |
| GET, POST | `/api/{api_version}/cities/coordinates` | Public | Find the nearest city from coordinates; POST body is read-only and bounded. |
| GET | `/api/{api_version}/cities/nearby` | Public | Find nearby cities within a bounded radius. |
| GET | `/api/{api_version}/zipcodes/{zip}` | Public | Look up a five-digit US ZIP code. |
| GET | `/api/{api_version}/zipcodes/search` | Public | Search ZIP locations by city and optional state. |
| GET | `/api/{api_version}/zipcodes/state/{state}` | Public | Paginated ZIP locations by state. |
| GET | `/api/{api_version}/zipcodes/nearby` | Public | Find ZIP locations near coordinates. |
| GET | `/api/{api_version}/random`, `/api/{api_version}/random.txt` | Public | Return a random city as JSON or text. |
| GET | `/api/{api_version}/stats`, `/api/{api_version}/stats.txt` | Public | Dataset statistics as JSON or text. |
| GET | `/api/{api_version}/count`, `/api/{api_version}/count.txt` | Public | City count as JSON or text. |
| GET | `/api/{api_version}/server/about`, `/api/{api_version}/server/help`, `/api/{api_version}/server/help/mcp`, `/api/{api_version}/server/security`, `/api/{api_version}/server/privacy`, `/api/{api_version}/server/dpo`, `/api/{api_version}/server/terms` | Public | JSON or negotiated text mirrors of informational pages. |
| GET, POST | `/api/{api_version}/server/contact` | Public | Contact form metadata or bounded submission API. |
| GET, POST | `/api/{api_version}/server/preferences` | Public | Read or update guest preferences using JSON or form fields; no CSRF token is required. |
| GET | `/api/{api_version}/server/preferences/sync` | Public | Export preference state, canonical URL, and short code as JSON. |
| GET | `/api/{api_version}/server/preferences/sync/import` | Public | Import query or code values and return JSON. |
| GET | `/api/{api_version}/server/preferences/tokens` | Primary owner Bearer token | List owner tokens. |
| POST | `/api/{api_version}/server/preferences/tokens`, `/api/{api_version}/server/preferences/tokens/primary` | Primary owner Bearer token for linked-token creation; public for primary-token issuance | Create linked or primary owner tokens. |
| POST | `/api/{api_version}/server/preferences/tokens/primary/{action}` | Primary owner Bearer token | Reveal or rotate the primary token. |
| DELETE | `/api/{api_version}/server/preferences/tokens/primary` | Primary owner Bearer token and confirmation of the token's final four characters | Revoke the primary token and delete its owner and linked tokens. |
| POST, PATCH, DELETE | `/api/{api_version}/server/preferences/tokens/{id}` or `/{id}/{action}` | Primary owner Bearer token | Manage linked tokens; actions include reveal, rotate, update, and revoke. |
| GET | `/api/{api_version}/server/auth/check` | Bearer token | Validate operator or owner credentials without returning the raw token. |
| GET | `/api/{api_version}/server/healthz`, `/api/healthz` | Public | Machine-readable public health response. |
| GET | `/api/{api_version}/server/security/policy`, `/api/{api_version}/server/security/thanks` | Public | JSON mirrors of the disclosure policy and researcher acknowledgments. |
| GET | `/api/{api_version}/server/swagger`, `/api/swagger` | Public | OpenAPI JSON document. |
| GET | `/manifest.json`, `/sw.js`, `/robots.txt` | Public | PWA manifest, service worker, and crawler policy. |
| GET, HEAD | `/.well-known/security.txt`, `/.well-known/pgp-key.asc`, `/.well-known/llms.txt`, `/llms.txt` | Public | Security contact, optional published OpenPGP public key, and machine-readable service discovery. The PGP key route returns 404 unless publication is enabled and a public key exists. |
| GET | `/healthz` | Public, optional | Root health alias when enabled by server configuration. |

The API preferences and sync endpoints act only on guest browser preference cookies. Sync exports theme, language, and valid `citylist_pref_*` values, excluding consent, CCPA, build, CSRF, and credential cookies. Owner-token API routes ignore cookies and require explicit Bearer authentication. No route creates or mutates city or ZIP records.
