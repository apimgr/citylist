package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

var legacySecretPaths = [][]string{
	{"server", "token"},
	{"server", "security", "encryption_key"},
	{"server", "security", "encryption_key_version"},
	{"server", "notifications", "email", "smtp", "password"},
	{"server", "cache", "password"},
	{"server", "database", "token"},
	{"server", "pages", "contact", "captcha_secret_key"},
	{"server", "ssl", "letsencrypt", "dns_credentials"},
	{"data", "cve", "api_key"},
	{"server", "backup", "encryption_password"},
}

func extractLegacySecrets(data []byte) (map[string]any, error) {
	root := map[string]any{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return map[string]any{}, nil
	}
	secrets := map[string]any{}
	for _, path := range legacySecretPaths {
		value, exists := nestedValue(root, path...)
		if !exists || !hasSecretValue(value) {
			continue
		}
		setNestedValue(secrets, value, path...)
	}
	for _, path := range [][]string{{"server", "database", "url"}, {"server", "cache", "url"}} {
		value, exists := nestedValue(root, path...)
		url, ok := value.(string)
		if exists && ok && databaseURLHasCredential(url) {
			setNestedValue(secrets, url, path...)
		}
	}
	if tokens, exists := nestedValue(root, "server", "metrics", "auth", "tokens"); exists && hasSecretValue(tokens) {
		setNestedValue(secrets, tokens, "server", "metrics", "auth", "tokens")
	}
	if contacts, ok := nestedValue(root, "server", "contact"); ok {
		if roles, ok := contacts.(map[string]any); ok {
			for role, value := range roles {
				contact, ok := value.(map[string]any)
				if !ok {
					continue
				}
				webhooks, exists := contact["webhooks"]
				if exists && hasSecretValue(webhooks) {
					setNestedValue(secrets, webhooks, "server", "contact", role, "webhooks")
				}
			}
		}
	}
	return secrets, nil
}

func configFilesContainLegacySecrets(configPath string) (bool, error) {
	paths := []string{configPath}
	dropins, err := readDropins(filepath.Join(filepath.Dir(configPath), "yaml.d"))
	if err != nil {
		return false, err
	}
	for _, dropin := range dropins {
		paths = append(paths, dropin.path)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("read %s while checking legacy credentials: %w", path, err)
		}
		secrets, err := extractLegacySecrets(data)
		if err != nil {
			return false, fmt.Errorf("parse %s while checking legacy credentials: %w", path, err)
		}
		if len(secrets) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func removeLegacySecretsFromConfigFiles(configPath string) error {
	paths := []string{configPath}
	dropins, err := readDropins(filepath.Join(filepath.Dir(configPath), "yaml.d"))
	if err != nil {
		return err
	}
	for _, dropin := range dropins {
		paths = append(paths, dropin.path)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		root := map[string]any{}
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if root == nil || !deleteLegacySecrets(root) {
			continue
		}
		updated, err := yaml.Marshal(root)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", path, err)
		}
		if path == configPath {
			updated = append([]byte(nodeConfigHeader), updated...)
		}
		if err := writePrivateFileAtomically(path, updated); err != nil {
			return fmt.Errorf("rewrite %s without credentials: %w", path, err)
		}
	}
	return nil
}

func deleteLegacySecrets(root map[string]any) bool {
	changed := false
	for _, path := range legacySecretPaths {
		if _, exists := nestedValue(root, path...); exists {
			changed = deleteNestedValue(root, path...) || changed
		}
	}
	for _, path := range [][]string{{"server", "database", "url"}, {"server", "cache", "url"}} {
		value, exists := nestedValue(root, path...)
		url, ok := value.(string)
		if exists && ok && databaseURLHasCredential(url) {
			changed = deleteNestedValue(root, path...) || changed
		}
	}
	changed = deleteNestedValue(root, "server", "metrics", "auth", "tokens") || changed
	if contacts, ok := nestedValue(root, "server", "contact"); ok {
		if roles, ok := contacts.(map[string]any); ok {
			for role, value := range roles {
				contact, ok := value.(map[string]any)
				if ok {
					_, exists := contact["webhooks"]
					if exists {
						changed = deleteNestedValue(root, "server", "contact", role, "webhooks") || changed
					}
				}
			}
		}
	}
	return changed
}

func nestedValue(root map[string]any, path ...string) (any, bool) {
	var value any = root
	for _, key := range path {
		current, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = current[key]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func setNestedValue(root map[string]any, value any, path ...string) {
	current := root
	for _, key := range path[:len(path)-1] {
		nested, ok := current[key].(map[string]any)
		if !ok {
			nested = map[string]any{}
			current[key] = nested
		}
		current = nested
	}
	current[path[len(path)-1]] = value
}

func deleteNestedValue(root map[string]any, path ...string) bool {
	if len(path) == 0 {
		return false
	}
	current := root
	for _, key := range path[:len(path)-1] {
		nested, ok := current[key].(map[string]any)
		if !ok {
			return false
		}
		current = nested
	}
	if _, exists := current[path[len(path)-1]]; !exists {
		return false
	}
	delete(current, path[len(path)-1])
	return true
}

func hasSecretValue(value any) bool {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case map[string]any:
		for _, nested := range value {
			if hasSecretValue(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range value {
			if hasSecretValue(nested) {
				return true
			}
		}
	default:
		return value != nil && !reflect.ValueOf(value).IsZero()
	}
	return false
}
