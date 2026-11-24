# CI/CD Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Do not use Makefile in CI.
- Do not install Go tools inline in workflow steps.
- Do not leave pushed CI failures unresolved.

## CRITICAL - ALWAYS DO
- Use the GitHub workflow location for this GitHub repository.
- Keep equivalent provider-native pipelines for the repository's configured GitLab, Gitea, Forgejo, and Jenkins CI systems.
- Pin third-party actions by full commit SHA.
- Run test, lint, vulnerability, and secret scanning gates.
- Keep workflow permissions least-privilege and resolve failed CI checks after a push.

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
For complete details, see AI.md PART 27.
