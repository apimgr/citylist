package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type cliConfig struct {
	Server struct {
		Primary    string `yaml:"primary"`
		APIVersion string `yaml:"api_version"`
		Timeout    string `yaml:"timeout"`
		Retry      int    `yaml:"retry"`
		RetryDelay string `yaml:"retry_delay"`
	} `yaml:"server"`
	Auth struct {
		Token     string `yaml:"token,omitempty"`
		TokenFile string `yaml:"token_file,omitempty"`
	} `yaml:"auth"`
	Output struct {
		Format  string `yaml:"format"`
		Color   string `yaml:"color"`
		Pager   string `yaml:"pager"`
		Quiet   bool   `yaml:"quiet"`
		Verbose bool   `yaml:"verbose"`
	} `yaml:"output"`
	Display struct {
		Mode string `yaml:"mode"`
	} `yaml:"display"`
	Theme struct {
		Mode string `yaml:"mode"`
	} `yaml:"theme"`
	TUI struct {
		Enabled bool   `yaml:"enabled"`
		Theme   string `yaml:"theme"`
		Mouse   bool   `yaml:"mouse"`
		Unicode bool   `yaml:"unicode"`
	} `yaml:"tui"`
	Logging struct {
		Level    string `yaml:"level"`
		File     string `yaml:"file"`
		MaxSize  string `yaml:"max_size"`
		MaxFiles int    `yaml:"max_files"`
	} `yaml:"logging"`
	Cache struct {
		Enabled bool   `yaml:"enabled"`
		TTL     string `yaml:"ttl"`
		MaxSize string `yaml:"max_size"`
	} `yaml:"cache"`
	Update struct {
		Auto          bool   `yaml:"auto"`
		CheckInterval string `yaml:"check_interval"`
		Channel       string `yaml:"channel"`
	} `yaml:"update"`
	Debug    bool           `yaml:"debug"`
	Defaults map[string]any `yaml:"defaults"`
	Lang     string         `yaml:"lang"`
}

func defaultCLIConfig() cliConfig {
	var cfg cliConfig
	cfg.Server.APIVersion = "v1"
	cfg.Server.Timeout = "30s"
	cfg.Server.Retry = 3
	cfg.Server.RetryDelay = "1s"
	cfg.Output.Format = "table"
	cfg.Output.Color = "auto"
	cfg.Output.Pager = "auto"
	cfg.TUI.Enabled = true
	cfg.TUI.Theme = "dark"
	cfg.TUI.Mouse = true
	cfg.TUI.Unicode = true
	cfg.Logging.Level = "warn"
	cfg.Logging.MaxSize = "10MB"
	cfg.Logging.MaxFiles = 5
	cfg.Cache.Enabled = true
	cfg.Cache.TTL = "5m"
	cfg.Cache.MaxSize = "100MB"
	cfg.Update.CheckInterval = "per_invocation"
	cfg.Update.Channel = "stable"
	cfg.Defaults = map[string]any{}
	cfg.Lang = "auto"
	cfg.Display.Mode = "auto"
	cfg.Theme.Mode = "auto"
	return cfg
}

func cliConfigPath(profile string) (string, error) {
	if strings.TrimSpace(profile) == "" {
		return defaultCLIConfigPath(), nil
	}
	profile = strings.TrimSpace(profile)
	if strings.HasPrefix(profile, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		if profile == "~" {
			profile = home
		} else if strings.HasPrefix(profile, "~/") || strings.HasPrefix(profile, `~\\`) {
			profile = filepath.Join(home, profile[2:])
		}
	}
	if !filepath.IsAbs(profile) {
		profile = filepath.Join(filepath.Dir(defaultCLIConfigPath()), profile)
	}
	return resolveYAMLExtension(profile), nil
}

func defaultCLIConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if runtime.GOOS == "windows" {
		base = os.Getenv("APPDATA")
	}
	if base == "" {
		home, _ := os.UserHomeDir()
		if runtime.GOOS == "windows" {
			base = filepath.Join(home, "AppData", "Roaming")
		} else {
			base = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(base, InternalOrg, InternalName, "cli.yml")
}

func resolveYAMLExtension(path string) string {
	extension := filepath.Ext(path)
	if extension == ".yml" || extension == ".yaml" || extension != "" {
		return path
	}
	if _, err := os.Stat(path + ".yml"); err == nil {
		return path + ".yml"
	}
	if _, err := os.Stat(path + ".yaml"); err == nil {
		return path + ".yaml"
	}
	return path + ".yml"
}

func loadCLIConfig(path string) (cliConfig, error) {
	cfg := defaultCLIConfig()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return cfg, fmt.Errorf("create CLI config directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil && runtime.GOOS != "windows" {
		return cfg, fmt.Errorf("secure CLI config directory: %w", err)
	}
	info, statErr := os.Lstat(path)
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return cfg, errors.New("CLI config must not be a symbolic link")
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return cfg, fmt.Errorf("inspect CLI config: %w", statErr)
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := saveCLIConfig(path, cfg); err != nil {
			return cfg, err
		}
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read CLI config: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0600); err != nil {
			return cfg, fmt.Errorf("secure CLI config: %w", err)
		}
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse CLI config: %w", err)
	}
	if cfg.Server.APIVersion == "" {
		cfg.Server.APIVersion = "v1"
	}
	if cfg.Server.Timeout == "" {
		cfg.Server.Timeout = "30s"
	}
	if cfg.Server.RetryDelay == "" {
		cfg.Server.RetryDelay = "1s"
	}
	timeout, timeoutErr := time.ParseDuration(cfg.Server.Timeout)
	retryDelay, retryErr := time.ParseDuration(cfg.Server.RetryDelay)
	if timeoutErr != nil || timeout <= 0 || retryErr != nil || retryDelay < 0 || cfg.Server.Retry < 0 || cfg.Server.Retry > 10 {
		return cfg, errors.New("invalid server timeout or retry policy in CLI config")
	}
	if cfg.Output.Format == "" {
		cfg.Output.Format = "table"
	}
	if cfg.Output.Color == "" {
		cfg.Output.Color = "auto"
	}
	if cfg.Output.Color != "auto" && cfg.Output.Color != "yes" && cfg.Output.Color != "no" {
		return cfg, errors.New("output.color must be auto, yes, or no")
	}
	if cfg.Output.Pager == "" {
		cfg.Output.Pager = "auto"
	}
	if cfg.Output.Pager != "auto" && cfg.Output.Pager != "always" && cfg.Output.Pager != "never" {
		return cfg, errors.New("output.pager must be auto, always, or never")
	}
	if cfg.TUI.Theme == "" {
		cfg.TUI.Theme = "dark"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "warn"
	}
	if cfg.Logging.Level != "debug" && cfg.Logging.Level != "info" && cfg.Logging.Level != "warn" && cfg.Logging.Level != "error" {
		return cfg, errors.New("logging.level must be debug, info, warn, or error")
	}
	if cfg.Logging.MaxFiles < 0 || cfg.Logging.MaxFiles > 100 {
		return cfg, errors.New("logging.max_files must be between 0 and 100")
	}
	if _, err := parseSize(cfg.Logging.MaxSize); err != nil {
		return cfg, errors.New("logging.max_size must be a positive size such as 10MB")
	}
	if cfg.Cache.TTL == "" {
		cfg.Cache.TTL = "5m"
	}
	if ttl, err := time.ParseDuration(cfg.Cache.TTL); err != nil || ttl < 0 {
		return cfg, errors.New("cache.ttl must be a non-negative duration")
	}
	if _, err := parseSize(cfg.Cache.MaxSize); err != nil {
		return cfg, errors.New("cache.max_size must be a positive size such as 100MB")
	}
	if cfg.Update.Channel == "" {
		cfg.Update.Channel = "stable"
	}
	if cfg.Update.Channel != "stable" && cfg.Update.Channel != "beta" && cfg.Update.Channel != "devel" {
		return cfg, errors.New("update.channel must be stable, beta, or devel")
	}
	if cfg.Update.CheckInterval == "" {
		cfg.Update.CheckInterval = "per_invocation"
	}
	if cfg.Update.CheckInterval != "per_invocation" {
		return cfg, errors.New("update.check_interval must be per_invocation")
	}
	if cfg.Lang == "" {
		cfg.Lang = "auto"
	}
	return cfg, nil
}

func parseSize(value string) (int64, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	units := []struct {
		suffix string
		factor int64
	}{{"GB", 1 << 30}, {"G", 1 << 30}, {"MB", 1 << 20}, {"M", 1 << 20}, {"KB", 1 << 10}, {"K", 1 << 10}, {"B", 1}}
	factor := int64(1)
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			factor = unit.factor
			value = strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			break
		}
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number <= 0 || number > int64(^uint64(0)>>1)/factor {
		return 0, errors.New("size is invalid")
	}
	return number * factor, nil
}

func saveCLIConfig(path string, cfg cliConfig) error {
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("encode CLI config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create CLI config directory: %w", err)
	}
	if runtime.GOOS == "windows" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("open CLI config: %w", err)
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return fmt.Errorf("write CLI config: %w", err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return fmt.Errorf("sync CLI config: %w", err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close CLI config: %w", err)
		}
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cli-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary CLI config: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(0600); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("secure temporary CLI config: %w", err)
		}
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write CLI config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync CLI config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close CLI config: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace CLI config: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0600); err != nil {
			return fmt.Errorf("secure CLI config: %w", err)
		}
	}
	return nil
}

func saveServerIfEmptyOrInvalid(path string, cfg *cliConfig, value string) error {
	if cfg == nil {
		return errors.New("CLI configuration is required")
	}
	if !validServerURL(value) {
		return errors.New("server URL is invalid")
	}
	if validServerURL(cfg.Server.Primary) {
		return nil
	}
	cfg.Server.Primary = value
	return saveCLIConfig(path, *cfg)
}
