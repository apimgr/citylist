package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func Verify(ctx context.Context, path, password string, limits Limits) (Manifest, error) {
	manifest, files, _, err := readArchive(ctx, path, password, limits)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Type == "incremental" {
		basePath := filepath.Join(filepath.Dir(path), manifest.BaseBackup)
		baseManifest, baseFiles, baseChecksum, err := readArchive(ctx, basePath, password, limits)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return Manifest{}, fmt.Errorf("ERR_BASE_MISSING: incremental base backup is unavailable: %w", err)
			}
			return Manifest{}, fmt.Errorf("ERR_BASE_CORRUPT: incremental base backup is invalid: %w", err)
		}
		if baseManifest.Type != "full" {
			return Manifest{}, errors.New("incremental base backup is not a full backup")
		}
		if baseChecksum != manifest.BaseSHA256 {
			return Manifest{}, errors.New("ERR_BASE_CORRUPT: incremental base backup checksum does not match")
		}
		for _, deleted := range manifest.Deleted {
			delete(baseFiles, deleted)
		}
		for key, content := range files {
			baseFiles[key] = content
		}
		files = baseFiles
		if err := verifyCombinedLimits(files, limits); err != nil {
			return Manifest{}, err
		}
	}
	if _, ok := files["config/server.yml"]; !ok {
		return Manifest{}, errors.New("backup manifest is missing server.yml")
	}
	if databaseFile, ok := files["database/server.db"]; ok {
		if err := verifySQLite(ctx, databaseFile, limits.TempDir); err != nil {
			return Manifest{}, fmt.Errorf("database integrity check failed: %w", err)
		}
	}
	return manifest, nil
}

// ExtractVerified writes the already-validated archive contents beneath a
// private destination directory without using archive-controlled file modes.
func ExtractVerified(ctx context.Context, path, password string, limits Limits, destination string) (Manifest, error) {
	manifest, files, _, err := readArchive(ctx, path, password, limits)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return Manifest{}, fmt.Errorf("create verified extraction directory: %w", err)
	}
	root, err := filepath.Abs(destination)
	if err != nil {
		return Manifest{}, err
	}
	for key, content := range files {
		if !filepath.IsLocal(filepath.FromSlash(key)) {
			return Manifest{}, fmt.Errorf("backup contains unsafe extraction path %q", key)
		}
		path := filepath.Join(root, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return Manifest{}, err
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			return Manifest{}, fmt.Errorf("extract verified backup file %q: %w", key, err)
		}
	}
	return manifest, nil
}

func readArchive(ctx context.Context, path, password string, limits Limits) (manifest Manifest, contents map[string][]byte, checksum string, retErr error) {
	limits = normalizeLimits(limits)
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, nil, "", fmt.Errorf("open backup: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return Manifest{}, nil, "", err
	}
	if !info.Mode().IsRegular() {
		return Manifest{}, nil, "", errors.New("backup path is not a regular file")
	}
	if info.Size() <= 0 || info.Size() > limits.MaxArchiveSize {
		return Manifest{}, nil, "", fmt.Errorf("backup archive size %d exceeds the allowed range", info.Size())
	}
	archive, err := io.ReadAll(io.LimitReader(file, limits.MaxArchiveSize+1))
	if err != nil {
		return Manifest{}, nil, "", fmt.Errorf("read backup: %w", err)
	}
	select {
	case <-ctx.Done():
		return Manifest{}, nil, "", ctx.Err()
	default:
	}
	archiveDigest := sha256.Sum256(archive)
	checksum = hex.EncodeToString(archiveDigest[:])
	encrypted := bytes.HasPrefix(archive, []byte(encryptionMark))
	if encrypted {
		if password == "" {
			return Manifest{}, nil, "", errors.New("encrypted backup requires an interactively supplied password")
		}
		archive, err = decrypt(archive, password)
		if err != nil {
			return Manifest{}, nil, "", err
		}
	} else if strings.HasSuffix(path, ".enc") {
		return Manifest{}, nil, "", errors.New("encrypted backup marker is missing")
	}
	if encrypted != strings.HasSuffix(path, ".enc") {
		return Manifest{}, nil, "", errors.New("backup filename extension does not match its encryption state")
	}
	manifest, files, err := decodeArchive(archive, limits)
	if err != nil {
		return Manifest{}, nil, "", err
	}
	if manifest.Encrypted != encrypted {
		return Manifest{}, nil, "", errors.New("backup encryption metadata does not match the archive")
	}
	if manifest.Encrypted && manifest.EncryptionMethod != "AES-256-GCM" {
		return Manifest{}, nil, "", errors.New("backup manifest has an unsupported encryption method")
	}
	if !manifest.Encrypted && manifest.EncryptionMethod != "" {
		return Manifest{}, nil, "", errors.New("unencrypted backup declares an encryption method")
	}
	if manifest.Type != "full" && manifest.Type != "incremental" {
		return Manifest{}, nil, "", errors.New("backup manifest has an invalid type")
	}
	if manifest.Type == "incremental" {
		if manifest.BaseBackup == "" || filepath.Base(manifest.BaseBackup) != manifest.BaseBackup || manifest.BaseSHA256 == "" {
			return Manifest{}, nil, "", errors.New("incremental manifest has invalid base backup metadata")
		}
	}
	return manifest, files, checksum, nil
}

