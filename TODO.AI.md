# TODO.AI

## Alignment with AI.md (main, 086f76c9)

Dependency order: establish the required package boundaries before updating the build and test targets; then reconcile server documentation and CI behavior, run the full verification matrix, and remove completed items as each is finished.

- [ ] Restructure server startup to the PART 28 contract: `src/main.go` contains only the thin entry point; `src/config` parses flags/environment and resolves paths; `src/app` owns runtime startup, signal handling, and graceful shutdown; build metadata is defined in `src/version.go`.
- [ ] Replace the Makefile with exactly the six PART 25 targets and implement debug builds, production artifacts without publishing, local Docker image build, and the full test gate including staticcheck, vulnerability/shell checks, coverage, and the basic binary tier.
- [ ] Align the CI workflow gates with PART 27 and the Makefile test contract, while preserving the multi-provider requirements and current provider workflows.
- [ ] Bring README and all server/developer documentation into agreement with the current Makefile, startup architecture, runtime behavior, and supported server pages/APIs.
- [ ] Re-audit the complete AI.md checklist against the implementation and fix every remaining applicable requirement before final verification.
- [ ] Add the 13 required `.claude/rules/` summaries from their assigned AI.md PARTs; the directory is absent.
