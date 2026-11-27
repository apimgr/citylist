// Package tor manages the app-owned Tor process and its isolated onion backend.
package tor

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cretz/bine/control"
	"github.com/pires/go-proxyproto"
	"golang.org/x/net/proxy"
)

const (
	statusDisabled    = "disabled"
	statusStarting    = "starting"
	statusHealthy     = "healthy"
	statusUnavailable = "unavailable"
	backendPortMin    = 64000
	backendPortMax    = 64999
)

var ErrBinaryNotFound = errors.New("tor binary not found")
var bandwidthPattern = regexp.MustCompile(`^[0-9]+(?:[[:space:]]+(?:B|KB|MB|GB|TB))?$`)

// Options supplies already-resolved application directories and validated Tor
// settings. Empty TorBinary enables normal binary discovery.
type Options struct {
	ConfigDir                 string
	DataDir                   string
	LogDir                    string
	TorBinary                 string
	UseNetwork                bool
	MaxCircuits               int
	StartupTimeout            time.Duration
	CircuitTimeout            time.Duration
	BootstrapTimeout          time.Duration
	SafeLogging               bool
	MaxStreamsPerCircuit      int
	CloseCircuitOnStreamLimit bool
	BandwidthRate             string
	BandwidthBurst            string
	MaxMonthlyBandwidth       string
	NumIntroPoints            int
	VirtualPort               int
	Logger                    *slog.Logger
}

// DefaultOptions returns the PART 31.1 defaults for application-owned paths.
func DefaultOptions(configDir, dataDir, logDir string) Options {
	return Options{
		ConfigDir: configDir, DataDir: dataDir, LogDir: logDir,
		MaxCircuits: 32, StartupTimeout: 60 * time.Second, CircuitTimeout: 60 * time.Second,
		BootstrapTimeout: 180 * time.Second, SafeLogging: true,
		MaxStreamsPerCircuit: 100, CloseCircuitOnStreamLimit: true,
		BandwidthRate: "1 MB", BandwidthBurst: "2 MB", MaxMonthlyBandwidth: "100 GB",
		NumIntroPoints: 3, VirtualPort: 80,
	}
}

// Snapshot is safe for status endpoints; it never includes paths or raw errors.
type Snapshot struct {
	Enabled            bool   `json:"enabled"`
	Running            bool   `json:"running"`
	Status             string `json:"status"`
	Version            string `json:"version"`
	Hostname           string `json:"hostname,omitempty"`
	BootstrapPercent   int    `json:"bootstrap_percent"`
	CircuitEstablished bool   `json:"circuit_established"`
	BackendPort        int    `json:"backend_port,omitempty"`
	PID                int    `json:"pid,omitempty"`
	Diagnostic         string `json:"diagnostic,omitempty"`
}

// Manager immediately returns after starting its bounded background bootstrap.
// Listener is a dedicated loopback listener that requires Tor's PROXY header.
type Manager struct {
	mu            sync.RWMutex
	options       Options
	logger        *slog.Logger
	snapshot      Snapshot
	listener      net.Listener
	backendPort   int
	process       *exec.Cmd
	processDone   chan struct{}
	control       *control.Conn
	controlCancel context.CancelFunc
	dialer        proxy.ContextDialer
	closed        bool
	closeOnce     sync.Once
	controlMu     sync.Mutex
	vanityMu      sync.RWMutex
	vanity        *vanitySearch
	ctx           context.Context
	cancel        context.CancelFunc
	restart       chan struct{}
}

