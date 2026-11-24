package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	configWatchInterval = 5 * time.Second
	configWatchDebounce = 250 * time.Millisecond
)

// ConfigReloadCallback applies a fully loaded configuration. The callback must
// not modify either configuration value; it receives the restart-only fields
// that changed and must keep those values pending until the next start.
type ConfigReloadCallback func(previous, next *Config, restartRequired []string) error

// ConfigWatcher observes configuration and branding asset files and reloads
// only complete configurations that pass the normal loader and validation.
type ConfigWatcher struct {
	configPath string
	callback   ConfigReloadCallback
	interval   time.Duration
	debounce   time.Duration

	mu        sync.RWMutex
	current   *Config
	signature string
	restart   map[string]string
}

// NewConfigWatcher creates a watcher using the already-loaded configuration as
// its last known-good state.
func NewConfigWatcher(configPath string, initial *Config, callback ConfigReloadCallback) (*ConfigWatcher, error) {
	if strings.TrimSpace(configPath) == "" {
		return nil, fmt.Errorf("configuration path is required")
	}
	if initial == nil {
		return nil, fmt.Errorf("initial configuration is required")
	}
	if callback == nil {
		return nil, fmt.Errorf("configuration reload callback is required")
	}
	signature, err := configurationFilesSignature(configPath)
	if err != nil {
		return nil, err
	}
	restartSnapshot, err := restartSensitiveSnapshot(configPath)
	if err != nil {
		return nil, err
	}
	return &ConfigWatcher{
		configPath: filepath.Clean(configPath),
		callback:   callback,
		interval:   configWatchInterval,
		debounce:   configWatchDebounce,
		current:    initial,
		signature:  signature,
		restart:    restartSnapshot,
	}, nil
}

// Run polls the configuration tree until ctx is canceled.
func (w *ConfigWatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.checkForChanges(ctx); err != nil && ctx.Err() == nil {
				log.Printf("Configuration reload rejected; keeping last good configuration: %v", err)
			}
		}
	}
}

func (w *ConfigWatcher) checkForChanges(ctx context.Context) error {
	observed, err := configurationFilesSignature(w.configPath)
	if err != nil {
		return err
	}
	w.mu.RLock()
	previousSignature := w.signature
	w.mu.RUnlock()
	if observed == previousSignature {
		return nil
	}

	if err := waitForConfigDebounce(ctx, w.debounce); err != nil {
		return err
	}
	stable, err := configurationFilesSignature(w.configPath)
	if err != nil {
		return err
	}
	if stable != observed {
		return nil
	}
	if err := validateReloadDropins(w.configPath); err != nil {
		return err
	}

	next, err := Load(w.configPath)
	if err != nil {
		return err
	}
	loadedSignature, err := configurationFilesSignature(w.configPath)
	if err != nil {
		return err
	}
	if loadedSignature != stable {
		return nil
	}
	w.mu.RLock()
	previous := w.current
	previousRestart := w.restart
	w.mu.RUnlock()
	restartRequired := restartRequiredSettings(previous, next)
	nextRestart, err := restartSensitiveSnapshot(w.configPath)
	if err != nil {
		return err
	}
	if previousRestart["server.ssl"] != nextRestart["server.ssl"] {
		restartRequired = append(restartRequired, "server.ssl")
		sort.Strings(restartRequired)
	}
	if len(restartRequired) > 0 {
		log.Printf("Configuration changes require restart to take effect: %s", strings.Join(restartRequired, ", "))
	}
	if err := w.callback(previous, next, restartRequired); err != nil {
		return fmt.Errorf("apply configuration reload: %w", err)
	}

	w.mu.Lock()
	w.current = next
	w.signature = loadedSignature
	w.restart = nextRestart
	w.mu.Unlock()
	return nil
}

func validateReloadDropins(configPath string) error {
	dropins, err := readDropins(filepath.Join(filepath.Dir(configPath), "yaml.d"))
	if err != nil {
		return err
	}
	for _, dropin := range dropins {
		if _, err := decodeConfigMap(dropin.data); err != nil {
			return fmt.Errorf("parse configuration drop-in %s: %w", dropin.path, err)
		}
	}
	return nil
}

