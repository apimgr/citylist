package tor

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDefaultOptionsAndTorrcHardening(t *testing.T) {
	options := DefaultOptions(t.TempDir(), t.TempDir(), t.TempDir())
	if options.MaxCircuits != 32 || options.StartupTimeout != 60*time.Second || options.VirtualPort != 80 {
		t.Fatalf("unexpected Tor defaults: %+v", options)
	}
	paths := torPaths(options)
	content := torrcConfig(options, paths, 64567)
	for _, required := range []string{
		"ControlPort 127.0.0.1:auto", "ExitRelay 0", "ExitPolicy reject *:*", "ORPort 0",
		"DirPort 0", "PublishServerDescriptor 0", "SocksPort 0",
		"HiddenServicePort 80 127.0.0.1:64567", "HiddenServiceExportCircuitID haproxy",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("generated torrc omitted %q", required)
		}
	}
	options.UseNetwork = true
	content = torrcConfig(options, paths, 64567)
	if !strings.Contains(content, "SocksPort 127.0.0.1:auto\nSocksPolicy accept 127.0.0.1") {
		t.Fatal("outbound SOCKS listener is not restricted to loopback")
	}
}

func TestTorHostnameValidation(t *testing.T) {
	valid := strings.Repeat("a", 56) + ".onion"
	if !validHostname(valid) {
		t.Fatalf("valid v3 onion hostname rejected")
	}
	for _, value := range []string{"short.onion", strings.Repeat("0", 56) + ".onion", strings.Repeat("A", 56) + ".onion", strings.Repeat("a", 56) + ".onion.example"} {
		if validHostname(value) {
			t.Errorf("invalid onion hostname accepted: %q", value)
		}
	}
}

func TestControlPortFileMustBeLoopbackAndComplete(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "control_port")
	if err := os.WriteFile(path, []byte("PORT=127.0.0.1:12345\n"), 0600); err != nil {
		t.Fatal(err)
	}
	address, err := waitControlPort(context.Background(), nil, make(chan struct{}), path, time.Second)
	if err != nil || address != "127.0.0.1:12345" {
		t.Fatalf("waitControlPort() = %q, %v", address, err)
	}
	for _, invalid := range []string{"PORT=0.0.0.0:12345\n", "PORT=127.0.0.1:0\n", "PORT=127.0.0.1:12345", "PORT=127.0.0.1:65536\n"} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		_, err := waitControlPort(ctx, nil, make(chan struct{}), path, 20*time.Millisecond)
		cancel()
		if err == nil {
			t.Errorf("invalid control port content accepted: %q", invalid)
		}
	}
}

func TestTorBackendIsDedicatedLoopbackAndUsesRandomRange(t *testing.T) {
	listener, port, err := bindBackend()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if port < backendPortMin || port > backendPortMax {
		t.Fatalf("backend port %d outside required range", port)
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() {
		t.Fatalf("backend listener is not loopback: %v", listener.Addr())
	}
}

func TestEnforcePrivateFixesModesAndChecksWriteAccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses inherited ACLs")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "tor")
	if err := os.Mkdir(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := enforcePrivate(directory, true); err != nil {
		t.Fatalf("enforcePrivate(directory) error: %v", err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("directory mode = %v; want 0700", info.Mode().Perm())
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("directory write probe was not removed: entries=%v err=%v", entries, err)
	}

	filePath := filepath.Join(root, "torrc")
	if err := os.WriteFile(filePath, []byte("ControlPort 127.0.0.1:auto\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := enforcePrivate(filePath, false); err != nil {
		t.Fatalf("enforcePrivate(file) error: %v", err)
	}
	info, err = os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file mode = %v; want 0600", info.Mode().Perm())
	}
}

func TestEnforcePrivateRejectsUnexpectedPathTypes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses inherited ACLs")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(root, "file")
	if err := os.WriteFile(filePath, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path      string
		directory bool
	}{{directory, false}, {filePath, true}} {
		if err := enforcePrivate(test.path, test.directory); err == nil {
			t.Errorf("enforcePrivate(%q, %t) accepted the wrong path type", test.path, test.directory)
		}
	}
}

func TestStartWithoutTorBinaryIsDisabledAndAllocatesNoBackend(t *testing.T) {
	options := DefaultOptions(t.TempDir(), t.TempDir(), t.TempDir())
	options.TorBinary = filepath.Join(t.TempDir(), "missing-tor")
	manager, err := Start(context.Background(), options)
	if err != nil {
		t.Fatalf("Start() without Tor binary error: %v", err)
	}
	defer func() { _ = manager.Close() }()
	if snapshot := manager.Snapshot(); snapshot.Enabled || snapshot.Running || snapshot.Status != statusDisabled {
		t.Fatalf("missing Tor binary snapshot = %+v", snapshot)
	}
	if manager.BackendPort() != 0 || manager.Listener() != nil {
		t.Fatal("missing Tor binary allocated a backend listener")
	}
}
