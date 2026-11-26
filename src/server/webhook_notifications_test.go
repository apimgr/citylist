package server

import (
	"testing"

	"github.com/apimgr/citylist/src/config"
)

func TestWebhookRoleFallbackUsesFirstConfiguredTransport(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Contact.Admin.Webhooks = map[string]string{
		"generic": "https://admin.example.test/generic",
		"slack":   "https://admin.example.test/slack",
	}
	cfg.Server.Contact.Admin.WebhookSecrets = map[string]string{"generic": "admin-generic", "slack": "admin-slack"}
	cfg.Server.Contact.General.Webhooks = map[string]string{"generic": "https://general.example.test/generic"}
	cfg.Server.Contact.General.WebhookSecrets = map[string]string{"generic": "general-generic"}
	cfg.Server.Contact.Abuse.Webhooks = map[string]string{"telegram": "https://telegram.example.test/hook"}
	cfg.Server.Contact.Abuse.WebhookSecrets = map[string]string{"telegram": "abuse-telegram"}

	targets := webhookTargets(cfg, "abuse")
	if len(targets) != 3 {
		t.Fatalf("abuse webhook targets = %+v, want abuse, general, and admin destinations", targets)
	}
	seen := map[string]string{}
	for _, target := range targets {
		seen[target.Transport] = target.URL
		if target.Role != "abuse" {
			t.Errorf("fallback target role = %q, want requested role abuse", target.Role)
		}
	}
	if seen["telegram"] != "https://telegram.example.test/hook" || seen["generic"] != "https://general.example.test/generic" || seen["slack"] != "https://admin.example.test/slack" {
		t.Errorf("resolved webhook targets = %v", seen)
	}
}
