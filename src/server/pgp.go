package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"github.com/apimgr/citylist/src/paths"
	"golang.org/x/crypto/hkdf"
)

const (
	projectPGPIdentityName = "CityList API Security"
	projectPGPKeyLifetime  = 2 * 365 * 24 * time.Hour
	projectPGPMaxKeyBytes  = 16 << 10
	projectPGPMaxFetch     = 64 << 10
	projectPGPArmorType    = "PGP PRIVATE KEY BLOCK"
	projectPGPFileMagic    = "CITYLIST-PGP-PRIVATE-v1\x00"
)

var projectPGPLock sync.Mutex

type projectPGPKeypair struct {
	Entity      *openpgp.Entity
	PublicArmor []byte
	Fingerprint string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type ProjectPGPKey = projectPGPKeypair

var ErrProjectPGPIdentityMismatch = errors.New("imported OpenPGP key identity does not match the configured project identity")

// NewPGPMaintenance creates a server context for key maintenance without
// creating HTTP routes or requiring the public data services.
func NewPGPMaintenance(configDir string, cfg *config.Config, store *database.Store) (*Server, error) {
	if cfg == nil || store == nil {
		return nil, errors.New("PGP maintenance requires configuration and the application database")
	}
	instance := &Server{cfg: cfg, store: store}
	if err := instance.SetProjectPGPConfigDir(configDir); err != nil {
		return nil, err
	}
	return instance, nil
}

func (s *Server) SetProjectPGPConfigDir(dir string) error {
	if s == nil || strings.TrimSpace(dir) == "" {
		return errors.New("project PGP config directory is required")
	}
	absPath, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve project PGP config directory: %w", err)
	}
	s.SetConfigDir(filepath.Clean(absPath))
	return nil
}

func (s *Server) projectPGPPaths() (string, string, string, error) {
	configDir := strings.TrimSpace(s.configDir)
	if configDir == "" {
		configDir = paths.GetConfigDir("citylist")
	}
	dir := filepath.Join(configDir, "security")
	if err := paths.EnsurePrivateDir(dir); err != nil {
		return "", "", "", fmt.Errorf("prepare PGP key directory: %w", err)
	}
	return dir, filepath.Join(dir, "pgp.priv.asc.enc"), filepath.Join(dir, "pgp.pub.asc"), nil
}

func (s *Server) LoadProjectPGPKeypair(ctx context.Context) (*projectPGPKeypair, error) {
	projectPGPLock.Lock()
	defer projectPGPLock.Unlock()
	return s.loadProjectPGPKeypairLocked(ctx)
}

