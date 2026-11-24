package securitydata

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/config"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

const fireholLevel1URL = "https://raw.githubusercontent.com/firehol/blocklist-ipsets/master/firehol_level1.netset"

var blocklistIDPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

type blocklistSource struct {
	id     string
	typeID string
	url    string
}

// UpdateBlocklists downloads configured lists and publishes each validated source atomically.
func UpdateBlocklists(ctx context.Context, directory string, blocklists config.SecurityBlocklistsConfig, trusted []string, client *http.Client) error {
	if !blocklists.Enabled {
		return nil
	}
	sources := []blocklistSource{{id: "firehol_level1", typeID: "ip", url: fireholLevel1URL}}
	seenIDs := map[string]struct{}{"firehol_level1": {}}
	for _, extra := range blocklists.ExtraSources {
		if extra.Enabled != nil && !*extra.Enabled {
			continue
		}
		id := strings.TrimSpace(extra.ID)
		typeID := strings.ToLower(strings.TrimSpace(extra.Type))
		if !blocklistIDPattern.MatchString(id) || id == "firehol_level1" {
			slog.Warn("securitydata.blocklist_source_skipped", "reason", "invalid or reserved id")
			continue
		}
		if _, duplicate := seenIDs[id]; duplicate {
			slog.Warn("securitydata.blocklist_source_skipped", "id", id, "reason", "duplicate id")
			continue
		}
		seenIDs[id] = struct{}{}
		if typeID != "ip" && typeID != "domain" {
			slog.Warn("securitydata.blocklist_source_skipped", "id", id, "reason", "unsupported type")
			continue
		}
		sources = append(sources, blocklistSource{id: id, typeID: typeID, url: strings.TrimSpace(extra.URL)})
	}
	trustedPrefixes := parsePrefixes(trusted)
	if err := os.MkdirAll(directory, 0750); err != nil {
		return fmt.Errorf("create blocklist directory: %w", err)
	}
	for _, source := range sources {
		body, err := fetch(ctx, client, source.url, 64<<20)
		if err != nil {
			return fmt.Errorf("download blocklist %s: %w", source.id, err)
		}
		entries, skipped := canonicalBlocklistEntries(string(body), source.typeID, trustedPrefixes)
		if skipped > 0 {
			slog.Warn("securitydata.blocklist_entries_skipped", "id", source.id, "count", skipped)
		}
		if len(entries) == 0 {
			return fmt.Errorf("blocklist %s has no valid entries (%d skipped)", source.id, skipped)
		}
		content := []byte(strings.Join(entries, "\n") + "\n")
		if err := writeAtomically(directory, source.id+".txt", content, 0640); err != nil {
			return fmt.Errorf("publish blocklist %s: %w", source.id, err)
		}
	}
	if err := writeLastUpdated(directory, time.Now()); err != nil {
		return fmt.Errorf("record blocklist update time: %w", err)
	}
	return nil
}

func canonicalBlocklistEntries(text, typeID string, trusted []netip.Prefix) ([]string, int) {
	seen := make(map[string]struct{})
	entries := make([]string, 0)
	skipped := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if cut := strings.IndexAny(line, " \t;"); cut >= 0 {
			line = line[:cut]
		}
		var value string
		var ok bool
		if typeID == "ip" {
			value, ok = normalizeIPEntry(line, trusted)
		} else {
			value, ok = normalizeDomainEntry(line)
		}
		if !ok {
			skipped++
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		entries = append(entries, value)
	}
	sort.Strings(entries)
	return entries, skipped
}

func normalizeIPEntry(value string, trusted []netip.Prefix) (string, bool) {
	var prefix netip.Prefix
	if parsed, err := netip.ParsePrefix(value); err == nil {
		prefix = parsed.Masked()
	} else if address, err := netip.ParseAddr(value); err == nil {
		prefix = netip.PrefixFrom(address, address.BitLen())
	} else {
		return "", false
	}
	address := prefix.Addr()
	if address.Is4In6() {
		if prefix.Bits() < 96 {
			return "", false
		}
		prefix = netip.PrefixFrom(address.Unmap(), prefix.Bits()-96).Masked()
	}
	if overlapsExcluded(prefix) {
		return "", false
	}
	for _, proxy := range trusted {
		if prefixesOverlap(prefix, proxy) {
			return "", false
		}
	}
	return prefix.String(), true
}

func overlapsExcluded(candidate netip.Prefix) bool {
	for _, raw := range []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7", "fe80::/10"} {
		if excluded, err := netip.ParsePrefix(raw); err == nil && prefixesOverlap(candidate, excluded) {
			return true
		}
	}
	return false
}

func prefixesOverlap(left, right netip.Prefix) bool {
	return left.Addr().Is4() == right.Addr().Is4() && (left.Contains(right.Addr()) || right.Contains(left.Addr()))
}

func parsePrefixes(values []string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(value)); err == nil {
			prefixes = append(prefixes, prefix.Masked())
		} else if address, err := netip.ParseAddr(strings.TrimSpace(value)); err == nil {
			prefixes = append(prefixes, netip.PrefixFrom(address, address.BitLen()))
		}
	}
	return prefixes
}

func normalizeDomainEntry(value string) (string, bool) {
	wildcard := strings.HasPrefix(value, "*.")
	if wildcard {
		value = strings.TrimPrefix(value, "*.")
	}
	value = strings.TrimSpace(strings.ToLower(value))
	if at := strings.LastIndex(value, "@"); at >= 0 {
		value = value[at+1:]
	}
	if strings.ContainsAny(value, "/?#[]") {
		return "", false
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.TrimSuffix(value, ".")
	if value == "" {
		return "", false
	}
	if parsed, err := url.Parse("https://" + value); err != nil || parsed.Hostname() == "" {
		return "", false
	} else {
		value = parsed.Hostname()
	}
	if address, err := netip.ParseAddr(value); err == nil {
		_ = address
		return "", false
	}
	ascii, err := idna.Lookup.ToASCII(value)
	if err != nil || len(ascii) > 253 {
		return "", false
	}
	for _, label := range strings.Split(ascii, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
	}
	if wildcard {
		publicSuffix, _ := publicsuffix.PublicSuffix(ascii)
		if publicSuffix == ascii {
			return "", false
		}
		return "*." + ascii, true
	}
	return ascii, true
}
