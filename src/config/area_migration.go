package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

const nodeConfigHeader = "# This node only: listen, identity, database, cache, ssl. The rest is in yaml.d/ and credentials are in secrets.yml.\n"

var nodeServerKeys = map[string]bool{
	"port": true, "address": true, "fqdn": true, "mode": true, "api_version": true,
	"baseurl": true, "pidfile": true, "daemonize": true, "debug": true, "database": true,
	"cache": true, "ssl": true, "trusted_proxies": true,
}

var serverAreaFiles = map[string]string{
	"branding": "branding.yml", "seo": "branding.yml",
	"cors": "web.yml", "csrf": "web.yml", "url_detection": "web.yml", "compression": "web.yml", "healthz": "web.yml", "i18n": "web.yml",
	"pages": "pages.yml", "contact": "contact.yml", "privacy": "privacy.yml", "compliance": "privacy.yml", "tracking": "privacy.yml",
	"security": "security.yml", "rate_limit": "security.yml", "geoip": "security.yml", "limits": "security.yml", "tokens": "security.yml",
	"logging": "logs.yml", "logs": "logs.yml", "metrics": "metrics.yml", "notifications": "notifications.yml",
	"tor": "tor.yml", "i2p": "i2p.yml",
	"maintenance": "maintenance.yml", "scheduler": "maintenance.yml", "update": "maintenance.yml", "auto_update": "maintenance.yml", "backup": "maintenance.yml", "restore": "maintenance.yml",
}

// migrateAreaConfig splits settings in server.yml into the files assigned by
// PART 5. It runs before typed decoding so product-specific sections can be
// moved to app.yml without being rejected as unknown configuration fields.
func migrateAreaConfig(path string) {
	migrateAreaConfigWithPrecedence(path, false)
}

func migrateAreaConfigWithPrecedence(path string, savedValuesWin bool) {
	original, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("Warning: configuration area migration skipped: read %s: %v", path, err)
		return
	}
	root := map[string]any{}
	if err := yaml.Unmarshal(original, &root); err != nil || root == nil {
		log.Printf("Warning: configuration area migration skipped: %s cannot be parsed", path)
		return
	}
	changed := false
	areas := map[string]map[string]any{}
	area := func(name string) map[string]any {
		if areas[name] == nil {
			areas[name] = map[string]any{}
		}
		return areas[name]
	}

	server, _ := root["server"].(map[string]any)
	for key, value := range server {
		if nodeServerKeys[key] {
			continue
		}
		movedKey := key
		if key == "logging" {
			movedKey = "logs"
			value = migrateLegacyLogging(value)
		}
		filename := serverAreaFiles[key]
		if filename == "" {
			filename = "app.yml"
		}
		putNested(area(filename), "server", movedKey, value)
		delete(server, key)
		changed = true
	}
	if len(server) == 0 {
		delete(root, "server")
	}

	if web, ok := root["web"].(map[string]any); ok {
		if ui, exists := web["ui"]; exists {
			putNested(area("branding.yml"), "web", "ui", ui)
			delete(web, "ui")
			changed = true
		}
		if len(web) > 0 {
			area("web.yml")["web"] = cloneAnyMap(web)
			delete(root, "web")
			changed = true
		} else if changed {
			delete(root, "web")
		}
	}
	if tor, exists := root["tor"]; exists {
		area("tor.yml")["tor"] = tor
		delete(root, "tor")
		changed = true
	}
	if data, exists := root["data"]; exists {
		area("security.yml")["data"] = data
		delete(root, "data")
		changed = true
	}
	for key, value := range root {
		if key == "server" || key == "web" || key == "tor" || key == "data" {
			continue
		}
		area("app.yml")[key] = value
		delete(root, key)
		changed = true
	}
	if !changed {
		return
	}

	directory := filepath.Join(filepath.Dir(path), "yaml.d")
	if err := os.MkdirAll(directory, 0700); err != nil {
		log.Printf("Warning: configuration area migration skipped: create %s: %v", directory, err)
		return
	}
	if err := os.Chmod(directory, 0700); err != nil {
		info, statErr := os.Stat(directory)
		if statErr != nil || info.Mode().Perm()&0077 != 0 {
			log.Printf("Warning: configuration area migration skipped: secure %s: %v", directory, err)
			return
		}
	}
	if info, statErr := os.Stat(path); statErr != nil || info.Mode().Perm()&0200 == 0 {
		log.Printf("Warning: configuration area migration skipped: %s is read-only", path)
		return
	}
	if info, statErr := os.Stat(directory); statErr != nil || info.Mode().Perm()&0200 == 0 {
		log.Printf("Warning: configuration area migration skipped: %s is read-only", directory)
		return
	}

	backupPath := path + ".bak"
	if err := writePrivateFileAtomically(backupPath, original); err != nil {
		log.Printf("Warning: configuration area migration skipped: back up %s: %v", path, err)
		return
	}
	for _, name := range sortedAreaNames(areas) {
		areaPath := filepath.Join(directory, name)
		if err := mergeExistingArea(areaPath, areas[name], savedValuesWin); err != nil {
			log.Printf("Warning: configuration area migration skipped after backup: %v", err)
			return
		}
	}
	remaining, err := yaml.Marshal(root)
	if err != nil {
		log.Printf("Warning: configuration area migration skipped after backup: marshal %s: %v", path, err)
		return
	}
	if err := writePrivateFileAtomically(path, append([]byte(nodeConfigHeader), remaining...)); err != nil {
		log.Printf("Warning: configuration area migration incomplete; backup is %s: %v", backupPath, err)
	}
}

