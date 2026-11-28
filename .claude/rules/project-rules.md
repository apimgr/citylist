# Project Structure Rules (PART 2, 3, 4)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never invent or alter license, attribution, project identity, or product behavior.
- Never place runtime data, secrets, or generated artifacts among project source files.
- Never guess platform-specific paths or persist state outside the prescribed user/system directories.

## CRITICAL - ALWAYS DO

- Preserve the project license and required attribution in source and distributed artifacts.
- Keep the repository layout, required files, and naming aligned with PART 3.
- Resolve config, data, cache, state, and log paths using PART 4 conventions.
- Keep runtime/test/build data in project-scoped OS temp locations when required by the task instructions.

## Key rules

- Required structure and optional files are specified by the project profile and PART 3 inventory.
- Use platform-native directories and private permissions for sensitive per-user state.
- AI.md remains the implementation authority; project identity and user-provided values are not guessed.

For complete details, see AI.md PART 2, 3, 4.
