package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// AdminCredentials holds admin authentication data
type AdminCredentials struct {
	Username     string
	Password     string // Plaintext (only for initial display)
	PasswordHash string // SHA-256 hash
	Token        string // Plaintext (only for initial display)
	TokenHash    string // SHA-256 hash
}

// GenerateAdminCredentials creates random admin credentials
func GenerateAdminCredentials() (*AdminCredentials, error) {
	// Generate random password (16 chars)
	password := generateRandomString(16)

	// Generate random token (32 chars)
	token := generateRandomString(32)

	// Hash password
	passwordHash := hashString(password)

	// Hash token
	tokenHash := hashString(token)

	return &AdminCredentials{
		Username:     "administrator",
		Password:     password,
		PasswordHash: passwordHash,
		Token:        token,
		TokenHash:    tokenHash,
	}, nil
}

// SaveCredentialsFile writes credentials to a file with secure permissions
func SaveCredentialsFile(configDir string, creds *AdminCredentials) error {
	content := fmt.Sprintf(`Username: %s
Password: %s
API Token: %s

Keep these credentials secure!
They will not be shown again.
`, creds.Username, creds.Password, creds.Token)

	credFile := filepath.Join(configDir, "admin_credentials")
	return os.WriteFile(credFile, []byte(content), 0600)
}

// generateRandomString creates a cryptographically secure random string
func generateRandomString(length int) string {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to less secure random if crypto/rand fails
		panic(fmt.Sprintf("failed to generate random string: %v", err))
	}
	return base64.URLEncoding.EncodeToString(bytes)[:length]
}

// hashString creates a SHA-256 hash of the input string
func hashString(s string) string {
	hash := sha256.Sum256([]byte(s))
	return hex.EncodeToString(hash[:])
}

// VerifyPassword checks if a password matches its hash
func VerifyPassword(password, hash string) bool {
	return hashString(password) == hash
}

// VerifyToken checks if a token matches its hash
func VerifyToken(token, hash string) bool {
	return hashString(token) == hash
}
