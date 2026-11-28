# CI/CD Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never make CI call the local Makefile as its build/test contract.
- Never use mutable/unverified third-party actions or omit the required provider workflows.
- Never let a workflow publish on an unapproved event or without required checks.
- Never claim a push is complete before checking the provider CI run.

## CRITICAL - ALWAYS DO

- Keep all five required provider configurations aligned with PART 27.
- Run tools directly in the specified toolchain container and match the Makefile test steps.
- Pin and verify workflow dependencies according to the project CI conventions.
- Validate workflow syntax and behavior; after push, inspect CI and fix failures.

## Key rules

- PART 27 defines provider events, jobs, release flows, secret handling, and required gates.
- Local Makefile targets are for development; CI expresses its own equivalent commands.

For complete details, see AI.md PART 27.
