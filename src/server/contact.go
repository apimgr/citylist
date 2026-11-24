package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/smtp"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"github.com/go-chi/chi/v5"
)

const contactBodyLimit = 256 << 10

type contactSubmission map[string]string

type contactMailMessage struct {
	FromName       string
	From           string
	To             string
	ReplyTo        string
	Subject        string
	Text           string
	Attachment     []byte
	AttachmentName string
}

type contactCounter struct {
	Started time.Time
	Count   int
}

type contactStrike struct {
	Started time.Time
	Count   int
	Banned  time.Time
}

var contactMailSender = func(ctx context.Context, cfg config.SMTPConfig, message contactMailMessage, fqdn string) error {
	return sendContactSMTP(ctx, cfg, message, fqdn)
}

var contactState = struct {
	sync.Mutex
	used       map[string]time.Time
	counters   map[string]contactCounter
	strikes    map[string]contactStrike
	duplicates map[string]time.Time
}{used: make(map[string]time.Time), counters: make(map[string]contactCounter), strikes: make(map[string]contactStrike), duplicates: make(map[string]time.Time)}

var contactInstallationSecrets sync.Map

var (
	contactLinkPattern      = regexp.MustCompile(`(?i)(?:https?://|ftp://|www\.)[^\s<>\[\]{}"']+|\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}(?::[0-9]{1,5})?(?:/[^\s<>\[\]{}"']*)?`)
	contactMarkupPattern    = regexp.MustCompile(`(?is)<\s*/?\s*[a-z!][^>]*>|\[(?:/?(?:url|img|b|i|u|quote|code))(?:\s*=|\])`)
	contactZeroWidthPattern = regexp.MustCompile(`[\x{200B}-\x{200F}\x{202A}-\x{202E}\x{2060}\x{FEFF}]`)
)

// handleContactPost processes a browser form submission.
func (s *Server) handleContactPost(w http.ResponseWriter, r *http.Request) {
	s.handleContactSubmission(w, r, false)
}

// handleContactAPI returns the API form description or processes an API submission.
func (s *Server) handleContactAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		s.handleContactAPIGet(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This route accepts GET, HEAD, and POST requests")
		return
	}
	s.handleContactSubmission(w, r, true)
}

func (s *Server) contactEnabled(w http.ResponseWriter, r *http.Request) bool {
	if !s.cfg.Server.Pages.Contact.Enabled {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", i18n.Translate(i18n.LangFromRequest(r), "errors.not_found"))
		return false
	}
	return true
}

func (s *Server) installationSecret(ctx context.Context) ([]byte, error) {
	if s.store == nil {
		return nil, errors.New("application database is unavailable")
	}
	if value, ok := contactInstallationSecrets.Load(s.store); ok {
		return append([]byte(nil), value.([]byte)...), nil
	}
	if err := InitializeInstallationSecret(ctx, s.store); err != nil {
		return nil, err
	}
	var stored string
	if err := s.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name = 'installation_secret'`).Scan(&stored)
	}); err != nil {
		return nil, err
	}
	secret, err := base64.StdEncoding.DecodeString(stored)
	if err != nil || len(secret) != 32 {
		return nil, errors.New("stored installation secret is invalid")
	}
	contactInstallationSecrets.Store(s.store, append([]byte(nil), secret...))
	return secret, nil
}

func (s *Server) installationSecretCandidates(ctx context.Context) ([][]byte, error) {
	current, err := s.installationSecret(ctx)
	if err != nil {
		return nil, err
	}
	candidates := [][]byte{current}
	if s.store == nil {
		return candidates, nil
	}
	var previous, until string
	err = s.store.Read(ctx, func(ctx context.Context, r database.Reader) error {
		if e := r.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name='installation_secret_previous'`).Scan(&previous); e != nil {
			return e
		}
		return r.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name='installation_secret_previous_until'`).Scan(&until)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return candidates, nil
	}
	if err != nil {
		return nil, err
	}
	expiration, err := time.Parse(time.RFC3339, until)
	if err != nil {
		return nil, errors.New("previous installation secret expiry is invalid")
	}
	if time.Now().UTC().After(expiration) {
		return candidates, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(previous)
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("previous installation secret is invalid")
	}
	return append(candidates, decoded), nil
}

// InitializeInstallationSecret creates the required installation secret after
// schema initialization and before the server starts accepting requests.
func InitializeInstallationSecret(ctx context.Context, store *database.Store) error {
	if store == nil {
		return errors.New("application database is unavailable")
	}
	secretNames := []string{"installation_secret", "cookie_signing_key", "csrf_token_secret"}
	return store.Write(ctx, func(tx *sql.Tx) error {
		for _, name := range secretNames {
			generated := make([]byte, 32)
			if _, err := rand.Read(generated); err != nil {
				return err
			}
			encoded := base64.StdEncoding.EncodeToString(generated)
			result, err := tx.ExecContext(ctx, `INSERT INTO app_secrets (name, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(name) DO NOTHING`, name, encoded, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				return err
			}
			inserted, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if inserted == 1 {
				details, err := json.Marshal(map[string]string{"key": "server.security." + name})
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (level, category, action, details, success) VALUES ('info', 'config', 'config.secret_generated', ?, 1)`, string(details)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Server) newContactFormToken(ctx context.Context) (string, error) {
	secret, err := s.installationSecret(ctx)
	if err != nil {
		return "", err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	issued := strconv.FormatInt(time.Now().Unix(), 10)
	nonceText := hex.EncodeToString(nonce[:])
	message := "contact-form|" + issued + "|" + nonceText
	return issued + "." + nonceText + "." + contactHMAC(secret, message), nil
}

func contactHMAC(secret []byte, value string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) securityID(ctx context.Context, window int64) (string, error) {
	secret, err := s.installationSecret(ctx)
	if err != nil {
		return "", err
	}
	value := contactHMAC(secret, strconv.FormatInt(window, 10))
	return value[:16], nil
}

func (s *Server) validSecurityID(ctx context.Context, supplied string) bool {
	_, valid := s.validSecurityIDWindow(ctx, supplied)
	return valid
}

func (s *Server) validSecurityIDWindow(ctx context.Context, supplied string) (int64, bool) {
	if len(supplied) != 16 {
		return 0, false
	}
	if _, err := hex.DecodeString(supplied); err != nil {
		return 0, false
	}
	secrets, err := s.installationSecretCandidates(ctx)
	if err != nil {
		return 0, false
	}
	window := time.Now().Unix() / 172800
	for _, candidateWindow := range []int64{window, window - 1} {
		message := strconv.FormatInt(candidateWindow, 10)
		for _, secret := range secrets {
			candidate := contactHMAC(secret, message)[:16]
			if subtle.ConstantTimeCompare([]byte(candidate), []byte(strings.ToLower(supplied))) == 1 {
				return candidateWindow, true
			}
		}
	}
	return 0, false
}

func (s *Server) newSimpleCaptcha(ctx context.Context, lang string) (map[string]string, error) {
	if s.store == nil {
		return nil, errors.New("captcha storage is unavailable")
	}
	var values [3]byte
	if _, err := rand.Read(values[:]); err != nil {
		return nil, err
	}
	a, b := int(values[0]%20)+1, int(values[1]%20)+1
	operator := "+"
	answer := a + b
	if values[2]&1 == 1 {
		operator = "-"
		if b > a {
			a, b = b, a
		}
		answer = a - b
	}
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(idBytes[:])
	answerHash := sha256.Sum256([]byte(strconv.Itoa(answer)))
	expires := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339Nano)
	err := s.store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM captcha_challenges WHERE expires_at <= ?`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO captcha_challenges (id, answer_hash, expires_at) VALUES (?, ?, ?)`, id, hex.EncodeToString(answerHash[:]), expires)
		return err
	})
	if err != nil {
		return nil, err
	}
	key := "contact.captcha_add"
	if operator == "-" {
		key = "contact.captcha_subtract"
	}
	question := i18n.TranslateFormat(lang, key, map[string]string{"left": strconv.Itoa(a), "right": strconv.Itoa(b)})
	return map[string]string{"provider": "simple", "captcha_id": id, "question": question}, nil
}

func (s *Server) contactCaptcha(ctx context.Context, r *http.Request) (map[string]any, error) {
	provider := s.cfg.Server.Pages.Contact.Captcha
	if contactRequestOnPrivateNetwork(r) {
		provider = "simple"
	}
	lang := i18n.LangFromRequest(r)
	challenge, err := s.newSimpleCaptcha(ctx, lang)
	if err != nil {
		return nil, err
	}
	if provider == "simple" {
		return map[string]any{"provider": "simple", "captcha_id": challenge["captcha_id"], "question": challenge["question"]}, nil
	}
	return map[string]any{
		"provider": provider,
		"site_key": s.cfg.Server.Pages.Contact.CaptchaSiteKey,
		"fallback": challenge,
	}, nil
}

