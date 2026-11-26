package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type RestoreOptions struct {
	ConfigDir    string
	ConfigFile   string
	DataDir      string
	DatabasePath string
	TempDir      string
	TLSFiles     []string
}

func Restore(ctx context.Context, backupPath, password string, options RestoreOptions, limits Limits) (Manifest, error) {
	if options.ConfigDir == "" || options.ConfigFile == "" || options.DataDir == "" || options.TempDir == "" {
		return Manifest{}, errors.New("restore config, data and project-scoped temporary directories are required")
	}
	limits = normalizeLimits(limits)
	if limits.TempDir == "" {
		limits.TempDir = options.TempDir
	}
	manifest, files, _, err := readArchive(ctx, backupPath, password, limits)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Type == "incremental" {
		if filepath.Base(manifest.BaseBackup) != manifest.BaseBackup {
			return Manifest{}, errors.New("incremental base backup name is unsafe")
		}
		basePath := filepath.Join(filepath.Dir(backupPath), manifest.BaseBackup)
		baseManifest, baseFiles, baseChecksum, err := readArchive(ctx, basePath, password, limits)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return Manifest{}, fmt.Errorf("ERR_BASE_MISSING: incremental base backup is unavailable: %w", err)
			}
			return Manifest{}, fmt.Errorf("ERR_BASE_CORRUPT: incremental base backup is invalid: %w", err)
		}
		if baseManifest.Type != "full" || baseChecksum != manifest.BaseSHA256 {
			return Manifest{}, errors.New("ERR_BASE_CORRUPT: incremental base backup checksum does not match")
		}
		for _, deleted := range manifest.Deleted {
			delete(baseFiles, deleted)
		}
		for key, value := range files {
			baseFiles[key] = value
		}
		files = baseFiles
		manifest.Files = mergedManifestFiles(baseManifest.Files, manifest.Files, manifest.Deleted)
		manifest.Contents = make([]string, 0, len(manifest.Files))
		for _, file := range manifest.Files {
			manifest.Contents = append(manifest.Contents, file.Target+"/"+file.Path)
		}
		if _, ok := files["config/server.yml"]; !ok {
			return Manifest{}, errors.New("ERR_BASE_CORRUPT: restored configuration is missing")
		}
	} else {
		if _, ok := files["config/server.yml"]; !ok {
			return Manifest{}, errors.New("backup manifest is missing server.yml")
		}
	}
	if err := verifyCombinedLimits(files, limits); err != nil {
		return Manifest{}, err
	}
	if err := rewriteExternalTLSConfig(files, manifest.Files, options.ConfigDir); err != nil {
		return Manifest{}, err
	}
	if databaseContent, ok := files["database/server.db"]; ok {
		if err := verifySQLite(ctx, databaseContent, limits.TempDir); err != nil {
			return Manifest{}, fmt.Errorf("database integrity check failed: %w", err)
		}
	}
	plans, err := makeRestorePlan(files, manifest.Files, options)
	if err != nil {
		return Manifest{}, err
	}
	if err := applyRestorePlan(ctx, plans, options.TempDir); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

type restoreFile struct {
	path    string
	content []byte
	mode    os.FileMode
}

type originalFile struct {
	path    string
	backup  string
	existed bool
	mode    os.FileMode
}

func makeRestorePlan(files map[string][]byte, manifestFiles []ManifestFile, options RestoreOptions) ([]restoreFile, error) {
	allowedTLS := make(map[string]bool, len(options.TLSFiles))
	for _, path := range options.TLSFiles {
		if path != "" {
			absolute, err := filepath.Abs(path)
			if err != nil {
				return nil, err
			}
			allowedTLS[filepath.Clean(absolute)] = true
		}
	}
	plans := make([]restoreFile, 0, len(files))
	for key, content := range files {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) != 2 || !filepath.IsLocal(filepath.FromSlash(parts[1])) {
			return nil, fmt.Errorf("archive contains unsafe destination %q", key)
		}
		relative := filepath.FromSlash(parts[1])
		var target string
		mode := os.FileMode(0600)
		switch parts[0] {
		case "config":
			if relative == "server.yml" {
				target = options.ConfigFile
			} else {
				target = filepath.Join(options.ConfigDir, relative)
			}
		case "database":
			if relative != "server.db" {
				return nil, fmt.Errorf("archive contains unexpected database path %q", relative)
			}
			if options.DatabasePath == "" {
				return nil, errors.New("backup contains server.db but no restore database destination is configured")
			}
			target = options.DatabasePath
			mode = 0600
		case "data":
			target = filepath.Join(options.DataDir, relative)
		case "tls":
			var manifestPath string
			for _, file := range manifestFiles {
				if file.Target+"/"+file.Path == key {
					manifestPath = file.SourcePath
					break
				}
			}
			if manifestPath == "" {
				return nil, fmt.Errorf("archive TLS entry %q has no original path", relative)
			}
			absolute, err := filepath.Abs(manifestPath)
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(relative, "ssl-external/") {
				destination := filepath.Base(relative)
				if destination != "cert.pem" && destination != "key.pem" {
					return nil, fmt.Errorf("archive contains unexpected external TLS file %q", relative)
				}
				target = filepath.Join(options.ConfigDir, "ssl", destination)
				mode = 0600
			} else if !allowedTLS[filepath.Clean(absolute)] {
				return nil, fmt.Errorf("TLS restore destination %q is not allowed by the current configuration", manifestPath)
			} else {
				target = absolute
			}
		default:
			return nil, fmt.Errorf("archive contains unsupported target %q", parts[0])
		}
		plans = append(plans, restoreFile{path: target, content: content, mode: mode})
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].path < plans[j].path })
	for index := 1; index < len(plans); index++ {
		if plans[index-1].path == plans[index].path {
			return nil, fmt.Errorf("archive maps multiple files to restore destination %q", plans[index].path)
		}
	}
	return plans, nil
}

