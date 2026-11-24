# TODO.AI

Project implementation gaps verified against AI.md and the current repository.

- [ ] Implement the opt-in SQLite cache mode in PART 10 (AI.md lines 12087–12123), including cache-first reads, remote-first writes, offline read-only behavior, background sync, max-age handling, and the specified cache invalidation/dirty-state behavior after cache-update failures.
- [ ] Change `api_tokens.id` from the current integer key to the public UUID v7 text key required by AI.md PART 10 (line 12288), including a safe migration for existing databases and aligned route, test, and documentation behavior.
