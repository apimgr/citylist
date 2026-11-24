package paths

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// EnforcePrivatePaths creates credential-bearing paths and secures them before configuration is read.
func EnforcePrivatePaths(configDir, dataDir string) error {
	privateDirs := []string{
		filepath.Join(configDir, "yaml.d"),
		filepath.Join(configDir, "assets"),
		filepath.Join(configDir, "ssl"),
		filepath.Join(configDir, "tor"),
		filepath.Join(configDir, "i2p"),
		filepath.Join(dataDir, "security"),
		filepath.Join(dataDir, "tor"),
		filepath.Join(dataDir, "i2p"),
	}
	for _, directory := range privateDirs {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return fmt.Errorf("create private directory %s: %w", directory, err)
		}
		info, err := os.Lstat(directory)
		if err != nil {
			return fmt.Errorf("inspect private directory %s: %w", directory, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("private path %s has mode %04o and is not a directory", directory, info.Mode().Perm())
		}
		if err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				info, err := entry.Info()
				if err != nil {
					return fmt.Errorf("inspect private path %s: %w", path, err)
				}
				return fmt.Errorf("private path %s has mode %04o and is a symbolic link", path, info.Mode().Perm())
			}
			mode := os.FileMode(0600)
			if entry.IsDir() {
				mode = 0700
			} else if !entry.Type().IsRegular() {
				info, err := entry.Info()
				if err != nil {
					return fmt.Errorf("inspect private path %s: %w", path, err)
				}
				return fmt.Errorf("private path %s has mode %04o and is not a regular file or directory", path, info.Mode().Perm())
			}
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("inspect private path %s: %w", path, err)
			}
			if err := os.Chmod(path, mode); err != nil {
				return fmt.Errorf("secure private path %s with mode %04o (current %04o): %w", path, mode, info.Mode().Perm(), err)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("secure private directory %s: %w", directory, err)
		}
	}
	for _, file := range []string{filepath.Join(configDir, "server.yml"), filepath.Join(configDir, "secrets.yml")} {
		info, err := os.Lstat(file)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect private file %s: %w", file, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("private path %s has mode %04o and is not a regular file", file, info.Mode().Perm())
		}
		if err := os.Chmod(file, 0600); err != nil {
			return fmt.Errorf("secure private file %s with mode 0600 (current %04o): %w", file, info.Mode().Perm(), err)
		}
	}
	return nil
}
