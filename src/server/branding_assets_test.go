package server

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/common/urlutil"
	"github.com/apimgr/citylist/src/config"
)

func TestBrandingImageRoutesSupportLocalAndDataSources(t *testing.T) {
	configDir := t.TempDir()
	assetsDir := filepath.Join(configDir, "assets")
	if err := os.MkdirAll(assetsDir, 0700); err != nil {
		t.Fatal(err)
	}
	localLogo := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><circle r="1"/></svg>`)
	if err := os.WriteFile(filepath.Join(assetsDir, "logo.svg"), localLogo, 0600); err != nil {
		t.Fatal(err)
	}
	dataIcon := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Logo = "logo.svg"
	cfg.Server.Branding.Favicon = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(dataIcon))
	cfg.Server.SEO.OGImage = "logo.svg"
	s := testServerWithConfig(t, cfg)
	s.SetConfigDir(configDir)

	for route, want := range map[string]string{
		"/branding/logo":     string(localLogo),
		"/branding/og-image": string(localLogo),
		"/favicon.ico":       dataIcon,
	} {
		response := httptest.NewRecorder()
		s.router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
		if response.Code != http.StatusOK || response.Body.String() != want {
			t.Errorf("GET %s = %d %q", route, response.Code, response.Body.String())
		}
	}
}

func TestBrandingImageInvalidPathUsesEmbeddedFallback(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Logo = "../outside.svg"
	s := testServerWithConfig(t, cfg)
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/branding/logo", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/svg+xml" || !bytes.Contains(response.Body.Bytes(), []byte("<svg")) {
		t.Fatalf("invalid local image response = %d %q", response.Code, response.Body.String())
	}
}

func TestBrandingImageDataURILimitsAndTypes(t *testing.T) {
	if _, err := decodeBrandingDataURI("data:text/html,hello"); err == nil {
		t.Fatal("unsupported data URI type was accepted")
	}
	large := "data:image/png;base64," + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), maxInlineBrandImage+1))
	if _, err := decodeBrandingDataURI(large); err == nil {
		t.Fatal("oversized data URI was accepted")
	}
}

func TestBrandingImageFaviconResize(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 8, 4))
	canvas.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	resized := resizeBrandingImage(brandingImage{data: encoded.Bytes(), contentType: "image/png"}, 16, 16, false)
	decoded, err := png.Decode(bytes.NewReader(resized.data))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 16 || decoded.Bounds().Dy() != 16 || resized.contentType != "image/png" {
		t.Fatalf("favicon size = %v, content type %q", decoded.Bounds(), resized.contentType)
	}
}

func TestRefreshBrandingImagesUsesCachedValueUntilRefreshIsDue(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Logo = "https://127.0.0.1/logo.png"
	s := testServerWithConfig(t, cfg)
	key := "logo\x00" + cfg.Server.Branding.Logo
	s.brandingImages = map[string]brandingImage{
		key: {data: []byte("cached"), contentType: "image/png", fetchedAt: time.Now().Add(-25 * time.Hour)},
	}
	if err := s.RefreshBrandingImages(t.Context()); err == nil {
		t.Fatal("unsafe URL should fail to refresh")
	}
	image, err := s.resolveBrandingImage("logo")
	if err != nil || string(image.data) != "cached" {
		t.Fatalf("failed refresh lost the previous cached image: %q, %v", image.data, err)
	}
}

func TestBrandingRemoteCachePersistsPrivatelyAcrossServerInstances(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Logo = "https://images.example.test/logo.png"
	cacheDir := t.TempDir()
	first := testServerWithConfig(t, cfg)
	first.SetCacheDir(cacheDir)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	image := brandingImage{data: encoded.Bytes(), contentType: "image/png", fetchedAt: time.Now()}
	if err := first.writeBrandingImageCache("logo", cfg.Server.Branding.Logo, image); err != nil {
		t.Fatal(err)
	}

	second := testServerWithConfig(t, cfg)
	second.SetCacheDir(cacheDir)
	cached, err := second.resolveBrandingImage("logo")
	if err != nil || !bytes.Equal(cached.data, image.data) {
		t.Fatalf("disk branding cache = %d bytes, %v", len(cached.data), err)
	}
	info, err := os.Stat(second.brandingImageCachePath("logo", cfg.Server.Branding.Logo))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("branding cache permissions = %v, %v; want 0600", info, err)
	}
	if err := second.RefreshBrandingImages(t.Context()); err != nil {
		t.Fatalf("fresh cached image triggered an unnecessary refresh: %v", err)
	}
}

func TestRemoteBrandingImageRejectsUnsafeURLs(t *testing.T) {
	for _, rawURL := range []string{"http://example.com/image.png", "https://localhost/image.png", "https://127.0.0.1/image.png", "https://user:pass@example.com/image.png"} {
		if _, _, err := urlutil.FetchRemoteImage(t.Context(), rawURL); err == nil {
			t.Errorf("unsafe remote image URL %q was accepted", rawURL)
		}
	}
}
