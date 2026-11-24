# Binaries and Client Rules (PART 7, 8, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never enable CGO.
- Never omit a required platform or shell completion.

## CRITICAL - ALWAYS DO
- Build server and client as static binaries for all eight required targets.
- Keep CGO disabled and embed application assets while downloading security databases at runtime.
- Keep client state in user-scoped OS directories with private file modes.
- Auto-detect CLI/TUI/GUI mode as specified.
- Keep shell completions and supported language catalogs synchronized with the CLI command registry.
- Keep the `citylist` server and `citylist-cli` companion client as distinct binaries.

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
For complete details, see AI.md PART 7, 8, 32.
