package config

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestNewConfigWatcherRequiresInputs(t *testing.T) {
	initial := DefaultConfig()
	callback := func(*Config, *Config, []string) error { return nil }
	for _, test := range []struct {
		name string
		path string
		cfg  *Config
		cb   ConfigReloadCallback
	}{
		{name: "path", cfg: initial, cb: callback},
		{name: "initial config", path: filepath.Join(t.TempDir(), "server.yml"), cb: callback},
		{name: "callback", path: filepath.Join(t.TempDir(), "server.yml"), cfg: initial},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewConfigWatcher(test.path, test.cfg, test.cb); err == nil {
				t.Fatal("NewConfigWatcher() succeeded with a missing input")
			}
		})
	}
}

func TestConfigurationFilesSignatureTracksConfigDropinsAndAssets(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "server.yml")
	writeWatcherFile(t, configPath, "server:\n  mode: production\n")
	first, err := configurationFilesSignature(configPath)
	if err != nil {
		t.Fatal(err)
	}
	writeWatcherFile(t, configPath, "server:\n  mode: development\n")
	second, err := configurationFilesSignature(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("server.yml change did not change the signature")
	}
	writeWatcherFile(t, filepath.Join(directory, "yaml.d", "branding.yml"), "server:\n  branding:\n    title: Citylist\n")
	third, err := configurationFilesSignature(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if second == third {
		t.Fatal("new drop-in did not change the signature")
	}
	writeWatcherFile(t, filepath.Join(directory, "assets", "logo.png"), "image data")
	fourth, err := configurationFilesSignature(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if third == fourth {
		t.Fatal("asset change did not change the signature")
	}
	if err := os.Remove(filepath.Join(directory, "yaml.d", "branding.yml")); err != nil {
		t.Fatal(err)
	}
	fifth, err := configurationFilesSignature(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if fourth == fifth {
		t.Fatal("drop-in removal did not change the signature")
	}
}

func TestConfigWatcherAppliesValidChangeAndDetectsRestartSettings(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "server.yml")
	writeWatcherFile(t, configPath, "server:\n  mode: production\n")
	initial := loadWatcherInitial(t, configPath)
	var calls int
	var appliedMode string
	var restart []string
	watcher, err := NewConfigWatcher(configPath, initial, func(previous, next *Config, requiresRestart []string) error {
		calls++
		if previous.Server.Mode != "production" {
			t.Errorf("previous mode = %q, want production", previous.Server.Mode)
		}
		appliedMode = next.Server.Mode
		restart = append([]string(nil), requiresRestart...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	watcher.debounce = 0
	writeWatcherFile(t, configPath, "server:\n  mode: development\n  address: 127.0.0.1\n")
	if err := watcher.checkForChanges(context.Background()); err != nil {
		t.Fatalf("checkForChanges() error: %v", err)
	}
	if calls != 1 || appliedMode != "development" {
		t.Fatalf("callback calls=%d, applied mode=%q", calls, appliedMode)
	}
	if !reflect.DeepEqual(restart, []string{"server.address"}) {
		t.Fatalf("restart settings = %v, want [server.address]", restart)
	}
	watcher.mu.RLock()
	gotMode := watcher.current.Server.Mode
	watcher.mu.RUnlock()
	if gotMode != "development" {
		t.Fatalf("last good mode = %q, want development", gotMode)
	}
}

func TestConfigWatcherDetectsSSLSettingsWithoutApplyingThem(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "server.yml")
	writeWatcherFile(t, configPath, "server:\n  mode: production\n  ssl:\n    enabled: false\n")
	var restart []string
	initial := loadWatcherInitial(t, configPath)
	watcher, err := NewConfigWatcher(configPath, initial, func(_, _ *Config, requiresRestart []string) error {
		restart = append([]string(nil), requiresRestart...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	watcher.debounce = 0
	writeWatcherFile(t, configPath, "server:\n  mode: production\n  ssl:\n    enabled: true\n")
	if err := watcher.checkForChanges(context.Background()); err != nil {
		t.Fatalf("checkForChanges() error: %v", err)
	}
	if !reflect.DeepEqual(restart, []string{"server.ssl"}) {
		t.Fatalf("restart settings = %v, want [server.ssl]", restart)
	}
}

func TestConfigWatcherPreservesLastGoodConfigAfterInvalidFile(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "server.yml")
	writeWatcherFile(t, configPath, "server:\n  mode: production\n")
	initial := loadWatcherInitial(t, configPath)
	calls := 0
	watcher, err := NewConfigWatcher(configPath, initial, func(_, _ *Config, _ []string) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	watcher.debounce = 0
	writeWatcherFile(t, configPath, "server: [not a mapping]\n")
	if err := watcher.checkForChanges(context.Background()); err == nil {
		t.Fatal("invalid configuration was accepted")
	}
	watcher.mu.RLock()
	gotMode, gotSignature := watcher.current.Server.Mode, watcher.signature
	watcher.mu.RUnlock()
	if gotMode != "production" || calls != 0 {
		t.Fatalf("invalid file changed last good config: mode=%q callbacks=%d", gotMode, calls)
	}
	if gotSignature == "" {
		t.Fatal("last good file signature was lost")
	}
}

func TestConfigWatcherPreservesLastGoodConfigAfterMalformedDropin(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "server.yml")
	writeWatcherFile(t, configPath, "server:\n  mode: production\n")
	initial := loadWatcherInitial(t, configPath)
	calls := 0
	watcher, err := NewConfigWatcher(configPath, initial, func(_, _ *Config, _ []string) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	watcher.debounce = 0
	writeWatcherFile(t, filepath.Join(directory, "yaml.d", "20-invalid.yml"), "server: [not a mapping\n")
	if err := watcher.checkForChanges(context.Background()); err == nil {
		t.Fatal("malformed drop-in was accepted")
	}
	watcher.mu.RLock()
	gotMode := watcher.current.Server.Mode
	watcher.mu.RUnlock()
	if gotMode != "production" || calls != 0 {
		t.Fatalf("malformed drop-in changed last good config: mode=%q callbacks=%d", gotMode, calls)
	}
}

func TestConfigWatcherCallbackFailureDoesNotCommit(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "server.yml")
	writeWatcherFile(t, configPath, "server:\n  mode: production\n")
	initial := loadWatcherInitial(t, configPath)
	watcher, err := NewConfigWatcher(configPath, initial, func(_, _ *Config, _ []string) error {
		return os.ErrPermission
	})
	if err != nil {
		t.Fatal(err)
	}
	watcher.debounce = 0
	writeWatcherFile(t, configPath, "server:\n  mode: development\n")
	if err := watcher.checkForChanges(context.Background()); err == nil {
		t.Fatal("callback failure was ignored")
	}
	watcher.mu.RLock()
	defer watcher.mu.RUnlock()
	if watcher.current.Server.Mode != "production" {
		t.Fatalf("failed callback changed last good config to %q", watcher.current.Server.Mode)
	}
}

func TestRestartRequiredSettings(t *testing.T) {
	before := DefaultConfig()
	after := *before
	after.Server.Mode = "development"
	if got := restartRequiredSettings(before, &after); len(got) != 0 {
		t.Fatalf("hot-reloadable mode change requires restart: %v", got)
	}
	after.Server.Port = "8080"
	after.Server.Address = "127.0.0.1"
	after.Server.Daemonize = true
	after.Server.Tor.UseNetwork = true
	if got, want := restartRequiredSettings(before, &after), []string{"server.address", "server.daemonize", "server.port", "server.tor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("restartRequiredSettings() = %v, want %v", got, want)
	}
}

func TestWaitForConfigDebounceStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForConfigDebounce(ctx, time.Second); err != context.Canceled {
		t.Fatalf("waitForConfigDebounce() error = %v, want context canceled", err)
	}
}

func writeWatcherFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func loadWatcherInitial(t *testing.T, path string) *Config {
	t.Helper()
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load initial config: %v", err)
	}
	return cfg
}
