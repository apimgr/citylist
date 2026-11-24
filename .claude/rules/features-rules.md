# Feature Services Rules (PART 17, 18, 19, 20, 21, 22)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Do not use external cron or task schedulers.
- Do not make GeoIP the sole access decision.

## CRITICAL - ALWAYS DO
- Use the built-in scheduler for background work.
- Make enabled services configurable and observable.
- Keep backup encryption and update verification aligned with the spec.
- Isolate each scheduled task so a task panic cannot stop the scheduler or other tasks.
- Persist task outcomes and retry interrupted tasks according to their configured retry policy.
- Run durable `keyserver_retry` submissions every 15 minutes and verify backups before reporting success.

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
For complete details, see AI.md PART 17, 18, 19, 20, 21, 22.
