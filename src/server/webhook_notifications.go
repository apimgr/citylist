package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/notify"
)

func contactRoleConfig(cfg *config.Config, role string) *config.ContactRoleConfig {
	switch role {
	case "admin":
		return &cfg.Server.Contact.Admin
	case "security":
		return &cfg.Server.Contact.Security
	case "abuse":
		return &cfg.Server.Contact.Abuse
	case "general":
		return &cfg.Server.Contact.General
	default:
		return nil
	}
}

func webhookFallbackRoles(role string) []string {
	switch role {
	case "security":
		return []string{"security", "admin"}
	case "abuse":
		return []string{"abuse", "general", "admin"}
	case "general":
		return []string{"general", "admin"}
	default:
		return []string{"admin"}
	}
}

func webhookTargets(cfg *config.Config, role string) []notify.Target {
	targets := make([]notify.Target, 0)
	selected := map[string]bool{}
	for _, roleName := range webhookFallbackRoles(role) {
		roleConfig := contactRoleConfig(cfg, roleName)
		if roleConfig == nil {
			continue
		}
		transports := make([]string, 0, len(roleConfig.Webhooks))
		for transport := range roleConfig.Webhooks {
			transports = append(transports, transport)
		}
		sort.Strings(transports)
		for _, transport := range transports {
			if selected[transport] || strings.HasSuffix(transport, "_secret") {
				continue
			}
			endpoint := strings.TrimSpace(roleConfig.Webhooks[transport])
			secret := strings.TrimSpace(roleConfig.WebhookSecrets[transport])
			if endpoint == "" || secret == "" {
				continue
			}
			targets = append(targets, notify.Target{Role: role, Transport: transport, URL: endpoint, Secret: secret})
			selected[transport] = true
		}
	}
	return targets
}

func (s *Server) notifyRoleWebhooks(ctx context.Context, r *http.Request, role, eventName, subject, body, severity, trackingID string) error {
	targets := webhookTargets(s.cfg, role)
	if len(targets) == 0 {
		return nil
	}
	if r != nil && s.contactRequestOnPrivateNetwork(r) {
		slog.Warn("notify.webhook_skipped", "role", role, "reason", "outbound webhooks are unavailable for onion or I2P requests")
		return nil
	}
	appURL := ""
	if r != nil {
		appURL = s.BuildURL(r, "/")
	} else {
		fqdn := strings.TrimSuffix(strings.TrimSpace(s.cfg.Server.FQDN), ".")
		if fqdn != "" {
			scheme := "http"
			if s.cfg.Server.SSL.Enabled {
				scheme = "https"
			}
			appURL = scheme + "://" + fqdn + "/"
		}
	}
	return dispatchWebhooks(ctx, targets, notify.Event{
		Role: role, Name: eventName, Subject: subject, Body: body,
		Severity: normalizeNotificationSeverity(severity), Timestamp: time.Now().UTC().Truncate(time.Second),
		ProjectName: "citylist", ProjectVersion: s.version, AppURL: appURL, TrackingID: trackingID,
	})
}

func dispatchWebhooks(ctx context.Context, targets []notify.Target, event notify.Event) error {
	var failures []error
	for _, target := range targets {
		if err := notify.Dispatch(ctx, target, event); err != nil {
			failures = append(failures, fmt.Errorf("%s.%s: %w", target.Role, target.Transport, err))
		}
	}
	return errors.Join(failures...)
}

func normalizeNotificationSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical", "error", "high":
		return "critical"
	case "warning", "warn", "medium":
		return "warning"
	default:
		return "info"
	}
}
