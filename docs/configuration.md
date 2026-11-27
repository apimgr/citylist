# Configuration

Citylist reads `server.yml`, ordered YAML area files from `yaml.d/`, and `secrets.yml`. A fresh install creates only the node-specific `server.yml` and `secrets.yml`; built-in defaults supply other settings until an operator creates an area file or an existing all-in-one `server.yml` is migrated. The configuration directory is enforced as mode `0700`; `server.yml`, area files, and `secrets.yml` are enforced as mode `0600`. There is no web administration panel. Watched configuration files reload when changed; restart the process for settings that are only read during startup.

On first server startup, Citylist also creates `secrets.yml` with mode `0600`. It contains a randomly generated `adm_` operator token, a 32-byte AES encryption key, and separate `mtr_` bearer tokens for the Prometheus, Grafana, and Loki metrics endpoints. The operator token is printed once to the server's standard output; the metrics tokens are read from `secrets.yml`. These values are merged into runtime configuration and are never written to `server.yml`. `citylist --status` reads listener settings without creating secrets.

## Effective default settings

The block is a merged reference view of the defaults; it is not a literal `server.yml`. Citylist also has additional empty contact-recipient and SMTP fields listed in the reference below. `server.yml` holds this node's listener, identity, database, cache, SSL, and trusted-proxy settings. Shared settings are placed in the corresponding area files when configured.

```yaml
server:
  port: ""
  fqdn: ""
  address: "[::]"
  baseurl: /
  mode: production
  api_version: v1
  update:
    branch: stable
    auto_install: false
    defer_days: 0
    cli_min_version: ""
  i18n:
    default_language: en
    available_languages: [en, es, fr, de, it, ar]
  database:
    driver: sqlite
    pool:
      max_open: 25
      max_idle: 5
      max_lifetime: 5m
      max_idle_time: 1m
  healthz:
    root:
      enabled: false
  metrics:
    enabled: true
    root:
      enabled: true
    auth:
      allow_unauthenticated: false
    include_system: true
    include_runtime: true
    loki:
      max_entries: 1000
      max_age: 1h
    duration_buckets: [0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10]
    size_buckets: [100, 1000, 10000, 100000, 1000000, 10000000]
  logging:
    access_format: apache
    level: info
  privacy:
    data:
      sold: false
    consent:
      show_until_acknowledged: true
      message: "In accordance with the EU GDPR law this message is being displayed. We use cookies for essential site functionality and, with your consent, for preferences and analytics. Your data is stored on our servers and is never sold."
      message_if_sold: "In accordance with the EU GDPR law this message is being displayed. We use cookies for essential site functionality and, with your consent, for preferences and analytics. Your data may be shared with or sold to third parties as described in our Privacy Policy."
      policy:
        text: Privacy Policy
        url: /server/privacy
      buttons:
        decline: Decline
        accept: I Agree
      position: bottom
      show_preferences: true
      preferences_text: Manage Preferences
  cors:
    allowed_origins: ["*"]
    allow_credentials: true
    max_age: 86400
  contact:
    security:
      email: security@{fqdn}
    dpo:
      name: ""
      email: ""
  pages:
    contact:
      enabled: true
      captcha: simple
      antispam:
        honeypot: true
        min_fill_seconds: 3
        max_age_seconds: 43200
        per_ip: 5
        per_email: 3
        global: 60
        window: 1h
        max_links: 10
        reject_markup: true
        strikes: 3
        ban_hours: 24
        review_min_seconds: 2
        review_max_seconds: 900
        duplicate_window_hours: 24
      success_message: "Thank you for your report."
  notifications:
    email:
      smtp:
        host: ""
        port: 587
        username: ""
        tls: auto
      from:
        name: ""
        email: ""
      events:
        ssl_expiring: true
        ssl_renewed: false
        ssl_renewal_failed: true
  tokens:
    cookie_max_age: 1y
    max_linked: 25
    idle_purge_days: 0
web:
  announcements:
    enabled: true
    messages: []
  ui:
    theme: dark
  robots:
    allow: [/, /api]
    deny: [/debug]
  security:
    report_url: https://github.com/apimgr/citylist/security/advisories/new
    contact: security@{fqdn}
    expires: "{1year}"
    policy: ""
    keyservers: []
    publish_pgp_key: true
```

