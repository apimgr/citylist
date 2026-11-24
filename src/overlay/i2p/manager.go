package i2p

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// State is the detailed lifecycle state used by operators and diagnostics.
type State string

const (
	StateDisabled   State = "disabled"
	StateNoProvider State = "no_provider"
	StateStarting   State = "starting"
	StateBuilding   State = "building"
	StateRunning    State = "running"
	StateError      State = "error"
)

// Options contains the I2P settings needed by the manager. Enabled is resolved
// by the application from its config, environment, flags, and container state.
type Options struct {
	Enabled          bool
	Binary           string
	SAMAddress       string
	VirtualPort      int
	InboundLength    int
	OutboundLength   int
	InboundQuantity  int
	OutboundQuantity int
	SignatureType    int
	BootstrapTimeout time.Duration
	ConfigDir        string
	DataDir          string
	LogDir           string
	Handler          http.Handler
	Logger           *slog.Logger
}

// DefaultOptions returns the base values from AI.md. The caller computes the
// container-dependent Enabled value before calling Start.
func DefaultOptions() Options {
	return Options{
		SAMAddress:       "127.0.0.1:7656",
		VirtualPort:      80,
		InboundLength:    3,
		OutboundLength:   3,
		InboundQuantity:  5,
		OutboundQuantity: 5,
		SignatureType:    7,
		BootstrapTimeout: 300 * time.Second,
	}
}

// Snapshot is a point-in-time view. Status is safe to map to public health;
// Diagnostic is for local operator output only and must never enter responses.
type Snapshot struct {
	Enabled    bool
	Running    bool
	Status     string
	State      State
	Hostname   string
	Provider   string
	Diagnostic string `json:"-"`
}

// Manager owns the provider process/session and its dedicated HTTP backend.
type Manager struct {
	mu         sync.RWMutex
	opts       Options
	state      State
	enabled    bool
	provider   string
	address    string
	diagnostic string
	service    *service
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	log        *slog.Logger
}

const providerProbeInterval = 30 * time.Second