func (s *Server) verifySimpleCaptcha(ctx context.Context, id, answer string) bool {
	if s.store == nil || id == "" || len(answer) > 16 {
		return false
	}
	var expected string
	var expires string
	err := s.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT answer_hash, expires_at FROM captcha_challenges WHERE id = ?`, id).Scan(&expected, &expires)
	})
	if err != nil {
		return false
	}
	got := sha256.Sum256([]byte(strings.TrimSpace(answer)))
	expiresAt, parseErr := time.Parse(time.RFC3339Nano, expires)
	valid := parseErr == nil && expiresAt.After(time.Now()) && subtle.ConstantTimeCompare([]byte(expected), []byte(hex.EncodeToString(got[:]))) == 1
	_ = s.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM captcha_challenges WHERE id = ?`, id)
		return err
	})
	return valid
}

func (s *Server) verifyProviderCaptcha(ctx context.Context, r *http.Request, response string) (bool, error) {
	if strings.TrimSpace(response) == "" {
		return false, nil
	}
	provider := s.cfg.Server.Pages.Contact.Captcha
	endpoint := "https://www.google.com/recaptcha/api/siteverify"
	if provider == "hcaptcha" {
		endpoint = "https://api.hcaptcha.com/siteverify"
	}
	form := url.Values{"secret": {s.cfg.Server.Pages.Contact.CaptchaSecretKey}, "response": {response}}
	if ip := contactClientIP(r); ip != "" {
		form.Set("remoteip", ip)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 5 * time.Second}
	result, err := client.Do(request)
	if err != nil {
		return false, err
	}
	defer func() { _ = result.Body.Close() }()
	var verification struct {
		Success bool `json:"success"`
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return false, errors.New("captcha provider returned an error")
	}
	if err := json.NewDecoder(io.LimitReader(result.Body, 4096)).Decode(&verification); err != nil {
		return false, err
	}
	return verification.Success, nil
}

func contactRequestOnPrivateNetwork(r *http.Request) bool {
	host := r.Host
	if parsed, err := url.Parse("//" + host); err == nil {
		host = parsed.Hostname()
	}
	return strings.HasSuffix(strings.ToLower(host), ".onion") || strings.HasSuffix(strings.ToLower(host), ".i2p")
}

func (s *Server) contactTypes(security bool, lang string) []contactTypeChoice {
	types := []contactTypeChoice{
		{Type: "abuse", Title: i18n.Translate(lang, "contact.type_abuse")},
		{Type: "dmca", Title: i18n.Translate(lang, "contact.type_dmca")},
		{Type: "privacy", Title: i18n.Translate(lang, "contact.type_privacy")},
	}
	if security {
		types = append(types, contactTypeChoice{Type: "security", Title: i18n.Translate(lang, "contact.type_security")})
	}
	return types
}

func (s *Server) contactFields(kind, lang string, values contactSubmission, securityID string) []contactField {
	field := func(name, typ string, required bool, max int, opts ...contactOption) contactField {
		value := values[name]
		return contactField{Name: name, Type: typ, Value: value, Required: required, MaxLength: max, Label: i18n.Translate(lang, "contact.field_"+name), Options: opts}
	}
	option := func(value string) contactOption {
		return contactOption{Value: value, Label: i18n.Translate(lang, "contact.option_"+value)}
	}
	fields := []contactField{field("name", "text", true, 100), field("email", "email", true, 254)}
	switch kind {
	case "abuse":
		fields = append(fields,
			field("reported_url", "url", true, 2048),
			field("reason", "select", true, 0, option("illegal_content"), option("terms_violation"), option("harassment"), option("malware_phishing"), option("other")),
			field("subject", "text", true, 200), field("message", "textarea", true, 5000))
	case "dmca":
		fields = append(fields,
			field("reported_url", "url", true, 2048), field("copyrighted_work", "textarea", true, 2000),
			field("good_faith", "checkbox", true, 0), field("accuracy", "checkbox", true, 0),
			field("signature", "text", true, 100), field("subject", "text", true, 200), field("message", "textarea", true, 5000))
	case "privacy":
		fields = append(fields, field("request_kind", "select", true, 0, option("access"), option("export"), option("deletion"), option("correction"), option("opt_out"), option("other")),
			field("subject", "text", true, 200), field("message", "textarea", true, 5000))
	case "security":
		fields = append(fields,
			field("researcher_gpg", "textarea", false, 16384), field("component", "text", true, 255),
			field("endpoint", "text", false, 2048), field("severity", "select", true, 0, option("critical"), option("high"), option("medium"), option("low"), option("informational")),
			field("summary", "text", true, 200), field("steps", "textarea", true, 10000), field("impact", "textarea", true, 5000),
			field("suggested_fix", "textarea", false, 5000), field("cve_requested", "checkbox", false, 0),
			field("disclosure_days", "number", false, 0), field("credit_preference", "select", true, 0, option("real_name"), option("handle"), option("no"), option("anonymous")),
			field("credit_handle", "text", false, 100), field("coordinated_disclosure", "checkbox", true, 0),
			field("security_id", "hidden", true, 16))
		fields[len(fields)-1].Value = securityID
	}
	return fields
}

func (s *Server) handleContactAPIGet(w http.ResponseWriter, r *http.Request) {
	if !s.contactEnabled(w, r) {
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	lang := i18n.LangFromRequest(r)
	securityID := strings.TrimSpace(r.URL.Query().Get("security_id"))
	_, security := s.validSecurityIDWindow(r.Context(), securityID)
	if securityID != "" && !security {
		s.logInvalidSecurityID(r, securityID)
	}
	kind := strings.TrimSpace(r.URL.Query().Get("type"))
	if security {
		kind = "security"
	}
	fields := []contactField(nil)
	if contactTypeOffered(kind, security) {
		fields = s.contactFields(kind, lang, nil, securityID)
	}
	formToken, err := s.newContactFormToken(r.Context())
	if err != nil {
		s.respondError(w, http.StatusServiceUnavailable, "CONTACT_UNAVAILABLE", "Contact service is temporarily unavailable")
		return
	}
	captcha, err := s.contactCaptcha(r.Context(), r)
	if err != nil {
		s.respondError(w, http.StatusServiceUnavailable, "CONTACT_UNAVAILABLE", "Contact service is temporarily unavailable")
		return
	}
	data := map[string]any{
		"fields": contactFieldDescriptors(fields), "captcha": captcha, "form_token": formToken,
		"types": contactTypeValues(s.contactTypes(security, lang)), "security_url": s.BuildURL(r, "/server/security"),
		"abuse_email": s.publicAbuseEmail(r),
	}
	s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: data})
}

func contactTypeOffered(kind string, security bool) bool {
	switch kind {
	case "abuse", "dmca", "privacy":
		return true
	case "security":
		return security
	default:
		return false
	}
}

func contactTypeValues(choices []contactTypeChoice) []string {
	values := make([]string, 0, len(choices))
	for _, choice := range choices {
		values = append(values, choice.Type)
	}
	return values
}

func contactFieldDescriptors(fields []contactField) []map[string]any {
	result := make([]map[string]any, 0, len(fields))
	for _, field := range fields {
		var maxLength any
		if field.MaxLength > 0 {
			maxLength = field.MaxLength
		}
		var requiredWhen any
		if field.Name == "reported_url" {
			requiredWhen = map[string]any{"field": "type", "in": []string{"abuse", "dmca"}}
		}
		if field.Name == "credit_handle" {
			requiredWhen = map[string]any{"field": "credit_preference", "equals": "handle"}
		}
		var maxLengthWhen any
		if field.Name == "researcher_gpg" {
			maxLengthWhen = []map[string]any{{"prefix": "https://", "max_length": 2048}}
		}
		item := map[string]any{"name": field.Name, "type": field.Type, "required": field.Required, "max_length": maxLength, "required_when": requiredWhen, "max_length_when": maxLengthWhen}
		if len(field.Options) > 0 {
			options := make([]string, 0, len(field.Options))
			for _, option := range field.Options {
				options = append(options, option.Value)
			}
			item["options"] = options
		}
		result = append(result, item)
	}
	return result
}

func (s *Server) publicAbuseEmail(r *http.Request) string {
	email := strings.TrimSpace(s.cfg.Server.Contact.Abuse.Email)
	if email == "" {
		email = strings.TrimSpace(s.cfg.Server.Contact.General.Email)
	}
	return s.resolveContactEmail(r, email)
}

func (s *Server) resolveContactEmail(r *http.Request, email string) string {
	if !strings.Contains(email, "{fqdn}") {
		return strings.TrimSpace(email)
	}
	host := strings.TrimSpace(s.cfg.Server.FQDN)
	if host == "" {
		if parsed, err := url.Parse(s.BuildURL(r, "/")); err == nil {
			host = parsed.Hostname()
		}
	}
	return strings.TrimSpace(strings.ReplaceAll(email, "{fqdn}", host))
}

func (s *Server) logInvalidSecurityID(r *http.Request, supplied string) {
	hash := sha256.Sum256([]byte(supplied))
	slog.Warn("security.security_id_invalid", "ip", contactClientIP(r), "user_agent", r.UserAgent(), "id_hash", hex.EncodeToString(hash[:])[:8])
}

