//go:build windows

package i2p

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func terminateChild(command *exec.Cmd, exited <-chan struct{}) {
	if command == nil || command.Process == nil {
		return
	}
	_ = command.Process.Kill()
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
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
	output, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
	if err != nil || !strings.Contains(strings.ToLower(string(output)), strings.ToLower(filepath.Base(binary))) {
		_ = os.Remove(pidPath)
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return nil
	}
	_ = process.Kill()
	_ = os.Remove(pidPath)
	return nil
}