The `?lang=` query parameter selects a supported language for the current request and saves it in the `lang` cookie for one year only when preference-cookie consent is enabled. Subsequent requests use that choice until it is cleared or changed in `/server/preferences`. Without an allowed saved selection, language detection uses the best supported `Accept-Language` match, then `server.i18n.default_language`. `server.i18n` has two settings: `default_language` and `available_languages`; the cookie name (`lang`) and one-year lifetime are fixed. The client uses the same six locale catalogs for localized help, prompts, and messages.

## Configuration drop-ins

Citylist merges built-in defaults, `server.yml`, then `.yml` and `.yaml` files in `yaml.d/` in byte-wise filename order. Nested maps merge by key; a scalar or list replaces the earlier value, and lists are never appended. A `null` value clears an earlier override and restores that setting's built-in default. Hidden files, backups, other extensions, subdirectories, and symlinks that resolve outside `yaml.d/` are ignored. The directory is private (`0700`) and drop-in files are private (`0600`).

Use the file assigned to the setting area. The automatic migration puts settings found in an older all-in-one `server.yml` into these files:

| File | Settings |
|---|---|
| `yaml.d/branding.yml` | `server.branding`, `server.seo`, `web.ui` |
| `yaml.d/web.yml` | Other `web.*`, plus `server.cors`, `server.csrf`, `server.url_detection`, `server.compression`, `server.healthz`, `server.i18n` |
| `yaml.d/pages.yml` | `server.pages` |
| `yaml.d/contact.yml` | `server.contact` |
| `yaml.d/privacy.yml` | `server.privacy`, `server.compliance`, `server.tracking` |
| `yaml.d/security.yml` | `server.security`, `server.rate_limit`, `server.geoip`, `server.limits`, `server.tokens`, `data.*` |
| `yaml.d/logs.yml` | `server.logs` |
| `yaml.d/metrics.yml` | `server.metrics` |
| `yaml.d/notifications.yml` | `server.notifications` |
| `yaml.d/tor.yml` | `server.tor`, top-level `tor` |
| `yaml.d/i2p.yml` | `server.i2p` |
| `yaml.d/maintenance.yml` | `server.maintenance`, scheduler and update settings |

The built-in persistent scheduler registers `ssl_renewal`, `geoip_update`, `blocklist_update`, `security_cleanup`, `keyserver_retry`, `cve_update`, `update_check`, `token_cleanup`, `log_rotation`, `backup_daily`, `backup_hourly`, `disk_check`, `database_check`, `healthcheck_self`, `dataset_health`, and `maintenance_health`. `dataset_health` refreshes registered dataset checks every 60 seconds. `maintenance_health` checks recovery every 10 seconds and cannot be disabled or rescheduled; actual recovery attempts follow `server.maintenance.self_healing.retry_interval`. `database_check` probes both database handles at `server.notifications.database_error.check_interval`; the threshold and repeat interval control operator alerts. `keyserver_retry` checks durable keyserver retry state every 15 minutes and makes no network request until an attempt is due. The `branding_image_refresh` task fetches configured remote logos, favicons, and social-card images at startup and on a fixed daily schedule; the task cannot be disabled or rescheduled, and it fetches only when the cached image is at least 24 hours old. A failed fetch leaves the previous cached image available. `tor_health` and `i2p_health` are registered only when their respective overlay services are enabled. Disabled GeoIP and blocklist features do not register their update tasks. Other configurable tasks use the schedule and enabled state under `server.scheduler.tasks`; a configured task without a registered implementation is skipped.
| `yaml.d/app.yml` | Project-specific and otherwise unassigned settings |

For example, use `yaml.d/i2p.yml` to keep overlay settings separate from the listener configuration:

```yaml
server:
  i2p:
    enabled: false
```