func (s *Server) handleContactSubmission(w http.ResponseWriter, r *http.Request, api bool) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Cache-Control", "no-store")
	if !s.contactEnabled(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This route accepts POST requests")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, contactBodyLimit)
	if !api && !s.contactOriginOK(r) {
		s.contactStrike(r, "origin")
		s.contactBlocked(r, "origin", false)
		s.contactFormFailure(w, r, api, nil, "origin", http.StatusBadRequest)
		return
	}
	values, err := parseContactSubmission(r, api)
	if err != nil {
		s.contactFormFailure(w, r, api, nil, "shape", http.StatusBadRequest)
		return
	}
	step := values["step"]
	if step == "" {
		step = "submit"
	}
	securityID := values["security_id"]
	securityWindow, security := s.validSecurityIDWindow(r.Context(), securityID)
	kind := values["type"]
	if security {
		kind = "security"
	}
	if !contactTypeOffered(kind, security) {
		s.contactFormFailure(w, r, api, values, "type", http.StatusBadRequest)
		return
	}
	if step == "edit" {
		s.renderContactForm(w, r, kind, values, securityID, "")
		return
	}
	if step != "submit" && step != "confirm" {
		s.contactFormFailure(w, r, api, values, "step", http.StatusBadRequest)
		return
	}
	fieldSet := contactFieldNameSet(kind)
	if err := validateContactShape(values, fieldSet, api); err != nil {
		s.contactStrike(r, "shape")
		s.contactFormFailure(w, r, api, values, err.Error(), http.StatusBadRequest)
		return
	}
	if step == "submit" {
		if kind == "security" && strings.TrimSpace(values["researcher_gpg"]) != "" {
			if err := ValidateResearcherPGPKey(r.Context(), values["researcher_gpg"]); err != nil {
				s.contactFormFailure(w, r, api, values, "researcher_gpg", http.StatusBadRequest)
				return
			}
		}
		if allowed, reason := s.contactRateAllowed(r, values["email"], false); !allowed {
			s.contactBlocked(r, reason, false)
			s.contactFormFailure(w, r, api, values, "rate", http.StatusTooManyRequests)
			return
		}
		validToken, nonce, expires := s.verifyContactFormToken(r.Context(), values["form_token"])
		if !validToken {
			s.contactStrike(r, "fill_time")
			s.contactBlocked(r, "fill_time", false)
			s.contactFormFailure(w, r, api, values, "form_token", http.StatusBadRequest)
			return
		}
		if !s.consumeContactNonce("form:"+nonce, expires) {
			s.contactStrike(r, "replay")
			s.contactBlocked(r, "replay", false)
			s.contactFormFailure(w, r, api, values, "form_token", http.StatusBadRequest)
			return
		}
		honeypot := strings.TrimSpace(values["website"]) != ""
		captchaValid, captchaErr := s.verifyContactCaptcha(r.Context(), r, values)
		if captchaErr != nil {
			s.contactStrike(r, "captcha")
			s.contactBlocked(r, "captcha", false)
			s.contactFormFailure(w, r, api, values, "captcha", http.StatusServiceUnavailable)
			return
		}
		if !captchaValid {
			s.contactStrike(r, "captcha")
			s.contactBlocked(r, "captcha", false)
			s.contactFormFailure(w, r, api, values, "captcha", http.StatusBadRequest)
			return
		}
		if kind != "security" {
			if reason := s.validateContactContent(r, kind, values); reason != "" {
				s.contactStrike(r, reason)
				s.contactBlocked(r, reason, false)
				s.contactFormFailure(w, r, api, values, "content", http.StatusBadRequest)
				return
			}
		}
		reviewToken, err := s.newContactReviewToken(r.Context(), values, kind, securityID, honeypot)
		if err != nil {
			s.respondError(w, http.StatusServiceUnavailable, "CONTACT_UNAVAILABLE", "Contact service is temporarily unavailable")
			return
		}
		formToken, _ := s.newContactFormToken(r.Context())
		if api {
			s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]any{"review": map[string]string{"type": kind, "email": values["email"], "subject": contactSubject(kind, values)}, "review_token": reviewToken, "form_token": formToken, "confirm_after_seconds": s.cfg.Server.Pages.Contact.Antispam.ReviewMinSeconds}})
			return
		}
		view := s.contactPageView(r, kind, values, securityID)
		view.FormToken, view.ReviewToken, view.Review = formToken, reviewToken, true
		s.renderContactPage(w, r, view)
		return
	}
	s.handleContactConfirm(w, r, api, values, kind, securityID, securityWindow)
}

func parseContactSubmission(r *http.Request, api bool) (contactSubmission, error) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	values := contactSubmission{}
	if mediaType == "application/json" {
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			return nil, err
		}
		if len(raw) > 64 {
			return nil, errors.New("too many fields")
		}
		for name, value := range raw {
			var text string
			if err := json.Unmarshal(value, &text); err == nil {
				values[name] = text
				continue
			}
			var boolean bool
			if err := json.Unmarshal(value, &boolean); err == nil {
				if boolean {
					values[name] = "true"
				}
				continue
			}
			return nil, fmt.Errorf("invalid field %s", name)
		}
		return values, nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	for name, items := range r.PostForm {
		if len(items) != 1 {
			return nil, errors.New("duplicate field")
		}
		values[name] = items[0]
	}
	if !api && values["confirmed"] == "on" {
		values["confirmed"] = "true"
	}
	if !api && values["good_faith"] == "on" {
		values["good_faith"] = "true"
	}
	if !api && values["accuracy"] == "on" {
		values["accuracy"] = "true"
	}
	if !api && values["cve_requested"] == "on" {
		values["cve_requested"] = "true"
	}
	if !api && values["coordinated_disclosure"] == "on" {
		values["coordinated_disclosure"] = "true"
	}
	return values, nil
}

func contactFieldNameSet(kind string) map[string]bool {
	result := map[string]bool{"name": true, "email": true, "type": true, "form_token": true, "step": true, "review_token": true, "confirmed": true, "csrf_token": true, "captcha_id": true, "captcha_answer": true, "captcha_response": true, "g-recaptcha-response": true, "h-captcha-response": true, "security_id": true}
	switch kind {
	case "abuse":
		for _, key := range []string{"reported_url", "reason", "subject", "message"} {
			result[key] = true
		}
	case "dmca":
		for _, key := range []string{"reported_url", "copyrighted_work", "good_faith", "accuracy", "signature", "subject", "message"} {
			result[key] = true
		}
	case "privacy":
		for _, key := range []string{"request_kind", "subject", "message"} {
			result[key] = true
		}
	case "security":
		for _, key := range []string{"researcher_gpg", "component", "endpoint", "severity", "summary", "steps", "impact", "suggested_fix", "cve_requested", "disclosure_days", "credit_preference", "credit_handle", "coordinated_disclosure"} {
			result[key] = true
		}
	}
	result["website"] = true
	return result
}

func validateContactShape(values contactSubmission, allowed map[string]bool, api bool) error {
	if api {
		delete(allowed, "website")
	}
	for key := range values {
		if !allowed[key] {
			return errors.New("unknown field")
		}
	}
	for key, value := range values {
		if !utf8.ValidString(value) || contactZeroWidthPattern.MatchString(value) {
			return errors.New("invalid text")
		}
		if (key == "name" || key == "email" || key == "subject") && strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("invalid header text")
		}
	}
	for _, name := range []string{"name", "email", "form_token"} {
		if strings.TrimSpace(values[name]) == "" {
			return fmt.Errorf("%s", name)
		}
	}
	if strings.TrimSpace(values["type"]) == "" {
		return errors.New("type")
	}
	if len(values["website"]) > 200 {
		return errors.New("website")
	}
	if strings.ContainsAny(values["email"], ",;<>\r\n") {
		return errors.New("email")
	}
	address, err := mail.ParseAddress(strings.TrimSpace(values["email"]))
	if err != nil || address.Address != strings.TrimSpace(values["email"]) {
		return errors.New("email")
	}
	limits := map[string]int{"name": 100, "email": 254, "subject": 200, "message": 5000, "reported_url": 2048, "copyrighted_work": 2000, "signature": 100, "researcher_gpg": 16384, "component": 255, "endpoint": 2048, "summary": 200, "steps": 10000, "impact": 5000, "suggested_fix": 5000, "credit_handle": 100, "security_id": 16}
	for name, limit := range limits {
		if value := strings.TrimSpace(values[name]); value != "" && len([]rune(value)) > limit {
			return fmt.Errorf("%s", name)
		}
	}
	conditionalRequired := map[string]bool{
		"reported_url": values["type"] == "abuse" || values["type"] == "dmca",
		"reason":       values["type"] == "abuse", "copyrighted_work": values["type"] == "dmca",
		"signature": values["type"] == "dmca", "subject": values["type"] != "security",
		"message": values["type"] != "security", "request_kind": values["type"] == "privacy",
		"component": values["type"] == "security", "severity": values["type"] == "security",
		"summary": values["type"] == "security", "steps": values["type"] == "security",
		"impact": values["type"] == "security", "credit_preference": values["type"] == "security",
		"coordinated_disclosure": values["type"] == "security",
	}
	for name, required := range conditionalRequired {
		if required && strings.TrimSpace(values[name]) == "" {
			return fmt.Errorf("%s", name)
		}
	}
	if values["type"] == "dmca" && (values["good_faith"] != "true" || values["accuracy"] != "true") {
		return errors.New("good_faith")
	}
	if values["credit_preference"] == "handle" && strings.TrimSpace(values["credit_handle"]) == "" {
		return errors.New("credit_handle")
	}
	if values["type"] == "security" {
		if !oneOf(values["severity"], "critical", "high", "medium", "low", "informational") {
			return errors.New("severity")
		}
		if !oneOf(values["credit_preference"], "real_name", "handle", "no", "anonymous") {
			return errors.New("credit_preference")
		}
		if value := strings.TrimSpace(values["disclosure_days"]); value != "" {
			if days, err := strconv.Atoi(value); err != nil || days < 1 || days > 3650 {
				return errors.New("disclosure_days")
			}
		}
		if gpg := strings.TrimSpace(values["researcher_gpg"]); strings.HasPrefix(strings.ToLower(gpg), "https://") {
			if len([]rune(gpg)) > 2048 {
				return errors.New("researcher_gpg")
			}
			parsed, err := url.Parse(gpg)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				return errors.New("researcher_gpg")
			}
		}
	}
	return nil
}

