package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
)

const secretKeyRotationPending = "encryption_key_rotation_pending"
const installationSecretRotationPending = "installation_secret_rotation_pending"

// RotateInstallationSecret replaces the HMAC root and re-encrypts the project PGP key.
func RotateInstallationSecret(ctx context.Context, configDir string, cfg *config.Config, store *database.Store) error {
	if cfg == nil || store == nil {
		return errors.New("secret rotation requires configuration and database")
	}
	oldEncoded, err := readAppSecret(ctx, store, "installation_secret")
	if err != nil {
		return err
	}
	old, err := base64.StdEncoding.DecodeString(oldEncoded)
	if err != nil || len(old) != 32 {
		return errors.New("current installation secret is invalid")
	}
	var newKey [32]byte
	if _, err = rand.Read(newKey[:]); err != nil {
		return err
	}
	now := time.Now().UTC()
	until := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	pending, err := sealSecret(old, newKey[:], []byte("citylist-installation-secret-pending-v1"))
	if err != nil {
		return err
	}
	if err = store.Write(ctx, func(tx *sql.Tx) error {
		var exists int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_secrets WHERE name IN ('installation_secret_previous','installation_secret_previous_until')`).Scan(&exists); e != nil {
			return e
		}
		if exists != 0 {
			return errors.New("previous installation secret is still retained; wait for grace cleanup")
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES('installation_secret_previous',?,?)`, oldEncoded, now.Format(time.RFC3339Nano)); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES('installation_secret_previous_until',?,?)`, until, now.Format(time.RFC3339Nano)); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES(?,?,?)`, installationSecretRotationPending, base64.StdEncoding.EncodeToString(pending), now.Format(time.RFC3339Nano))
		return e
	}); err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(newKey[:])
	if err = store.Write(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `UPDATE app_secrets SET value=?,updated_at=? WHERE name='installation_secret'`, encoded, now.Format(time.RFC3339Nano))
		return e
	}); err != nil {
		return err
	}
	if err = rotatePGPPrivateSecret(ctx, configDir, old, newKey[:]); err != nil {
		return err
	}
	contactInstallationSecrets.Store(store, append([]byte(nil), newKey[:]...))
	return nil
}

func rotatePGPPrivateSecret(ctx context.Context, configDir string, old, newKey []byte) error {
	maintenance := &Server{}
	maintenance.SetConfigDir(configDir)
	_, privatePath, _, err := maintenance.projectPGPPaths()
	if err != nil {
		return err
	}
	protected, err := readPrivatePGPFile(privatePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := unprotectProjectPGPPrivate(old, protected)
	if err != nil {
		if _, alreadyRotated := unprotectProjectPGPPrivate(newKey, protected); alreadyRotated == nil {
			return nil
		}
		return errors.New("decrypt PGP private key during installation secret rotation")
	}
	replacement, err := protectProjectPGPPrivate(newKey, plain)
	if err != nil {
		return err
	}
	return writePGPFileAtomic(filepath.Dir(privatePath), privatePath, replacement, 0600)
}

// RotateEncryptionKey persists a wrapped grace key before changing secrets.yml,
// then eagerly re-encrypts token and AES report ciphertext in bounded batches.
func RotateEncryptionKey(ctx context.Context, configPath string, cfg *config.Config, store *database.Store) error {
	if cfg == nil || store == nil {
		return errors.New("secret rotation requires configuration and database")
	}
	old, err := base64.StdEncoding.DecodeString(cfg.Server.Security.EncryptionKey)
	if err != nil || len(old) != 32 {
		return errors.New("current encryption key is invalid")
	}
	if _, pendingErr := readAppSecret(ctx, store, secretKeyRotationPending); pendingErr == nil {
		return errors.New("encryption key rotation recovery is pending")
	} else if !errors.Is(pendingErr, sql.ErrNoRows) {
		return pendingErr
	}
	var prior string
	if err = store.Read(ctx, func(ctx context.Context, r database.Reader) error {
		return r.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name='encryption_key_previous'`).Scan(&prior)
	}); err == nil {
		return errors.New("previous encryption key is still retained; wait for grace cleanup")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	newKey := make([]byte, 32)
	if _, err = rand.Read(newKey); err != nil {
		return err
	}
	wrappedPrevious, err := sealSecret(newKey, old, []byte("citylist-encryption-key-previous-v1"))
	if err != nil {
		return err
	}
	pending, err := sealSecret(old, newKey, []byte("citylist-encryption-key-pending-v1"))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	until := now.Add(30 * 24 * time.Hour).Format(time.RFC3339)
	if err = store.Write(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES('encryption_key_previous',?,?)`, base64.StdEncoding.EncodeToString(wrappedPrevious), now.Format(time.RFC3339Nano)); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES('encryption_key_previous_until',?,?)`, until, now.Format(time.RFC3339Nano)); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES(?,?,?)`, secretKeyRotationPending, base64.StdEncoding.EncodeToString(pending), now.Format(time.RFC3339Nano))
		return e
	}); err != nil {
		return err
	}
	if err = config.ReplaceEncryptionKey(configPath, newKey); err != nil {
		return err
	}
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(newKey)
	return reEncryptAtRest(ctx, store, newKey, old)
}

