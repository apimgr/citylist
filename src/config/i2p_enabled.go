package config

import "os"

// ResolveI2PEnabled applies CLI, environment, explicit configuration, and
// container-default precedence for the optional I2P eepsite.
func ResolveI2PEnabled(cfg *Config, flagValue string, flagSet, inContainer bool) (bool, error) {
	if flagSet {
		return ParseBoolOrDefault("i2p.enabled flag", flagValue, false), nil
	}
	if value, set := os.LookupEnv("I2P_ENABLED"); set {
		return ParseBoolOrDefault("I2P_ENABLED", value, inContainer), nil
	}
	if cfg != nil && cfg.Server.I2P.Enabled != nil {
		return *cfg.Server.I2P.Enabled, nil
	}
	return inContainer, nil
}
