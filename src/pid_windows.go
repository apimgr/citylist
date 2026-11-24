//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	if !isCitylistProcess(pid) {
		_ = os.Remove(path)
		return false, 0, nil
	}
	return true, pid, nil
}

func isCitylistProcess(pid int) bool {
	output, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
	return err == nil && strings.Contains(strings.ToLower(string(output)), "citylist.exe")
}
