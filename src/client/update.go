package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/paths"
	"github.com/apimgr/citylist/src/updater"
)

const maxCLIBinaryBytes = 512 << 20

type cliUpdateInfo struct {
	CLIVersions map[string]struct {
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	} `json:"cli_versions"`
	CLIMinVersion string `json:"cli_min_version"`
}

// checkCLIUpdate reads the optional update contract exposed by a server.
func checkCLIUpdate(ctx context.Context, serverURL, token, version string, timeout time.Duration) (*cliUpdateInfo, bool, error) {
	endpoint, err := cliServerURL(serverURL, "/api/autodiscover")
	if err != nil {
		return nil, false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", ProjectName+"-cli/"+version)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		return nil, false, fmt.Errorf("check CLI update: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
		return nil, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("autodiscover returned HTTP %d", response.StatusCode)
	}
	var info cliUpdateInfo
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&info); err != nil {
		return nil, true, fmt.Errorf("decode autodiscover: %w", err)
	}
	return &info, true, nil
}

func maybeUpdateCLI(args []string, input io.Reader, output, errorOutput io.Writer, serverURL, token, version string, timeout time.Duration, cfg cliConfig) (bool, int) {
	requested := ""
	for index, argument := range args {
		if argument == "--update" && index+1 < len(args) {
			requested = args[index+1]
		} else if strings.HasPrefix(argument, "--update=") {
			requested = strings.TrimPrefix(argument, "--update=")
		}
	}
	if requested != "" && requested != "check" && requested != "yes" {
		_, _ = fmt.Fprintln(errorOutput, "--update must be check or yes")
		return true, 2
	}
	if serverURL == "" {
		if requested != "" {
			_, _ = fmt.Fprintln(errorOutput, "configure a server before checking for CLI updates")
			return true, 2
		}
		return false, 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	info, supported, err := checkCLIUpdate(ctx, serverURL, token, version, timeout)
	cancel()
	if err != nil {
		if requested != "" {
			_, _ = fmt.Fprintln(errorOutput, err)
			return true, 1
		}
		return false, 0
	}
	if !supported || info == nil {
		if requested != "" {
			_, _ = fmt.Fprintln(output, "This server does not publish CLI update information.")
			return true, 0
		}
		return false, 0
	}
	minimumRequired := info.CLIMinVersion != "" && updater.IsNewer(info.CLIMinVersion, version, 0, 0)
	if minimumRequired && requested != "yes" {
		auditCLIEvent("cli.update_forced", map[string]string{"current_version": version, "minimum": info.CLIMinVersion})
		_, _ = fmt.Fprintf(errorOutput, "this CLI is too old; the server requires %s — run '%s-cli --update yes' to upgrade.\n", info.CLIMinVersion, ProjectName)
		return true, 1
	}
	target, exists := info.CLIVersions[runtime.GOOS+"-"+runtime.GOARCH]
	if !exists || target.Version == "" || !updater.IsNewer(target.Version, version, 0, 0) {
		if minimumRequired {
			auditCLIEvent("cli.update_forced", map[string]string{"current_version": version, "minimum": info.CLIMinVersion})
			_, _ = fmt.Fprintf(errorOutput, "the server requires CLI %s but provides no compatible update for %s-%s\n", info.CLIMinVersion, runtime.GOOS, runtime.GOARCH)
			return true, 1
		}
		if requested != "" {
			_, _ = fmt.Fprintf(output, "%s is up to date.\n", ProjectName+"-cli")
			return true, 0
		}
		return false, 0
	}
	if minimumRequired && updater.IsNewer(info.CLIMinVersion, target.Version, 0, 0) {
		_, _ = fmt.Fprintf(errorOutput, "the available CLI update %s does not meet the server minimum %s\n", target.Version, info.CLIMinVersion)
		return true, 1
	}
	if requested == "check" {
		_, _ = fmt.Fprintf(output, "CLI update available: %s → %s\n", version, target.Version)
		return true, 0
	}
	interactive := isInteractive(input)
	shouldUpdate := requested == "yes" && (interactive || cfg.Update.Auto)
	if interactive && requested != "yes" {
		_, _ = fmt.Fprintf(output, "CLI update %s is available. Install it now? [y/N] ", target.Version)
		line, _ := bufio.NewReader(input).ReadString('\n')
		shouldUpdate = strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes")
	}
	if !shouldUpdate {
		if requested == "yes" {
			_, _ = fmt.Fprintln(errorOutput, "automatic CLI updates require update.auto: true for non-interactive runs")
			return true, 1
		}
		if minimumRequired {
			auditCLIEvent("cli.update_forced", map[string]string{"current_version": version, "minimum": info.CLIMinVersion})
			_, _ = fmt.Fprintf(errorOutput, "this CLI is too old; the server requires %s — run '%s-cli --update yes' to upgrade.\n", info.CLIMinVersion, ProjectName)
			return true, 1
		}
		return false, 0
	}
	if !validSHA256(target.SHA256) {
		auditCLIEvent("cli.update_checksum_invalid", map[string]string{"current_version": version, "new_version": target.Version, "os_arch": runtime.GOOS + "-" + runtime.GOARCH})
		_, _ = fmt.Fprintln(errorOutput, "autodiscover supplied an invalid CLI SHA-256")
		return true, 1
	}
	binaryURL, err := cliServerURL(serverURL, "/cli/binaries/"+ProjectName+"-cli-"+runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, err)
		return true, 1
	}
	if err := downloadAndReplaceCLI(binaryURL, target.SHA256, token, timeout); err != nil {
		_, _ = fmt.Fprintln(errorOutput, err)
		return true, 1
	}
	auditCLIEvent("cli.update_completed", map[string]string{"old_version": version, "new_version": target.Version, "os_arch": runtime.GOOS + "-" + runtime.GOARCH})
	_, _ = fmt.Fprintf(errorOutput, "Updated %s from %s to %s; restarting.\n", ProjectName+"-cli", version, target.Version)
	if err := restartUpdatedCLI(); err != nil {
		_, _ = fmt.Fprintln(errorOutput, err)
		return true, 1
	}
	return true, 0
}

func downloadAndReplaceCLI(source, expectedHash, token string, timeout time.Duration) error {
	if err := validateCLIDownloadURL(source); err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", ProjectName+"-cli/"+Version)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if err := validateCLIDownloadURL(request.URL.String()); err != nil {
			return err
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download CLI update: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("CLI binary endpoint returned HTTP %d", response.StatusCode)
	}
	tmpDir, cleanup, err := paths.NewProjectTempDir(InternalName)
	if err != nil {
		return fmt.Errorf("create update temp directory: %w", err)
	}
	defer cleanup()
	stagedPath := filepath.Join(tmpDir, "cli.update.tmp")
	file, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return fmt.Errorf("create staged CLI update: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxCLIBinaryBytes+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("write staged CLI update: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close staged CLI update: %w", closeErr)
	}
	if syncErr != nil {
		return fmt.Errorf("sync staged CLI update: %w", syncErr)
	}
	if written > maxCLIBinaryBytes {
		return errors.New("CLI update exceeds the 512 MiB limit")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expectedHash) {
		auditCLIEvent("cli.update_checksum_invalid", map[string]string{"new_version": "unknown", "os_arch": runtime.GOOS + "-" + runtime.GOARCH})
		return errors.New("CLI update checksum verification failed")
	}
	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate CLI executable: %w", err)
	}
	currentPath, err = filepath.EvalSymlinks(currentPath)
	if err != nil {
		return fmt.Errorf("resolve CLI executable: %w", err)
	}
	if err := replaceClientBinary(currentPath, stagedPath); err != nil {
		return fmt.Errorf("you do not have permission to update %s; ask your admin or move the binary to a writable path: %w", currentPath, err)
	}
	return nil
}

func cliServerURL(base, route string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("server URL is invalid")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + route
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func validateCLIDownloadURL(source string) error {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return errors.New("refusing invalid CLI download URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && (host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
		return nil
	}
	return errors.New("refusing non-HTTPS CLI download URL")
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func auditCLIEvent(event string, fields map[string]string) {
	if activeCLILogger != nil {
		activeCLILogger.Event(event, fields)
		return
	}
	directory := cliLogDirectory()
	if err := os.MkdirAll(directory, 0700); err != nil {
		return
	}
	filename := filepath.Join(directory, "cli.log")
	record := map[string]any{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "event": event}
	for key, value := range fields {
		record[key] = value
	}
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	_, _ = file.Write(append(data, '\n'))
	_ = file.Close()
}

func cliLogDirectory() string {
	if runtime.GOOS == "windows" {
		root := os.Getenv("LOCALAPPDATA")
		if root == "" {
			home, _ := os.UserHomeDir()
			root = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(root, InternalOrg, InternalName, "log")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "log", InternalOrg, InternalName)
}

func restartCLIArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] == "--update" && index+1 < len(args) && (args[index+1] == "yes" || args[index+1] == "check") {
			index++
			continue
		}
		if strings.HasPrefix(args[index], "--update=") {
			continue
		}
		result = append(result, args[index])
	}
	return result
}
