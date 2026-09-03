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

## Business logic

### Product scope & non-goals

**In scope:**
- Global city list with GeoNames IDs (compatible with OpenWeatherMap city IDs)
- US zipcode dataset with city, state, and coordinates
- Search/filter cities by name and country code
- Lookup city by GeoNames ID; lookup US zipcode by postal code
- Full web frontend (server-side Go templates, dark/light/auto theme, PWA, mobile-first)
- Server pages: `/server/about`, `/server/help`, `/server/healthz`, `/server/privacy`, `/server/terms`
- CLI client (`citylist-cli`) for queries from the terminal
- OpenAPI/Swagger docs at `/api/{api_version}/server/swagger`
- GraphQL at `/graphql`

**Non-goals:**
- No user accounts, registration, or login of any kind
- No admin web panel (server configured via `server.yml` only)
- No write/mutation API (data is read-only, embedded at build time)
- No real-time population, weather, or timezone data beyond what is in the embedded dataset
- No paid tiers, no API keys, no rate-limited access tiers

### Roles & permissions

There are no user roles. All endpoints are public and require no authentication.

| Actor | Access |
|-------|--------|
| **Anonymous visitor (browser)** | Full read access to all web pages and API endpoints |
| **Anonymous API client (curl/CLI)** | Full read access to all API endpoints |
| **Server operator** | Configures server via `server.yml` only; no web management interface |

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

No external services called at runtime.

### Threat model & abuse cases

**Primary assets:** service availability.

**Attacker/abuser goals:**
- DoS via high-rate requests or expensive city name searches
- Bulk scraping of the full dataset

**Defenses:**
- Rate limiting on all endpoints
- Request size limits on all inputs
- No user accounts eliminates credential stuffing and privilege escalation entirely

### Security decisions & exceptions

- **No authentication on any endpoint**: intentional. Public read-only reference API.
- **All responses include `Access-Control-Allow-Origin: *`**: intentional. Public data API designed for cross-origin browser use.
