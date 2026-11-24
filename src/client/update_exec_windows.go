//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func replaceClientBinary(currentPath, stagedPath string) error {
	oldPath := currentPath + ".old"
	staged, err := os.Open(stagedPath)
	if err != nil {
		return err
	}
	defer func() { _ = staged.Close() }()
	local, err := os.CreateTemp(filepath.Dir(currentPath), ".citylist-cli-update-*.exe")
	if err != nil {
		return fmt.Errorf("stage update beside executable: %w", err)
	}
	localPath := local.Name()
	defer func() { _ = os.Remove(localPath) }()
	if _, err := io.Copy(local, staged); err != nil {
		_ = local.Close()
		return err
	}
	if err := local.Close(); err != nil {
		return err
	}
	_ = os.Remove(oldPath)
	if err := os.Rename(currentPath, oldPath); err != nil {
		return fmt.Errorf("move old executable aside: %w", err)
	}
	if err := os.Rename(localPath, currentPath); err != nil {
		_ = os.Rename(oldPath, currentPath)
		return fmt.Errorf("install new executable: %w", err)
	}
	return nil
}

func restartUpdatedCLI() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, restartCLIArgs(os.Args[1:])...)
	command.Stdout, command.Stderr, command.Stdin = os.Stdout, os.Stderr, os.Stdin
	if err := command.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
