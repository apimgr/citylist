package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	manifestName   = "manifest.json"
	encryptionMark = "CITYLIST-BACKUP-ENC-1\n"
	argonTime      = uint32(3)
	argonMemory    = uint32(64 * 1024)
	argonThreads   = uint8(1)
	argonKeyBytes  = uint32(32)
)

type SnapshotDatabase func(context.Context, string) error

type ExternalTLSFile struct {
	Name string
	Path string
}

type Options struct {
	ProjectName       string
	AppVersion        string
	ConfigDir         string
	ConfigFile        string
	DataDir           string
	DatabasePath      string
	BackupDir         string
	TempDir           string
	TLSFiles          []string
	ExternalTLSFiles  []ExternalTLSFile
	IncludeSSL        bool
	IncludeData       bool
	Password          string
	ComplianceEnabled bool
	SnapshotDatabase  SnapshotDatabase
	Now               func() time.Time
}

type Manifest struct {
	Version          string         `json:"version"`
	CreatedAt        string         `json:"created_at"`
	CreatedBy        string         `json:"created_by"`
	AppVersion       string         `json:"app_version"`
	Type             string         `json:"type"`
	SnapshotAt       string         `json:"snapshot_at"`
	Contents         []string       `json:"contents"`
	Files            []ManifestFile `json:"files"`
	Encrypted        bool           `json:"encrypted"`
	EncryptionMethod string         `json:"encryption_method,omitempty"`
	SHA256           string         `json:"sha256"`
	BaseBackup       string         `json:"base_backup,omitempty"`
	BaseSHA256       string         `json:"base_sha256,omitempty"`
	Deleted          []string       `json:"deleted,omitempty"`
}

type ManifestFile struct {
	Target     string `json:"target"`
	Path       string `json:"path"`
	SourcePath string `json:"source_path,omitempty"`
	Size       int64  `json:"size"`
	MTime      int64  `json:"mtime"`
	SHA256     string `json:"sha256"`
}

type Limits struct {
	MaxArchiveSize  int64
	MaxExpandedSize int64
	MaxFiles        int
	MaxFileSize     int64
	MaxRatio        int64
	TempDir         string
}

func DefaultLimits() Limits {
	return Limits{MaxArchiveSize: 1 << 30, MaxExpandedSize: 4 << 30, MaxFiles: 10000, MaxFileSize: 512 << 20, MaxRatio: 100}
}

