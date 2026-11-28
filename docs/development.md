# Development

## Prerequisites

- Git and Make.
- Docker with a running daemon; the project uses the `casjaysdev/go:latest` toolchain container for Go builds and tests.
- A working Go toolchain container image, fetched by Docker when it is not already available.

Go binaries and tests must run inside the configured container. Do not build Go binaries or execute the compiled server/client directly on the host. `make build` produces debug binaries with symbols under the ignored `binaries/` directory; `make release` produces stripped production artifacts under the ignored `releases/` directory without tagging or publishing.

## Build and test

```sh
make build
make test
tests/run_tests.sh
tests/incus.sh
tests/e2e.sh
```

`make build` produces local and cross-platform debug server and CLI binaries for Linux, macOS, Windows, and FreeBSD on amd64 and arm64. These retain symbols and DWARF, disable optimization and inlining, and do not use `-trimpath`. `make test` runs `go vet ./...`, `staticcheck ./...`, `govulncheck ./...`, ShellCheck, `shfmt`, and all Go tests in the Go container, then enforces at least 60% aggregate coverage and runs `tests/run_tests.sh` for basic binary checks. `make release` builds stripped, optimized production artifacts for all eight targets, plus the source archive and checksums; it never creates tags or publishes. Local build metadata and the application version come from the Makefile, `release.txt`, and optional `site.txt`; CI releases derive the version from the pushed tag, while beta and development builds use generated identifiers.

`make docker` builds the host-platform image locally without pushing, then runs `tests/check-image.sh` to check the image's bundled tools, startup, and Tor health. CI/CD handles image publishing.

The GitHub Actions workflow runs `go vet`, `staticcheck`, the coverage-gated test suite, a build, `govulncheck`, secret scanning, workflow pin checks, and a Trivy image scan.

The interactive API pages use locally served pinned assets: Swagger UI 5.36.0 and GraphiQL 4.1.2 with React and ReactDOM 18.3.1. Their startup code is kept in `src/server/static/js/docs-swagger.js` and `docs-graphql.js`; do not load these libraries from a CDN or merge their startup code into `app.js`.

## Integration and browser checks

Integration and browser checks are developer-initiated checks, separate from the unit-test commit gate:

`make test` invokes `tests/run_tests.sh` for the basic Docker binary checks. The full Incus service lifecycle suite stays explicit through `tests/incus.sh`; it runs Debian/systemd and Alpine/OpenRC sequentially. The Incus and Docker scripts build test binaries in project-scoped OS temporary storage. The Incus runner also accepts prebuilt binaries through `CITYLIST_BIN_DIR`, which is useful when Go builds run in a separate container. The browser check is standalone, requires Docker, and runs the pure-Go chromedp suite in an isolated container. The scripts create temporary data and clean up only resources they created.

If the browser check fails, its project-scoped temporary artifact directory is retained with owner-only permissions. The script prints the artifact path; server startup logs are saved there instead of being dumped to the terminal. Successful runs remove the temporary artifacts.

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