The supported I2P controls are `server.i2p.enabled`, `binary`, `sam_address`, `virtual_port`, `inbound_length`, `outbound_length`, `inbound_quantity`, `outbound_quantity`, `signature_type`, and `bootstrap_timeout`. I2P defaults off on a host or VM and on in a container; `--i2p`, `I2P_ENABLED`, and an explicit config value override that computed default, in that order. If enabled without a local `i2pd` process, Citylist may connect to the configured SAM bridge.

Tor hidden service support starts automatically when a `tor` binary is available. Its settings belong in `yaml.d/tor.yml`; `use_network` controls only app-originated outbound HTTP requests. Tor and I2P use dedicated loopback backends and do not change the public listener.

## Settings reference

The owner-token cookie name is the stable `owner_token` project variable in `IDEA.md` (`citylist_owner_token_lgqdhqtw`); it is not operator-editable.

| YAML path | Default | Description and current behavior |
|---|---|---|
| `server.port` | Random unused `64000–64999` on native first start; `80` in the container | The selected native first-run port is saved to `server.yml`. The `--port` flag takes precedence, followed by `PORT`, then the configured value. |
| `server.address` | `[::]` | Dual-stack HTTP bind address. The `--address` flag takes precedence, followed by `LISTEN`, then the configured value. IPv4-only hosts fall back to `0.0.0.0`. |
| `server.fqdn` | empty | Public hostname used for the optional `Canonical` line in `/.well-known/security.txt` and MCP origin validation. |
| `server.token` | generated | Operator bearer token. Runtime value is stored only in `secrets.yml`, never `server.yml`; it authenticates operator checks and MCP requests. |
| `server.security.encryption_key` | generated | Base64-encoded 32-byte encryption key stored only in `secrets.yml`; encrypts recoverable owner and linked token values at rest. |
| `server.security.encryption_key_version` | `1` | Version of the encryption key stored in `secrets.yml`. |
| `server.i18n.default_language` | `en` | Language used when no supported query, cookie, or `Accept-Language` match is available. |
| `server.i18n.available_languages` | `en, es, fr, de, it, ar` | Must contain the six embedded locales exactly once; invalid or partial lists fall back to the complete registry. |
| `server.tor.binary` | auto-detected | Optional Tor executable path. When empty, Citylist searches common locations and `PATH`. |
| `server.tor.use_network` | `false` | Route app-originated outbound HTTP through the managed Tor SOCKS client. Inbound request data cannot select outbound destinations. |
| `server.tor.*` | see AI.md PART 31.1 | Circuit, bootstrap, bandwidth, stream, introduction-point, and virtual-port settings for the app-owned Tor process. |
| `server.i2p.enabled` | host/VM `false`; container `true` | Optional I2P eepsite. Can be overridden with `--i2p`, `I2P_ENABLED`, or config. |
| `server.i2p.binary` | auto-detected | Optional app-owned `i2pd` executable path; when unavailable, the configured SAM bridge is used. |
| `server.i2p.sam_address` | `127.0.0.1:7656` | SAMv3 bridge used when a dedicated `i2pd` process is not selected. |
| `server.i2p.virtual_port` | `80` | I2P address port forwarded to Citylist's dedicated local backend. |
| `server.i2p.inbound_length`, `outbound_length` | `3` | I2P tunnel lengths. |
| `server.i2p.inbound_quantity`, `outbound_quantity` | `5` | I2P tunnel counts. |
| `server.i2p.signature_type` | `7` | SAM destination signature type (EdDSA-SHA512-Ed25519). |
| `server.i2p.bootstrap_timeout` | `300` seconds | Maximum time to wait for I2P tunnel construction. |
| `server.mode` | `production` | Application mode: `production`, `development`, or `debug`. |
| `output.emoji` | automatic | Optional terminal emoji setting. An explicit value overrides `NO_COLOR`; `TERM=dumb` always disables emoji output. |
| `server.api_version` | `v1` | Version segment used for native REST routes, such as `/api/v1/cities`. Invalid values fall back to `v1`. |
| `server.update.branch` | `stable` | Release channel: `stable`, `beta`, or `devel`. Legacy channel labels `daily`, `rolling`, `dev`, and `development` normalize to `devel`. |
| `server.update.auto_install` | `false` | Reserved for scheduled updater installation; Citylist currently supports manual update checks and installation only. |
| `server.update.defer_days` | `0` | Reserved for scheduled stable/beta update eligibility; manual checks and installation are never deferred. |
| `server.update.cli_min_version` | empty | Minimum companion CLI version published by `/api/autodiscover`. Set it only when compatible CLI binaries are available to users; Citylist does not yet serve CLI binary assets. |
| `server.database.driver` | `sqlite` | Database driver: local SQLite or remote libSQL/Turso. |
| `server.database.url` | derived | SQLite file path or libSQL URL, according to the selected driver. `{data_dir}` is replaced with the selected data directory for SQLite; relative SQLite paths resolve under it. |
| `server.database.token` | secret file or environment | Authentication token for a remote libSQL/Turso database. Keep it in `secrets.yml` or provide it through the supported environment reference; do not put credentials in a public config file. |
| `server.database.cache.enabled` | `false` | Enables the separate local SQLite read replica. This option requires the `libsql` driver. |
| `server.database.cache.path` | unset | Cache database path; required when enabled. Relative paths resolve under the server data directory. The path may contain `${DATA_DIR}`. |
| `server.database.cache.sync_interval` | `30s` | How often a transactional reconciliation copies remote state into the cache. |
| `server.database.cache.offline_mode` | `false` | When true, existing cached data can serve reads while libSQL is unavailable; all writes still fail until the remote is available. |
| `server.database.cache.max_age` | `1h` | Maximum replica age used for cache reads. Older data is read from the remote, with cache fallback only when offline mode is enabled and the remote is unavailable. |
| `server.database.pool.max_open` | `25` | Maximum number of read connections. Writes use a separate one-connection pool. |
| `server.database.pool.max_idle` | `5` | Maximum number of idle read connections. |
| `server.database.pool.max_lifetime` | `5m` | Maximum lifetime for read connections. |
| `server.database.pool.max_idle_time` | `1m` | Maximum idle time for read connections. |
| `server.healthz.root.enabled` | `false` | Mounts `/healthz` as a direct alias of `/server/healthz` for tools that require a root-level probe. |
| `server.metrics.enabled` | `true` | Enables Prometheus, Grafana dashboard, and Loki endpoints. All endpoints require their own bearer token from `secrets.yml` by default. |
| `server.metrics.root.enabled` | `true` | Mounts direct `/metrics[/{service}]` aliases in addition to the `/server/metrics` and API aliases. |
| `server.metrics.auth.allow_unauthenticated` | `false` | Disables bearer-token checks for all metrics services. Use only on a firewalled internal network. |
| `server.metrics.auth.tokens.{prometheus,grafana,loki}` | generated | Separate `mtr_` tokens are generated into `secrets.yml`; they are never written to `server.yml`. |
| `server.metrics.include_system` | `true` | Collects CPU, memory, and data-volume disk metrics. |
| `server.metrics.include_runtime` | `true` | Collects Go runtime memory, goroutine, and garbage-collection metrics. |
| `server.metrics.loki.max_entries` | `1000` | Maximum number of recent structured log entries exposed by the Loki service (hard cap: 10,000). |
| `server.metrics.loki.max_age` | `1h` | Maximum age of entries returned by the Loki service. |
| `server.metrics.duration_buckets` | built-in | Histogram buckets for HTTP request duration, in seconds. |
| `server.metrics.size_buckets` | built-in | Histogram buckets for HTTP request and response sizes, in bytes. |