func decodeArchive(data []byte, limits Limits) (manifest Manifest, files map[string][]byte, retErr error) {
	limits = normalizeLimits(limits)
	compressedSize := int64(len(data))
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("invalid gzip archive: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, reader.Close()) }()
	tarReader := tar.NewReader(reader)
	files = make(map[string][]byte)
	manifestFound := false
	var expandedSize int64
	count := 0
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, nil, fmt.Errorf("read tar archive: %w", err)
		}
		count++
		if count > limits.MaxFiles {
			return Manifest{}, nil, errors.New("backup archive exceeds the file count limit")
		}
		if header.Typeflag != tar.TypeReg {
			return Manifest{}, nil, fmt.Errorf("backup contains unsupported tar entry %q", header.Name)
		}
		if header.Size < 0 || header.Size > limits.MaxFileSize {
			return Manifest{}, nil, fmt.Errorf("backup entry %q exceeds the per-file size limit", header.Name)
		}
		expandedSize += header.Size
		if expandedSize > limits.MaxExpandedSize {
			return Manifest{}, nil, errors.New("backup archive exceeds the expanded-size limit")
		}
		if limits.MaxRatio > 0 && compressedSize > 0 && expandedSize > compressedSize*limits.MaxRatio {
			return Manifest{}, nil, errors.New("backup archive exceeds the compression-ratio limit")
		}
		if header.Name == manifestName {
			if manifestFound || header.Size > 1<<20 {
				return Manifest{}, nil, errors.New("backup has a duplicate or oversized manifest")
			}
			encoded, err := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
			if err != nil || int64(len(encoded)) != header.Size {
				return Manifest{}, nil, errors.New("backup manifest is truncated")
			}
			if err := jsonUnmarshalManifest(encoded, &manifest); err != nil {
				return Manifest{}, nil, fmt.Errorf("invalid backup manifest: %w", err)
			}
			manifestFound = true
			continue
		}
		name := filepath.ToSlash(filepath.Clean(header.Name))
		if name != header.Name || !strings.HasPrefix(name, "files/") || !filepath.IsLocal(filepath.FromSlash(name)) {
			return Manifest{}, nil, fmt.Errorf("backup contains an unsafe path %q", header.Name)
		}
		key := strings.TrimPrefix(name, "files/")
		if _, found := files[key]; found {
			return Manifest{}, nil, fmt.Errorf("backup contains duplicate file %q", key)
		}
		content, err := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if err != nil || int64(len(content)) != header.Size {
			return Manifest{}, nil, fmt.Errorf("backup file %q is truncated", key)
		}
		files[key] = content
	}
	if !manifestFound {
		return Manifest{}, nil, errors.New("backup manifest is missing")
	}
	if len(files) != len(manifest.Files) {
		return Manifest{}, nil, errors.New("backup manifest file count does not match the archive")
	}
	if err := verifyManifestContents(manifest, files); err != nil {
		return Manifest{}, nil, err
	}
	return manifest, files, nil
}

func jsonUnmarshalManifest(data []byte, target *Manifest) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("backup manifest contains trailing JSON data")
		}
		return err
	}
	return nil
}