// Start creates a manager and returns immediately. Provider detection,
// destination creation, and tunnel building continue in the background.
func Start(ctx context.Context, opts Options) (*Manager, error) {
	if ctx == nil {
		return nil, errors.New("i2p context is required")
	}
	if !opts.Enabled {
		return &Manager{opts: opts, state: StateDisabled, provider: "none", done: closedChannel()}, nil
	}
	if err := validateOptions(&opts); err != nil {
		return nil, err
	}
	if opts.Handler == nil {
		return nil, errors.New("i2p backend HTTP handler is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	managerCtx, cancel := context.WithCancel(ctx)
	m := &Manager{
		opts:     opts,
		state:    StateStarting,
		provider: "none",
		ctx:      managerCtx,
		cancel:   cancel,
		done:     make(chan struct{}),
		log:      opts.Logger,
	}
	go m.run()
	return m, nil
}

// Snapshot returns a safe, normalized status and local diagnostic details.
func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := "disabled"
	if m.enabled {
		switch m.state {
		case StateRunning:
			status = "healthy"
		case StateError:
			status = "unavailable"
		default:
			status = "starting"
		}
	}
	hostname := ""
	if m.state == StateRunning {
		hostname = m.address
	}
	return Snapshot{
		Enabled:    m.enabled,
		Running:    m.state == StateRunning,
		Status:     status,
		State:      m.state,
		Hostname:   hostname,
		Provider:   providerOrNone(m.provider),
		Diagnostic: m.diagnostic,
	}
}

// Close stops the provider, backend HTTP server, and retry loop.
func (m *Manager) Close() error {
	if m.cancel != nil {
		m.cancel()
		<-m.done
	}
	return nil
}

func (m *Manager) run() {
	defer func() {
		m.setStopped()
		close(m.done)
	}()
	var backoff = 5 * time.Second
	noProviderLogged := false
	for m.ctx.Err() == nil {
		provider, binary, err := resolveProvider(m.ctx, m.opts)
		if err != nil {
			m.setNoProvider(err)
			if !noProviderLogged {
				m.log.Warn("I2P provider is unavailable", "error", err)
				noProviderLogged = true
			}
			if !waitContext(m.ctx, providerProbeInterval) {
				return
			}
			continue
		}
		noProviderLogged = false
		m.setProvider(provider, StateStarting, "")
		svc, err := startService(m.ctx, m.opts, provider, binary, func(state State) {
			m.setProvider(provider, state, "")
		})
		if err != nil {
			if m.ctx.Err() != nil {
				return
			}
			m.setFailure(provider, err)
			m.log.Warn("I2P startup failed", "error", err)
			delay := backoff
			var retry retryAfterError
			if errors.As(err, &retry) && retry.after > delay {
				delay = retry.after
			}
			if !waitContext(m.ctx, delay) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		backoff = 5 * time.Second
		m.setService(provider, svc)
		m.log.Info("I2P eepsite is running", "provider", provider, "hostname", svc.address)
		lost := m.monitor(svc)
		svc.close()
		m.clearService()
		if m.ctx.Err() != nil {
			return
		}
		m.setFailure(provider, lost)
		m.log.Warn("I2P provider stopped; retrying", "error", lost)
		if !waitContext(m.ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

func (m *Manager) monitor(svc *service) error {
	ticker := time.NewTicker(providerProbeInterval)
	defer ticker.Stop()
	probeFailures := 0
	for {
		select {
		case <-m.ctx.Done():
			return nil
		case <-svc.childExited:
			if svc.provider == "i2pd" {
				return errors.New("managed i2pd process exited")
			}
		case err := <-svc.failed:
			if err != nil {
				return err
			}
		case <-ticker.C:
			if svc.provider == "i2pd" {
				select {
				case <-svc.childExited:
					return errors.New("managed i2pd process exited")
				default:
				}
			} else if err := probeSAM(m.ctx, svc.samAddress); err != nil {
				probeFailures++
				if probeFailures >= 3 {
					return fmt.Errorf("external SAM bridge is unresponsive: %w", err)
				}
			} else {
				probeFailures = 0
			}
		}
	}
}

func (m *Manager) setNoProvider(err error) {
	m.mu.Lock()
	m.enabled = false
	m.provider = "none"
	m.state = StateNoProvider
	m.address = ""
	m.diagnostic = err.Error()
	m.mu.Unlock()
}

func (m *Manager) setProvider(provider string, state State, diagnostic string) {
	m.mu.Lock()
	m.enabled = true
	m.provider = provider
	m.state = state
	m.address = ""
	m.diagnostic = diagnostic
	m.mu.Unlock()
}

func (m *Manager) setService(provider string, svc *service) {
	m.mu.Lock()
	m.enabled = true
	m.provider = provider
	m.service = svc
	m.address = svc.address
	m.state = StateRunning
	m.diagnostic = ""
	m.mu.Unlock()
}

func (m *Manager) setFailure(provider string, err error) {
	m.mu.Lock()
	m.enabled = true
	m.provider = provider
	m.state = StateError
	m.address = ""
	if err != nil {
		m.diagnostic = err.Error()
	} else {
		m.diagnostic = "I2P provider stopped"
	}
	m.mu.Unlock()
}

func (m *Manager) clearService() {
	m.mu.Lock()
	m.service = nil
	m.address = ""
	m.mu.Unlock()
}

func (m *Manager) setStopped() {
	m.mu.Lock()
	m.enabled = false
	m.state = StateDisabled
	m.address = ""
	m.mu.Unlock()
}

func validateOptions(opts *Options) error {
	if opts.SAMAddress == "" {
		return errors.New("i2p SAM bridge address is required")
	}
	if opts.VirtualPort < 1 || opts.VirtualPort > 65535 {
		return errors.New("i2p virtual port must be between 1 and 65535")
	}
	if opts.InboundLength < 0 || opts.InboundLength > 7 || opts.OutboundLength < 0 || opts.OutboundLength > 7 {
		return errors.New("i2p tunnel lengths must be between 0 and 7")
	}
	if opts.InboundQuantity < 1 || opts.InboundQuantity > 16 || opts.OutboundQuantity < 1 || opts.OutboundQuantity > 16 {
		return errors.New("i2p tunnel quantities must be between 1 and 16")
	}
	if opts.BootstrapTimeout < 30*time.Second || opts.BootstrapTimeout > 600*time.Second {
		return errors.New("i2p bootstrap timeout must be between 30 and 600 seconds")
	}
	if opts.DataDir == "" {
		return errors.New("i2p data directory is required")
	}
	if opts.Binary != "" && strings.ContainsAny(opts.Binary, "\r\n\x00") {
		return errors.New("i2pd binary path contains invalid characters")
	}
	if strings.ContainsAny(opts.SAMAddress, "\r\n\x00") {
		return errors.New("i2p SAM address contains invalid characters")
	}
	if opts.ConfigDir != "" && strings.ContainsAny(opts.ConfigDir, "\r\n\x00") {
		return errors.New("i2p config directory contains invalid characters")
	}
	if opts.LogDir != "" && strings.ContainsAny(opts.LogDir, "\r\n\x00") {
		return errors.New("i2p log directory contains invalid characters")
	}
	if opts.ConfigDir == "" || opts.LogDir == "" {
		return errors.New("i2pd config and log directories are required when I2P is enabled")
	}
	return nil
}

func nextBackoff(current time.Duration) time.Duration {
	if current >= 150*time.Second {
		return 300 * time.Second
	}
	return current * 2
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func providerOrNone(provider string) string {
	if provider == "" {
		return "none"
	}
	return provider
}

func closedChannel() chan struct{} {
	closed := make(chan struct{})
	close(closed)
	return closed
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0700); err != nil {
			info, statErr := os.Stat(path)
			if statErr != nil || info.Mode().Perm()&0077 != 0 {
				return err
			}
		}
	}
	probe, err := os.CreateTemp(path, ".citylist-write-check-*")
	if err != nil {
		return fmt.Errorf("directory is not writable: %w", err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func ensureRuntimeDirectories(opts Options, provider string) error {
	dataDir := filepath.Join(opts.DataDir, "i2p")
	if err := ensurePrivateDirectory(dataDir); err != nil {
		return fmt.Errorf("prepare I2P data directory: %w", err)
	}
	if err := ensurePrivateDirectory(filepath.Join(dataDir, "site")); err != nil {
		return fmt.Errorf("prepare I2P site directory: %w", err)
	}
	if provider != "i2pd" {
		return nil
	}
	for _, dir := range []string{filepath.Join(opts.ConfigDir, "i2p"), filepath.Join(opts.LogDir)} {
		if err := ensurePrivateDirectory(dir); err != nil {
			return fmt.Errorf("prepare I2P directory: %w", err)
		}
	}
	return nil
}