// Start resolves Tor before allocating a backend port. A missing Tor binary is
// a normal disabled state and returns a manager with no listener.
func Start(ctx context.Context, options Options) (*Manager, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	logger := options.Logger.With("component", "tor")
	managerCtx, cancel := context.WithCancel(ctx)
	m := &Manager{options: options, logger: logger, ctx: managerCtx, cancel: cancel, restart: make(chan struct{}, 1)}
	m.snapshot.Status = statusStarting
	binary, err := resolveBinary(options.TorBinary)
	if err != nil {
		if errors.Is(err, ErrBinaryNotFound) {
			m.snapshot.Status = statusDisabled
			m.snapshot.Version = "unavailable"
			logger.Info("Tor binary not found, hidden service disabled")
			return m, nil
		}
		cancel()
		return nil, err
	}
	m.snapshot.Version = torBinaryVersion(ctx, binary)
	backend, port, err := bindBackend()
	if err != nil {
		cancel()
		return nil, err
	}
	m.listener = &proxyproto.Listener{Listener: backend, ReadHeaderTimeout: 5 * time.Second}
	m.backendPort = port
	m.snapshot.BackendPort = port
	m.snapshot.Enabled = true
	go m.run()
	return m, nil
}

// Snapshot returns a concurrency-safe copy of the current public health state.
func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

// Listener returns the currently-bound Tor-only listener, or nil until startup
// has allocated it. The returned listener is wrapped to parse PROXY v1/v2.
func (m *Manager) Listener() net.Listener {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.listener
}

// BackendPort is zero until Tor has been found and the dedicated listener bound.
func (m *Manager) BackendPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.backendPort
}

// CircuitID extracts Tor's opaque 64-bit circuit token from the PROXY header.
// It returns false for non-Tor addresses, including malformed headers.
func CircuitID(conn net.Conn) (string, bool) {
	proxied, ok := conn.(*proxyproto.Conn)
	if !ok {
		return "", false
	}
	address, ok := proxied.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return "", false
	}
	ip := address.IP.To16()
	if ip == nil || ip[0] != 0xfc {
		return "", false
	}
	return hex.EncodeToString(ip[8:16]), true
}

// GetHTTPClient returns a bounded direct client or a client using this manager's
// SOCKS5 dialer. Callers must only choose Tor for app-determined destinations.
func (m *Manager) GetHTTPClient(useTor bool) *http.Client {
	client := &http.Client{Timeout: 30 * time.Second}
	if !useTor {
		return client
	}
	m.mu.RLock()
	dialer := m.dialer
	m.mu.RUnlock()
	if dialer == nil {
		return client
	}
	client.Timeout = 60 * time.Second
	client.Transport = &http.Transport{DialContext: dialer.DialContext, ForceAttemptHTTP2: true}
	return client
}

// Close stops startup work, closes the backend, and shuts down the Tor child.
func (m *Manager) Close() error {
	var closeErr error
	m.StopVanitySearch()
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.cancel()
		listener, cmd, ctrl, done := m.listener, m.process, m.control, m.processDone
		cancelControl := m.controlCancel
		m.mu.Unlock()
		if cancelControl != nil {
			cancelControl()
		}
		if listener != nil {
			_ = listener.Close()
		}
		closeErr = stopProcess(cmd, ctrl, done)
		m.mu.Lock()
		m.snapshot.Running = false
		m.listener = nil
		m.backendPort = 0
		m.snapshot.BackendPort = 0
		m.mu.Unlock()
	})
	return closeErr
}

