package server

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/notify"
)

// NotifyOperator dispatches an operator event through configured email and webhook channels.
func NotifyOperator(ctx context.Context, cfg *config.Config, event string, details map[string]string) error {
	return NotifyOperatorWithVersion(ctx, cfg, event, details, "unknown")
}

// NotifyOperatorWithVersion adds the running binary version to webhook metadata.
func NotifyOperatorWithVersion(ctx context.Context, cfg *config.Config, event string, details map[string]string, version string) error {
	if cfg == nil || strings.TrimSpace(event) == "" {
		return errors.New("operator notification requires configuration and an event")
	}
	from := cfg.Server.Notifications.Email.From
	fromAddress := strings.TrimSpace(from.Email)
	fqdn := strings.TrimSuffix(strings.TrimSpace(cfg.Server.FQDN), ".")
	if fqdn == "" {
		fqdn, _ = os.Hostname()
	}
	if fromAddress == "" {
		fromAddress = "no-reply@" + fqdn
	}
	lang := strings.TrimSpace(cfg.Server.I18n.DefaultLanguage)
	if !i18n.IsSupported(lang) {
		lang = "en"
	}
	appName := strings.TrimSpace(cfg.Server.Branding.Title)
	if appName == "" {
		appName = "citylist"
	}
	scheme := "http"
	if cfg.Server.SSL.Enabled {
		scheme = "https"
	}
	appURL := scheme + "://" + fqdn
	eventLabel := i18n.Translate(lang, "email.events."+event)
	if eventLabel == "email.events."+event {
		eventLabel = strings.ReplaceAll(event, "_", " ")
	}
	lines := make([]string, 0, len(details))
	keys := make([]string, 0, len(details))
	for key := range details {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := strings.NewReplacer("\r", " ", "\n", " ").Replace(details[key])
		label := i18n.Translate(lang, "email.fields."+key)
		if label == "email.fields."+key {
			label = key
		}
		lines = append(lines, fmt.Sprintf("%s: %s", label, value))
	}
	values := map[string]string{
		"app_name":  appName,
		"fqdn":      fqdn,
		"app_url":   appURL,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"event":     eventLabel,
		"details":   strings.Join(lines, "\r\n"),
	}
	body := i18n.TranslateFormat(lang, "email.body.operator_event", values)
	if len(lines) == 0 {
		body = i18n.TranslateFormat(lang, "email.body.operator_event_empty", values)
	}
	recipient := strings.TrimSpace(cfg.Server.Contact.Admin.Email)
	message := contactMailMessage{
		FromName: firstNonEmpty(from.Name, appName),
		From:     fromAddress,
		To:       recipient,
		Subject:  i18n.TranslateFormat(lang, "email.subjects.operator_event", map[string]string{"app_name": appName, "event": eventLabel}),
		Text:     body + "\r\n",
	}
	var failures []error
	if cfg.Server.Notifications.Email.Events[event] && strings.TrimSpace(cfg.Server.Notifications.Email.SMTP.Host) != "" {
		if _, err := mail.ParseAddress(recipient); err != nil {
			failures = append(failures, errors.New("operator notification recipient is not configured"))
		} else if strings.ContainsAny(fromAddress+from.Name+event, "\r\n") {
			failures = append(failures, errors.New("operator notification contains an unsafe mail header"))
		} else if _, err := mail.ParseAddress(fromAddress); err != nil {
			failures = append(failures, errors.New("operator notification sender is invalid"))
		} else {
			sendContext, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := contactMailSender(sendContext, cfg.Server.Notifications.Email.SMTP, message, fqdn); err != nil {
				failures = append(failures, err)
			}
			cancel()
		}
	}
	severity := "info"
	if strings.Contains(strings.ToLower(event), "panic") || strings.Contains(strings.ToLower(event), "fail") || strings.Contains(strings.ToLower(event), "error") {
		severity = "critical"
	} else if strings.Contains(strings.ToLower(event), "warn") || strings.Contains(strings.ToLower(event), "expir") {
		severity = "warning"
	}
	appURL = ""
	if fqdn != "" {
		scheme := "http"
		if cfg.Server.SSL.Enabled {
			scheme = "https"
		}
		appURL = scheme + "://" + fqdn + "/"
	}
	eventName := strings.ReplaceAll(event, "_", ".")
	if err := dispatchWebhooks(ctx, webhookTargets(cfg, "admin"), notify.Event{
		Role: "admin", Name: eventName, Subject: message.Subject, Body: body,
		Severity: severity, Timestamp: time.Now().UTC().Truncate(time.Second),
		ProjectName: "citylist", ProjectVersion: version, AppURL: appURL,
	}); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
