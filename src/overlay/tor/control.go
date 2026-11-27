package tor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha3"
	"crypto/sha512"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"filippo.io/edwards25519"
)

const torSecretHeader = "== ed25519v1-secret: type0 ==\n"
const torPublicHeader = "== ed25519v1-public: type0 ==\n"

var vanityPrefixPattern = regexp.MustCompile(`^[a-z2-7]{1,6}$`)

var ErrCandidateNotFound = errors.New("tor vanity candidate not found")
var ErrCandidateAmbiguous = errors.New("tor vanity candidate prefix is ambiguous")
var ErrInvalidIdentity = errors.New("invalid Tor identity")

type VanityProgress struct {
	State          string   `json:"state"`
	Prefix         string   `json:"prefix,omitempty"`
	Workers        int      `json:"workers,omitempty"`
	Attempts       uint64   `json:"attempts"`
	Rate           float64  `json:"rate"`
	ElapsedSeconds float64  `json:"elapsed_seconds"`
	Candidates     []string `json:"candidates"`
}

type vanitySearch struct {
	prefix   string
	workers  int
	started  time.Time
	attempts atomic.Uint64
	cancel   context.CancelFunc
}

// Validate reports whether the options accepted by this manager remain valid.
func (m *Manager) Validate() error {
	if m == nil {
		return errors.New("tor manager is unavailable")
	}
	return ValidateConfig(m.options)
}

// ValidateConfig checks effective settings and the persisted torrc without
// starting the managed Tor process or allocating a backend listener.
func ValidateConfig(options Options) error {
	if err := validateOptions(options); err != nil {
		return err
	}
	configPath := torPaths(options).torrc
	info, err := os.Lstat(configPath)
	if err != nil {
		return fmt.Errorf("inspect Tor configuration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > 1<<20 {
		return errors.New("tor configuration file is invalid")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return errors.New("tor configuration file is not private")
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read Tor configuration: %w", err)
	}
	if !strings.Contains(string(content), "DataDirectory ") || !strings.Contains(string(content), "HiddenServiceDir ") {
		return errors.New("tor configuration is missing required hidden-service directives")
	}
	binary, err := resolveBinary(options.TorBinary)
	if errors.Is(err, ErrBinaryNotFound) {
		if options.TorBinary == "" {
			return nil
		}
		return errors.New("configured Tor binary is unavailable")
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "--verify-config", "-f", configPath)
	var output boundedBuffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("tor configuration validation timed out")
		}
		return errors.New("tor rejected its configuration")
	}
	return nil
}

// ReadOnionAddress returns only a validated hostname from the private live-site directory.
func ReadOnionAddress(dataDir string) (string, error) {
	directory := filepath.Join(dataDir, "tor", "site")
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && directoryInfo.Mode().Perm()&0077 != 0 {
		return "", errors.New("tor site directory is not a private directory")
	}
	path := filepath.Join(directory, "hostname")
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > 256 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return "", errors.New("tor hostname file is invalid or not private")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	address := strings.TrimSpace(string(contents))
	if !validHostname(address) {
		return "", errors.New("tor hostname file does not contain a valid onion address")
	}
	return address, nil
}

// VanityStatus returns progress without exposing candidate key material.
func (m *Manager) VanityStatus() VanityProgress {
	if m == nil {
		return VanityProgress{State: "idle", Candidates: []string{}}
	}
	candidates := m.candidateAddresses()
	m.vanityMu.RLock()
	search := m.vanity
	if search == nil {
		m.vanityMu.RUnlock()
		state := "idle"
		if len(candidates) > 0 {
			state = "found"
		}
		return VanityProgress{State: state, Candidates: candidates}
	}
	progress := VanityProgress{
		State: "running", Prefix: search.prefix, Workers: search.workers,
		Attempts: search.attempts.Load(), Candidates: candidates,
		ElapsedSeconds: time.Since(search.started).Seconds(),
	}
	m.vanityMu.RUnlock()
	if progress.ElapsedSeconds > 0 {
		progress.Rate = float64(progress.Attempts) / progress.ElapsedSeconds
	}
	return progress
}