// ResumeSecretRotations completes durable rotation work after interrupted startup.
func ResumeSecretRotations(ctx context.Context, configPath, configDir string, cfg *config.Config, store *database.Store) error {
	return ResumeSecretRotationsWithAudit(ctx, configPath, configDir, cfg, store, nil)
}

// ResumeSecretRotationsWithAudit appends recovery audit events after a durable rotation finishes.
func ResumeSecretRotationsWithAudit(ctx context.Context, configPath, configDir string, cfg *config.Config, store *database.Store, audit func(string) error) error {
	if cfg == nil || store == nil {
		return errors.New("secret rotation recovery requires configuration and database")
	}
	installationPending, err := secretExists(ctx, store, installationSecretRotationPending)
	if err != nil {
		return err
	}
	encryptionPending, err := secretExists(ctx, store, secretKeyRotationPending)
	if err != nil {
		return err
	}
	if err := resumeInstallationRotation(ctx, configDir, store); err != nil {
		return err
	}
	if err := resumeEncryptionRotation(ctx, configPath, cfg, store); err != nil {
		return err
	}
	if err := CleanupPreviousSecrets(ctx, store); err != nil {
		return err
	}
	if (installationPending || encryptionPending) && audit == nil {
		return nil
	}
	if audit != nil {
		if installationPending {
			if err := audit("security.installation_secret_rotated"); err != nil {
				return err
			}
		}
		if encryptionPending {
			if err := audit("security.encryption_key_rotated"); err != nil {
				return err
			}
		}
	}
	return store.Write(ctx, func(tx *sql.Tx) error {
		for _, name := range []string{installationSecretRotationPending, secretKeyRotationPending} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM app_secrets WHERE name=?`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// AcknowledgeSecretRotation removes the recovery marker after its audit event is durable.
func AcknowledgeSecretRotation(ctx context.Context, store *database.Store, name string) error {
	marker := map[string]string{"installation_secret": installationSecretRotationPending, "encryption_key": secretKeyRotationPending}[name]
	if marker == "" {
		return errors.New("unsupported secret rotation acknowledgement")
	}
	return store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM app_secrets WHERE name=?`, marker)
		return err
	})
}

