# Integrations

Citylist offers public read-only interfaces for browser applications, scripts, and command-line tools. MCP uses the same Bearer token model as the API and requires a valid owner or operator token.

## HTTP APIs

- REST: `/api/v1/`
- GraphQL: `POST /api/graphql` or `/api/v1/server/graphql`
- OpenAPI JSON: `/api/v1/server/swagger` or `/api/swagger`
- Swagger documentation page: `/server/docs/swagger`
- GraphQL query explorer: `/server/docs/graphql`
- Locale catalogs: `/locales/en.json`, `/locales/es.json`, `/locales/fr.json`, `/locales/de.json`, `/locales/it.json`, and `/locales/ar.json`

Public API responses are CORS-enabled according to `server.cors.allowed_origins`, `server.cors.allow_credentials`, and `server.cors.max_age`. Defaults permit any origin for this public dataset API; wildcard origins are not returned with credential permission. JSON is the default representation; clients can request plain text with `Accept: text/plain` or use supported `.txt` routes. API details and limits are in the [API reference](api.md).

## MCP clients

The MCP Streamable HTTP endpoint is `POST /api/v1/server/mcp`. The `/api/mcp` and `/mcp` paths are aliases. Use protocol revision `2026-07-28` and send a valid `Authorization: Bearer …` token on every request. Issue a primary token at `POST /api/v1/server/preferences/tokens/primary`, then create one linked token per MCP client at `/server/preferences/tokens`; linked tokens can be revoked independently. Cookies are ignored by MCP.

Configure the MCP client to use the server's origin and provide the protocol headers required by the current transport. The live `/server/help/mcp` page lists the endpoint details, tools, and example connection configuration. Do not put the operator token in an MCP client configuration.

## Browser application

The web application server-renders its pages and consolidates browser behavior in the external `/static/js/app.js`; pages do not depend on inline scripts. It includes a web app manifest at `/manifest.json` and a service worker at `/sw.js`. The worker precaches version-stamped CSS, JavaScript, the icon, and Swagger UI assets. Page navigations use network-first caching: a page is cached only after a successful, non-redirected response, and an offline fallback is returned when no cached page is available. It does not intercept `/api/` responses, so data lookups and API documentation data still require a server response. When a new worker is ready, the page offers an update prompt and reloads after acceptance.

## Discovery and security metadata

- `/.well-known/llms.txt` and `/llms.txt` publish a plain-text API overview and endpoint list.
- `/.well-known/security.txt` publishes the vulnerability-reporting URL and disclosure metadata.
- `/.well-known/pgp-key.asc` publishes the project's ASCII-armored OpenPGP key when the key exists and `web.security.publish_pgp_key` is enabled.
- `/robots.txt` publishes the configured crawler allow/deny entries.
- `/sitemap.xml` is generated for the registered public HTML pages and excludes paths denied by the crawler rules.
- `/feed.xml` (Atom 1.0) and `/rss.xml` (RSS 2.0) are always available. Citylist has no public per-item pages with creation timestamps, so both feeds currently contain no entries.
- `/server/security/policy` serves the default or configured disclosure policy; `/server/security/thanks` lists opted-in researcher credits.

The sitemap and feeds use absolute URLs resolved for each request. They support `GET` and `HEAD`, set `ETag` and `Last-Modified`, and honor conditional requests. The feeds are not advertised by page links. HTML pages include route-aware robots directives, canonical URLs, OpenGraph and Twitter metadata, JSON-LD, and validated configured verification tags. `server.seo.noindex` also adds the `X-Robots-Tag` response header. `/favicon.ico` serves the embedded SVG icon; `web.ui.accent` generates a theme-aware `/static/css/accent.css` when configured. Remote and local image customization through `server.branding.favicon`, `logo`, and `server.seo.og_image` is not implemented yet.

Unknown paths beneath `/.well-known/` return `404`; there is no arbitrary file serving. OpenPGP publication is conditional as described above, and operators can manage the key with `citylist --maintenance pgp` (see [Security](security.md)). OAuth/OIDC, federation, WebFinger, and app association files are not currently enabled. Email delivery covers contact/security-report messages and configured operator events; contact webhooks are limited to contact/security-report notifications and are not a general-purpose integration API.

## Data provenance

The global city JSON and US postal-code archive are embedded in the server binary. Their sources, import scope, snapshot hashes, licenses, and attribution are recorded in [DATA-SOURCES.md](https://github.com/apimgr/citylist/blob/main/DATA-SOURCES.md).
