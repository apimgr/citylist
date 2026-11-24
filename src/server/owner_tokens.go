package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/apimgr/citylist/src/database"
	"github.com/apimgr/citylist/src/server/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const ownerTokenCookieName = "citylist_owner_token_lgqdhqtw"
const ownerTokenViewCookieName = "citylist_token_view"

const ownerTokenAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

type ownerTokenRecord struct {
	ID         string
	OwnerID    string
	Kind       string
	Name       string
	Hash       string
	Prefix     string
	Last4      string
	Encrypted  string
	CreatedAt  int64
	LastUsedAt sql.NullInt64
	ExpiresAt  sql.NullInt64
	RevokedAt  sql.NullInt64
}

type ownerIdentity struct {
	ID     string
	Kind   string
	Prefix string
}

type ownerTokenWindow struct {
	Started time.Time
	Count   int
}

var ownerTokenRateState = struct {
	sync.Mutex
	windows map[string]ownerTokenWindow
}{windows: make(map[string]ownerTokenWindow)}

var (
	errOwnerTokenLimit        = errors.New("linked token limit reached")
	errOwnerTokenName         = errors.New("linked token name already exists")
	errOwnerTokenConfirmation = errors.New("owner token confirmation failed")
)

// handleOwnerTokensPage renders the credential-gated owner token page.
func (s *Server) handleOwnerTokensPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This route accepts GET and HEAD requests")
		return
	}
	view := tokensPageView{CSRFToken: s.csrfToken(w, r), ResourcesLabel: "items"}
	identity, ok := s.ownerCookieIdentity(w, r, true)
	if ok {
		view = s.ownerTokenPageView(r, identity.ID)
		if r.Method == http.MethodGet {
			s.consumeOwnerTokenView(w, r, identity.ID, &view)
		}
		view.CSRFToken = s.csrfToken(w, r)
	}
	s.renderTokensPage(w, r, view)
}

// handleOwnerTokenIssueWeb creates an owner identity and its primary token.
func (s *Server) handleOwnerTokenIssueWeb(w http.ResponseWriter, r *http.Request) {
	if !s.requireTokenMethod(w, r, http.MethodPost) || !s.allowOwnerTokenAction(w, r, "issue") {
		return
	}
	if _, ok := s.ownerCookieIdentity(w, r, false); ok {
		s.respondError(w, http.StatusConflict, "CONFLICT", "This browser already has an owner token")
		return
	}
	if identity, ok := s.ownerBearerIdentity(r, false); ok {
		if identity.Kind != "primary" {
			s.respondError(w, http.StatusForbidden, "FORBIDDEN", "A primary owner token is required")
			return
		}
		s.respondError(w, http.StatusConflict, "CONFLICT", "This caller already has an owner token")
		return
	}
	if s.validOperatorBearer(r) {
		s.respondError(w, http.StatusConflict, "CONFLICT", "This caller already has a valid token")
		return
	}
	ownerID, token, err := s.issuePrimary(r.Context())
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "TOKEN_CREATE_FAILED", "Unable to create an owner token")
		return
	}
	s.setOwnerCookie(w, r, token)
	s.auditOwnerToken(r, "token.created", ownerID, token[:12], "issue", true)
	s.setOwnerTokenViewCookie(w, r, "primary")
	s.safeTokenRedirect(w, r, "#primary")
}

// handleOwnerTokenIssueAPI issues a primary token without requiring a token.
func (s *Server) handleOwnerTokenIssueAPI(w http.ResponseWriter, r *http.Request) {
	if !s.requireTokenMethod(w, r, http.MethodPost) || !s.allowOwnerTokenAction(w, r, "issue") {
		return
	}
	if identity, ok := s.ownerBearerIdentity(r, false); ok {
		if identity.Kind != "primary" {
			s.respondError(w, http.StatusForbidden, "FORBIDDEN", "A primary owner token is required")
			return
		}
		s.respondError(w, http.StatusConflict, "CONFLICT", "This caller already has an owner token")
		return
	}
	if s.validOperatorBearer(r) {
		s.respondError(w, http.StatusConflict, "CONFLICT", "This caller already has a valid token")
		return
	}
	ownerID, token, err := s.issuePrimary(r.Context())
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "TOKEN_CREATE_FAILED", "Unable to create an owner token")
		return
	}
	s.auditOwnerToken(r, "token.created", ownerID, token[:12], "issue", true)
	s.respondJSON(w, http.StatusCreated, APIResponse{Success: true, Data: map[string]string{"owner_token": token}})
}

// handleOwnerTokenRestore restores an owner identity from a primary token.
func (s *Server) handleOwnerTokenRestore(w http.ResponseWriter, r *http.Request) {
	if !s.requireTokenMethod(w, r, http.MethodPost) || !s.allowOwnerTokenAction(w, r, "restore") {
		return
	}
	token := strings.TrimSpace(r.FormValue("owner_token"))
	identity, valid := s.lookupOwnerToken(r.Context(), token, true)
	if !valid || identity.Kind != "primary" {
		s.auditOwnerToken(r, "token.restored", "", "", "restore", false)
		s.respondError(w, http.StatusForbidden, "TOKEN_INVALID", "The primary owner token is invalid")
		return
	}
	s.setOwnerCookie(w, r, token)
	s.auditOwnerToken(r, "token.restored", identity.ID, identity.Prefix, "restore", true)
	safeOwnerRedirect(w, r, "/server/preferences/tokens#primary")
}

// handleOwnerTokenForget clears the browser's owner cookie but keeps the token valid.
func (s *Server) handleOwnerTokenForget(w http.ResponseWriter, r *http.Request) {
	if !s.requireTokenMethod(w, r, http.MethodPost) {
		return
	}
	s.clearOwnerCookie(w, r)
	w.Header().Set("Clear-Site-Data", `"storage"`)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/server/preferences/tokens", http.StatusSeeOther)
}