### Metrics endpoints

Metrics remain internal operational endpoints and are not advertised by the public API. Each service uses its own bearer token from `secrets.yml`:

| Endpoint | Response | Token |
|---|---|---|
| `/server/metrics` and `/server/metrics/prometheus` | Prometheus text exposition | `server.metrics.auth.tokens.prometheus` |
| `/server/metrics/grafana` | Importable Grafana 12+ dashboard JSON (`schemaVersion` 42) | `server.metrics.auth.tokens.grafana` |
| `/server/metrics/loki` | Recent bounded Loki-compatible JSON log stream | `server.metrics.auth.tokens.loki` |

The same handlers are mounted at `/api/{api_version}/server/metrics[/{service}]` and `/api/metrics[/{service}]`. When `server.metrics.root.enabled` is true, they are also mounted at `/metrics[/{service}]`. These aliases respond directly and do not redirect. Send credentials only in `Authorization: Bearer <token>`; query-string tokens are not accepted. Set `server.metrics.auth.allow_unauthenticated: true` only when network controls already restrict all metrics routes to trusted monitoring systems.
| `server.logs.level` | mode-dependent | Minimum application log level: `debug`, `info`, `warn`, or `error`. Empty uses `info` in production and `debug` in development/debug mode. |
| `server.logs.defaults.custom` | empty | Optional custom line template used by any log kind configured with `format: custom`; supports the variables listed in AI.md PART 11. |
| `server.logs.defaults.rotate`, `keep` | `weekly,50MB`, `none` | Rotation and archive retention inherited by log kinds that do not override them. Rotation supports time and size policies; retention supports `none`, a count, an age, or `forever`. |
| `server.logs.access.*` | Apache, monthly, inherited retention | HTTP access log format (`apache`, `nginx`, `json`, or `custom`), filename, enablement, rotation, and retention. Successful health checks are always suppressed. |
| `server.logs.{server,error,app,auth,audit,security,debug}.*` | per-kind defaults | Per-file enablement, filename, format, rotation, and retention. Audit logs are JSON Lines; audit settings also control event categories, email masking, compression, and user-agent inclusion. |
| `server.privacy.data.sold` | `false` | States whether personal information is sold or shared. When true, the preferences form exposes the CCPA opt-out control. |
| `web.headers.honor_sec_gpc` | `true` | Honors `Sec-GPC: 1` for the request: suppresses behavioral analytics and preference cookies, records `compliance.gpc_honored`, and shows the opt-out notice on privacy/preferences pages. |
| `web.headers.honor_dnt` | `false` | When enabled, honors `DNT: 1` by suppressing preference and analytics cookies and behavioral analytics, recording `compliance.dnt_honored`, and showing a notice. It does not change the visitor's data sale or sharing choice. |

