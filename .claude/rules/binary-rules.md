# Binary and Client Rules (PART 7, 8, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never enable CGO or add an undocumented runtime dependency.
- Never omit the required `citylist-cli` companion client or build fewer than the eight specified targets.
- Never execute Go builds or project binaries directly on the host.
- Never make the server's read-only public lookup API writable or require user accounts.

## CRITICAL - ALWAYS DO

- Build static binaries with the required names, flags, metadata, and OS/architecture matrix.
- Keep `citylist` as the server binary and `citylist-cli` as the companion client.
- Implement the specified server CLI and client commands, setup, config, and update behavior.
- Exercise `--help`, `--version`, and client/server behavior in isolated containers.

## Key rules

- PART 7 governs binary toolchain and release properties.
- PART 8 governs the server command line; PART 32 governs the client binary and its runtime state.
- Client state belongs in the invoking user's config/data/cache/log directories with private permissions.

For complete details, see AI.md PART 7, 8, 32.