func (m *Manager) run() {
	for m.ctx.Err() == nil {
		if err := m.start(); err != nil {
			if m.ctx.Err() != nil {
				_ = m.Close()
				return
			}
			m.logger.Warn("Tor hidden service unavailable", "error", err)
			m.setStatus(statusUnavailable, false, "", m.Snapshot().BootstrapPercent, false, safeDiagnostic(err))
			select {
			case m.restart <- struct{}{}:
			default:
			}
		}
		m.mu.RLock()
		done := m.processDone
		m.mu.RUnlock()
		select {
		case <-m.ctx.Done():
			_ = m.Close()
			return
		case <-m.restart:
		case <-done:
			if m.ctx.Err() == nil {
				m.logger.Warn("Tor child exited; restarting")
			}
		}
		if m.ctx.Err() != nil {
			_ = m.Close()
			return
		}
		m.resetRuntime()
		select {
		case <-m.ctx.Done():
			_ = m.Close()
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (m *Manager) resetRuntime() {
	m.mu.Lock()
	listener, cmd, ctrl, done := m.listener, m.process, m.control, m.processDone
	cancelControl := m.controlCancel
	m.listener, m.process, m.processDone, m.control, m.controlCancel = nil, nil, nil, nil, nil
	m.dialer = nil
	m.backendPort = 0
	m.snapshot.BackendPort = 0
	m.snapshot.Running = false
	m.snapshot.Status = statusStarting
	m.snapshot.Hostname = ""
	m.snapshot.BootstrapPercent = 0
	m.snapshot.CircuitEstablished = false
	m.mu.Unlock()
	if cancelControl != nil {
		cancelControl()
	}
	_ = stopProcess(cmd, ctrl, done)
	if listener != nil {
		_ = listener.Close()
	}
	if m.ctx.Err() != nil {
		return
	}
	backend, port, err := bindBackend()
	if err != nil {
		m.setStatus(statusUnavailable, false, "", 0, false, "backend listener unavailable")
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = backend.Close()
		return
	}
	m.listener = &proxyproto.Listener{Listener: backend, ReadHeaderTimeout: 5 * time.Second}
	m.backendPort = port
	m.snapshot.BackendPort = port
	m.mu.Unlock()
}

func (m *Manager) start() error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	binary, err := resolveBinary(m.options.TorBinary)
	if err != nil {
		return err
	}
	if err := m.prepareDirectories(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return context.Canceled
	}
	listener := m.listener
	port := m.backendPort
	m.mu.Unlock()
	if listener == nil {
		backend, newPort, err := bindBackend()
		if err != nil {
			return fmt.Errorf("bind Tor backend: %w", err)
		}
		listener = &proxyproto.Listener{Listener: backend, ReadHeaderTimeout: 5 * time.Second}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			_ = listener.Close()
			return context.Canceled
		}
		m.listener, m.backendPort = listener, newPort
		m.snapshot.BackendPort = newPort
		m.mu.Unlock()
		port = newPort
	}

	paths := torPaths(m.options)
	if err := writeTorrc(paths.torrc, torrcConfig(m.options, paths, port)); err != nil {
		return err
	}
	if err := os.Remove(paths.controlPort); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale control port file: %w", err)
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	cmd := exec.Command(binary, "-f", paths.torrc)
	var stderr boundedBuffer
	if _, err := os.Stat(paths.logFile); err == nil {
		if err := enforcePrivate(paths.logFile, false); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Tor log: %w", err)
	}
	logFile, err := os.OpenFile(paths.logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open Tor log: %w", err)
	}
	if err := enforcePrivate(paths.logFile, false); err != nil {
		_ = logFile.Close()
		return err
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.MultiWriter(&stderr, logFile)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start tor child: %w", err)
	}
	done := make(chan struct{})
	m.mu.Lock()
	closed := m.closed
	m.process, m.processDone = cmd, done
	m.snapshot.PID = cmd.Process.Pid
	m.snapshot.Running = true
	m.mu.Unlock()
	go func() { _ = cmd.Wait(); _ = logFile.Close(); m.childExited(&stderr); close(done) }()
	if closed {
		_ = cmd.Process.Kill()
		return context.Canceled
	}

	controlAddress, err := waitControlPort(m.ctx, cmd, done, paths.controlPort, m.options.StartupTimeout)
	if err != nil {
		return fmt.Errorf("control port startup: %w; %s", err, stderr.String())
	}
	conn, err := dialControl(m.ctx, controlAddress)
	if err != nil {
		return fmt.Errorf("control connection: %w", err)
	}
	events := make(chan control.Event, 64)
	if err := conn.AddEventListener(events, control.EventCodeStatusClient, control.EventCodeHSDesc); err != nil {
		_ = conn.Close()
		return fmt.Errorf("subscribe Tor events: %w", err)
	}
	eventCtx, eventCancel := context.WithCancel(m.ctx)
	m.mu.Lock()
	m.control, m.controlCancel = conn, eventCancel
	m.mu.Unlock()
	go m.readEvents(eventCtx, conn, events)
	if err := conn.SetConf(control.NewKeyVal("DisableNetwork", "0")); err != nil {
		return fmt.Errorf("enable Tor network: %w", err)
	}
	bootCtx, bootCancel := context.WithTimeout(m.ctx, m.options.BootstrapTimeout)
	defer bootCancel()
	if err := waitBootstrap(bootCtx, conn, events, m); err != nil {
		return fmt.Errorf("tor bootstrap: %w", err)
	}
	hostname, err := waitHostname(bootCtx, paths.hostname)
	if err != nil {
		return fmt.Errorf("tor hostname: %w", err)
	}
	for _, file := range []string{paths.controlPort, filepath.Join(paths.dataDir, "control_auth_cookie"), filepath.Join(paths.dataDir, "tor.pid"), filepath.Join(paths.siteDir, "hs_ed25519_secret_key"), filepath.Join(paths.siteDir, "hs_ed25519_public_key"), paths.hostname} {
		if _, err := os.Stat(file); err == nil {
			if err := enforcePrivate(file, false); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect Tor runtime file: %w", err)
		}
	}
	m.mu.Lock()
	m.snapshot.Hostname = hostname
	m.snapshot.BootstrapPercent = 100
	m.snapshot.Status = statusHealthy
	m.snapshot.CircuitEstablished = hasBuiltCircuit(conn)
	m.mu.Unlock()
	if m.options.UseNetwork {
		m.initializeSOCKS(conn)
	}
	go m.waitDescriptor(events)
	m.logger.Info("Tor hidden service healthy", "onion_address", hostname, "backend_port", port)
	return nil
}

func hasBuiltCircuit(conn *control.Conn) bool {
	values, err := conn.GetInfo("circuit-status")
	if err != nil {
		return false
	}
	for _, value := range values {
		if strings.Contains(value.Val, " BUILT ") || strings.HasSuffix(value.Val, " BUILT") {
			return true
		}
	}
	return false
}

func (m *Manager) prepareDirectories() error {
	p := torPaths(m.options)
	for _, dir := range []string{p.configDir, p.dataDir, p.siteDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create Tor directory: %w", err)
		}
		if err := enforcePrivate(dir, true); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(m.options.LogDir, 0700); err != nil {
		return fmt.Errorf("create Tor log directory: %w", err)
	}
	if err := enforcePrivate(m.options.LogDir, true); err != nil {
		return err
	}
	for _, file := range []string{filepath.Join(p.dataDir, "control_port"), filepath.Join(p.dataDir, "control_auth_cookie"), filepath.Join(p.dataDir, "tor.pid"), filepath.Join(p.siteDir, "hs_ed25519_secret_key"), filepath.Join(p.siteDir, "hs_ed25519_public_key"), p.hostname} {
		if _, err := os.Stat(file); err == nil {
			if err := enforcePrivate(file, false); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect Tor runtime file: %w", err)
		}
	}
	return nil
}

func (m *Manager) readEvents(ctx context.Context, conn *control.Conn, events <-chan control.Event) {
	_ = events
	err := conn.HandleEvents(ctx)
	if ctx.Err() == nil && err != nil {
		m.logger.Warn("Tor control event stream lost", "error", err)
		m.setStatus(statusUnavailable, false, "", m.Snapshot().BootstrapPercent, false, "control connection lost")
		select {
		case m.restart <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) waitDescriptor(events <-chan control.Event) {
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if hs, ok := event.(*control.HSDescEvent); ok && hs.Action == "UPLOADED" {
				return
			}
		case <-timer.C:
			m.logger.Warn("Tor hidden service descriptor upload timed out")
			return
		case <-m.ctx.Done():
			return
		}
	}
}

func (m *Manager) initializeSOCKS(conn *control.Conn) {
	values, err := conn.GetInfo("net/listeners/socks")
	if err != nil || len(values) == 0 {
		m.logger.Warn("Tor SOCKS listener unavailable")
		return
	}
	address := strings.Trim(strings.TrimSpace(values[0].Val), "\"")
	if host, _, err := net.SplitHostPort(address); err != nil || host != "127.0.0.1" {
		m.logger.Warn("Tor SOCKS listener address rejected")
		return
	}
	dialer, err := proxy.SOCKS5("tcp", address, nil, proxy.Direct)
	if err != nil {
		m.logger.Warn("Tor SOCKS dialer unavailable", "error", err)
		return
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		m.logger.Warn("Tor SOCKS dialer does not support context")
		return
	}
	m.mu.Lock()
	m.dialer = contextDialer
	m.mu.Unlock()
}

func (m *Manager) childExited(stderr *boundedBuffer) {
	m.mu.Lock()
	closed := m.closed
	m.snapshot.Running = false
	if !closed && m.snapshot.Status == statusHealthy {
		m.snapshot.Status = statusUnavailable
	}
	m.mu.Unlock()
	if !closed {
		m.logger.Warn("Tor child exited", "diagnostic", safeDiagnostic(errors.New(stderr.String())))
	}
}

func (m *Manager) setStatus(status string, running bool, hostname string, bootstrap int, circuit bool, diagnostic string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshot.Status = status
	m.snapshot.Running = running
	m.snapshot.Hostname = hostname
	m.snapshot.BootstrapPercent = bootstrap
	m.snapshot.CircuitEstablished = circuit
	m.snapshot.Diagnostic = diagnostic
}

type paths struct{ configDir, dataDir, siteDir, torrc, controlPort, hostname, logFile string }

func torPaths(options Options) paths {
	data := filepath.Join(options.DataDir, "tor")
	return paths{configDir: filepath.Join(options.ConfigDir, "tor"), dataDir: data,
		siteDir: filepath.Join(data, "site"), torrc: filepath.Join(options.ConfigDir, "tor", "torrc"),
		controlPort: filepath.Join(data, "control_port"), hostname: filepath.Join(data, "site", "hostname"),
		logFile: filepath.Join(options.LogDir, "tor.log")}
}

func torrcConfig(o Options, p paths, backendPort int) string {
	socks := "SocksPort 0"
	if o.UseNetwork {
		socks = "SocksPort 127.0.0.1:auto\nSocksPolicy accept 127.0.0.1"
	}
	safe := 0
	if o.SafeLogging {
		safe = 1
	}
	closeCircuit := 0
	if o.CloseCircuitOnStreamLimit {
		closeCircuit = 1
	}
	accounting := ""
	if o.MaxMonthlyBandwidth != "" && !strings.EqualFold(o.MaxMonthlyBandwidth, "unlimited") {
		accounting = fmt.Sprintf("AccountingStart month 1 00:00\nAccountingMax %s\n", cleanTorToken(o.MaxMonthlyBandwidth))
	}
	return fmt.Sprintf("DataDirectory %s\nControlPort 127.0.0.1:auto\nControlPortWriteToFile %s\nCookieAuthentication 1\nDisableNetwork 1\n__OwningControllerProcess %d\n%s\nSafeLogging %d\nMaxCircuitDirtiness 600\nMaxClientCircuitsPending %d\nLearnCircuitBuildTimeout 0\nCircuitBuildTimeout %d\nBandwidthRate %s\nBandwidthBurst %s\n%sExitRelay 0\nExitPolicy reject *:*\nORPort 0\nDirPort 0\nPublishServerDescriptor 0\nVanguardsLiteEnabled 1\nHiddenServiceSingleHopMode 0\nFetchDirInfoEarly 1\nFetchDirInfoExtraEarly 1\nDisableDebuggerAttachment 1\nHiddenServiceDir %s\nHiddenServiceVersion 3\nHiddenServiceNumIntroductionPoints %d\nHiddenServiceMaxStreams %d\nHiddenServiceMaxStreamsCloseCircuit %d\nHiddenServicePort %d 127.0.0.1:%d\nHiddenServiceExportCircuitID haproxy\n",
		quoteTor(p.dataDir), quoteTor(p.controlPort), os.Getpid(), socks, safe,
		o.MaxCircuits, int(o.CircuitTimeout.Seconds()), cleanTorToken(o.BandwidthRate),
		cleanTorToken(o.BandwidthBurst), accounting, quoteTor(p.siteDir), o.NumIntroPoints,
		o.MaxStreamsPerCircuit, closeCircuit, o.VirtualPort, backendPort)
}

func writeTorrc(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		if err := enforcePrivate(path, false); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Tor configuration: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("write Tor configuration: %w", err)
	}
	if _, err = io.WriteString(file, content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write Tor configuration: %w", err)
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync Tor configuration: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("close Tor configuration: %w", err)
	}
	return enforcePrivate(path, false)
}

