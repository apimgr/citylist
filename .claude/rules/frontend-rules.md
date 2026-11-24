# Frontend Rules (PART 16)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Do not use client-side-only rendering.
- Do not add JavaScript for functionality already provided by HTML/CSS.

## CRITICAL - ALWAYS DO
- Render pages server-side with Go templates.
- Support mobile-first accessible layouts and dark/light/auto themes.
- Keep every registered page and HTML error response within the shared accessibility and semantic-HTML conformance checks.
- Keep links and assets correct behind configured base paths.
- Make core page functionality work without JavaScript; add JavaScript only for capabilities HTML and CSS cannot provide.
- Translate every visible string in all six required catalogs and use toast feedback instead of JavaScript alerts.

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
For complete details, see AI.md PART 16.
