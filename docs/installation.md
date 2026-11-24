# Installation

Citylist ships as a static server binary and a Docker image. The city and ZIP datasets are embedded, so lookups do not depend on startup downloads. On first run, the server initializes its local SQLite database and queues missing security datasets for background download; download failures are logged and do not prevent the listener from starting.

## Docker Compose

The production Compose file is `docker/docker-compose.yml`. It publishes the server on `172.17.0.1:64580` and stores configuration and application data in `docker/volumes/config` and `docker/volumes/data`.

```sh
docker compose -f docker/docker-compose.yml up -d
docker compose -f docker/docker-compose.yml logs -f citylist
```

The image is `ghcr.io/apimgr/citylist:latest` and listens on port `80` inside the container. The included Compose file binds to the Docker bridge gateway at `172.17.0.1` for use by a host reverse proxy.

To run the image directly:

```sh
docker run -d \
  --name citylist-app \
  -p 172.17.0.1:64580:80 \
  -v citylist-config:/config \
  -v citylist-data:/data \
  ghcr.io/apimgr/citylist:latest
```

## Static binaries

Download the `citylist-<os>-<arch>` server asset from [GitHub Releases](https://github.com/apimgr/citylist/releases/latest). The release build matrix is:

| OS | Architectures |
|---|---|
| Linux | amd64, arm64 |
| macOS | amd64, arm64 |
| Windows | amd64, arm64 |
| FreeBSD | amd64, arm64 |

For Linux amd64:

```sh
curl -q -LSsf https://github.com/apimgr/citylist/releases/latest/download/citylist-linux-amd64 \
  -o citylist && chmod +x citylist
./citylist --help
```

For macOS, choose the matching architecture asset, mark it executable, and remove the quarantine attribute if needed. On Windows, download the `.exe` asset and add its directory to `PATH`.

The required companion `citylist-cli` has matching OS/architecture release assets. It sends bounded read-only requests to a Citylist server. See the [CLI reference](cli.md).

## Run the server

Run `citylist --help` for options. On a native first start, Citylist selects an unused port from `64000–64999` and saves it in `server.yml`; inside a container, it listens on port `80` unless `PORT` overrides it. The default bind address is `[::]`. Use `--address` and `--port` to override the listener. The server creates `server.yml` and `secrets.yml` on first startup. SQLite creates `server.db`; a remote libSQL installation creates a separate local `cache.db` only when its optional cache is enabled. See [Configuration](configuration.md) for platform paths and directory overrides.

On first startup, the server prints the generated operator token once to standard output. Store it securely. Public city and ZIP lookups remain anonymous. MCP and operator-only maintenance require a Bearer token; for MCP, issue an owner token and create a separate linked token for each client instead of using the operator token.

## Install as a system service

The service dispatcher supports the service manager available on Linux, macOS, Windows, and BSD. Run the install command with the privileges required by the target operating system:

```sh
sudo ./citylist --service --install
```

On Linux with systemd, the install command installs and enables the unit. Start it with:

```sh
sudo systemctl start citylist
sudo systemctl status citylist
```

Run `citylist --service --help` for the service actions supported by the current platform. Alternatively, run Citylist in the foreground under an existing process manager.
