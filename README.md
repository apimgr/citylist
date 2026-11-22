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

The production Compose file is [docker/docker-compose.yml](docker/docker-compose.yml). It publishes port `64580` on the host and persists configuration, data, and logs in named volumes.

```sh
docker compose -f docker/docker-compose.yml up -d
```

The published image is `ghcr.io/apimgr/citylist:latest`. Its HTTP port is `80`; see the Compose file for volume and port settings.

## 🖥️ Client CLI

`citylist-cli` is the companion client. Download the matching `citylist-cli-<os>-<arch>` release asset. Set `CITYLIST_SERVER_PRIMARY`, configure `server.primary` in its private `cli.yml`, or pass `--server URL` to choose a server. On an interactive first run without a configured server, the client prompts for one.

```sh
citylist-cli --server http://127.0.0.1:8080 city 2643743
citylist-cli zipcode 10001
citylist-cli search London GB
citylist-cli zip-search York NY
citylist-cli --json stats
```

Run `citylist-cli --help` for the available commands. See the [CLI reference](docs/cli.md) for request limits and output behavior.

## 🤖 Citylist Server

Run `citylist --help` for server options. The first start creates `server.yml` and a private `secrets.yml` containing the operator credential and encryption key. The server listens on port `8080` by default. The `--service` commands install and manage a system service on supported operating systems; see [Installation](docs/installation.md).

The web application provides city search and coordinate lookup, plus about, help, contact, security, privacy, terms, preferences, and health pages. It serves server-rendered HTML and works without JavaScript for its core lookup forms. The `/server/preferences` page saves theme and language choices in browser cookies; a `?lang=CODE` URL overrides language for that request only. The server's language behavior and current localization gaps are described in [Translating](docs/translating.md).

## API and data

The REST API is rooted at `/api/v1`. It provides city and US ZIP lookups, search, nearest/nearby results, statistics, and random city lookup. GraphQL is available at `/graphql`; OpenAPI is served at `/api/v1/server/swagger`; MCP Streamable HTTP is served at `/api/v1/server/mcp`. Public data lookups are anonymous and read-only. See the [API reference](docs/api.md), [integrations guide](docs/integrations.md), and [security guide](docs/security.md).

City records retain GeoNames identifiers. Dataset sources, import scope, snapshot hashes, and their CC BY 4.0 attribution are documented in [DATA-SOURCES.md](DATA-SOURCES.md).

## 🛠️ Development

Go builds and tests run through the `casjaysdev/go:latest` Docker toolchain using Make:

```sh
make build
make test
```

`make build` creates server and CLI binaries for the eight OS/architecture pairs listed above. `make test` runs the Go suite and enforces at least 60% aggregate statement coverage. See the [development guide](docs/development.md) for build prerequisites and integration checks.

The server and client embed catalogs for English, Spanish, French, Arabic, German, and Italian. The client localizes help, prompts, and messages; server UI localization is partial. See the [translation guide](docs/translating.md) for coverage and remaining gaps.

## 📄 License

The Citylist application code is licensed under the [MIT License](LICENSE.md). The bundled datasets retain their own licenses and attribution requirements, described in [DATA-SOURCES.md](DATA-SOURCES.md).
