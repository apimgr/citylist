package urlutil

import (
	"context"
	"net"
	"testing"
)

func TestFetchRemoteImageRejectsInvalidURLsBeforeNetworking(t *testing.T) {
	for _, rawURL := range []string{"", "://bad", "http://example.com/image.png", "https://user:pass@example.com/image.png"} {
		if _, _, err := FetchRemoteImage(context.Background(), rawURL); err == nil {
			t.Errorf("FetchRemoteImage(%q) succeeded", rawURL)
		}
	}
}

func TestValidateRemoteURLRejectsLocalAndMalformedHosts(t *testing.T) {
	for _, rawURL := range []string{"https:///image.png", "https://localhost/image.png", "https://printer.local/image.png", "https://service.internal/image.png"} {
		if _, err := validateRemoteURL(rawURL); err == nil {
			t.Errorf("validateRemoteURL(%q) succeeded", rawURL)
		}
	}
	if _, err := validateRemoteURL("https://8.8.8.8/image.png"); err != nil {
		t.Fatalf("public IP URL should pass validation: %v", err)
	}
}

func TestFetchRemoteImageHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := FetchRemoteImage(ctx, "https://8.8.8.8/image.png"); err == nil {
		t.Fatal("FetchRemoteImage succeeded with a canceled context")
	}
}

func TestPublicAddressRejectsNonPublicRanges(t *testing.T) {
	for _, rawIP := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "::1", "ff02::1", "0.0.0.0"} {
		if publicAddress(net.ParseIP(rawIP)) {
			t.Errorf("publicAddress(%q) = true", rawIP)
		}
	}
	for _, rawIP := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !publicAddress(net.ParseIP(rawIP)) {
			t.Errorf("publicAddress(%q) = false", rawIP)
		}
	}
}