func (s *Server) GenerateProjectPGPKeypair(ctx context.Context) (*projectPGPKeypair, error) {
	projectPGPLock.Lock()
	defer projectPGPLock.Unlock()
	if _, err := s.loadProjectPGPKeypairLocked(ctx); err == nil {
		return nil, errors.New("project PGP keypair already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, _, publicPath, err := s.projectPGPPaths(); err != nil {
		return nil, err
	} else if _, err := os.Lstat(publicPath); err == nil {
		return nil, errors.New("PGP public key exists without its private key; refusing to replace the keypair")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect existing PGP public key: %w", err)
	}
	if s.cfg == nil {
		return nil, errors.New("server configuration is unavailable")
	}
	identityEmail := strings.TrimSpace(s.cfg.Server.Contact.Security.Email)
	identityEmail = strings.ReplaceAll(identityEmail, "{fqdn}", strings.TrimSpace(s.cfg.Server.FQDN))
	if strings.ContainsAny(identityEmail, "{}") {
		return nil, errors.New("configured security contact email must have a resolved FQDN")
	}
	securityContact, err := mail.ParseAddress(identityEmail)
	if err != nil {
		return nil, errors.New("configured security contact email is required to generate the project OpenPGP key")
	}
	pgpConfig := &packet.Config{
		Algorithm:       packet.PubKeyAlgoEdDSA,
		Curve:           packet.Curve25519,
		KeyLifetimeSecs: uint32(projectPGPKeyLifetime.Seconds()),
	}
	entity, err := openpgp.NewEntity(projectPGPIdentityName, "", securityContact.Address, pgpConfig)
	if err != nil {
		return nil, fmt.Errorf("generate OpenPGP keypair: %w", err)
	}
	if entity.PrimaryKey == nil || len(entity.PrimaryKey.Fingerprint) != 20 || entity.PrimaryKey.PubKeyAlgo != packet.PubKeyAlgoEdDSA {
		return nil, errors.New("generated OpenPGP key does not have a version 4 fingerprint")
	}
	encryptionKey, ok := entity.EncryptionKey(time.Now())
	if !ok || encryptionKey.PublicKey.PubKeyAlgo != packet.PubKeyAlgoECDH {
		return nil, errors.New("generated OpenPGP key does not have a Curve25519 encryption subkey")
	}
	encryptionCurve, err := encryptionKey.PublicKey.Curve()
	if err != nil || encryptionCurve != packet.Curve25519 {
		return nil, errors.New("generated OpenPGP encryption subkey is not Curve25519")
	}
	publicArmor, err := serializeProjectPGPPublic(entity)
	if err != nil {
		return nil, err
	}
	privateArmor, err := serializeProjectPGPPrivate(entity)
	if err != nil {
		return nil, err
	}
	secret, err := s.installationSecret(ctx)
	if err != nil {
		return nil, fmt.Errorf("load installation secret for PGP key: %w", err)
	}
	protected, err := protectProjectPGPPrivate(secret, privateArmor)
	if err != nil {
		return nil, err
	}
	dir, privatePath, publicPath, err := s.projectPGPPaths()
	if err != nil {
		return nil, err
	}
	if err := writePGPFileExclusive(privatePath, protected, 0600); err != nil {
		return nil, fmt.Errorf("store encrypted PGP private key: %w", err)
	}
	if err := writePGPFileAtomic(dir, publicPath, publicArmor, 0644); err != nil {
		return nil, fmt.Errorf("store PGP public key: %w", err)
	}
	now := time.Now().UTC()
	return &projectPGPKeypair{Entity: entity, PublicArmor: publicArmor, Fingerprint: strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint)), CreatedAt: now, ExpiresAt: now.Add(projectPGPKeyLifetime)}, nil
}

// ExportProjectPGPPrivate returns the full ASCII-armored private key after
// decrypting it with the installation secret.
func (s *Server) ExportProjectPGPPrivate(ctx context.Context) ([]byte, error) {
	projectPGPLock.Lock()
	defer projectPGPLock.Unlock()
	keypair, err := s.loadProjectPGPKeypairLocked(ctx)
	if err != nil {
		return nil, err
	}
	return serializeProjectPGPPrivate(keypair.Entity)
}