// handleOwnerTokenPrimaryAction reveals or rotates the current primary token.
func (s *Server) handleOwnerTokenPrimaryAction(w http.ResponseWriter, r *http.Request) {
	if !s.requireTokenMethod(w, r, http.MethodPost) {
		return
	}
	action := tokenActionFromPath(r.URL.Path)
	if action != "reveal" && action != "rotate" {
		s.respondError(w, http.StatusBadRequest, "INVALID_ACTION", "Unknown primary token action")
		return
	}
	if !s.allowOwnerTokenAction(w, r, action) {
		return
	}
	identity, ok := s.managementIdentity(w, r)
	if !ok {
		return
	}
	if action == "reveal" {
		token, record, err := s.decryptPrimary(r.Context(), identity.ID)
		if err != nil {
			s.respondError(w, http.StatusInternalServerError, "TOKEN_READ_FAILED", "Unable to reveal the primary token")
			return
		}
		s.auditOwnerToken(r, "token.revealed", identity.ID, record.Prefix, "primary", true)
		if s.isTokenAPIRequest(r) {
			s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{"owner_token": token}})
			return
		}
		s.setOwnerTokenViewCookie(w, r, "primary")
		s.safeTokenRedirect(w, r, "#primary")
		return
	}
	token, record, err := s.rotateOwnerToken(r.Context(), identity.ID, "")
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "TOKEN_ROTATE_FAILED", "Unable to rotate the primary token")
		return
	}
	s.setOwnerCookie(w, r, token)
	s.auditOwnerToken(r, "token.rotated", identity.ID, record.Prefix, "primary", true)
	if s.isTokenAPIRequest(r) {
		s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{"owner_token": token}})
		return
	}
	s.setOwnerTokenViewCookie(w, r, "primary")
	s.safeTokenRedirect(w, r, "#primary")
}

// handleOwnerTokenRevokePrimary deletes the owner and all attached tokens.
func (s *Server) handleOwnerTokenRevokePrimary(w http.ResponseWriter, r *http.Request) {
	method := http.MethodPost
	if s.isTokenAPIRequest(r) {
		method = http.MethodDelete
	}
	if !s.requireTokenMethod(w, r, method) || !s.allowOwnerTokenAction(w, r, "revoke") {
		return
	}
	identity, ok := s.managementIdentity(w, r)
	if !ok {
		return
	}
	source := "web"
	if s.isTokenAPIRequest(r) {
		source = "api"
	}
	confirmation, valid := ownerTokenRevokeConfirmation(w, r)
	if !valid {
		s.respondError(w, http.StatusBadRequest, "VALIDATION_FAILED", "The token confirmation is invalid")
		return
	}
	tokenCount, resourceCount, err := s.revokePrimaryOwner(r.Context(), identity.ID, confirmation, source, identity.Prefix, ownerTokenClientIP(r))
	if err != nil {
		if errors.Is(err, errOwnerTokenConfirmation) {
			s.respondError(w, http.StatusBadRequest, "VALIDATION_FAILED", "The token confirmation does not match")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			s.respondError(w, http.StatusNotFound, "NOT_FOUND", "Owner not found")
			return
		}
		s.tokenStoreError(w, err)
		return
	}
	if s.isTokenAPIRequest(r) {
		s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]int64{"tokens": tokenCount, "resources": resourceCount}})
		return
	}
	s.clearOwnerCookie(w, r)
	w.Header().Set("Clear-Site-Data", `"storage"`)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, strings.TrimSuffix(s.baseURL, "/")+"/server/preferences/tokens", http.StatusSeeOther)
}

func ownerTokenRevokeConfirmation(w http.ResponseWriter, r *http.Request) (string, bool) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Method == http.MethodDelete && mediaType == "application/json" {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var input struct {
			Confirm string `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			return "", false
		}
		return input.Confirm, true
	}
	return r.FormValue("confirm"), true
}

func (s *Server) revokePrimaryOwner(ctx context.Context, ownerID, confirmation, source, prefix, actorIP string) (int64, int64, error) {
	if s.store == nil {
		return 0, 0, errors.New("token database is unavailable")
	}
	var tokenCount int64
	const resourceCount int64 = 0
	err := s.store.Write(ctx, func(tx *sql.Tx) error {
		var last4 string
		if err := tx.QueryRowContext(ctx, `SELECT token_last4 FROM api_tokens WHERE owner_id = ? AND kind = 'primary' AND revoked_at IS NULL`, ownerID).Scan(&last4); err != nil {
			return err
		}
		if len(confirmation) != len(last4) || subtle.ConstantTimeCompare([]byte(confirmation), []byte(last4)) != 1 {
			return errOwnerTokenConfirmation
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ?`, ownerID).Scan(&tokenCount); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM owners WHERE id = ?`, ownerID)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted != 1 {
			return sql.ErrNoRows
		}
		details, err := json.Marshal(map[string]any{"scope": "owner", "tokens": tokenCount, "resources": resourceCount, "source": source, "token_prefix": prefix})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_log (level, category, action, actor_ip, target_type, target_id, details, success) VALUES ('info', 'token', 'token.revoked', ?, 'owner', ?, ?, 1)`, actorIP, ownerID, string(details))
		return err
	})
	return tokenCount, resourceCount, err
}