func (s *Server) contactOriginOK(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	referer := strings.TrimSpace(r.Header.Get("Referer"))
	fetchSite := strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))
	if origin == "" && referer == "" && fetchSite == "" {
		return false
	}
	if fetchSite != "" && !strings.EqualFold(fetchSite, "same-origin") {
		return false
	}
	provided := origin
	if provided == "" {
		provided = referer
	}
	if provided == "" {
		return true
	}
	parsed, err := url.Parse(provided)
	if err != nil || parsed.Host == "" {
		return false
	}
	providedOrigin := strings.ToLower(parsed.Scheme + "://" + parsed.Host)
	wanted, err := url.Parse(s.BuildURL(r, "/"))
	if err != nil || wanted.Host == "" {
		return false
	}
	wantedOrigin := strings.ToLower(wanted.Scheme + "://" + wanted.Host)
	return providedOrigin == wantedOrigin
}

func (s *Server) verifyContactFormToken(ctx context.Context, token string) (bool, string, time.Time) {
	secrets, err := s.installationSecretCandidates(ctx)
	if err != nil {
		return false, "", time.Time{}
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(parts[1]) != 32 || len(parts[2]) != 64 {
		return false, "", time.Time{}
	}
	issued, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false, "", time.Time{}
	}
	validMAC := 0
	for _, secret := range secrets {
		want := contactHMAC(secret, "contact-form|"+parts[0]+"|"+parts[1])
		validMAC |= subtle.ConstantTimeCompare([]byte(want), []byte(parts[2]))
	}
	if validMAC != 1 {
		return false, "", time.Time{}
	}
	age := time.Now().Unix() - issued
	anti := s.cfg.Server.Pages.Contact.Antispam
	if age < int64(anti.MinFillSeconds) || age > int64(anti.MaxAgeSeconds) {
		return false, parts[1], time.Unix(issued+int64(anti.MaxAgeSeconds), 0)
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return false, "", time.Time{}
	}
	return true, parts[1], time.Unix(issued+int64(anti.MaxAgeSeconds), 0)
}

func (s *Server) consumeContactNonce(key string, expires time.Time) bool {
	now := time.Now()
	contactState.Lock()
	defer contactState.Unlock()
	for nonce, expiry := range contactState.used {
		if !expiry.After(now) {
			delete(contactState.used, nonce)
		}
	}
	if _, exists := contactState.used[key]; exists {
		return false
	}
	if len(contactState.used) >= 50000 {
		return false
	}
	contactState.used[key] = expires
	return true
}

func (s *Server) newContactReviewToken(ctx context.Context, values contactSubmission, kind, securityID string, drop bool) (string, error) {
	secret, err := s.installationSecret(ctx)
	if err != nil {
		return "", err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	issued := strconv.FormatInt(time.Now().Unix(), 10)
	nonceText := hex.EncodeToString(nonce[:])
	flags := "-"
	if drop {
		flags = "d"
	}
	hash := contactFieldHash(values, kind, securityID)
	message := "contact-review|" + issued + "|" + nonceText + "|" + flags + "|" + hash
	return issued + "." + nonceText + "." + flags + "." + contactHMAC(secret, message), nil
}

func contactFieldHash(values contactSubmission, kind, securityID string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key == "csrf_token" || key == "form_token" || key == "review_token" || key == "step" || key == "confirmed" || key == "captcha_id" || key == "captcha_answer" || key == "captcha_response" || key == "g-recaptcha-response" || key == "h-captcha-response" || key == "website" || key == "type" || key == "security_id" {
			continue
		}
		keys = append(keys, key)
	}
	keys = append(keys, "type", "security_id")
	sort.Strings(keys)
	var serialized strings.Builder
	serialized.WriteString("type=" + kind + "\nsecurity_id=" + securityID + "\n")
	for _, key := range keys {
		if key != "type" && key != "security_id" {
			serialized.WriteString(key + "=" + strings.TrimSpace(values[key]) + "\n")
		}
	}
	hash := sha256.Sum256([]byte(serialized.String()))
	return hex.EncodeToString(hash[:])
}

func (s *Server) handleContactConfirm(w http.ResponseWriter, r *http.Request, api bool, values contactSubmission, kind, securityID string, securityWindow int64) {
	secrets, err := s.installationSecretCandidates(r.Context())
	if err != nil {
		s.respondError(w, http.StatusServiceUnavailable, "CONTACT_UNAVAILABLE", "Contact service is temporarily unavailable")
		return
	}
	parts := strings.Split(values["review_token"], ".")
	valid := false
	nonce, flags := "", ""
	var expires time.Time
	if len(parts) == 4 && len(parts[1]) == 32 && len(parts[3]) == 64 {
		issued, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr == nil {
			nonce, flags = parts[1], parts[2]
			age := time.Now().Unix() - issued
			anti := s.cfg.Server.Pages.Contact.Antispam
			expires = time.Unix(issued+int64(anti.ReviewMaxSeconds), 0)
			message := "contact-review|" + parts[0] + "|" + nonce + "|" + flags + "|" + contactFieldHash(values, kind, securityID)
			validMAC := 0
			for _, secret := range secrets {
				want := contactHMAC(secret, message)
				validMAC |= subtle.ConstantTimeCompare([]byte(want), []byte(parts[3]))
			}
			valid = (flags == "-" || flags == "d") && age >= int64(anti.ReviewMinSeconds) && age <= int64(anti.ReviewMaxSeconds) && validMAC == 1
		}
	}
	if !valid || values["confirmed"] != "true" || !s.consumeContactNonce("review:"+nonce, expires) {
		s.contactStrike(r, "review")
		s.contactBlocked(r, "review", false)
		s.renderContactReviewFailure(w, r, api, values, kind, securityID)
		return
	}
	if allowed, reason := s.contactRateAllowed(r, values["email"], true); !allowed {
		s.contactBlocked(r, reason, false)
		s.contactFormFailure(w, r, api, values, "rate", http.StatusTooManyRequests)
		return
	}
	if kind != "security" && flags != "d" && s.isDuplicateContact(values, kind) {
		s.contactBlocked(r, "duplicate", true)
		s.contactSuccess(w, r, api, "", false)
		return
	}
	if flags == "d" {
		s.contactBlocked(r, "honeypot", true)
		s.contactSuccess(w, r, api, "", false)
		return
	}
	if kind == "security" {
		trackingID, statusToken, encryptedBody, bodyAlgorithm, err := s.storeSecurityReport(r.Context(), values, securityWindow, r.UserAgent(), i18n.LangFromRequest(r))
		if err != nil {
			s.respondError(w, http.StatusServiceUnavailable, "SECURITY_REPORT_UNAVAILABLE", "Security reports cannot be accepted right now")
			return
		}
		acknowledgmentSent := false
		if strings.TrimSpace(s.cfg.Server.Notifications.Email.SMTP.Host) != "" {
			if err := s.deliverSecurityNotification(r.Context(), r, trackingID, encryptedBody, bodyAlgorithm); err != nil {
				slog.Error("security.report_notification_failed", "tracking_id", trackingID, "error", err)
			}
			if err := s.sendResearcherAcknowledgment(r.Context(), r, values["email"], trackingID, statusToken, values["researcher_gpg"]); err != nil {
				slog.Warn("security.report_acknowledgment_failed", "tracking_id", trackingID, "error", err)
			} else {
				acknowledgmentSent = true
			}
		}
		slog.Info("security.report_received", "tracking_id", trackingID, "severity", values["severity"], "component", sanitizeLogValue(values["component"]))
		s.contactSuccess(w, r, api, trackingID, acknowledgmentSent)
		return
	}
	if err := s.deliverContact(r.Context(), r, kind, values, ""); err != nil {
		s.respondError(w, http.StatusServiceUnavailable, "CONTACT_DELIVERY_FAILED", "The report could not be delivered right now")
		return
	}
	s.markContactDuplicate(values, kind)
	s.contactSuccess(w, r, api, "", false)
}