func verifyManifestContents(manifest Manifest, files map[string][]byte) error {
	if manifest.Version != "1.0.0" || manifest.CreatedAt == "" || manifest.SnapshotAt == "" || manifest.SHA256 == "" {
		return errors.New("backup manifest is missing required fields")
	}
	if _, err := time.Parse(time.RFC3339, manifest.CreatedAt); err != nil {
		return errors.New("backup manifest has an invalid created_at timestamp")
	}
	if _, err := time.Parse(time.RFC3339, manifest.SnapshotAt); err != nil {
		return errors.New("backup manifest has an invalid snapshot_at timestamp")
	}
	checksum := sha256.New()
	seen := make(map[string]bool, len(manifest.Files))
	for _, item := range manifest.Files {
		if item.Target != "config" && item.Target != "database" && item.Target != "data" && item.Target != "tls" {
			return fmt.Errorf("backup manifest contains an invalid target %q", item.Target)
		}
		if item.Path == "" || !filepath.IsLocal(filepath.FromSlash(item.Path)) || filepath.ToSlash(filepath.Clean(item.Path)) != item.Path {
			return fmt.Errorf("backup manifest contains an unsafe path %q", item.Path)
		}
		key := item.Target + "/" + item.Path
		if seen[key] {
			return fmt.Errorf("backup manifest contains duplicate path %q", key)
		}
		seen[key] = true
		data, ok := files[key]
		if !ok || int64(len(data)) != item.Size {
			return fmt.Errorf("backup file %q is missing or has the wrong size", key)
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != item.SHA256 {
			return fmt.Errorf("backup file %q checksum does not match", key)
		}
		if _, err := fmt.Fprintf(checksum, "%s\x00%s\x00%s\x00%d\x00%s\x00", item.Target, item.Path, item.SourcePath, item.Size, item.SHA256); err != nil {
			return err
		}
		if _, err := checksum.Write(data); err != nil {
			return err
		}
	}
	if hex.EncodeToString(checksum.Sum(nil)) != manifest.SHA256 {
		return errors.New("backup manifest checksum does not match its contents")
	}
	if len(manifest.Contents) != len(manifest.Files) {
		return errors.New("backup manifest contents list does not match its files")
	}
	for index, item := range manifest.Files {
		if manifest.Contents[index] != item.Target+"/"+item.Path {
			return errors.New("backup manifest contents list does not match its files")
		}
	}
	for _, item := range manifest.Deleted {
		parts := strings.SplitN(item, "/", 2)
		if len(parts) != 2 || (parts[0] != "config" && parts[0] != "database" && parts[0] != "data" && parts[0] != "tls") || !filepath.IsLocal(filepath.FromSlash(parts[1])) {
			return fmt.Errorf("incremental manifest contains an unsafe deletion path %q", item)
		}
	}
	return nil
}

func verifySQLite(ctx context.Context, content []byte, temporaryRoot string) (retErr error) {
	if temporaryRoot == "" {
		return errors.New("project-scoped temporary directory is required for SQLite verification")
	}
	if err := os.MkdirAll(temporaryRoot, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(temporaryRoot, ".citylist-backup-db-*")
	if err != nil {
		return err
	}
	path := file.Name()
	defer func() {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary backup database: %w", err))
		}
	}()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	parsed := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := parsed.Query()
	query.Set("mode", "ro")
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", parsed.String())
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, db.Close()) }()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("SQLite integrity_check returned %q", result)
	}
	return nil
}

func normalizeLimits(limits Limits) Limits {
	defaults := DefaultLimits()
	if limits.MaxArchiveSize <= 0 {
		limits.MaxArchiveSize = defaults.MaxArchiveSize
	}
	if limits.MaxExpandedSize <= 0 {
		limits.MaxExpandedSize = defaults.MaxExpandedSize
	}
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = defaults.MaxFiles
	}
	if limits.MaxFileSize <= 0 {
		limits.MaxFileSize = defaults.MaxFileSize
	}
	if limits.MaxRatio <= 0 {
		limits.MaxRatio = defaults.MaxRatio
	}
	return limits
}

func sortedManifestFiles(files []ManifestFile) []ManifestFile {
	result := append([]ManifestFile(nil), files...)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Target+"/"+result[i].Path < result[j].Target+"/"+result[j].Path
	})
	return result
}
