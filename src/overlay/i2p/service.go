package i2p

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errMissingDestination = errors.New("destination key does not exist")

type service struct {
	provider    string
	address     string
	samAddress  string
	control     net.Conn
	forward     net.Conn
	child       *exec.Cmd
	childExited chan struct{}
	pidPath     string
	backend     net.Listener
	backendHTTP *http.Server
	failed      chan error
	closeOnce   sync.Once
	logFile     *os.File
}

func resolveProvider(ctx context.Context, opts Options) (string, string, error) {
	if binary, err := resolveI2PDBinary(opts.Binary); err == nil {
		return "i2pd", binary, nil
	}
	if opts.SAMAddress == "" {
		return "", "", errors.New("no i2pd binary or SAM bridge configured")
	}
	conn, _, err := dialSAM(ctx, opts.SAMAddress)
	if err != nil {
		return "", "", fmt.Errorf("no i2pd binary or reachable SAM bridge at %s", opts.SAMAddress)
	}
	_ = conn.Close()
	return "sam", "", nil
}

func resolveI2PDBinary(configured string) (string, error) {
	if configured != "" {
		info, err := os.Stat(configured)
		if err == nil && !info.IsDir() && isExecutable(info.Mode()) {
			return configured, nil
		}
		return "", fmt.Errorf("configured i2pd binary is not usable: %s", configured)
	}
	for _, candidate := range []string{"/usr/bin/i2pd", "/usr/sbin/i2pd", "/usr/local/bin/i2pd", "/opt/homebrew/bin/i2pd"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && isExecutable(info.Mode()) {
			return candidate, nil
		}
	}
	return exec.LookPath("i2pd")
}

func isExecutable(mode os.FileMode) bool {
	if runtimeIsWindows() {
		return true
	}
	return mode.Perm()&0111 != 0
}

func startService(ctx context.Context, opts Options, provider, binary string, state func(State)) (*service, error) {
	if err := ensureRuntimeDirectories(opts, provider); err != nil {
		return nil, err
	}
	backend, err := randomLoopbackListener()
	if err != nil {
		return nil, fmt.Errorf("allocate I2P backend listener: %w", err)
	}
	backendHTTP := &http.Server{
		Handler:           opts.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      65 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	ready := make(chan struct{})
	readyListener := &acceptReadyListener{Listener: backend, ready: ready}
	svc := &service{
		provider:    provider,
		samAddress:  opts.SAMAddress,
		backend:     backend,
		backendHTTP: backendHTTP,
		failed:      make(chan error, 1),
	}
	go func() {
		if err := backendHTTP.Serve(readyListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case svc.failed <- fmt.Errorf("I2P backend listener stopped: %w", err):
			default:
			}
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		svc.close()
		return nil, ctx.Err()
	}
	if provider == "i2pd" {
		address, err := startI2PD(ctx, opts, binary, svc)
		if err != nil {
			svc.close()
			return nil, err
		}
		svc.samAddress = address
	}
	if err := establishEepsite(ctx, opts, svc, state); err != nil {
		svc.close()
		return nil, err
	}
	return svc, nil
}

type acceptReadyListener struct {
	net.Listener
	ready chan struct{}
	once  sync.Once
}

func (l *acceptReadyListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.ready) })
	return l.Listener.Accept()
}

