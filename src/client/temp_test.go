package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func clientTestDir(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve client test source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	metadata, err := os.ReadFile(filepath.Join(root, "IDEA.md"))
	if err != nil {
		t.Fatal(err)
	}
	value := func(key string) string {
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:\s*([^\s#]+)\s*$`)
		match := pattern.FindSubmatch(metadata)
		if len(match) != 2 {
			t.Fatalf("IDEA.md is missing %s", key)
		}
		return string(match[1])
	}
	base := filepath.Join(os.TempDir(), value("project_org"))
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(base, value("internal_name")+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