func validateOptions(o Options) error {
	if o.ConfigDir == "" || o.DataDir == "" || o.LogDir == "" {
		return errors.New("tor config, data, and log directories are required")
	}
	if o.MaxCircuits <= 0 || o.CircuitTimeout <= 0 || o.BootstrapTimeout <= 0 || o.StartupTimeout <= 0 || o.NumIntroPoints < 3 || o.NumIntroPoints > 10 || o.MaxStreamsPerCircuit <= 0 || o.VirtualPort < 1 || o.VirtualPort > 65535 {
		return errors.New("invalid Tor settings")
	}
	for _, token := range []string{o.BandwidthRate, o.BandwidthBurst} {
		if !bandwidthPattern.MatchString(token) {
			return errors.New("invalid Tor bandwidth setting")
		}
	}
	if o.MaxMonthlyBandwidth != "" && !strings.EqualFold(o.MaxMonthlyBandwidth, "unlimited") && !bandwidthPattern.MatchString(o.MaxMonthlyBandwidth) {
		return errors.New("invalid Tor monthly bandwidth setting")
	}
	return nil
}

func resolveBinary(configured string) (string, error) {
	if configured != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("%w: configured Tor binary unavailable", ErrBinaryNotFound)
		}
		return path, nil
	}
	locations := []string{"/usr/bin/tor", "/usr/local/bin/tor", "/opt/homebrew/bin/tor"}
	if runtime.GOOS == "windows" {
		locations = append(locations, `C:\Program Files\Tor\tor.exe`)
	}
	for _, location := range locations {
		if info, err := os.Stat(location); err == nil && !info.IsDir() {
			return location, nil
		}
	}
	if path, err := exec.LookPath("tor"); err == nil {
		return path, nil
	}
	return "", ErrBinaryNotFound
}

