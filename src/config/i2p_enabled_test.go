package config

import (
	"os"
	"testing"
)

func TestResolveI2PEnabled(t *testing.T) {
	tests := []struct {
		name        string
		container   bool
		flagValue   string
		flagSet     bool
		env         string
		envSet      bool
		configValue *bool
		want        bool
		wantErr     bool
	}{
		{name: "container default", container: true, want: true},
		{name: "host default", want: false},
		{name: "config explicit true", configValue: boolPointer(true), want: true},
		{name: "config explicit false", container: true, configValue: boolPointer(false), want: false},
		{name: "environment overrides config", container: true, env: "no", envSet: true, configValue: boolPointer(true), want: false},
		{name: "flag overrides environment", env: "yes", envSet: true, flagValue: "off", flagSet: true, want: false},
		{name: "invalid flag uses default", container: true, flagValue: "maybe", flagSet: true, want: false},
		{name: "invalid environment uses container default", container: true, env: "maybe", envSet: true, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.envSet {
				t.Setenv("I2P_ENABLED", test.env)
			} else {
				previous, hadPrevious := os.LookupEnv("I2P_ENABLED")
				if err := os.Unsetenv("I2P_ENABLED"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if hadPrevious {
						_ = os.Setenv("I2P_ENABLED", previous)
					} else {
						_ = os.Unsetenv("I2P_ENABLED")
					}
				})
			}
			cfg := DefaultConfig()
			cfg.Server.I2P.Enabled = test.configValue
			got, err := ResolveI2PEnabled(cfg, test.flagValue, test.flagSet, test.container)
			if (err != nil) != test.wantErr {
				t.Fatalf("ResolveI2PEnabled() error = %v, wantErr %t", err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("ResolveI2PEnabled() = %t, want %t", got, test.want)
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }
