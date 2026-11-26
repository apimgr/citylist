# CLI reference

`citylist-cli` sends bounded, read-only HTTP requests to a Citylist server. The default static artifact provides command mode and a responsive terminal interface. The native graphical interface is a separate optional artifact for Linux, macOS, and Windows. The lookup API remains public; a bearer token identifies the owner for token management and is required for MCP.

## Choose a server

The client resolves its server in this order: `--server URL`, `CITYLIST_SERVER_PRIMARY`, then `server.primary` in the CLI profile. A command-line URL is saved only when the profile has no valid primary URL; the environment override is never saved. Citylist does not define a compiled official-site default. On an interactive first run with no server resolved, the client prompts for a URL; non-interactive commands require one to be configured or passed explicitly.

The URL must be an absolute HTTP or HTTPS URL without embedded credentials, a query string, or a fragment. Requests are GET-only, use the configured `server.timeout` (default `30s`), and response bodies are capped at 2 MiB. The client retries eligible failed requests according to `server.retry` (default `3`) and `server.retry_delay` (default `1s`); configuration limits retries to 0–10 and requires a positive timeout.

## Commands

| Command | Arguments | Description |
|---|---|---|
| `city` | `ID` | Look up a GeoNames city ID. |
| `zipcode` | `ZIP` | Look up a US ZIP code. |
| `search` | `QUERY [COUNTRY]` | Search cities, optionally filtering by country code. |
| `zip-search` | `QUERY [STATE]` | Search ZIP locations, optionally filtering by state. |
| `country` | `CODE` | List cities for a country code. |
| `cities` | `[PAGE] [LIMIT]` | List a page of cities. |
| `stats` | — | Show dataset statistics. |
| `random` | — | Get a random city. |
| `nearest` | `LATITUDE LONGITUDE` | Find the nearest city. |
| `nearby` | `LATITUDE LONGITUDE RADIUS_KM` | Find nearby cities. |
| `zip-state` | `STATE [LIMIT]` | List ZIPs for a state. |
| `zip-nearby` | `LATITUDE LONGITUDE RADIUS_KM` | Find nearby ZIP locations. |

If the first positional argument is not a known command, the client treats the arguments as a city search. This makes `citylist-cli London` equivalent to `citylist-cli search London`.

```sh
citylist-cli --server https://YOUR_SERVER city 2643743
citylist-cli zipcode 10001
citylist-cli search London GB
citylist-cli zip-search York NY
citylist-cli country GB
citylist-cli cities 1 50
citylist-cli stats
citylist-cli random
citylist-cli nearest 51.5 -0.1
citylist-cli nearby 51.5 -0.1 25
citylist-cli zip-state NY 100
citylist-cli zip-nearby 40.75 -73.99 10
citylist-cli London
```

When the default client is started without a command, it opens the terminal interface when attached to a terminal. In non-interactive mode, provide a command. The default artifact is built without GUI support, so display detection never changes it into a graphical application.

## Optional native GUI

Download the separate `citylist-cli-gui-<os>-<arch>` release asset for Linux, macOS, or Windows to use the native graphical interface. This desktop-only artifact is built with GUI support and is dynamically linked. On Linux, it requires the runtime loader used by its build toolchain and may not run on distributions with a different loader. The static `citylist-cli-<os>-<arch>` artifact remains the portable command and terminal client; its update flow does not replace the GUI artifact.

## Output and language

Use `--output json`, `--output table`, or `--output plain` to select a format. The default is table output. `--json` is a shortcut for `--output json`. Plain output requests `text/plain` from the server where supported. Table output formats record arrays and individual records as columns.

The client embeds six language catalogs: English (`en`), Spanish (`es`), French (`fr`), Arabic (`ar`), German (`de`), and Italian (`it`). Its help, prompts, messages, errors, GUI, TUI, and shell completion labels use these catalogs. `--lang CODE` selects both the client output language and the `Accept-Language` sent with requests. Language precedence is `--lang`, profile `lang`, `LC_ALL`, `LANG`, then English. Unsupported values fall back to English. See [Translating](translating.md) for the catalog format, glossary, and contribution guidance.

## Flags

Options must precede the command because the standard Go flag parser stops at the first positional argument.

| Flag | Purpose |
|---|---|
| `--server URL` | Select the server URL. |
| `--token TOKEN` | Provide a Bearer token for token-protected endpoints; a token can be saved to the profile. |
| `--token-file FILE` | Read a token from a file for this invocation. |
| `--config NAME` | Select a profile filename in the standard CLI config directory; `.yml` is added when omitted. |
| `--output FORMAT` | Choose `json`, `table`, or `plain`. |
| `--json` | Shortcut for JSON output. |
| `--lang CODE` | Select a supported request language. |
| `--color MODE` | Accepts `auto`, `yes`, or `no`. |
| `--debug` | Enable debug logging for this invocation. |
| `--quiet` | Suppress non-essential output. |
| `--verbose` | Print request details to stderr. |
| `--shell completions [SHELL]` | Print completions for bash, zsh, fish, sh, dash, ksh, PowerShell, or pwsh. |
| `--shell init [SHELL]` | Print a shell initialization command. |
| `--version` | Print build information. `-v` is an alias. |
| `--help` | Print command and flag help. `-h` is an alias. |