`server.tracking` configures an optional browser analytics provider. Supported types include Google Analytics, Matomo/Piwik, Open Web Analytics, Fathom, Plausible, Umami, Simple Analytics, and Cloudflare Web Analytics. The provider-specific `id` and, where applicable, `url` are configured in `yaml.d/privacy.yml`. Tracking remains disabled unless the provider and analytics cookie category are enabled and the visitor grants analytics consent; GPC, CCPA opt-out, Tor, and I2P requests suppress it. The privacy page reports the configured provider. Provider requests leave the visitor’s browser when tracking is allowed.

| `server.privacy.consent.show_until_acknowledged` | `true` | Compatibility setting for the required consent banner; the banner remains mandatory until a valid consent choice is recorded. |
| `server.privacy.consent.message`, `message_if_sold` | built-in notices | Main consent text selected according to `server.privacy.data.sold`; unchanged built-in values use the translated locale text. |
| `server.privacy.consent.policy.text`, `policy.url` | `Privacy Policy`, `/server/privacy` | Label and destination for the banner privacy-policy link. Relative paths are mounted under the configured base URL. |
| `server.privacy.consent.buttons.decline`, `buttons.accept` | `Decline`, `I Agree` | Consent action labels. |
| `server.privacy.consent.position` | `bottom` | Banner position; supported values are top and bottom. Invalid values render at the bottom. |
| `server.privacy.consent.show_preferences`, `preferences_text` | `true`, `Manage Preferences` | Displays the link that opens granular consent controls. |
| `server.cors.allowed_origins` | `[*]` | Allowed browser origins. A wildcard policy is suitable for public lookup APIs. |
| `server.cors.allow_credentials` | `true` | Enables credentialed CORS only for matched explicit origins; wildcard responses never include credential permission. |
| `server.cors.max_age` | `86400` | Preflight response cache duration in seconds. |
| `server.pages.contact.enabled` | `true` | Enables the contact and security-report forms and their API endpoint. |
| `server.pages.contact.captcha` | `simple` | Challenge type: `simple`, `recaptcha`, or `hcaptcha`. The external options require site and secret keys. |
| `server.pages.contact.captcha_site_key` | empty | Public site key for the configured external CAPTCHA provider. |
| `server.pages.contact.captcha_secret_key` | empty | Secret key for external CAPTCHA verification; keep it private. |
| `server.pages.contact.antispam.*` | bounded defaults | Contact-form honeypot, timing, per-IP/email/global limits, blocked content, link limits, strikes, temporary bans, duplicate suppression, and review timing. See the default YAML block for the current values. |
| `server.contact.{admin,security,abuse,general}.email` | role-specific | Configures role recipients. Empty role email uses the specified fallback chain; admin has no generated default and must be set explicitly to receive operator email. The default security email is `security@{fqdn}`. |
| `server.contact.{admin,security,abuse,general}.webhooks.*` | empty | Configures one or more named notification transports: `telegram`, `discord`, `slack`, `mattermost`, `pushover`, `gotify`, or `generic`. Webhook URLs and generated sibling `_secret` values are stored in `secrets.yml`. |
| `server.ssl.letsencrypt.email` | empty | Optional ACME account contact. Set it explicitly when configuring Let's Encrypt; Citylist never invents an admin address. |
| `server.contact.dpo.name`, `server.contact.dpo.email` | empty | Optional public data-protection contact details used by `/server/dpo`. |
| `server.notifications.email.smtp.*` | host/credentials empty, port `587`, TLS `auto` | SMTP relay used for contact, security-report, and enabled operator-event notifications. TLS accepts `auto`, `starttls`, `tls`, or `none`. |
| `server.notifications.email.from.*` | empty | Sender name and address used for outbound notification mail. |
| `server.notifications.email.events.*` | Event-specific | Enables or disables operator notification email by event, including `maintenance_entered`, `maintenance_exited`, and `self_healing_exhausted`. SSL expiry warnings run at 7, 3, and 1 day before expiration; renewal success is logged, while renewal failure can send a separate alert. |
| `server.notifications.database_error.*` | enabled, 3 failures, `1m` check interval, `24h` repeat | Probes the read and write database handles; consecutive failures trigger a deduplicated operator event, and recovery resets the alert state. |
| `server.tokens.cookie_max_age` | `1y` | Lifetime of the browser's primary owner-token cookie; valid uses refresh the expiry. |
| `server.tokens.max_linked` | `25` | Maximum active linked API tokens for one owner. |
| `server.tokens.idle_purge_days` | `0` | Disabled by default; when positive, purges owners with no resources after that many idle days. |
| `web.ui.theme` | `dark` | The configuration value is stored, but the current browser theme form reads the visitor's `theme` cookie instead. |
| `web.announcements.enabled` | `true` | Enables scheduled server-rendered site banners. |
| `web.announcements.messages` | `[]` | Ordered notices with `id`, `type`, `title`, `message`, optional RFC3339 `start`/`end`, and `dismissible`. Dismissals are stored in a visitor cookie. |
| `web.robots.allow` | `/`, `/api` | Paths emitted as `Allow` entries in `/robots.txt`. |
| `web.robots.deny` | `/debug` | Paths emitted as `Disallow` entries in `/robots.txt`. |
| `server.branding.title` | `CityList API` | Display name used in the page title, shared header, footer, manifest, Swagger UI, and syndication channels. |
| `server.branding.tagline` | empty | Optional text appended to the configured page title. |
| `server.branding.description` | built-in Citylist description | Description used in generated SEO metadata and RSS channel metadata. |
| `server.branding.content.header`, `.footer` | empty | Optional sanitized HTML fragments added to the shared page header or used in place of the built-in footer branding block. |
| `server.branding.content.about`, `.privacy`, `.terms` | empty | Optional language-aware Markdown, HTML, or text page content. About and privacy content extends built-in content; terms content replaces the built-in terms text. |
| `server.seo.keywords`, `author` | empty | Optional generated keywords and author metadata. |
| `server.seo.title_separator` | ` - ` | Separator between the configured title and tagline; values longer than eight characters reset to the default. |
| `server.seo.meta_tags` | `{}` | Up to 20 additional metadata names and values. Invalid names and generated names are ignored; values are HTML-escaped. |
| `server.seo.canonical` | request origin | Optional absolute HTTPS origin for canonical and OpenGraph URLs. The request path is appended. |
| `server.seo.noindex` | `false` | Marks all pages `noindex,nofollow` and sets `X-Robots-Tag`; otherwise only registered public pages are indexable. |
| `server.seo.opengraph.*`, `server.seo.twitter.*` | enabled | Controls generated OpenGraph and Twitter card metadata. Invalid Twitter card types reset to `summary_large_image`. |
| `server.seo.og_image`, `twitter_handle`, `verification.*` | empty | Optional social card image, site handle, and validated search-engine ownership tags. Images accept an HTTPS URL, supported `data:` URI, or a file name under the private `assets/` directory; invalid values use the embedded fallback. Invalid verification values are omitted. |
| `web.ui.accent` | empty | Optional `#RRGGBB` accent. A valid value generates `/static/css/accent.css`, with theme-specific colors adjusted for 4.5:1 contrast. |
| `server.branding.favicon`, `server.branding.logo` | empty | Optional HTTPS URL, supported `data:` URI, or file name under `{config_dir}/assets/`; local files are limited to 2 MiB and data URIs to 256 KiB. Remote images are fetched over HTTPS with SSRF protections, cached privately under the configured cache directory, and refreshed every 24 hours; cached images remain available if a refresh fails. |
| `web.security.report_url` | GitHub private reporting URL | Primary security-report destination used by the security page and `security.txt`. |
| `web.security.contact` | `security@{fqdn}` | Configured security contact used by the security page and `security.txt`; falls back to `server.contact.security.email`. |
| `web.security.expires` | `{1year}` | Expiry value for `security.txt`; invalid values fall back to one year from the current time. |
| `web.security.policy` | empty | Optional Markdown replacement for the built-in disclosure policy. Rendered links and markup are sanitized. |
| `web.security.keyservers` | `[]` | OpenPGP keyservers used by key publication/rotation maintenance. |
| `web.security.publish_pgp_key` | `true` | Enables `/.well-known/pgp-key.asc` and the `Encryption` line when a keypair exists. |
| `server.maintenance.self_healing.retry_interval` | `30s` | Time between recovery attempts; validated to be at least 10 seconds. |
| `server.maintenance.self_healing.max_attempts` | `0` | Failed recovery attempts before retries stop; zero retries indefinitely. |
| `server.maintenance.cleanup.disk_threshold` | `90` | Disk usage percentage that starts log, project temporary directory, and backup retention cleanup. |
| `server.maintenance.cleanup.log_retention_days` | `7` | Age after which inactive regular log files can be removed during disk pressure cleanup. |
| `server.maintenance.notify.on_enter`, `on_exit` | `true` | Enables operator notifications when maintenance starts or recovers. |

