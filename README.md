# Citylist

[![CI](https://github.com/apimgr/citylist/actions/workflows/ci.yml/badge.svg)](https://github.com/apimgr/citylist/actions/workflows/ci.yml)
[![Documentation](https://readthedocs.org/projects/apimgr-citylist/badge/?version=latest)](https://apimgr-citylist.readthedocs.io/en/latest/)
[![Release](https://img.shields.io/github/v/release/apimgr/citylist)](https://github.com/apimgr/citylist/releases)
[![License](https://img.shields.io/github/license/apimgr/citylist)](LICENSE.md)

## About

Citylist is a public, read-only reference API and server-rendered web application for global GeoNames city records and geographic United States ZIP codes. Both datasets are embedded in the server binary, and requests do not depend on external data services.

## Features

- Look up cities by GeoNames ID, name, country, and coordinates.
- Search US ZIP codes by postal code, city, state, and nearby coordinates.
- Use REST, GraphQL, and OpenAPI interfaces.
- Browse the responsive web interface and installable PWA shell.
- Query the service with the companion `citylist-cli`.

## Production

### Docker

```sh
docker run -d \
  --name citylist-app \
  -p 127.0.0.1:64580:80 \
  -v citylist-config:/config \
  -v citylist-data:/data \
  -v citylist-logs:/logs \
  ghcr.io/apimgr/citylist:latest
```

### Binary and system service

Download the server binary for your operating system from [GitHub Releases](https://github.com/apimgr/citylist/releases). Linux AMD64 is named `citylist-linux-amd64`.

```sh
chmod +x citylist-linux-amd64
./citylist-linux-amd64 --help
sudo ./citylist-linux-amd64 --service --install
```

The server listens on port `8080` by default. [Installation instructions](docs/installation.md) cover Docker, binary downloads, and service setup.

## Client

Download the matching `citylist-cli` release asset or build both binaries with `make build`.

```sh
citylist-cli --server http://127.0.0.1:8080 city 2643743
citylist-cli zipcode 10001
citylist-cli search London GB
citylist-cli --help
```

Set `CITYLIST_SERVER` to choose the default server URL. See the [CLI reference](docs/cli.md) for commands and options.

## Configuration

The first server start creates `server.yml` in the selected configuration directory. Common overrides are `--port`, `--address`, `--config`, `--data`, `--logs`, and `--baseurl`; `PORT`, `ADDRESS`, `CONFIG_DIR`, `DATA_DIR`, and `LOGS_DIR` provide environment overrides. See the [configuration reference](docs/configuration.md) for all settings and platform paths.

## API

The base REST API is `/api/v1`. Useful routes include:

- `GET /api/v1/cities/search?q=London`
- `GET /api/v1/cities/2643743`
- `GET /api/v1/zipcodes/10001`
- `GET /api/v1/zipcodes/search?q=New%20York&state=NY`
- `GET /graphql`
- `GET /api/v1/openapi.json`

Request plain text with `Accept: text/plain` or use `.txt` on API routes. All data endpoints are public and read-only. See the [API reference](docs/api.md) for parameters, response formats, rate limits, and documentation routes.

## Other

City records preserve GeoNames identifiers. The city and postal-code datasets are licensed under CC BY 4.0; attribution, snapshot hashes, and import scope are documented in [DATA-SOURCES.md](DATA-SOURCES.md).

## Development

The project uses the `casjaysdev/go:latest` Docker toolchain through Make:

```sh
make build
make test
```

Build produces server and CLI binaries for eight OS/architecture pairs. The test target runs the Go suite and requires at least 60% aggregate coverage. See the [development guide](docs/development.md) for the source layout and contribution notes.

## Disclaimer

Citylist provides geographic reference data for informational use. Verify that the data is suitable and current enough for your application before relying on it. Citylist is provided without guarantees about accuracy, completeness, or availability.

## License

The Citylist application code is licensed under the [MIT License](LICENSE.md). The included city and postal-code datasets retain their own CC BY 4.0 terms and attribution requirements.
