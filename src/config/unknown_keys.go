package config

import (
	"log"
	"reflect"
	"strings"
)

func discardUnknownConfigKeys(values map[string]any, schema reflect.Type, prefix string) {
	if schema.Kind() == reflect.Pointer {
		schema = schema.Elem()
	}
	if schema.Kind() != reflect.Struct {
		return
	}
	fields := configFieldTypes(schema)
	for name, value := range values {
		fieldType, exists := fields[name]
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if !exists {
			log.Printf("Ignoring unknown configuration key %s", path)
			delete(values, name)
			continue
		}
		nestedValues, isMap := value.(map[string]any)
		if !isMap {
			continue
		}
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct {
			discardUnknownConfigKeys(nestedValues, fieldType, path)
		}
	}
}

func configFieldTypes(schema reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, schema.NumField())
	for index := 0; index < schema.NumField(); index++ {
		field := schema.Field(index)
		parts := strings.Split(field.Tag.Get("yaml"), ",")
		name := parts[0]
		inline := false
		for _, option := range parts[1:] {
			if option == "inline" {
				inline = true
				break
			}
		}
		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if inline && fieldType.Kind() == reflect.Struct {
			for nestedName, nestedType := range configFieldTypes(fieldType) {
				fields[nestedName] = nestedType
			}
			continue
		}
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name[:1]) + field.Name[1:]
		}
		fields[name] = field.Type
	}
	return fields
}
