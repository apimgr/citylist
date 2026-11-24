# API Rules (PART 13, 14, 15)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Do not expose unversioned project API routes without documented aliases.
- Do not disclose internal paths, addresses, or stack traces to clients.
- Do not retain superseded legacy routes or compatibility shims after a route migration.

## CRITICAL - ALWAYS DO
- Keep REST, OpenAPI, GraphQL, and browser surfaces in sync.
- Pair user-facing web routes with their JSON API counterparts; mark machine-only routes clearly.
- Support required content negotiation and request bounds.
- Use secure TLS and overlay HTTP semantics from the spec.
- Version native API routes under `/api/{api_version}` and keep direct aliases on the same handler.
- Document every project-defined route in IDEA.md and test route inventory drift.

## KEY DECISIONS

| Question | Answer | Spec Reference |
|----------|--------|----------------|
| What is authoritative? | AI.md controls implementation; IDEA.md controls product behavior. | PART 0 |
| What identifies product requirements? | Citylist supports global city and US ZIP lookups with public read-only access. | IDEA.md |

## TERMINOLOGY

| Term | Meaning |
|------|---------|
| server | `citylist`, the main binary | IDEA.md |
| client | `citylist-cli`, the companion binary | PART 32 |

## QUICK REFERENCE

- Read the complete relevant PART before changing code.
- Keep source, docs, and product behavior in sync.

---
For complete details, see AI.md PART 13, 14, 15.