func establishEepsite(ctx context.Context, opts Options, svc *service, state func(State)) error {
	conn, reader, err := dialSAM(ctx, svc.samAddress)
	if err != nil {
		return fmt.Errorf("connect to SAM bridge: %w", err)
	}
	stopSessionOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopSessionOnCancel()
	keyPath := filepath.Join(opts.DataDir, "i2p", "site", "site-keys.dat")
	priv, err := loadOrCreateDestination(ctx, conn, reader, keyPath, opts.SignatureType)
	if err != nil {
		_ = conn.Close()
		return err
	}
	destination, err := destinationBytes(priv)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("read destination: %w", err)
	}
	address := b32Address(destination)
	backendPort, err := listenerPort(svc.backend)
	if err != nil {
		_ = conn.Close()
		return err
	}
	state(StateBuilding)
	for attempt := 0; attempt < 2; attempt++ {
		sessionID := fmt.Sprintf("site-%d", os.Getpid())
		if attempt > 0 {
			sessionID += "-retry"
		}
		command := fmt.Sprintf("SESSION CREATE STYLE=STREAM ID=%s DESTINATION=%s i2cp.leaseSetEncType=4 inbound.length=%d outbound.length=%d inbound.quantity=%d outbound.quantity=%d",
			sessionID, priv, opts.InboundLength, opts.OutboundLength, opts.InboundQuantity, opts.OutboundQuantity)
		reply, err := samCommand(conn, reader, opts.BootstrapTimeout, command)
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("SESSION CREATE failed: %w", err)
		}
		if reply.values["RESULT"] == "DUPLICATED_ID" && attempt == 0 {
			continue
		}
		if reply.values["RESULT"] != "OK" {
			_ = conn.Close()
			if reply.values["RESULT"] == "INVALID_KEY" {
				return errors.New("SESSION CREATE rejected the persisted destination key; it was not regenerated")
			}
			if reply.values["RESULT"] == "DUPLICATED_DEST" {
				return retryAfterError{after: 60 * time.Second, err: errors.New("SESSION CREATE reports the destination is already in use")}
			}
			return errors.New("SESSION CREATE failed")
		}
		forwardConn, forwardReader, err := dialSAM(ctx, svc.samAddress)
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("open SAM forwarding socket: %w", err)
		}
		stopForwardOnCancel := context.AfterFunc(ctx, func() { _ = forwardConn.Close() })
		defer stopForwardOnCancel()
		forwardCommand := fmt.Sprintf("STREAM FORWARD ID=%s PORT=%d HOST=127.0.0.1 SILENT=true", sessionID, backendPort)
		forwardReply, err := samCommand(forwardConn, forwardReader, 10*time.Second, forwardCommand)
		if err != nil || forwardReply.values["RESULT"] != "OK" {
			_ = forwardConn.Close()
			_ = conn.Close()
			return errors.New("STREAM FORWARD failed")
		}
		svc.control = conn
		svc.forward = forwardConn
		svc.address = address
		watchConnection(ctx, conn, svc.failed)
		watchConnection(ctx, forwardConn, svc.failed)
		return nil
	}
	_ = conn.Close()
	return errors.New("SESSION CREATE duplicated its session identifier twice")
}

type retryAfterError struct {
	after time.Duration
	err   error
}

func (e retryAfterError) Error() string { return e.err.Error() }
func (e retryAfterError) Unwrap() error { return e.err }

func listenerPort(listener net.Listener) (int, error) {
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("I2P backend listener is not TCP")
	}
	return address.Port, nil
}

func watchConnection(ctx context.Context, conn net.Conn, failed chan<- error) {
	go func() {
		buffer := make([]byte, 1)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			_, err := conn.Read(buffer)
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			if err != nil {
				if ctx.Err() == nil {
					select {
					case failed <- errors.New("SAM session socket closed"):
					default:
					}
				}
				return
			}
		}
	}()
}

func (s *service) close() {
	s.closeOnce.Do(func() {
		if s.forward != nil {
			_ = s.forward.Close()
		}
		if s.control != nil {
			_ = s.control.Close()
		}
		if s.backendHTTP != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.backendHTTP.Shutdown(ctx)
			cancel()
		}
		if s.backend != nil {
			_ = s.backend.Close()
		}
		if s.child != nil {
			terminateChild(s.child, s.childExited)
		}
		if s.pidPath != "" {
			_ = os.Remove(s.pidPath)
		}
		if s.logFile != nil {
			_ = s.logFile.Close()
		}
	})
}

