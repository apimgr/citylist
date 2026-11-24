package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"github.com/apimgr/citylist/src/paths"
	servicepkg "github.com/apimgr/citylist/src/service"
)

func TestCheckHealth(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/server/healthz" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("failed to parse test server URL: %v", err)
		}

		if err := checkHealth(u.Port()); err != nil {
			t.Errorf("checkHealth() error = %v, want nil", err)
		}
	})

	t.Run("bad status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("failed to parse test server URL: %v", err)
		}

		if err := checkHealth(u.Port()); err == nil {
			t.Error("expected error for non-200 status, got nil")
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		if err := checkHealth("1"); err == nil {
			t.Error("expected error connecting to closed port, got nil")
		}
	})
}

func TestRunServerHelpVersionAndMainEntry(t *testing.T) {
	originalFlags := flag.CommandLine
	originalArgs := os.Args
	originalLanguage := serverOutputLanguage
	t.Cleanup(func() {
		flag.CommandLine = originalFlags
		os.Args = originalArgs
		serverOutputLanguage = originalLanguage
	})

	for _, args := range [][]string{{"--help"}, {"--version"}, {"--service", "--help"}, {"--maintenance", "--help"}} {
		flag.CommandLine = flag.NewFlagSet("citylist", flag.ContinueOnError)
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			runServer(args)
		})
	}

	flag.CommandLine = flag.NewFlagSet("citylist", flag.ContinueOnError)
	os.Args = []string{"citylist", "--version"}
	main()
}

func newTestConfigPath(t *testing.T) string {
	t.Helper()
	tmpDir := mainProjectTempDir(t)
	path := filepath.Join(tmpDir, "server.yml")
	cfg := config.DefaultConfig()
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("failed to save test config: %v", err)
	}
	return path
}

func TestSetApplicationMode(t *testing.T) {
	t.Run("valid mode", func(t *testing.T) {
		path := newTestConfigPath(t)
		setApplicationMode("development", path)

		cfg, err := config.Load(path)
		if err != nil {
			t.Fatalf("failed to reload config: %v", err)
		}
		if cfg.Server.Mode != "development" {
			t.Errorf("Server.Mode = %q, want development", cfg.Server.Mode)
		}
	})
}

func TestShowCurrentMode(t *testing.T) {
	path := newTestConfigPath(t)
	showCurrentMode(path)
}

func TestRunInitialSetup(t *testing.T) {
	path := newTestConfigPath(t)
	runInitialSetup(path)
}

func TestHandleUpdateCommand(t *testing.T) {
	t.Run("branch persists canonical channel", func(t *testing.T) {
		path := newTestConfigPath(t)
		cfg := config.DefaultConfig()
		handleUpdateCommand("branch", cfg, path, []string{"daily"})
		loaded, err := config.Load(path)
		if err != nil {
			t.Fatalf("reload config: %v", err)
		}
		if loaded.Server.Update.Branch != "devel" {
			t.Fatalf("branch = %q, want devel", loaded.Server.Update.Branch)
		}
	})
}

func TestManualUpdatePrivilegeError(t *testing.T) {
	tests := []struct {
		name     string
		binary   string
		goos     string
		elevated bool
		service  bool
		want     string
	}{
		{name: "elevated service", binary: "citylist", goos: "linux", elevated: true, service: true},
		{name: "user mode command can update its own binary", binary: "citylist", goos: "linux"},
		{name: "unix service requests sudo", binary: "citylist", goos: "linux", service: true, want: "sudo citylist --update yes"},
		{name: "windows service requests elevated prompt", binary: "custom-citylist.exe", goos: "windows", service: true, want: "Run custom-citylist.exe --update yes from an elevated prompt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := manualUpdatePrivilegeError(test.binary, test.goos, test.elevated, test.service)
			if test.want == "" {
				if err != nil {
					t.Fatalf("manualUpdatePrivilegeError() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("manualUpdatePrivilegeError() = %v, want message containing %q", err, test.want)
			}
		})
	}
}

func TestPrintHelp(t *testing.T) {
	printHelp()
}

func TestRunServerImmediateHelpAndVersionFlags(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "help", args: []string{"--help"}, want: "Usage:"},
		{name: "version", args: []string{"--version"}, want: "OS/Arch:"},
		{name: "shell help", args: []string{"--shell", "--help"}, want: "completions"},
		{name: "update help", args: []string{"--update", "--help"}, want: "branch"},
		{name: "maintenance help", args: []string{"--maintenance", "--help"}, want: "backup"},
		{name: "shell completions", args: []string{"--shell", "completions", "bash"}, want: "complete -W"},
	} {
		t.Run(test.name, func(t *testing.T) {
			originalCommandLine, originalStdout := flag.CommandLine, os.Stdout
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			flag.CommandLine = flag.NewFlagSet("citylist", flag.ContinueOnError)
			os.Stdout = writer
			runServer(test.args)
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			os.Stdout = originalStdout
			flag.CommandLine = originalCommandLine
			output, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			_ = reader.Close()
			if !strings.Contains(string(output), test.want) {
				t.Fatalf("runServer(%v) output missing %q: %s", test.args, test.want, output)
			}
		})
	}
}

