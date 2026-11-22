## Project description

Citylist is a full-stack Go web application providing a global city list and US zipcode dataset via REST API and a server-side rendered web UI. City data originates from the GeoNames database (IDs compatible with OpenWeatherMap) and includes city name, country code, and geographic coordinates. US zipcode data includes city, state, and coordinates. All data is embedded in the binary at build time. Deployed as a single self-contained static binary.

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

## Business logic

### Product scope & non-goals

**In scope:**
- Global city list with GeoNames IDs (compatible with OpenWeatherMap city IDs)
- US zipcode dataset with city, state, and coordinates
- Search/filter cities by name and country code
- Lookup city by GeoNames ID; lookup US zipcode by postal code
- Full web frontend (server-side Go templates, dark/light/auto theme, PWA, mobile-first)
- Server pages: `/server/about`, `/server/help`, `/server/contact`, `/server/security`, `/server/healthz`, `/server/privacy`, `/server/terms`
- CLI client (`citylist-cli`) for queries from the terminal
- OpenAPI/Swagger docs at `/api/{api_version}/server/swagger`
- GraphQL at `/graphql`

**Non-goals:**
- No visitor accounts, registration, or login
- No admin web panel (server configured via `server.yml` only)
- No write/mutation API (data is read-only, embedded at build time)
- No real-time population, weather, or timezone data beyond what is in the embedded dataset
- No paid tiers, no visitor API keys, and no differentiated rate-limit tiers

### Roles & permissions

There are no visitor accounts or user roles. Public lookup and informational endpoints require no authentication. Operator-only maintenance actions use the required operator-token or local-root authorization and are not exposed as a web administration interface.

| Actor | Access |
|-------|--------|
| **Anonymous visitor (browser)** | Read access to public pages and lookup features |
| **Anonymous API client (curl/CLI)** | Read access to public data endpoints |
| **Server operator** | Configures the server through `server.yml` and related configuration files; privileged maintenance uses the operator token or local root |

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

No PII stored or served.

### Trust boundaries & external services

| Boundary | Trust level | Notes |
|----------|-------------|-------|
| City and zipcode datasets (embedded at build) | Fully trusted | Static, compiled into binary |
| Incoming HTTP requests | **Untrusted** | All query parameters validated |

City and ZIP lookup requests use only the embedded datasets and never call upstream data providers. Operator-enabled maintenance or notification integrations may contact their configured providers as required by AI.md; these services do not supply lookup results.

### Threat model & abuse cases

**Primary assets:** service availability.

**Attacker/abuser goals:**
- DoS via high-rate requests or expensive city name searches
- Bulk scraping of the full dataset

**Defenses:**
- Rate limiting on all endpoints
- Request size limits on all inputs
- Anonymous access removes visitor account enumeration and credential-stuffing surfaces; privileged operator actions remain protected by the server operator token

### Security decisions & exceptions

- **No user accounts or login:** intentional. Public city and ZIP lookup routes do not require credentials. Operator-only management endpoints use the configured operator token and are not part of the public data API.
- **All responses include `Access-Control-Allow-Origin: *`**: intentional. Public data API designed for cross-origin browser use.

### API routes

All routes are public unless marked operator-token protected. Read-only lookup APIs do not accept writes. The POST coordinates and GraphQL POST routes execute read-only queries and do not mutate data. JSON responses use the standard `{"ok":true,"data":...}` envelope; `Accept: text/plain` selects text where supported.

