# TODO.AI.md — citylist

Remaining work identified against `AI.md` and `IDEA.md`. The implemented city and geographic US ZIP APIs, GraphQL, initial CLI, public pages, PWA shell, rate limiting, docs, and GitHub Actions are present. These items remain open; they are not claims of completed compliance.

## Required implementation still open

1. **Overlay network lifecycle (AI.md PART 31).** The Docker entrypoint still starts Tor outside the server process. Move Tor ownership into the server binary, generate the app-scoped torrc and isolated hidden-service backend, expose onion state through health/status, and stop Tor on shutdown. Implement optional I2P only when enabled in config. Do not claim overlay support until runtime behavior is covered.
2. **Full companion client (AI.md PART 32).** `citylist-cli` supports core REST reads, but lacks the required persisted client configuration, setup wizard, complete CLI command coverage, interactive TUI, native GUI, shell completions, language parity, and verified SHA-256 self-update flow.
3. **Internationalization and accessibility (AI.md PART 30).** The UI, server messages, docs, and CLI remain primarily English and do not yet use the required seven-language translation catalogs or parity checks. Add locale selection, translation coverage, and automated accessibility checks.
4. **Base URL path support.** The server accepts `--baseurl` and routes prefixed requests, but page links and browser JavaScript still contain root-relative paths. Prefix-aware rendering and browser requests are needed before reverse-proxy subpath deployments are reliable.
5. **Overlay, metrics, and operations surfaces.** Tor/I2P status, Prometheus metrics, structured access logging, persistent rate-limit state, and complete service lifecycle behavior are not implemented to the shared spec.
6. **Dataset refresh process.** Embedded dataset snapshots have attribution and hashes in `DATA-SOURCES.md`, but no reproducible refresh/import workflow exists. Preserve the current snapshots and counts until a refresh process verifies compatible IDs, licensing, and data shape.

## Verification still required

- Build the server and CLI binaries for all required target platforms.
- Build and run the production and development Docker images, including health checks and graceful shutdown.
- Validate MkDocs/ReadTheDocs output and all cross-references.
- Exercise the actual browser UI, PWA offline behavior, API content negotiation, rate limits, and prefixed routing.
- Review the CI workflows and run the complete project gates after the remaining feature work is integrated.
