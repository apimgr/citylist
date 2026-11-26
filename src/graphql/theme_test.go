package graphql

import (
	"regexp"
	"strings"
	"testing"
)

func TestThemeCSSUsesSharedPalettes(t *testing.T) {
	css := ThemeCSS()
	for _, expected := range []string{
		"body.graphiql-dark .graphiql-container",
		"body.graphiql-light .graphiql-container",
		"--color-primary:",
		"--color-base:",
		"--color-neutral:",
		"var(--color-border)",
		"var(--font-sans)",
	} {
		if !strings.Contains(css, expected) {
			t.Errorf("GraphiQL theme CSS is missing %q", expected)
		}
	}
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(css) {
		t.Error("GraphiQL theme CSS must derive its colors from the shared palettes")
	}
}

func TestHSLToken(t *testing.T) {
	tests := map[string]string{
		"#000000":     "0, 0%, 0%",
		"#ffffff":     "0, 0%, 100%",
		"#ff0000":     "0, 100%, 50%",
		"not-a-color": "0, 0%, 0%",
	}
	for input, want := range tests {
		if got := hslToken(input); got != want {
			t.Errorf("hslToken(%q) = %q, want %q", input, got, want)
		}
	}
}
