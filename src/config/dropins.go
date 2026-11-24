package config

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func readMergedConfig(path string) ([]byte, error) {
	base, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	defaults, err := yaml.Marshal(DefaultConfig())
	if err != nil {
		return nil, fmt.Errorf("marshal configuration defaults: %w", err)
	}
	defaultValues, err := decodeConfigMap(defaults)
	if err != nil {
		return nil, fmt.Errorf("decode configuration defaults: %w", err)
	}
	operatorValues, err := decodeConfigMap(base)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	removeNullConfigValues(operatorValues)

	dropins, err := readDropins(filepath.Join(filepath.Dir(path), "yaml.d"))
	if err != nil {
		return nil, err
	}
	for _, dropin := range dropins {
		values, err := decodeConfigMap(dropin.data)
		if err != nil {
			log.Printf("Ignoring invalid configuration drop-in %s: %v", dropin.path, err)
			continue
		}
		mergeConfigMap(operatorValues, values)
		log.Printf("Loaded configuration drop-in %s", dropin.path)
	}
	merged := cloneConfigMap(defaultValues)
	mergeConfigMap(merged, operatorValues)
	return yaml.Marshal(merged)
}

func unmarshalConfig(data []byte, cfg *Config) error {
	values, err := decodeConfigMap(data)
	if err != nil {
		return err
	}
	server, _ := values["server"].(map[string]any)
	metrics, _ := server["metrics"].(map[string]any)
	auth, _ := metrics["auth"].(map[string]any)
	if tokens, ok := auth["tokens"].(map[string]any); ok {
		delete(tokens, "prometheus")
		delete(tokens, "grafana")
		delete(tokens, "loki")
	}
	normalizeConfigBooleans(values, reflect.TypeOf(Config{}), reflect.ValueOf(DefaultConfig()).Elem(), "")
	discardUnknownConfigKeys(values, reflect.TypeOf(Config{}), "")
	data, err = yaml.Marshal(values)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return err
	}
	return nil
}

func normalizeConfigBooleans(values any, valueType reflect.Type, defaults reflect.Value, path string) {
	for valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
		if defaults.IsValid() && !defaults.IsNil() {
			defaults = defaults.Elem()
		} else {
			defaults = reflect.Value{}
		}
	}
	if valueType.Kind() == reflect.Struct {
		mapping, ok := values.(map[string]any)
		if !ok {
			return
		}
		for index := 0; index < valueType.NumField(); index++ {
			field := valueType.Field(index)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("yaml"), ",")[0]
			if name == "" {
				name = strings.ToLower(field.Name)
			}
			if name == "-" {
				continue
			}
			child, exists := mapping[name]
			if !exists {
				continue
			}
			var defaultChild reflect.Value
			if defaults.IsValid() && defaults.Kind() == reflect.Struct {
				defaultChild = defaults.Field(index)
			}
			childPath := name
			if path != "" {
				childPath = path + "." + name
			}
			if isBooleanConfigType(field.Type) {
				if child == nil {
					continue
				}
				value := fmt.Sprint(child)
				parsed, parseErr := ParseBool(value, false)
				if parseErr == nil {
					mapping[name] = parsed
					continue
				}
				defaultValue, hasDefault := booleanDefault(defaultChild)
				if !hasDefault {
					ParseBoolOrDefault(childPath, value, false)
					delete(mapping, name)
					continue
				}
				mapping[name] = ParseBoolOrDefault(childPath, value, defaultValue)
				continue
			}
			normalizeConfigBooleans(child, field.Type, defaultChild, childPath)
		}
		return
	}
	if valueType.Kind() == reflect.Slice {
		items, ok := values.([]any)
		if !ok {
			return
		}
		for index := range items {
			var defaultItem reflect.Value
			if defaults.IsValid() && defaults.Kind() == reflect.Slice && index < defaults.Len() {
				defaultItem = defaults.Index(index)
			}
			normalizeConfigBooleans(items[index], valueType.Elem(), defaultItem, fmt.Sprintf("%s.%d", path, index))
		}
		return
	}
	if valueType.Kind() == reflect.Map {
		mapping, ok := values.(map[string]any)
		if !ok {
			return
		}
		for key, child := range mapping {
			var defaultChild reflect.Value
			if defaults.IsValid() && defaults.Kind() == reflect.Map {
				defaultChild = defaults.MapIndex(reflect.ValueOf(key).Convert(defaults.Type().Key()))
			}
			if isBooleanConfigType(valueType.Elem()) && child != nil {
				value := fmt.Sprint(child)
				parsed, parseErr := ParseBool(value, false)
				if parseErr == nil {
					mapping[key] = parsed
					continue
				}
				defaultValue, hasDefault := booleanDefault(defaultChild)
				if !hasDefault {
					defaultValue = false
				}
				mapping[key] = ParseBoolOrDefault(strings.Trim(strings.Join([]string{path, key}, "."), "."), value, defaultValue)
				continue
			}
			normalizeConfigBooleans(child, valueType.Elem(), defaultChild, strings.Trim(strings.Join([]string{path, key}, "."), "."))
		}
	}
}

