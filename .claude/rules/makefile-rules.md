# Makefile Rules (PART 25)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never use Makefile targets in CI/CD.
- Never run Go directly on the host.

## CRITICAL - ALWAYS DO
- Provide the prescribed `dev`, `local`, `build`, `test`, `release`, and `docker` targets plus safe cleanup.
- Use casjaysdev/go:latest through Docker for Go operations.
- Keep build and test artifacts in their prescribed locations.
- Keep Makefile targets out of CI workflows; workflows invoke their tool commands directly.

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
For complete details, see AI.md PART 25.