func (o Options) normalized() (Options, error) {
	if o.ProjectName == "" || o.ConfigDir == "" || o.DataDir == "" || o.BackupDir == "" {
		return o, errors.New("backup project, config, data and backup directories are required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ConfigFile == "" {
		o.ConfigFile = filepath.Join(o.ConfigDir, "server.yml")
	}
	if o.DatabasePath == "" {
		o.DatabasePath = filepath.Join(o.DataDir, "db", "server.db")
	}
	if o.ComplianceEnabled && strings.TrimSpace(o.Password) == "" {
		return o, errors.New("backup refused: set server.backup.encryption_password in secrets.yml while compliance mode is enabled")
	}
	if o.TempDir == "" {
		return o, errors.New("project-scoped temporary directory is required for backups")
	}
	return o, nil
}

func Create(ctx context.Context, options Options, outputPath string) (Manifest, error) {
	return create(ctx, options, outputPath, "full", "", time.Time{})
}

func CreateIncremental(ctx context.Context, options Options, outputPath, basePath string, snapshotAt time.Time) (Manifest, error) {
	if basePath == "" {
		return Manifest{}, errors.New("incremental backup requires a base full backup")
	}
	return create(ctx, options, outputPath, "incremental", basePath, snapshotAt)
}

func create(ctx context.Context, options Options, outputPath, backupType, basePath string, snapshotAt time.Time) (manifest Manifest, err error) {
	options, err = options.normalized()
	if err != nil {
		return manifest, err
	}
	if outputPath == "" {
		stamp := options.Now().UTC().Format("2006-01-02_150405")
		outputPath = filepath.Join(options.BackupDir, options.ProjectName+"_backup_"+stamp+".tar.gz")
	} else if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(options.BackupDir, outputPath)
	}
	backupDir, err := filepath.Abs(options.BackupDir)
	if err != nil {
		return manifest, err
	}
	outputPath, err = filepath.Abs(outputPath)
	if err != nil {
		return manifest, err
	}
	relativeOutput, err := filepath.Rel(backupDir, outputPath)
	if err != nil || !filepath.IsLocal(relativeOutput) || filepath.Dir(relativeOutput) != "." {
		return manifest, errors.New("backup filename must be directly inside the configured backup directory")
	}
	if options.Password != "" && !strings.HasSuffix(outputPath, ".enc") {
		outputPath += ".enc"
	}
	if options.Password == "" && strings.HasSuffix(outputPath, ".enc") {
		return manifest, errors.New("encrypted backup filename requires server.backup.encryption_password")
	}
	if err = os.MkdirAll(filepath.Dir(outputPath), 0700); err != nil {
		return manifest, fmt.Errorf("create backup directory: %w", err)
	}
	workingParent := options.TempDir
	if err := os.MkdirAll(workingParent, 0700); err != nil {
		return manifest, fmt.Errorf("create backup temporary directory: %w", err)
	}
	workingDir, err := os.MkdirTemp(workingParent, ".citylist-backup-*")
	if err != nil {
		return manifest, fmt.Errorf("create backup workspace: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(workingDir); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove backup workspace: %w", cleanupErr))
		}
	}()
	if err := os.Chmod(workingDir, 0700); err != nil {
		return manifest, err
	}
	snapshot := options.Now().UTC()
	if !snapshotAt.IsZero() {
		snapshot = snapshotAt.UTC()
	}
	databasePath := ""
	if options.SnapshotDatabase != nil {
		databasePath = filepath.Join(workingDir, "server.db")
		if err := options.SnapshotDatabase(ctx, databasePath); err != nil {
			return manifest, fmt.Errorf("snapshot database: %w", err)
		}
	}
	sources, err := collectSources(ctx, options, databasePath)
	if err != nil {
		return manifest, err
	}
	if len(sources) == 0 {
		return manifest, errors.New("backup has no files")
	}
	if backupType == "incremental" {
		if !filepath.IsAbs(basePath) {
			basePath = filepath.Join(backupDir, basePath)
		}
		basePath, err = filepath.Abs(basePath)
		if err != nil {
			return manifest, err
		}
		baseRelative, err := filepath.Rel(backupDir, basePath)
		if err != nil || !filepath.IsLocal(baseRelative) || filepath.Dir(baseRelative) != "." || basePath == outputPath {
			return manifest, errors.New("incremental base backup must be a different file directly inside the configured backup directory")
		}
		baseInfo, err := os.Lstat(basePath)
		if err != nil {
			return manifest, fmt.Errorf("inspect incremental base backup: %w", err)
		}
		if !baseInfo.Mode().IsRegular() || baseInfo.Mode()&os.ModeSymlink != 0 {
			return manifest, errors.New("incremental base backup is not a regular file")
		}
		baseManifest, _, baseFile, err := readArchive(ctx, basePath, options.Password, DefaultLimits())
		if err != nil {
			return manifest, fmt.Errorf("read base backup: %w", err)
		}
		if baseManifest.Type != "full" {
			return manifest, errors.New("incremental base must be a full backup")
		}
		baseMap := make(map[string]ManifestFile, len(baseManifest.Files))
		for _, file := range baseManifest.Files {
			baseMap[file.Target+"/"+file.Path] = file
		}
		currentMap := make(map[string]sourceFile, len(sources))
		for _, source := range sources {
			key := source.file.Target + "/" + source.file.Path
			currentMap[key] = source
		}
		var delta []sourceFile
		for key, source := range currentMap {
			previous, found := baseMap[key]
			if !found || previous.Size != source.file.Size || previous.MTime != source.file.MTime || previous.SHA256 != source.file.SHA256 || previous.SourcePath != source.file.SourcePath {
				delta = append(delta, source)
			}
		}
		var deleted []string
		for key := range baseMap {
			if _, found := currentMap[key]; !found {
				deleted = append(deleted, key)
			}
		}
		sort.Strings(deleted)
		sources = delta
		manifest.BaseBackup = filepath.Base(basePath)
		manifest.BaseSHA256 = baseFile
		manifest.Deleted = deleted
	}
	sort.Slice(sources, func(i, j int) bool {
		left, right := sources[i].file.Target+"/"+sources[i].file.Path, sources[j].file.Target+"/"+sources[j].file.Path
		return left < right
	})
	manifest.Version = "1.0.0"
	manifest.CreatedAt = options.Now().UTC().Format(time.RFC3339)
	manifest.CreatedBy = "operator"
	manifest.AppVersion = options.AppVersion
	manifest.Type = backupType
	manifest.SnapshotAt = snapshot.Format(time.RFC3339)
	manifest.Encrypted = options.Password != ""
	if manifest.Encrypted {
		manifest.EncryptionMethod = "AES-256-GCM"
	}
	var checksumInput bytes.Buffer
	for _, source := range sources {
		manifest.Files = append(manifest.Files, source.file)
		manifest.Contents = append(manifest.Contents, source.file.Target+"/"+source.file.Path)
		if err := writeChecksumPart(&checksumInput, source.file, source.path); err != nil {
			return manifest, err
		}
	}
	checksum := sha256.Sum256(checksumInput.Bytes())
	manifest.SHA256 = hex.EncodeToString(checksum[:])
	archive, err := makeArchive(sources, manifest)
	if err != nil {
		return manifest, err
	}
	if manifest.Encrypted {
		archive, err = encrypt(archive, options.Password)
		if err != nil {
			return manifest, err
		}
	}
	verifyLimits := DefaultLimits()
	verifyLimits.TempDir = workingDir
	if err := publishVerifiedArchive(ctx, outputPath, archive, options.Password, verifyLimits); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func publishVerifiedArchive(ctx context.Context, outputPath string, archive []byte, password string, limits Limits) error {
	temporary, err := os.CreateTemp(filepath.Dir(outputPath), ".citylist-backup-verify-*")
	if err != nil {
		return fmt.Errorf("create backup archive: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(archive); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write backup archive: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync backup archive: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close backup archive: %w", err)
	}
	if strings.HasSuffix(outputPath, ".enc") {
		verifyPath := temporaryPath + ".enc"
		if err := os.Rename(temporaryPath, verifyPath); err != nil {
			return fmt.Errorf("prepare encrypted backup verification: %w", err)
		}
		temporaryPath = verifyPath
	}
	if _, err := Verify(ctx, temporaryPath, password, limits); err != nil {
		return fmt.Errorf("verify created backup: %w", err)
	}
	previousPath := ""
	if info, err := os.Lstat(outputPath); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup destination %q is not a regular file", outputPath)
		}
		previous, err := os.CreateTemp(filepath.Dir(outputPath), ".citylist-backup-previous-*")
		if err != nil {
			return fmt.Errorf("preserve existing backup: %w", err)
		}
		previousPath = previous.Name()
		if err := previous.Close(); err != nil {
			_ = os.Remove(previousPath)
			return err
		}
		if err := os.Remove(previousPath); err != nil {
			return err
		}
		if err := os.Rename(outputPath, previousPath); err != nil {
			return fmt.Errorf("preserve existing backup: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect backup destination: %w", err)
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		if previousPath != "" {
			if restoreErr := os.Rename(previousPath, outputPath); restoreErr != nil {
				return errors.Join(fmt.Errorf("publish verified backup: %w", err), fmt.Errorf("restore previous backup: %w", restoreErr))
			}
		}
		return fmt.Errorf("publish verified backup: %w", err)
	}
	if previousPath != "" {
		if err := os.Remove(previousPath); err != nil {
			return fmt.Errorf("remove replaced backup: %w", err)
		}
	}
	return nil
}