func TestServerShellCommands(t *testing.T) {
	t.Run("bash completion script", func(t *testing.T) {
		var output bytes.Buffer
		if err := runServerShell(&output, "completions", []string{"bash"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "--backup") || !strings.Contains(output.String(), filepath.Base(os.Args[0])) {
			t.Fatalf("completion script is incomplete: %s", output.String())
		}
	})
	t.Run("POSIX init command", func(t *testing.T) {
		var output bytes.Buffer
		if err := runServerShell(&output, "init", []string{"dash"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), `eval "$(`) {
			t.Fatalf("dash init command = %q, want eval wrapper", output.String())
		}
	})
	if err := runServerShell(&bytes.Buffer{}, "unknown", []string{"bash"}); err == nil {
		t.Fatal("unknown shell action succeeded")
	}
	if err := runServerShell(&bytes.Buffer{}, "init", []string{"unknown"}); err == nil {
		t.Fatal("unknown shell succeeded")
	}
}

func TestCommandHelpPreScan(t *testing.T) {
	for _, args := range [][]string{{"--shell", "--help"}, {"--shell", "help"}, {"--shell", "-h"}} {
		if command, ok := commandHelp(args, "shell"); !ok || command != "shell" {
			t.Errorf("commandHelp(%v) = (%q, %t), want shell help", args, command, ok)
		}
	}
}

func TestNormalizeOptionalBoolFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "bare flag enables", args: []string{"--i2p"}, want: []string{"--i2p=true"}},
		{name: "space value", args: []string{"--i2p", "no", "--help"}, want: []string{"--i2p=no", "--help"}},
		{name: "equals value", args: []string{"--i2p=false"}, want: []string{"--i2p=false"}},
		{name: "other flags remain", args: []string{"--port", "8080", "--i2p"}, want: []string{"--port", "8080", "--i2p=true"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeOptionalBoolFlag(test.args, "--i2p")
			if strings.Join(got, " ") != strings.Join(test.want, " ") {
				t.Fatalf("normalizeOptionalBoolFlag() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSelectDefaultPort(t *testing.T) {
	port, err := selectDefaultPort("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 64000 || number > 64999 {
		t.Fatalf("selected port = %q, want numeric port in 64000-64999", port)
	}
}

func TestResolveDatabasePathExtended(t *testing.T) {
	dataDir := filepath.Join(string(filepath.Separator), "var", "lib", "citylist")
	tests := []struct {
		name          string
		configuredURL string
		want          string
		wantErr       bool
	}{
		{name: "default database path", want: filepath.Join(paths.GetDatabaseDir(dataDir), "server.db")},
		{name: "relative path is rooted in data directory", configuredURL: filepath.Join("custom", "cities.db"), want: filepath.Join(dataDir, "custom", "cities.db")},
		{name: "absolute path is preserved", configuredURL: filepath.Join(string(filepath.Separator), "srv", "citylist", "cities.db"), want: filepath.Join(string(filepath.Separator), "srv", "citylist", "cities.db")},
		{name: "data directory placeholder is expanded", configuredURL: "{data_dir}/db/cities.db", want: filepath.Join(dataDir, "db", "cities.db")},
		{name: "file URL path is decoded", configuredURL: "file:///srv/city%20list/cities.db", want: filepath.Join(string(filepath.Separator), "srv", "city list", "cities.db")},
		{name: "query is rejected", configuredURL: "file:///srv/cities.db?mode=ro", wantErr: true},
		{name: "fragment is rejected", configuredURL: "file:///srv/cities.db#backup", wantErr: true},
		{name: "empty file URL path is rejected", configuredURL: "file:", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveDatabasePath(test.configuredURL, dataDir)
			if test.wantErr {
				if err == nil {
					t.Fatal("resolveDatabasePath() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveDatabasePath() error = %v", err)
			}
			if got != test.want {
				t.Errorf("resolveDatabasePath() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLanguageArgument(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "defaults to automatic", want: "auto"},
		{name: "space separated value", args: []string{"--lang", "fr"}, want: "fr"},
		{name: "equals value", args: []string{"--lang=es"}, want: "es"},
		{name: "ignores unrelated flags", args: []string{"--port", "8080", "--lang", "de"}, want: "de"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := languageArgument(test.args); got != test.want {
				t.Errorf("languageArgument(%v) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

func TestRunCommand(t *testing.T) {
	t.Run("existing command", func(t *testing.T) {
		runCommand("true")
	})
	t.Run("missing command", func(t *testing.T) {
		runCommand("citylist-definitely-not-a-real-command")
	})
}

// Exercises the service lifecycle wrapper functions. On linux they shell
// out to systemctl (absent in the test container, so runCommand just logs
func TestHandleServiceCommand(t *testing.T) {
	tmpDir := mainProjectTempDir(t)
	options := servicepkg.Options{Name: "citylist-test", Org: "apimgr", ConfigDir: tmpDir, DataDir: tmpDir, LogsDir: tmpDir}
	if err := handleServiceCommand("--help", options); err != nil {
		t.Fatalf("help command returned error: %v", err)
	}
	if err := handleServiceCommand("invalid", options); err == nil {
		t.Fatal("invalid service command succeeded")
	}
}

func TestHandleMaintenanceCommand(t *testing.T) {
	path := newTestConfigPath(t)
	tmpDir := filepath.Dir(path)
	cfg := config.DefaultConfig()

	t.Run("update", func(t *testing.T) {
		handleMaintenanceCommand("update", tmpDir, tmpDir, tmpDir, tmpDir, path, cfg, false, false)
	})
	t.Run("mode", func(t *testing.T) {
		handleMaintenanceCommand("mode", tmpDir, tmpDir, tmpDir, tmpDir, path, cfg, false, false)
	})
	t.Run("setup", func(t *testing.T) {
		handleMaintenanceCommand("setup", tmpDir, tmpDir, tmpDir, tmpDir, path, cfg, false, false)
	})
}

func TestMaintenanceBackupAndRestore(t *testing.T) {
	tmpDir := mainProjectTempDir(t)
	configDir := filepath.Join(tmpDir, "config")
	dataDir := filepath.Join(tmpDir, "data")
	backupDir := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("failed to create data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "server.yml"), []byte("server:\n  port: \"8080\"\n"), 0644); err != nil {
		t.Fatalf("failed to write config fixture: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(dataDir, "db"), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := database.OpenSQLite(filepath.Join(dataDir, "db", "server.db"), database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Server.Database.Driver = "sqlite"
	if err := runBackupCommand(filepath.Join(configDir, "server.yml"), configDir, dataDir, backupDir, "", cfg, false, false); err != nil {
		t.Fatalf("runBackupCommand() error: %v", err)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("backup files = %d, want 1", len(entries))
	}
	if err := runBackupTestCommand(entries[0].Name(), backupDir, configDir, cfg); err != nil {
		t.Fatalf("runBackupTestCommand() error: %v", err)
	}
	if err := runRestoreCommand(entries[0].Name(), filepath.Join(configDir, "server.yml"), configDir, dataDir, backupDir, cfg); err != nil {
		t.Fatalf("runRestoreCommand() error: %v", err)
	}
}

func mainProjectTempDir(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve project test source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), ".."))
	metadata, err := os.ReadFile(filepath.Join(root, "IDEA.md"))
	if err != nil {
		t.Fatal(err)
	}
	value := func(key string) string {
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:\s*([^\s#]+)\s*$`)
		match := pattern.FindSubmatch(metadata)
		if len(match) != 2 {
			t.Fatalf("IDEA.md is missing %s", key)
		}
		return string(match[1])
	}
	base := filepath.Join(os.TempDir(), value("project_org"))
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(base, value("internal_name")+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