func rewriteExternalTLSConfig(files map[string][]byte, manifestFiles []ManifestFile, configDir string) error {
	certPath, keyPath := "", ""
	for _, file := range manifestFiles {
		if file.Target != "tls" || !strings.HasPrefix(file.Path, "ssl-external/") {
			continue
		}
		destination := filepath.Join(configDir, "ssl", filepath.Base(file.Path))
		absolute, err := filepath.Abs(destination)
		if err != nil {
			return err
		}
		switch filepath.Base(file.Path) {
		case "cert.pem":
			certPath = absolute
		case "key.pem":
			keyPath = absolute
		}
	}
	if certPath == "" && keyPath == "" {
		return nil
	}
	var configuration map[string]any
	if err := yaml.Unmarshal(files["config/server.yml"], &configuration); err != nil {
		return fmt.Errorf("rewrite restored TLS paths: parse server.yml: %w", err)
	}
	server, ok := configuration["server"].(map[string]any)
	if !ok {
		return errors.New("rewrite restored TLS paths: server configuration is missing")
	}
	ssl, ok := server["ssl"].(map[string]any)
	if !ok {
		return errors.New("rewrite restored TLS paths: server.ssl configuration is missing")
	}
	if certPath != "" {
		ssl["cert"] = certPath
	}
	if keyPath != "" {
		ssl["key"] = keyPath
	}
	server["ssl"] = ssl
	configuration["server"] = server
	updated, err := yaml.Marshal(configuration)
	if err != nil {
		return fmt.Errorf("rewrite restored TLS paths: serialize server.yml: %w", err)
	}
	files["config/server.yml"] = updated
	return nil
}

func applyRestorePlan(ctx context.Context, plans []restoreFile, temporaryRoot string) (retErr error) {
	if err := os.MkdirAll(temporaryRoot, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(temporaryRoot, ".citylist-restore-*")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove restore staging directory: %w", cleanupErr))
		}
	}()
	if err := os.Chmod(stage, 0700); err != nil {
		return err
	}
	originals := make([]originalFile, 0, len(plans))
	for index, plan := range plans {
		if err := validateDestination(plan.path); err != nil {
			return err
		}
		entry := originalFile{path: plan.path}
		info, err := os.Lstat(plan.path)
		if err == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("restore destination %q is not a regular file", plan.path)
			}
			entry.existed = true
			entry.mode = info.Mode().Perm()
			entry.backup = filepath.Join(stage, fmt.Sprintf("original-%08d", index))
			if err := copyFile(plan.path, entry.backup, 0600); err != nil {
				return fmt.Errorf("stage original %q: %w", plan.path, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		originals = append(originals, entry)
	}
	for index, plan := range plans {
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), rollback(originals[:index]))
		default:
		}
		if err := os.MkdirAll(filepath.Dir(plan.path), 0700); err != nil {
			return errors.Join(fmt.Errorf("create restore destination directory: %w", err), rollback(originals[:index]))
		}
		if err := writeAtomic(plan.path, plan.content, plan.mode); err != nil {
			return errors.Join(fmt.Errorf("restore %q: %w", plan.path, err), rollback(originals[:index]))
		}
	}
	return nil
}

func validateDestination(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("restore destination %q is not absolute", path)
	}
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	current := string(filepath.Separator)
	if volume != "" {
		current = volume + string(filepath.Separator)
	}
	for _, part := range strings.Split(strings.TrimPrefix(clean, current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore destination traverses symlink %q", current)
		}
		if current != clean && !info.IsDir() {
			return fmt.Errorf("restore destination parent %q is not a directory", current)
		}
	}
	return nil
}

func rollback(originals []originalFile) error {
	var failures []error
	for index := len(originals) - 1; index >= 0; index-- {
		entry := originals[index]
		if entry.existed {
			content, err := os.ReadFile(entry.backup)
			if err == nil {
				err = writeAtomic(entry.path, content, entry.mode)
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("rollback %q: %w", entry.path, err))
			}
		} else {
			if err := os.Remove(entry.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, fmt.Errorf("rollback remove %q: %w", entry.path, err))
			}
		}
	}
	return errors.Join(failures...)
}

func copyFile(source, destination string, mode os.FileMode) (retErr error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, input.Close()) }()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	return errors.Join(copyErr, closeErr)
}

func mergedManifestFiles(base, increment []ManifestFile, deleted []string) []ManifestFile {
	result := make(map[string]ManifestFile, len(base)+len(increment))
	for _, file := range base {
		result[file.Target+"/"+file.Path] = file
	}
	for _, path := range deleted {
		delete(result, path)
	}
	for _, file := range increment {
		result[file.Target+"/"+file.Path] = file
	}
	files := make([]ManifestFile, 0, len(result))
	for _, file := range result {
		files = append(files, file)
	}
	return sortedManifestFiles(files)
}

func verifyCombinedLimits(files map[string][]byte, limits Limits) error {
	limits = normalizeLimits(limits)
	if len(files) > limits.MaxFiles {
		return errors.New("restored backup exceeds the file count limit")
	}
	var total int64
	for path, content := range files {
		if int64(len(content)) > limits.MaxFileSize {
			return fmt.Errorf("restored file %q exceeds the per-file size limit", path)
		}
		total += int64(len(content))
		if total > limits.MaxExpandedSize {
			return errors.New("restored backup exceeds the expanded-size limit")
		}
	}
	return nil
}