type sourceFile struct {
	path string
	file ManifestFile
}

func collectSources(ctx context.Context, options Options, databasePath string) ([]sourceFile, error) {
	var result []sourceFile
	add := func(sourcePath, target, name string) error {
		info, err := os.Lstat(sourcePath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse symlink backup source %q", sourcePath)
		}
		if info.IsDir() {
			return filepath.WalkDir(sourcePath, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if path == sourcePath || entry.IsDir() {
					return nil
				}
				if target == "data" {
					cleanPath := filepath.Clean(path)
					cleanDatabasePath := filepath.Clean(options.DatabasePath)
					if cleanPath == cleanDatabasePath || strings.HasPrefix(cleanPath, cleanDatabasePath+"-") {
						return nil
					}
				}
				if entry.Type()&os.ModeSymlink != 0 {
					return fmt.Errorf("refuse symlink backup source %q", path)
				}
				relative, err := filepath.Rel(sourcePath, path)
				if err != nil {
					return err
				}
				return addFile(path, target, filepath.ToSlash(filepath.Join(name, relative)), "", &result)
			})
		}
		return addFile(sourcePath, target, name, "", &result)
	}
	if _, err := os.Stat(options.ConfigFile); err != nil {
		return nil, fmt.Errorf("required server.yml is unavailable: %w", err)
	}
	if databasePath != "" {
		if _, err := os.Stat(databasePath); err != nil {
			return nil, fmt.Errorf("server.db snapshot is unavailable: %w", err)
		}
	}
	if err := add(options.ConfigFile, "config", "server.yml"); err != nil {
		return nil, fmt.Errorf("backup config: %w", err)
	}
	for _, item := range []struct{ source, archive string }{
		{filepath.Join(options.ConfigDir, "yaml.d"), "yaml.d"},
		{filepath.Join(options.ConfigDir, "secrets.yml"), "secrets.yml"},
		{filepath.Join(options.ConfigDir, "assets"), "assets"},
		{filepath.Join(options.ConfigDir, "security"), "security"},
		{filepath.Join(options.ConfigDir, "template"), "template"},
	} {
		if err := add(item.source, "config", item.archive); err != nil {
			return nil, err
		}
	}
	if databasePath != "" {
		if err := add(databasePath, "database", "server.db"); err != nil {
			return nil, fmt.Errorf("backup database: %w", err)
		}
	}
	if options.IncludeSSL {
		if err := add(filepath.Join(options.ConfigDir, "ssl"), "config", "ssl"); err != nil {
			return nil, err
		}
	}
	for index, path := range options.TLSFiles {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("referenced TLS file %q is unavailable: %w", path, err)
		}
		if err := addFile(path, "tls", fmt.Sprintf("connection-%d-%s", index+1, filepath.Base(path)), path, &result); err != nil {
			return nil, fmt.Errorf("backup referenced TLS file: %w", err)
		}
	}
	for _, file := range options.ExternalTLSFiles {
		if file.Path == "" || (file.Name != "cert.pem" && file.Name != "key.pem") {
			continue
		}
		configSSLDir, err := filepath.Abs(filepath.Join(options.ConfigDir, "ssl"))
		if err != nil {
			return nil, err
		}
		absolutePath, err := filepath.Abs(file.Path)
		if err != nil {
			return nil, err
		}
		if absolutePath == configSSLDir || strings.HasPrefix(absolutePath, configSSLDir+string(filepath.Separator)) {
			continue
		}
		if _, err := os.Stat(file.Path); err != nil {
			return nil, fmt.Errorf("referenced external TLS file %q is unavailable: %w", file.Path, err)
		}
		if err := addFile(file.Path, "tls", filepath.ToSlash(filepath.Join("ssl-external", file.Name)), file.Path, &result); err != nil {
			return nil, fmt.Errorf("backup referenced external TLS file: %w", err)
		}
	}
	if options.IncludeData {
		if err := add(options.DataDir, "data", ""); err != nil {
			return nil, err
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return result, nil
}

func addFile(path, target, name, sourcePath string, result *[]sourceFile) (retErr error) {
	if target != "config" && target != "database" && target != "data" && target != "tls" {
		return fmt.Errorf("invalid backup target %q", target)
	}
	name = filepath.ToSlash(filepath.Clean(name))
	if name == "." || name == "" {
		name = filepath.Base(path)
	}
	if !filepath.IsLocal(filepath.FromSlash(name)) {
		return fmt.Errorf("invalid backup path %q", name)
	}
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse symlink backup source %q", path)
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, input.Close()) }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup source %q is not a regular file", path)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return err
	}
	*result = append(*result, sourceFile{path: path, file: ManifestFile{
		Target: target, Path: name, SourcePath: sourcePath, Size: info.Size(), MTime: info.ModTime().Unix(), SHA256: hex.EncodeToString(hash.Sum(nil)),
	}})
	return nil
}

