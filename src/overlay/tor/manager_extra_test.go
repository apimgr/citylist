package tor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cretz/bine/control"
)

func TestValidateOptions(t *testing.T) {
	base := DefaultOptions("config", "data", "logs")
	if err := validateOptions(base); err != nil {
		t.Fatalf("validateOptions(defaults) error: %v", err)
	}

	tests := []struct {
		name   string
		change func(*Options)
	}{
		{"missing config directory", func(o *Options) { o.ConfigDir = "" }},
		{"missing data directory", func(o *Options) { o.DataDir = "" }},
		{"missing log directory", func(o *Options) { o.LogDir = "" }},
		{"non-positive circuits", func(o *Options) { o.MaxCircuits = 0 }},
		{"non-positive circuit timeout", func(o *Options) { o.CircuitTimeout = 0 }},
		{"non-positive bootstrap timeout", func(o *Options) { o.BootstrapTimeout = 0 }},
		{"non-positive startup timeout", func(o *Options) { o.StartupTimeout = 0 }},
		{"intro points below range", func(o *Options) { o.NumIntroPoints = 2 }},
		{"intro points above range", func(o *Options) { o.NumIntroPoints = 11 }},
		{"non-positive streams", func(o *Options) { o.MaxStreamsPerCircuit = 0 }},
		{"virtual port below range", func(o *Options) { o.VirtualPort = 0 }},
		{"virtual port above range", func(o *Options) { o.VirtualPort = 65536 }},
		{"invalid rate token", func(o *Options) { o.BandwidthRate = "1 MB\nExitRelay 1" }},
		{"invalid burst token", func(o *Options) { o.BandwidthBurst = "unlimited" }},
		{"invalid monthly bandwidth", func(o *Options) { o.MaxMonthlyBandwidth = "unlimited\nExitRelay 1" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := base
			test.change(&options)
			if err := validateOptions(options); err == nil {
				t.Fatal("validateOptions() error = nil, want invalid settings error")
			}
		})
	}

	for _, allowed := range []string{"unlimited", "Unlimited", "0", "10 MB"} {
		options := base
		options.MaxMonthlyBandwidth = allowed
		if err := validateOptions(options); err != nil {
			t.Errorf("validateOptions(monthly=%q) error: %v", allowed, err)
		}
	}
}

func TestResolveBinaryConfiguredPath(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveBinary(executable)
	if err != nil || resolved == "" {
		t.Fatalf("resolveBinary(%q) = %q, %v", executable, resolved, err)
	}
	missing := filepath.Join(t.TempDir(), "missing-tor")
	if _, err := resolveBinary(missing); !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("resolveBinary(missing) error = %v, want ErrBinaryNotFound", err)
	}
}

func TestStartRejectsInvalidOptionsAndCloseIsIdempotent(t *testing.T) {
	options := DefaultOptions(t.TempDir(), t.TempDir(), t.TempDir())
	options.VirtualPort = 0
	if _, err := Start(context.Background(), options); err == nil {
		t.Fatal("Start() accepted invalid options")
	}

	options = DefaultOptions(t.TempDir(), t.TempDir(), t.TempDir())
	options.TorBinary = filepath.Join(t.TempDir(), "missing-tor")
	manager, err := Start(context.Background(), options)
	if err != nil {
		t.Fatalf("Start() with missing Tor binary error: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
	if got := manager.Snapshot(); got.Status != statusDisabled || got.Enabled || got.Running {
		t.Fatalf("closed disabled manager snapshot = %+v", got)
	}
}

func TestTorPathsAndGeneratedConfiguration(t *testing.T) {
	options := DefaultOptions(filepath.Join(t.TempDir(), `conf"dir`), t.TempDir(), t.TempDir())
	paths := torPaths(options)
	if paths.torrc != filepath.Join(options.ConfigDir, "tor", "torrc") || paths.hostname != filepath.Join(options.DataDir, "tor", "site", "hostname") {
		t.Fatalf("unexpected Tor paths: %+v", paths)
	}
	config := torrcConfig(options, paths, 64567)
	quotedControlPort := strings.ReplaceAll(paths.controlPort, `"`, `\"`)
	if !strings.Contains(config, `ControlPortWriteToFile "`+quotedControlPort+`"`) {
		t.Errorf("quoted control port path missing from torrc: %q", config)
	}
	if !strings.Contains(config, "AccountingMax 100 GB") || !strings.Contains(config, "SafeLogging 1") || !strings.Contains(config, "HiddenServiceMaxStreamsCloseCircuit 1") {
		t.Errorf("default hardening settings missing from torrc: %s", config)
	}
	options.SafeLogging = false
	options.CloseCircuitOnStreamLimit = false
	options.MaxMonthlyBandwidth = "unlimited"
	config = torrcConfig(options, paths, 64567)
	if strings.Contains(config, "AccountingMax") || !strings.Contains(config, "SafeLogging 0") || !strings.Contains(config, "HiddenServiceMaxStreamsCloseCircuit 0") {
		t.Errorf("disabled optional torrc settings were emitted incorrectly: %s", config)
	}
}

func TestWriteTorrcAndRejectsMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "torrc")
	if err := writeTorrc(path, "ControlPort 127.0.0.1:auto\n"); err != nil {
		t.Fatalf("writeTorrc() error: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "ControlPort 127.0.0.1:auto\n" {
		t.Fatalf("torrc contents = %q, %v", content, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("torrc permissions = %v, %v; want 0600", info, err)
		}
	}
	if err := writeTorrc(filepath.Join(t.TempDir(), "missing", "torrc"), "x\n"); err == nil {
		t.Fatal("writeTorrc() succeeded when its parent directory was absent")
	}
}

func TestWaitControlPortReturnsOnCancellationAndExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control_port")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitControlPort(ctx, nil, make(chan struct{}), path, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitControlPort(canceled) error = %v, want context.Canceled", err)
	}
	done := make(chan struct{})
	close(done)
	if _, err := waitControlPort(context.Background(), nil, done, path, time.Second); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("waitControlPort(exited) error = %v, want early-exit error", err)
	}
}

func TestWaitHostnameReturnsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitHostname(ctx, filepath.Join(t.TempDir(), "hostname")); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitHostname(canceled) error = %v, want context.Canceled", err)
	}
}

func TestPrepareDirectoriesCreatesPrivateRuntimeTree(t *testing.T) {
	options := DefaultOptions(t.TempDir(), t.TempDir(), t.TempDir())
	manager := &Manager{options: options}
	if err := manager.prepareDirectories(); err != nil {
		t.Fatalf("prepareDirectories() error: %v", err)
	}
	paths := torPaths(options)
	for _, directory := range []string{paths.configDir, paths.dataDir, paths.siteDir, options.LogDir} {
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			t.Fatalf("runtime directory %q stat = %v, %v", directory, info, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
			t.Errorf("runtime directory %q mode = %04o, want 0700", directory, info.Mode().Perm())
		}
	}
}

func TestWaitDescriptorExitsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager := &Manager{ctx: ctx, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	manager.waitDescriptor(make(chan control.Event))
}

func TestBootstrapStatusAndChildExitTransitions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := &Manager{logger: logger, restart: make(chan struct{}, 1)}
	manager.recordBootstrap("NOTICE BOOTSTRAP PROGRESS=42 TAG=one")
	if got := manager.Snapshot().BootstrapPercent; got != 42 {
		t.Fatalf("bootstrap percent = %d, want 42", got)
	}
	manager.recordBootstrap("NOTICE BOOTSTRAP PROGRESS=999 TAG=one")
	if got := manager.Snapshot().BootstrapPercent; got != 100 {
		t.Fatalf("bootstrap percent = %d, want cap at 100", got)
	}
	manager.recordBootstrap("NOTICE unrelated")
	if got := manager.Snapshot().BootstrapPercent; got != 100 {
		t.Fatalf("unrecognized bootstrap update changed percent to %d", got)
	}

	manager.setStatus(statusHealthy, true, strings.Repeat("a", 56)+".onion", 100, true, "")
	manager.childExited(&boundedBuffer{})
	if got := manager.Snapshot(); got.Status != statusUnavailable || got.Running {
		t.Fatalf("unexpected child-exit snapshot: %+v", got)
	}
	manager.closed = true
	manager.setStatus(statusHealthy, true, "", 100, false, "")
	manager.childExited(&boundedBuffer{})
	if got := manager.Snapshot(); got.Status != statusHealthy || got.Running {
		t.Fatalf("closed child should only clear running state: %+v", got)
	}
}

func TestBoundedBufferAndPublicHelpers(t *testing.T) {
	var buffer boundedBuffer
	written := strings.Repeat("x", 9000)
	n, err := buffer.Write([]byte(written))
	if err != nil || n != len(written) {
		t.Fatalf("boundedBuffer.Write() = %d, %v", n, err)
	}
	if got := buffer.String(); len(got) != 8192 || got != written[len(written)-8192:] {
		t.Fatalf("bounded buffer length/content = %d", len(got))
	}
	if safeDiagnostic(nil) != "" || safeDiagnostic(errors.New("secret-bearing error")) != "startup failed" {
		t.Fatal("safeDiagnostic() did not return a generic diagnostic")
	}
	if got := cleanTorToken(" 1 MB\r\n"); got != "1 MB" {
		t.Fatalf("cleanTorToken() = %q", got)
	}
	if err := stopProcess(nil, nil, nil); err != nil {
		t.Fatalf("stopProcess(nil) error: %v", err)
	}
	manager := &Manager{}
	client := manager.GetHTTPClient(false)
	if client.Timeout != 30*time.Second || client.Transport != nil {
		t.Fatalf("direct HTTP client = %+v", client)
	}
	client = manager.GetHTTPClient(true)
	if client.Timeout != 30*time.Second || client.Transport != nil {
		t.Fatalf("HTTP client without SOCKS dialer = %+v", client)
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	if id, ok := CircuitID(left); ok || id != "" {
		t.Fatalf("CircuitID(non-PROXY connection) = %q, %t", id, ok)
	}
}