// handleOwnerTokensAPI lists tokens or creates a linked token for the owner.
func (s *Server) handleOwnerTokensAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		identity, ok := s.ownerBearerIdentity(r, true)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="citylist"`)
			s.respondError(w, http.StatusUnauthorized, "UNAUTHORIZED", "A valid owner token is required")
			return
		}
		if identity.Kind != "primary" {
			s.respondError(w, http.StatusForbidden, "FORBIDDEN", "A primary owner token is required")
			return
		}
		primary, linked := s.ownerTokenList(r.Context(), identity.ID)
		maxLinked := s.cfg.Server.Tokens.MaxLinked
		if maxLinked <= 0 {
			maxLinked = 25
		}
		s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]any{
			"primary": map[string]any{"id": primary.ID, "name": "", "kind": "primary", "token_masked": maskOwnerTokenLast4(primary.Last4), "created_at": time.Unix(primary.CreatedAt, 0).UTC().Format(time.RFC3339), "last_used_at": nullTokenTime(primary.LastUsedAt), "expires_at": nil, "status": "active"},
			"linked":  tokenItemsForAPI(linked), "max_linked": maxLinked,
		}})
		return
	}
	if !s.requireTokenMethod(w, r, http.MethodPost) {
		return
	}
	if !s.allowOwnerTokenAction(w, r, "issue") {
		return
	}
	identity, ok := s.managementIdentity(w, r)
	if !ok {
		return
	}
	name, expiry, valid := parseLinkedTokenForm(r)
	if !valid {
		s.respondError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Name and expiry are invalid")
		return
	}
	record, token, err := s.createLinkedToken(r.Context(), identity.ID, name, expiry)
	if err != nil {
		s.tokenStoreError(w, err)
		return
	}
	s.auditOwnerToken(r, "token.created", identity.ID, record.Prefix, "linked", true)
	data := tokenRecordForAPI(record)
	data["linked_token"] = token
	s.respondJSON(w, http.StatusCreated, APIResponse{Success: true, Data: data})
}

// handleOwnerTokenLinkedCreateWeb issues a linked token from the browser page.
func (s *Server) handleOwnerTokenLinkedCreateWeb(w http.ResponseWriter, r *http.Request) {
	if !s.requireTokenMethod(w, r, http.MethodPost) || !s.allowOwnerTokenAction(w, r, "issue") {
		return
	}
	identity, ok := s.managementIdentity(w, r)
	if !ok {
		return
	}
	name, expiry, valid := parseLinkedTokenForm(r)
	if !valid {
		s.respondError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Name and expiry are invalid")
		return
	}
	record, _, err := s.createLinkedToken(r.Context(), identity.ID, name, expiry)
	if err != nil {
		s.tokenStoreError(w, err)
		return
	}
	s.auditOwnerToken(r, "token.created", identity.ID, record.Prefix, "linked", true)
	s.setOwnerTokenViewCookie(w, r, record.ID)
	s.safeTokenRedirect(w, r, "#token-"+record.ID)
}

// handleOwnerTokenLinkedAction manages an individual linked token.
func (s *Server) handleOwnerTokenLinkedAction(w http.ResponseWriter, r *http.Request) {
	action := tokenActionFromPath(r.URL.Path)
	if s.isTokenAPIRequest(r) {
		switch r.Method {
		case http.MethodPatch:
			action = "update"
		case http.MethodDelete:
			action = "revoke"
		}
	}
	var identity ownerIdentity
	if s.isTokenAPIRequest(r) {
		var ok bool
		identity, ok = s.managementIdentity(w, r)
		if !ok {
			return
		}
	} else if action == "reveal" || action == "rotate" {
		if !s.allowOwnerTokenAction(w, r, action) {
			return
		}
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", "Token not found")
		return
	}
	if action == "update" {
		if r.Method != http.MethodPatch && r.Method != http.MethodPost {
			w.Header().Set("Allow", "PATCH, POST")
			s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
			return
		}
	} else if action == "revoke" {
		if r.Method != http.MethodDelete && r.Method != http.MethodPost {
			w.Header().Set("Allow", "DELETE, POST")
			s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
			return
		}
	} else if !s.requireTokenMethod(w, r, http.MethodPost) {
		return
	}
	if !s.isTokenAPIRequest(r) {
		var ok bool
		identity, ok = s.managementIdentity(w, r)
		if !ok {
			return
		}
	}
	switch action {
	case "reveal":
		token, record, err := s.decryptLinked(r.Context(), identity.ID, id)
		if err != nil {
			s.tokenStoreError(w, err)
			return
		}
		s.auditOwnerToken(r, "token.revealed", identity.ID, record.Prefix, "linked", true)
		if s.isTokenAPIRequest(r) {
			data := tokenRecordForAPI(record)
			data["linked_token"] = token
			s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: data})
			return
		}
		s.setOwnerTokenViewCookie(w, r, id)
		s.safeTokenRedirect(w, r, "#token-"+id)
	case "rotate":
		token, record, err := s.rotateOwnerToken(r.Context(), identity.ID, id)
		if err != nil {
			s.tokenStoreError(w, err)
			return
		}
		s.auditOwnerToken(r, "token.rotated", identity.ID, record.Prefix, "linked", true)
		if s.isTokenAPIRequest(r) {
			data := tokenRecordForAPI(record)
			data["linked_token"] = token
			s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: data})
			return
		}
		s.setOwnerTokenViewCookie(w, r, id)
		s.safeTokenRedirect(w, r, "#token-"+id)
	case "update":
		name, expiry, valid := parseLinkedTokenForm(r)
		if !valid {
			s.respondError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Name and expiry are invalid")
			return
		}
		record, err := s.updateLinkedToken(r.Context(), identity.ID, id, name, expiry)
		if err != nil {
			s.tokenStoreError(w, err)
			return
		}
		if s.isTokenAPIRequest(r) {
			s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: tokenRecordForAPI(record)})
			return
		}
		s.safeTokenRedirect(w, r, "#token-"+id)
	case "revoke":
		record, err := s.revokeLinkedToken(r.Context(), identity.ID, id)
		if err != nil {
			s.tokenStoreError(w, err)
			return
		}
		s.auditOwnerToken(r, "token.revoked", identity.ID, record.Prefix, "linked", true)
		if s.isTokenAPIRequest(r) {
			s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]any{"id": record.ID, "status": "revoked"}})
			return
		}
		s.safeTokenRedirect(w, r, "#token-"+id)
	default:
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", "Token action not found")
	}
}

func (s *Server) issuePrimary(ctx context.Context) (string, string, error) {
	if s.store == nil {
		return "", "", errors.New("token database is unavailable")
	}
	ownerID, err := newOwnerID()
	if err != nil {
		return "", "", err
	}
	token, err := newOwnerToken()
	if err != nil {
		return "", "", err
	}
	prepared, err := s.prepareOwnerToken(token)
	if err != nil {
		return "", "", err
	}
	tokenID, err := newOwnerTokenID()
	if err != nil {
		return "", "", err
	}
	err = s.store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO owners (id) VALUES (?)`, ownerID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (id, owner_id, kind, name, token_hash, token_prefix, token_last4, token_enc) VALUES (?, ?, 'primary', NULL, ?, ?, ?, ?)`, tokenID, ownerID, prepared.Hash, prepared.Prefix, prepared.Last4, prepared.Encrypted)
		return err
	})
	if err != nil {
		return "", "", err
	}
	return ownerID, token, nil
}

func newOwnerTokenID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate owner-token UUID: %w", err)
	}
	return id.String(), nil
}

// MigrateOwnerTokenLast4 backfills the display and revoke-confirmation suffix
// once for tokens created before token_last4 became part of the schema.
func (s *Server) MigrateOwnerTokenLast4(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	var records []ownerTokenRecord
	if err := s.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		rows, err := reader.QueryContext(ctx, `SELECT id, token_enc FROM api_tokens WHERE token_last4 = ''`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var record ownerTokenRecord
			if err := rows.Scan(&record.ID, &record.Encrypted); err != nil {
				return err
			}
			records = append(records, record)
		}
		return rows.Err()
	}); err != nil {
		return fmt.Errorf("read owner-token suffixes: %w", err)
	}
	if len(records) == 0 {
		return nil
	}
	for index := range records {
		token, err := s.decryptOwnerTokenWithGrace(ctx, records[index])
		if err != nil {
			return fmt.Errorf("decrypt legacy owner token %s: %w", records[index].ID, err)
		}
		if !validOwnerToken(token) {
			return fmt.Errorf("legacy owner token %s has an invalid format", records[index].ID)
		}
		records[index].Last4 = token[len(token)-4:]
	}
	return s.store.Write(ctx, func(tx *sql.Tx) error {
		for _, record := range records {
			if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET token_last4 = ? WHERE id = ? AND token_last4 = ''`, record.Last4, record.ID); err != nil {
				return fmt.Errorf("backfill owner-token suffix %s: %w", record.ID, err)
			}
		}
		return nil
	})
}

