package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var cacheDefaults struct {
	sync.RWMutex
	directory string
	enabled   bool
	ttl       time.Duration
	maxBytes  int64
}

var responseCacheMu sync.Mutex

// SetDefaultResponseCache configures response caching for subsequently created clients.
func SetDefaultResponseCache(directory string, enabled bool, ttl time.Duration, maxBytes int64) error {
	if ttl < 0 || maxBytes <= 0 {
		return errors.New("invalid response cache policy")
	}
	if enabled && ttl > 0 {
		if strings.TrimSpace(directory) == "" {
			return errors.New("response cache directory is required")
		}
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		if err := os.Chmod(directory, 0700); err != nil {
			return err
		}
	}
	cacheDefaults.Lock()
	cacheDefaults.directory, cacheDefaults.enabled, cacheDefaults.ttl, cacheDefaults.maxBytes = directory, enabled, ttl, maxBytes
	cacheDefaults.Unlock()
	return nil
}

func applyDefaultResponseCache(client *Client) {
	cacheDefaults.RLock()
	client.cacheDir = cacheDefaults.directory
	client.cacheEnabled = cacheDefaults.enabled && cacheDefaults.ttl > 0
	client.cacheTTL = cacheDefaults.ttl
	client.cacheMaxBytes = cacheDefaults.maxBytes
	cacheDefaults.RUnlock()
}

// SetResponseCache configures a private, bounded cache for public API responses.
func (c *Client) SetResponseCache(directory string, enabled bool, ttl time.Duration, maxBytes int64) error {
	if ttl < 0 || maxBytes <= 0 {
		return errors.New("invalid response cache policy")
	}
	c.cacheDir = directory
	c.cacheEnabled = enabled && ttl > 0
	c.cacheTTL = ttl
	c.cacheMaxBytes = maxBytes
	if !c.cacheEnabled {
		return nil
	}
	if strings.TrimSpace(directory) == "" {
		return errors.New("response cache directory is required")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	return os.Chmod(directory, 0700)
}

func cacheablePath(path string) bool {
	return strings.HasPrefix(path, "/api/") && !strings.Contains(path, "/server/") && !strings.Contains(path, "/graphql")
}

func (c *Client) cacheKey(endpoint, accept string) string {
	tokenHash := sha256.Sum256([]byte(c.token))
	key := sha256.Sum256([]byte(endpoint + "\n" + accept + "\n" + c.language + "\n" + hex.EncodeToString(tokenHash[:])))
	return hex.EncodeToString(key[:]) + ".cache"
}

func (c *Client) cacheRead(name string) ([]byte, bool) {
	responseCacheMu.Lock()
	defer responseCacheMu.Unlock()
	path := filepath.Join(c.cacheDir, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > c.cacheMaxBytes {
		return nil, false
	}
	if time.Since(info.ModTime()) >= c.cacheTTL {
		_ = os.Remove(path)
		return nil, false
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) > maxResponseBytes {
		_ = os.Remove(path)
		return nil, false
	}
	return body, true
}

func (c *Client) cacheWrite(name string, body []byte) {
	if len(body) == 0 || int64(len(body)) > c.cacheMaxBytes {
		return
	}
	responseCacheMu.Lock()
	defer responseCacheMu.Unlock()
	if err := os.MkdirAll(c.cacheDir, 0700); err != nil {
		return
	}
	if err := os.Chmod(c.cacheDir, 0700); err != nil {
		return
	}
	if !c.cacheMakeRoom(name, int64(len(body))) {
		return
	}
	tmp, err := os.CreateTemp(c.cacheDir, ".response-*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	target := filepath.Join(c.cacheDir, name)
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return
		}
		_ = os.Remove(target)
	}
	_ = os.Rename(tmpPath, target)
}

func (c *Client) cacheMakeRoom(target string, added int64) bool {
	entries, err := os.ReadDir(c.cacheDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	type cacheFile struct {
		path    string
		modTime time.Time
		size    int64
	}
	files := make([]cacheFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".cache") || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		file := cacheFile{path: filepath.Join(c.cacheDir, entry.Name()), modTime: info.ModTime(), size: info.Size()}
		files = append(files, file)
		total += file.size
	}
	if targetInfo, err := os.Lstat(filepath.Join(c.cacheDir, target)); err == nil && targetInfo.Mode().IsRegular() {
		total -= targetInfo.Size()
	}
	if total+added <= c.cacheMaxBytes {
		return true
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, file := range files {
		if filepath.Base(file.path) == target {
			continue
		}
		_ = os.Remove(file.path)
		total -= file.size
		if total+added <= c.cacheMaxBytes {
			return true
		}
	}
	return total+added <= c.cacheMaxBytes
}