// ImportProjectPGPPrivate validates and installs an ASCII-armored private key.
func (s *Server) ImportProjectPGPPrivate(ctx context.Context, armored []byte, allowIdentityMismatch bool) (*projectPGPKeypair, error) {
	projectPGPLock.Lock()
	defer projectPGPLock.Unlock()
	if len(armored) == 0 || len(armored) > projectPGPMaxKeyBytes {
		return nil, errors.New("imported PGP private key is empty or too large")
	}
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(armored))
	if err != nil || len(entities) != 1 {
		return nil, errors.New("imported OpenPGP private key is invalid")
	}
	entity := entities[0]
	if entity.PrivateKey == nil || entity.PrimaryKey == nil || len(entity.PrimaryKey.Fingerprint) != 20 || entity.PrivateKey.Encrypted {
		return nil, errors.New("import must contain one unencrypted OpenPGP private key with a version 4 fingerprint")
	}
	for _, subkey := range entity.Subkeys {
		if subkey.PrivateKey != nil && subkey.PrivateKey.Encrypted {
			return nil, errors.New("imported OpenPGP private subkeys must be unencrypted")
		}
	}
	if !validProjectPGPEntity(entity) {
		return nil, errors.New("imported OpenPGP key does not have Ed25519 signing and Curve25519 encryption keys")
	}
	if !s.projectPGPIdentityMatches(entity) && !allowIdentityMismatch {
		return nil, ErrProjectPGPIdentityMismatch
	}
	current, currentErr := s.loadProjectPGPKeypairLocked(ctx)
	if currentErr != nil && !errors.Is(currentErr, os.ErrNotExist) {
		return nil, currentErr
	}
	activeFingerprint := ""
	if currentErr == nil {
		activeFingerprint = strings.ToUpper(hex.EncodeToString(current.Entity.PrimaryKey.Fingerprint))
	}
	importedFingerprint := strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint))
	if activeFingerprint != importedFingerprint {
		var retained int
		var recordedFingerprint sql.NullString
		if err := s.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
			if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_reports WHERE body_alg = 'pgp'`).Scan(&retained); err != nil {
				return err
			}
			return reader.QueryRowContext(ctx, `SELECT fingerprint FROM project_pgp_key WHERE id = 1`).Scan(&recordedFingerprint)
		}); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if retained > 0 {
			expectedFingerprint := activeFingerprint
			if expectedFingerprint == "" && recordedFingerprint.Valid {
				expectedFingerprint = strings.ToUpper(recordedFingerprint.String)
			}
			if expectedFingerprint == "" || expectedFingerprint != importedFingerprint {
				return nil, errors.New("cannot import a different PGP key while retained PGP-encrypted reports exist; restore the matching key or rotate the active key instead")
			}
		}
	}
	secret, err := s.installationSecret(ctx)
	if err != nil {
		return nil, err
	}
	privateArmor, err := serializeProjectPGPPrivate(entity)
	if err != nil {
		return nil, err
	}
	protected, err := protectProjectPGPPrivate(secret, privateArmor)
	if err != nil {
		return nil, err
	}
	publicArmor, err := serializeProjectPGPPublic(entity)
	if err != nil {
		return nil, err
	}
	dir, privatePath, publicPath, err := s.projectPGPPaths()
	if err != nil {
		return nil, err
	}
	if err := replacePGPFilePair(dir, privatePath, protected, publicPath, publicArmor); err != nil {
		return nil, err
	}
	created := entity.PrimaryKey.CreationTime.UTC()
	return &projectPGPKeypair{Entity: entity, PublicArmor: publicArmor, Fingerprint: strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint)), CreatedAt: created, ExpiresAt: created.Add(projectPGPKeyLifetime)}, nil
}

// RotateProjectPGPKeypair re-encrypts all retained PGP reports under one
// database write transaction before replacing the active key files.
func (s *Server) RotateProjectPGPKeypair(ctx context.Context) (*projectPGPKeypair, []byte, error) {
	projectPGPLock.Lock()
	defer projectPGPLock.Unlock()
	oldKey, err := s.loadProjectPGPKeypairLocked(ctx)
	if err != nil {
		return nil, nil, err
	}
	entity, err := newProjectPGPEntity(s.cfg)
	if err != nil {
		return nil, nil, err
	}
	publicArmor, err := serializeProjectPGPPublic(entity)
	if err != nil {
		return nil, nil, err
	}
	privateArmor, err := serializeProjectPGPPrivate(entity)
	if err != nil {
		return nil, nil, err
	}
	secret, err := s.installationSecret(ctx)
	if err != nil {
		return nil, nil, err
	}
	protected, err := protectProjectPGPPrivate(secret, privateArmor)
	if err != nil {
		return nil, nil, err
	}
	var rotationSignature bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&rotationSignature, oldKey.Entity, bytes.NewReader(publicArmor), nil); err != nil {
		return nil, nil, fmt.Errorf("sign rotated OpenPGP public key: %w", err)
	}
	dir, privatePath, publicPath, err := s.projectPGPPaths()
	if err != nil {
		return nil, nil, err
	}
	oldPrivate, err := readPrivatePGPFile(privatePath)
	if err != nil {
		return nil, nil, err
	}
	oldPublic, err := readPublicPGPFile(publicPath)
	if err != nil {
		return nil, nil, err
	}
	oldPrivatePath := filepath.Join(dir, "pgp.priv.old.asc.enc")
	if _, err := os.Lstat(oldPrivatePath); err == nil {
		return nil, nil, errors.New("an unexpired old PGP private key already exists; rotation is refused")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if err := writePGPFileExclusive(oldPrivatePath, oldPrivate, 0600); err != nil {
		return nil, nil, fmt.Errorf("preserve previous encrypted PGP key: %w", err)
	}
	newKey := &projectPGPKeypair{Entity: entity, PublicArmor: publicArmor, Fingerprint: strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint)), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(projectPGPKeyLifetime)}
	rotationAt := time.Now().UTC()
	swapped := false
	err = s.store.Write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT tracking_id, body_enc FROM security_reports WHERE body_alg = 'pgp'`)
		if err != nil {
			return err
		}
		type reportCiphertext struct {
			id   string
			body []byte
		}
		var reports []reportCiphertext
		for rows.Next() {
			var report reportCiphertext
			if err := rows.Scan(&report.id, &report.body); err != nil {
				_ = rows.Close()
				return err
			}
			reports = append(reports, report)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, report := range reports {
			plain, err := decryptProjectPGPMessage(oldKey.Entity, report.body)
			if err != nil {
				return fmt.Errorf("decrypt retained security report %s: %w", report.id, err)
			}
			encrypted, err := encryptPGPMessage(entity, plain, nil)
			if err != nil {
				return fmt.Errorf("encrypt retained security report %s with rotated key: %w", report.id, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE security_reports SET body_enc = ?, updated_at = ? WHERE tracking_id = ? AND body_alg = 'pgp'`, encrypted, rotationAt.Format(time.RFC3339Nano), report.id); err != nil {
				return err
			}
		}
		if err := writePGPFileAtomic(dir, privatePath, protected, 0600); err != nil {
			return err
		}
		if err := writePGPFileAtomic(dir, publicPath, publicArmor, 0644); err != nil {
			_ = writePGPFileAtomic(dir, privatePath, oldPrivate, 0600)
			return err
		}
		if err := writePGPFileAtomic(dir, filepath.Join(dir, "pgp.pub.asc.sig"), rotationSignature.Bytes(), 0644); err != nil {
			_ = writePGPFileAtomic(dir, privatePath, oldPrivate, 0600)
			_ = writePGPFileAtomic(dir, publicPath, oldPublic, 0644)
			return err
		}
		swapped = true
		_, err = tx.ExecContext(ctx, `INSERT INTO project_pgp_key (id, fingerprint, created_at, expires_at, last_rotated_at, keyservers_published, revoked, old_key_expires_at) VALUES (1, ?, ?, ?, ?, '[]', FALSE, ?) ON CONFLICT(id) DO UPDATE SET fingerprint = excluded.fingerprint, created_at = excluded.created_at, expires_at = excluded.expires_at, last_rotated_at = excluded.last_rotated_at, keyservers_published = '[]', revoked = FALSE, old_key_expires_at = excluded.old_key_expires_at`, newKey.Fingerprint, newKey.CreatedAt.Format(time.RFC3339Nano), newKey.ExpiresAt.Format(time.RFC3339Nano), rotationAt.Format(time.RFC3339Nano), rotationAt.Add(30*24*time.Hour).Format(time.RFC3339Nano))
		return err
	})
	if err != nil {
		if swapped {
			if restoreErr := writePGPFileAtomic(dir, privatePath, oldPrivate, 0600); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("restore active PGP private key: %w", restoreErr))
			}
			if restoreErr := writePGPFileAtomic(dir, publicPath, oldPublic, 0644); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("restore active PGP public key: %w", restoreErr))
			}
			if removeErr := os.Remove(filepath.Join(dir, "pgp.pub.asc.sig")); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove failed rotation signature: %w", removeErr))
			}
		}
		if removeErr := os.Remove(oldPrivatePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove staged prior PGP private key: %w", removeErr))
		}
		return nil, nil, err
	}
	return newKey, rotationSignature.Bytes(), nil
}

// DeleteProjectPGPKeypair removes key material while retaining its fingerprint
// in metadata for the audit history.
func (s *Server) DeleteProjectPGPKeypair(ctx context.Context) error {
	projectPGPLock.Lock()
	defer projectPGPLock.Unlock()
	dir, privatePath, publicPath, err := s.projectPGPPaths()
	if err != nil {
		return err
	}
	keypair, err := s.loadProjectPGPKeypairLocked(ctx)
	if err != nil {
		return err
	}
	paths := []string{privatePath, publicPath, filepath.Join(dir, "pgp.priv.old.asc.enc"), filepath.Join(dir, "pgp.pub.asc.sig")}
	type movedFile struct{ original, staged string }
	moved := make([]movedFile, 0, len(paths))
	for index, original := range paths {
		staged := filepath.Join(dir, fmt.Sprintf(".pgp-delete-%d-%d", time.Now().UnixNano(), index))
		if err := os.Rename(original, staged); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			for i := len(moved) - 1; i >= 0; i-- {
				_ = os.Rename(moved[i].staged, moved[i].original)
			}
			return fmt.Errorf("stage PGP key file for deletion: %w", err)
		}
		moved = append(moved, movedFile{original: original, staged: staged})
	}
	err = s.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO project_pgp_key (id, fingerprint, created_at, expires_at, keyservers_published, revoked) VALUES (1, ?, ?, ?, '[]', TRUE) ON CONFLICT(id) DO UPDATE SET revoked = TRUE`, keypair.Fingerprint, keypair.CreatedAt.UTC().Format(time.RFC3339Nano), keypair.ExpiresAt.UTC().Format(time.RFC3339Nano))
		return err
	})
	if err != nil {
		for i := len(moved) - 1; i >= 0; i-- {
			_ = os.Rename(moved[i].staged, moved[i].original)
		}
		return err
	}
	for _, file := range moved {
		if err := os.Remove(file.staged); err != nil {
			return fmt.Errorf("remove staged PGP key file: %w", err)
		}
	}
	return nil
}

