# AI Assistant Rules (PART 0, 1)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never modify AI.md; it is the read-only implementation source of truth.
- Never guess about product behavior, skip a relevant PART, or add unspecified features.
- Never leave stubs, TODO/FIXME/HACK markers, or partial implementations.
- Never treat code, generated documentation, or an existing audit as more authoritative than AI.md.
- Never run host-affecting or host-execution work outside the isolation rules in AI.md.

## CRITICAL - ALWAYS DO

- Read the relevant AI.md PART before each task and search for existing patterns.
- Implement all specified behavior, preserve user changes, and verify the result.
- Resolve AI.md contradictions in the project issue list for later template correction; do not change AI.md.
- Keep project documentation aligned and complete the applicable final-checkpoint review.
- Follow repository communication, temp-directory, test-gate, and gitcommit instructions.

## Key rules

- PART 0 defines the workflow and safety rules; PART 1 defines project-wide implementation constraints.
- IDEA.md describes product behavior and must yield to AI.md where they conflict.
- Progress belongs in TODO.AI.md/TODO.md, not loader files or AI.md.

For complete details, see AI.md PART 0, 1.