func writeChecksumPart(output io.Writer, file ManifestFile, path string) (retErr error) {
	if _, err := fmt.Fprintf(output, "%s\x00%s\x00%s\x00%d\x00%s\x00", file.Target, file.Path, file.SourcePath, file.Size, file.SHA256); err != nil {
		return err
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, input.Close()) }()
	_, err = io.Copy(output, input)
	return err
}

func makeArchive(sources []sourceFile, manifest Manifest) ([]byte, error) {
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gz)
	for _, source := range sources {
		name := "files/" + source.file.Target + "/" + source.file.Path
		if err := addTarFile(tarWriter, name, source.path, source.file.Size, 0600); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if err := tarWriter.WriteHeader(&tar.Header{Name: manifestName, Mode: 0600, Size: int64(len(encoded)), Typeflag: tar.TypeReg, ModTime: time.Now().UTC()}); err != nil {
		return nil, err
	}
	if _, err := tarWriter.Write(encoded); err != nil {
		return nil, err
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func addTarFile(writer *tar.Writer, name, path string, size int64, mode int64) (retErr error) {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: size, Typeflag: tar.TypeReg, ModTime: time.Now().UTC()}); err != nil {
		return err
	}
	_, err = io.CopyN(writer, file, size)
	return err
}

func encrypt(plain []byte, password string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyBytes)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	header := []byte(encryptionMark)
	header = append(header, salt...)
	header = append(header, nonce...)
	return append(header, aead.Seal(nil, nonce, plain, header)...), nil
}

func decrypt(ciphertext []byte, password string) ([]byte, error) {
	mark := []byte(encryptionMark)
	if !bytes.HasPrefix(ciphertext, mark) || len(ciphertext) < len(mark)+16+12+16 {
		return nil, errors.New("invalid encrypted backup format")
	}
	position := len(mark)
	header := ciphertext[:position+16+12]
	salt := ciphertext[position : position+16]
	nonce := ciphertext[position+16 : position+16+12]
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyBytes)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, ciphertext[len(header):], header)
	if err != nil {
		return nil, errors.New("invalid backup password or corrupted encrypted backup")
	}
	return plain, nil
}

func writeAtomic(path string, content []byte, mode os.FileMode) (retErr error) {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".citylist-backup-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() {
		if cleanupErr := os.Remove(temporary); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary backup file: %w", cleanupErr))
		}
	}()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