// CleanupExpiredOldProjectPGPKey removes the prior private key after its
// configured 30-day transition window.
func (s *Server) CleanupExpiredOldProjectPGPKey(ctx context.Context) error {
	projectPGPLock.Lock()
	defer projectPGPLock.Unlock()
	if s.store == nil {
		return errors.New("application database is unavailable")
	}
	var expires sql.NullString
	err := s.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT old_key_expires_at FROM project_pgp_key WHERE id = 1`).Scan(&expires)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !expires.Valid {
		return nil
	}
	deadline, err := time.Parse(time.RFC3339Nano, expires.String)
	if err != nil {
		return fmt.Errorf("parse prior PGP key expiration: %w", err)
	}
	if time.Now().UTC().Before(deadline) {
		return nil
	}
	_, privatePath, _, err := s.projectPGPPaths()
	if err != nil {
		return err
	}
	oldPath := filepath.Join(filepath.Dir(privatePath), "pgp.priv.old.asc.enc")
	if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE project_pgp_key SET old_key_expires_at = NULL WHERE id = 1`)
		return err
	})
}

func validProjectPGPEntity(entity *openpgp.Entity) bool {
	if entity.PrimaryKey.PubKeyAlgo != packet.PubKeyAlgoEdDSA {
		return false
	}
	if _, ok := entity.SigningKey(time.Now()); !ok {
		return false
	}
	key, ok := entity.EncryptionKey(time.Now())
	if !ok || key.PublicKey.PubKeyAlgo != packet.PubKeyAlgoECDH {
		return false
	}
	curve, err := key.PublicKey.Curve()
	return err == nil && curve == packet.Curve25519
}

