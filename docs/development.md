# Development

## Prerequisites

- Git and Make.
- Docker with a running daemon; the project uses the `casjaysdev/go:latest` toolchain container for Go builds and tests.
- A working Go toolchain container image, fetched by Docker when it is not already available.

Go binaries and tests must run inside the configured container. Do not build Go binaries or execute the compiled server/client directly on the host. `make dev` builds binaries into a project-scoped temporary directory; `make build` produces release binaries under the ignored `binaries/` directory.

## Build and test

```sh
make build
make test
tests/run_tests.sh
tests/incus.sh
tests/e2e.sh
```

`make build` produces the server and CLI binaries for Linux, macOS, Windows, and FreeBSD on amd64 and arm64. `make test` runs `go vet ./...`, `govulncheck ./...`, and `go test ./...` in the Go container, then enforces 60% aggregate statement coverage. Local build metadata and the application version come from the Makefile, `release.txt`, and optional `site.txt`; CI releases derive the version from the pushed tag, while beta and development builds use generated identifiers.

The GitHub Actions workflow runs `go vet`, `staticcheck`, the coverage-gated test suite, a build, `govulncheck`, secret scanning, workflow pin checks, and a Trivy image scan.

## Integration and browser checks

Integration and browser checks are developer-initiated checks, separate from the unit-test commit gate:

`tests/run_tests.sh` runs the basic Docker binary checks only. Unit tests use `make test`. The full Incus service lifecycle suite stays explicit through `tests/incus.sh`; it runs Debian/systemd and Alpine/OpenRC sequentially. The Incus and Docker scripts build test binaries in project-scoped OS temporary storage. The Incus runner also accepts prebuilt binaries through `CITYLIST_BIN_DIR`, which is useful when Go builds run in a separate container. The browser check is standalone, requires Docker, and runs the pure-Go chromedp suite in an isolated container. The scripts create temporary data and clean up only resources they created.

## Source layout

- `src/cities` and `src/zipcodes`: immutable dataset lookup services.
- `src/server`: REST handlers, GraphQL/MCP transport, content negotiation, and web pages.
- `src/graphql`: read-only GraphQL schema and resolvers.
- `src/client`: CLI, HTTP client, configuration, runtime paths, and shell completion support.
- `src/common/i18n`: embedded locale catalogs and translation helpers.
- `src/config`, `src/database`, `src/paths`, and `src/service`: server configuration, SQLite/libSQL storage, OS paths, and service integration.
- `src/data`: embedded city and US postal datasets.
- `tests`: container integration and browser verification scripts.
- `docs`: MkDocs/ReadTheDocs documentation only.

## Contributions

Keep changes scoped, add or update Go tests alongside changed behavior, and update the relevant API, configuration, CLI, security, or integration documentation in the same change. Follow the repository's `AI.md`, `IDEA.md`, and `AGENTS.md` instructions. User-visible changes should keep `README.md` and the documentation site aligned with the implementation.
