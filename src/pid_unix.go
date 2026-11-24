//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func checkPIDFile(path string) (bool, int, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid < 1 {
		_ = os.Remove(path)
		return false, 0, nil
	}
	process, err := os.FindProcess(pid)
	if err != nil || process.Signal(syscall.Signal(0)) != nil {
		_ = os.Remove(path)
		return false, 0, nil
	}
	if !isCitylistProcess(pid) {
		_ = os.Remove(path)
		return false, 0, nil
	}
	return true, pid, nil
}

func isCitylistProcess(pid int) bool {
	link := fmt.Sprintf("/proc/%d/exe", pid)
	if target, err := os.Readlink(link); err == nil {
		return filepath.Base(target) == projectName
	}
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	return err == nil && filepath.Base(strings.TrimSpace(string(output))) == projectName
}