// StartVanitySearch starts one bounded worker pool and persists only completed candidates.
func (m *Manager) StartVanitySearch(prefix string, workers int) error {
	if m == nil || !m.Snapshot().Enabled {
		return errors.New("tor hidden service is not running")
	}
	prefix = strings.TrimSpace(prefix)
	if !vanityPrefixPattern.MatchString(prefix) {
		return errors.New("prefix must contain 1 to 6 lowercase base32 characters (a-z, 2-7)")
	}
	if workers == 0 {
		workers = runtime.NumCPU() - 1
		if workers < 1 {
			workers = 1
		}
	}
	if workers < 1 || workers > runtime.NumCPU() {
		return fmt.Errorf("workers must be between 1 and %d", runtime.NumCPU())
	}
	m.vanityMu.Lock()
	if m.vanity != nil {
		existing := m.vanity.prefix
		m.vanityMu.Unlock()
		return fmt.Errorf("a vanity search for %q is already running", existing)
	}
	ctx, cancel := context.WithCancel(m.ctx)
	search := &vanitySearch{prefix: prefix, workers: workers, started: time.Now(), cancel: cancel}
	m.vanity = search
	m.vanityMu.Unlock()
	for range workers {
		go m.searchVanity(ctx, search)
	}
	return nil
}

// StopVanitySearch cancels the active search while retaining completed candidates.
func (m *Manager) StopVanitySearch() []string {
	if m == nil {
		return []string{}
	}
	m.vanityMu.Lock()
	if m.vanity != nil {
		m.vanity.cancel()
		m.vanity = nil
	}
	m.vanityMu.Unlock()
	return m.candidateAddresses()
}

// Restart asks the manager's owner loop to restart Tor and waits for a healthy new process.
func (m *Manager) Restart(ctx context.Context) error {
	if m == nil || !m.Snapshot().Enabled {
		return errors.New("tor hidden service is not running")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.controlMu.Lock()
	defer m.controlMu.Unlock()
	return m.restartLocked(ctx)
}

// Regenerate replaces the hidden-service identity with a new random v3 keypair.
func (m *Manager) Regenerate(ctx context.Context) (string, string, error) {
	if m == nil || !m.Snapshot().Enabled {
		return "", "", errors.New("tor hidden service is not running")
	}
	m.StopVanitySearch()
	m.controlMu.Lock()
	defer m.controlMu.Unlock()
	privateKey, publicKey, address, err := generateIdentity()
	if err != nil {
		return "", "", err
	}
	previous := strings.TrimSpace(m.Snapshot().Hostname)
	if err := m.installIdentity(ctx, privateKey, publicKey, address); err != nil {
		return "", "", err
	}
	return address, previous, nil
}

// ApplyVanityCandidate atomically installs a unique saved candidate and verifies Tor published it.
func (m *Manager) ApplyVanityCandidate(ctx context.Context, address string) (string, error) {
	if m == nil || !m.Snapshot().Enabled {
		return "", errors.New("tor hidden service is not running")
	}
	m.StopVanitySearch()
	m.controlMu.Lock()
	defer m.controlMu.Unlock()
	candidate, err := m.resolveCandidate(address)
	if err != nil {
		return "", err
	}
	privateKey, publicKey, foundAddress, err := readIdentity(candidate)
	if err != nil {
		return "", err
	}
	if err := m.installIdentity(ctx, privateKey, publicKey, foundAddress); err != nil {
		return "", err
	}
	if foundAddress != m.Snapshot().Hostname {
		return "", fmt.Errorf("tor published %q after installing candidate %q", m.Snapshot().Hostname, foundAddress)
	}
	if err := os.RemoveAll(candidate); err != nil {
		return "", fmt.Errorf("remove applied Tor candidate: %w", err)
	}
	return foundAddress, nil
}

// ImportIdentity installs a key directory in the same way as applying a vanity candidate.
func (m *Manager) ImportIdentity(ctx context.Context, directory string) (string, error) {
	if m == nil || !m.Snapshot().Enabled {
		return "", errors.New("tor hidden service is not running")
	}
	if !filepath.IsAbs(directory) {
		return "", errors.New("key directory path must be absolute")
	}
	m.StopVanitySearch()
	m.controlMu.Lock()
	defer m.controlMu.Unlock()
	privateKey, publicKey, address, err := readIdentity(directory)
	if err != nil {
		return "", err
	}
	if err := m.installIdentity(ctx, privateKey, publicKey, address); err != nil {
		return "", err
	}
	if address != m.Snapshot().Hostname {
		return "", fmt.Errorf("tor published %q after importing %q", m.Snapshot().Hostname, address)
	}
	return address, nil
}

func (m *Manager) searchVanity(ctx context.Context, search *vanitySearch) {
	for ctx.Err() == nil {
		privateKey, publicKey, address, err := generateIdentity()
		if err != nil {
			m.logger.Error("Tor vanity key generation failed", "error", err)
			return
		}
		search.attempts.Add(1)
		if !strings.HasPrefix(strings.TrimSuffix(address, ".onion"), search.prefix) {
			continue
		}
		if err := writeCandidate(m.vanityDirectory(), privateKey, publicKey, address); err != nil {
			m.logger.Error("Tor vanity candidate could not be saved", "error", err)
			return
		}
		m.vanityMu.Lock()
		if m.vanity == search {
			search.cancel()
			m.vanity = nil
		}
		m.vanityMu.Unlock()
		return
	}
}

func (m *Manager) candidateAddresses() []string {
	entries, err := os.ReadDir(m.vanityDirectory())
	if err != nil {
		return []string{}
	}
	addresses := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".onion") && validHostname(entry.Name()) {
			addresses = append(addresses, entry.Name())
		}
	}
	return addresses
}

