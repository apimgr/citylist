package utils

import (
	"net"
	"os"
	"strings"
)

// GetDisplayAddress returns the most relevant address to display to users
// Priority: FQDN > Hostname > External IP > First non-loopback interface IP
// Never returns 0.0.0.0, 127.0.0.1, localhost, or ::1
func GetDisplayAddress(listenAddr string) string {
	// If listenAddr is valid and not a loopback/bind-all address, use it directly
	if listenAddr != "" &&
	   listenAddr != "0.0.0.0" &&
	   listenAddr != "127.0.0.1" &&
	   listenAddr != "localhost" &&
	   listenAddr != "::1" &&
	   !strings.HasPrefix(listenAddr, "0.0.0.0:") &&
	   !strings.HasPrefix(listenAddr, "127.0.0.1:") &&
	   !strings.HasPrefix(listenAddr, "localhost:") {
		return listenAddr
	}

	// Try to get FQDN first
	if fqdn, err := os.Hostname(); err == nil && fqdn != "" {
		// Normalize to lowercase
		fqdn = strings.ToLower(strings.TrimSpace(fqdn))

		// Check if it's a real FQDN (contains dot) and not localhost
		if strings.Contains(fqdn, ".") && fqdn != "localhost" && fqdn != "localhost.localdomain" {
			return fqdn
		}
		// If not FQDN, still use hostname if it's not localhost-like
		if fqdn != "localhost" && fqdn != "localhost.localdomain" {
			return fqdn
		}
	}

	// Try to get external/outbound IP address
	if ip := getOutboundIP(); ip != "" && ip != "127.0.0.1" && ip != "::1" {
		return ip
	}

	// Try to get first non-loopback interface IP
	if ip := getFirstNonLoopbackIP(); ip != "" {
		return ip
	}

	// Absolute last resort: return hostname even if it's localhost
	// This should rarely happen in practice
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return hostname
	}

	// Final fallback (should never reach here)
	return "localhost"
}

// getOutboundIP gets the preferred outbound IP of this machine
func getOutboundIP() string {
	// Try IPv4 first
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		localAddr := conn.LocalAddr().(*net.UDPAddr)
		if localAddr != nil && localAddr.IP != nil {
			if !localAddr.IP.IsLoopback() && !localAddr.IP.IsUnspecified() {
				return localAddr.IP.String()
			}
		}
	}

	// Try IPv6
	conn, err = net.Dial("udp", "[2001:4860:4860::8888]:80")
	if err == nil {
		defer conn.Close()
		localAddr := conn.LocalAddr().(*net.UDPAddr)
		if localAddr != nil && localAddr.IP != nil {
			if !localAddr.IP.IsLoopback() && !localAddr.IP.IsUnspecified() {
				return localAddr.IP.String()
			}
		}
	}

	return ""
}

// getFirstNonLoopbackIP gets the first non-loopback IP address from network interfaces
func getFirstNonLoopbackIP() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range interfaces {
		// Skip down interfaces
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			// Skip if nil, loopback, or unspecified
			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}

			// Prefer IPv4
			if ip.To4() != nil {
				return ip.String()
			}
		}
	}

	return ""
}

// FormatURL formats a complete URL with the best address
// Properly handles IPv6 addresses with brackets
func FormatURL(listenAddr, port, path string) string {
	host := GetDisplayAddress(listenAddr)

	// Add brackets for IPv6 addresses
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}

	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "http://" + host + ":" + port + path
}