func (s *Server) renderContactReviewFailure(w http.ResponseWriter, r *http.Request, api bool, values contactSubmission, kind, securityID string) {
	token, _ := s.newContactReviewToken(r.Context(), values, kind, securityID, false)
	formToken, _ := s.newContactFormToken(r.Context())
	if api {
		s.respondJSON(w, http.StatusBadRequest, APIResponse{Success: false, Error: "VALIDATION_FAILED", Message: i18n.Translate(i18n.LangFromRequest(r), "contact.errors.review_confirmation"), Details: map[string]any{"field": "review_token", "review_token": token, "form_token": formToken}})
		return
	}
	view := s.contactPageView(r, kind, values, securityID)
	view.Review, view.ReviewToken, view.FormToken = true, token, formToken
	view.FormError = i18n.Translate(i18n.LangFromRequest(r), "contact.errors.review_confirmation")
	s.renderContactPage(w, r, view)
}

func (s *Server) contactPageView(r *http.Request, kind string, values contactSubmission, securityID string) contactPageView {
	lang := i18n.LangFromRequest(r)
	return contactPageView{Type: kind, TypeTitle: i18n.Translate(lang, "contact.type_"+kind), Fields: s.contactFields(kind, lang, values, securityID),
		SubmittedFields: s.contactFields(kind, lang, values, securityID), SecurityMode: kind == "security", SecurityURL: s.BuildURL(r, "/server/security"), AbuseEmail: s.publicAbuseEmail(r), FQDN: requestFQDN(r)}
}

func (s *Server) renderContactForm(w http.ResponseWriter, r *http.Request, kind string, values contactSubmission, securityID, message string) {
	view := s.contactPageView(r, kind, values, securityID)
	token, err := s.newContactFormToken(r.Context())
	if err != nil {
		s.respondError(w, http.StatusServiceUnavailable, "CONTACT_UNAVAILABLE", "Contact service is temporarily unavailable")
		return
	}
	view.FormToken, view.FormError = token, message
	captcha, err := s.contactCaptcha(r.Context(), r)
	if err != nil {
		s.respondError(w, http.StatusServiceUnavailable, "CONTACT_UNAVAILABLE", "Contact service is temporarily unavailable")
		return
	}
	view.CaptchaProvider = fmt.Sprint(captcha["provider"])
	view.CaptchaID, _ = captcha["captcha_id"].(string)
	view.CaptchaQuestion, _ = captcha["question"].(string)
	view.CaptchaSiteKey, _ = captcha["site_key"].(string)
	if fallback, ok := captcha["fallback"].(map[string]string); ok {
		view.CaptchaFallbackID = fallback["captcha_id"]
		view.CaptchaFallbackQuestion = fallback["question"]
	}
	s.renderContactPage(w, r, view)
}

func (s *Server) contactFormFailure(w http.ResponseWriter, r *http.Request, api bool, values contactSubmission, field string, status int) {
	lang := i18n.LangFromRequest(r)
	key := "contact.errors.required_fields"
	switch field {
	case "origin":
		key = "contact.errors.origin"
	case "shape", "step":
		key = "contact.errors.invalid_form"
	case "type":
		key = "contact.errors.invalid_type"
	case "researcher_gpg":
		key = "contact.errors.invalid_researcher_key"
	case "rate":
		key = "contact.errors.rate_limited"
	case "form_token":
		key = "contact.errors.wait_before_submit"
	case "captcha":
		key = "contact.errors.captcha_required"
		if status == http.StatusServiceUnavailable {
			key = "contact.errors.captcha_unavailable"
		}
	case "content":
		key = "contact.errors.content_rejected"
	}
	message := i18n.Translate(lang, key)
	if api {
		captcha, _ := s.contactCaptcha(r.Context(), r)
		details := map[string]any{"field": field}
		if captcha != nil {
			details["captcha"] = captcha
		}
		if token, err := s.newContactFormToken(r.Context()); err == nil {
			details["form_token"] = token
		}
		s.respondJSON(w, status, APIResponse{Success: false, Error: "VALIDATION_FAILED", Message: message, Details: details})
		return
	}
	w.WriteHeader(status)
	kind := values["type"]
	securityID := values["security_id"]
	if securityID != "" && s.validSecurityID(r.Context(), securityID) {
		kind = "security"
	}
	if !contactTypeOffered(kind, kind == "security") {
		view := contactPageView{Types: s.contactTypes(false, i18n.LangFromRequest(r)), SecurityURL: s.BuildURL(r, "/server/security"), AbuseEmail: s.publicAbuseEmail(r), FormError: message}
		s.renderContactPage(w, r, view)
		return
	}
	s.renderContactForm(w, r, kind, values, securityID, message)
}

func (s *Server) contactSuccess(w http.ResponseWriter, r *http.Request, api bool, trackingID string, acknowledgmentSent bool) {
	message := s.cfg.Server.Pages.Contact.SuccessMessage
	lang := i18n.LangFromRequest(r)
	if strings.TrimSpace(message) == "" || message == "contact.success_message" || message == "Thank you for your report." {
		message = i18n.Translate(lang, "contact.success_message")
	}
	if trackingID != "" {
		message = i18n.TranslateFormat(lang, "contact.security_received", map[string]string{"tracking_id": trackingID})
		if acknowledgmentSent {
			message += " " + i18n.Translate(lang, "contact.security_acknowledgment_sent")
		}
	}
	if api {
		data := map[string]string{"message": message}
		if trackingID != "" {
			data["tracking_id"] = trackingID
		}
		s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: data})
		return
	}
	view := contactPageView{FormNotice: message, SecurityURL: s.BuildURL(r, "/server/security")}
	s.renderContactPage(w, r, view)
}

func (s *Server) storeSecurityReport(ctx context.Context, values contactSubmission, idWindow int64, userAgent, lang string) (string, string, []byte, string, error) {
	if s.store == nil {
		return "", "", nil, "", errors.New("application database is unavailable")
	}
	legacySecurityID, err := s.store.HasColumn(ctx, "security_reports", "security_id")
	if err != nil {
		return "", "", nil, "", err
	}
	projectPGPLock.Lock()
	defer projectPGPLock.Unlock()
	if _, err := s.installationSecret(ctx); err != nil {
		return "", "", nil, "", err
	}
	var idBytes [8]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return "", "", nil, "", err
	}
	trackingID := "sec_" + hex.EncodeToString(idBytes[:])
	bodyText := contactEmailBody(lang, "security", values, trackingID)
	bodyText += i18n.Translate(lang, "email.body.application_version") + ": " + sanitizeLogValue(s.version) + "\r\n"
	bodyText += i18n.Translate(lang, "email.body.commit_hash") + ": " + sanitizeLogValue(s.commit) + "\r\n"
	bodyText += i18n.Translate(lang, "email.body.submitted_at") + ": " + time.Now().UTC().Format(time.RFC3339Nano) + "\r\n"
	bodyText += i18n.Translate(lang, "email.body.request_user_agent") + ": " + sanitizeLogValue(userAgent) + "\r\n"
	contactData, err := json.Marshal(map[string]string{"name": values["name"], "email": values["email"], "researcher_gpg": values["researcher_gpg"]})
	if err != nil {
		return "", "", nil, "", err
	}
	encryptedContact, err := s.encryptContactBody(contactData)
	if err != nil {
		return "", "", nil, "", err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", "", nil, "", err
	}
	statusToken := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256([]byte(statusToken))
	credit := map[string]string{"real_name": "name", "handle": "handle", "anonymous": "anon", "no": "no"}[values["credit_preference"]]
	creditName := ""
	switch credit {
	case "name":
		creditName = values["name"]
	case "handle":
		creditName = values["credit_handle"]
	}
	var encryptedBody []byte
	var bodyAlgorithm string
	encryptedBody, bodyAlgorithm, err = s.encryptSecurityReportBodyLocked(ctx, []byte(bodyText))
	if err != nil {
		return "", "", nil, "", err
	}
	insert := `INSERT INTO security_reports (tracking_id, id_window, severity, component, status, body_enc, body_alg, contact_enc, credit_pref, credit_name, cve_requested, token_hash) VALUES (?, ?, ?, ?, 'received', ?, ?, ?, ?, ?, ?, ?)`
	arguments := []any{trackingID, idWindow, values["severity"], values["component"], encryptedBody, bodyAlgorithm, encryptedContact, credit, nullableContactText(creditName), values["cve_requested"] == "true", hex.EncodeToString(tokenHash[:])}
	if legacySecurityID {
		insert = `INSERT INTO security_reports (tracking_id, id_window, security_id, severity, component, status, body_enc, body_alg, contact_enc, credit_pref, credit_name, cve_requested, token_hash) VALUES (?, ?, '', ?, ?, 'received', ?, ?, ?, ?, ?, ?, ?)`
	}
	err = s.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, insert, arguments...)
		return err
	})
	if err != nil {
		return "", "", nil, "", err
	}
	return trackingID, statusToken, encryptedBody, bodyAlgorithm, nil
}