func bindBackend() (net.Listener, int, error) {
	for attempt := 0; attempt < 64; attempt++ {
		seed := time.Now().UnixNano() + int64(attempt*7919)
		port := backendPortMin + int(seed%int64(backendPortMax-backendPortMin+1))
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			return listener, port, nil
		}
	}
	return nil, 0, errors.New("no available Tor backend port in 64000-64999")
}

var controlPortPattern = regexp.MustCompile(`^PORT=(127\.0\.0\.1:[0-9]{1,5})\n$`)

func waitControlPort(ctx context.Context, cmd *exec.Cmd, done <-chan struct{}, file string, timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return "", errors.New("tor exited before publishing control port")
		default:
		}
		if data, err := os.ReadFile(file); err == nil {
			if match := controlPortPattern.FindSubmatch(data); match != nil {
				address := string(match[1])
				_, rawPort, splitErr := net.SplitHostPort(address)
				port, parseErr := strconv.Atoi(rawPort)
				if splitErr == nil && parseErr == nil && port > 0 && port <= 65535 {
					return address, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", errors.New("tor control port startup timed out")
		case <-done:
			return "", errors.New("tor exited before publishing control port")
		case <-ticker.C:
		}
	}
}

func dialControl(ctx context.Context, address string) (*control.Conn, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	controlConn := control.NewConn(textproto.NewConn(conn))
	if err := controlConn.Authenticate(""); err != nil {
		_ = controlConn.Close()
		return nil, fmt.Errorf("control authentication: %w", err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = controlConn.Close()
		return nil, err
	}
	return controlConn, nil
}

func waitBootstrap(ctx context.Context, conn *control.Conn, events <-chan control.Event, manager *Manager) error {
	values, _ := conn.GetInfo("status/bootstrap-phase")
	if len(values) > 0 {
		manager.recordBootstrap(values[0].Val)
	}
	for {
		if manager.Snapshot().BootstrapPercent >= 100 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-events:
			status, ok := event.(*control.StatusEvent)
			if !ok || status.Type != control.EventCodeStatusClient {
				continue
			}
			manager.recordBootstrap(status.Raw)
			if strings.Contains(status.Raw, "BOOTSTRAP PROGRESS=100") {
				return nil
			}
		}
	}
}

