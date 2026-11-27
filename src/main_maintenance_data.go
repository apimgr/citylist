package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"golang.org/x/term"
)

type ownerExport struct {
	Format int `json:"format"`
	Owner  struct {
		ID         string    `json:"id"`
		CreatedAt  time.Time `json:"created_at"`
		LastSeenAt time.Time `json:"last_seen_at"`
	} `json:"owner"`
	Tokens    []ownerExportToken `json:"tokens"`
	Resources []any              `json:"resources"`
}
type ownerExportToken struct {
	Prefix     string     `json:"prefix"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Status     string     `json:"status"`
}

func maintenanceDataCommand(args []string, dataDir string, cfg *config.Config) error {
	if err := authorizeTokenMaintenance(cfg); err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: citylist --maintenance data {export|delete} --owner <prefix> [--output FILE|--yes]")
	}
	action := args[0]
	fs := flag.NewFlagSet("maintenance data", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	ownerPrefix := fs.String("owner", "", "owner token prefix")
	output := fs.String("output", "", "export destination")
	yes := fs.Bool("yes", false, "skip typed confirmation")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *ownerPrefix == "" {
		return errors.New("--owner token prefix is required")
	}
	store, err := openConfiguredDatabase(cfg, dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = store.EnsureSchema(ctx); err != nil {
		return err
	}
	switch action {
	case "export":
		return exportOwnerData(ctx, store, *ownerPrefix, *output, os.Stdout)
	case "delete":
		return deleteOwnerData(ctx, store, *ownerPrefix, *yes, os.Stdin, os.Stdout)
	default:
		return errors.New("unknown data action; use export or delete")
	}
}

func findOwner(ctx context.Context, store *database.Store, prefix string) (string, error) {
	var owner string
	count := 0
	err := store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		rows, err := reader.QueryContext(ctx, `SELECT DISTINCT owner_id FROM api_tokens WHERE substr(token_prefix,1,length(?)) = ? ORDER BY owner_id LIMIT 2`, prefix, prefix)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			if err := rows.Scan(&owner); err != nil {
				return err
			}
			count++
		}
		return rows.Err()
	})
	if err != nil {
		return "", err
	}
	if count == 0 {
		return "", errors.New("owner token prefix was not found")
	}
	if count > 1 {
		return "", errors.New("owner token prefix is ambiguous")
	}
	return owner, nil
}

func exportOwnerData(ctx context.Context, store *database.Store, prefix, path string, stdout io.Writer) error {
	ownerID, err := findOwner(ctx, store, prefix)
	if err != nil {
		return err
	}
	data := ownerExport{Format: 1, Tokens: []ownerExportToken{}, Resources: []any{}}
	requestID, err := randomMaintenanceID()
	if err != nil {
		return err
	}
	if err = writeAuditEvent("compliance.data_export_requested", "info", "success", "cli", maintenanceOperatorIP(), map[string]string{"type": "owner", "id": ownerID}, map[string]any{"request_id": requestID}); err != nil {
		return err
	}
	if err = store.Write(ctx, func(tx *sql.Tx) error {
		return insertMaintenanceAudit(ctx, tx, "compliance.data_export_requested", ownerID, map[string]any{"owner_id": ownerID, "request_id": requestID, "source": "cli"})
	}); err != nil {
		return err
	}
	err = store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		var created, last int64
		if err := reader.QueryRowContext(ctx, `SELECT created_at,last_seen_at FROM owners WHERE id=?`, ownerID).Scan(&created, &last); err != nil {
			return err
		}
		data.Owner.ID = ownerID
		data.Owner.CreatedAt = time.Unix(created, 0).UTC()
		data.Owner.LastSeenAt = time.Unix(last, 0).UTC()
		rows, err := reader.QueryContext(ctx, `SELECT token_prefix,kind,COALESCE(name,''),created_at,last_used_at,expires_at,revoked_at FROM api_tokens WHERE owner_id=? ORDER BY created_at,id`, ownerID)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		now := time.Now().Unix()
		for rows.Next() {
			var t ownerExportToken
			var created int64
			var used, expires, revoked sql.NullInt64
			if err := rows.Scan(&t.Prefix, &t.Kind, &t.Name, &created, &used, &expires, &revoked); err != nil {
				return err
			}
			t.CreatedAt = time.Unix(created, 0).UTC()
			if used.Valid {
				v := time.Unix(used.Int64, 0).UTC()
				t.LastUsedAt = &v
			}
			if expires.Valid {
				v := time.Unix(expires.Int64, 0).UTC()
				t.ExpiresAt = &v
			}
			t.Status = "active"
			if revoked.Valid {
				t.Status = "revoked"
			} else if expires.Valid && expires.Int64 <= now {
				t.Status = "expired"
			}
			data.Tokens = append(data.Tokens, t)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if path == "-" {
		if _, err = stdout.Write(b); err != nil {
			return err
		}
	} else {
		if path == "" {
			path = fmt.Sprintf("citylist-export-%s-%s.json", ownerID, time.Now().UTC().Format("20060102"))
		}
		f, openErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if openErr != nil {
			return openErr
		}
		if _, err = f.Write(b); err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		if _, err = fmt.Fprintf(stdout, "Exported owner data to %s (%d bytes)\n", path, len(b)); err != nil {
			return err
		}
	}
	if err = store.Write(ctx, func(tx *sql.Tx) error {
		return insertMaintenanceAudit(ctx, tx, "compliance.data_export_completed", ownerID, map[string]any{"owner_id": ownerID, "request_id": requestID, "file_size": len(b), "source": "cli"})
	}); err != nil {
		return err
	}
	return writeAuditEvent("compliance.data_export_completed", "info", "success", "cli", maintenanceOperatorIP(), map[string]string{"type": "owner", "id": ownerID}, map[string]any{"request_id": requestID, "file_size": len(b)})
}

func deleteOwnerData(ctx context.Context, store *database.Store, prefix string, yes bool, input io.Reader, output io.Writer) error {
	ownerID, err := findOwner(ctx, store, prefix)
	if err != nil {
		return err
	}
	if err = confirmOwnerDeletion(ownerID, yes, term.IsTerminal(int(os.Stdin.Fd())), input, output); err != nil {
		return err
	}
	var randomID [16]byte
	if _, err = rand.Read(randomID[:]); err != nil {
		return err
	}
	anon := "anon_" + hex.EncodeToString(randomID[:])
	var deleted int64
	err = store.Write(ctx, func(tx *sql.Tx) error {
		var result sql.Result
		var e error
		var tokenCount int64
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id=?`, ownerID).Scan(&tokenCount); e != nil {
			return e
		}
		if e = insertMaintenanceAudit(ctx, tx, "compliance.data_deletion_requested", anon, map[string]any{"owner_id": anon, "source": "cli"}); e != nil {
			return e
		}
		result, e = tx.ExecContext(ctx, `DELETE FROM owners WHERE id=?`, ownerID)
		if e != nil {
			return e
		}
		deleted, e = result.RowsAffected()
		if e != nil {
			return e
		}
		if deleted == 0 {
			return errors.New("owner no longer exists")
		}
		deleted += tokenCount
		return insertMaintenanceAudit(ctx, tx, "compliance.data_deletion_completed", anon, map[string]any{"owner_id": anon, "deleted_count": deleted, "anonymized_count": 0, "source": "cli"})
	})
	if err != nil {
		return err
	}
	if err = writeAuditEvent("compliance.data_deletion_requested", "info", "success", "cli", maintenanceOperatorIP(), map[string]string{"type": "owner", "id": anon}, nil); err != nil {
		return err
	}
	if err = writeAuditEvent("compliance.data_deletion_completed", "info", "success", "cli", maintenanceOperatorIP(), map[string]string{"type": "owner", "id": anon}, map[string]any{"deleted_count": deleted, "anonymized_count": 0}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Deleted %d associated records; anonymized 0 records\n", deleted)
	return err
}

