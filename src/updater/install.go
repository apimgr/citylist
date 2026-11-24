package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maxUpdateBytes = 512 << 20

// Install downloads and verifies the release binary before replacing the running executable.
func Install(ctx context.Context, client *http.Client, release *Release, tempRoot string) error {
	if release == nil {
		return fmt.Errorf("release is required")
	}
	if client == nil {
		client = &http.Client{}
	}
	assetName := "citylist-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}
	assets := make(map[string]string, len(release.Assets))
	for _, asset := range release.Assets {
		assets[asset.Name] = asset.BrowserDownloadURL
	}
	if assets[assetName] == "" || assets["sha256.txt"] == "" {
		return fmt.Errorf("release is missing %s or sha256.txt", assetName)
	}
	tmpParent := filepath.Join(tempRoot, "apimgr")
	if err := os.MkdirAll(tmpParent, 0o700); err != nil {
		return fmt.Errorf("create update temp parent: %w", err)
	}
	tmpDir, err := os.MkdirTemp(tmpParent, "citylist-")
	if err != nil {
		return fmt.Errorf("create update temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manifest, err := download(ctx, client, assets["sha256.txt"], 1<<20)
	if err != nil {
		return fmt.Errorf("download checksum manifest: %w", err)
	}
	expected, err := checksumFor(string(manifest), assetName)
	if err != nil {
		return err
	}
	binary, err := download(ctx, client, assets[assetName], maxUpdateBytes)
	if err != nil {
		return fmt.Errorf("download update binary: %w", err)
	}
	actual := sha256.Sum256(binary)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), expected) {
		return fmt.Errorf("update checksum mismatch")
	}
	stagedPath := filepath.Join(tmpDir, assetName)
	mode := os.FileMode(0o755)
	if runtime.GOOS == "windows" {
		mode = 0o700
	}
	if err := os.WriteFile(stagedPath, binary, mode); err != nil {
		return fmt.Errorf("write verified update: %w", err)
	}
	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running executable: %w", err)
	}
	currentPath, err = filepath.EvalSymlinks(currentPath)
	if err != nil {
		return fmt.Errorf("resolve running executable: %w", err)
	}
	return replaceBinary(currentPath, stagedPath)
}

func download(ctx context.Context, client *http.Client, source string, limit int64) ([]byte, error) {
	if err := validateDownloadURL(source); err != nil {
		return nil, err
	}
	safeClient := *client
	previousRedirect := safeClient.CheckRedirect
	safeClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if err := validateDownloadURL(request.URL.String()); err != nil {
			return err
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	response, err := safeClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("download exceeds size limit")
	}
	return body, nil
}

func validateDownloadURL(source string) error {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("refusing non-HTTPS update URL")
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return nil
	}
	if !strings.EqualFold(parsed.Scheme, "http") {
		return fmt.Errorf("refusing non-HTTPS update URL")
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing non-HTTPS update URL")
}

func checksumFor(manifest, name string) (string, error) {
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			if len(fields[0]) != sha256.Size*2 {
				break
			}
			if _, err := hex.DecodeString(fields[0]); err != nil {
				break
			}
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum manifest has no valid entry for %s", name)
}
