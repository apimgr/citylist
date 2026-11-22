# AI Alignment Backlog

This backlog records implementation work only. AI.md defects are tracked in
`../TODO.API.md` and are not duplicated here. The dependency order below is
topological: project/config foundations precede shared services; locale catalogs
and shared frontend contracts precede page implementations; complete behavior
precedes documentation and final verification.

## Critical (P0)

- [ ] Complete Parts 0–8 foundations: align IDEA.md with AI.md, project files and OS paths, configuration startup and modes, static server/client binaries, and the server CLI and maintenance dispatcher.
- [ ] Complete Parts 5, 9–12 configuration and backend foundations: schema/defaults, drop-ins, validation, secrets and hot reload, structured errors, bounded caches, SQLite persistence, operator security, audit/security logging, and rate controls.
- [ ] Implement Part 30 locale catalogs and shared localization support before translating templates or client output: provide all six required locale catalogs with key parity and translate user-facing text.
- [ ] Complete Parts 17–24 reusable services and data foundations: SMTP/notifications, scheduler, GeoIP, metrics, backup/restore, updater, privilege escalation, systemd/OpenRC, and city/ZIP data provenance, licenses, lookup behavior, and API/web parity.
- [ ] Complete Parts 13–16 server contracts and pages after API/security services and localization are ready: health/versioning, routes and discovery, OpenAPI/GraphQL/MCP consistency, SSL behavior, required SSR pages, accessibility, privacy, and abuse controls.

## High (P1)

- [ ] Complete Part 32 `citylist-cli` after the API contracts stabilize: command/config parity, private runtime directories, setup flow, output formats, shell completions, TUI/GUI requirements, and API parity.
- [ ] Complete Parts 25–27 build, deployment, and release requirements: Makefile targets, Docker files and compose variants, security scans, and every provider workflow required by AI.md.
- [ ] Complete Parts 28–29 tests and documentation after behavior and interfaces stabilize: required unit/integration/browser coverage, ReadTheDocs configuration and navigation, and current README plus operator, API, client, security, integration, and development docs.
- [ ] Reconcile project rule summaries, templates, endpoint inventory, OpenAPI/GraphQL/MCP surfaces, and docs with implemented behavior and IDEA.md.

## Final

- [ ] Complete AI.md FINAL CHECKPOINT line by line, close every remaining implementation gap, and remove each backlog item only after its implementation has been committed, following the required TODO.AI.md completion sequence.
