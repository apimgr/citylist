package i2p

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSAM struct {
	listener net.Listener
	keyPath  string
	private  string

	mu                  sync.Mutex
	nextID              int
	commands            []string
	sessionConnID       int
	forwardConnID       int
	keyExistedAtSession bool
	backendRequestOK    bool
	errors              chan error
}

func newFakeSAM(t *testing.T, keyPath, private string) *fakeSAM {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSAM{listener: listener, keyPath: keyPath, private: private, errors: make(chan error, 8)}
	go fake.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return fake
}

func (fake *fakeSAM) serve() {
	for {
		conn, err := fake.listener.Accept()
		if err != nil {
			return
		}
		fake.mu.Lock()
		fake.nextID++
		id := fake.nextID
		fake.mu.Unlock()
		go fake.handle(conn, id)
	}
}

func (fake *fakeSAM) handle(conn net.Conn, id int) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	if _, err := reader.ReadString('\n'); err != nil {
		fake.report(err)
		return
	}
	if _, err := io.WriteString(conn, "HELLO REPLY RESULT=OK VERSION=3.3\n"); err != nil {
		fake.report(err)
		return
	}
	for {
		command, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command = strings.TrimSpace(command)
		fake.mu.Lock()
		fake.commands = append(fake.commands, command)
		fake.mu.Unlock()
		switch {
		case strings.HasPrefix(command, "DEST GENERATE "):
			err = func() error {
				_, writeErr := fmt.Fprintf(conn, "DEST REPLY PUB=unused PRIV=%s\n", fake.private)
				return writeErr
			}()
		case strings.HasPrefix(command, "SESSION CREATE "):
			info, statErr := os.Stat(fake.keyPath)
			fake.mu.Lock()
			fake.sessionConnID = id
			fake.keyExistedAtSession = statErr == nil && info.Mode().Perm() == 0600
			fake.mu.Unlock()
			if statErr != nil {
				fake.report(fmt.Errorf("session started before destination key persisted: %w", statErr))
				_, _ = io.WriteString(conn, "SESSION STATUS RESULT=INVALID_KEY\n")
				return
			}
			_, err = io.WriteString(conn, "SESSION STATUS RESULT=OK\n")
		case strings.HasPrefix(command, "STREAM FORWARD "):
			fake.mu.Lock()
			fake.forwardConnID = id
			fake.mu.Unlock()
			_, err = io.WriteString(conn, "STREAM STATUS RESULT=OK\n")
			if err == nil {
				fake.verifyBackend(command)
			}
		case command == "QUIT":
			_, err = io.WriteString(conn, "QUIT REPLY RESULT=OK\n")
		default:
			fake.report(fmt.Errorf("unexpected SAM command: %s", command))
			return
		}
		if err != nil {
			fake.report(err)
			return
		}
	}
}