func (s *Server) createLinkedToken(ctx context.Context, ownerID, name string, expiry sql.NullInt64) (ownerTokenRecord, string, error) {
	if s.store == nil {
		return ownerTokenRecord{}, "", errors.New("token database is unavailable")
	}
	token, err := newOwnerToken()
	if err != nil {
		return ownerTokenRecord{}, "", err
	}
	prepared, err := s.prepareOwnerToken(token)
	if err != nil {
		return ownerTokenRecord{}, "", err
	}
	tokenID, err := newOwnerTokenID()
	if err != nil {
		return ownerTokenRecord{}, "", err
	}
	var record ownerTokenRecord
	err = s.store.Write(ctx, func(tx *sql.Tx) error {
		now := time.Now().Unix()
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ? AND kind = 'linked' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, ownerID, now).Scan(&count); err != nil {
			return err
		}
		limit := s.cfg.Server.Tokens.MaxLinked
		if limit <= 0 {
			limit = 25
		}
		if count >= limit {
			return errOwnerTokenLimit
		}
		var duplicate int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ? AND kind = 'linked' AND lower(name) = lower(?) AND revoked_at IS NULL`, ownerID, name).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate != 0 {
			return errOwnerTokenName
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (id, owner_id, kind, name, token_hash, token_prefix, token_last4, token_enc, expires_at) VALUES (?, ?, 'linked', ?, ?, ?, ?, ?, ?)`, tokenID, ownerID, name, prepared.Hash, prepared.Prefix, prepared.Last4, prepared.Encrypted, nullableTokenInt(expiry))
		if err != nil {
			return err
		}
		record.ID = tokenID
		record.OwnerID, record.Kind, record.Name = ownerID, "linked", name
		record.Hash, record.Prefix, record.Last4, record.Encrypted = prepared.Hash, prepared.Prefix, prepared.Last4, prepared.Encrypted
		record.CreatedAt, record.ExpiresAt = now, expiry
		return nil
	})
	if err != nil {
		return ownerTokenRecord{}, "", err
	}
	return record, token, nil
}

func (s *Server) updateLinkedToken(ctx context.Context, ownerID, id, name string, expiry sql.NullInt64) (ownerTokenRecord, error) {
	if s.store == nil {
		return ownerTokenRecord{}, errors.New("token database is unavailable")
	}
	var record ownerTokenRecord
	err := s.store.Write(ctx, func(tx *sql.Tx) error {
		var duplicate int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ? AND kind = 'linked' AND lower(name) = lower(?) AND id <> ? AND revoked_at IS NULL`, ownerID, name, id).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate > 0 {
			return errOwnerTokenName
		}
		result, err := tx.ExecContext(ctx, `UPDATE api_tokens SET name = ?, expires_at = ? WHERE id = ? AND owner_id = ? AND kind = 'linked' AND revoked_at IS NULL`, name, nullableTokenInt(expiry), id, ownerID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return sql.ErrNoRows
		}
		return scanOwnerTokenRecord(tx.QueryRowContext(ctx, ownerTokenSelect+` WHERE id = ? AND owner_id = ?`, id, ownerID), &record)
	})
	return record, err
}

func (s *Server) revokeLinkedToken(ctx context.Context, ownerID, id string) (ownerTokenRecord, error) {
	if s.store == nil {
		return ownerTokenRecord{}, errors.New("token database is unavailable")
	}
	var record ownerTokenRecord
	err := s.store.Write(ctx, func(tx *sql.Tx) error {
		if err := scanOwnerTokenRecord(tx.QueryRowContext(ctx, ownerTokenSelect+` WHERE id = ? AND owner_id = ? AND kind = 'linked' AND revoked_at IS NULL`, id, ownerID), &record); err != nil {
			return sql.ErrNoRows
		}
		_, err := tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ?, revoked_reason = 'owner_revoked' WHERE id = ? AND owner_id = ? AND kind = 'linked' AND revoked_at IS NULL`, time.Now().Unix(), id, ownerID)
		return err
	})
	return record, err
}