var progressPattern = regexp.MustCompile(`PROGRESS=([0-9]{1,3})`)

func (m *Manager) recordBootstrap(raw string) {
	match := progressPattern.FindStringSubmatch(raw)
	if match == nil {
		return
	}
	progress, _ := strconv.Atoi(match[1])
	if progress > 100 {
		progress = 100
	}
	m.mu.Lock()
	m.snapshot.BootstrapPercent = progress
	m.mu.Unlock()
}

func waitHostname(ctx context.Context, file string) (string, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if data, err := os.ReadFile(file); err == nil {
			name := strings.TrimSpace(string(data))
			if validHostname(name) {
				return name, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func validHostname(value string) bool {
	if !strings.HasSuffix(value, ".onion") {
		return false
	}
	label := strings.TrimSuffix(value, ".onion")
	if len(label) != 56 {
		return false
	}
	for _, char := range label {
		if (char < 'a' || char > 'z') && (char < '2' || char > '7') {
			return false
		}
	}
	return true
}

func stopProcess(cmd *exec.Cmd, conn *control.Conn, done <-chan struct{}) error {
	if conn != nil {
		_ = conn.Signal("SHUTDOWN")
		_ = conn.Close()
	}
	if cmd == nil || cmd.Process == nil || done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
	}
	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
	} else {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
	}
	_ = cmd.Process.Kill()
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("tor child did not exit after kill")
	}
}

