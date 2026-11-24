package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/apimgr/citylist/src/database"
)

const csrfCookieName = "csrf_token"

type csrfTokenContextKey struct{}

func (s *Server) csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && isFrontendRoute(r.URL.Path) && !isReadOnlyEndpoint(r.URL.Path) && r.Header.Get("Authorization") == "" {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if err := r.ParseForm(); err != nil {
				s.respondError(w, http.StatusForbidden, "CSRF_FAILED", "CSRF token validation failed")
				return
			}
			cookie, err := r.Cookie(csrfCookieName)
			provided := r.Header.Get("X-CSRF-Token")
			if provided == "" {
				provided = r.FormValue("csrf_token")
			}
			secret, secretErr := s.projectSecret(r.Context(), "csrf_token_secret")
			if err != nil || secretErr != nil || !verifyCSRFToken(secret, cookie.Value) || !verifyCSRFToken(secret, provided) || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(provided)) != 1 {
				s.respondError(w, http.StatusForbidden, "CSRF_FAILED", "CSRF token validation failed")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isReadOnlyEndpoint(path string) bool {
	switch path {
	case "/.well-known/security.txt", "/.well-known/llms.txt", "/llms.txt":
		return true
	default:
		return false
	}
}

func (s *Server) csrfToken(w http.ResponseWriter, r *http.Request) string {
	if token, ok := r.Context().Value(csrfTokenContextKey{}).(string); ok {
		return token
	}
	secret, err := s.projectSecret(r.Context(), "csrf_token_secret")
	if err != nil {
		return ""
	}
	if cookie, err := r.Cookie(csrfCookieName); err == nil && verifyCSRFToken(secret, cookie.Value) {
		return cookie.Value
	}
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(append([]byte("csrf-token-v1|"), value...))
	tokenBytes := append(value, mac.Sum(nil)...)
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: token, Path: "/", MaxAge: 12 * 60 * 60,
		SameSite: http.SameSiteStrictMode, Secure: s.requestUsesHTTPS(r),
	})
	*r = *r.WithContext(context.WithValue(r.Context(), csrfTokenContextKey{}, token))
	return token
}

func isFrontendRoute(path string) bool {
	if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/graphql") || path == "/mcp" {
		return false
	}
	return true
}

func validCSRFToken(token string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == 64
}

func verifyCSRFToken(secret []byte, token string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 64 || len(secret) != 32 {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(append([]byte("csrf-token-v1|"), decoded[:32]...))
	return subtle.ConstantTimeCompare(decoded[32:], mac.Sum(nil)) == 1
}

func (s *Server) projectSecret(ctx context.Context, name string) ([]byte, error) {
	if s.store == nil {
		return nil, errors.New("application database is unavailable")
	}
	var encoded string
	if err := s.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name = ?`, name).Scan(&encoded)
	}); err != nil {
		return nil, err
	}
	secret, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(secret) != 32 {
		return nil, errors.New("stored project secret is invalid")
	}
	return secret, nil
}
