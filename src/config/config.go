package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds the application configuration
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	WebUI       WebUIConfig       `yaml:"web-ui"`
	WebRobots   WebRobotsConfig   `yaml:"web-robots"`
	WebSecurity WebSecurityConfig `yaml:"web-security"`
}

// ServerConfig holds server-specific configuration
type ServerConfig struct {
	Port         string        `yaml:"port"`
	FQDN         string        `yaml:"fqdn"`
	Address      string        `yaml:"address"`
	Mode         string        `yaml:"mode"`
	UpdateBranch string        `yaml:"update_branch"`
	Admin        AdminConfig   `yaml:"admin"`
	Session      SessionConfig `yaml:"session"`
	Metrics      MetricsConfig `yaml:"metrics"`
	Logging      LoggingConfig `yaml:"logging"`
}

// AdminConfig holds admin panel settings
type AdminConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	APIToken string `yaml:"api_token"`
}

// SessionConfig holds session settings
type SessionConfig struct {
	Timeout int `yaml:"timeout"`
}

// MetricsConfig holds metrics configuration
type MetricsConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Endpoint string `yaml:"endpoint"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	AccessFormat string `yaml:"access_format"`
	Level        string `yaml:"level"`
}

// WebUIConfig holds web UI configuration
type WebUIConfig struct {
	Theme         string              `yaml:"theme"`
	Notifications NotificationsConfig `yaml:"notifications"`
}

// NotificationsConfig holds notification settings
type NotificationsConfig struct {
	Enabled       bool     `yaml:"enabled"`
	Announcements []string `yaml:"announcements"`
}

// WebRobotsConfig holds robots.txt configuration
type WebRobotsConfig struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// WebSecurityConfig holds security configuration
type WebSecurityConfig struct {
	Admin string `yaml:"admin"`
	CORS  string `yaml:"cors"`
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:         "8080",
			Address:      "0.0.0.0",
			Mode:         "production",
			UpdateBranch: "stable",
			Admin: AdminConfig{
				Username: "admin",
				Password: "",
				APIToken: "",
			},
			Session: SessionConfig{
				Timeout: 3600,
			},
			Metrics: MetricsConfig{
				Enabled:  false,
				Endpoint: "/metrics",
			},
			Logging: LoggingConfig{
				AccessFormat: "apache",
				Level:        "info",
			},
		},
		WebUI: WebUIConfig{
			Theme: "dark",
			Notifications: NotificationsConfig{
				Enabled:       true,
				Announcements: []string{},
			},
		},
		WebRobots: WebRobotsConfig{
			Allow: []string{"/", "/api"},
			Deny:  []string{"/debug"},
		},
		WebSecurity: WebSecurityConfig{
			Admin: "admin@example.com",
			CORS:  "*",
		},
	}
}

// migrateYamlToYml migrates from .yaml to .yml extension
func migrateYamlToYml(path string) error {
	if !strings.HasSuffix(path, ".yml") {
		return nil
	}

	oldPath := strings.TrimSuffix(path, ".yml") + ".yaml"
	if _, err := os.Stat(oldPath); err == nil {
		// Old .yaml file exists, rename it to .yml
		if err := os.Rename(oldPath, path); err != nil {
			return err
		}
	}
	return nil
}

// Load loads configuration from a YAML file
func Load(path string) (*Config, error) {
	// Migrate .yaml to .yml if needed
	if err := migrateYamlToYml(path); err != nil {
		// Log but don't fail - continue with defaults
	}

	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create default config file
			if err := Save(path, cfg); err != nil {
				return cfg, nil // Return defaults if can't save
			}
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// Save saves configuration to a YAML file
func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