func (fake *fakeSAM) verifyBackend(command string) {
	var port string
	for _, field := range strings.Fields(command) {
		if strings.HasPrefix(field, "PORT=") {
			port = strings.TrimPrefix(field, "PORT=")
		}
	}
	if !strings.Contains(command, "SILENT=true") || port == "" {
		fake.report(fmt.Errorf("STREAM FORWARD missing SILENT or backend port: %s", command))
		return
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second)
	if err != nil {
		fake.report(fmt.Errorf("connect to ready backend: %w", err))
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(conn, "GET /overlay-check HTTP/1.1\r\nHost: example.b32.i2p\r\nConnection: close\r\n\r\n"); err != nil {
		fake.report(err)
		return
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		fake.report(err)
		return
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "plain-http" {
		fake.report(fmt.Errorf("unexpected backend response: status=%d body=%q err=%v", response.StatusCode, body, err))
		return
	}
	fake.mu.Lock()
	fake.backendRequestOK = true
	fake.mu.Unlock()
}

func (fake *fakeSAM) report(err error) {
	select {
	case fake.errors <- err:
	default:
	}
}

func (fake *fakeSAM) snapshot() (int, int, bool, bool, []string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.sessionConnID, fake.forwardConnID, fake.keyExistedAtSession, fake.backendRequestOK, append([]string(nil), fake.commands...)
}

func TestManagerSAMKeyPersistenceSeparateForwardSocketAndPlainHTTP(t *testing.T) {
	directory := t.TempDir()
	keyPath := filepath.Join(directory, "data", "i2p", "site", "site-keys.dat")
	private := testDestinationPrivateKey()
	fake := newFakeSAM(t, keyPath, private)
	options := DefaultOptions()
	options.Enabled = true
	options.SAMAddress = fake.listener.Addr().String()
	options.ConfigDir = filepath.Join(directory, "config")
	options.DataDir = filepath.Join(directory, "data")
	options.LogDir = filepath.Join(directory, "logs")
	options.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/overlay-check" || r.Host != "example.b32.i2p" {
			http.Error(w, "bad forwarded request", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, "plain-http")
	})
	manager, err := Start(context.Background(), options)
	if err != nil {
		t.Fatalf("start I2P manager: %v", err)
	}
	defer func() { _ = manager.Close() }()
	deadline := time.After(5 * time.Second)
	for {
		if manager.Snapshot().Status == "healthy" {
			_, _, _, requestOK, _ := fake.snapshot()
			if requestOK {
				break
			}
		}
		select {
		case err := <-fake.errors:
			t.Fatal(err)
		case <-deadline:
			sessionID, forwardID, keyReady, requestOK, commands := fake.snapshot()
			select {
			case err := <-fake.errors:
				t.Fatalf("I2P did not become healthy: %+v; fake SAM: %v; commands=%v session=%d forward=%d key=%t request=%t", manager.Snapshot(), err, commands, sessionID, forwardID, keyReady, requestOK)
			default:
				t.Fatalf("I2P did not become healthy: %+v; commands=%v session=%d forward=%d key=%t request=%t", manager.Snapshot(), commands, sessionID, forwardID, keyReady, requestOK)
			}
		case <-time.After(10 * time.Millisecond):
		}
	}
	sessionID, forwardID, keyReady, requestOK, commands := fake.snapshot()
	if sessionID == 0 || forwardID == 0 || sessionID == forwardID {
		t.Fatalf("session and forwarding sockets were not separate: session=%d forward=%d", sessionID, forwardID)
	}
	if !keyReady {
		t.Fatal("destination key was not securely persisted before SESSION CREATE")
	}
	if !requestOK {
		t.Fatal("forwarded backend did not serve plain HTTP before STREAM FORWARD completed")
	}
	if manager.Snapshot().Provider != "sam" || !strings.HasSuffix(manager.Snapshot().Hostname, ".b32.i2p") {
		t.Fatalf("unexpected healthy snapshot: %+v", manager.Snapshot())
	}
	if len(commands) < 3 || !strings.HasPrefix(commands[0], "DEST GENERATE ") || !strings.HasPrefix(commands[1], "SESSION CREATE ") || !strings.HasPrefix(commands[2], "STREAM FORWARD ") {
		t.Fatalf("SAM protocol sequence = %v", commands)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil || strings.TrimSpace(string(key)) != private {
		t.Fatalf("persisted destination key mismatch: err=%v", err)
	}
}

func TestManagerEnabledWithoutProviderStaysNonfatal(t *testing.T) {
	directory := t.TempDir()
	options := DefaultOptions()
	options.Enabled = true
	options.SAMAddress = "127.0.0.1:1"
	options.Binary = filepath.Join(directory, "missing-i2pd")
	options.ConfigDir = filepath.Join(directory, "config")
	options.DataDir = filepath.Join(directory, "data")
	options.LogDir = filepath.Join(directory, "logs")
	options.Handler = http.NotFoundHandler()
	manager, err := Start(context.Background(), options)
	if err != nil {
		t.Fatalf("Start() must return immediately without a provider: %v", err)
	}
	defer func() { _ = manager.Close() }()
	deadline := time.After(time.Second)
	for manager.Snapshot().State != StateNoProvider {
		select {
		case <-deadline:
			t.Fatalf("expected no-provider state, got %+v", manager.Snapshot())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if manager.Snapshot().Status != "disabled" {
		t.Fatalf("missing provider should map to disabled, got %+v", manager.Snapshot())
	}
	if _, err := os.Stat(filepath.Join(directory, "data", "i2p")); !os.IsNotExist(err) {
		t.Fatalf("no-provider path created I2P runtime files: %v", err)
	}
}

func testDestinationPrivateKey() string {
	data := make([]byte, 663)
	for index := range data {
		data[index] = byte(index)
	}
	data[385] = 0
	data[386] = 4
	return i2pBase64.EncodeToString(data)
}
