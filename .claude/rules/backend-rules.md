# Backend and Security Rules (PART 9, 10, 11, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Do not use SELECT * in committed application code.
- Do not use the app as a relay for caller-supplied destinations.
- Do not let optional Tor/I2P failures prevent the server from starting.

## CRITICAL - ALWAYS DO
- Validate and bound all untrusted input.
- Redact credentials from logs and errors.
- Use idempotent startup schema creation/updates without a migrations table; keep writes behind the store API.
- Keep database reader and writer pools separate, with one SQLite writer and 25/5 default read-pool sizing.
- Own the lifecycle of app-scoped Tor; I2P remains opt-in.

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
For complete details, see AI.md PART 9, 10, 11, 31.
