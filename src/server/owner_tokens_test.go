package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"github.com/google/uuid"
)

func TestOwnerTokenIssueListAndLinkedTokenLifecycle(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	s := testServerWithConfig(t, cfg)
	issue := doRequest(t, s, http.MethodPost, "/api/v1/server/preferences/tokens/primary", nil)
	if issue.Code != http.StatusCreated {
		t.Fatalf("issue primary = %d: %s", issue.Code, issue.Body.String())
	}
	var issued struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(issue.Body.Bytes(), &issued); err != nil {
		t.Fatalf("decode issue response: %v", err)
	}
	primary := issued.Data["owner_token"]
	if !validOwnerToken(primary) {
		t.Fatalf("issued owner token is invalid")
	}

	create := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens", strings.NewReader(url.Values{"name": {"automation"}, "expiry": {"30d"}}.Encode()))
	create.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	create.Header.Set("Accept", "application/json")
	create.Header.Set("Authorization", "Bearer "+primary)
	created := httptest.NewRecorder()
	s.Router().ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create linked token = %d: %s", created.Code, created.Body.String())
	}
	var linked struct {
		Data struct {
			ID    string `json:"id"`
			Token string `json:"linked_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &linked); err != nil {
		t.Fatalf("decode linked token response: %v", err)
	}
	linkedID, err := uuid.Parse(linked.Data.ID)
	if err != nil || linkedID.Version() != 7 || !validOwnerToken(linked.Data.Token) {
		t.Fatalf("invalid linked token response: %+v", linked.Data)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/tokens", nil)
	listReq.Header.Set("Accept", "application/json")
	listReq.Header.Set("Authorization", "Bearer "+primary)
	listed := httptest.NewRecorder()
	s.Router().ServeHTTP(listed, listReq)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "automation") || strings.Contains(listed.Body.String(), linked.Data.Token) {
		t.Fatalf("list tokens = %d, body %s", listed.Code, listed.Body.String())
	}

	linkedListReq := httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/tokens", nil)
	linkedListReq.Header.Set("Authorization", "Bearer "+linked.Data.Token)
	linkedList := httptest.NewRecorder()
	s.Router().ServeHTTP(linkedList, linkedListReq)
	if linkedList.Code != http.StatusForbidden {
		t.Fatalf("linked token management status = %d, want 403", linkedList.Code)
	}

	revokeReq := httptest.NewRequest(http.MethodDelete, "/api/v1/server/preferences/tokens/"+linked.Data.ID, nil)
	revokeReq.Header.Set("Authorization", "Bearer "+primary)
	revoked := httptest.NewRecorder()
	s.Router().ServeHTTP(revoked, revokeReq)
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke linked token = %d: %s", revoked.Code, revoked.Body.String())
	}
	if _, _, ok := s.ResolveBearerOwner(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Fatal("request without a bearer token resolved an owner")
	}
}

func TestOwnerTokenIssueRejectsExistingOwnerAndCookieAuthIsNotAPIAuth(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	s := testServerWithConfig(t, cfg)
	issue := doRequest(t, s, http.MethodPost, "/api/v1/server/preferences/tokens/primary", nil)
	if issue.Code != http.StatusCreated {
		t.Fatalf("issue primary = %d", issue.Code)
	}
	var issued struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(issue.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	token := issued.Data["owner_token"]

	conflictReq := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens/primary", nil)
	conflictReq.Header.Set("Authorization", "Bearer "+token)
	conflict := httptest.NewRecorder()
	s.Router().ServeHTTP(conflict, conflictReq)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("issue with existing token = %d, want 409", conflict.Code)
	}

	cookieReq := httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/tokens", nil)
	cookieReq.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: token})
	cookieOnly := httptest.NewRecorder()
	s.Router().ServeHTTP(cookieOnly, cookieReq)
	if cookieOnly.Code != http.StatusUnauthorized {
		t.Fatalf("API cookie-only auth status = %d, want 401", cookieOnly.Code)
	}
}

func TestOwnerTokenBrowserPageIssueRevealAndForget(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	s := testServerWithConfig(t, cfg)

	page := doRequest(t, s, http.MethodGet, "/server/preferences/tokens", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Use an existing token") || strings.Contains(page.Body.String(), "linked_tokens") {
		t.Fatalf("ownerless token page = %d: %s", page.Code, page.Body.String())
	}
	var csrfCookie *http.Cookie
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			csrfCookie = cookie
		}
	}
	if csrfCookie == nil || !verifyCSRFToken(mustProjectSecret(t, s, "csrf_token_secret"), csrfCookie.Value) {
		t.Fatal("token page did not issue a valid CSRF cookie")
	}

	postForm := func(path string, fields url.Values, ownerCookie *http.Cookie) *httptest.ResponseRecorder {
		fields.Set("csrf_token", csrfCookie.Value)
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(fields.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Accept", "text/html")
		request.RemoteAddr = "192.0.2.15:1234"
		request.AddCookie(csrfCookie)
		if ownerCookie != nil {
			request.AddCookie(ownerCookie)
		}
		response := httptest.NewRecorder()
		s.Router().ServeHTTP(response, request)
		return response
	}

	issued := postForm("/server/preferences/token/create", url.Values{}, nil)
	if issued.Code != http.StatusSeeOther || strings.Contains(issued.Body.String(), "own_") {
		t.Fatalf("web token issue = %d: %s", issued.Code, issued.Body.String())
	}
	var ownerCookie *http.Cookie
	var tokenViewCookie *http.Cookie
	for _, cookie := range issued.Result().Cookies() {
		if cookie.Name == s.ownerCookieName() {
			ownerCookie = cookie
		}
		if cookie.Name == ownerTokenViewCookieName {
			tokenViewCookie = cookie
		}
	}
	if ownerCookie == nil || !validOwnerToken(ownerCookie.Value) {
		t.Fatal("web token issue did not set a valid owner cookie")
	}
	if tokenViewCookie == nil || tokenViewCookie.Value != "primary" || tokenViewCookie.Path != "/server/preferences/tokens" || !tokenViewCookie.HttpOnly || tokenViewCookie.MaxAge != 60 {
		t.Fatalf("web token issue did not set a scoped one-use token-view cookie: %+v", tokenViewCookie)
	}

	ownedPage := doRequestWithCookies(t, s, http.MethodGet, "/server/preferences/tokens", nil, csrfCookie, ownerCookie, tokenViewCookie)
	if ownedPage.Code != http.StatusOK || !strings.Contains(ownedPage.Body.String(), "Primary token") || !strings.Contains(ownedPage.Body.String(), ownerCookie.Value) || !strings.Contains(ownedPage.Body.String(), "Shown once") {
		t.Fatalf("owned token page did not show the one-use token: %d %s", ownedPage.Code, ownedPage.Body.String())
	}
	revealed := postForm("/server/preferences/token/reveal", url.Values{}, ownerCookie)
	if revealed.Code != http.StatusSeeOther || strings.Contains(revealed.Body.String(), ownerCookie.Value) {
		t.Fatalf("primary reveal = %d, cache=%q: %s", revealed.Code, revealed.Header().Get("Cache-Control"), revealed.Body.String())
	}
	for _, cookie := range revealed.Result().Cookies() {
		if cookie.Name == ownerTokenViewCookieName {
			tokenViewCookie = cookie
		}
	}
	revealedPage := doRequestWithCookies(t, s, http.MethodGet, "/server/preferences/tokens", nil, csrfCookie, ownerCookie, tokenViewCookie)
	if revealedPage.Code != http.StatusOK || !strings.Contains(revealedPage.Body.String(), ownerCookie.Value) || revealedPage.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("primary reveal page = %d, cache=%q: %s", revealedPage.Code, revealedPage.Header().Get("Cache-Control"), revealedPage.Body.String())
	}

	forgotten := postForm("/server/preferences/token/forget", url.Values{}, ownerCookie)
	if forgotten.Code != http.StatusSeeOther || forgotten.Header().Get("Clear-Site-Data") != `"storage"` {
		t.Fatalf("forget token = %d, clear-site-data=%q", forgotten.Code, forgotten.Header().Get("Clear-Site-Data"))
	}
	cleared := false
	for _, cookie := range forgotten.Result().Cookies() {
		if cookie.Name == s.ownerCookieName() && cookie.MaxAge == 0 && cookie.Value == "" {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("forget token did not clear the owner cookie")
	}
}

func TestOwnerTokenLast4Backfill(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	s := testServerWithConfig(t, cfg)
	ownerID, token, err := s.issuePrimary(context.Background())
	if err != nil {
		t.Fatalf("issue primary owner: %v", err)
	}
	if err := s.store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE api_tokens SET token_last4 = '' WHERE owner_id = ?`, ownerID)
		return err
	}); err != nil {
		t.Fatalf("clear token suffix for migration fixture: %v", err)
	}
	if err := s.MigrateOwnerTokenLast4(context.Background()); err != nil {
		t.Fatalf("MigrateOwnerTokenLast4() error: %v", err)
	}
	var last4 string
	if err := s.store.Read(context.Background(), func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT token_last4 FROM api_tokens WHERE owner_id = ?`, ownerID).Scan(&last4)
	}); err != nil {
		t.Fatalf("read migrated token suffix: %v", err)
	}
	if last4 != token[len(token)-4:] {
		t.Fatalf("migrated token suffix = %q, want final four characters", last4)
	}
}

func TestOwnerTokenPrimaryRevokeRequiresConfirmationAndDeletesOwner(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	s := testServerWithConfig(t, cfg)
	issued := doRequest(t, s, http.MethodPost, "/api/v1/server/preferences/tokens/primary", nil)
	if issued.Code != http.StatusCreated {
		t.Fatalf("issue primary = %d: %s", issued.Code, issued.Body.String())
	}
	var issueBody struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(issued.Body.Bytes(), &issueBody); err != nil {
		t.Fatalf("decode primary issue response: %v", err)
	}
	token := issueBody.Data["owner_token"]
	identity, ok := s.lookupOwnerToken(context.Background(), token, false)
	if !ok {
		t.Fatal("issued primary token did not resolve")
	}
	request := func(confirm string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"confirm": confirm})
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/server/preferences/tokens/primary", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		s.Router().ServeHTTP(response, req)
		return response
	}
	wrong := token[len(token)-4:]
	if wrong[0] == 'A' {
		wrong = "B" + wrong[1:]
	} else {
		wrong = "A" + wrong[1:]
	}
	if response := request(wrong); response.Code != http.StatusBadRequest {
		t.Fatalf("wrong primary revoke confirmation = %d, want 400", response.Code)
	}
	if _, ok := s.lookupOwnerToken(context.Background(), token, false); !ok {
		t.Fatal("invalid confirmation deleted the owner")
	}
	response := request(token[len(token)-4:])
	if response.Code != http.StatusOK {
		t.Fatalf("primary revoke = %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Data map[string]int64 `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode primary revoke response: %v", err)
	}
	if result.Data["tokens"] != 1 || result.Data["resources"] != 0 {
		t.Fatalf("primary revoke counts = %+v, want one token and no resources", result.Data)
	}
	if _, ok := s.lookupOwnerToken(context.Background(), token, false); ok {
		t.Fatal("revoked primary token still resolves")
	}
	var events int
	if err := s.store.Read(context.Background(), func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action = 'token.revoked' AND target_id = ?`, identity.ID).Scan(&events)
	}); err != nil {
		t.Fatalf("count owner revoke audit events: %v", err)
	}
	if events != 1 {
		t.Fatalf("owner revoke audit events = %d, want 1", events)
	}
}

func mustProjectSecret(t *testing.T, s *Server, name string) []byte {
	t.Helper()
	secret, err := s.projectSecret(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func doRequestWithCookies(t *testing.T, s *Server, method, path string, body []byte, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	request.Header.Set("Accept", "text/html")
	request.RemoteAddr = "192.0.2.15:1234"
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	s.Router().ServeHTTP(response, request)
	return response
}