func (m *Manager) vanityDirectory() string {
	return filepath.Join(m.options.DataDir, "tor", "vanity")
}

func (m *Manager) resolveCandidate(address string) (string, error) {
	candidates := m.candidateAddresses()
	address = strings.TrimSpace(address)
	var matches []string
	for _, candidate := range candidates {
		if address == "" && len(candidates) == 1 || address != "" && strings.HasPrefix(candidate, address) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return "", ErrCandidateNotFound
	}
	if len(matches) != 1 {
		return "", ErrCandidateAmbiguous
	}
	return filepath.Join(m.vanityDirectory(), matches[0]), nil
}

func (m *Manager) installIdentity(ctx context.Context, privateKey, publicKey []byte, address string) (result error) {
	if !validHostname(address) {
		return errors.New("tor identity has an invalid v3 hostname")
	}
	paths := torPaths(m.options)
	parent := filepath.Dir(paths.siteDir)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create Tor data directory: %w", err)
	}
	if err := enforcePrivate(parent, true); err != nil {
		return err
	}
	backup, err := os.MkdirTemp(parent, ".tor-site-backup-")
	if err != nil {
		return fmt.Errorf("prepare Tor identity rollback directory: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("prepare Tor identity rollback path: %w", err)
	}
	staged, err := os.MkdirTemp(parent, ".tor-site-")
	if err != nil {
		return fmt.Errorf("stage Tor identity: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(staged); err != nil {
			result = errors.Join(result, fmt.Errorf("remove staged Tor identity: %w", err))
		}
	}()
	if err := os.Chmod(staged, 0700); err != nil {
		return fmt.Errorf("secure staged Tor identity: %w", err)
	}
	if err := writeIdentityFiles(staged, privateKey, publicKey, address); err != nil {
		return err
	}
	if err := os.Rename(paths.siteDir, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stage current Tor identity: %w", err)
	}
	if err := os.Rename(staged, paths.siteDir); err != nil {
		_ = os.Rename(backup, paths.siteDir)
		return fmt.Errorf("install Tor identity: %w", err)
	}
	if err := m.restartLocked(ctx); err != nil {
		return err
	}
	if got := strings.TrimSpace(m.Snapshot().Hostname); got != address {
		return fmt.Errorf("tor published hostname %q, expected %q", got, address)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove previous Tor identity: %w", err)
	}
	return nil
}

func (m *Manager) restartLocked(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	oldPID := m.Snapshot().PID
	select {
	case m.restart <- struct{}{}:
	default:
		return errors.New("tor restart is already pending")
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot := m.Snapshot()
		if snapshot.Running && snapshot.Status == statusHealthy && snapshot.PID != oldPID {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("tor restart did not finish: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func generateIdentity() ([]byte, []byte, string, error) {
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, nil, "", fmt.Errorf("generate Tor identity: %w", err)
	}
	digest := sha512.Sum512(seed)
	secretScalar := append([]byte(nil), digest[:32]...)
	secretScalar[0] &= 248
	secretScalar[31] &= 63
	secretScalar[31] |= 64
	scalar, err := new(edwards25519.Scalar).SetBytesWithClamping(secretScalar)
	if err != nil {
		return nil, nil, "", fmt.Errorf("derive Tor identity scalar: %w", err)
	}
	publicKey := new(edwards25519.Point).ScalarBaseMult(scalar).Bytes()
	privateKey := append(secretScalar, digest[32:]...)
	address := onionAddress(publicKey)
	return privateKey, append([]byte(nil), publicKey...), address, nil
}

func onionAddress(publicKey []byte) string {
	input := append([]byte(".onion checksum"), publicKey...)
	input = append(input, 3)
	checksum := sha3.Sum256(input)
	address := append(append([]byte(nil), publicKey...), checksum[:2]...)
	address = append(address, 3)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(address)) + ".onion"
}

func writeIdentityFiles(directory string, privateKey, publicKey []byte, address string) error {
	secret := []byte(torSecretHeader + base64.StdEncoding.EncodeToString(privateKey) + "\n")
	public := []byte(torPublicHeader + base64.StdEncoding.EncodeToString(publicKey) + "\n")
	for name, contents := range map[string][]byte{
		"hs_ed25519_secret_key": secret,
		"hs_ed25519_public_key": public,
		"hostname":              []byte(address + "\n"),
	} {
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0600); err != nil {
			return fmt.Errorf("write Tor identity file: %w", err)
		}
	}
	return nil
}

