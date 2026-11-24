# Configuration Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Do not expose runtime configuration mutation through the API.
- Do not trust proxy headers from an untrusted peer.

## CRITICAL - ALWAYS DO
- Use `server.yml` plus `yaml.d/` for node and area configuration, and `secrets.yml` for secrets.
- Validate all configuration and request values.
- Apply security defaults and reload supported settings safely.
- Keep secrets out of public responses and logs; use the config directory for operator-managed files.
- Keep unknown keys fatal and apply CLI > environment > file > defaults precedence.

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
For complete details, see AI.md PART 5, 6, 12.