func newProjectPGPEntity(cfg *config.Config) (*openpgp.Entity, error) {
	if cfg == nil {
		return nil, errors.New("server configuration is unavailable")
	}
	identityEmail := strings.TrimSpace(cfg.Server.Contact.Security.Email)
	identityEmail = strings.ReplaceAll(identityEmail, "{fqdn}", strings.TrimSpace(cfg.Server.FQDN))
	if strings.ContainsAny(identityEmail, "{}") {
		return nil, errors.New("configured security contact email must have a resolved FQDN")
	}
	securityContact, err := mail.ParseAddress(identityEmail)
	if err != nil {
		return nil, errors.New("configured security contact email is required to generate the project OpenPGP key")
	}
	entity, err := openpgp.NewEntity(projectPGPIdentityName, "", securityContact.Address, &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA, Curve: packet.Curve25519, KeyLifetimeSecs: uint32(projectPGPKeyLifetime.Seconds())})
	if err != nil {
		return nil, fmt.Errorf("generate OpenPGP keypair: %w", err)
	}
	if !validProjectPGPEntity(entity) {
		return nil, errors.New("generated OpenPGP key does not meet the configured signing and encryption algorithms")
	}
	return entity, nil
}

func (s *Server) projectPGPIdentityMatches(entity *openpgp.Entity) bool {
	if s.cfg == nil {
		return false
	}
	expectedEmail := strings.TrimSpace(s.cfg.Server.Contact.Security.Email)
	expectedEmail = strings.ReplaceAll(expectedEmail, "{fqdn}", strings.TrimSpace(s.cfg.Server.FQDN))
	parsed, err := mail.ParseAddress(expectedEmail)
	if err != nil {
		return false
	}
	for _, identity := range entity.Identities {
		if identity.UserId.Name == projectPGPIdentityName && strings.EqualFold(identity.UserId.Email, parsed.Address) {
			return true
		}
	}
	return false
}

