# Feature Service Rules (PART 17-22)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never add external schedulers or undocumented network/service dependencies.
- Never send email, notifications, or external updates unless configured as specified.
- Never collect or expose GeoIP, metrics, backup, or update data beyond their documented contracts.
- Never leave any specified service as a stub or silently disable a required capability.

## CRITICAL - ALWAYS DO

- Implement email/notifications, scheduler, GeoIP, metrics, backup/restore, and update behavior as specified.
- Honor configuration, mode, safety, retention, and failure-handling requirements for each feature.
- Keep operational output, audit events, documentation, and user-facing pages accurate.

## Key rules

- PART 17: email and notifications; 18: built-in scheduler; 19: GeoIP; 20: metrics; 21: backup/restore; 22: updates.
- Optional integrations remain disabled until their specified configuration enables them.

For complete details, see AI.md PART 17-22.
