# API Rules (PART 13, 14, 15)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never retain obsolete project endpoints through redirects or compatibility shims.
- Never diverge API routes, schemas, status codes, or examples across OpenAPI, GraphQL, docs, and handlers.
- Never expose write operations or account/auth flows absent from the project specification.
- Never weaken TLS, certificate, or health/version behavior specified in AI.md.

## CRITICAL - ALWAYS DO

- Follow exact route/version patterns, response envelopes, validation, and error semantics.
- Keep OpenAPI/GraphQL schemas and route inventories synchronized with implementation.
- Implement health/version endpoints and TLS/Let's Encrypt behavior from their assigned PARTs.
- Verify API status, headers, and body against the specified contract.

## Key rules

- PART 13 defines health and versioning; PART 14 defines API structure; PART 15 defines TLS and certificate management.
- External compatibility applies only where AI.md explicitly requires matching an external service.

For complete details, see AI.md PART 13, 14, 15.
