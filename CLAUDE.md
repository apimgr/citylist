# Project SPEC

Project: citylist
Role: Efficient loader for AI.md

⚠️ **THIS FILE IS AUTO-LOADED EVERY CONVERSATION. FOLLOW IT EXACTLY.** ⚠️

Purpose:
- This file is a short loader for the most important rules.
- `AI.md` is the full source of truth for implementation.
- `IDEA.md` defines Citylist's product behavior and constraints.
- Read each relevant `AI.md` PART completely before implementation.

## Asking Questions

- Continue through implied work; do not ask permission to keep going.
- Never guess when a material product, behavior, or safety decision is missing.
- Do not ask to confirm routine edits or validations.
- When a user asks a question, answer it instead of treating it as a command.

## Before ANY Code Change

1. Read the relevant PART in `AI.md`.
2. Search for existing implementations and patterns.
3. Follow the spec exactly; preserve current user changes.
4. Verify the behavior with the appropriate project checks.

## Binary Terminology

- `citylist` is the server binary.
- `citylist-cli` is the required companion client.

## NEVER Do

- Modify `AI.md`; it is read-only and authoritative.
- Add user accounts, login, write APIs, paid tiers, or runtime external API calls; see `IDEA.md`.
- Build Go binaries or run tests directly on the host; use Docker/Incus per AI.md PART 28.
- Use CGO, external schedulers, or client-side-only rendering.
- Put Dockerfiles or Compose files in the repository root.
- Claim completion without checking actual command output.

## ALWAYS Do

- Keep `IDEA.md`, README, docs, OpenAPI, and GraphQL aligned with implemented behavior.
- Keep the server read-only and public, with input validation, bounded requests, and abuse controls.
- Build static binaries for all eight required OS/architecture targets.
- Keep CLI runtime state in the invoking user's config/data/cache/log directories with private permissions.
- Keep generated runtime/test/build data under project-scoped OS temp directories.
- Run tests and direct linters through the configured session gate wrapper.
- Keep all source and documentation edits in this repository.

## Where to Find Details

- AI behavior and core rules: `.claude/rules/ai-rules.md` (PART 0, 1)
- Project structure and license: `.claude/rules/project-rules.md` (PART 2, 3, 4)
- Configuration and modes: `.claude/rules/config-rules.md` (PART 5, 6, 12)
- Binaries and client: `.claude/rules/binary-rules.md` (PART 7, 8, 32)
- Backend and security: `.claude/rules/backend-rules.md` (PART 9, 10, 11, 31)
- APIs: `.claude/rules/api-rules.md` (PART 13, 14, 15)
- Frontend: `.claude/rules/frontend-rules.md` (PART 16)
- Feature services: `.claude/rules/features-rules.md` (PART 17-22)
- Services: `.claude/rules/service-rules.md` (PART 23, 24)
- Makefile: `.claude/rules/makefile-rules.md` (PART 25)
- Containers: `.claude/rules/docker-rules.md` (PART 26)
- CI/CD: `.claude/rules/cicd-rules.md` (PART 27)
- Testing, docs, translations: `.claude/rules/testing-rules.md` (PART 28-30)
- Full specification and final checklist: `AI.md`

## Current Project State

- Last read AI.md: 2026-10-31
- Current task: bring the repository into alignment with AI.md and verify it.
- Relevant PARTs: 0-32 and FINAL CHECKPOINT, as applicable to IDEA.md.
