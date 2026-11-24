# Citylist

Citylist is a public, read-only reference API and server-rendered web application for GeoNames city records and geographic United States ZIP codes. Both lookup datasets are embedded in the server binary, so geographic lookups do not call upstream data providers. Optional security-data feeds may be downloaded in the background after startup.

## Features

- Global city lookup by GeoNames ID, name, country, and coordinates.
- US ZIP lookup by postal code, city, state, and nearby coordinates.
- Versioned REST APIs, GraphQL, and OpenAPI documentation.
- Server-rendered browser pages and the companion `citylist-cli`.
- Embedded locale catalogs for English, Spanish, French, Arabic, German, and Italian. The client localizes its own help, prompts, and messages; server templates use the catalogs for much of their UI. Some server-generated and API text remains English; see [Translating](translating.md).
- About, privacy, and terms pages include sanitized operator-supplied content extensions. The privacy page reflects configured analytics and CCPA settings.
- Guest theme, language, and consent preferences at `/server/preferences`; `/server/preferences/sync` exports and imports guest preferences. The consent banner supports accept, decline, and granular save actions. Preference cookies are written only with preference consent. A valid `?lang=...` query selects the current language and persists it in the `lang` cookie only when preference consent is enabled. CCPA opt-out controls appear when `server.privacy.data.sold` is true. Configured analytics trackers run only with analytics consent and are suppressed for GPC, CCPA opt-out, Tor, and I2P requests.
- Anonymous owner-token management at `/server/preferences/tokens`; MCP requires a Bearer owner or operator token.
- Public discovery through OpenAPI, GraphQL, MCP, `/.well-known/security.txt`, `/.well-known/llms.txt`, `/robots.txt`, `/sitemap.xml`, and empty Atom/RSS feeds at `/feed.xml` and `/rss.xml`.
- Security disclosure policy and acknowledgments at `/server/security/policy` and `/server/security/thanks`; a project OpenPGP public key is published at `/.well-known/pgp-key.asc` when available and enabled.

## Start here

- [Install Citylist](installation.md)
- [Configure the server](configuration.md)
- [Use the API](api.md)
- [Use the CLI](cli.md)
- [Security and privacy](security.md)
- [Data sources and integrations](integrations.md)
- [Contribute](development.md)
- [Translate Citylist](translating.md)

Dataset licenses, snapshot hashes, attribution, and import scope are documented in the repository's [DATA-SOURCES.md](https://github.com/apimgr/citylist/blob/main/DATA-SOURCES.md).
