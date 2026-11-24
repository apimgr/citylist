package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func ensureCLIDirectories(configPath string) error {
	configDir := filepath.Dir(configPath)
	dataRoot := os.Getenv("XDG_DATA_HOME")
	cacheRoot := os.Getenv("XDG_CACHE_HOME")
	logRoot := ""
	if runtime.GOOS == "windows" {
		dataRoot = os.Getenv("LOCALAPPDATA")
		cacheRoot = dataRoot
		logRoot = dataRoot
	}
	if dataRoot == "" || cacheRoot == "" || logRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("find home directory: %w", err)
		}
		if runtime.GOOS == "windows" {
			local := filepath.Join(home, "AppData", "Local")
			if dataRoot == "" {
				dataRoot = local
			}
			if cacheRoot == "" {
				cacheRoot = local
			}
			if logRoot == "" {
				logRoot = local
			}
		} else {
			if dataRoot == "" {
				dataRoot = filepath.Join(home, ".local", "share")
			}
			if cacheRoot == "" {
				cacheRoot = filepath.Join(home, ".cache")
			}
			logRoot = filepath.Join(home, ".local", "log")
		}
	}
	dataDir := filepath.Join(dataRoot, InternalOrg, InternalName)
	cacheDir := filepath.Join(cacheRoot, InternalOrg, InternalName)
	logDir := filepath.Join(logRoot, InternalOrg, InternalName)
	if runtime.GOOS == "windows" {
		dataDir = filepath.Join(dataRoot, InternalOrg, InternalName, "data")
		cacheDir = filepath.Join(cacheRoot, InternalOrg, InternalName, "cache")
		logDir = filepath.Join(logRoot, InternalOrg, InternalName, "log")
	}
	for _, dir := range []string{configDir, dataDir, cacheDir, logDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create CLI runtime directory %s: %w", dir, err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(dir, 0700); err != nil {
				return fmt.Errorf("secure CLI runtime directory %s: %w", dir, err)
			}
		}
	}
	return nil
}

func cliCacheDirectory() string {
	root := os.Getenv("XDG_CACHE_HOME")
	if runtime.GOOS == "windows" {
		root = os.Getenv("LOCALAPPDATA")
	}
	if root == "" {
		home, _ := os.UserHomeDir()
		if runtime.GOOS == "windows" {
			root = filepath.Join(home, "AppData", "Local")
		} else {
			root = filepath.Join(home, ".cache")
		}
	}
	cacheDir := filepath.Join(root, InternalOrg, InternalName)
	if runtime.GOOS == "windows" {
		cacheDir = filepath.Join(cacheDir, "cache")
	}
	return cacheDir
}
