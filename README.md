# Citylist

Citylist is a public, read-only reference API and server-rendered web application for GeoNames cities and United States ZIP codes. City and ZIP datasets are embedded in the server binary, so lookups do not call external data services at runtime.

[![CI](https://github.com/apimgr/citylist/actions/workflows/ci.yml/badge.svg)](https://github.com/apimgr/citylist/actions/workflows/ci.yml)
[![Documentation](https://readthedocs.org/projects/apimgr-citylist/badge/?version=latest)](https://apimgr-citylist.readthedocs.io/en/latest/)
[![Release](https://img.shields.io/github/v/release/apimgr/citylist)](https://github.com/apimgr/citylist/releases)
[![License](https://img.shields.io/github/license/apimgr/citylist)](LICENSE.md)

---

## 📦 Install

Download the server binary for your operating system from [GitHub Releases](https://github.com/apimgr/citylist/releases/latest). Unix binaries do not include a file extension; Windows binaries use `.exe`.

| Operating system | Architecture | Server asset | CLI asset |
|---|---|---|---|
| Linux | amd64 | `citylist-linux-amd64` | `citylist-cli-linux-amd64` |
| Linux | arm64 | `citylist-linux-arm64` | `citylist-cli-linux-arm64` |
| macOS | amd64 | `citylist-darwin-amd64` | `citylist-cli-darwin-amd64` |
| macOS | arm64 | `citylist-darwin-arm64` | `citylist-cli-darwin-arm64` |
| Windows | amd64 | `citylist-windows-amd64.exe` | `citylist-cli-windows-amd64.exe` |
| Windows | arm64 | `citylist-windows-arm64.exe` | `citylist-cli-windows-arm64.exe` |
| FreeBSD | amd64 | `citylist-freebsd-amd64` | `citylist-cli-freebsd-amd64` |
| FreeBSD | arm64 | `citylist-freebsd-arm64` | `citylist-cli-freebsd-arm64` |

For Linux amd64:

```sh
curl -LSsf https://github.com/apimgr/citylist/releases/latest/download/citylist-linux-amd64 \
  -o citylist && chmod +x citylist
./citylist --help
```

On macOS, select the binary matching `uname -m` (`amd64` for Intel, `arm64` for Apple Silicon), make it executable, and remove quarantine if macOS blocks it:

```sh
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -LSsf "https://github.com/apimgr/citylist/releases/latest/download/citylist-darwin-${ARCH}" \
  -o citylist && chmod +x citylist
xattr -d com.apple.quarantine citylist 2>/dev/null || true
```

On FreeBSD, download the matching architecture asset and make it executable:

```sh
curl -LSsf https://github.com/apimgr/citylist/releases/latest/download/citylist-freebsd-amd64 \
  -o citylist && chmod +x citylist
```

Windows users can download the matching `.exe` asset and add its directory to `PATH`. See [Installation](docs/installation.md) for Docker and service setup.

## 🐳 Docker

The production Compose file is [docker/docker-compose.yml](docker/docker-compose.yml). It publishes port `64580` on the Docker bridge gateway and persists configuration and data under `docker/volumes/`.

```sh
docker compose -f docker/docker-compose.yml up -d
```

The published image is `ghcr.io/apimgr/citylist:latest`. Its HTTP port is `80`; see the Compose file for volume and port settings.

## 🖥️ Client CLI

`citylist-cli` is the companion client, with terminal and graphical interfaces. Download the matching `citylist-cli-<os>-<arch>` release asset. Set `CITYLIST_SERVER_PRIMARY`, configure `server.primary` in its private `cli.yml`, or pass `--server URL` to choose a server. On an interactive first run without a configured server, the client prompts for one.

```sh
citylist-cli --server http://127.0.0.1:8080 city 2643743
citylist-cli zipcode 10001
citylist-cli search London GB
citylist-cli zip-search York NY
citylist-cli --json stats
```

Run `citylist-cli --help` for the available commands. See the [CLI reference](docs/cli.md) for request limits and output behavior.

## 🤖 Citylist Server

Run `citylist --help` for server options. The first start creates `server.yml` and a private `secrets.yml` containing the operator credential and encryption key. A native install selects and saves an unused port in `64000–64999`; the container image listens on port `80` unless `PORT` overrides it. The `--service` commands install and manage a system service on supported operating systems; see [Installation](docs/installation.md).

Operators can check and install releases with `citylist --update check` and `citylist --update yes` (also available as `citylist --maintenance update`). Release channels are selected with `citylist --update branch {stable|beta|devel}`. The installer verifies the downloaded platform binary against the release SHA-256 manifest before replacing it.

The web application provides city and ZIP search, coordinate lookup, and about, help, contact, security, privacy, DPO, terms, preferences, owner-token, and health pages. Configured scheduled announcements render as accessible site banners and can be dismissed without JavaScript. Security policy and acknowledgments are at `/server/security/policy` and `/server/security/thanks`; the report-status page is a GET-only link protected by a token usable once per day. Discovery endpoints include `/robots.txt`, `/sitemap.xml`, an empty Atom feed at `/feed.xml`, and an empty RSS feed at `/rss.xml`. It serves server-rendered HTML and works without JavaScript for its forms. A consent banner appears until a choice is saved; visitors can accept, decline, or save granular preference and analytics choices. Theme and language preference cookies are written only when preference-cookie consent is enabled. A valid `?lang=CODE` query selects the language immediately and persists the choice in the `lang` cookie when that consent is enabled. The server and client ship English, Spanish, French, German, Italian, and Arabic catalogs; see [Translating](docs/translating.md).

## API and data

The REST API is rooted at `/api/v1`. It provides city and US ZIP lookups, search, nearest/nearby results, statistics, random city lookup, preference sync, and owner-token management. GraphQL is available at `/api/graphql` and `/api/v1/server/graphql`; OpenAPI is served at `/api/v1/server/swagger`; MCP Streamable HTTP is served at `/api/v1/server/mcp` and requires a Bearer token. Public data lookups are anonymous and read-only. Visitors can issue an anonymous owner token to manage their token set and authenticate MCP clients. See the [API reference](docs/api.md), [integrations guide](docs/integrations.md), and [security guide](docs/security.md).

City records retain GeoNames identifiers. Dataset sources, import scope, snapshot hashes, and their CC BY 4.0 attribution are documented in [DATA-SOURCES.md](DATA-SOURCES.md).

## 🛠️ Development

Go builds and tests run through the `casjaysdev/go:latest` Docker toolchain using Make:

```sh
make build
make test
```

`make build` creates server and CLI binaries for the eight OS/architecture pairs listed above. `make test` runs `go vet`, `govulncheck`, and the Go test suite, and enforces at least 60% aggregate statement coverage. See the [development guide](docs/development.md) for build prerequisites and integration checks.

The server and client embed catalogs for English, Spanish, French, Arabic, German, and Italian. The client localizes help, prompts, and messages; server UI localization is partial. See the [translation guide](docs/translating.md) for coverage and remaining gaps.

## 📄 License

The Citylist application code is licensed under the [MIT License](LICENSE.md). The bundled datasets retain their own licenses and attribution requirements, described in [DATA-SOURCES.md](DATA-SOURCES.md).
