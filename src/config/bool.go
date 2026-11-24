package config

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

var truthyValues = map[string]bool{
	"1": true, "y": true, "t": true,
	"yes": true, "true": true, "on": true, "ok": true,
	"enable": true, "enabled": true,
	"yep": true, "yup": true, "yeah": true,
	"aye": true, "si": true, "oui": true, "da": true, "hai": true,
	"affirmative": true, "accept": true, "allow": true, "grant": true,
	"sure": true, "totally": true,
}

var falsyValues = map[string]bool{
	"0": true, "n": true, "f": true,
	"no": true, "false": true, "off": true,
	"disable": true, "disabled": true,
	"nope": true, "nah": true, "nay": true,
	"nein": true, "non": true, "niet": true, "iie": true, "lie": true,
	"negative": true, "reject": true, "block": true, "revoke": true,
	"deny": true, "never": true, "noway": true,
}

var invalidBooleanWarnings sync.Map

// ParseBool accepts the application's complete truthy and falsy vocabulary.
func ParseBool(value string, defaultValue bool) (bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return defaultValue, nil
	}
	if truthyValues[value] {
		return true, nil
	}
	if falsyValues[value] {
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean value: %q", value)
}

// ParseBoolOrDefault uses the default and logs once when configuration input is invalid.
func ParseBoolOrDefault(name, value string, defaultValue bool) bool {
	parsed, err := ParseBool(value, defaultValue)
	if err == nil {
		return parsed
	}
	if _, loaded := invalidBooleanWarnings.LoadOrStore(name, struct{}{}); !loaded {
		slog.Warn("invalid boolean, using default", "setting", name, "value", value, "default", defaultValue)
	}
	return defaultValue
}

// IsTruthy reports whether value is one of the accepted truthy strings.
func IsTruthy(value string) bool {
	return truthyValues[strings.ToLower(strings.TrimSpace(value))]
}

// IsFalsy reports whether value is one of the accepted falsy strings.
func IsFalsy(value string) bool {
	return falsyValues[strings.ToLower(strings.TrimSpace(value))]
}
