# Project Structure Rules (PART 2, 3, 4)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Do not place source outside the prescribed src/ layout.
- Do not modify license terms or dataset attribution.

## CRITICAL - ALWAYS DO
- Keep runtime data outside the repository.
- Embed the city and ZIP snapshots into the server binary.
- Preserve GeoNames IDs and required attribution.
- Keep IDEA.md focused on product behavior and AI.md as the implementation specification.
- Keep `AGENTS.md` as a real file identical to the short `CLAUDE.md` loader.
- Keep the project MIT license and embedded third-party license attribution complete.

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
For complete details, see AI.md PART 2, 3, 4.