func confirmOwnerDeletion(ownerID string, yes, terminal bool, input io.Reader, output io.Writer) error {
	if yes {
		return nil
	}
	if !terminal {
		return errors.New("data deletion requires a terminal confirmation or --yes")
	}
	if _, err := fmt.Fprintf(output, "Type owner id %s to confirm deletion: ", ownerID); err != nil {
		return err
	}
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	if strings.TrimSpace(line) != ownerID {
		return errors.New("owner confirmation did not match")
	}
	return nil
}

func randomMaintenanceID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

func insertMaintenanceAudit(ctx context.Context, tx *sql.Tx, event, target string, details any) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(level,category,action,actor_ip,target_type,target_id,details,success) VALUES('info','compliance',?,?, 'owner',?,?,1)`, event, maintenanceOperatorIP(), target, string(encoded))
	return err
}

func maintenanceComplianceReport(args []string, dataDir string, cfg *config.Config, output io.Writer) error {
	if err := authorizeTokenMaintenance(cfg); err != nil {
		return err
	}
	if len(args) != 1 || args[0] != "report" {
		return errors.New("usage: citylist --maintenance compliance report")
	}
	store, err := openConfiguredDatabase(cfg, dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = store.EnsureSchema(ctx); err != nil {
		return err
	}
	var owners, tokens, reports, events int64
	var lastBackup sql.NullInt64
	err = store.Read(ctx, func(ctx context.Context, r database.Reader) error {
		for _, q := range []struct {
			query string
			dest  *int64
		}{{`SELECT COUNT(*) FROM owners`, &owners}, {`SELECT COUNT(*) FROM api_tokens WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?)`, &tokens}, {`SELECT COUNT(*) FROM security_reports WHERE status NOT IN ('closed','resolved')`, &reports}, {`SELECT COUNT(*) FROM audit_log WHERE timestamp>=?`, &events}} {
			var e error
			if q.query == `SELECT COUNT(*) FROM api_tokens WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?)` {
				e = r.QueryRowContext(ctx, q.query, time.Now().Unix()).Scan(q.dest)
			} else if strings.Contains(q.query, "timestamp>=?") {
				e = r.QueryRowContext(ctx, q.query, time.Now().Add(-30*24*time.Hour).Unix()).Scan(q.dest)
			} else {
				e = r.QueryRowContext(ctx, q.query).Scan(q.dest)
			}
			if e != nil {
				return e
			}
		}
		return r.QueryRowContext(ctx, `SELECT MAX(created_at) FROM backups`).Scan(&lastBackup)
	})
	if err != nil {
		return err
	}
	if err = renderComplianceSummary(output, cfg, owners, tokens, reports, events, lastBackup); err != nil {
		return err
	}
	if err = store.Write(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO audit_log (level,category,action,actor_ip,target_type,target_id,details,success) VALUES ('info','compliance','compliance.report_generated',?,'report','compliance',? ,1)`, maintenanceOperatorIP(), `{"source":"cli"}`)
		return e
	}); err != nil {
		return err
	}
	return writeAuditEvent("compliance.report_generated", "info", "success", "cli", maintenanceOperatorIP(), map[string]string{"type": "report", "id": "compliance"}, map[string]any{"owners": owners, "resources": 0, "active_tokens": tokens, "open_security_reports": reports, "events_30d": events})
}

