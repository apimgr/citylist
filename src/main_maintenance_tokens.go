package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"golang.org/x/term"
)

type maintenanceTokenRow struct {
	ID        string
	OwnerID   string
	Kind      string
	Name      string
	Prefix    string
	CreatedAt int64
	LastUsed  sql.NullInt64
	ExpiresAt sql.NullInt64
	RevokedAt sql.NullInt64
}

func maintenanceTokenCommand(args []string, dataDir string, cfg *config.Config) error {
	if err := authorizeTokenMaintenance(cfg); err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: citylist --maintenance token {list|revoke <prefix>}")
	}
	store, err := openConfiguredDatabase(cfg, dataDir)
	if err != nil {
		return fmt.Errorf("open application database: %w", err)
	}
	defer func() { _ = store.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("initialize application schema: %w", err)
	}
	return maintenanceTokenWithStore(ctx, args, store, os.Stdout)
}

func authorizeTokenMaintenance(cfg *config.Config) error {
	if os.Geteuid() == 0 {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("token maintenance authorization requires root or a terminal to enter the operator token")
	}
	provided, err := readHiddenPGPInput("Operator token: ")
	if err != nil {
		return err
	}
	if cfg == nil || cfg.Server.Token == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(cfg.Server.Token)) != 1 {
		return errors.New("operator token was not accepted")
	}
	return nil
}

func maintenanceTokenWithStore(ctx context.Context, args []string, store *database.Store, output io.Writer) error {
	if store == nil {
		return errors.New("application database is unavailable")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("usage: citylist --maintenance token list")
		}
		return listMaintenanceTokens(ctx, store, output)
	case "revoke":
		if len(args) < 2 || len(args) > 3 || strings.TrimSpace(args[1]) == "" || (len(args) == 3 && args[2] != "--yes") {
			return errors.New("usage: citylist --maintenance token revoke <prefix> [--yes]")
		}
		return revokeMaintenanceToken(ctx, store, strings.TrimSpace(args[1]), len(args) == 3, output)
	default:
		return errors.New("unknown token maintenance action; use list or revoke")
	}
}

func listMaintenanceTokens(ctx context.Context, store *database.Store, output io.Writer) error {
	rows := make([]maintenanceTokenRow, 0)
	if err := store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		result, err := reader.QueryContext(ctx, `
			SELECT id, owner_id, kind, COALESCE(name, ''), token_prefix,
			       created_at, last_used_at, expires_at, revoked_at
			FROM api_tokens
			ORDER BY owner_id, kind, created_at, id`)
		if err != nil {
			return err
		}
		defer func() { _ = result.Close() }()
		for result.Next() {
			var row maintenanceTokenRow
			if err := result.Scan(&row.ID, &row.OwnerID, &row.Kind, &row.Name, &row.Prefix, &row.CreatedAt, &row.LastUsed, &row.ExpiresAt, &row.RevokedAt); err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return result.Err()
	}); err != nil {
		return fmt.Errorf("list owner tokens: %w", err)
	}
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "prefix\tkind\towner\tname\tcreated\tlast used\tstatus"); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, row := range rows {
		status := "active"
		if row.RevokedAt.Valid {
			status = "revoked"
		} else if row.ExpiresAt.Valid && row.ExpiresAt.Int64 <= now {
			status = "expired"
		}
		lastUsed := "-"
		if row.LastUsed.Valid {
			lastUsed = time.Unix(row.LastUsed.Int64, 0).UTC().Format(time.RFC3339)
		}
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.Prefix, row.Kind, row.OwnerID, row.Name, time.Unix(row.CreatedAt, 0).UTC().Format(time.RFC3339), lastUsed, status); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func revokeMaintenanceToken(ctx context.Context, store *database.Store, prefix string, yes bool, output io.Writer) error {
	var revokedCount int64
	var ownerID, kind string
	if err := store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT owner_id, kind FROM api_tokens WHERE token_prefix = ?`, prefix).Scan(&ownerID, &kind)
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("token prefix was not found")
		}
		return err
	}
	if !yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("token revocation requires a terminal confirmation or --yes")
		}
		if _, err := fmt.Fprintf(output, "Type token prefix %s to confirm revocation: ", prefix); err != nil {
			return err
		}
		line, err := maintenanceInput.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		if strings.TrimSpace(line) != prefix {
			return errors.New("token confirmation did not match")
		}
	}
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		var tokenID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM api_tokens WHERE token_prefix = ?`, prefix).Scan(&tokenID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("token prefix was not found")
			}
			return err
		}
		now := time.Now().Unix()
		var result sql.Result
		var err error
		if kind == "primary" {
			result, err = tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ?, revoked_reason = 'operator_revoked' WHERE owner_id = ? AND revoked_at IS NULL`, now, ownerID)
		} else {
			result, err = tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ?, revoked_reason = 'operator_revoked' WHERE id = ? AND revoked_at IS NULL`, now, tokenID)
		}
		if err != nil {
			return err
		}
		revokedCount, err = result.RowsAffected()
		if err != nil {
			return err
		}
		details, err := json.Marshal(map[string]any{"owner_id": ownerID, "token_prefix": prefix, "kind": kind, "source": "cli", "revoked_count": revokedCount})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_log (level, category, action, actor_ip, target_type, target_id, details, success) VALUES ('warn', 'token', 'token.revoked', '127.0.0.1', 'owner', ?, ?, 1)`, ownerID, string(details))
		return err
	}); err != nil {
		return fmt.Errorf("revoke owner token: %w", err)
	}
	if kind == "primary" {
		_, err := fmt.Fprintf(output, "Revoked primary token and %d linked token(s) for owner %s\n", revokedCount-1, ownerID)
		return err
	}
	_, err := fmt.Fprintf(output, "Revoked linked token %s for owner %s\n", prefix, ownerID)
	return err
}
