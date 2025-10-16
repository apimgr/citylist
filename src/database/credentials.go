package database

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SaveCredentialsToFile writes admin credentials to a file with secure permissions
// IMPORTANT: port parameter must be provided for accurate URL generation
func SaveCredentialsToFile(username, password, token, configDir, port string) error {
	filePath := filepath.Join(configDir, "admin_credentials")

	// Get the best accessible URL (never shows localhost/127.0.0.1/0.0.0.0)
	serverURL := getAccessibleURL(port)

	content := fmt.Sprintf(`==============================================================
🔐 CityList API - Admin Credentials
==============================================================
WEB UI LOGIN:
  URL:      %s/admin
  Username: %s

API ACCESS:
  URL:      %s/api/v1/admin
  Header:   Authorization: Bearer %s

CREDENTIALS:
  Username: %s
  Password: %s
  Token:    %s

Created: %s
==============================================================
⚠️  Keep these credentials secure!
⚠️  They will not be shown again after this initial setup.
==============================================================`,
		serverURL, username,
		serverURL, token,
		username, password, token,
		time.Now().Format("2006-01-02 15:04:05"))

	// Write with 0600 permissions (owner read/write only)
	err := os.WriteFile(filePath, []byte(content), 0600)
	if err != nil {
		return fmt.Errorf("failed to write credentials file: %w", err)
	}

	return nil
}

// getAccessibleURL returns the most relevant URL for accessing the server
// Priority: FQDN > hostname > public IP > fallback
// NEVER shows localhost, 127.0.0.1, 0.0.0.0, ::, or ::1
func getAccessibleURL(port string) string {
	// Try hostname resolution (FQDN)
	hostname, err := os.Hostname()
	if err == nil && hostname != "" && hostname != "localhost" {
		// Try to resolve hostname to see if it's accessible
		if addrs, err := net.LookupHost(hostname); err == nil && len(addrs) > 0 {
			// Check if it's a real external address (not loopback)
			for _, addr := range addrs {
				ip := net.ParseIP(addr)
				if ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
					// Valid FQDN with external IP - use hostname
					return formatURLWithHost(hostname, port)
				}
			}
		}
	}

	// Try to get outbound IP (most likely accessible IP)
	if ip := getOutboundIP(); ip != "" {
		return formatURLWithHost(ip, port)
	}

	// Fallback to hostname if we have one (even if not fully resolvable)
	if hostname != "" && hostname != "localhost" {
		return formatURLWithHost(hostname, port)
	}

	// Last resort: use a generic placeholder
	return fmt.Sprintf("http://<your-host>:%s", port)
}

// getOutboundIP gets the preferred outbound IP of this machine
// Tries IPv4 first, then IPv6
func getOutboundIP() string {
	// Try IPv4 first (connect to Google DNS)
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		localAddr := conn.LocalAddr().(*net.UDPAddr)
		if localAddr != nil && localAddr.IP != nil {
			ip := localAddr.IP.String()
			// Exclude loopback and unspecified addresses
			if ip != "127.0.0.1" && ip != "0.0.0.0" {
				return ip
			}
		}
	}

	// Try IPv6 (connect to Google DNS IPv6)
	conn, err = net.Dial("udp", "[2001:4860:4860::8888]:80")
	if err == nil {
		defer conn.Close()
		localAddr := conn.LocalAddr().(*net.UDPAddr)
		if localAddr != nil && localAddr.IP != nil {
			ip := localAddr.IP.String()
			// Exclude loopback and unspecified addresses
			if ip != "::1" && ip != "::" {
				return ip
			}
		}
	}

	return ""
}

// formatURLWithHost formats a URL with proper IPv6 bracket handling
func formatURLWithHost(host, port string) string {
	// Add brackets for IPv6 addresses
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return fmt.Sprintf("http://[%s]:%s", host, port)
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}