func replacePGPFilePair(dir, privatePath string, private []byte, publicPath string, public []byte) error {
	previousPrivate, privateErr := readPrivatePGPFile(privatePath)
	previousPublic, publicErr := readPublicPGPFile(publicPath)
	hadPrivate := privateErr == nil
	hadPublic := publicErr == nil
	if privateErr != nil && !errors.Is(privateErr, os.ErrNotExist) {
		return privateErr
	}
	if publicErr != nil && !errors.Is(publicErr, os.ErrNotExist) {
		return publicErr
	}
	if err := writePGPFileAtomic(dir, privatePath, private, 0600); err != nil {
		return err
	}
	if err := writePGPFileAtomic(dir, publicPath, public, 0644); err != nil {
		if hadPrivate {
			_ = writePGPFileAtomic(dir, privatePath, previousPrivate, 0600)
		} else {
			_ = os.Remove(privatePath)
		}
		if hadPublic {
			_ = writePGPFileAtomic(dir, publicPath, previousPublic, 0644)
		}
		return err
	}
	return nil
}

func decryptProjectPGPMessage(entity *openpgp.Entity, encrypted []byte) ([]byte, error) {
	block, err := armor.Decode(bytes.NewReader(encrypted))
	if err != nil || block.Type != openpgp.MessageType {
		return nil, errors.New("stored OpenPGP report is invalid")
	}
	message, err := openpgp.ReadMessage(block.Body, openpgp.EntityList{entity}, nil, nil)
	if err != nil {
		return nil, err
	}
	plaintext, err := io.ReadAll(message.UnverifiedBody)
	if err != nil {
		return nil, err
	}
	if message.SignatureError != nil {
		return nil, message.SignatureError
	}
	return plaintext, nil
}

func (s *Server) loadProjectPGPKeypairLocked(ctx context.Context) (*projectPGPKeypair, error) {
	_, privatePath, publicPath, err := s.projectPGPPaths()
	if err != nil {
		return nil, err
	}
	protected, err := readPrivatePGPFile(privatePath)
	if err != nil {
		return nil, err
	}
	secret, err := s.installationSecret(ctx)
	if err != nil {
		return nil, fmt.Errorf("load installation secret for PGP key: %w", err)
	}
	privateArmor, err := unprotectProjectPGPPrivate(secret, protected)
	if err != nil {
		return nil, err
	}
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(privateArmor))
	if err != nil || len(entities) != 1 || entities[0].PrivateKey == nil {
		return nil, errors.New("stored PGP private key is invalid")
	}
	entity := entities[0]
	publicArmor, err := serializeProjectPGPPublic(entity)
	if err != nil {
		return nil, err
	}
	storedPublic, err := readPublicPGPFile(publicPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		dir := filepath.Dir(publicPath)
		if err := writePGPFileAtomic(dir, publicPath, publicArmor, 0644); err != nil {
			return nil, fmt.Errorf("restore PGP public key: %w", err)
		}
	} else {
		parsed, err := readProjectPublicKey(storedPublic)
		if err != nil || !bytes.Equal(parsed.PrimaryKey.Fingerprint, entity.PrimaryKey.Fingerprint) {
			return nil, errors.New("stored PGP public and private keys do not match")
		}
		publicArmor = storedPublic
	}
	created := entity.PrimaryKey.CreationTime
	return &projectPGPKeypair{Entity: entity, PublicArmor: publicArmor, Fingerprint: strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint)), CreatedAt: created, ExpiresAt: created.Add(projectPGPKeyLifetime)}, nil
}

func serializeProjectPGPPublic(entity *openpgp.Entity) ([]byte, error) {
	var output bytes.Buffer
	writer, err := armor.Encode(&output, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, fmt.Errorf("armor PGP public key: %w", err)
	}
	if err := entity.Serialize(writer); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("serialize PGP public key: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish PGP public key armor: %w", err)
	}
	return output.Bytes(), nil
}

func serializeProjectPGPPrivate(entity *openpgp.Entity) ([]byte, error) {
	var output bytes.Buffer
	writer, err := armor.Encode(&output, projectPGPArmorType, nil)
	if err != nil {
		return nil, fmt.Errorf("armor PGP private key: %w", err)
	}
	if err := entity.SerializePrivate(writer, nil); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("serialize PGP private key: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish PGP private key armor: %w", err)
	}
	return output.Bytes(), nil
}

