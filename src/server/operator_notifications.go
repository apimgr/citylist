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
)

// NotifyOperator sends an enabled operator event through the configured SMTP
// channel. Events remain log-only when SMTP or the event switch is disabled.
func NotifyOperator(ctx context.Context, cfg *config.Config, event string, details map[string]string) error {
	if cfg == nil || strings.TrimSpace(event) == "" {
		return errors.New("operator notification requires configuration and an event")
	}
	if !cfg.Server.Notifications.Email.Events[event] || strings.TrimSpace(cfg.Server.Notifications.Email.SMTP.Host) == "" {
		return nil
	}
	recipient := strings.TrimSpace(cfg.Server.Contact.Admin.Email)
	if _, err := mail.ParseAddress(recipient); err != nil {
		return errors.New("operator notification recipient is not configured")
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
	if strings.ContainsAny(fromAddress+from.Name+event, "\r\n") {
		return errors.New("operator notification contains an unsafe mail header")
	}
	if _, err := mail.ParseAddress(fromAddress); err != nil {
		return errors.New("operator notification sender is invalid")
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
	message := contactMailMessage{
		FromName: firstNonEmpty(from.Name, appName),
		From:     fromAddress,
		To:       recipient,
		Subject:  i18n.TranslateFormat(lang, "email.subjects.operator_event", map[string]string{"app_name": appName, "event": eventLabel}),
		Text:     body + "\r\n",
	}
	sendContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return contactMailSender(sendContext, cfg.Server.Notifications.Email.SMTP, message, fqdn)
}
