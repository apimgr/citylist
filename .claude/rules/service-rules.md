# Service Rules (PART 23, 24)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never perform privileged operations without the prescribed escalation boundary and checks.
- Never assume one init system or distribution; do not run service lifecycle operations on the host during tests.
- Never leave system service definitions or support flows inconsistent with the runtime binary.

## CRITICAL - ALWAYS DO

- Implement privilege escalation and service installation/operation exactly as specified.
- Support the required service managers and preserve least privilege.
- Test service lifecycle behavior in the prescribed isolated OS environment.
- Keep service documentation current with shipped units, scripts, and commands.

## Key rules

- PART 23 governs privilege escalation and service integration.
- PART 24 governs service support and platform-specific behavior.

For complete details, see AI.md PART 23, 24.
