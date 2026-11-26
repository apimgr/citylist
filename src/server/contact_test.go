package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
)

func TestBuildContactMIMEIsPlainText(t *testing.T) {
	message := contactMailMessage{
		FromName: "Citylist",
		From:     "no-reply@example.test",
		To:       "reports@example.test",
		ReplyTo:  "sender@example.test",
		Subject:  "Contact report",
		Text:     "A plain text report with non-ASCII: café",
	}
	data, err := buildContactMIME(message, "example.test")
	if err != nil {
		t.Fatalf("buildContactMIME() error: %v", err)
	}
	got := string(data)
	for _, want := range []string{"Content-Type: text/plain; charset=UTF-8", "From: Citylist <no-reply@example.test>", "Reply-To: <sender@example.test>", "Message-ID: <"} {
		if !strings.Contains(got, want) {
			t.Errorf("message missing %q", want)
		}
	}
	for _, forbidden := range []string{"text/html", "multipart/", "Content-Disposition:", "no-reply@example.test\r\nTo:"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("message unexpectedly contains %q", forbidden)
		}
	}
}

func TestContactRequestOnPrivateNetworkRequiresConfiguredOverlayHost(t *testing.T) {
	s := testServer(t)
	for _, host := range []string{"service.onion", "service.b32.i2p", "example.test"} {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
		if s.contactRequestOnPrivateNetwork(r) {
			t.Errorf("unconfigured host %q was classified as a private overlay", host)
		}
	}
}

func TestContactSuccessDoesNotExposeSkippedTorWebhook(t *testing.T) {
	s := testServer(t)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/server/contact", nil)
	s.contactSuccess(response, request, true, "sec_1234567890abcdef", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "sec_1234567890abcdef") {
		t.Fatalf("security report success response = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(strings.ToLower(response.Body.String()), "webhook") || strings.Contains(response.Body.String(), "operator retrieval") {
		t.Fatalf("security report response exposes skipped webhook delivery: %s", response.Body.String())
	}
}

func TestContactTLSVerificationPolicy(t *testing.T) {
	tests := []struct {
		host            string
		insecureSkipTLS bool
	}{
		{host: "127.0.0.1", insecureSkipTLS: true},
		{host: "172.17.0.1", insecureSkipTLS: true},
		{host: "100.64.0.1", insecureSkipTLS: true},
		{host: "fd00::1", insecureSkipTLS: true},
		{host: "fe80::1", insecureSkipTLS: true},
		{host: "203.0.113.1", insecureSkipTLS: false},
		{host: "smtp.example.test", insecureSkipTLS: false},
	}
	for _, test := range tests {
		config := contactTLSConfig(test.host)
		if config.InsecureSkipVerify != test.insecureSkipTLS {
			t.Errorf("contactTLSConfig(%q).InsecureSkipVerify = %v, want %v", test.host, config.InsecureSkipVerify, test.insecureSkipTLS)
		}
		if config.MinVersion != tls.VersionTLS12 {
			t.Errorf("contactTLSConfig(%q).MinVersion = %v, want TLS 1.2", test.host, config.MinVersion)
		}
	}
}

func TestInitializeInstallationSecretIsIdempotent(t *testing.T) {
	store := testServer(t).store
	ctx := context.Background()
	if err := InitializeInstallationSecret(ctx, store); err != nil {
		t.Fatalf("InitializeInstallationSecret() error: %v", err)
	}
	var first, updated string
	if err := store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT value, updated_at FROM app_secrets WHERE name = 'installation_secret'`).Scan(&first, &updated)
	}); err != nil {
		t.Fatalf("read installation_secret: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(first)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("installation secret is not a base64-encoded 32-byte value")
	}
	if _, err := time.Parse(time.RFC3339Nano, updated); err != nil {
		t.Fatalf("updated_at is not UTC ISO 8601: %v", err)
	}
	if err := InitializeInstallationSecret(ctx, store); err != nil {
		t.Fatalf("second InitializeInstallationSecret() error: %v", err)
	}
	var second string
	if err := store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name = 'installation_secret'`).Scan(&second)
	}); err != nil {
		t.Fatalf("read second installation_secret: %v", err)
	}
	if second != first {
		t.Fatal("initialization replaced the existing installation secret")
	}
}

