# Testing, Documentation, and Accessibility Rules (PART 28, 29, 30)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never run Go builds, tests, or binaries on the host.
- Never put runtime/test data in the repository or use production/development Compose files for automated tests.
- Never declare completion from compilation alone; never leave docs, translations, or accessibility checks stale.
- Never run tests outside the specified Docker/Incus and test-gate workflows.

## CRITICAL - ALWAYS DO

- Run `make test` and required basic/full integration tiers in their prescribed isolated environments.
- Keep temporary runtime data under project-scoped OS temp directories and clean up only task-created resources.
- Keep ReadTheDocs navigation and content synchronized with actual features and server pages.
- Localize all user-visible strings and meet PART 30 accessibility requirements.

## Key rules

- PART 28 defines testing, isolation, beta testing, and verification; PART 29 defines documentation; PART 30 defines i18n and accessibility.
- Verify observed behavior and report actual command output; a successful build does not prove product completion.

For complete details, see AI.md PART 28, 29, 30.
