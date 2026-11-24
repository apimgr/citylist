//go:build linux

package service

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestPrepareSystemRuntimeOwnsPrivateDirectories(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("system runtime ownership requires root")
	}
	for _, command := range []string{"useradd", "userdel", "groupdel", "chown"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is unavailable", command)
		}
	}

	name := "citylist-test-" + strconv.Itoa(os.Getpid())
	org := "apimgr-test-" + strconv.Itoa(os.Getpid())
	root := t.TempDir()
	options := Options{
		Name:      name,
		Org:       org,
		ConfigDir: filepath.Join(root, "etc", "citylist"),
		DataDir:   filepath.Join(root, "var", "lib", "citylist"),
		CacheDir:  filepath.Join(root, "var", "cache", "citylist"),
		LogsDir:   filepath.Join(root, "var", "log", "citylist"),
		BackupDir: filepath.Join(root, "backup", "citylist"),
	}
	if err := os.MkdirAll(options.ConfigDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{filepath.Join(options.ConfigDir, "server.yml"), filepath.Join(options.ConfigDir, "secrets.yml")} {
		if err := os.WriteFile(file, []byte("server: {}\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	privateFiles := []string{
		filepath.Join(options.ConfigDir, "assets", "logo.png"),
		filepath.Join(options.ConfigDir, "ssl", "server.key"),
		filepath.Join(options.ConfigDir, "tor", "torrc"),
		filepath.Join(options.ConfigDir, "i2p", "i2pd.conf"),
		filepath.Join(options.ConfigDir, "yaml.d", "50-test.yml"),
		filepath.Join(options.DataDir, "security", "key"),
		filepath.Join(options.DataDir, "tor", "site", "hostname"),
		filepath.Join(options.DataDir, "i2p", "site", "site-keys.dat"),
	}
	for _, file := range privateFiles {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("fixture\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	runtimeOrgDir := filepath.Join("/var/run", org)
	runtimeServiceDir := filepath.Join(runtimeOrgDir, name)
	t.Cleanup(func() {
		_ = exec.Command("userdel", name).Run()
		_ = exec.Command("groupdel", name).Run()
		_ = os.RemoveAll(runtimeServiceDir)
		_ = os.Remove(runtimeOrgDir)
	})

	databaseDir := filepath.Join(options.DataDir, "db")
	if err := PrepareSystemRuntime(options, databaseDir); err != nil {
		t.Fatal(err)
	}

	account, err := user.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	group, err := user.LookupGroup(name)
	if err != nil {
		t.Fatal(err)
	}
	if account.Gid != group.Gid {
		t.Fatalf("service account gid = %s, group gid = %s", account.Gid, group.Gid)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := strconv.ParseUint(group.Gid, 10, 32)
	if err != nil {
		t.Fatal(err)
	}

	for directory, wantMode := range map[string]os.FileMode{
		options.ConfigDir:                             0755,
		filepath.Join(options.ConfigDir, "assets"):    0700,
		filepath.Join(options.ConfigDir, "ssl"):       0700,
		filepath.Join(options.ConfigDir, "tor"):       0700,
		filepath.Join(options.ConfigDir, "i2p"):       0700,
		filepath.Join(options.ConfigDir, "yaml.d"):    0700,
		options.DataDir:                               0755,
		filepath.Join(options.DataDir, "security"):    0700,
		filepath.Join(options.DataDir, "tor"):         0700,
		filepath.Join(options.DataDir, "tor", "site"): 0700,
		filepath.Join(options.DataDir, "i2p"):         0700,
		filepath.Join(options.DataDir, "i2p", "site"): 0700,
	} {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatalf("stat %s: %v", directory, err)
		}
		if got := info.Mode().Perm(); got != wantMode {
			t.Errorf("mode for %s = %04o, want %04o", directory, got, wantMode)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("stat %s has unexpected type %T", directory, info.Sys())
		}
		if stat.Uid != uint32(uid) || stat.Gid != uint32(gid) {
			t.Errorf("owner for %s = %d:%d, want %d:%d", directory, stat.Uid, stat.Gid, uid, gid)
		}
	}
	for _, file := range append(privateFiles, filepath.Join(options.ConfigDir, "server.yml"), filepath.Join(options.ConfigDir, "secrets.yml")) {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatalf("stat %s: %v", file, err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Errorf("mode for %s = %04o, want 0600", file, got)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("stat %s has unexpected type %T", file, info.Sys())
		}
		if stat.Uid != uint32(uid) || stat.Gid != uint32(gid) {
			t.Errorf("owner for %s = %d:%d, want %d:%d", file, stat.Uid, stat.Gid, uid, gid)
		}
	}
}
