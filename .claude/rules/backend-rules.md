# Backend and Security Rules (PART 9, 10, 11, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never swallow errors, return unbounded responses, expose secrets, or log raw credentials/tokens.
- Never bypass input validation, authorization boundaries, rate/abuse controls, or safe database access.
- Never add an external runtime service dependency beyond the documented optional overlay daemons.
- Never enable Tor/I2P behavior contrary to the required detection and opt-in rules.

## CRITICAL - ALWAYS DO

- Use the specified error handling, cache bounds, database practices, and audit logging.
- Keep public server APIs read-only; validate and bound all input and resource use.
- Apply PART 11 security controls and redact sensitive data from output.
- Implement Tor auto-detection and I2P defaults/configuration exactly as PART 31 defines.

## Key rules

- PART 9 covers errors and caching; PART 10 covers persistence; PART 11 is the security/logging authority.
- Overlay processes are optional at runtime and the server must continue without them.

For complete details, see AI.md PART 9, 10, 11, 31.
