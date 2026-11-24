//go:build !windows

package main

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func elevatedAtStartup() bool {
	return os.Geteuid() == 0
}

func isWindowsService() bool { return false }

func bindPrivilegedPorts(address, ports string) ([]net.Listener, error) {
	var listeners []net.Listener
	for _, raw := range strings.Split(ports, ",") {
		port, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || port < 1 || port > 65535 {
			closeListeners(listeners)
			return nil, fmt.Errorf("invalid server port %q", raw)
		}
		if port >= 1024 {
			continue
		}
		listener, err := net.Listen("tcp", net.JoinHostPort(strings.Trim(address, "[]"), strconv.Itoa(port)))
		if err != nil {
			closeListeners(listeners)
			return nil, fmt.Errorf("bind privileged port %d: %w", port, err)
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}

func closeListeners(listeners []net.Listener) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}

func bindRemainingPorts(address, ports string, listeners []net.Listener) ([]net.Listener, error) {
	bound := make(map[string]bool, len(listeners))
	for _, listener := range listeners {
		_, port, err := net.SplitHostPort(listener.Addr().String())
		if err == nil {
			bound[port] = true
		}
	}
	for _, raw := range strings.Split(ports, ",") {
		portText := strings.TrimSpace(raw)
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			closeListeners(listeners)
			return nil, fmt.Errorf("invalid server port %q", raw)
		}
		if bound[portText] {
			continue
		}
		if port < 1024 {
			closeListeners(listeners)
			return nil, fmt.Errorf("privileged port %d was not bound during root startup", port)
		}
		listener, err := net.Listen("tcp", net.JoinHostPort(strings.Trim(address, "[]"), portText))
		if err != nil {
			closeListeners(listeners)
			return nil, fmt.Errorf("bind server port %d: %w", port, err)
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}

func dropToServiceAccount(name, home string) error {
	account, err := user.Lookup(name)
	if err != nil {
		return fmt.Errorf("lookup service account %s: %w", name, err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}
	groups, err := account.GroupIds()
	if err != nil {
		return fmt.Errorf("lookup service groups: %w", err)
	}
	groupIDs := make([]int, 0, len(groups))
	for _, group := range groups {
		id, parseErr := strconv.Atoi(group)
		if parseErr != nil {
			return parseErr
		}
		groupIDs = append(groupIDs, id)
	}
	if err := unix.Setgroups(groupIDs); err != nil {
		return fmt.Errorf("set service supplementary groups: %w", err)
	}
	if err := unix.Setgid(gid); err != nil {
		return fmt.Errorf("set service group: %w", err)
	}
	if err := unix.Setuid(uid); err != nil {
		return fmt.Errorf("set service user: %w", err)
	}
	if os.Getuid() == 0 || os.Geteuid() == 0 {
		return fmt.Errorf("privilege drop did not succeed")
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USER", name)
	_ = os.Setenv("LOGNAME", name)
	return nil
}
