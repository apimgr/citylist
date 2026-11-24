package server

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/config"
)

var smtpProbeTimeout = 900 * time.Millisecond

// InitializeSMTP selects and checks the configured relay before the server starts.
func InitializeSMTP(cfg *config.Config, configPath string) error {
	smtpConfig := &cfg.Server.Notifications.Email.SMTP
	if strings.TrimSpace(smtpConfig.Host) != "" {
		if smtpHandshake(smtpConfig.Host, smtpConfig.Port, smtpConfig.TLS) {
			return nil
		}
		slog.Warn("smtp.disabled", "reason", "configured server failed connection test", "host", smtpConfig.Host, "port", smtpConfig.Port)
		smtpConfig.Host = ""
		return nil
	}

	fqdn := strings.TrimSuffix(strings.TrimSpace(cfg.Server.FQDN), ".")
	if fqdn == "" {
		fqdn, _ = os.Hostname()
	}
	hosts := smtpCandidateHosts(fqdn)
	for _, host := range hosts {
		for _, port := range []int{25, 465, 587} {
			if !smtpHandshake(host, port, "auto") {
				continue
			}
			smtpConfig.Host = host
			smtpConfig.Port = port
			smtpConfig.TLS = "auto"
			if err := config.Save(configPath, cfg); err != nil {
				smtpConfig.Host = ""
				return fmt.Errorf("persist detected SMTP relay: %w", err)
			}
			slog.Info("smtp.autodetected", "host", host, "port", port)
			return nil
		}
	}
	slog.Info("smtp.disabled", "reason", "no working SMTP relay detected")
	return nil
}

func smtpCandidateHosts(fqdn string) []string {
	candidates := []string{"127.0.0.1", "172.17.0.1"}
	if gateway := defaultGatewayIPv4(); gateway != "" {
		candidates = append(candidates, gateway)
	}
	if fqdn != "" {
		candidates = append(candidates, fqdn)
	}
	if address := globalIPv4(); address != "" {
		candidates = append(candidates, address)
	}
	if fqdn != "" {
		candidates = append(candidates, "mail."+fqdn, "smtp."+fqdn)
	}
	seen := make(map[string]bool, len(candidates))
	result := make([]string, 0, len(candidates))
	for _, host := range candidates {
		host = strings.TrimSpace(strings.Trim(host, "[]"))
		if host != "" && !seen[strings.ToLower(host)] {
			seen[strings.ToLower(host)] = true
			result = append(result, host)
		}
	}
	return result
}

func smtpHandshake(host string, port int, tlsMode string) bool {
	if port <= 0 || port > 65535 || strings.TrimSpace(host) == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), smtpProbeTimeout)
	defer cancel()
	connection, err := (&net.Dialer{Timeout: smtpProbeTimeout}).DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return false
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(smtpProbeTimeout))
	mode := strings.ToLower(strings.TrimSpace(tlsMode))
	implicitTLS := mode == "tls" || (mode == "auto" || mode == "") && port == 465
	if implicitTLS {
		secure := tls.Client(connection, contactTLSConfig(host))
		if secure.HandshakeContext(ctx) != nil {
			return false
		}
		connection = secure
	}
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		return false
	}
	defer func() { _ = client.Close() }()
	if !implicitTLS && mode != "none" {
		supported, _ := client.Extension("STARTTLS")
		if supported {
			if client.StartTLS(contactTLSConfig(host)) != nil {
				return false
			}
		} else if mode == "starttls" {
			return false
		}
	}
	if mode != "" && mode != "auto" && mode != "none" && mode != "tls" && mode != "starttls" {
		return false
	}
	return true
}

func globalIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, networkInterface := range interfaces {
		addresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr()
			if ip.Is4() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
				return ip.String()
			}
		}
	}
	return ""
}

func defaultGatewayIPv4() string {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		decoded, err := hex.DecodeString(fields[2])
		if err != nil || len(decoded) != 4 {
			continue
		}
		return net.IPv4(decoded[3], decoded[2], decoded[1], decoded[0]).String()
	}
	return ""
}