func protectProjectPGPPrivate(installationSecret, plaintext []byte) ([]byte, error) {
	key, err := deriveProjectPGPKey(installationSecret)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create PGP private-key cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create PGP private-key AEAD: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate PGP private-key nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plaintext, []byte(projectPGPFileMagic))
	return append(append([]byte(projectPGPFileMagic), nonce...), sealed...), nil
}

func unprotectProjectPGPPrivate(installationSecret, protected []byte) ([]byte, error) {
	if !bytes.HasPrefix(protected, []byte(projectPGPFileMagic)) {
		return nil, errors.New("encrypted PGP private-key file has an unsupported format")
	}
	key, err := deriveProjectPGPKey(installationSecret)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create PGP private-key cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create PGP private-key AEAD: %w", err)
	}
	start := len(projectPGPFileMagic)
	if len(protected) < start+gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("encrypted PGP private-key file is truncated")
	}
	nonce := protected[start : start+gcm.NonceSize()]
	plaintext, err := gcm.Open(nil, nonce, protected[start+gcm.NonceSize():], []byte(projectPGPFileMagic))
	if err != nil {
		return nil, errors.New("encrypted PGP private-key file failed authentication")
	}
	return plaintext, nil
}

func deriveProjectPGPKey(installationSecret []byte) ([]byte, error) {
	if len(installationSecret) != 32 {
		return nil, errors.New("installation secret must be 32 bytes")
	}
	key := make([]byte, 32)
	reader := hkdf.New(sha256.New, installationSecret, nil, []byte("citylist-pgp-private-key-v1/AES-256-GCM"))
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("derive PGP private-key encryption key: %w", err)
	}
	return key, nil
}

func writePGPFileExclusive(path string, contents []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pgp-private-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmpPath, path)
}

func writePGPFileAtomic(dir, path string, contents []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(dir, ".pgp-public-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func readPrivatePGPFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("PGP private-key path is not a regular file")
	}
	if info.Size() > projectPGPMaxKeyBytes*2 {
		return nil, errors.New("encrypted PGP private-key file is too large")
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, fmt.Errorf("secure PGP private-key file permissions: %w", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read encrypted PGP private key: %w", err)
	}
	if len(contents) > projectPGPMaxKeyBytes*2 {
		return nil, errors.New("encrypted PGP private-key file is too large")
	}
	return contents, nil
}

func readPublicPGPFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("PGP public-key path is not a regular file")
	}
	if info.Size() > projectPGPMaxKeyBytes {
		return nil, errors.New("PGP public-key file is too large")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read PGP public key: %w", err)
	}
	if len(contents) > projectPGPMaxKeyBytes {
		return nil, errors.New("PGP public-key file is too large")
	}
	return contents, nil
}

func readProjectPublicKey(armored []byte) (*openpgp.Entity, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(armored))
	if err != nil || len(entities) != 1 {
		return nil, errors.New("OpenPGP public key is invalid")
	}
	entity := entities[0]
	if entity.PrivateKey != nil {
		return nil, errors.New("OpenPGP public key input contains private key material")
	}
	if _, ok := entity.EncryptionKey(time.Now()); !ok {
		return nil, errors.New("OpenPGP key has no current encryption key")
	}
	return entity, nil
}

func (s *Server) EncryptForProjectPGP(ctx context.Context, plaintext []byte) ([]byte, error) {
	keypair, err := s.LoadProjectPGPKeypair(ctx)
	if err != nil {
		return nil, err
	}
	return encryptPGPMessage(keypair.Entity, plaintext, keypair.Entity)
}

func (s *Server) SignWithProjectPGP(ctx context.Context, plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("OpenPGP message is required")
	}
	keypair, err := s.LoadProjectPGPKeypair(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := keypair.Entity.SigningKey(time.Now()); !ok {
		return nil, errors.New("project OpenPGP key has no current signing key")
	}
	var output bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&output, keypair.Entity, bytes.NewReader(plaintext), nil); err != nil {
		return nil, fmt.Errorf("sign OpenPGP message: %w", err)
	}
	return output.Bytes(), nil
}

