//go:build gui && (linux || darwin || windows)

package gui

import "testing"

func TestGUICommandAndFieldDefinitionsStayAligned(t *testing.T) {
	if len(guiCommands) != len(guiFields) {
		t.Fatalf("GUI command count = %d, field definition count = %d", len(guiCommands), len(guiFields))
	}
	for index, command := range guiCommands {
		if command == "" {
			t.Errorf("GUI command %d is empty", index)
		}
		if len(guiFields[index]) > 3 {
			t.Errorf("GUI command %q has %d input fields; maximum is 3", command, len(guiFields[index]))
		}
	}
}

func TestGUITextAndFieldLabelsUseTranslations(t *testing.T) {
	if got := guiText("en", "gui_title"); got != "Citylist" {
		t.Fatalf("guiText(en, title) = %q, want Citylist", got)
	}
	if got := guiFieldLabel("en", 0, 0, "fallback"); got != "City ID" {
		t.Fatalf("city ID label = %q, want City ID", got)
	}
	if got := guiFieldLabel("en", 8, 1, "fallback"); got != "Longitude" {
		t.Fatalf("longitude label = %q, want Longitude", got)
	}
	if got := guiFieldLabel("en", -1, 0, "fallback"); got != "fallback" {
		t.Fatalf("invalid command label = %q, want fallback", got)
	}
	if got := guiFieldLabel("en", 0, -1, "fallback"); got != "fallback" {
		t.Fatalf("invalid field label = %q, want fallback", got)
	}
}

func TestGUIAvailable(t *testing.T) {
	if !Available() {
		t.Fatal("GUI build reports unavailable on a supported platform")
	}
}

func TestGUIHealthAndTokenLabels(t *testing.T) {
	for _, test := range []struct {
		body     string
		fallback string
		want     string
	}{
		{body: `{"project":{"name":"Citylist"},"version":"1.2.3"}`, fallback: "server", want: "Citylist 1.2.3"},
		{body: `{"project":{"name":"Citylist"}}`, fallback: "server", want: "Citylist"},
		{body: `{}`, fallback: "server", want: "server"},
		{body: `invalid`, fallback: "server", want: "server"},
	} {
		if got := guiHealthServerLabel([]byte(test.body), test.fallback); got != test.want {
			t.Errorf("guiHealthServerLabel(%q) = %q, want %q", test.body, got, test.want)
		}
	}
	if got := guiTokenKind([]byte(`{"data":{"kind":"owner"}}`)); got != "owner" {
		t.Fatalf("guiTokenKind() = %q, want owner", got)
	}
	if got := guiTokenKind([]byte("invalid")); got != "" {
		t.Fatalf("invalid guiTokenKind() = %q, want empty", got)
	}
}
