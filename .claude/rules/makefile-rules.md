# Makefile Rules (PART 25)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never add Makefile targets beyond `build`, `test`, `release`, `docker`, `clean`, and `help`.
- Never publish, tag, or push artifacts from local Makefile targets.
- Never run Go build/test tools on the host or route CI through `make`.
- Never add an `all` target or make it the default.

## CRITICAL - ALWAYS DO

- Make `help` the default target and document all six required targets.
- Build all eight platforms; separate debug builds from optimized release artifacts.
- Make `test` run lint, vulnerability checks, unit tests/coverage, and required binary checks.
- Use `release.txt` and version precedence only as specified; do not invent an official site.

## Key rules

- Local builds write to `binaries/` and `releases/`; those are ignored build outputs.
- The Docker target builds locally and never pushes. CI owns publishing.

For complete details, see AI.md PART 25.