func (s *Server) encryptSecurityReportBodyLocked(ctx context.Context, body []byte) ([]byte, string, error) {
	keypair, err := s.loadProjectPGPKeypairLocked(ctx)
	if err == nil {
		encrypted, encryptErr := encryptPGPMessage(keypair.Entity, body, keypair.Entity)
		return encrypted, "pgp", encryptErr
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	encrypted, err := s.encryptContactBody(body)
	return encrypted, "aes-256-gcm", err
}

func (s *Server) encryptContactBody(plain []byte) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(s.cfg.Server.Security.EncryptionKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("contact encryption key is invalid")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, []byte("citylist-security-report-v1")), nil
}

func nullableContactText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (s *Server) deliverSecurityNotification(ctx context.Context, r *http.Request, trackingID string, encryptedBody []byte, bodyAlgorithm string) error {
	to := s.resolveContactEmail(r, strings.TrimSpace(s.cfg.Server.Contact.Security.Email))
	if to == "" {
		to = strings.TrimSpace(s.cfg.Server.Contact.Admin.Email)
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return errors.New("security notification recipient is not configured")
	}
	from := s.cfg.Server.Notifications.Email.From
	fromEmail := strings.TrimSpace(from.Email)
	if fromEmail == "" {
		fromEmail = "no-reply@" + requestFQDN(r)
	}
	if _, err := mail.ParseAddress(fromEmail); err != nil {
		return errors.New("security notification sender is invalid")
	}
	lang := i18n.LangFromRequest(r)
	messageText := i18n.TranslateFormat(lang, "email.body.security_notification_pgp", map[string]string{"tracking_id": trackingID}) + "\r\n"
	if bodyAlgorithm != "pgp" {
		messageText = i18n.TranslateFormat(lang, "email.body.security_notification_fallback", map[string]string{"tracking_id": trackingID}) + "\r\n"
	}
	message := contactMailMessage{
		FromName: from.Name, From: fromEmail, To: to,
		Subject:    i18n.TranslateFormat(lang, "email.subjects.security_report_notification", map[string]string{"tracking_id": trackingID}),
		Text:       messageText,
		Attachment: encryptedBody, AttachmentName: trackingID + ".enc",
	}
	return contactMailSender(ctx, s.cfg.Server.Notifications.Email.SMTP, message, requestFQDN(r))
}

func (s *Server) sendResearcherAcknowledgment(ctx context.Context, r *http.Request, address, trackingID, token, researcherKey string) error {
	if _, err := mail.ParseAddress(address); err != nil {
		return err
	}
	from := s.cfg.Server.Notifications.Email.From
	fromEmail := strings.TrimSpace(from.Email)
	if fromEmail == "" {
		fromEmail = "no-reply@" + requestFQDN(r)
	}
	statusURL := s.BuildURL(r, "/server/security/report/"+url.PathEscape(trackingID)) + "?token=" + url.QueryEscape(token)
	lang := i18n.LangFromRequest(r)
	body := []byte(i18n.TranslateFormat(lang, "email.body.security_report_acknowledgment", map[string]string{"tracking_id": trackingID, "status_url": statusURL}) + "\r\n")
	if strings.TrimSpace(researcherKey) != "" {
		var err error
		body, err = s.EncryptForResearcherPGP(ctx, researcherKey, body)
		if err != nil {
			return fmt.Errorf("encrypt researcher acknowledgment: %w", err)
		}
	}
	message := contactMailMessage{
		FromName: from.Name, From: fromEmail, To: address,
		Subject: i18n.Translate(lang, "email.subjects.security_report_acknowledgment"),
		Text:    string(body),
	}
	return contactMailSender(ctx, s.cfg.Server.Notifications.Email.SMTP, message, requestFQDN(r))
}

func (s *Server) handleSecurityReportStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This page accepts GET requests")
		return
	}
	trackingID := chi.URLParam(r, "trackingID")
	token := r.URL.Query().Get("token")
	if len(token) > 128 || token == "" || s.store == nil {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", i18n.Translate(i18n.LangFromRequest(r), "errors.not_found"))
		return
	}
	hash := sha256.Sum256([]byte(token))
	var status, severity, component, comment, disclosure string
	err := s.store.Write(r.Context(), func(tx *sql.Tx) error {
		var expected string
		var lastUsed sql.NullString
		var closed sql.NullTime
		if err := tx.QueryRowContext(r.Context(), `SELECT token_hash, status, severity, component, researcher_comment, COALESCE(disclosure_date, ''), token_last_used, closed_at FROM security_reports WHERE tracking_id = ?`, trackingID).Scan(&expected, &status, &severity, &component, &comment, &disclosure, &lastUsed, &closed); err != nil {
			return err
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(hex.EncodeToString(hash[:]))) != 1 || lastUsed.Valid && lastUsed.String == time.Now().UTC().Format("2006-01-02") || closed.Valid && closed.Time.Add(30*24*time.Hour).Before(time.Now()) {
			return sql.ErrNoRows
		}
		result, err := tx.ExecContext(r.Context(), `UPDATE security_reports SET token_last_used = date('now') WHERE tracking_id = ? AND token_hash = ? AND (token_last_used IS NULL OR token_last_used < date('now'))`, trackingID, hex.EncodeToString(hash[:]))
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", i18n.Translate(i18n.LangFromRequest(r), "errors.not_found"))
		return
	}
	if err != nil {
		s.respondError(w, http.StatusServiceUnavailable, "STATUS_UNAVAILABLE", "Report status is temporarily unavailable")
		return
	}
	lang := i18n.LangFromRequest(r)
	s.renderPage(w, r, "security-report.html", map[string]interface{}{
		"Title":      i18n.Translate(lang, "security_report.status_title"),
		"TrackingID": trackingID, "Status": securityStatusLabel(lang, status),
		"Severity": i18n.Translate(lang, "pages.security.severity_"+strings.ToLower(severity)), "Component": component, "Comment": comment, "Disclosure": disclosure,
	})
}

func securityStatusLabel(lang, status string) string {
	key := "security_report.status_" + status
	translated := i18n.Translate(lang, key)
	if translated == key {
		return i18n.Translate(lang, "security_report.status_received")
	}
	return translated
}

func sanitizeLogValue(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	if len(value) > 128 {
		return value[:128]
	}
	return value
}

func (s *Server) verifyContactCaptcha(ctx context.Context, r *http.Request, values contactSubmission) (bool, error) {
	providerResponse := values["captcha_response"]
	if providerResponse == "" {
		providerResponse = values["g-recaptcha-response"]
	}
	if providerResponse == "" {
		providerResponse = values["h-captcha-response"]
	}
	provider := s.cfg.Server.Pages.Contact.Captcha
	if contactRequestOnPrivateNetwork(r) {
		provider = "simple"
	}
	if providerResponse != "" && provider != "simple" {
		valid, err := s.verifyProviderCaptcha(ctx, r, providerResponse)
		return valid, err
	}
	return s.verifySimpleCaptcha(ctx, values["captcha_id"], values["captcha_answer"]), nil
}