func (s *Server) rotateOwnerToken(ctx context.Context, ownerID, linkedID string) (string, ownerTokenRecord, error) {
	if s.store == nil {
		return "", ownerTokenRecord{}, errors.New("token database is unavailable")
	}
	token, err := newOwnerToken()
	if err != nil {
		return "", ownerTokenRecord{}, err
	}
	prepared, err := s.prepareOwnerToken(token)
	if err != nil {
		return "", ownerTokenRecord{}, err
	}
	var record ownerTokenRecord
	err = s.store.Write(ctx, func(tx *sql.Tx) error {
		query := ownerTokenSelect + ` WHERE owner_id = ? AND kind = 'primary' AND revoked_at IS NULL LIMIT 1`
		args := []any{ownerID}
		if linkedID != "" {
			query = ownerTokenSelect + ` WHERE id = ? AND owner_id = ? AND kind = 'linked' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`
			args = []any{linkedID, ownerID, time.Now().Unix()}
		}
		if err := scanOwnerTokenRecord(tx.QueryRowContext(ctx, query, args...), &record); err != nil {
			return sql.ErrNoRows
		}
		_, err := tx.ExecContext(ctx, `UPDATE api_tokens SET token_hash = ?, token_prefix = ?, token_last4 = ?, token_enc = ?, rotated_at = ? WHERE id = ?`, prepared.Hash, prepared.Prefix, prepared.Last4, prepared.Encrypted, time.Now().Unix(), record.ID)
		if err != nil {
			return err
		}
		record.Hash, record.Prefix, record.Last4, record.Encrypted = prepared.Hash, prepared.Prefix, prepared.Last4, prepared.Encrypted
		return nil
	})
	return token, record, err
}

func (s *Server) decryptPrimary(ctx context.Context, ownerID string) (string, ownerTokenRecord, error) {
	var record ownerTokenRecord
	if s.store == nil {
		return "", record, errors.New("token database is unavailable")
	}
	err := s.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return scanOwnerTokenRecord(reader.QueryRowContext(ctx, ownerTokenSelect+` WHERE owner_id = ? AND kind = 'primary' AND revoked_at IS NULL LIMIT 1`, ownerID), &record)
	})
	if err != nil {
		return "", record, err
	}
	token, err := s.decryptOwnerTokenWithGrace(ctx, record)
	return token, record, err
}

func (s *Server) decryptLinked(ctx context.Context, ownerID, id string) (string, ownerTokenRecord, error) {
	var record ownerTokenRecord
	if s.store == nil {
		return "", record, errors.New("token database is unavailable")
	}
	err := s.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return scanOwnerTokenRecord(reader.QueryRowContext(ctx, ownerTokenSelect+` WHERE id = ? AND owner_id = ? AND kind = 'linked' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, id, ownerID, time.Now().Unix()), &record)
	})
	if err != nil {
		return "", record, err
	}
	token, err := s.decryptOwnerTokenWithGrace(ctx, record)
	return token, record, err
}

const ownerTokenSelect = `SELECT id, owner_id, kind, name, token_hash, token_prefix, token_last4, token_enc, created_at, last_used_at, expires_at, revoked_at FROM api_tokens`

func scanOwnerTokenRecord(row *sql.Row, record *ownerTokenRecord) error {
	var name sql.NullString
	err := row.Scan(&record.ID, &record.OwnerID, &record.Kind, &name, &record.Hash, &record.Prefix, &record.Last4, &record.Encrypted, &record.CreatedAt, &record.LastUsedAt, &record.ExpiresAt, &record.RevokedAt)
	if name.Valid {
		record.Name = name.String
	}
	return err
}

func (s *Server) prepareOwnerToken(token string) (ownerTokenRecord, error) {
	hash := sha256.Sum256([]byte(token))
	encrypted, err := s.encryptOwnerToken(token)
	if err != nil {
		return ownerTokenRecord{}, err
	}
	return ownerTokenRecord{Hash: hex.EncodeToString(hash[:]), Prefix: token[:12], Last4: token[len(token)-4:], Encrypted: encrypted}, nil
}

func (s *Server) encryptOwnerToken(token string) (string, error) {
	key, err := base64.StdEncoding.DecodeString(s.cfg.Server.Security.EncryptionKey)
	if err != nil || len(key) != 32 {
		return "", errors.New("token encryption key is invalid")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(token), []byte("citylist-owner-token-v1"))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *Server) decryptOwnerToken(value string) (string, error) {
	key, err := base64.StdEncoding.DecodeString(s.cfg.Server.Security.EncryptionKey)
	if err != nil || len(key) != 32 {
		return "", errors.New("token encryption key is invalid")
	}
	return decryptOwnerTokenWithKey(value, key)
}

func decryptOwnerTokenWithKey(value string, key []byte) (string, error) {
	sealed, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return "", errors.New("encrypted token is invalid")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte("citylist-owner-token-v1"))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Server) decryptOwnerTokenWithGrace(ctx context.Context, record ownerTokenRecord) (string, error) {
	value, err := s.decryptOwnerToken(record.Encrypted)
	if err == nil {
		return value, nil
	}
	previous, keyErr := s.previousEncryptionKey(ctx)
	if keyErr != nil {
		slog.Error("security.decrypt_failed", "table", "api_tokens", "row_id", record.ID)
		return "", err
	}
	value, err = decryptOwnerTokenWithKey(record.Encrypted, previous)
	if err != nil {
		slog.Error("security.decrypt_failed", "table", "api_tokens", "row_id", record.ID)
		return "", err
	}
	replacement, err := s.encryptOwnerToken(value)
	if err != nil {
		return "", err
	}
	if err := s.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE api_tokens SET token_enc = ? WHERE id = ?`, replacement, record.ID)
		return err
	}); err != nil {
		return "", err
	}
	return value, nil
}

type primaryTokenPageItem struct {
	ID         string
	Last4      string
	CreatedAt  int64
	LastUsedAt sql.NullInt64
}

func (s *Server) ownerTokenPageView(r *http.Request, ownerID string) tokensPageView {
	primaryLast4, linked := s.ownerTokenList(r.Context(), ownerID)
	return tokensPageView{
		HasOwner: true, PrimaryMasked: maskOwnerTokenLast4(primaryLast4.Last4), PrimaryLast4: primaryLast4.Last4,
		PrimaryStatus: "Active", LinkedTokens: linked, LinkedCount: len(linked), TokenCount: len(linked) + 1,
		ResourcesLabel: "items",
	}
}