func TestContactSMTPRequiresSelectedHost(t *testing.T) {
	err := sendContactSMTP(context.Background(), config.SMTPConfig{}, contactMailMessage{}, "example.test")
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("sendContactSMTP() error = %v, want missing-host error", err)
	}
}

func TestContactTypeFieldsAndDescriptors(t *testing.T) {
	s := testServer(t)
	for _, kind := range []string{"abuse", "dmca", "privacy"} {
		fields := s.contactFields(kind, "en", contactSubmission{"subject": "example"}, "")
		if len(fields) < 4 || fields[0].Name != "name" || fields[1].Name != "email" {
			t.Fatalf("unexpected %s fields: %+v", kind, fields)
		}
		for _, field := range fields {
			if field.Name == "security_id" || field.Name == "researcher_gpg" {
				t.Errorf("%s form includes security-only field %q", kind, field.Name)
			}
		}
		descriptors := contactFieldDescriptors(fields)
		for index, descriptor := range descriptors {
			if descriptor["name"] != fields[index].Name {
				t.Errorf("%s descriptor %d names %v, want %s", kind, index, descriptor["name"], fields[index].Name)
			}
		}
	}
	security := s.contactFields("security", "en", nil, "security-id")
	if !contactTypeOffered("security", true) || contactTypeOffered("security", false) || contactTypeOffered("support", false) {
		t.Fatal("security contact type was offered outside security mode")
	}
	foundID := false
	for _, field := range security {
		if field.Name == "security_id" && field.Value == "security-id" && field.Required {
			foundID = true
		}
	}
	if !foundID {
		t.Fatal("security form omitted its required validation ID")
	}
	fields := s.contactFields("abuse", "en", nil, "")
	for _, descriptor := range contactFieldDescriptors(fields) {
		if descriptor["name"] == "reported_url" && descriptor["required_when"] == nil {
			t.Fatal("reported_url omitted its type-dependent requirement")
		}
	}
}

func TestSecurityIDAcceptsCurrentAndPreviousWindows(t *testing.T) {
	s := testServer(t)
	now := time.Now().Unix() / 172800
	for _, window := range []int64{now, now - 1} {
		id, err := s.securityID(context.Background(), window)
		if err != nil {
			t.Fatal(err)
		}
		if !s.validSecurityID(context.Background(), id) {
			t.Errorf("valid security ID for window %d was rejected", window)
		}
		if got, valid := s.validSecurityIDWindow(context.Background(), id); !valid || got != window {
			t.Errorf("valid security ID window = (%d, %t), want (%d, true)", got, valid, window)
		}
	}
	if s.validSecurityID(context.Background(), "not-a-valid-id") || s.validSecurityID(context.Background(), "0000000000000000") {
		t.Fatal("invalid security ID was accepted")
	}
}

func TestContactFormTokenValidationAndSingleUseNonce(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Pages.Contact.Antispam.MinFillSeconds = 0
	s := testServerWithConfig(t, cfg)
	ctx := context.Background()
	token, err := s.newContactFormToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	valid, nonce, expiry := s.verifyContactFormToken(ctx, token)
	if !valid || nonce == "" || !expiry.After(time.Now()) {
		t.Fatalf("valid form token result = %t, %q, %v", valid, nonce, expiry)
	}
	if !s.consumeContactNonce("test-form:"+nonce, expiry) || s.consumeContactNonce("test-form:"+nonce, expiry) {
		t.Fatal("contact form nonce was not single-use")
	}
	forged := token[:len(token)-1] + "0"
	if valid, _, _ := s.verifyContactFormToken(ctx, forged); valid {
		t.Fatal("forged form token was accepted")
	}
}

