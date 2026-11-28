# Configuration Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never add configuration keys outside the specified namespaces or silently change defaults.
- Never use environment variables or flags to bypass the specified file-based server configuration model.
- Never persist secrets in ordinary configuration files or logs.

## CRITICAL - ALWAYS DO

- Use the exact configuration paths and types defined in AI.md.
- Validate configuration and report actionable errors; preserve documented mode behavior.
- Support the required area files, secrets handling, path resolution, and hot reload behavior.
- Update configuration examples and reference documentation when behavior changes.

## Key rules

- PART 5 defines approved configuration libraries and file formats.
- PART 6 defines application modes and precedence.
- PART 12 defines server configuration layout, defaults, and reload behavior.

For complete details, see AI.md PART 5, 6, 12.
