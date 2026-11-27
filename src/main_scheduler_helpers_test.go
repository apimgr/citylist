package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"github.com/apimgr/citylist/src/scheduler"
	"github.com/apimgr/citylist/src/updater"
)

func TestSchedulerHelperValidationAndState(t *testing.T) {
	domains := []string{"example.org"}
	if got := appendUniqueSchedulerDomain(domains, "EXAMPLE.ORG"); !reflect.DeepEqual(got, domains) {
		t.Fatalf("duplicate domain append = %v", got)
	}
	if got := appendUniqueSchedulerDomain(domains, "other.example"); !reflect.DeepEqual(got, []string{"example.org", "other.example"}) {
		t.Fatalf("new domain append = %v", got)
	}
	for _, test := range []struct {
		value string
		valid bool
	}{{"firehol_1", true}, {"", false}, {"Upper", false}, {"bad-name", false}, {"../evil", false}} {
		if got := validBlocklistFileID(test.value); got != test.valid {
			t.Errorf("validBlocklistFileID(%q) = %t, want %t", test.value, got, test.valid)
		}
	}
	if got := releaseIdentity(nil); got != "" {
		t.Fatalf("nil release identity = %q", got)
	}
	if got := releaseIdentity(&updater.Release{TagName: "v1.2.3"}); got != "v1.2.3" {
		t.Fatalf("stable release identity = %q", got)
	}
	if got := releaseIdentity(&updater.Release{TagName: "devel"}); got != "devel:0001-01-01T00:00:00Z" {
		t.Fatalf("devel release identity = %q", got)
	}

	statePath := filepath.Join(mainProjectTempDir(t), "nested", "state")
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateState(statePath, []byte("release:v1\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(statePath)
	contents, readErr := os.ReadFile(statePath)
	if err != nil || readErr != nil || info.Mode().Perm() != 0600 || string(contents) != "release:v1\n" {
		t.Fatalf("private state = %q, %v, %v; want exact contents and mode 0600", contents, err, readErr)
	}
}

func TestManagedCertificateExpiryNotificationDetails(t *testing.T) {
	root := mainProjectTempDir(t)
	cfg := config.DefaultConfig()
	cfg.Server.FQDN = "example.test"
	now := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(3 * 24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePath := filepath.Join(root, "ssl", "letsencrypt", cfg.Server.FQDN, "fullchain.pem")
	if err := os.MkdirAll(filepath.Dir(certificatePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	certificate, err := managedCertificate(root, cfg.Server.FQDN)
	if err != nil {
		t.Fatalf("managedCertificate() error: %v", err)
	}
	if got := daysUntilCertificateExpiry(certificate, now); got != 3 {
		t.Fatalf("daysUntilCertificateExpiry() = %d, want 3", got)
	}
	details := sslCertificateNotificationDetails(root, cfg, now)
	if details["fqdn"] != cfg.Server.FQDN || details["expires_in"] != "3" || details["expiry_date"] != template.NotAfter.Format(time.RFC3339) {
		t.Fatalf("SSL expiry notification details = %#v", details)
	}
	if _, err := managedCertificate(root, "../outside"); err == nil {
		t.Fatal("managedCertificate() accepted a path traversal domain")
	}
}

func TestScheduledSSLRenewalEmitsUrgentExpiryEventBeforeRenewal(t *testing.T) {
	root := mainProjectTempDir(t)
	cfg := config.DefaultConfig()
	cfg.Server.FQDN = "example.test"
	cfg.Server.SSL.Enabled = true
	cfg.Server.SSL.LetsEncrypt.Enabled = true
	cfg.Server.SSL.LetsEncrypt.Challenge = "invalid"
	now := time.Now().UTC()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(7 * 24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePath := filepath.Join(root, "ssl", "letsencrypt", cfg.Server.FQDN, "fullchain.pem")
	if err := os.MkdirAll(filepath.Dir(certificatePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	events := []string{}
	notify := operatorEventNotifier(func(_ context.Context, event string, details map[string]string) error {
		events = append(events, event)
		if details["fqdn"] != cfg.Server.FQDN || details["expires_in"] != "7" {
			t.Errorf("SSL expiry notification details = %#v", details)
		}
		return nil
	})
	if err := runScheduledSSLRenewal(root, cfg, notify); err == nil {
		t.Fatal("invalid challenge should prevent renewal")
	}
	if !reflect.DeepEqual(events, []string{"ssl_expiring"}) {
		t.Fatalf("SSL notification events = %v, want only ssl_expiring", events)
	}
}

func TestTrustedBlocklistRangesAndDisabledDiskCheck(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Address = "192.0.2.9"
	cfg.Server.TrustedProxies.Additional = []string{"198.51.100.0/24", "203.0.113.5"}
	got := trustedBlocklistRanges(context.Background(), cfg)
	for _, want := range []string{"198.51.100.0/24", "203.0.113.5", "192.0.2.0/24"} {
		found := false
		for _, value := range got {
			if value == want {
				found = true
			}
		}
		if !found {
			t.Errorf("trusted ranges %v do not include %q", got, want)
		}
	}
	if err := runScheduledDiskCheck(mainProjectTempDir(t), cfg, nil); err != nil {
		t.Fatalf("disabled disk check should be a no-op: %v", err)
	}
}

func TestInitializeSecurityDataSchedulesMissingEnabledSources(t *testing.T) {
	root := mainProjectTempDir(t)
	cfg := config.DefaultConfig()
	cfg.Server.GeoIP.Enabled = true
	cfg.Server.Security.Blocklists.Enabled = true
	cfg.Server.Security.Blocklists.ExtraSources = append(cfg.Server.Security.Blocklists.ExtraSources, config.SecurityBlocklistSource{ID: "../ignored"})
	ran := make(chan string, 3)
	store, err := database.OpenSQLite(filepath.Join(root, "scheduler.db"), database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	instance, err := scheduler.NewPersistent(store, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"geoip_update", "blocklist_update", "cve_update"} {
		name := name
		if err := instance.AddScheduledTask(name, "@daily", true, time.UTC, func() error {
			ran <- name
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	initializeSecurityData(instance, cfg, root, false)
	if len(ran) != 0 {
		t.Fatal("security updates ran before the database was ready")
	}
	instance.Start()
	defer instance.Stop()
	initializeSecurityData(instance, cfg, root, true)
	completed := make(map[string]bool, 3)
	for range 3 {
		select {
		case name := <-ran:
			completed[name] = true
		case <-time.After(3 * time.Second):
			t.Fatal("initial security updates did not all run")
		}
	}
	for _, want := range []string{"geoip_update", "blocklist_update", "cve_update"} {
		if !completed[want] {
			t.Errorf("initial update %q was not scheduled", want)
		}
	}
	if completed["../ignored"] {
		t.Fatal("unsafe blocklist source was scheduled")
	}
}
