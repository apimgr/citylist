# Citylist

[![CI](https://github.com/apimgr/citylist/actions/workflows/ci.yml/badge.svg)](https://github.com/apimgr/citylist/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/apimgr/citylist)](https://github.com/apimgr/citylist/releases)
[![License](https://img.shields.io/github/license/apimgr/citylist)](LICENSE.md)
[![Documentation](https://readthedocs.org/projects/apimgr-citylist/badge/?version=latest)](https://apimgr-citylist.readthedocs.io/en/latest/)

## About

Citylist is a public, read-only reference API and server-rendered web application for GeoNames cities and United States ZIP codes. It embeds both datasets in the server binary and provides REST, GraphQL, MCP, and a companion command-line client.

## Features

- Global city lookup by GeoNames ID, name, country, and coordinates.
- United States ZIP lookup by postal code, city, state, and nearby coordinates.
- Read-only REST, GraphQL, OpenAPI, and MCP interfaces.
- Optional Tor hidden-service and I2P eepsite access, with operator-only Tor lifecycle and key maintenance through the local CLI.
- Server-rendered lookup, help, legal, contact, and security-report pages, with core forms usable without JavaScript.
- Guest theme, language, consent, and preference-sync controls, plus browser-based anonymous owner-token management.
- Operator-configured branding, site announcements, SMTP notices, and role-specific webhook notifications.
- `citylist-cli` command and terminal interfaces, with optional native GUI release assets for Linux, macOS, and Windows.
- Anonymous owner tokens for token-gated integrations without visitor accounts or passwords.
- English, Spanish, French, German, Italian, and Arabic catalogs shared by the server and client; see the [translation guide](docs/translating.md).

## Production

### Docker Compose

The production Compose file publishes port `64580` on the Docker bridge gateway and persists data under `docker/volumes/`.

```sh
docker compose -f docker/docker-compose.yml up -d
docker compose -f docker/docker-compose.yml logs -f citylist
```

The image is `ghcr.io/apimgr/citylist:latest` and listens on port `80` inside the container.

### Docker

```sh
docker run -d \
  --name citylist \
  -p 172.17.0.1:64580:80 \
  -v citylist-config:/config \
  -v citylist-data:/data \
  ghcr.io/apimgr/citylist:latest
```

### Binary

Download the matching server asset from [GitHub Releases](https://github.com/apimgr/citylist/releases/latest). The release matrix includes Linux, macOS, Windows, and FreeBSD, each on amd64 and arm64.

```sh
curl -q -LSsf -o citylist https://github.com/apimgr/citylist/releases/latest/download/citylist-linux-amd64
chmod +x citylist
./citylist --help
```

On first native start, the server selects and saves an unused port in `64000–64999`. The container listens on port `80` unless `PORT` overrides it. First startup creates `server.yml`, `secrets.yml`, and the local database. The generated operator token is printed once to standard output; store it securely.

### System service

The service dispatcher supports the service manager available on Linux, macOS, Windows, and BSD. Run installation with the privileges required by the target operating system:

```sh
sudo ./citylist --service --install
```

See [Installation](docs/installation.md) for service actions and platform details.

## Client

`citylist-cli` is the companion client for public lookups and token-gated integrations. Download the matching `citylist-cli-<os>-<arch>` asset from [GitHub Releases](https://github.com/apimgr/citylist/releases/latest). This default artifact is static and supports command and terminal modes. Optional `citylist-cli-gui-<os>-<arch>` assets provide the native graphical interface on Linux, macOS, and Windows; they are dynamically linked and desktop-only. The Linux GUI asset requires the runtime loader used by its build toolchain, so it is not portable across every Linux distribution.

```sh
citylist-cli --server https://YOUR_SERVER city 2643743
citylist-cli zipcode 10001
citylist-cli search London GB
citylist-cli zip-search York NY
citylist-cli --json stats
```

Set `CITYLIST_SERVER_PRIMARY`, configure the server in the private CLI profile, or pass `--server URL`. On an interactive first run without a configured server, the client prompts for one. See the [CLI reference](docs/cli.md) for commands, output formats, flags, and runtime files.

## Configuration

The server reads `server.yml`, ordered YAML files from `yaml.d/`, and sensitive values from `secrets.yml`. There is no web administration panel; edit configuration files directly. See the [configuration reference](docs/configuration.md) for settings, environment variables, paths, defaults, and reload behavior.

Common command-line overrides include `--port`, `--address`, `--config`, `--data`, `--cache`, `--log`, `--backup`, `--baseurl`, `--lang`, `--service`, `--maintenance`, and `--update`. Run `citylist --help` for the complete server command list.

## API

Public geographic lookups do not require authentication and never mutate the embedded datasets. The default REST version prefix is `/api/v1`.

| Endpoint | Description |
|---|---|
| `GET /api/v1/cities` | Paginated city records. |
| `GET /api/v1/cities/search` | Search city names. |
| `GET /api/v1/cities/{id}` | Look up a GeoNames city ID. |
| `GET /api/v1/cities/coordinates` | Find the nearest city. |
| `GET /api/v1/zipcodes/{zip}` | Look up a United States ZIP code. |
| `GET /api/v1/zipcodes/search` | Search ZIP locations. |
| `GET /api/v1/server/healthz` | Public machine-readable health response. |
| `GET /api/v1/server/swagger` | OpenAPI JSON document. |
| `GET`, `POST /api/graphql` | Read-only GraphQL endpoint. GET accepts `query`, optional JSON `variables`, and `operationName`. |
| `POST /api/v1/server/mcp` | MCP Streamable HTTP endpoint; Bearer token required. |

OpenAPI documentation is at `/server/docs/swagger`; the GraphQL explorer is at `/server/docs/graphql`. MCP also accepts `/api/mcp` and `/mcp`. To make a request, replace `YOUR_SERVER` with the server's origin:

```sh
curl -q -LSsf 'http://YOUR_SERVER/api/v1/cities/search?q=London&country=GB'
curl -q -LSsf 'http://YOUR_SERVER/api/v1/zipcodes/10001'
curl -q -LSsf 'http://YOUR_SERVER/api/v1/server/healthz'
```

See the [API reference](docs/api.md), [integrations guide](docs/integrations.md), and [security guide](docs/security.md) for response formats, limits, discovery, and authentication details.

## Other

The web application includes about, help, MCP help, contact, security, disclosure policy, researcher acknowledgments, privacy, DPO, terms, preferences, owner-token management, preference sync, and health pages. Discovery includes `/robots.txt`, `/sitemap.xml`, Atom and RSS feeds, `/.well-known/security.txt`, `/.well-known/llms.txt`, and conditional OpenPGP public-key publication.

City records retain GeoNames identifiers. Dataset sources, import scope, snapshot hashes, licenses, and attribution are documented in [DATA-SOURCES.md](DATA-SOURCES.md).

For common setup issues, check the service logs and request `/server/healthz`. The [documentation site](https://apimgr-citylist.readthedocs.io/en/latest/) includes installation, security, API, and integration guidance.

## Development

Go builds and tests run inside the `casjaysdev/go:latest` container through Make:

```sh
make dev
make build
make test
```

`make build` creates server and client binaries for all eight OS/architecture targets. `make test` runs `go vet`, `govulncheck`, and the Go test suite with the aggregate coverage gate. Browser and container integration checks are documented in the [development guide](docs/development.md).

## Disclaimer

This software is provided "as is" without warranty of any kind. Use at your own risk.

- **No Warranty**: The authors are not responsible for any damages, data loss, or issues arising from use of this software
- **Not Professional Advice**: This software does not constitute legal, financial, medical, or other professional advice
- **Third-Party Services**: If this software connects to external APIs or services, their terms of service apply separately
- **Security**: While we strive to follow security best practices, no software is guaranteed to be free of vulnerabilities
- **Production Use**: Evaluate thoroughly before deploying in production environments

By using this software, you acknowledge that you have read and understood this disclaimer.

## License

The Citylist application code is licensed under the [MIT License](LICENSE.md). The bundled datasets retain their own licenses and attribution requirements, described in [DATA-SOURCES.md](DATA-SOURCES.md).
