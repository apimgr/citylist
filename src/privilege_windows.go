//go:build windows

package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

func elevatedAtStartup() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func isWindowsService() bool {
	isService, err := svc.IsWindowsService()
	return err == nil && isService
}

func bindPrivilegedPorts(string, string) ([]net.Listener, error) { return nil, nil }

func closeListeners([]net.Listener) {}

func dropToServiceAccount(string, string) error { return nil }

func bindRemainingPorts(address, ports string, listeners []net.Listener) ([]net.Listener, error) {
	for _, raw := range strings.Split(ports, ",") {
		port, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || port < 1 || port > 65535 {
			closeListeners(listeners)
			return nil, fmt.Errorf("invalid server port %q", raw)
		}
		listener, err := net.Listen("tcp", net.JoinHostPort(strings.Trim(address, "[]"), strconv.Itoa(port)))
		if err != nil {
			closeListeners(listeners)
			return nil, fmt.Errorf("bind server port %d: %w", port, err)
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}