func secretExists(ctx context.Context, store *database.Store, name string) (bool, error) {
	var count int
	err := store.Read(ctx, func(ctx context.Context, r database.Reader) error {
		return r.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_secrets WHERE name=?`, name).Scan(&count)
	})
	return count > 0, err
}

// CleanupPreviousSecrets removes grace keys after expiry, after startup recovery has completed.
func CleanupPreviousSecrets(ctx context.Context, store *database.Store) error {
	if store == nil {
		return errors.New("application database unavailable")
	}
	now := time.Now().UTC()
	return store.Write(ctx, func(tx *sql.Tx) error {
		for _, name := range []string{"installation_secret", "encryption_key", "cookie_signing_key", "csrf_token_secret"} {
			var until string
			err := tx.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, name+"_previous_until").Scan(&until)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			expiry, err := time.Parse(time.RFC3339, until)
			if err != nil {
				return fmt.Errorf("invalid previous secret expiry for %s", name)
			}
			if now.After(expiry) {
				if _, err = tx.ExecContext(ctx, `DELETE FROM app_secrets WHERE name IN (?,?)`, name+"_previous", name+"_previous_until"); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// StartSecretCleanup retires expired grace keys once per day while the server runs.
func StartSecretCleanup(ctx context.Context, store *database.Store) {
	if store == nil || store.UsingOfflineCache() {
		return
	}
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := CleanupPreviousSecrets(ctx, store); err != nil {
					slog.Error("security.secret_cleanup_failed", "error", err)
				}
			}
		}
	}()
}

func resumeInstallationRotation(ctx context.Context, configDir string, store *database.Store) error {
	current, err := readAppSecret(ctx, store, "installation_secret")
	if err != nil {
		return err
	}
	previous, err := readAppSecret(ctx, store, "installation_secret_previous")
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	old, err := base64.StdEncoding.DecodeString(previous)
	if err != nil || len(old) != 32 {
		return errors.New("previous installation secret is invalid")
	}
	next, err := base64.StdEncoding.DecodeString(current)
	if err != nil || len(next) != 32 {
		return errors.New("current installation secret is invalid")
	}
	pendingValue, pendingErr := readAppSecret(ctx, store, installationSecretRotationPending)
	if pendingErr == nil {
		wrapped, e := base64.StdEncoding.DecodeString(pendingValue)
		if e != nil {
			return e
		}
		pending, e := openSecret(next, wrapped, []byte("citylist-installation-secret-pending-v1"))
		if e != nil {
			pending, e = openSecret(old, wrapped, []byte("citylist-installation-secret-pending-v1"))
			if e != nil {
				return errors.New("pending installation secret rotation cannot be recovered")
			}
			if !equalBytes(pending, next) {
				return errors.New("pending installation secret does not match current value")
			}
		}
		if !equalBytes(pending, next) {
			next = pending
			encoded := base64.StdEncoding.EncodeToString(next)
			if e = store.Write(ctx, func(tx *sql.Tx) error {
				_, x := tx.ExecContext(ctx, `UPDATE app_secrets SET value=?,updated_at=? WHERE name='installation_secret'`, encoded, time.Now().UTC().Format(time.RFC3339Nano))
				return x
			}); e != nil {
				return e
			}
		}
	} else if !errors.Is(pendingErr, sql.ErrNoRows) {
		return pendingErr
	}
	maintenance := &Server{}
	maintenance.SetConfigDir(configDir)
	_, privatePath, _, err := maintenance.projectPGPPaths()
	if err != nil {
		return err
	}
	protected, err := readPrivatePGPFile(privatePath)
	if err == nil {
		if _, currentErr := unprotectProjectPGPPrivate(next, protected); currentErr != nil {
			untilText, e := readAppSecret(ctx, store, "installation_secret_previous_until")
			if e != nil {
				return e
			}
			until, e := time.Parse(time.RFC3339, untilText)
			if e != nil || time.Now().UTC().After(until) {
				return errors.New("previous installation secret grace period expired before PGP recovery")
			}
			if e = rotatePGPPrivateSecret(ctx, configDir, old, next); e != nil {
				return e
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func resumeEncryptionRotation(ctx context.Context, configPath string, cfg *config.Config, store *database.Store) error {
	current, err := base64.StdEncoding.DecodeString(cfg.Server.Security.EncryptionKey)
	if err != nil || len(current) != 32 {
		return errors.New("current encryption key is invalid")
	}
	var pendingValue string
	err = store.Read(ctx, func(ctx context.Context, r database.Reader) error {
		return r.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, secretKeyRotationPending).Scan(&pendingValue)
	})
	if err == nil {
		wrapped, e := base64.StdEncoding.DecodeString(pendingValue)
		if e != nil {
			return e
		}
		newKey, e := openSecret(current, wrapped, []byte("citylist-encryption-key-pending-v1"))
		if e != nil {
			var oldWrapped string
			if readErr := store.Read(ctx, func(ctx context.Context, r database.Reader) error {
				return r.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name='encryption_key_previous'`).Scan(&oldWrapped)
			}); readErr != nil {
				return errors.New("pending encryption-key rotation cannot be recovered")
			}
			oldCipher, decodeErr := base64.StdEncoding.DecodeString(oldWrapped)
			if decodeErr != nil {
				return decodeErr
			}
			oldKey, openErr := openSecret(current, oldCipher, []byte("citylist-encryption-key-previous-v1"))
			if openErr != nil {
				return errors.New("pending encryption-key rotation cannot be recovered")
			}
			newKey, e = openSecret(oldKey, wrapped, []byte("citylist-encryption-key-pending-v1"))
			if e != nil {
				return errors.New("pending encryption-key rotation cannot be recovered")
			}
			if !equalBytes(newKey, current) {
				return errors.New("pending encryption-key rotation does not match current config")
			}
		} else {
			if e = config.ReplaceEncryptionKey(configPath, newKey); e != nil {
				return e
			}
			cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(newKey)
			current = newKey
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	previousWrapped, err := readAppSecret(ctx, store, "encryption_key_previous")
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	untilText, err := readAppSecret(ctx, store, "encryption_key_previous_until")
	if err != nil {
		return err
	}
	until, err := time.Parse(time.RFC3339, untilText)
	if err != nil {
		return errors.New("previous encryption key expiry is invalid")
	}
	var previous []byte
	if !time.Now().UTC().After(until) {
		wrapped, e := base64.StdEncoding.DecodeString(previousWrapped)
		if e != nil {
			return e
		}
		previous, e = openSecret(current, wrapped, []byte("citylist-encryption-key-previous-v1"))
		if e != nil {
			return errors.New("previous encryption key cannot be recovered")
		}
	}
	return reEncryptAtRest(ctx, store, current, previous)
}

