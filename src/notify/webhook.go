package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var retryDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}
var retrySlots = make(chan struct{}, 1000)
var failureLogMu sync.Mutex
var failureLogAt = map[string]time.Time{}

// Target is one configured notification destination.
type Target struct {
	Role      string
	Transport string
	URL       string
	Secret    string
}

// Event contains the summary shared with external notification destinations.
type Event struct {
	Role           string    `json:"role"`
	Name           string    `json:"event"`
	Subject        string    `json:"subject"`
	Body           string    `json:"body"`
	Severity       string    `json:"severity"`
	Timestamp      time.Time `json:"-"`
	ProjectName    string    `json:"project_name"`
	ProjectVersion string    `json:"project_version"`
	AppURL         string    `json:"app_url"`
	TrackingID     string    `json:"tracking_id,omitempty"`
}

type payload struct {
	Role           string `json:"role"`
	Event          string `json:"event"`
	Subject        string `json:"subject"`
	Body           string `json:"body"`
	Severity       string `json:"severity"`
	Timestamp      string `json:"timestamp"`
	ProjectName    string `json:"project_name"`
	ProjectVersion string `json:"project_version"`
	AppURL         string `json:"app_url"`
	TrackingID     string `json:"tracking_id,omitempty"`
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

var webhookHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Deliver sends one signed webhook attempt and returns only errors safe to log.
func Deliver(ctx context.Context, target Target, event Event, id string, now time.Time) (retErr error) {
	if strings.TrimSpace(target.URL) == "" || strings.TrimSpace(target.Secret) == "" || strings.TrimSpace(event.Name) == "" {
		return errors.New("webhook configuration is incomplete")
	}
	parsed, err := url.Parse(target.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("webhook URL must be a valid HTTPS URL without user information or a fragment")
	}
	if id == "" {
		generatedID, err := uuid.NewV7()
		if err != nil {
			return errors.New("webhook delivery ID generation failed")
		}
		id = generatedID.String()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = now.UTC().Truncate(time.Second)
	}
	body, contentType, destination, err := encodeDelivery(target.Transport, parsed, event)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(body))
	if err != nil {
		return errors.New("webhook request construction failed")
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-Webhook-Signature", signature(target.Secret, body))
	request.Header.Set("X-Webhook-Timestamp", fmt.Sprintf("%d", now.Unix()))
	request.Header.Set("X-Webhook-ID", id)
	request.Header.Set("X-Webhook-Event", event.Name)
	project := strings.Trim(strings.TrimSpace(event.ProjectName), "/")
	version := strings.TrimSpace(event.ProjectVersion)
	appURL := strings.TrimSpace(event.AppURL)
	request.Header.Set("User-Agent", project+"/"+version+" (+"+appURL+")")
	response, err := webhookHTTPClient.Do(request)
	if err != nil {
		return errors.New("webhook delivery failed")
	}
	defer func() { retErr = errors.Join(retErr, response.Body.Close()) }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New("webhook receiver returned a non-success status")
	}
	return nil
}

func encodeDelivery(transport string, target *url.URL, event Event) ([]byte, string, string, error) {
	message := strings.TrimSpace(event.Subject)
	if body := strings.TrimSpace(event.Body); body != "" {
		message += "\n" + body
	}
	switch strings.ToLower(transport) {
	case "telegram":
		query := target.Query()
		query.Set("text", message)
		target.RawQuery = query.Encode()
		return nil, "application/x-www-form-urlencoded", target.String(), nil
	case "discord":
		body, err := json.Marshal(map[string]string{"content": message, "username": event.ProjectName})
		return body, "application/json", target.String(), err
	case "slack", "mattermost":
		body, err := json.Marshal(map[string]string{"text": message})
		return body, "application/json", target.String(), err
	case "pushover":
		query := target.Query()
		values := url.Values{"title": {event.Subject}, "message": {event.Body}, "priority": {pushoverPriority(event.Severity)}}
		for _, key := range []string{"user", "token"} {
			if value := query.Get(key); value != "" {
				values.Set(key, value)
			} else {
				return nil, "", "", errors.New("pushover webhook URL must include user and token")
			}
		}
		return []byte(values.Encode()), "application/x-www-form-urlencoded", target.String(), nil
	case "gotify":
		query := target.Query()
		token := query.Get("token")
		if token == "" {
			return nil, "", "", errors.New("gotify webhook URL must include a token")
		}
		query.Del("token")
		target.Path = strings.TrimRight(target.Path, "/") + "/message"
		query.Set("token", token)
		target.RawQuery = query.Encode()
		body, err := json.Marshal(map[string]interface{}{"title": event.Subject, "message": event.Body, "priority": gotifyPriority(event.Severity)})
		return body, "application/json", target.String(), err
	case "generic":
		body, err := json.Marshal(payload{
			Role: event.Role, Event: event.Name, Subject: event.Subject, Body: event.Body,
			Severity: event.Severity, Timestamp: event.Timestamp.UTC().Truncate(time.Second).Format(time.RFC3339),
			ProjectName: event.ProjectName, ProjectVersion: event.ProjectVersion, AppURL: event.AppURL, TrackingID: event.TrackingID,
		})
		return body, "application/json", target.String(), err
	default:
		return nil, "", "", errors.New("webhook transport is not supported")
	}
}

func signature(secret string, body []byte) string {
	key := []byte(secret)
	if decoded, err := base64.StdEncoding.DecodeString(secret); err == nil && len(decoded) == 32 {
		key = decoded
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func pushoverPriority(severity string) string {
	if severity == "critical" {
		return "1"
	}
	return "0"
}

func gotifyPriority(severity string) int {
	switch severity {
	case "critical":
		return 8
	case "warning":
		return 5
	default:
		return 2
	}
}

// Dispatch starts the bounded retry sequence after the first failed attempt.
func Dispatch(ctx context.Context, target Target, event Event) error {
	id, err := uuid.NewV7()
	if err != nil {
		return errors.New("webhook delivery ID generation failed")
	}
	event.Timestamp = event.Timestamp.UTC().Truncate(time.Second)
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC().Truncate(time.Second)
	}
	err = Deliver(ctx, target, event, id.String(), time.Now().UTC())
	if err == nil {
		return nil
	}
	logFailure(target)
	select {
	case retrySlots <- struct{}{}:
		go retry(target, event, id.String())
	default:
		return errors.New("webhook retry queue is full")
	}
	return nil
}

func retry(target Target, event Event, id string) {
	defer func() { <-retrySlots }()
	for _, delay := range retryDelays {
		timer := time.NewTimer(delay)
		<-timer.C
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := Deliver(ctx, target, event, id, time.Now().UTC())
		cancel()
		if err == nil {
			return
		}
		logFailure(target)
	}
}

func logFailure(target Target) {
	key := target.Role + "." + target.Transport
	now := time.Now()
	failureLogMu.Lock()
	last := failureLogAt[key]
	if now.Sub(last) >= time.Minute {
		failureLogAt[key] = now
		failureLogMu.Unlock()
		slog.Error("notify.webhook_failed", "role", target.Role, "transport", target.Transport)
		return
	}
	failureLogMu.Unlock()
}