func (s *Server) ownerTokenList(ctx context.Context, ownerID string) (primaryTokenPageItem, []linkedTokenPageItem) {
	if s.store == nil {
		return primaryTokenPageItem{}, nil
	}
	primary := primaryTokenPageItem{}
	linked := make([]linkedTokenPageItem, 0)
	err := s.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		if err := reader.QueryRowContext(ctx, `SELECT id, token_last4, created_at, last_used_at FROM api_tokens WHERE owner_id = ? AND kind = 'primary' AND revoked_at IS NULL LIMIT 1`, ownerID).Scan(&primary.ID, &primary.Last4, &primary.CreatedAt, &primary.LastUsedAt); err != nil {
			return err
		}
		rows, err := reader.QueryContext(ctx, `SELECT id, name, token_last4, created_at, last_used_at, expires_at, revoked_at FROM api_tokens WHERE owner_id = ? AND kind = 'linked' ORDER BY created_at DESC, id DESC`, ownerID)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var item linkedTokenPageItem
			var id string
			var created int64
			var name sql.NullString
			var last4 string
			var used, expires, revoked sql.NullInt64
			if err := rows.Scan(&id, &name, &last4, &created, &used, &expires, &revoked); err != nil {
				return err
			}
			item.ID = id
			item.Name = name.String
			item.Masked = maskOwnerTokenLast4(last4)
			item.CreatedAt = time.Unix(created, 0).UTC().Format(time.RFC3339)
			item.LastUsedAt = formatOwnerTokenTime(used)
			item.ExpiresAt = formatOwnerTokenTime(expires)
			item.Status = "Active"
			if revoked.Valid {
				item.Status = "Revoked"
			} else if expires.Valid && expires.Int64 <= time.Now().Unix() {
				item.Status = "Expired"
			}
			linked = append(linked, item)
		}
		return rows.Err()
	})
	if err != nil {
		return primaryTokenPageItem{}, nil
	}
	return primary, linked
}

// ResolveBearerOwner validates an owner Bearer token and refreshes last-used state.
func (s *Server) ResolveBearerOwner(r *http.Request) (ownerID, kind string, ok bool) {
	identity, ok := s.ownerBearerIdentity(r, true)
	return identity.ID, identity.Kind, ok
}

// ResolvePrimaryBearerOwner requires a valid primary owner Bearer token.
func (s *Server) ResolvePrimaryBearerOwner(r *http.Request) (ownerID string, ok bool) {
	identity, valid := s.ownerBearerIdentity(r, true)
	return identity.ID, valid && identity.Kind == "primary"
}

func (s *Server) lookupOwnerToken(ctx context.Context, token string, updateUse bool) (ownerIdentity, bool) {
	if !validOwnerToken(token) || s.store == nil {
		return ownerIdentity{}, false
	}
	hash := sha256.Sum256([]byte(token))
	hashText := hex.EncodeToString(hash[:])
	var identity ownerIdentity
	valid := false
	err := s.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		rows, err := reader.QueryContext(ctx, `SELECT id, owner_id, kind, token_hash, token_prefix, revoked_at, expires_at, last_used_at FROM api_tokens WHERE token_prefix = ?`, token[:12])
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			var ownerID, kind, storedHash, prefix string
			var revokedAt, expiresAt, lastUsed sql.NullInt64
			if err := rows.Scan(&id, &ownerID, &kind, &storedHash, &prefix, &revokedAt, &expiresAt, &lastUsed); err != nil {
				return err
			}
			matches := subtle.ConstantTimeCompare([]byte(storedHash), []byte(hashText)) == 1
			if !matches || revokedAt.Valid || expiresAt.Valid && expiresAt.Int64 <= time.Now().Unix() {
				continue
			}
			identity = ownerIdentity{ID: ownerID, Kind: kind, Prefix: prefix}
			valid = true
			if updateUse && (!lastUsed.Valid || time.Now().Unix()-lastUsed.Int64 >= 60) {
				if err := s.updateOwnerTokenUse(ctx, id, ownerID); err != nil {
					if s.store.UsingOfflineCache() {
						return nil
					}
					return err
				}
			}
			break
		}
		return rows.Err()
	})
	return identity, err == nil && valid
}

func (s *Server) updateOwnerTokenUse(ctx context.Context, tokenID, ownerID string) error {
	if s.store == nil {
		return errors.New("token database is unavailable")
	}
	now := time.Now().Unix()
	return s.store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at <= ?)`, now, tokenID, now-60); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE owners SET last_seen_at = ? WHERE id = ?`, now, ownerID)
		return err
	})
}

func (s *Server) ownerBearerIdentity(r *http.Request, updateUse bool) (ownerIdentity, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		metrics.AuthAttempts.WithLabelValues("owner_token", "failed").Inc()
		return ownerIdentity{}, false
	}
	identity, valid := s.lookupOwnerToken(r.Context(), parts[1], updateUse)
	status := "failed"
	if valid {
		status = "success"
	}
	metrics.AuthAttempts.WithLabelValues("owner_token", status).Inc()
	return identity, valid
}

func (s *Server) validOperatorBearer(r *http.Request) bool {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || s.cfg.Server.Token == "" {
		return false
	}
	provided := sha256.Sum256([]byte(parts[1]))
	wanted := sha256.Sum256([]byte(s.cfg.Server.Token))
	return subtle.ConstantTimeCompare(provided[:], wanted[:]) == 1
}

func (s *Server) ownerCookieIdentity(w http.ResponseWriter, r *http.Request, refresh bool) (ownerIdentity, bool) {
	cookie, err := r.Cookie(s.ownerCookieName())
	if err != nil {
		return ownerIdentity{}, false
	}
	identity, valid := s.lookupOwnerToken(r.Context(), cookie.Value, true)
	if !valid || identity.Kind != "primary" {
		s.clearOwnerCookie(w, r)
		return ownerIdentity{}, false
	}
	if refresh {
		s.setOwnerCookie(w, r, cookie.Value)
	}
	return identity, true
}

