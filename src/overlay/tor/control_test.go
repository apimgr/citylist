package tor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGenerateAndReadIdentityRoundTrip(t *testing.T) {
	privateKey, publicKey, address, err := generateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if len(privateKey) != 64 || len(publicKey) != 32 || !validHostname(address) {
		t.Fatalf("generated Tor identity has invalid dimensions or hostname: %d/%d %q", len(privateKey), len(publicKey), address)
	}
	if got := onionAddress(publicKey); got != address {
		t.Fatalf("onionAddress() = %q, want %q", got, address)
	}

	directory := filepath.Join(t.TempDir(), "candidate")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeIdentityFiles(directory, privateKey, publicKey, address); err != nil {
		t.Fatal(err)
	}
	gotPrivate, gotPublic, gotAddress, err := readIdentity(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPrivate, privateKey) || !bytes.Equal(gotPublic, publicKey) || gotAddress != address {
		t.Fatal("readIdentity() did not preserve the generated keypair")
	}
	for _, name := range []string{"hs_ed25519_secret_key", "hs_ed25519_public_key", "hostname"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %04o, want 0600", name, info.Mode().Perm())
		}
	}
}

func TestValidateConfigReadsPrivateTorrcWithoutTorBinary(t *testing.T) {
	root := t.TempDir()
	options := DefaultOptions(filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "logs"))
	if runtime.GOOS != "windows" {
		fakeTor := filepath.Join(root, "tor")
		if err := os.WriteFile(fakeTor, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
		options.TorBinary = fakeTor
	}
	configDir := filepath.Join(options.ConfigDir, "tor")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	torrcPath := filepath.Join(configDir, "torrc")
	if err := os.WriteFile(torrcPath, []byte("DataDirectory /private/tor\nHiddenServiceDir /private/site\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(options); err != nil {
		t.Fatalf("ValidateConfig(valid persisted torrc) error: %v", err)
	}
	if err := os.WriteFile(torrcPath, []byte("DataDirectory /private/tor\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(options); err == nil {
		t.Fatal("ValidateConfig(incomplete torrc) error = nil")
	}
}

func TestReadOnionAddressRejectsSymlinkAndInvalidHostname(t *testing.T) {
	dataDir := t.TempDir()
	siteDir := filepath.Join(dataDir, "tor", "site")
	if err := os.MkdirAll(siteDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(siteDir, 0700); err != nil {
		t.Fatal(err)
	}
	address := "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"
	hostnamePath := filepath.Join(siteDir, "hostname")
	if err := os.WriteFile(hostnamePath, []byte(address+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadOnionAddress(dataDir); err != nil || got != address {
		t.Fatalf("ReadOnionAddress() = (%q, %v), want valid address", got, err)
	}
	if err := os.WriteFile(hostnamePath, []byte("not-an-onion\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOnionAddress(dataDir); err == nil {
		t.Fatal("ReadOnionAddress(invalid hostname) error = nil")
	}
	if runtime.GOOS != "windows" {
		if err := os.Remove(hostnamePath); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dataDir, "sensitive")
		if err := os.WriteFile(target, []byte("secret data\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, hostnamePath); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadOnionAddress(dataDir); err == nil {
			t.Fatal("ReadOnionAddress(symlink) error = nil")
		}
	}
}

func TestVanityCandidateStorageAndResolution(t *testing.T) {
	privateKey, publicKey, address, err := generateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "tor", "vanity")
	if err := writeCandidate(root, privateKey, publicKey, address); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{options: Options{DataDir: filepath.Dir(filepath.Dir(root))}}
	if got := manager.candidateAddresses(); len(got) != 1 || got[0] != address {
		t.Fatalf("candidate addresses = %v, want [%s]", got, address)
	}
	resolved, err := manager.resolveCandidate("")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(root, address) {
		t.Fatalf("resolved candidate = %q", resolved)
	}
	if _, _, gotAddress, err := readIdentity(resolved); err != nil || gotAddress != address {
		t.Fatalf("candidate key files are not reusable: address=%q error=%v", gotAddress, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("candidate directory mode = %04o, want 0700", info.Mode().Perm())
	}
}

func TestVanitySearchValidatesBeforeStartingWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &Manager{ctx: ctx, options: Options{DataDir: t.TempDir()}}
	manager.snapshot.Enabled = true
	if err := manager.StartVanitySearch("bad!", 1); err == nil {
		t.Fatal("invalid prefix was accepted")
	}
	if err := manager.StartVanitySearch("abc", runtime.NumCPU()+1); err == nil {
		t.Fatal("an enabled search accepted more workers than logical CPUs")
	}
	if manager.VanityStatus().State != "idle" {
		t.Fatal("invalid vanity configuration started a worker")
	}
	manager.vanity = &vanitySearch{prefix: "ab", workers: 1}
	if err := manager.StartVanitySearch("cd", 1); err == nil {
		t.Fatal("a second vanity search was accepted")
	}
}

func TestVanitySearchStartsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager := &Manager{ctx: ctx, options: Options{DataDir: t.TempDir()}}
	manager.snapshot.Enabled = true
	if err := manager.StartVanitySearch("abc", 1); err != nil {
		t.Fatalf("StartVanitySearch(valid prefix) error: %v", err)
	}
	if status := manager.VanityStatus(); status.State != "running" || status.Prefix != "abc" || status.Workers != 1 {
		t.Fatalf("running vanity status = %+v", status)
	}
	if err := manager.StartVanitySearch("def", 1); err == nil {
		t.Fatal("a second vanity search was accepted")
	}
	if candidates := manager.StopVanitySearch(); len(candidates) != 0 {
		t.Fatalf("stopped search candidates = %v, want none", candidates)
	}
	if status := manager.VanityStatus(); status.State != "idle" {
		t.Fatalf("stopped vanity status = %+v, want idle", status)
	}
}

func TestNilManagerControlMethods(t *testing.T) {
	var manager *Manager
	if err := manager.Validate(); err == nil {
		t.Fatal("nil Manager.Validate() error = nil")
	}
	if err := manager.Restart(context.Background()); err == nil {
		t.Fatal("nil Manager.Restart() error = nil")
	}
	if _, _, err := manager.Regenerate(context.Background()); err == nil {
		t.Fatal("nil Manager.Regenerate() error = nil")
	}
	if _, err := manager.ApplyVanityCandidate(context.Background(), ""); err == nil {
		t.Fatal("nil Manager.ApplyVanityCandidate() error = nil")
	}
	if _, err := manager.ImportIdentity(context.Background(), "/keys"); err == nil {
		t.Fatal("nil Manager.ImportIdentity() error = nil")
	}
	if err := manager.StartVanitySearch("abc", 1); err == nil {
		t.Fatal("nil Manager.StartVanitySearch() error = nil")
	}
	if candidates := manager.StopVanitySearch(); len(candidates) != 0 {
		t.Fatalf("nil Manager.StopVanitySearch() = %v", candidates)
	}
	if status := manager.VanityStatus(); status.State != "idle" {
		t.Fatalf("nil Manager.VanityStatus() = %+v", status)
	}
}

func TestTorBinaryVersionOutputAndFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test uses a shell executable")
	}
	root := t.TempDir()
	versionScript := filepath.Join(root, "tor-version")
	if err := os.WriteFile(versionScript, []byte("#!/bin/sh\nprintf '%s\\n' 'Tor version 0.4.8.12, build'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := torBinaryVersion(context.Background(), versionScript); got != "0.4.8.12" {
		t.Fatalf("torBinaryVersion(valid) = %q", got)
	}
	if got := torBinaryVersion(context.Background(), filepath.Join(root, "missing")); got != "unknown" {
		t.Fatalf("torBinaryVersion(missing) = %q, want unknown", got)
	}
}

func TestReadIdentityRejectsSymlinkAndOversizedKey(t *testing.T) {
	privateKey, publicKey, address, err := generateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "secret")
	if err := os.WriteFile(target, []byte(torSecretHeader+"invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "hs_ed25519_secret_key")); err != nil {
		t.Fatal(err)
	}
	if err := writeIdentityFiles(filepath.Join(root, "valid"), privateKey, publicKey, address); err == nil {
		t.Fatal("writeIdentityFiles accepted a missing destination directory")
	}
	if _, _, _, err := readIdentity(root); err == nil {
		t.Fatal("readIdentity accepted a symlinked secret key")
	}

	oversized := filepath.Join(root, "oversized")
	if err := os.Mkdir(oversized, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oversized, "hs_ed25519_secret_key"), bytes.Repeat([]byte("x"), 2048), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readIdentity(oversized); err == nil {
		t.Fatal("readIdentity accepted an oversized key file")
	}
}