func TestContactOriginLinksAndReportedURLValidation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.FQDN = "citylist.test"
	s := testServerWithConfig(t, cfg)
	request := httptest.NewRequest(http.MethodPost, "https://citylist.test/server/contact", nil)
	request.Host = "citylist.test"
	origin := strings.TrimSuffix(s.BuildURL(request, "/"), "/")
	request.Header.Set("Origin", origin)
	if !s.contactOriginOK(request) {
		t.Fatal("matching Origin was rejected")
	}
	request.Header.Set("Origin", "https://attacker.test")
	if s.contactOriginOK(request) {
		t.Fatal("mismatched Origin was accepted")
	}
	request.Header.Del("Origin")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if !s.contactOriginOK(request) {
		t.Fatal("same-origin fetch metadata was rejected")
	}
	request.Header.Del("Sec-Fetch-Site")
	if s.contactOriginOK(request) {
		t.Fatal("request with no origin evidence was accepted")
	}

	request = httptest.NewRequest(http.MethodPost, "https://citylist.test/server/contact", nil)
	request.Host = "citylist.test"
	if err := s.contactLinksAllowed(request, "abuse", contactSubmission{"message": "See https://citylist.test/cities/1"}, 2); err != nil {
		t.Fatalf("same-host link rejected: %v", err)
	}
	if err := s.contactLinksAllowed(request, "abuse", contactSubmission{"message": "See https://external.test/path"}, 2); err == nil {
		t.Fatal("external link was accepted")
	}
	if err := s.contactLinksAllowed(request, "abuse", contactSubmission{"message": "https://citylist.test/a https://citylist.test/b"}, 1); err == nil {
		t.Fatal("submission exceeding max_links was accepted")
	}
	for raw, want := range map[string]bool{
		"https://citylist.test/server/about":   true,
		"https://citylist.test/cities/999999":  false,
		"https://citylist.test/api/v1/cities":  false,
		"https://citylist.test/server/contact": false,
		"https://external.test/cities/1":       false,
	} {
		if got := s.validReportedURL(request, raw); got != want {
			t.Errorf("validReportedURL(%q) = %t, want %t", raw, got, want)
		}
	}
}

func TestParseAndValidateContactSubmissionShapes(t *testing.T) {
	validPrivacy := contactSubmission{
		"type": "privacy", "name": "Reporter", "email": "reporter@example.test",
		"form_token": "signed-token", "request_kind": "access", "subject": "Data request", "message": "Please provide my data.",
	}
	if err := validateContactShape(validPrivacy, contactFieldNameSet("privacy"), true); err != nil {
		t.Fatalf("valid privacy report rejected: %v", err)
	}
	cases := []struct {
		name   string
		values contactSubmission
		want   string
	}{
		{name: "unknown field", values: contactSubmission{"type": "privacy", "name": "Reporter", "email": "reporter@example.test", "form_token": "signed-token", "request_kind": "access", "subject": "Subject", "message": "Body", "support": "no"}, want: "unknown field"},
		{name: "header injection", values: contactSubmission{"type": "privacy", "name": "Reporter", "email": "reporter@example.test", "form_token": "signed-token", "request_kind": "access", "subject": "Subject\r\nBcc: victim@example.test", "message": "Body"}, want: "invalid header text"},
		{name: "invalid email", values: contactSubmission{"type": "privacy", "name": "Reporter", "email": "not an email", "form_token": "signed-token", "request_kind": "access", "subject": "Subject", "message": "Body"}, want: "email"},
		{name: "missing request kind", values: contactSubmission{"type": "privacy", "name": "Reporter", "email": "reporter@example.test", "form_token": "signed-token", "subject": "Subject", "message": "Body"}, want: "request_kind"},
		{name: "missing dmca statement", values: contactSubmission{"type": "dmca", "name": "Reporter", "email": "reporter@example.test", "form_token": "signed-token", "reported_url": "https://citylist.test/server/about", "copyrighted_work": "Work", "accuracy": "true", "signature": "Reporter", "subject": "Notice", "message": "Body"}, want: "good_faith"},
		{name: "invalid security severity", values: contactSubmission{"type": "security", "name": "Reporter", "email": "reporter@example.test", "form_token": "signed-token", "component": "API", "severity": "urgent", "summary": "Issue", "steps": "Steps", "impact": "Impact", "credit_preference": "anonymous", "coordinated_disclosure": "true"}, want: "severity"},
		{name: "security handle missing", values: contactSubmission{"type": "security", "name": "Reporter", "email": "reporter@example.test", "form_token": "signed-token", "component": "API", "severity": "high", "summary": "Issue", "steps": "Steps", "impact": "Impact", "credit_preference": "handle", "coordinated_disclosure": "true"}, want: "credit_handle"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := validateContactShape(test.values, contactFieldNameSet(test.values["type"]), true)
			if err == nil || err.Error() != test.want {
				t.Fatalf("validateContactShape() error = %v, want %q", err, test.want)
			}
		})
	}

	jsonRequest := httptest.NewRequest(http.MethodPost, "/api/v1/server/contact", strings.NewReader(`{"type":"privacy","name":"A","email":"a@example.test","request_kind":"export","subject":"Export","message":"Please export","form_token":"token","confirmed":true}`))
	jsonRequest.Header.Set("Content-Type", "application/json")
	parsed, err := parseContactSubmission(jsonRequest, true)
	if err != nil || parsed["confirmed"] != "true" {
		t.Fatalf("parse JSON contact submission = %v, %v", parsed, err)
	}

	formRequest := httptest.NewRequest(http.MethodPost, "/server/contact", strings.NewReader("type=dmca&good_faith=on&accuracy=on&confirmed=on"))
	formRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	parsed, err = parseContactSubmission(formRequest, false)
	if err != nil || parsed["good_faith"] != "true" || parsed["accuracy"] != "true" || parsed["confirmed"] != "true" {
		t.Fatalf("parse web form submission = %v, %v", parsed, err)
	}
	duplicate := httptest.NewRequest(http.MethodPost, "/server/contact", strings.NewReader("type=abuse&type=dmca"))
	duplicate.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := parseContactSubmission(duplicate, false); err == nil {
		t.Fatal("duplicate form values were accepted")
	}
	badJSON := httptest.NewRequest(http.MethodPost, "/api/v1/server/contact", strings.NewReader(`{"name":7}`))
	badJSON.Header.Set("Content-Type", "application/json")
	if _, err := parseContactSubmission(badJSON, true); err == nil {
		t.Fatal("non-string JSON form value was accepted")
	}
}