func renderComplianceSummary(output io.Writer, cfg *config.Config, owners, tokens, reports, events int64, lastBackup sql.NullInt64) error {
	standards := []struct {
		name    string
		enabled bool
	}{{"gdpr", cfg.Server.Compliance.GDPR}, {"ccpa", cfg.Server.Compliance.CCPA}, {"hipaa", cfg.Server.Compliance.HIPAA}, {"soc2", cfg.Server.Compliance.SOC2}, {"pci_dss", cfg.Server.Compliance.PCIDSS}, {"iso27001", cfg.Server.Compliance.ISO27001}, {"fedramp", cfg.Server.Compliance.FedRAMP}, {"lgpd", cfg.Server.Compliance.LGPD}, {"pipeda", cfg.Server.Compliance.PIPEDA}, {"appi", cfg.Server.Compliance.APPI}, {"pdpa", cfg.Server.Compliance.PDPA}}
	if _, err := fmt.Fprintln(output, "Compliance report\nStandards:"); err != nil {
		return err
	}
	for _, s := range standards {
		state := "disabled"
		if s.enabled {
			state = "enabled"
		}
		if _, err := fmt.Fprintf(output, "  %s: %s\n", s.name, state); err != nil {
			return err
		}
	}
	auditKeep := effectiveAuditRetention(cfg.Server.Compliance, cfg.Server.Logs.Audit.Keep)
	if auditKeep == "" {
		auditKeep = cfg.Server.Logs.Defaults.Keep
	}
	if auditKeep == "" {
		auditKeep = "none"
	}
	if _, err := fmt.Fprintf(output, "Retention: audit logs %s; security reports %d days; logs %d days; backups keep %d; disk cleanup at %d%%\n", auditKeep, cfg.Server.Security.Reports.RetentionDays, cfg.Server.Maintenance.Cleanup.LogRetentionDays, cfg.Server.Backup.Retention.MaxBackups, cfg.Server.Maintenance.Cleanup.DiskThreshold); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "Owners: %d\nResources: 0\nActive tokens: %d\nOpen security reports: %d\nCompliance events (30 days): %d\n", owners, tokens, reports, events); err != nil {
		return err
	}
	if lastBackup.Valid {
		_, err := fmt.Fprintf(output, "Last backup: %s\n", time.Unix(lastBackup.Int64, 0).UTC().Format(time.RFC3339))
		return err
	}
	_, err := fmt.Fprintln(output, "Last backup: never")
	return err
}