Boolean flags accept a bare flag as true or an explicit `--flag=value`. Explicit values use the shared case-insensitive truthy/falsy vocabulary, including `yes/no`, `on/off`, and `enable/disable`; invalid values return an error.

Examples:

```sh
citylist-cli --output table --server https://YOUR_SERVER search London
citylist-cli --json stats
citylist-cli --shell completions bash
citylist-cli --shell init fish
```

## Configuration and runtime files

The default profile is `cli.yml` under `${XDG_CONFIG_HOME:-~/.config}/apimgr/citylist` on Unix-like systems or `%APPDATA%\apimgr\citylist` on Windows. It stores the primary server URL, optional token, output format, and language preference. Unix config directories are mode `0700`; the profile is mode `0600`.

The client creates private data, cache, and log directories in the invoking user's XDG locations or `%LOCALAPPDATA%`. These paths and the supported environment variables are documented in [Configuration](configuration.md).

## Server maintenance commands

The `citylist` server binary provides operator-only maintenance commands separately from `citylist-cli`. These commands read or change server state and are not available through the public HTTP API. Commands that require operator authorization accept the operator token through a hidden terminal prompt, or run as root. Token values are never printed by token maintenance.

| Command | Effect |
|---|---|
| `citylist --maintenance token list` | List owner and linked token prefixes, metadata, and status. Raw tokens and token hashes are not shown. |
| `citylist --maintenance token revoke PREFIX` | Revoke one linked token, or revoke the primary token and all linked tokens for that owner. Type the exact prefix when prompted. |
| `citylist --maintenance token revoke PREFIX --yes` | Run the same revocation without the typed confirmation; intended for scripts. |
| `citylist --maintenance secret rotate installation_secret` | Rotate the installation HMAC root and re-encrypt the project PGP private key. Requires operator authorization and typed confirmation. |
| `citylist --maintenance secret rotate encryption_key` | Rotate the at-rest encryption key and re-encrypt stored token and security-report data. Requires operator authorization and typed confirmation. |
| `citylist --maintenance data export --owner PREFIX [--output FILE]` | Export an owner's metadata as a private JSON file. Use `--output -` to write JSON to standard output. The export does not contain token values. |
| `citylist --maintenance data delete --owner PREFIX [--yes]` | Delete the owner and associated tokens after typing the owner ID; `--yes` skips that confirmation for scripts. |
| `citylist --maintenance compliance report` | Print enabled standards, effective retention settings, aggregate counts, recent compliance-event count, and last backup time. |
| `citylist --maintenance backup [file] [--include-ssl] [--include-data]` | Create and verify a backup. A local SQLite database or enabled libSQL SQLite cache is included; the remote database itself is never archived. |
| `citylist --maintenance backup test [file]` | Verify the selected or newest backup archive and its integrity. |
| `citylist --maintenance restore FILE` | Restore configuration and local database state. A restored database seeds a configured remote libSQL database only when that remote database is empty. |

These commands use the configured SQLite or libSQL database. `token revoke` confirms the prefix before changing state; revoking a primary token affects every linked token for that owner. Data export and deletion identify the owner using a prefix shown by `token list`. A remote database is never included in a backup; the local SQLite cache is included when enabled. For all server options, run `citylist --help` or `citylist --maintenance --help`.

## Scheduler commands

The `citylist` server binary exposes the persistent background scheduler for local operators. Task state and the latest 100 execution records per task are stored in the configured application database.

| Command | Effect |
|---|---|
| `citylist scheduler list` | List registered tasks, enabled state, schedule, and last/next run times. |
| `citylist scheduler show ID` | Show a task's state, run counts, and last error. |
| `citylist scheduler run ID` | Run an enabled task immediately and wait for completion. |
| `citylist scheduler enable ID` | Enable a registered task and persist the change. |
| `citylist scheduler disable ID` | Disable a registered task and persist the change. |
| `citylist scheduler history ID` | Show the task's recent execution history. |

Task IDs are listed by `scheduler list`. Tasks that depend on an inactive optional service are not registered. Scheduler output is written to the invoking terminal; normal scheduled execution writes to the configured logs.

## Updates

The CLI supports `--update check` and `--update yes`. The server exposes `/api/autodiscover` with server details and the configured minimum CLI version, but it currently publishes no platform version/checksum entries and does not serve `/cli/binaries/{binary}`. The AI.md contract does not define where the server obtains binary assets or checksums, so install CLI upgrades from the matching GitHub release asset. The server binary has separate manual update commands described in [Installation](installation.md).
