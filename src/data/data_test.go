package data

import "testing"

func TestReadFile(t *testing.T) {
	content, err := ReadFile("city.list.json")
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	if len(content) == 0 {
		t.Error("ReadFile() returned empty content")
	}
}

func TestReadFileNotFound(t *testing.T) {
	_, err := ReadFile("does-not-exist.json")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}
