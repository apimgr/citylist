package paths

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// ProjectOrganization resolves the owning organization from the module path.
func ProjectOrganization() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	parts := strings.Split(info.Main.Path, "/")
	if len(parts) < 3 || parts[len(parts)-2] == "" {
		return ""
	}
	return parts[len(parts)-2]
}

// NewProjectTempDir creates an owner-only project-scoped temporary directory.
func NewProjectTempDir(internalName string) (string, func(), error) {
	organization := ProjectOrganization()
	if organization == "" || strings.TrimSpace(internalName) == "" {
		return "", func() {}, errors.New("project organization and internal name are required for temporary files")
	}
	base := filepath.Join(os.TempDir(), organization)
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", func() {}, err
	}
	if err := os.Chmod(base, 0700); err != nil {
		return "", func() {}, err
	}
	directory, err := os.MkdirTemp(base, internalName+"-")
	if err != nil {
		return "", func() {}, err
	}
	return directory, func() { _ = os.RemoveAll(directory) }, nil
}
