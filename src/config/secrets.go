package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const tokenAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

type contactWebhookRole struct {
	name   string
	config *ContactRoleConfig
}

func webhookRoles(cfg *Config) []contactWebhookRole {
	return []contactWebhookRole{
		{name: "admin", config: &cfg.Server.Contact.Admin},
		{name: "security", config: &cfg.Server.Contact.Security},
		{name: "abuse", config: &cfg.Server.Contact.Abuse},
		{name: "general", config: &cfg.Server.Contact.General},
	}
}

func loadSecrets(secretsPath, configPath string, cfg *Config, mergedConfig []byte) (string, error) {
	legacySecrets, err := extractLegacySecrets(mergedConfig)
	if err != nil {
		return "", fmt.Errorf("extract legacy configuration secrets: %w", err)
	}
	values := map[string]interface{}{}
	if _, err := os.Lstat(secretsPath); err == nil {
		if err := securePrivateFile(secretsPath); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	data, err := os.ReadFile(secretsPath)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &values); err != nil {
			return "", fmt.Errorf("parse %s: %w", secretsPath, err)
		}
		if values == nil {
			values = map[string]interface{}{}
		}
	}
	mergeMissingConfigMap(values, legacySecrets)

	server := nestedMap(values, "server")
	backup := nestedMap(server, "backup")
	database := nestedMap(server, "database")
	security := nestedMap(server, "security")
	notifications := nestedMap(server, "notifications")
	email := nestedMap(notifications, "email")
	smtp := nestedMap(email, "smtp")
	pages := nestedMap(server, "pages")
	contactPage := nestedMap(pages, "contact")
	metrics := nestedMap(server, "metrics")
	metricsAuth := nestedMap(metrics, "auth")
	metricsTokens := nestedMap(metricsAuth, "tokens")
	secretData := nestedMap(values, "data")
	cve := nestedMap(secretData, "cve")
	contact := nestedMap(server, "contact")
	generatedWebhookSecrets := false
	for _, role := range webhookRoles(cfg) {
		roleValues := nestedMap(contact, role.name)
		webhookValues := nestedMap(roleValues, "webhooks")
		if role.config.Webhooks == nil {
			role.config.Webhooks = map[string]string{}
		}
		if role.config.WebhookSecrets == nil {
			role.config.WebhookSecrets = map[string]string{}
		}
		for transport, value := range webhookValues {
			secret := stringValue(value)
			if strings.HasSuffix(transport, "_secret") {
				role.config.WebhookSecrets[strings.TrimSuffix(transport, "_secret")] = secret
			} else if secret != "" {
				role.config.Webhooks[transport] = secret
			}
		}
		for transport, webhookURL := range role.config.Webhooks {
			if strings.HasSuffix(transport, "_secret") || strings.TrimSpace(webhookURL) == "" {
				continue
			}
			secretName := transport + "_secret"
			secret := stringValue(webhookValues[secretName])
			if secret == "" {
				generated := make([]byte, 32)
				if _, err := rand.Read(generated); err != nil {
					return "", fmt.Errorf("generate server.contact.%s.webhooks.%s_secret: %w", role.name, transport, err)
				}
				secret = base64.StdEncoding.EncodeToString(generated)
				webhookValues[secretName] = secret
				cfg.GeneratedSecrets = append(cfg.GeneratedSecrets, "server.contact."+role.name+".webhooks."+secretName)
				generatedWebhookSecrets = true
			}
			webhookValues[transport] = webhookURL
			role.config.WebhookSecrets[transport] = secret
		}
		roleValues["webhooks"] = webhookValues
		contact[role.name] = roleValues
	}
	legacyConfigHasSecrets, err := configFilesContainLegacySecrets(configPath)
	if err != nil {
		return "", err
	}
	if value := stringValue(database["token"]); value != "" {
		cfg.Server.Database.Token = value
	} else if cfg.Server.Database.Token != "" {
		database["token"] = cfg.Server.Database.Token
	}
	if value := stringValue(database["url"]); value != "" {
		cfg.Server.Database.URL = value
	} else if databaseURLHasCredential(cfg.Server.Database.URL) {
		database["url"] = cfg.Server.Database.URL
		legacyConfigHasSecrets = true
	}
	if value := stringValue(server["token"]); value != "" {
		cfg.Server.Token = value
	} else if cfg.Server.Token != "" {
		server["token"] = cfg.Server.Token
	}
	if value := stringValue(security["encryption_key"]); value != "" {
		cfg.Server.Security.EncryptionKey = value
		if cfg.Server.Security.EncryptionKeyVersion == 0 {
			cfg.Server.Security.EncryptionKeyVersion = 1
			security["encryption_key_version"] = 1
		}
	} else if cfg.Server.Security.EncryptionKey != "" {
		security["encryption_key"] = cfg.Server.Security.EncryptionKey
		if cfg.Server.Security.EncryptionKeyVersion == 0 {
			cfg.Server.Security.EncryptionKeyVersion = 1
		}
		security["encryption_key_version"] = cfg.Server.Security.EncryptionKeyVersion
	}
	if version, ok := integerValue(security["encryption_key_version"]); ok {
		cfg.Server.Security.EncryptionKeyVersion = version
	}
	if value := stringValue(smtp["password"]); value != "" {
		cfg.Server.Notifications.Email.SMTP.Password = value
	} else if cfg.Server.Notifications.Email.SMTP.Password != "" {
		smtp["password"] = cfg.Server.Notifications.Email.SMTP.Password
	}
	if value := stringValue(contactPage["captcha_secret_key"]); value != "" {
		cfg.Server.Pages.Contact.CaptchaSecretKey = value
	} else if cfg.Server.Pages.Contact.CaptchaSecretKey != "" {
		contactPage["captcha_secret_key"] = cfg.Server.Pages.Contact.CaptchaSecretKey
	}
	if value := stringValue(cve["api_key"]); value != "" {
		cfg.Data.CVE.APIKey = value
	} else if cfg.Data.CVE.APIKey != "" {
		cve["api_key"] = cfg.Data.CVE.APIKey
		secretData["cve"] = cve
		values["data"] = secretData
		legacyConfigHasSecrets = true
	}
	if value := stringValue(backup["encryption_password"]); value != "" {
		cfg.Server.Backup.EncryptionPassword = value
	} else if cfg.Server.Backup.EncryptionPassword != "" {
		backup["encryption_password"] = cfg.Server.Backup.EncryptionPassword
		server["backup"] = backup
		values["server"] = server
		legacyConfigHasSecrets = true
	}
	metricsTokenFields := []struct {
		name  string
		value *string
	}{
		{name: "prometheus", value: &cfg.Server.Metrics.Auth.Tokens.Prometheus},
		{name: "grafana", value: &cfg.Server.Metrics.Auth.Tokens.Grafana},
		{name: "loki", value: &cfg.Server.Metrics.Auth.Tokens.Loki},
	}
	generatedMetricsTokens := false
	generatedMetricNames := make([]string, 0, len(metricsTokenFields))
	for _, token := range metricsTokenFields {
		if value := stringValue(metricsTokens[token.name]); value != "" {
			*token.value = value
			continue
		} else if *token.value != "" {
			metricsTokens[token.name] = *token.value
			generatedMetricsTokens = true
			generatedMetricNames = append(generatedMetricNames, token.name)
			continue
		}
		generated, err := randomToken("mtr_")
		if err != nil {
			return "", fmt.Errorf("generate metrics %s token: %w", token.name, err)
		}
		*token.value = generated
		metricsTokens[token.name] = *token.value
		generatedMetricsTokens = true
		generatedMetricNames = append(generatedMetricNames, token.name)
		continue
	}
	for _, token := range metricsTokenFields {
		if !validToken(*token.value, "mtr_") {
			return "", fmt.Errorf("server.metrics.auth.tokens.%s must be an mtr_ token with 32 base62 characters", token.name)
		}
	}

	generatedToken := ""
	if cfg.Server.Token == "" {
		generatedToken, err = randomToken("adm_")
		if err != nil {
			return "", err
		}
		cfg.Server.Token = generatedToken
		server["token"] = generatedToken
	}
	if !validToken(cfg.Server.Token, "adm_") {
		return "", errors.New("server.token in secrets.yml must be an adm_ token with 32 base62 characters")
	}

	generatedKey := false
	if cfg.Server.Security.EncryptionKey == "" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return "", fmt.Errorf("generate server.security.encryption_key: %w", err)
		}
		cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(key)
		cfg.Server.Security.EncryptionKeyVersion = 1
		security["encryption_key"] = cfg.Server.Security.EncryptionKey
		security["encryption_key_version"] = 1
		generatedKey = true
	} else {
		key, err := base64.StdEncoding.DecodeString(cfg.Server.Security.EncryptionKey)
		if err != nil || len(key) != 32 || cfg.Server.Security.EncryptionKeyVersion != 1 {
			return "", errors.New("server.security.encryption_key must be a base64-encoded 32-byte key with version 1")
		}
	}

	if generatedToken != "" {
		cfg.GeneratedSecrets = append(cfg.GeneratedSecrets, "server.token")
	}
	if generatedKey {
		cfg.GeneratedSecrets = append(cfg.GeneratedSecrets, "server.security.encryption_key")
	}
	if generatedMetricsTokens {
		for _, name := range generatedMetricNames {
			cfg.GeneratedSecrets = append(cfg.GeneratedSecrets, "server.metrics.auth.tokens."+name)
		}
	}
	if generatedToken != "" || generatedKey || generatedMetricsTokens || generatedWebhookSecrets || legacyConfigHasSecrets {
		server["database"] = database
		if cfg.Server.Backup.EncryptionPassword != "" {
			backup["encryption_password"] = cfg.Server.Backup.EncryptionPassword
		}
		server["backup"] = backup
		server["security"] = security
		smtp["password"] = cfg.Server.Notifications.Email.SMTP.Password
		email["smtp"] = smtp
		notifications["email"] = email
		server["notifications"] = notifications
		contactPage["captcha_secret_key"] = cfg.Server.Pages.Contact.CaptchaSecretKey
		pages["contact"] = contactPage
		server["pages"] = pages
		server["contact"] = contact
		values["server"] = server
		metricsAuth["tokens"] = metricsTokens
		metrics["auth"] = metricsAuth
		server["metrics"] = metrics
		values["server"] = server
		if err := writeSecretsAtomically(secretsPath, values); err != nil {
			return "", err
		}
	}
	if legacyConfigHasSecrets {
		if err := removeLegacySecretsFromConfigFiles(configPath); err != nil {
			return "", fmt.Errorf("remove migrated secrets from configuration files: %w", err)
		}
	}
	return generatedToken, nil
}

