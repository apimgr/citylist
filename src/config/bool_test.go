package config

import "testing"

func TestParseBool(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		defaultValue bool
		want         bool
		wantErr      bool
	}{
		{name: "truthy", value: "YES", want: true},
		{name: "falsy", value: "No", defaultValue: true, want: false},
		{name: "default true", value: " ", defaultValue: true, want: true},
		{name: "default false", value: "", want: false},
		{name: "invalid", value: "perhaps", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseBool(test.value, test.defaultValue)
			if (err != nil) != test.wantErr {
				t.Fatalf("ParseBool() error = %v, wantErr %t", err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("ParseBool() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestParseBoolOrDefault(t *testing.T) {
	if !ParseBoolOrDefault("test.enabled", "YES", false) {
		t.Fatal("valid true configuration should parse")
	}
	if ParseBoolOrDefault("test.enabled.invalid", "perhaps", true) != true {
		t.Fatal("invalid configuration should return its default")
	}
	if ParseBoolOrDefault("test.enabled.false", "no", true) {
		t.Fatal("valid false configuration should parse")
	}
}

func TestBooleanValueSets(t *testing.T) {
	truthy := []string{"1", "y", "t", "yes", "true", "on", "ok", "enable", "enabled", "yep", "yup", "yeah", "aye", "si", "oui", "da", "hai", "affirmative", "accept", "allow", "grant", "sure", "totally"}
	falsy := []string{"0", "n", "f", "no", "false", "off", "disable", "disabled", "nope", "nah", "nay", "nein", "non", "niet", "iie", "lie", "negative", "reject", "block", "revoke", "deny", "never", "noway"}
	for _, value := range truthy {
		if !IsTruthy(value) || IsFalsy(value) {
			t.Errorf("%q was not recognized as truthy only", value)
		}
	}
	for _, value := range falsy {
		if !IsFalsy(value) || IsTruthy(value) {
			t.Errorf("%q was not recognized as falsy only", value)
		}
	}
}