func startI2PD(ctx context.Context, opts Options, binary string, svc *service) (string, error) {
	confDir := filepath.Join(opts.ConfigDir, "i2p")
	dataDir := filepath.Join(opts.DataDir, "i2p")
	confPath := filepath.Join(confDir, "i2pd.conf")
	tunnelConfig := filepath.Join(confDir, "tunnels.conf")
	tunnelDirectory := filepath.Join(confDir, "tunnels.d")
	pidPath := filepath.Join(dataDir, "i2pd.pid")
	logPath := filepath.Join(opts.LogDir, "i2pd.log")
	if strings.ContainsAny(logPath, "\r\n\x00") {
		return "", errors.New("invalid i2pd log path")
	}
	if err := reapStaleI2PD(pidPath, binary); err != nil {
		return "", fmt.Errorf("reap stale i2pd: %w", err)
	}
	samPort, err := unusedLoopbackPort()
	if err != nil {
		return "", err
	}
	if err := writePrivateFile(confPath, []byte(i2pdConfig(samPort, logPath))); err != nil {
		return "", fmt.Errorf("write i2pd.conf: %w", err)
	}
	if err := writePrivateFile(tunnelConfig, nil); err != nil {
		return "", fmt.Errorf("write empty tunnels.conf: %w", err)
	}
	if err := resetEmptyDirectory(tunnelDirectory); err != nil {
		return "", fmt.Errorf("prepare empty tunnels.d: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return "", fmt.Errorf("open i2pd log: %w", err)
	}
	if err := logFile.Chmod(0600); err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("secure i2pd log: %w", err)
	}
	command := exec.Command(binary, "--conf", confPath, "--tunconf", tunnelConfig, "--tunnelsdir", tunnelDirectory,
		"--datadir", dataDir, "--pidfile", pidPath)
	command.Stdout, command.Stderr = logFile, logFile
	setParentDeathSignal(command)
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("start i2pd: %w", err)
	}
	svc.child, svc.childExited, svc.logFile, svc.pidPath = command, make(chan struct{}), logFile, pidPath
	go func() {
		_ = command.Wait()
		close(svc.childExited)
	}()
	if err := writePrivateFile(pidPath, []byte(strconv.Itoa(command.Process.Pid)+"\n")); err != nil {
		terminateChild(command, svc.childExited)
		return "", fmt.Errorf("write i2pd pid file: %w", err)
	}
	address := fmt.Sprintf("127.0.0.1:%d", samPort)
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	for {
		if conn, _, err := dialSAM(ctx, address); err == nil {
			_ = conn.Close()
			return address, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-svc.childExited:
			return "", errors.New("i2pd exited before its SAM port opened; see i2pd.log")
		case <-deadline.C:
			return "", errors.New("i2pd SAM port did not open within 60 seconds; see i2pd.log")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func i2pdConfig(samPort int, logPath string) string {
	return fmt.Sprintf("daemon = false\nipv4 = true\nipv6 = false\nnotransit = true\nfloodfill = false\nlog = file\nlogfile = %s\nloglevel = warn\n\n[http]\nenabled = false\n[httpproxy]\nenabled = false\n[socksproxy]\nenabled = false\n[sam]\nenabled = true\naddress = 127.0.0.1\nport = %d\n[bob]\nenabled = false\n[i2cp]\nenabled = false\n[i2pcontrol]\nenabled = false\n[upnp]\nenabled = false\n", logPath, samPort)
}

func resetEmptyDirectory(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return ensurePrivateDirectory(path)
}

func writePrivateFile(path string, contents []byte) error {
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".i2p-private-*")
	if err != nil {
		return err
	}
	tempPath := temporary.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func readDestination(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errMissingDestination
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("destination key file %s is not a regular file", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	priv := strings.TrimSuffix(string(contents), "\n")
	if strings.ContainsAny(priv, "\r\n") {
		return "", fmt.Errorf("destination key file %s must contain exactly one line", path)
	}
	if _, err := destinationBytes(priv); err != nil {
		return "", fmt.Errorf("destination key file %s is invalid: %w", path, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		if err := os.Chmod(path, 0600); err != nil {
			return "", fmt.Errorf("secure destination key file %s: %w", path, err)
		}
	}
	return priv, nil
}

func writeDestinationAtomic(path, priv string) error {
	if strings.ContainsAny(priv, "\r\n \t") {
		return errors.New("invalid destination key")
	}
	if _, err := destinationBytes(priv); err != nil {
		return err
	}
	return writePrivateFile(path, []byte(priv+"\n"))
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}

func unusedLoopbackPort() (int, error) {
	listener, err := randomLoopbackListener()
	if err != nil {
		return 0, err
	}
	port, err := listenerPort(listener)
	_ = listener.Close()
	return port, err
}

func randomLoopbackListener() (net.Listener, error) {
	var lastErr error
	for attempt := 0; attempt < 100; attempt++ {
		port, err := randomPort()
		if err != nil {
			return nil, err
		}
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return listener, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("no unused loopback port in 64000-64999: %w", lastErr)
}