func (s *Server) EncryptForResearcherPGP(ctx context.Context, keyInput string, plaintext []byte) ([]byte, error) {
	entity, err := resolveResearcherPGPKey(ctx, keyInput)
	if err != nil {
		return nil, err
	}
	return encryptPGPMessage(entity, plaintext, nil)
}

func ValidateResearcherPGPKey(ctx context.Context, keyInput string) error {
	_, err := resolveResearcherPGPKey(ctx, keyInput)
	return err
}

func resolveResearcherPGPKey(ctx context.Context, keyInput string) (*openpgp.Entity, error) {
	keyInput = strings.TrimSpace(keyInput)
	if keyInput == "" || len(keyInput) > projectPGPMaxKeyBytes {
		return nil, errors.New("researcher OpenPGP key is empty or too large")
	}
	if strings.HasPrefix(strings.ToLower(keyInput), "https://") {
		if len(keyInput) > 2048 {
			return nil, errors.New("researcher OpenPGP key URL is too long")
		}
		data, err := fetchResearcherPGPKey(ctx, keyInput)
		if err != nil {
			return nil, err
		}
		keyInput = string(data)
	}
	return readProjectPublicKey([]byte(keyInput))
}

func fetchResearcherPGPKey(ctx context.Context, rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("researcher OpenPGP key URL must be an HTTPS URL without user information")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create researcher OpenPGP key request: %w", err)
	}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{DialContext: safePGPDialContext},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 || !strings.EqualFold(request.URL.Scheme, "https") || request.URL.User != nil || request.URL.Hostname() == "" {
				return errors.New("unsafe researcher OpenPGP key redirect")
			}
			return nil
		},
	}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch researcher OpenPGP key: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("researcher OpenPGP key server returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, projectPGPMaxFetch+1))
	if err != nil {
		return nil, fmt.Errorf("read researcher OpenPGP key response: %w", err)
	}
	if len(data) > projectPGPMaxFetch {
		return nil, errors.New("researcher OpenPGP key response is too large")
	}
	return data, nil
}

func safePGPDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if !safePublicPGPAddress(net.IP(ip.AsSlice())) {
			return nil, errors.New("researcher OpenPGP key URL resolved to a non-public address")
		}
		return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve researcher OpenPGP key host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("researcher OpenPGP key host has no addresses")
	}
	for _, item := range addresses {
		if !safePublicPGPAddress(item.IP) {
			return nil, errors.New("researcher OpenPGP key URL resolved to a non-public address")
		}
	}
	var lastErr error
	for _, item := range addresses {
		conn, err := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(item.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("connect to researcher OpenPGP key host: %w", lastErr)
}

func safePublicPGPAddress(address net.IP) bool {
	parsed, ok := netip.AddrFromSlice(address)
	if !ok {
		return false
	}
	parsed = parsed.Unmap()
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	return parsed.IsGlobalUnicast() && !parsed.IsPrivate() && !parsed.IsLoopback() && !parsed.IsLinkLocalUnicast() && !parsed.IsUnspecified() && !cgnat.Contains(parsed)
}

func encryptPGPMessage(recipient *openpgp.Entity, plaintext []byte, signer *openpgp.Entity) ([]byte, error) {
	if recipient == nil || len(plaintext) == 0 {
		return nil, errors.New("OpenPGP recipient and message are required")
	}
	if _, ok := recipient.EncryptionKey(time.Now()); !ok {
		return nil, errors.New("OpenPGP recipient has no current encryption key")
	}
	var output bytes.Buffer
	armored, err := armor.Encode(&output, openpgp.MessageType, nil)
	if err != nil {
		return nil, fmt.Errorf("armor OpenPGP message: %w", err)
	}
	writer, err := openpgp.Encrypt(armored, []*openpgp.Entity{recipient}, signer, &openpgp.FileHints{IsBinary: true}, nil)
	if err != nil {
		_ = armored.Close()
		return nil, fmt.Errorf("encrypt OpenPGP message: %w", err)
	}
	if _, err := writer.Write(plaintext); err != nil {
		_ = writer.Close()
		_ = armored.Close()
		return nil, fmt.Errorf("write OpenPGP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		_ = armored.Close()
		return nil, fmt.Errorf("finish OpenPGP message: %w", err)
	}
	if err := armored.Close(); err != nil {
		return nil, fmt.Errorf("finish OpenPGP message armor: %w", err)
	}
	return output.Bytes(), nil
}