func isBooleanConfigType(valueType reflect.Type) bool {
	for valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	return valueType.Kind() == reflect.Bool
}

func booleanDefault(value reflect.Value) (bool, bool) {
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false, false
		}
		value = value.Elem()
	}
	if value.IsValid() && value.Kind() == reflect.Bool {
		return value.Bool(), true
	}
	return false, false
}

type configDropin struct {
	path string
	data []byte
}

func readDropins(directory string) ([]configDropin, error) {
	directoryInfo, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect configuration drop-in directory: %w", err)
	}
	if !directoryInfo.IsDir() {
		return nil, fmt.Errorf("configuration drop-in path %s is not a directory", directory)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create configuration drop-in directory: %w", err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		info, statErr := os.Stat(directory)
		if statErr != nil || info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("secure configuration drop-in directory: %w", err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read configuration drop-in directory: %w", err)
	}
	selected := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		candidate := filepath.Join(directory, name)
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("Ignoring configuration drop-in %s: %v", candidate, err)
			}
			continue
		}
		relative, err := filepath.Rel(directory, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			log.Printf("Ignoring configuration drop-in outside yaml.d: %s", candidate)
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := os.Chmod(resolved, 0600); err != nil {
			if info.Mode().Perm()&0077 != 0 {
				log.Printf("Ignoring configuration drop-in with unsafe permissions %s: %v", candidate, err)
				continue
			}
		}
		selected = append(selected, candidate)
	}
	sort.Strings(selected)
	dropins := make([]configDropin, 0, len(selected))
	for _, candidate := range selected {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			log.Printf("Ignoring unreadable configuration drop-in %s: %v", candidate, err)
			continue
		}
		dropins = append(dropins, configDropin{path: candidate, data: data})
	}
	return dropins, nil
}

func decodeConfigMap(data []byte) (map[string]any, error) {
	values := map[string]any{}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&values); err != nil {
		return nil, err
	}
	return values, nil
}

func mergeConfigMap(destination, source map[string]any) {
	for key, value := range source {
		if value == nil {
			delete(destination, key)
			continue
		}
		sourceMap, sourceIsMap := value.(map[string]any)
		if !sourceIsMap {
			destination[key] = value
			continue
		}
		destinationMap, destinationIsMap := destination[key].(map[string]any)
		if !destinationIsMap {
			destinationMap = map[string]any{}
			destination[key] = destinationMap
		}
		mergeConfigMap(destinationMap, sourceMap)
	}
}

func cloneConfigMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		if nested, ok := value.(map[string]any); ok {
			clone[key] = cloneConfigMap(nested)
		} else {
			clone[key] = value
		}
	}
	return clone
}

func removeNullConfigValues(values map[string]any) {
	for key, value := range values {
		if value == nil {
			delete(values, key)
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			removeNullConfigValues(nested)
		}
	}
}
