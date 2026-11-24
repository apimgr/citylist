# Testing, Docs, and I18N Rules (PART 28, 29, 30)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Do not run tests against project-local runtime data.
- Do not omit content-negotiation checks or accessibility checks.

## CRITICAL - ALWAYS DO
- Run unit and integration checks inside containers.
- Keep Docker, Incus, and browser E2E scripts bounded and isolate runtime data under project-scoped OS temp directories.
- Maintain ReadTheDocs files and docs that match shipped behavior.
- Keep the six required translation catalogs (`en`, `es`, `fr`, `ar`, `de`, `it`) in key parity.
- Translate every human-readable string and keep interpolation variables and locale keys in parity with English.

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
For complete details, see AI.md PART 28, 29, 30.