func databaseURLHasCredential(rawURL string) bool {
	if parsed, err := url.Parse(rawURL); err == nil && parsed.User != nil {
		return true
	}
	for _, part := range strings.SplitN(rawURL, "?", 2)[1:] {
		for _, option := range strings.Split(part, "&") {
			key, _, _ := strings.Cut(option, "=")
			if strings.EqualFold(key, "authToken") || strings.EqualFold(key, "token") || strings.EqualFold(key, "password") || strings.EqualFold(key, "pwd") {
				return true
			}
		}
	}
	return strings.Contains(rawURL, "@")
}

func nestedMap(values map[string]interface{}, key string) map[string]interface{} {
	if nested, ok := values[key].(map[string]interface{}); ok {
		return nested
	}
	nested := map[string]interface{}{}
	values[key] = nested
	return nested
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}

func integerValue(value interface{}) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), true
	case uint64:
		return int(number), true
	default:
		return 0, false
	}
}

func randomToken(prefix string) (string, error) {
	var token strings.Builder
	token.Grow(len(prefix) + 32)
	token.WriteString(prefix)
	buffer := make([]byte, 64)
	for token.Len() < len(prefix)+32 {
		if _, err := rand.Read(buffer); err != nil {
			return "", fmt.Errorf("generate operator token: %w", err)
		}
		for _, value := range buffer {
			if value >= 248 {
				continue
			}
			token.WriteByte(tokenAlphabet[int(value)%len(tokenAlphabet)])
			if token.Len() == len(prefix)+32 {
				break
			}
		}
	}
	return token.String(), nil
}