| Method | Path | Auth | Purpose and request/response summary |
|--------|------|------|--------------------------------------|
| GET | `/` | Public | Server-rendered lookup home page; JSON/text negotiation exposes service summary. |
| GET | `/search` | Public | Server-rendered city search form/results. |
| GET | `/coordinates` | Public | Server-rendered coordinate lookup form/results. |
| GET | `/server/about` | Public | Service description and project features. |
| GET | `/server/help` | Public | User help and current endpoint examples. |
| GET | `/server/help/mcp` | Public | MCP connection instructions and available public tools. |
| GET | `/server/contact` | Public | Contact and security-reporting information. |
| GET | `/server/security` | Public | Security reporting policy and reporting links. |
| GET | `/server/privacy` | Public | Privacy and request-log practices. |
| GET | `/server/terms` | Public | Service and dataset terms. |
| GET | `/api/v1/server/about` | Public | JSON or negotiated text mirror of the about page. |
| GET | `/api/v1/server/help` | Public | JSON or negotiated text mirror of the help page. |
| GET | `/api/v1/server/help/mcp` | Public | JSON MCP connection metadata and available public tools. |
| GET | `/api/v1/server/contact` | Public | JSON or negotiated text mirror of contact information. |
| GET | `/api/v1/server/security` | Public | JSON or negotiated text mirror of security information. |
| GET | `/api/v1/server/privacy` | Public | JSON or negotiated text mirror of the privacy page. |
| GET | `/api/v1/server/terms` | Public | JSON or negotiated text mirror of the terms page. |
| GET | `/server/healthz` | Public | Health and public build status; HTML, JSON, or text by content negotiation. |
| GET | `/api/v1/` | Public | API discovery summary and endpoint list. |
| GET | `/api/v1/server/healthz` | Public | JSON health response; text is available by negotiation. |
| GET | `/api/healthz` | Public | Direct machine-probe alias for health. |
| GET | `/api/v1/cities` | Public | Paginated cities; `page` and `limit` query parameters. |
| GET | `/api/v1/cities/search` | Public | Search cities by `q` and optional country filter; paginated JSON. |
| GET | `/api/v1/cities/{id}` | Public | Fetch a city by GeoNames integer ID. |
| GET | `/api/v1/cities/country/{code}` | Public | List cities for an ISO 3166-1 alpha-2 country code. |
| GET | `/api/v1/cities/coordinates` | Public | Nearest city from `latitude` and `longitude` query values. |
| POST | `/api/v1/cities/coordinates` | Public | Read-only nearest-city lookup from bounded JSON coordinates. |
| GET | `/api/v1/cities/nearby` | Public | Find nearby cities by coordinates and bounded radius. |
| GET | `/api/v1/zipcodes/{zip}` | Public | Look up one five-digit US ZIP code. |
| GET | `/api/v1/zipcodes/search` | Public | Search ZIP records by city/state query. |
| GET | `/api/v1/zipcodes/state/{state}` | Public | Paginated ZIP records for a state abbreviation. |
| GET | `/api/v1/zipcodes/nearby` | Public | Nearby ZIP records by coordinates and bounded radius. |
| GET | `/api/v1/random` | Public | Return one embedded city record. |
| GET | `/api/v1/random.txt` | Public | Return one city in plain text. |
| GET | `/api/v1/stats` | Public | Dataset counts and summary statistics. |
| GET | `/api/v1/stats.txt` | Public | Dataset summary in plain text. |
| GET | `/api/v1/count` | Public | City count summary. |
| GET | `/api/v1/count.txt` | Public | City count in plain text. |
| GET, POST | `/graphql` | Public | Read-only GraphQL queries; GET accepts query parameters, POST accepts a bounded JSON request. |
| GET, POST | `/api/v1/server/graphql` | Public | Versioned GraphQL query endpoint. |
| POST | `/api/v1/server/mcp` | Public | MCP Streamable HTTP endpoint exposing public lookup tools. |
| POST | `/api/mcp`, `/mcp` | Public | Direct aliases for the current versioned MCP endpoint. |
| GET | `/server/docs/graphql` | Public | Server-rendered GraphQL query explorer. |
| GET | `/server/docs/swagger` | Public | Server-rendered OpenAPI/Swagger documentation. |
| GET | `/api/v1/server/swagger` | Public | OpenAPI JSON document. |
| GET | `/api/v1/openapi.json` | Public | OpenAPI JSON convenience route. |
| GET | `/manifest.json` | Public | PWA web app manifest. |
| GET | `/sw.js` | Public | PWA service worker. |
| GET | `/robots.txt` | Public | Crawler policy. |
| GET, HEAD | `/.well-known/security.txt` | Public | Security contact and disclosure policy. |
| GET, HEAD | `/.well-known/llms.txt`, `/llms.txt` | Public | Machine-readable service and API overview. |
| POST | `/server/preferences/theme` | Public | Set browser theme cookie; accepts `theme=dark|light|auto` and a local `return_to` path. |

Operator-only routes required by the server baseline are authenticated with the configured operator token and documented separately from public lookup routes. No route creates or mutates city or ZIP data.
