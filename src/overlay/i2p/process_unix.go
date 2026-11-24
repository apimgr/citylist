//go:build !windows

package i2p

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func terminateChild(command *exec.Cmd, exited <-chan struct{}) {
	if command == nil || command.Process == nil {
		return
	}
	_ = command.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
		}
	}
}

func reapStaleI2PD(pidPath, binary string) error {
	contents, err := os.ReadFile(pidPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil || pid <= 1 {
		return errors.New("invalid stale i2pd pid file")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return nil
	}
	if !ownedI2PDProcess(pid, binary) {
		_ = os.Remove(pidPath)
		return nil
	}
	_ = process.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !ownedI2PDProcess(pid, binary) {
			_ = os.Remove(pidPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = process.Kill()
	_ = os.Remove(pidPath)
	return nil
}

func ownedI2PDProcess(pid int, binary string) bool {
	actual, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		output, psErr := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
		if psErr != nil {
			return false
		}
		arguments := strings.Fields(string(output))
		if len(arguments) == 0 {
			return false
		}
		return filepath.Base(arguments[0]) == filepath.Base(binary)
	}
	expected, err := filepath.EvalSymlinks(binary)
	if err != nil {
		expected = binary
	}
	actual, err = filepath.EvalSymlinks(actual)
	if err == nil {
		return filepath.Clean(actual) == filepath.Clean(expected)
	}
	return strings.TrimSuffix(filepath.Base(actual), " (deleted)") == filepath.Base(expected)
}
