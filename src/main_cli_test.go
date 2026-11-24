package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/apimgr/citylist/src/config"
)

func TestSetServerOutputLanguageFallbackAndNormalization(t *testing.T) {
	previous := serverOutputLanguage
	t.Cleanup(func() { serverOutputLanguage = previous })
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "fr_CA.UTF-8")

	setServerOutputLanguage("auto", nil)
	if serverOutputLanguage != "fr" {
		t.Fatalf("language from LANG = %q, want fr", serverOutputLanguage)
	}
	cfg := config.DefaultConfig()
	cfg.Server.I18n.DefaultLanguage = "de"
	setServerOutputLanguage(" auto ", cfg)
	if serverOutputLanguage != "de" {
		t.Fatalf("configured language = %q, want de", serverOutputLanguage)
	}
	setServerOutputLanguage("unsupported", cfg)
	if serverOutputLanguage != "en" {
		t.Fatalf("unsupported language = %q, want English fallback", serverOutputLanguage)
	}
	setServerOutputLanguage(" ES ", cfg)
	if serverOutputLanguage != "es" {
		t.Fatalf("explicit language = %q, want normalized es", serverOutputLanguage)
	}
}

func TestSetInitialServerOutputLanguageConfigAndFlagPrecedence(t *testing.T) {
	previous := serverOutputLanguage
	t.Cleanup(func() { serverOutputLanguage = previous })
	root := mainProjectTempDir(t)
	cfg := config.DefaultConfig()
	cfg.Server.I18n.DefaultLanguage = "it"
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(filepath.Join(configDir, "server.yml"), cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_DIR", filepath.Join(root, "ignored"))
	setInitialServerOutputLanguage([]string{"--config", configDir})
	if serverOutputLanguage != "it" {
		t.Fatalf("configured initial language = %q, want it", serverOutputLanguage)
	}
	setInitialServerOutputLanguage([]string{"--config=" + configDir, "--lang", "ar"})
	if serverOutputLanguage != "ar" {
		t.Fatalf("flag initial language = %q, want ar", serverOutputLanguage)
	}
}

func TestServerOutputFormattingAndBuildEpoch(t *testing.T) {
	previousLanguage, previousEpoch := serverOutputLanguage, BuildEpoch
	t.Cleanup(func() {
		serverOutputLanguage = previousLanguage
		BuildEpoch = previousEpoch
	})
	serverOutputLanguage = "en"
	if got := serverOutputFormat("missing.test.key", "Update {version} on {branch}", map[string]string{"version": "1.2.3", "branch": "stable"}); got != "Update 1.2.3 on stable" {
		t.Fatalf("serverOutputFormat() = %q", got)
	}
	BuildEpoch = "1763596800"
	if got := mustBuildEpoch(); got != 1763596800 {
		t.Fatalf("mustBuildEpoch() = %d", got)
	}
	BuildEpoch = "invalid"
	if got := mustBuildEpoch(); got != 0 {
		t.Fatalf("invalid BuildEpoch = %d, want 0", got)
	}
}

func TestServiceManagedStartAndApplicationContainerDetection(t *testing.T) {
	t.Setenv("INVOCATION_ID", "unit-42")
	t.Setenv("RC_SVCNAME", "")
	if !serviceManagedStart() {
		t.Fatal("serviceManagedStart() did not recognize INVOCATION_ID")
	}
	t.Setenv("INVOCATION_ID", "")
	t.Setenv("RC_SVCNAME", "citylist")
	if !serviceManagedStart() {
		t.Fatal("serviceManagedStart() did not recognize RC_SVCNAME")
	}

	t.Setenv("container", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	expectedContainer := false
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv", "/dev/lxc"} {
		if _, err := os.Stat(marker); err == nil {
			expectedContainer = true
			break
		}
	}
	if got := isApplicationContainer(); got != expectedContainer {
		t.Fatalf("isApplicationContainer() = %v without environment markers, want %v from runtime markers", got, expectedContainer)
	}
	t.Setenv("container", "podman")
	if !isApplicationContainer() {
		t.Fatal("isApplicationContainer() did not detect the container marker")
	}
	t.Setenv("container", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	if !isApplicationContainer() {
		t.Fatal("isApplicationContainer() did not detect Kubernetes")
	}
}