func TestContactFieldHashExcludesAntiSpamAndControlFields(t *testing.T) {
	original := contactSubmission{"name": "A", "email": "a@example.test", "message": "Hello", "type": "privacy", "form_token": "one", "captcha_answer": "3", "website": ""}
	changedControls := contactSubmission{"name": "A", "email": "a@example.test", "message": "Hello", "type": "privacy", "form_token": "two", "captcha_answer": "7", "website": "bot"}
	if contactFieldHash(original, "privacy", "") != contactFieldHash(changedControls, "privacy", "") {
		t.Fatal("anti-spam control fields changed the signed report content")
	}
	changedReport := contactSubmission{"name": "A", "email": "a@example.test", "message": "Changed", "type": "privacy", "form_token": "one"}
	if contactFieldHash(original, "privacy", "") == contactFieldHash(changedReport, "privacy", "") {
		t.Fatal("changing report content did not change its signed hash")
	}
}

func TestContactAPIDescriptionAndValidationErrors(t *testing.T) {
	s := testServer(t)
	response := doRequest(t, s, http.MethodGet, "/api/v1/server/contact?type=privacy", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("contact API description = %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data struct {
			Fields []map[string]any `json:"fields"`
			Types  []string         `json:"types"`
			Token  string           `json:"form_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Token == "" || len(body.Data.Fields) == 0 || len(body.Data.Types) != 3 {
		t.Fatalf("contact API description missing data: %+v", body.Data)
	}
	for _, field := range body.Data.Fields {
		if field["name"] == "reported_url" {
			t.Fatal("privacy form advertised a reported URL field")
		}
	}

	invalid := doRequest(t, s, http.MethodPost, "/api/v1/server/contact", []byte(`{"type":"support","name":"A","email":"a@example.test","form_token":"token"}`))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"field": "type"`) {
		t.Fatalf("unsupported contact type = %d: %s", invalid.Code, invalid.Body.String())
	}
	method := doRequest(t, s, http.MethodPut, "/api/v1/server/contact", nil)
	if method.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported contact API method = %d, want 405", method.Code)
	}
}

