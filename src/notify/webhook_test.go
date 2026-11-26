package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testSecret() string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
}

func response(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}
}

func TestDeliverSignsGenericPayload(t *testing.T) {
	previousClient := webhookHTTPClient
	t.Cleanup(func() { webhookHTTPClient = previousClient })
	var request *http.Request
	var body []byte
	webhookHTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		request = r
		body, _ = io.ReadAll(r.Body)
		return response(http.StatusAccepted), nil
	})}
	eventTime := time.Date(2026, 1, 15, 10, 30, 0, 500, time.FixedZone("offset", 3600))
	sendTime := time.Unix(1768469400, 0).UTC()
	event := Event{Role: "security", Name: "security.report_received", Subject: "Report received", Body: "Severity: critical", Severity: "critical", Timestamp: eventTime, ProjectName: "citylist", ProjectVersion: "1.2.3", AppURL: "https://city.example/", TrackingID: "report-123"}
	if err := Deliver(context.Background(), Target{Role: "security", Transport: "generic", URL: "https://hooks.example.test/report", Secret: testSecret()}, event, "019b89aa-7c00-7000-8000-000000000001", sendTime); err != nil {
		t.Fatalf("Deliver() error: %v", err)
	}
	if request == nil {
		t.Fatal("webhook request was not sent")
	}
	if got := request.Header.Get("X-Webhook-Signature"); got != signature(testSecret(), body) {
		t.Errorf("signature = %q, want matching HMAC", got)
	}
	if request.Header.Get("X-Webhook-ID") != "019b89aa-7c00-7000-8000-000000000001" || request.Header.Get("X-Webhook-Event") != event.Name {
		t.Errorf("webhook identity headers are incomplete: %+v", request.Header)
	}
	if request.Header.Get("X-Webhook-Timestamp") != "1768469400" || request.Header.Get("User-Agent") != "citylist/1.2.3 (+https://city.example/)" {
		t.Errorf("delivery metadata headers are incorrect: %+v", request.Header)
	}
	var got payload
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode generic payload: %v", err)
	}
	if got.Timestamp != "2026-01-15T09:30:00Z" || got.TrackingID != "report-123" || got.Body != event.Body {
		t.Errorf("generic payload = %+v", got)
	}
}

func TestTransportEncoders(t *testing.T) {
	base := Event{Role: "admin", Name: "backup.failed", Subject: "Backup failed", Body: "Details", Severity: "critical", Timestamp: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC), ProjectName: "citylist", ProjectVersion: "1.0", AppURL: "https://city.example"}
	for _, test := range []struct {
		name, rawURL string
		check        func(*testing.T, []byte, string)
	}{
		{"telegram", "https://api.telegram.org/bot123/sendMessage?chat_id=456", func(t *testing.T, body []byte, destination string) {
			parsed, err := url.Parse(destination)
			if err != nil || parsed.Query().Get("chat_id") != "456" || parsed.Query().Get("text") != "Backup failed\nDetails" || len(body) != 0 {
				t.Errorf("Telegram encoding = %q %q", destination, body)
			}
		}},
		{"discord", "https://discord.example.test/hook", func(t *testing.T, body []byte, _ string) {
			var value map[string]string
			if err := json.Unmarshal(body, &value); err != nil || value["content"] != "Backup failed\nDetails" || value["username"] != "citylist" {
				t.Errorf("Discord encoding = %s (%v)", body, err)
			}
		}},
		{"slack", "https://slack.example.test/hook", func(t *testing.T, body []byte, _ string) {
			var value map[string]string
			if err := json.Unmarshal(body, &value); err != nil || value["text"] != "Backup failed\nDetails" {
				t.Errorf("Slack encoding = %s (%v)", body, err)
			}
		}},
		{"mattermost", "https://mattermost.example.test/hook", func(t *testing.T, body []byte, _ string) {
			var value map[string]string
			if err := json.Unmarshal(body, &value); err != nil || value["text"] != "Backup failed\nDetails" {
				t.Errorf("Mattermost encoding = %s (%v)", body, err)
			}
		}},
		{"pushover", "https://api.pushover.net/1/messages.json?user=u&token=t", func(t *testing.T, body []byte, _ string) {
			values, err := url.ParseQuery(string(body))
			if err != nil || values.Get("user") != "u" || values.Get("token") != "t" || values.Get("priority") != "1" {
				t.Errorf("Pushover encoding = %q (%v)", body, err)
			}
		}},
		{"gotify", "https://gotify.example.test?token=secret", func(t *testing.T, body []byte, destination string) {
			parsed, err := url.Parse(destination)
			var value map[string]interface{}
			jsonErr := json.Unmarshal(body, &value)
			if err != nil || parsed.Path != "/message" || parsed.Query().Get("token") != "secret" || jsonErr != nil || value["priority"] != float64(8) {
				t.Errorf("Gotify encoding = %q %s (%v, %v)", destination, body, err, jsonErr)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, _, destination, err := encodeDelivery(test.name, mustURL(t, test.rawURL), base)
			if err != nil {
				t.Fatalf("encodeDelivery() error: %v", err)
			}
			test.check(t, body, destination)
		})
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestDeliverRejectsInsecureURLAndDoesNotFollowRedirects(t *testing.T) {
	target := Target{Role: "admin", Transport: "generic", URL: "http://hooks.example.test", Secret: testSecret()}
	if err := Deliver(context.Background(), target, Event{Name: "backup.failed"}, "id", time.Now()); err == nil {
		t.Fatal("HTTP webhook URL was accepted")
	}
	previousClient := webhookHTTPClient
	t.Cleanup(func() { webhookHTTPClient = previousClient })
	requests := 0
	webhookHTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return response(http.StatusFound), nil
	}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	target.URL = "https://hooks.example.test/redirect"
	if err := Deliver(context.Background(), target, Event{Name: "backup.failed"}, "id", time.Now()); err == nil {
		t.Fatal("redirect response was accepted")
	}
	if requests != 1 {
		t.Fatalf("redirect caused %d requests, want one", requests)
	}
}
