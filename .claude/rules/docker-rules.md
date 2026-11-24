# Docker Rules (PART 26)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Do not put Dockerfiles or Compose files in repository root.
- Do not run production or development Compose files as an AI agent.
- Do not create runtime volumes inside the repository.

## CRITICAL - ALWAYS DO
- Use a multi-stage casjaysdev/go:latest build and Alpine runtime.
- Build static server and CLI binaries for all required targets in the release workflow.
- Keep entrypoint minimal and end it with exec.
- Run the container with tini, OCI metadata, and a health check.
- Use test scripts or project-scoped temporary directories for runtime state.
- Keep Dockerfiles and Compose definitions under `docker/`, never in the repository root.

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
For complete details, see AI.md PART 26.