func reEncryptAtRest(ctx context.Context, store *database.Store, current, previous []byte) error {
	if err := reEncryptTokenRows(ctx, store, current, previous); err != nil {
		return err
	}
	return reEncryptReportRows(ctx, store, current, previous)
}

func reEncryptTokenRows(ctx context.Context, store *database.Store, current, previous []byte) error {
	var last string
	for {
		var rowsAffected int
		err := store.Write(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `SELECT id,token_enc FROM api_tokens WHERE id>? ORDER BY id LIMIT 500`, last)
			if err != nil {
				return err
			}
			type item struct {
				id    string
				value string
			}
			batch := []item{}
			for rows.Next() {
				var v item
				if err = rows.Scan(&v.id, &v.value); err != nil {
					_ = rows.Close()
					return err
				}
				batch = append(batch, v)
			}
			if err = rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			if err = rows.Close(); err != nil {
				return err
			}
			for _, row := range batch {
				last = row.id
				if _, e := openOwnerToken(current, row.value); e != nil {
					plain, e := openOwnerToken(previous, row.value)
					if e != nil {
						slog.Error("security.decrypt_failed", "table", "api_tokens", "row_id", row.id)
						return fmt.Errorf("decrypt_failed: table api_tokens row %s", row.id)
					}
					sealed, e := sealOwnerToken(current, plain)
					if e != nil {
						return e
					}
					if _, e = tx.ExecContext(ctx, `UPDATE api_tokens SET token_enc=? WHERE id=?`, sealed, row.id); e != nil {
						return e
					}
				}
			}
			rowsAffected = len(batch)
			return nil
		})
		if err != nil {
			return err
		}
		if rowsAffected < 500 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func reEncryptReportRows(ctx context.Context, store *database.Store, current, previous []byte) error {
	var last string
	for {
		var count int
		err := store.Write(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `SELECT tracking_id,body_enc,body_alg,contact_enc FROM security_reports WHERE tracking_id>? ORDER BY tracking_id LIMIT 500`, last)
			if err != nil {
				return err
			}
			type item struct {
				id            string
				body, contact []byte
				alg           string
			}
			batch := []item{}
			for rows.Next() {
				var v item
				if err = rows.Scan(&v.id, &v.body, &v.alg, &v.contact); err != nil {
					_ = rows.Close()
					return err
				}
				batch = append(batch, v)
			}
			if err = rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			if err = rows.Close(); err != nil {
				return err
			}
			for _, row := range batch {
				last = row.id
				body := row.body
				if row.alg == "aes-256-gcm" {
					if _, e := openSecret(current, body, []byte("citylist-security-report-v1")); e != nil {
						plain, e := openSecret(previous, body, []byte("citylist-security-report-v1"))
						if e != nil {
							return fmt.Errorf("decrypt_failed: table security_reports row %s", row.id)
						}
						body, e = sealSecret(current, plain, []byte("citylist-security-report-v1"))
						if e != nil {
							return e
						}
					}
				}
				contact := row.contact
				if len(contact) > 0 {
					if _, e := openSecret(current, contact, []byte("citylist-security-report-v1")); e != nil {
						plain, e := openSecret(previous, contact, []byte("citylist-security-report-v1"))
						if e != nil {
							return fmt.Errorf("decrypt_failed: table security_reports row %s", row.id)
						}
						contact, e = sealSecret(current, plain, []byte("citylist-security-report-v1"))
						if e != nil {
							return e
						}
					}
				}
				if !equalBytes(body, row.body) || !equalBytes(contact, row.contact) {
					if _, err = tx.ExecContext(ctx, `UPDATE security_reports SET body_enc=?,contact_enc=?,updated_at=? WHERE tracking_id=?`, body, contact, time.Now().UTC().Format(time.RFC3339Nano), row.id); err != nil {
						return err
					}
				}
			}
			count = len(batch)
			return nil
		})
		if err != nil {
			return err
		}
		if count < 500 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func openOwnerToken(key []byte, value string) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	return openSecret(key, sealed, []byte("citylist-owner-token-v1"))
}
func sealOwnerToken(key, plain []byte) (string, error) {
	sealed, err := sealSecret(key, plain, []byte("citylist-owner-token-v1"))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}
