# AI Assistant Rules (PART 0, 1)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Do not modify AI.md; it is read-only and authoritative.
- Do not guess when a material requirement is unclear.
- Never claim verification that was not performed.

## CRITICAL - ALWAYS DO
- Read the relevant PART before every implementation task.
- Preserve user changes and update product docs when behavior changes.
- Run Go builds and tests in containers, never directly on the host.
- Treat these summaries as navigation aids; AI.md is authoritative when a summary is stale or incomplete.
- Treat PART 33 as reference-only; keep IDEA.md focused on product WHAT, not implementation HOW.
- Do not create audit/report files for normal alignment work; use TODO.AI.md only when three or more implementation tasks remain.
- For defects or contradictions inside AI.md, never edit AI.md or log the issue in TODO.AI.md; report one bullet under the project's section in the parent apimgr/TODO.API.md, following the user's task instructions.

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
- Do not use TODO.AI.md as an implementation backlog when the user restricts it to AI.md defects.

---
For complete details, see AI.md PART 0, 1.
