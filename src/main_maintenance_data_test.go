package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
)

func TestExportOwnerDataOmitsCredentialSecrets(t *testing.T) {
	store := openMaintenanceTokenTestStore(t)
	seedMaintenanceToken(t, store, "owner-export", "primary", "exp_primary", "")
	seedMaintenanceToken(t, store, "owner-export", "linked", "exp_linked", "backup")
	var out bytes.Buffer
	if err := exportOwnerData(context.Background(), store, "exp_primary", "-", &out); err != nil {
		t.Fatal(err)
	}
	var result ownerExport
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if result.Format != 1 || result.Owner.ID != "owner-export" || len(result.Tokens) != 2 {
		t.Fatalf("unexpected export metadata: %#v", result)
	}
	if strings.Contains(out.String(), "hash-exp_") || strings.Contains(out.String(), "encrypted-secret") || strings.Contains(out.String(), "token_enc") {
		t.Fatalf("export contains credentials: %s", out.String())
	}
	var audits int
	if err := store.Read(context.Background(), func(ctx context.Context, r database.Reader) error {
		return r.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action IN ('compliance.data_export_requested','compliance.data_export_completed')`).Scan(&audits)
	}); err != nil {
		t.Fatal(err)
	}
	if audits != 2 {
		t.Fatalf("export audit entries=%d, want 2", audits)
	}
}

func TestExportOwnerDataCreatesPrivateFile(t *testing.T) {
	store := openMaintenanceTokenTestStore(t)
	seedMaintenanceToken(t, store, "owner-file", "primary", "file_primary", "")
	path := filepath.Join(t.TempDir(), "owner-export.json")
	if err := exportOwnerData(context.Background(), store, "file_primary", path, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("export permissions=%04o, want 0600", info.Mode().Perm())
	}
}

func TestFindOwnerTreatsPrefixWildcardsLiterallyAndDetectsAmbiguity(t *testing.T) {
	store := openMaintenanceTokenTestStore(t)
	seedMaintenanceToken(t, store, "owner-literal", "primary", "abc_%_literal", "")
	seedMaintenanceToken(t, store, "owner-one", "primary", "amb_one", "")
	seedMaintenanceToken(t, store, "owner-two", "primary", "amb_two", "")
	got, err := findOwner(context.Background(), store, "abc_%")
	if err != nil || got != "owner-literal" {
		t.Fatalf("literal wildcard prefix owner=%q err=%v", got, err)
	}
	if _, err = findOwner(context.Background(), store, "amb_"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous prefix error=%v", err)
	}
}

func TestDeleteOwnerDataCascadesTokensAndAnonymizesAudit(t *testing.T) {
	store := openMaintenanceTokenTestStore(t)
	seedMaintenanceToken(t, store, "owner-delete", "primary", "del_primary", "")
	seedMaintenanceToken(t, store, "owner-delete", "linked", "del_linked", "automation")
	var out bytes.Buffer
	if err := deleteOwnerData(context.Background(), store, "del_primary", true, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "owner-delete") {
		t.Fatalf("output disclosed deleted owner id: %s", out.String())
	}
	var owners, tokens, completed, linkedAudit int
	if err := store.Read(context.Background(), func(ctx context.Context, r database.Reader) error {
		if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners WHERE id='owner-delete'`).Scan(&owners); err != nil {
			return err
		}
		if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id='owner-delete'`).Scan(&tokens); err != nil {
			return err
		}
		if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action='compliance.data_deletion_completed' AND target_id LIKE 'anon_%'`).Scan(&completed); err != nil {
			return err
		}
		return r.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action IN ('compliance.data_deletion_requested','compliance.data_deletion_completed') AND (target_id='owner-delete' OR details LIKE '%owner-delete%')`).Scan(&linkedAudit)
	}); err != nil {
		t.Fatal(err)
	}
	if owners != 0 || tokens != 0 || completed != 1 || linkedAudit != 0 {
		t.Fatalf("owner=%d tokens=%d anonymized audit=%d linked audit=%d; want 0, 0, 1, 0", owners, tokens, completed, linkedAudit)
	}
}

func TestConfirmOwnerDeletionRequiresExactTypedOwner(t *testing.T) {
	if err := confirmOwnerDeletion("owner-exact", false, false, strings.NewReader("owner-exact\n"), &bytes.Buffer{}); err == nil {
		t.Fatal("confirmation without a terminal should fail")
	}
	if err := confirmOwnerDeletion("owner-exact", false, true, strings.NewReader("other\n"), &bytes.Buffer{}); err == nil {
		t.Fatal("mismatched owner confirmation should fail")
	}
	if err := confirmOwnerDeletion("owner-exact", false, true, strings.NewReader("owner-exact\n"), &bytes.Buffer{}); err != nil {
		t.Fatalf("exact owner confirmation: %v", err)
	}
	if err := confirmOwnerDeletion("owner-exact", true, false, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatalf("--yes confirmation: %v", err)
	}
}

func TestRenderComplianceSummaryIncludesEffectiveRetentionAndCounts(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Compliance.GDPR = true
	cfg.Server.Compliance.HIPAA = true
	cfg.Server.Logs.Audit.Keep = "none"
	cfg.Server.Backup.Retention.MaxBackups = 5
	var out bytes.Buffer
	backup := sql.NullInt64{Int64: time.Now().Unix(), Valid: true}
	if err := renderComplianceSummary(&out, cfg, 2, 3, 4, 5, backup); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"gdpr: enabled", "hipaa: enabled", "audit logs 72m", "security reports 365 days", "backups keep 5", "Owners: 2", "Resources: 0", "Active tokens: 3", "Open security reports: 4", "Compliance events (30 days): 5", "Last backup:"} {
		if !strings.Contains(got, want) {
			t.Errorf("compliance summary missing %q: %s", want, got)
		}
	}
}

func TestMaintenanceDataExportAndComplianceReportCommands(t *testing.T) {
	dataDir := filepath.Join(mainProjectTempDir(t), "data")
	t.Setenv("DATABASE_DIR", filepath.Join(dataDir, "db"))
	store, err := database.OpenSQLite(filepath.Join(dataDir, "db", "server.db"), database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close maintenance data store: %v", err)
		}
	})
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	seedMaintenanceToken(t, store, "owner-command", "primary", "cmd_primary", "")
	cfg := config.DefaultConfig()
	exportPath := filepath.Join(t.TempDir(), "owner-export.json")
	if err := maintenanceDataCommand([]string{"export", "--owner", "cmd_primary", "--output", exportPath}, dataDir, cfg); err != nil {
		t.Fatalf("maintenanceDataCommand(export): %v", err)
	}
	exported, err := os.ReadFile(exportPath)
	if err != nil || !strings.Contains(string(exported), "owner-command") {
		t.Fatalf("export command output = %q, %v", exported, err)
	}
	var report bytes.Buffer
	if err := maintenanceComplianceReport([]string{"report"}, dataDir, cfg, &report); err != nil {
		t.Fatalf("maintenanceComplianceReport(): %v", err)
	}
	for _, want := range []string{"Compliance report", "Owners: 1", "Active tokens: 1", "Last backup: never"} {
		if !strings.Contains(report.String(), want) {
			t.Errorf("compliance report missing %q: %s", want, report.String())
		}
	}
}