func writeCandidate(root string, privateKey, publicKey []byte, address string) (result error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return fmt.Errorf("create vanity candidate directory: %w", err)
	}
	if err := enforcePrivate(root, true); err != nil {
		return err
	}
	staged, err := os.MkdirTemp(root, ".candidate-")
	if err != nil {
		return fmt.Errorf("stage vanity candidate: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(staged); err != nil {
			result = errors.Join(result, fmt.Errorf("remove staged vanity candidate: %w", err))
		}
	}()
	if err := os.Chmod(staged, 0700); err != nil {
		return fmt.Errorf("secure vanity candidate: %w", err)
	}
	if err := writeIdentityFiles(staged, privateKey, publicKey, address); err != nil {
		return err
	}
	if err := os.Rename(staged, filepath.Join(root, address)); err != nil {
		return fmt.Errorf("publish vanity candidate: %w", err)
	}
	return nil
}

func readIdentity(directory string) ([]byte, []byte, string, error) {
	if !filepath.IsAbs(directory) {
		return nil, nil, "", fmt.Errorf("%w: key directory path must be absolute", ErrInvalidIdentity)
	}
	read := func(name string) ([]byte, error) {
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("%w: required key file %s is unavailable", ErrInvalidIdentity, name)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: key file %s is not a regular file", ErrInvalidIdentity, name)
		}
		if info.Size() < 1 || info.Size() > 1024 {
			return nil, fmt.Errorf("%w: key file %s has an invalid size", ErrInvalidIdentity, name)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: key file %s cannot be read", ErrInvalidIdentity, name)
		}
		return contents, nil
	}
	secretFile, err := read("hs_ed25519_secret_key")
	if err != nil {
		return nil, nil, "", fmt.Errorf("read Tor secret key: %w", err)
	}
	publicFile, err := read("hs_ed25519_public_key")
	if err != nil {
		return nil, nil, "", fmt.Errorf("read Tor public key: %w", err)
	}
	hostnameFile, err := read("hostname")
	if err != nil {
		return nil, nil, "", fmt.Errorf("read Tor hostname: %w", err)
	}
	privateKey, err := decodeTorKey(secretFile, torSecretHeader, 64)
	if err != nil {
		return nil, nil, "", err
	}
	publicKey, err := decodeTorKey(publicFile, torPublicHeader, 32)
	if err != nil {
		return nil, nil, "", err
	}
	scalar, err := new(edwards25519.Scalar).SetBytesWithClamping(privateKey[:32])
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: secret scalar is invalid", ErrInvalidIdentity)
	}
	derivedPublicKey := new(edwards25519.Point).ScalarBaseMult(scalar).Bytes()
	if !bytes.Equal(derivedPublicKey, publicKey) {
		return nil, nil, "", fmt.Errorf("%w: public and secret keys do not match", ErrInvalidIdentity)
	}
	address := strings.TrimSpace(string(hostnameFile))
	if expected := onionAddress(publicKey); address != expected {
		return nil, nil, "", fmt.Errorf("%w: hostname does not match public key", ErrInvalidIdentity)
	}
	return privateKey, publicKey, address, nil
}

func decodeTorKey(file []byte, header string, expectedLength int) ([]byte, error) {
	text := string(file)
	if !strings.HasPrefix(text, header) {
		return nil, fmt.Errorf("%w: key has an invalid format", ErrInvalidIdentity)
	}
	encoded := strings.TrimSpace(strings.TrimPrefix(text, header))
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != expectedLength {
		return nil, fmt.Errorf("%w: key has an invalid length or encoding", ErrInvalidIdentity)
	}
	return key, nil
}

func torBinaryVersion(parent context.Context, binary string) string {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "--version")
	var output boundedBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return "unknown"
	}
	fields := strings.Fields(output.String())
	for index, field := range fields {
		if field == "version" && index+1 < len(fields) {
			return strings.TrimSuffix(fields[index+1], ",")
		}
	}
	return "unknown"
}
