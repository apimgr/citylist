//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func replaceClientBinary(currentPath, stagedPath string) error {
	info, err := os.Stat(currentPath)
	if err != nil {
		return err
	}
	staged, err := os.Open(stagedPath)
	if err != nil {
		return err
	}
	defer func() { _ = staged.Close() }()
	local, err := os.CreateTemp(filepath.Dir(currentPath), ".citylist-cli-update-*")
	if err != nil {
		return fmt.Errorf("stage update beside executable: %w", err)
	}
	localPath := local.Name()
	defer func() { _ = os.Remove(localPath) }()
	if _, err := io.Copy(local, staged); err != nil {
		_ = local.Close()
		return err
	}
	if err := local.Chmod(info.Mode().Perm()); err != nil {
		_ = local.Close()
		return err
	}
	if err := local.Sync(); err != nil {
		_ = local.Close()
		return err
	}
	if err := local.Close(); err != nil {
		return err
	}
	return os.Rename(localPath, currentPath)
}

func restartUpdatedCLI() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := append([]string{executable}, restartCLIArgs(os.Args[1:])...)
	return syscall.Exec(executable, args, os.Environ())
}