## Directories and environment

The server derives native paths from the current user and operating system. Docker paths are set by the image entrypoint.

| Operating system | Config | Data | Logs |
|---|---|---|---|
| Linux/BSD user | `~/.config/apimgr/citylist` | `~/.local/share/apimgr/citylist` | `~/.local/state/apimgr/citylist` |
| Linux/BSD root | `/etc/apimgr/citylist` | `/var/lib/apimgr/citylist` | `/var/log/apimgr/citylist` |
| macOS user | `~/Library/Application Support/apimgr/Citylist` | `~/Library/Application Support/apimgr/Citylist/data` | `~/Library/Logs/apimgr/Citylist` |
| macOS root | `/Library/Application Support/apimgr/Citylist` | `/Library/Application Support/apimgr/Citylist/data` | `/Library/Logs/apimgr/Citylist` |
| Windows | `%ProgramData%\apimgr\Citylist` | `%ProgramData%\apimgr\Citylist\data` | `%ProgramData%\apimgr\Citylist\logs` |
| Docker image | `/config/citylist` | `/data/citylist` | `/logs` |

The SQLite database defaults to `{data_dir}/db/server.db` natively and `/data/db/sqlite/server.db` in Docker unless `DATABASE_DIR` or `server.database.url` selects another path.

| Variable | Purpose |
|---|---|
| `CONFIG_DIR` | Configuration directory; contains `server.yml`. |
| `DATA_DIR` | Writable application data directory. Dataset snapshots themselves are embedded in the binary. |
| `DATABASE_DIR` | SQLite database directory. Defaults to `DATA_DIR/db` natively and `/data/db/sqlite` in Docker. |
| `BACKUP_DIR` | Backup directory used by the maintenance backup command. |
| `LOG_DIR` | Application log directory. |
| `PORT` | Listen-port override, below `--port` and above `server.port`. |
| `LISTEN` | Bind-address override, below `--address` and above `server.address`. |
| `CITYLIST_SERVER_PRIMARY` | Default server URL for `citylist-cli`; a `--server` flag takes precedence. |
| `CITYLIST_TOKEN` | Optional API bearer token for `citylist-cli`; `--token` or `--token-file` takes precedence. |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_TLS`, `SMTP_FROM_NAME`, `SMTP_FROM_EMAIL` | Override the corresponding SMTP and sender settings in `server.yml`. |
| `NO_COLOR` | Disables automatic terminal color selection when `--color auto` is in use. |

For a custom deployment, `--config`, `--data`, `--cache`, `--log`, and `--backup` override their corresponding environment variables. Port and address precedence is command-line flag, environment variable, config file, then built-in default.

The CLI stores its profile at `${XDG_CONFIG_HOME:-~/.config}/apimgr/citylist/cli.yml` on Unix-like systems and `%APPDATA%\apimgr\citylist\cli.yml` on Windows. The profile stores the primary server URL, request settings, optional API token, output and language preferences, theme, display mode, terminal UI settings, logging, cache, and update preferences. Its directory is mode `0700` and the file is mode `0600` on Unix-like systems. `--config NAME` selects another profile name in the same directory. Server selection order is `--server`, `CITYLIST_SERVER_PRIMARY`, then the profile's `server.primary`; a command-line URL is saved only if the profile has no valid primary URL, and the environment value is not saved. Language precedence is `--lang`, profile `lang`, `LC_ALL`, `LANG`, then English. See [CLI reference](cli.md) for display modes and the separate optional GUI artifact.

The CLI also creates private data, cache, and log directories under the platform's XDG locations (or `%LOCALAPPDATA%` on Windows). It creates `cli.log` with mode `0600` on Unix-like systems.

The server creates its schema idempotently at startup. SQLite uses separate read-only and single-writer pools; its database directory is secured to mode `0700` and the database file to `0600`. SQLite must be on a local filesystem that supports WAL; network filesystems are not supported. Remote libSQL/Turso connections use the configured bounded connection pool and authentication token. Its optional local cache is a separate SQLite read replica, stored as a private `0600` file in a `0700` directory. Remote state always wins; reconciliation pulls and applies a batch in one local transaction, including deletions, and cache data is never written back to libSQL. Reads bypass a dirty cache until a full sync commits. When a remote write succeeds but its local cache update fails, the remote success is returned and the affected cached row is deleted best-effort; a failed remote write does not change the cache. In offline mode, an existing usable cache serves reads and writes fail until the remote recovers. Entries older than `max_age` are read from the remote.

## Command-line overrides

The server accepts `--port`, `--address`, `--config`, `--data`, `--cache`, `--log`, `--backup`, `--pid`, `--baseurl`, `--shell`, `--lang`, `--service`, `--maintenance`, and `--update`. `server.baseurl` sets the default URL prefix in the merged configuration; `--baseurl /prefix` takes precedence and mounts the HTTP application under that path. A base URL change is applied by the config watcher. `--mode production|development|debug` selects the mode for the current run and overrides `MODE` and `server.mode`. Run `citylist --help` for the complete server command list.