func migrateLegacyLogging(value any) map[string]any {
	legacy, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	logs := map[string]any{}
	for key, value := range legacy {
		switch key {
		case "level":
			logs[key] = value
		case "access_format":
			putNested(logs, "access", "format", value)
		case "access", "server", "error", "app", "auth", "audit", "security", "debug":
			logs[key] = value
		default:
			log.Printf("Ignoring unknown legacy configuration key server.logging.%s", key)
		}
	}
	return logs
}

func mergeExistingArea(path string, moved map[string]any, movedValuesWin bool) error {
	destination := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &destination); err != nil {
			return fmt.Errorf("parse existing area file %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read existing area file %s: %w", path, err)
	}
	if destination == nil {
		destination = map[string]any{}
	}
	if movedValuesWin {
		mergeConfigMap(destination, moved)
	} else {
		mergeMissingConfigMap(destination, moved)
	}
	data, err := yaml.Marshal(destination)
	if err != nil {
		return fmt.Errorf("marshal area file %s: %w", path, err)
	}
	return writePrivateFileAtomically(path, data)
}

func mergeMissingConfigMap(destination, source map[string]any) {
	for key, value := range source {
		sourceMap, sourceIsMap := value.(map[string]any)
		if !sourceIsMap {
			if _, exists := destination[key]; !exists {
				destination[key] = value
			}
			continue
		}
		destinationMap, destinationIsMap := destination[key].(map[string]any)
		if !destinationIsMap {
			if _, exists := destination[key]; exists {
				continue
			}
			destinationMap = map[string]any{}
			destination[key] = destinationMap
		}
		mergeMissingConfigMap(destinationMap, sourceMap)
	}
}

func putNested(destination map[string]any, section, key string, value any) {
	nested, _ := destination[section].(map[string]any)
	if nested == nil {
		nested = map[string]any{}
		destination[section] = nested
	}
	nested[key] = value
}

func cloneAnyMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		if nested, ok := value.(map[string]any); ok {
			clone[key] = cloneAnyMap(nested)
		} else {
			clone[key] = value
		}
	}
	return clone
}

func sortedAreaNames(areas map[string]map[string]any) []string {
	names := make([]string, 0, len(areas))
	for name := range areas {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
