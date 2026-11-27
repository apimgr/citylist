package database

import (
	"strings"
	"unicode"
)

var databaseObjects = map[string]struct{}{
	"rate_limits": {}, "idx_rate_limits_window": {},
	"audit_log": {}, "idx_audit_timestamp": {}, "idx_audit_category": {}, "idx_audit_actor_ip": {}, "idx_audit_target": {},
	"scheduler_tasks": {}, "scheduler_history": {}, "idx_scheduler_history_task": {}, "idx_scheduler_history_started": {},
	"backups": {}, "idx_backups_created": {},
	"owners": {}, "api_tokens": {}, "api_tokens_legacy": {}, "idx_api_tokens_primary": {}, "idx_api_tokens_hash": {}, "idx_api_tokens_prefix": {}, "idx_api_tokens_owner": {}, "idx_api_tokens_active": {},
	"app_secrets": {}, "health_write_probe": {}, "captcha_challenges": {}, "idx_captcha_challenges_expires": {},
	"security_reports": {}, "idx_security_reports_status": {}, "security_thanks": {}, "project_pgp_key": {},
}

// DatabaseObjectName maps a schema table or index to its driver-specific name.
func DatabaseObjectName(name, driver string) string {
	if strings.EqualFold(driver, "libsql") || strings.EqualFold(driver, "turso") {
		return "srv_" + strings.TrimPrefix(name, "srv_")
	}
	return name
}

// prefixDatabaseSQL maps known schema object identifiers without touching SQL
// string literals or comments. It is used only by the remote driver adapter.
func prefixDatabaseSQL(query string) string {
	var out strings.Builder
	out.Grow(len(query) + 32)
	for i := 0; i < len(query); {
		if query[i] == '\'' || query[i] == '"' || query[i] == '`' {
			quote := query[i]
			start := i
			i++
			for i < len(query) {
				if query[i] == quote {
					i++
					if i < len(query) && query[i] == quote {
						i++
						continue
					}
					break
				}
				i++
			}
			identifier := query[start+1 : i-1]
			if _, ok := databaseObjects[strings.ToLower(identifier)]; ok && quote != '\'' {
				out.WriteByte(quote)
				out.WriteString(DatabaseObjectName(identifier, "libsql"))
				out.WriteByte(quote)
			} else {
				out.WriteString(query[start:i])
			}
			continue
		}
		if query[i] == '-' && i+1 < len(query) && query[i+1] == '-' {
			start := i
			for i < len(query) && query[i] != '\n' {
				i++
			}
			out.WriteString(query[start:i])
			continue
		}
		if query[i] == '/' && i+1 < len(query) && query[i+1] == '*' {
			start := i
			i += 2
			for i+1 < len(query) && query[i:i+2] != "*/" {
				i++
			}
			if i+1 < len(query) {
				i += 2
			}
			out.WriteString(query[start:i])
			continue
		}
		if isIdentifierStart(rune(query[i])) {
			start := i
			i++
			for i < len(query) && isIdentifierPart(rune(query[i])) {
				i++
			}
			word := query[start:i]
			if _, ok := databaseObjects[strings.ToLower(word)]; ok {
				out.WriteString(DatabaseObjectName(word, "libsql"))
			} else {
				out.WriteString(word)
			}
			continue
		}
		out.WriteByte(query[i])
		i++
	}
	return out.String()
}

func isIdentifierStart(char rune) bool { return char == '_' || unicode.IsLetter(char) }
func isIdentifierPart(char rune) bool {
	return char == '_' || unicode.IsLetter(char) || unicode.IsDigit(char)
}