func (s *Server) managementIdentity(w http.ResponseWriter, r *http.Request) (ownerIdentity, bool) {
	var identity ownerIdentity
	var valid bool
	if s.isTokenAPIRequest(r) {
		identity, valid = s.ownerBearerIdentity(r, true)
	} else if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		identity, valid = s.ownerBearerIdentity(r, true)
	} else {
		identity, valid = s.ownerCookieIdentity(w, r, true)
	}
	if !valid || identity.Kind != "primary" {
		s.respondError(w, http.StatusForbidden, "FORBIDDEN", "A primary owner token is required")
		return ownerIdentity{}, false
	}
	return identity, true
}

func (s *Server) ownerCookieName() string {
	if s.cfg != nil && s.cfg.Server.Tokens.CookieName != "" {
		return s.cfg.Server.Tokens.CookieName
	}
	return ownerTokenCookieName
}

func (s *Server) setOwnerCookie(w http.ResponseWriter, r *http.Request, token string) {
	maxAge := s.cfg.Server.Tokens.CookieMaxAgeSeconds()
	if maxAge <= 0 {
		maxAge = 365 * 24 * 60 * 60
	}
	http.SetCookie(w, &http.Cookie{Name: s.ownerCookieName(), Value: token, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteStrictMode})
}

func (s *Server) clearOwnerCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: s.ownerCookieName(), Value: "", Path: "/", MaxAge: 0, HttpOnly: true, Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteStrictMode})
}

func (s *Server) ownerTokenViewPath() string {
	return strings.TrimSuffix(s.baseURL, "/") + "/server/preferences/tokens"
}

func (s *Server) setOwnerTokenViewCookie(w http.ResponseWriter, r *http.Request, card string) {
	http.SetCookie(w, &http.Cookie{Name: ownerTokenViewCookieName, Value: card, Path: s.ownerTokenViewPath(), MaxAge: 60, HttpOnly: true, Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteStrictMode})
}

func (s *Server) clearOwnerTokenViewCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: ownerTokenViewCookieName, Value: "", Path: s.ownerTokenViewPath(), MaxAge: 0, HttpOnly: true, Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteStrictMode})
}

func (s *Server) consumeOwnerTokenView(w http.ResponseWriter, r *http.Request, ownerID string, view *tokensPageView) {
	cookie, err := r.Cookie(ownerTokenViewCookieName)
	if err != nil {
		return
	}
	s.clearOwnerTokenViewCookie(w, r)
	if cookie.Value == "primary" {
		token, _, err := s.decryptPrimary(r.Context(), ownerID)
		if err == nil {
			view.PrimaryRevealed = token
		}
		return
	}
	id := cookie.Value
	if _, err := uuid.Parse(id); err != nil {
		return
	}
	token, _, err := s.decryptLinked(r.Context(), ownerID, id)
	if err != nil {
		return
	}
	for index := range view.LinkedTokens {
		if view.LinkedTokens[index].ID == id {
			view.LinkedTokens[index].Revealed = token
			return
		}
	}
}

func (s *Server) allowOwnerTokenAction(w http.ResponseWriter, r *http.Request, action string) bool {
	key := action + ":ip:" + ownerTokenClientIP(r)
	if identity, ok := s.ownerCookieIdentityNoRefresh(r); ok {
		key = action + ":owner:" + identity.ID
	} else if identity, ok := s.ownerBearerIdentity(r, false); ok {
		key = action + ":owner:" + identity.ID
	}
	now := time.Now()
	strictLimit := 10
	strictWindow := 900 * time.Second
	if s.cfg != nil {
		if configured := s.cfg.Server.RateLimit.Strict; configured.Requests > 0 {
			strictLimit = configured.Requests
		}
		if configured := s.cfg.Server.RateLimit.Strict; configured.Window > 0 {
			strictWindow = time.Duration(configured.Window) * time.Second
		}
	}
	ownerTokenRateState.Lock()
	defer ownerTokenRateState.Unlock()
	for candidate, window := range ownerTokenRateState.windows {
		if now.Sub(window.Started) >= strictWindow {
			delete(ownerTokenRateState.windows, candidate)
		}
	}
	if len(ownerTokenRateState.windows) >= 100000 {
		if window, ok := ownerTokenRateState.windows[key]; !ok || now.Sub(window.Started) >= strictWindow {
			w.Header().Set("Retry-After", "900")
			s.respondError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Token action rate limit exceeded")
			return false
		}
	}
	window := ownerTokenRateState.windows[key]
	if window.Started.IsZero() || now.Sub(window.Started) >= strictWindow {
		window = ownerTokenWindow{Started: now}
	}
	if window.Count >= strictLimit {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(time.Until(window.Started.Add(strictWindow)).Seconds()))))
		s.respondError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Token action rate limit exceeded")
		return false
	}
	window.Count++
	ownerTokenRateState.windows[key] = window
	return true
}

func (s *Server) ownerCookieIdentityNoRefresh(r *http.Request) (ownerIdentity, bool) {
	cookie, err := r.Cookie(s.ownerCookieName())
	if err != nil {
		return ownerIdentity{}, false
	}
	identity, valid := s.lookupOwnerToken(r.Context(), cookie.Value, false)
	return identity, valid && identity.Kind == "primary"
}