func validToken(token, prefix string) bool {
	if !strings.HasPrefix(token, prefix) || len(token) != len(prefix)+32 {
		return false
	}
	for _, value := range token[len(prefix):] {
		if !strings.ContainsRune(tokenAlphabet, value) {
			return false
		}
	}
	return true
}

func writeSecretsAtomically(path string, values map[string]interface{}) error {
	data, err := yaml.Marshal(values)
	if err != nil {
		return err
	}
	return writePrivateFileAtomically(path, data)
}

// ReplaceEncryptionKey persists a generated AES-256 key in secrets.yml atomically.
func ReplaceEncryptionKey(configPath string, key []byte) error {
	if len(key) != 32 {
		return errors.New("encryption key must contain 32 bytes")
	}
	path := filepath.Join(filepath.Dir(configPath), "secrets.yml")
	values := map[string]interface{}{}
	if _, err := os.Lstat(path); err == nil {
		if err = securePrivateFile(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err = yaml.Unmarshal(data, &values); err != nil {
			return err
		}
	}
	if values == nil {
		values = map[string]interface{}{}
	}
	server := nestedMap(values, "server")
	security := nestedMap(server, "security")
	security["encryption_key"] = base64.StdEncoding.EncodeToString(key)
	security["encryption_key_version"] = 1
	server["security"] = security
	values["server"] = server
	return writeSecretsAtomically(path, values)
}

func writePrivateFileAtomically(path string, data []byte) error {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".private-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = directoryHandle.Close() }()
	return directoryHandle.Sync()
}

func securePrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file", path)
	}
	return os.Chmod(path, 0600)
}
