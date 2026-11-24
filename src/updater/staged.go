package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const stagedBinaryName = "citylist.new"

type stagedRelease struct {
	Version     string `json:"version"`
	PublishedAt int64  `json:"published_at"`
	Binary      string `json:"binary"`
	SHA256      string `json:"sha256"`
}

// Stage downloads and verifies a release into dataDir/update without touching
// the currently running executable.
func Stage(ctx context.Context, client *http.Client, release *Release, currentVersion string, buildEpoch int64, dataDir string) error {
	if release == nil {
		return fmt.Errorf("release is required")
	}
	if !IsNewer(release.TagName, currentVersion, release.PublishedAt.Unix(), buildEpoch) {
		return fmt.Errorf("release %s is not newer than %s", release.TagName, currentVersion)
	}
	if client == nil {
		client = &http.Client{}
	}
	assetName := platformBinaryName()
	assets := make(map[string]string, len(release.Assets))
	for _, asset := range release.Assets {
		assets[asset.Name] = asset.BrowserDownloadURL
	}
	if assets[assetName] == "" || assets["sha256.txt"] == "" {
		return fmt.Errorf("release is missing %s or sha256.txt", assetName)
	}
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

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create update data directory: %w", err)
	}
	tmpDir, err := os.MkdirTemp(dataDir, ".update-stage-")
	if err != nil {
		return fmt.Errorf("create staged update directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	if err := os.Chmod(tmpDir, 0o700); err != nil {
		return fmt.Errorf("secure staged update directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, stagedBinaryName), binary, 0o700); err != nil {
		return fmt.Errorf("write staged update binary: %w", err)
	}
	metadata, err := json.Marshal(stagedRelease{Version: release.TagName, PublishedAt: release.PublishedAt.Unix(), Binary: assetName, SHA256: expected})
	if err != nil {
		return fmt.Errorf("encode staged update metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "release.json"), metadata, 0o600); err != nil {
		return fmt.Errorf("write staged update metadata: %w", err)
	}
	return replaceStagedDirectory(tmpDir, filepath.Join(dataDir, "update"))
}

// ApplyStaged verifies and applies an update staged under dataDir/update.
func ApplyStaged(dataDir, currentVersion string, buildEpoch int64) (bool, error) {
	currentPath, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("locate running executable: %w", err)
	}
	currentPath, err = filepath.EvalSymlinks(currentPath)
	if err != nil {
		return false, fmt.Errorf("resolve running executable: %w", err)
	}
	return applyStagedAt(filepath.Join(dataDir, "update"), currentPath, currentVersion, buildEpoch)
}

// StagedVersion reports the validated version currently waiting in dataDir/update.
func StagedVersion(dataDir string) (string, error) {
	version, _, err := StagedReleaseInfo(dataDir)
	return version, err
}

// StagedReleaseInfo reports the version and publication time waiting in dataDir/update.
func StagedReleaseInfo(dataDir string) (string, int64, error) {
	updateDir := filepath.Join(dataDir, "update")
	info, err := os.Lstat(updateDir)
	if os.IsNotExist(err) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", 0, fmt.Errorf("staged update path is not a directory")
	}
	data, err := os.ReadFile(filepath.Join(updateDir, "release.json"))
	if err != nil {
		return "", 0, err
	}
	var staged stagedRelease
	if err := json.Unmarshal(data, &staged); err != nil || staged.Version == "" {
		return "", 0, fmt.Errorf("staged update metadata is invalid")
	}
	return staged.Version, staged.PublishedAt, nil
}

func applyStagedAt(updateDir, currentPath, currentVersion string, buildEpoch int64) (bool, error) {
	if _, err := os.Lstat(updateDir); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect staged update: %w", err)
	}
	defer func() { _ = os.RemoveAll(updateDir) }()
	info, err := os.Lstat(updateDir)
	if err != nil {
		return false, fmt.Errorf("inspect staged update directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("staged update path is not a directory")
	}
	metadataBytes, err := os.ReadFile(filepath.Join(updateDir, "release.json"))
	if err != nil {
		return false, fmt.Errorf("read staged update metadata: %w", err)
	}
	var metadata stagedRelease
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return false, fmt.Errorf("decode staged update metadata: %w", err)
	}
	if metadata.Binary != platformBinaryName() || !IsNewer(metadata.Version, currentVersion, metadata.PublishedAt, buildEpoch) {
		return false, fmt.Errorf("staged update is not newer than the running version")
	}
	binaryPath := filepath.Join(updateDir, stagedBinaryName)
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		return false, fmt.Errorf("read staged update binary: %w", err)
	}
	actual := sha256.Sum256(binary)
	if len(metadata.SHA256) != sha256.Size*2 || !strings.EqualFold(hex.EncodeToString(actual[:]), metadata.SHA256) {
		return false, fmt.Errorf("staged update checksum mismatch")
	}
	if err := replaceBinary(currentPath, binaryPath); err != nil {
		return false, fmt.Errorf("apply staged update: %w", err)
	}
	return true, nil
}

func replaceStagedDirectory(stagedDir, targetDir string) error {
	if info, err := os.Lstat(targetDir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("existing update path is not a directory")
		}
		previousDir := targetDir + ".previous"
		_ = os.RemoveAll(previousDir)
		if err := os.Rename(targetDir, previousDir); err != nil {
			return fmt.Errorf("preserve prior staged update: %w", err)
		}
		if err := os.Rename(stagedDir, targetDir); err != nil {
			_ = os.Rename(previousDir, targetDir)
			return fmt.Errorf("publish staged update: %w", err)
		}
		_ = os.RemoveAll(previousDir)
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect existing staged update: %w", err)
	}
	if err := os.Rename(stagedDir, targetDir); err != nil {
		return fmt.Errorf("publish staged update: %w", err)
	}
	return nil
}

func platformBinaryName() string {
	name := "citylist-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}
