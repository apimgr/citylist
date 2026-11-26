package swagger

import (
	"strings"
	"testing"
)

func TestThemeCSSUsesSharedPageTokens(t *testing.T) {
	css := ThemeCSS()
	for _, expected := range []string{
		"#swagger-ui",
		"var(--color-bg-primary)",
		"var(--color-text-primary)",
		"var(--color-info)",
		"var(--color-success)",
		"var(--color-warning)",
		"var(--color-error)",
		"var(--color-border)",
	} {
		if !strings.Contains(css, expected) {
			t.Errorf("Swagger theme CSS is missing %q", expected)
		}
	}
	if strings.Contains(css, "#282a36") || strings.Contains(css, "#ffffff") {
		t.Error("Swagger theme CSS must derive colors from shared page tokens")
	}
}