func (s *Server) validateContactContent(r *http.Request, kind string, values contactSubmission) string {
	anti := s.cfg.Server.Pages.Contact.Antispam
	for _, key := range []string{"name", "subject"} {
		if strings.TrimSpace(values[key]) == "" && key == "subject" {
			return "shape"
		}
		if contactLinkPattern.MatchString(values[key]) {
			return "content"
		}
	}
	if kind == "abuse" {
		if values["reported_url"] == "" || !s.validReportedURL(r, values["reported_url"]) {
			return "content"
		}
		if !oneOf(values["reason"], "illegal_content", "terms_violation", "harassment", "malware_phishing", "other") {
			return "content"
		}
	}
	if kind == "dmca" {
		if values["reported_url"] == "" || !s.validReportedURL(r, values["reported_url"]) || strings.TrimSpace(values["copyrighted_work"]) == "" || values["good_faith"] != "true" || values["accuracy"] != "true" || strings.TrimSpace(values["signature"]) == "" {
			return "content"
		}
	}
	if kind == "privacy" && !oneOf(values["request_kind"], "access", "export", "deletion", "correction", "opt_out", "other") {
		return "content"
	}
	if anti.RejectMarkup {
		for key, value := range values {
			if key == "website" || key == "csrf_token" || key == "form_token" || key == "captcha_id" || key == "captcha_answer" || key == "type" || key == "step" || key == "security_id" {
				continue
			}
			decoded := html.UnescapeString(value)
			if contactMarkupPattern.MatchString(value) || strings.ContainsAny(decoded, "<>") && !strings.Contains(value, "<") && !strings.Contains(value, ">") {
				return "markup"
			}
		}
	}
	if s.contactLinksAllowed(r, kind, values, anti.MaxLinks) != nil {
		return "content"
	}
	if s.blockedContactContent(values, anti.BlockedWords, anti.BlockedEmailDomains) {
		return "content"
	}
	return ""
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func (s *Server) blockedContactContent(values contactSubmission, words, domains []string) bool {
	for _, value := range []string{values["name"], values["subject"], values["message"]} {
		lower := strings.ToLower(value)
		for _, word := range words {
			if word != "" && strings.Contains(lower, strings.ToLower(word)) {
				return true
			}
		}
	}
	parts := strings.Split(strings.ToLower(strings.TrimSpace(values["email"])), "@")
	if len(parts) == 2 {
		for _, domain := range domains {
			if strings.EqualFold(parts[1], strings.TrimSpace(domain)) {
				return true
			}
		}
	}
	return false
}

func (s *Server) contactLinksAllowed(r *http.Request, kind string, values contactSubmission, maxLinks int) error {
	if maxLinks <= 0 {
		return nil
	}
	count := 0
	for key, value := range values {
		switch key {
		case "name", "subject", "email", "reported_url", "researcher_gpg", "endpoint", "security_id",
			"website", "csrf_token", "form_token", "captcha_id", "captcha_answer", "type", "step", "confirmed", "review_token":
			continue
		}
		matches := contactLinkPattern.FindAllStringIndex(value, -1)
		for _, match := range matches {
			if match[0] > 0 && value[match[0]-1] == '@' {
				continue
			}
			count++
			if count > maxLinks {
				return errors.New("too many links")
			}
			token := value[match[0]:match[1]]
			host := contactLinkHost(token)
			if host != "" && !s.contactHostAllowed(r, host) {
				return errors.New("external link")
			}
		}
	}
	return nil
}

func contactLinkHost(value string) string {
	candidate := value
	if strings.HasPrefix(strings.ToLower(candidate), "www.") {
		candidate = "https://" + candidate
	}
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
}

func (s *Server) contactHostAllowed(r *http.Request, host string) bool {
	allowed := map[string]bool{}
	if parsed, err := url.Parse(s.BuildURL(r, "/")); err == nil {
		allowed[strings.ToLower(parsed.Hostname())] = true
	}
	if s.cfg.Server.FQDN != "" {
		allowed[strings.ToLower(s.cfg.Server.FQDN)] = true
	}
	for _, domain := range strings.Split(os.Getenv("DOMAIN"), ",") {
		domain = strings.TrimSpace(strings.ToLower(domain))
		if domain != "" {
			allowed[domain] = true
		}
	}
	if contactRequestOnPrivateNetwork(r) {
		allowed[strings.ToLower(hostnameOnly(r.Host))] = true
	}
	return allowed[host]
}

func (s *Server) validReportedURL(r *http.Request, raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || !s.contactHostAllowed(r, strings.ToLower(parsed.Hostname())) {
		return false
	}
	path := parsed.EscapedPath()
	if path == "" || path == "/" || path == "/server/contact" || strings.HasPrefix(path, "/api/") {
		return false
	}
	route := chi.NewRouteContext()
	if !s.router.Match(route, http.MethodGet, path) {
		return false
	}
	switch route.RoutePattern() {
	case "/cities/{id}":
		id, err := strconv.Atoi(route.URLParam("id"))
		if err != nil || s.citySvc == nil {
			return false
		}
		if _, found := s.citySvc.GetByID(id); !found {
			return false
		}
	case "/zipcodes/{zip}":
		if s.zipSvc == nil {
			return false
		}
		if _, found := s.zipSvc.Lookup(route.URLParam("zip")); !found {
			return false
		}
	}
	return true
}

func (s *Server) contactRateAllowed(r *http.Request, email string, count bool) (bool, string) {
	anti := s.cfg.Server.Pages.Contact.Antispam
	window, err := time.ParseDuration(anti.Window)
	if err != nil || window <= 0 {
		window = time.Hour
	}
	ip := contactClientIP(r)
	private := contactRequestOnPrivateNetwork(r)
	now := time.Now()
	contactState.Lock()
	defer contactState.Unlock()
	contactPruneLocked(now)
	if strike := contactState.strikes[ip]; strike.Banned.After(now) {
		return false, "banned"
	}
	keys := []struct {
		key    string
		limit  int
		reason string
	}{{"global", anti.Global, "rate_global"}, {"email:" + strings.ToLower(strings.TrimSpace(email)), anti.PerEmail, "rate_email"}}
	if !private {
		keys = append(keys, struct {
			key    string
			limit  int
			reason string
		}{"ip:" + ip, anti.PerIP, "rate_ip"})
	}
	for _, item := range keys {
		if item.limit == 0 {
			continue
		}
		counter := contactState.counters[item.key]
		if now.Sub(counter.Started) >= window {
			counter = contactCounter{Started: now}
		}
		if counter.Count >= item.limit {
			return false, item.reason
		}
	}
	if count {
		for _, item := range keys {
			if item.limit == 0 {
				continue
			}
			counter := contactState.counters[item.key]
			if now.Sub(counter.Started) >= window {
				counter = contactCounter{Started: now}
			}
			counter.Count++
			contactState.counters[item.key] = counter
		}
	}
	return true, ""
}

func contactPruneLocked(now time.Time) {
	for key, item := range contactState.used {
		if !item.After(now) {
			delete(contactState.used, key)
		}
	}
	for key, item := range contactState.counters {
		if now.Sub(item.Started) > 24*time.Hour {
			delete(contactState.counters, key)
		}
	}
	for key, item := range contactState.strikes {
		if !item.Banned.After(now) && now.Sub(item.Started) > 24*time.Hour {
			delete(contactState.strikes, key)
		}
	}
	for key, expiry := range contactState.duplicates {
		if !expiry.After(now) {
			delete(contactState.duplicates, key)
		}
	}
}

func (s *Server) contactStrike(r *http.Request, reason string) {
	anti := s.cfg.Server.Pages.Contact.Antispam
	if anti.Strikes <= 0 || contactRequestOnPrivateNetwork(r) {
		return
	}
	window, err := time.ParseDuration(anti.Window)
	if err != nil || window <= 0 {
		window = time.Hour
	}
	ip, now := contactClientIP(r), time.Now()
	contactState.Lock()
	defer contactState.Unlock()
	contactPruneLocked(now)
	strike := contactState.strikes[ip]
	if now.Sub(strike.Started) >= window {
		strike = contactStrike{Started: now}
	}
	strike.Count++
	if strike.Count >= anti.Strikes {
		strike.Banned = now.Add(time.Duration(anti.BanHours) * time.Hour)
	}
	contactState.strikes[ip] = strike
}

func (s *Server) contactBlocked(r *http.Request, reason string, silent bool) {
	slog.Warn("contact.spam_blocked", "reason", reason, "ip", contactClientIP(r), "silent", silent)
}

func (s *Server) isDuplicateContact(values contactSubmission, kind string) bool {
	window := time.Duration(s.cfg.Server.Pages.Contact.Antispam.DuplicateWindowHours) * time.Hour
	if window <= 0 {
		return false
	}
	canonical := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(values["email"])+"\n"+contactSubject(kind, values)+"\n"+contactMessage(kind, values)), " "))
	hash := sha256.Sum256([]byte(canonical))
	contactState.Lock()
	defer contactState.Unlock()
	contactPruneLocked(time.Now())
	expires := contactState.duplicates[hex.EncodeToString(hash[:])]
	return expires.After(time.Now())
}

func (s *Server) markContactDuplicate(values contactSubmission, kind string) {
	window := time.Duration(s.cfg.Server.Pages.Contact.Antispam.DuplicateWindowHours) * time.Hour
	if window <= 0 {
		return
	}
	canonical := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(values["email"])+"\n"+contactSubject(kind, values)+"\n"+contactMessage(kind, values)), " "))
	hash := sha256.Sum256([]byte(canonical))
	contactState.Lock()
	defer contactState.Unlock()
	contactPruneLocked(time.Now())
	if len(contactState.duplicates) >= 10000 {
		var oldestKey string
		var oldest time.Time
		for key, expiry := range contactState.duplicates {
			if oldest.IsZero() || expiry.Before(oldest) {
				oldestKey, oldest = key, expiry
			}
		}
		delete(contactState.duplicates, oldestKey)
	}
	contactState.duplicates[hex.EncodeToString(hash[:])] = time.Now().Add(window)
}

func contactSubject(kind string, values contactSubmission) string {
	if kind == "security" {
		return values["summary"]
	}
	return values["subject"]
}

func contactMessage(kind string, values contactSubmission) string {
	if kind == "security" {
		return values["steps"] + "\n" + values["impact"] + "\n" + values["suggested_fix"]
	}
	return values["message"]
}

func contactClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) deliverContact(ctx context.Context, r *http.Request, kind string, values contactSubmission, trackingID string) error {
	to := strings.TrimSpace(s.cfg.Server.Contact.Abuse.Email)
	if kind == "privacy" {
		to = strings.TrimSpace(s.cfg.Server.Contact.DPO.Email)
	}
	if kind == "security" {
		to = strings.TrimSpace(s.cfg.Server.Contact.Security.Email)
	}
	if to == "" {
		to = strings.TrimSpace(s.cfg.Server.Contact.General.Email)
	}
	if to == "" {
		to = strings.TrimSpace(s.cfg.Server.Contact.Admin.Email)
	}
	to = s.resolveContactEmail(r, to)
	if _, err := mail.ParseAddress(to); err != nil {
		return errors.New("contact recipient is not configured")
	}
	from := s.cfg.Server.Notifications.Email.From
	fromEmail := strings.TrimSpace(from.Email)
	if fromEmail == "" {
		fromEmail = "no-reply@" + requestFQDN(r)
	}
	if strings.ContainsAny(fromEmail+from.Name+values["email"]+contactSubject(kind, values), "\r\n") {
		return errors.New("unsafe email header")
	}
	if _, err := mail.ParseAddress(fromEmail); err != nil {
		return errors.New("contact sender is invalid")
	}
	lang := i18n.LangFromRequest(r)
	body := contactEmailBody(lang, kind, values, trackingID)
	message := contactMailMessage{FromName: from.Name, From: fromEmail, To: to, ReplyTo: strings.TrimSpace(values["email"]), Subject: i18n.TranslateFormat(lang, "email.subjects.contact_submission", map[string]string{"app_name": "Citylist", "subject": contactSubject(kind, values)}), Text: body}
	return contactMailSender(ctx, s.cfg.Server.Notifications.Email.SMTP, message, requestFQDN(r))
}

func requestFQDN(r *http.Request) string {
	host := r.Host
	if parsed, err := url.Parse("//" + host); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	return strings.TrimSuffix(host, ".")
}

func contactEmailBody(lang, kind string, values contactSubmission, trackingID string) string {
	var body strings.Builder
	fieldLabel := func(name string) string { return i18n.Translate(lang, "contact.field_"+name) }
	body.WriteString(i18n.Translate(lang, "email.body.field_type") + ": " + i18n.Translate(lang, "contact.type_"+kind) + "\r\n")
	body.WriteString(fieldLabel("name") + ": " + strings.TrimSpace(values["name"]) + "\r\n")
	body.WriteString(fieldLabel("email") + ": " + strings.TrimSpace(values["email"]) + "\r\n")
	if kind == "security" {
		for _, key := range []string{"component", "endpoint", "severity", "summary", "steps", "impact", "suggested_fix", "credit_preference", "credit_handle", "disclosure_days", "cve_requested"} {
			if values[key] != "" {
				value := strings.TrimSpace(values[key])
				if key == "severity" || key == "credit_preference" {
					value = i18n.Translate(lang, "contact.option_"+value)
				}
				body.WriteString(fieldLabel(key) + ": " + value + "\r\n")
			}
		}
		body.WriteString(i18n.Translate(lang, "email.body.tracking_id") + ": " + trackingID + "\r\n")
		return body.String()
	}
	for _, key := range []string{"reported_url", "reason", "copyrighted_work", "good_faith", "accuracy", "signature", "request_kind", "subject", "message"} {
		if values[key] != "" {
			value := strings.TrimSpace(values[key])
			if key == "reason" || key == "request_kind" {
				value = i18n.Translate(lang, "contact.option_"+value)
			}
			body.WriteString(fieldLabel(key) + ": " + value + "\r\n")
		}
	}
	return body.String()
}

func sendContactSMTP(ctx context.Context, cfg config.SMTPConfig, message contactMailMessage, fqdn string) error {
	host := strings.TrimSpace(cfg.Host)
	port := cfg.Port
	if host == "" {
		return errors.New("SMTP host has not been configured or autodetected")
	}
	if port <= 0 {
		port = 25
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.TLS))
	if mode == "" {
		mode = "auto"
	}
	implicitTLS := mode == "tls" || mode == "auto" && port == 465
	if implicitTLS {
		tlsConn := tls.Client(conn, contactTLSConfig(host))
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return err
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = client.Close() }()
	secure := implicitTLS
	if !implicitTLS && mode != "none" {
		supported, _ := client.Extension("STARTTLS")
		if supported {
			if err := client.StartTLS(contactTLSConfig(host)); err != nil {
				return err
			}
			secure = true
		} else if mode == "starttls" || cfg.Username != "" || cfg.Password != "" {
			return errors.New("SMTP server does not support required STARTTLS")
		}
	}
	if mode != "auto" && mode != "none" && mode != "tls" && mode != "starttls" {
		return errors.New("unsupported SMTP TLS mode")
	}
	if (cfg.Username != "" || cfg.Password != "") && !secure {
		return errors.New("SMTP credentials require TLS")
	}
	if cfg.Username != "" || cfg.Password != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, host)); err != nil {
			return err
		}
	}
	from, err := mail.ParseAddress(message.From)
	if err != nil {
		return err
	}
	to, err := mail.ParseAddress(message.To)
	if err != nil {
		return err
	}
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(to.Address); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	payload, err := buildContactMIME(message, fqdn)
	if err != nil {
		_ = writer.Close()
		return err
	}
	if _, err := writer.Write(payload); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func contactTLSConfig(host string) *tls.Config {
	parsed, err := netip.ParseAddr(strings.Trim(host, "[]"))
	private := false
	if err == nil {
		cgnat := netip.MustParsePrefix("100.64.0.0/10")
		private = parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() || cgnat.Contains(parsed)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, InsecureSkipVerify: private}
}

func buildContactMIME(message contactMailMessage, fqdn string) ([]byte, error) {
	from, err := mail.ParseAddress(message.From)
	if err != nil || strings.ContainsAny(message.FromName+message.To+message.ReplyTo+message.Subject, "\r\n") {
		return nil, errors.New("unsafe email header")
	}
	to, err := mail.ParseAddress(message.To)
	if err != nil {
		return nil, err
	}
	if message.ReplyTo != "" {
		if _, err := mail.ParseAddress(message.ReplyTo); err != nil {
			return nil, err
		}
	}
	messageID, err := contactRandomID()
	if err != nil {
		return nil, err
	}
	if fqdn == "" || strings.ContainsAny(fqdn, " <>\r\n") {
		fqdn = "localhost"
	}
	fromHeader := "<" + from.Address + ">"
	if message.FromName != "" {
		fromHeader = mime.QEncoding.Encode("UTF-8", message.FromName) + " <" + from.Address + ">"
	}
	var output strings.Builder
	fmt.Fprintf(&output, "From: %s\r\nTo: <%s>\r\n", fromHeader, to.Address)
	if message.ReplyTo != "" {
		fmt.Fprintf(&output, "Reply-To: <%s>\r\n", message.ReplyTo)
	}
	fmt.Fprintf(&output, "Subject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nAuto-Submitted: auto-generated\r\nMIME-Version: 1.0\r\n", mime.QEncoding.Encode("UTF-8", message.Subject), time.Now().Format(time.RFC1123Z), messageID, fqdn)
	if len(message.Attachment) == 0 {
		output.WriteString("Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
		encoder := quotedprintable.NewWriter(&output)
		if _, err := io.WriteString(encoder, message.Text); err != nil {
			return nil, err
		}
		if err := encoder.Close(); err != nil {
			return nil, err
		}
		return []byte(output.String()), nil
	}
	if message.AttachmentName == "" || strings.ContainsAny(message.AttachmentName, "\\/\r\n") {
		return nil, errors.New("unsafe attachment name")
	}
	var body bytes.Buffer
	mixed := multipart.NewWriter(&body)
	fmt.Fprintf(&output, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mixed.Boundary())
	textPart, err := mixed.CreatePart(map[string][]string{"Content-Type": {"text/plain; charset=UTF-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
	if err != nil {
		return nil, err
	}
	encoder := quotedprintable.NewWriter(textPart)
	if _, err := io.WriteString(encoder, message.Text); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	attachmentPart, err := mixed.CreatePart(map[string][]string{
		"Content-Type":              {"application/octet-stream"},
		"Content-Transfer-Encoding": {"base64"},
		"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": message.AttachmentName})},
	})
	if err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(message.Attachment)
	for len(encoded) > 76 {
		if _, err := io.WriteString(attachmentPart, encoded[:76]+"\r\n"); err != nil {
			return nil, err
		}
		encoded = encoded[76:]
	}
	if _, err := io.WriteString(attachmentPart, encoded+"\r\n"); err != nil {
		return nil, err
	}
	if err := mixed.Close(); err != nil {
		return nil, err
	}
	if _, err := output.Write(body.Bytes()); err != nil {
		return nil, err
	}
	return []byte(output.String()), nil
}

func contactRandomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