func waitForConfigDebounce(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func restartRequiredSettings(previous, next *Config) []string {
	if previous == nil || next == nil {
		return nil
	}
	settings := make([]string, 0, 6)
	if previous.Server.Port != next.Server.Port {
		settings = append(settings, "server.port")
	}
	if previous.Server.Address != next.Server.Address {
		settings = append(settings, "server.address")
	}
	if previous.Server.Daemonize != next.Server.Daemonize {
		settings = append(settings, "server.daemonize")
	}
	if !reflect.DeepEqual(previous.Server.Database, next.Server.Database) {
		settings = append(settings, "server.database")
	}
	if !reflect.DeepEqual(previous.Server.Tor, next.Server.Tor) {
		settings = append(settings, "server.tor")
	}
	sort.Strings(settings)
	return settings
}

func restartSensitiveSnapshot(configPath string) (map[string]string, error) {
	data, err := readMergedConfig(configPath)
	if os.IsNotExist(err) {
		data, err = yaml.Marshal(DefaultConfig())
	}
	if err != nil {
		return nil, fmt.Errorf("read restart-sensitive configuration: %w", err)
	}
	values, err := decodeConfigMap(data)
	if err != nil {
		return nil, fmt.Errorf("parse restart-sensitive configuration: %w", err)
	}
	server, _ := values["server"].(map[string]any)
	result := make(map[string]string, 1)
	ssl, err := yaml.Marshal(server["ssl"])
	if err != nil {
		return nil, fmt.Errorf("encode server.ssl configuration: %w", err)
	}
	digest := sha256.Sum256(ssl)
	result["server.ssl"] = hex.EncodeToString(digest[:])
	return result, nil
}

func configurationFilesSignature(configPath string) (string, error) {
	root := filepath.Dir(configPath)
	paths := []string{
		configPath,
		filepath.Join(root, "secrets.yml"),
		filepath.Join(root, "yaml.d"),
		filepath.Join(root, "assets"),
	}
	entries := make([]string, 0, 32)
	for _, path := range paths {
		if err := appendConfigTree(path, &entries); err != nil {
			return "", err
		}
	}
	sort.Strings(entries)
	hash := sha256.New()
	for _, entry := range entries {
		_, _ = hash.Write([]byte(entry))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func appendConfigTree(path string, entries *[]string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		*entries = append(*entries, "missing:"+filepath.Clean(path))
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect configuration path %s: %w", path, err)
	}
	if !info.IsDir() {
		return appendConfigFile(path, info, entries)
	}
	*entries = append(*entries, fmt.Sprintf("dir:%s:%o", filepath.Clean(path), info.Mode().Perm()))
	err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("inspect configuration path %s: %w", current, walkErr)
		}
		if current == path {
			return nil
		}
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect configuration path %s: %w", current, err)
		}
		if info.IsDir() {
			*entries = append(*entries, fmt.Sprintf("dir:%s:%o", filepath.Clean(current), info.Mode().Perm()))
			return nil
		}
		return appendConfigFile(current, info, entries)
	})
	return err
}

func appendConfigFile(path string, info fs.FileInfo, entries *[]string) error {
	clean := filepath.Clean(path)
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read configuration symlink %s: %w", clean, err)
		}
		*entries = append(*entries, fmt.Sprintf("link:%s:%s", clean, target))
		return nil
	}
	if !info.Mode().IsRegular() {
		*entries = append(*entries, fmt.Sprintf("other:%s:%o", clean, info.Mode()))
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read configuration file %s: %w", clean, err)
	}
	digest := sha256.Sum256(data)
	*entries = append(*entries, fmt.Sprintf("file:%s:%o:%d:%s", clean, info.Mode().Perm(), info.Size(), hex.EncodeToString(digest[:])))
	return nil
}