func sealSecret(key, plain, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, aad), nil
}
func openSecret(key, sealed, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("ciphertext is too short")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], aad)
}
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func readAppSecret(ctx context.Context, store *database.Store, name string) (string, error) {
	var value string
	err := store.Read(ctx, func(ctx context.Context, r database.Reader) error {
		return r.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name=?`, name).Scan(&value)
	})
	return value, err
}

func (s *Server) previousEncryptionKey(ctx context.Context) ([]byte, error) {
	if s == nil || s.store == nil || s.cfg == nil {
		return nil, errors.New("application database unavailable")
	}
	current, err := base64.StdEncoding.DecodeString(s.cfg.Server.Security.EncryptionKey)
	if err != nil || len(current) != 32 {
		return nil, errors.New("current encryption key invalid")
	}
	wrappedValue, err := readAppSecret(ctx, s.store, "encryption_key_previous")
	if err != nil {
		return nil, err
	}
	untilValue, err := readAppSecret(ctx, s.store, "encryption_key_previous_until")
	if err != nil {
		return nil, err
	}
	until, err := time.Parse(time.RFC3339, untilValue)
	if err != nil || time.Now().UTC().After(until) {
		return nil, errors.New("previous encryption key grace period ended")
	}
	wrapped, err := base64.StdEncoding.DecodeString(wrappedValue)
	if err != nil {
		return nil, err
	}
	return openSecret(current, wrapped, []byte("citylist-encryption-key-previous-v1"))
}
