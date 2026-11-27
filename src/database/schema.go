package database

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS rate_limits (
		key TEXT PRIMARY KEY,
		count INTEGER NOT NULL DEFAULT 1,
		window_start INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	)`,
	`CREATE INDEX IF NOT EXISTS idx_rate_limits_window ON rate_limits(window_start)`,
	`CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		level TEXT NOT NULL DEFAULT 'info',
		category TEXT NOT NULL,
		action TEXT NOT NULL,
		actor_ip TEXT,
		target_type TEXT,
		target_id TEXT,
		details TEXT,
		success INTEGER NOT NULL DEFAULT 1
	)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_category ON audit_log(category)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_actor_ip ON audit_log(actor_ip)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_target ON audit_log(target_type, target_id)`,
	`CREATE TABLE IF NOT EXISTS scheduler_tasks (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		schedule TEXT NOT NULL,
		last_run INTEGER,
		next_run INTEGER,
		last_status TEXT,
		last_error TEXT,
		run_count INTEGER NOT NULL DEFAULT 0,
		fail_count INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS scheduler_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		started_at INTEGER NOT NULL,
		finished_at INTEGER,
		status TEXT NOT NULL,
		error TEXT,
		duration_ms INTEGER
	)`,
	`CREATE INDEX IF NOT EXISTS idx_scheduler_history_task ON scheduler_history(task_id)`,
	`CREATE INDEX IF NOT EXISTS idx_scheduler_history_started ON scheduler_history(started_at)`,
	`CREATE TABLE IF NOT EXISTS backups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		filename TEXT NOT NULL UNIQUE,
		filepath TEXT NOT NULL,
		size_bytes INTEGER NOT NULL,
		type TEXT NOT NULL DEFAULT 'auto',
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		checksum TEXT,
		notes TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_backups_created ON backups(created_at)`,
	`CREATE TABLE IF NOT EXISTS owners (
		id TEXT PRIMARY KEY,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		last_seen_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	)`,
	`CREATE TABLE IF NOT EXISTS api_tokens (
		id TEXT PRIMARY KEY,
		owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
		kind TEXT NOT NULL CHECK (kind IN ('primary', 'linked')),
		name TEXT,
		token_hash TEXT NOT NULL UNIQUE,
		token_prefix TEXT NOT NULL,
		token_last4 TEXT NOT NULL,
		token_enc TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		rotated_at INTEGER,
		expires_at INTEGER,
		last_used_at INTEGER,
		revoked_at INTEGER,
		revoked_reason TEXT
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_primary ON api_tokens(owner_id) WHERE kind = 'primary' AND revoked_at IS NULL`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_prefix ON api_tokens(token_prefix)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_owner ON api_tokens(owner_id)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_active ON api_tokens(revoked_at) WHERE revoked_at IS NULL`,
	`CREATE TABLE IF NOT EXISTS app_secrets (
		name TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS health_write_probe (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		checked_at INTEGER NOT NULL
	)`,
	`INSERT OR IGNORE INTO health_write_probe (id, checked_at) VALUES (1, 0)`,
	`CREATE TABLE IF NOT EXISTS captcha_challenges (
		id VARCHAR(32) PRIMARY KEY,
		answer_hash VARCHAR(64) NOT NULL,
		expires_at TIMESTAMP NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_captcha_challenges_expires ON captcha_challenges(expires_at)`,
	`CREATE TABLE IF NOT EXISTS security_reports (
		tracking_id VARCHAR(32) PRIMARY KEY,
		id_window BIGINT NOT NULL DEFAULT 0,
		severity VARCHAR(16) NOT NULL,
		component VARCHAR(255) NOT NULL,
		status VARCHAR(16) NOT NULL DEFAULT 'received',
		body_enc BLOB NOT NULL,
		body_alg VARCHAR(16) NOT NULL,
		contact_enc BLOB,
		credit_pref VARCHAR(8) NOT NULL DEFAULT 'no',
		credit_name VARCHAR(100),
		researcher_comment TEXT NOT NULL DEFAULT '',
		disclosure_date DATE,
		cve_requested BOOLEAN NOT NULL DEFAULT FALSE,
		token_hash VARCHAR(64) NOT NULL,
		token_last_used DATE,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		closed_at TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS idx_security_reports_status ON security_reports(status, created_at)`,
	`CREATE TABLE IF NOT EXISTS security_thanks (
		tracking_id VARCHAR(32) PRIMARY KEY,
		credit VARCHAR(120) NOT NULL,
		severity VARCHAR(16) NOT NULL,
		disclosed_on DATE NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS project_pgp_key (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		fingerprint VARCHAR(40) NOT NULL,
		created_at TIMESTAMP NOT NULL,
		expires_at TIMESTAMP NOT NULL,
		last_rotated_at TIMESTAMP,
		keyservers_published TEXT NOT NULL DEFAULT '[]',
		revoked BOOLEAN NOT NULL DEFAULT FALSE,
		old_key_expires_at TIMESTAMP
	)`,
}