func (s *Server) auditOwnerToken(r *http.Request, action, ownerID, prefix, source string, success bool) {
	if s.store == nil {
		return
	}
	details, _ := json.Marshal(map[string]string{"source": source, "token_prefix": prefix})
	level := "info"
	if !success {
		level = "warn"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	_ = s.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (level, category, action, actor_ip, target_type, target_id, details, success) VALUES (?, 'token', ?, ?, 'owner', ?, ?, ?)`, level, action, ownerTokenClientIP(r), ownerID, string(details), success)
		return err
	})
}

func (s *Server) requireTokenMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
	return false
}

func (s *Server) isTokenAPIRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/")
}

func tokenActionFromPath(path string) string {
	path = strings.TrimSuffix(path, "/")
	if index := strings.LastIndexByte(path, '/'); index >= 0 {
		return path[index+1:]
	}
	return ""
}

func safeOwnerRedirect(w http.ResponseWriter, r *http.Request, fallback string) {
	location := fallback
	if target := safePreferenceTarget(r.FormValue("redirect")); target != "" {
		location = target
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func (s *Server) safeTokenRedirect(w http.ResponseWriter, r *http.Request, fragment string) {
	if s.isTokenAPIRequest(r) {
		s.respondSuccess(w, map[string]bool{"updated": true}, 1)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, strings.TrimSuffix(s.baseURL, "/")+"/server/preferences/tokens"+fragment, http.StatusSeeOther)
}

func (s *Server) tokenStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", "Token not found")
	case errors.Is(err, errOwnerTokenLimit):
		s.respondError(w, http.StatusConflict, "TOKEN_LIMIT", "The maximum number of active linked tokens has been reached")
	case errors.Is(err, errOwnerTokenName):
		s.respondError(w, http.StatusConflict, "TOKEN_NAME_CONFLICT", "A linked token with this name already exists")
	default:
		s.respondError(w, http.StatusInternalServerError, "TOKEN_OPERATION_FAILED", "Unable to complete the token operation")
	}
}

func ownerTokenClientIP(r *http.Request) string {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		return host
	}
	return ip
}

func newOwnerID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func newOwnerToken() (string, error) {
	var result strings.Builder
	result.Grow(36)
	result.WriteString("own_")
	var buffer [64]byte
	for result.Len() < 36 {
		if _, err := rand.Read(buffer[:]); err != nil {
			return "", err
		}
		for _, value := range buffer {
			if value >= 248 {
				continue
			}
			result.WriteByte(ownerTokenAlphabet[int(value)%len(ownerTokenAlphabet)])
			if result.Len() == 36 {
				break
			}
		}
	}
	return result.String(), nil
}

func validOwnerToken(token string) bool {
	if len(token) != 36 || !strings.HasPrefix(token, "own_") {
		return false
	}
	for _, value := range token[4:] {
		if !strings.ContainsRune(ownerTokenAlphabet, value) {
			return false
		}
	}
	return true
}

func maskOwnerTokenLast4(last4 string) string {
	if len(last4) != 4 {
		return "own_••••"
	}
	return "own_••••••••••••••••••••••••" + last4
}

func formatOwnerTokenTime(value sql.NullInt64) string {
	if !value.Valid {
		return "Never"
	}
	return time.Unix(value.Int64, 0).UTC().Format(time.RFC3339)
}

func nullableTokenInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func nullTokenTime(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return time.Unix(value.Int64, 0).UTC().Format(time.RFC3339)
}

func parseLinkedTokenForm(r *http.Request) (string, sql.NullInt64, bool) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "application/json" {
		r.Body = http.MaxBytesReader(nil, r.Body, 1<<16)
		var input struct {
			Name      string `json:"name"`
			Expires   string `json:"expires"`
			ExpiresOn string `json:"expires_on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			return "", sql.NullInt64{}, false
		}
		name := strings.TrimSpace(input.Name)
		if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 64 || strings.ContainsFunc(name, unicode.IsControl) {
			return "", sql.NullInt64{}, false
		}
		if input.Expires != "" && input.ExpiresOn != "" {
			return "", sql.NullInt64{}, false
		}
		expiry, ok := parseOwnerTokenExpiry(input.Expires, input.ExpiresOn)
		return name, expiry, ok
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 64 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", sql.NullInt64{}, false
	}
	expiresOn := r.FormValue("expires_on")
	expiresAt := r.FormValue("expires_at")
	if expiresOn != "" && expiresAt != "" {
		return "", sql.NullInt64{}, false
	}
	if expiresOn != "" {
		expiry, valid := parseOwnerTokenExpiryDate(expiresOn)
		return name, expiry, valid
	}
	expiry, valid := parseOwnerTokenExpiry(r.FormValue("expiry"), expiresAt)
	return name, expiry, valid
}

func parseOwnerTokenExpiryDate(value string) (sql.NullInt64, bool) {
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return sql.NullInt64{}, false
	}
	endOfDay := date.UTC().Add(24*time.Hour - time.Second)
	if endOfDay.Before(time.Now().UTC()) {
		return sql.NullInt64{}, false
	}
	return sql.NullInt64{Int64: endOfDay.Unix(), Valid: true}, true
}

func parseOwnerTokenExpiry(expiry, custom string) (sql.NullInt64, bool) {
	now := time.Now().UTC()
	switch expiry {
	case "", "never":
		return sql.NullInt64{}, true
	case "7d":
		return sql.NullInt64{Int64: now.Add(7 * 24 * time.Hour).Unix(), Valid: true}, true
	case "30d":
		return sql.NullInt64{Int64: now.Add(30 * 24 * time.Hour).Unix(), Valid: true}, true
	case "90d":
		return sql.NullInt64{Int64: now.Add(90 * 24 * time.Hour).Unix(), Valid: true}, true
	case "1y":
		return sql.NullInt64{Int64: now.AddDate(1, 0, 0).Unix(), Valid: true}, true
	case "custom":
		return parseOwnerTokenExpiryDate(custom)
	default:
		return sql.NullInt64{}, false
	}
}

func tokenItemsForAPI(items []linkedTokenPageItem) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"id": item.ID, "name": item.Name, "kind": "linked", "token_masked": item.Masked,
			"created_at": item.CreatedAt, "last_used_at": tokenPageTimeForAPI(item.LastUsedAt),
			"expires_at": tokenPageTimeForAPI(item.ExpiresAt), "status": strings.ToLower(item.Status),
		})
	}
	return result
}

func tokenPageTimeForAPI(value string) any {
	if value == "" || value == "Never" {
		return nil
	}
	return value
}

func tokenRecordForAPI(record ownerTokenRecord) map[string]any {
	return map[string]any{
		"id": record.ID, "name": record.Name, "kind": record.Kind,
		"token_masked": maskOwnerTokenLast4(record.Last4),
		"created_at":   time.Unix(record.CreatedAt, 0).UTC().Format(time.RFC3339),
		"last_used_at": formatOwnerTokenTime(record.LastUsedAt),
		"expires_at":   nullTokenTime(record.ExpiresAt), "status": "active",
	}
}
