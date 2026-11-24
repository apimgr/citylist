# Service Support Rules (PART 23, 24)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Do not hard-code one platform service manager.
- Do not create or modify host service state during ordinary tests.

## CRITICAL - ALWAYS DO
- Drop privileges according to the service rules.
- Ship supported service templates and manage lifecycle explicitly.
- Keep the application PID file distinct from the service manager's supervisor PID file.
- Apply platform-specific permissions and account creation without changing shared system directory ownership.

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
For complete details, see AI.md PART 23, 24.