func TestContactAPIReviewConfirmDeliversOnceAndDropsDuplicates(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Pages.Contact.Antispam.MinFillSeconds = 0
	cfg.Server.Pages.Contact.Antispam.ReviewMinSeconds = 0
	cfg.Server.Contact.DPO.Email = "privacy@citylist.test"
	cfg.Server.Notifications.Email.From.Name = "Citylist"
	cfg.Server.Notifications.Email.From.Email = "no-reply@citylist.test"
	cfg.Server.Notifications.Email.SMTP.Host = "smtp.citylist.test"
	s := testServerWithConfig(t, cfg)

	sendCount := 0
	var delivered contactMailMessage
	previousSender := contactMailSender
	contactMailSender = func(_ context.Context, _ config.SMTPConfig, message contactMailMessage, _ string) error {
		sendCount++
		delivered = message
		return nil
	}
	t.Cleanup(func() { contactMailSender = previousSender })

	getForm := func() (string, string, string) {
		response := doRequest(t, s, http.MethodGet, "/api/v1/server/contact?type=privacy", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("GET contact API = %d: %s", response.Code, response.Body.String())
		}
		var result struct {
			Data struct {
				FormToken string         `json:"form_token"`
				Captcha   map[string]any `json:"captcha"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		question, _ := result.Data.Captcha["question"].(string)
		parts := strings.Fields(question)
		if len(parts) != 5 {
			t.Fatalf("unexpected simple CAPTCHA question %q", question)
		}
		left, err := strconv.Atoi(parts[2])
		if err != nil {
			t.Fatal(err)
		}
		right, err := strconv.Atoi(strings.TrimRight(parts[4], "?."))
		if err != nil {
			t.Fatal(err)
		}
		answer := left + right
		if parts[3] == "-" {
			answer = left - right
		}
		captchaID, _ := result.Data.Captcha["captcha_id"].(string)
		if result.Data.FormToken == "" || captchaID == "" {
			t.Fatal("contact API omitted its form token or simple CAPTCHA")
		}
		return result.Data.FormToken, captchaID, strconv.Itoa(answer)
	}

	email := "contact-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.test"
	values := contactSubmission{
		"type": "privacy", "name": "Reporter", "email": email, "request_kind": "access",
		"subject": "Privacy request", "message": "Please provide a copy of my personal information.",
	}
	probe := httptest.NewRequest(http.MethodPost, "https://citylist.test/api/v1/server/contact", nil)
	probe.RemoteAddr = "192.0.2.55:3456"
	if reason := s.validateContactContent(probe, "privacy", values); reason != "" {
		t.Fatalf("contact fixture fails content validation: %s", reason)
	}
	submit := func(formToken, captchaID, answer string) string {
		values["step"] = "submit"
		values["form_token"] = formToken
		values["captcha_id"] = captchaID
		values["captcha_answer"] = answer
		response := postContactJSON(t, s, values)
		if response.Code != http.StatusOK {
			t.Fatalf("contact submit = %d: %s", response.Code, response.Body.String())
		}
		var result struct {
			Data struct {
				ReviewToken string `json:"review_token"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Data.ReviewToken == "" {
			t.Fatal("contact submit did not return a review token")
		}
		return result.Data.ReviewToken
	}
	confirm := func(reviewToken string) *httptest.ResponseRecorder {
		values["step"] = "confirm"
		values["review_token"] = reviewToken
		values["confirmed"] = "true"
		return postContactJSON(t, s, values)
	}

	formToken, captchaID, answer := getForm()
	reviewToken := submit(formToken, captchaID, answer)
	if sendCount != 0 {
		t.Fatal("contact submission delivered before explicit confirmation")
	}
	confirmed := confirm(reviewToken)
	if confirmed.Code != http.StatusOK || sendCount != 1 {
		t.Fatalf("contact confirmation = %d, deliveries=%d: %s", confirmed.Code, sendCount, confirmed.Body.String())
	}
	if delivered.From != "no-reply@citylist.test" || delivered.ReplyTo != email || delivered.To != "privacy@citylist.test" || !strings.Contains(delivered.Text, values["message"]) {
		t.Fatalf("contact delivery headers or body mismatch: %+v", delivered)
	}

	formToken, captchaID, answer = getForm()
	reviewToken = submit(formToken, captchaID, answer)
	duplicate := confirm(reviewToken)
	if duplicate.Code != http.StatusOK || sendCount != 1 {
		t.Fatalf("duplicate confirmation = %d, deliveries=%d: %s", duplicate.Code, sendCount, duplicate.Body.String())
	}
}

func postContactJSON(t *testing.T, s *Server, values contactSubmission) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/server/contact", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.RemoteAddr = "192.0.2.55:3456"
	response := httptest.NewRecorder()
	s.Router().ServeHTTP(response, request)
	return response
}