func enforcePrivate(path string, directory bool) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect private Tor path: %w", err)
	}
	if info.IsDir() != directory {
		return errors.New("tor path has an unexpected type")
	}
	if info.Mode().Perm() != mode.Perm() {
		if err := os.Chmod(path, mode); err != nil {
			if info.Mode().Perm()&0077 != 0 {
				return fmt.Errorf("cannot secure Tor path: %w", err)
			}
		}
	}
	info, err = os.Stat(path)
	if err != nil {
		return fmt.Errorf("verify private Tor path: %w", err)
	}
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("tor path is not private")
	}
	if info.IsDir() {
		probe, err := os.CreateTemp(path, ".probe-*")
		if err != nil {
			return fmt.Errorf("tor directory is not writable by the server: %w", err)
		}
		probePath := probe.Name()
		closeErr := probe.Close()
		removeErr := os.Remove(probePath)
		if err := errors.Join(closeErr, removeErr); err != nil {
			return fmt.Errorf("verify Tor directory write access: %w", err)
		}
		return nil
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("tor file is not usable by the server: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Tor file after permission check: %w", err)
	}
	return nil
}

func quoteTor(path string) string { return `"` + strings.ReplaceAll(path, `"`, `\"`) + `"` }
func cleanTorToken(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\n", ""), "\r", ""))
}
func safeDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	return "startup failed"
}

type boundedBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > 8192 {
		b.data = append([]byte(nil), b.data[len(b.data)-8192:]...)
	}
	return len(p), nil
}
func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.data))
}
