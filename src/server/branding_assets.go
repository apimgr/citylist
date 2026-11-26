package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/common/urlutil"
	"github.com/go-chi/chi/v5"
)

const (
	maxLocalBrandImageSize = 2 << 20
	maxInlineBrandImage    = 256 << 10
	brandingCacheLifetime  = 24 * time.Hour
)

type brandingImage struct {
	data        []byte
	contentType string
	fetchedAt   time.Time
}

type brandingImageCacheRecord struct {
	ContentType string    `json:"content_type"`
	FetchedAt   time.Time `json:"fetched_at"`
	Data        []byte    `json:"data"`
}

var brandingImageTypes = map[string]bool{
	"image/svg+xml": true, "image/png": true, "image/jpeg": true,
	"image/webp": true, "image/x-icon": true, "image/vnd.microsoft.icon": true,
}

func (s *Server) imageSource(name string) string {
	switch name {
	case "logo":
		return strings.TrimSpace(s.cfg.Server.Branding.Logo)
	case "favicon":
		return strings.TrimSpace(s.cfg.Server.Branding.Favicon)
	case "og-image":
		return strings.TrimSpace(s.cfg.Server.SEO.OGImage)
	default:
		return ""
	}
}

func (s *Server) imageURL(name string, r *http.Request) string {
	if s.imageSource(name) == "" {
		return s.BuildURL(r, "/static/images/icon.svg")
	}
	return s.BuildURL(r, "/branding/"+name)
}

func (s *Server) handleBrandingImage(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	s.serveBrandingImage(w, r, name)
}

func (s *Server) serveBrandingImage(w http.ResponseWriter, r *http.Request, name string) {
	if name != "logo" && name != "favicon" && name != "og-image" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	image, err := s.resolveBrandingImage(name)
	if err != nil {
		configKey := map[string]string{"logo": "server.branding.logo", "favicon": "server.branding.favicon", "og-image": "server.seo.og_image"}[name]
		s.logBrandingImageFailure(configKey, s.imageSource(name))
		image, err = s.embeddedBrandingImage()
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	image = s.scaledBrandingImage(name, image, r.URL.Query().Get("size"))
	w.Header().Set("Content-Type", image.contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	digest := sha256.Sum256(image.data)
	w.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(image.data)
}

func (s *Server) scaledBrandingImage(name string, source brandingImage, size string) brandingImage {
	key := ""
	resize := func() brandingImage { return source }
	switch {
	case name == "og-image" && size == "1200x630":
		key = name + ":" + size
		resize = func() brandingImage { return resizeBrandingImage(source, 1200, 630, true) }
	case name == "logo" && (size == "200" || size == "50"):
		key = name + ":" + size
		width := 200
		if size == "50" {
			width = 50
		}
		resize = func() brandingImage { return resizeBrandingWidth(source, width) }
	case name == "favicon":
		if dimension := map[string]int{"16": 16, "32": 32, "48": 48, "180": 180, "192": 192, "512": 512}[size]; dimension > 0 {
			key = name + ":" + size
			resize = func() brandingImage { return resizeBrandingImage(source, dimension, dimension, false) }
		}
	}
	if key == "" {
		return source
	}
	digest := sha256.Sum256(source.data)
	key += ":" + hex.EncodeToString(digest[:])
	s.brandingImageMu.RLock()
	cached, ok := s.brandingVariants[key]
	s.brandingImageMu.RUnlock()
	if ok {
		return cached
	}
	cached = resize()
	s.brandingImageMu.Lock()
	if s.brandingVariants == nil {
		s.brandingVariants = make(map[string]brandingImage)
	}
	if len(s.brandingVariants) >= 24 {
		s.brandingVariants = make(map[string]brandingImage)
	}
	s.brandingVariants[key] = cached
	s.brandingImageMu.Unlock()
	return cached
}

func (s *Server) resolveBrandingImage(name string) (brandingImage, error) {
	value := s.imageSource(name)
	if value == "" {
		return s.embeddedBrandingImage()
	}
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		return decodeBrandingDataURI(value)
	}
	if strings.HasPrefix(strings.ToLower(value), "https://") {
		return s.cachedBrandingImage(name, value)
	}
	return s.readLocalBrandingImage(value)
}

func (s *Server) readLocalBrandingImage(value string) (result brandingImage, retErr error) {
	if strings.ContainsAny(value, `/\\`) || filepath.Base(value) != value || value == "." || value == ".." {
		return brandingImage{}, fmt.Errorf("branding image must be a file name without a path")
	}
	extension := strings.ToLower(filepath.Ext(value))
	contentType := map[string]string{".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon", ".webp": "image/webp", ".jpg": "image/jpeg"}[extension]
	if contentType == "" {
		return brandingImage{}, fmt.Errorf("branding image extension is not supported")
	}
	if s.configDir == "" {
		return brandingImage{}, fmt.Errorf("branding image directory is not configured")
	}
	assetsDir := filepath.Join(s.configDir, "assets")
	directory, err := os.Lstat(assetsDir)
	if err != nil || !directory.IsDir() || directory.Mode().Perm()&0077 != 0 {
		return brandingImage{}, fmt.Errorf("branding image directory must be a private directory")
	}
	path := filepath.Join(assetsDir, value)
	before, err := os.Lstat(path)
	if err != nil {
		return brandingImage{}, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() <= 0 || before.Size() > maxLocalBrandImageSize {
		return brandingImage{}, fmt.Errorf("branding image must be a private regular file no larger than 2 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return brandingImage{}, err
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return brandingImage{}, fmt.Errorf("branding image changed while it was being opened")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxLocalBrandImageSize+1))
	if err != nil {
		return brandingImage{}, err
	}
	if len(data) == 0 || len(data) > maxLocalBrandImageSize {
		return brandingImage{}, fmt.Errorf("branding image is empty or exceeds 2 MiB")
	}
	image := brandingImage{data: data, contentType: contentType}
	if err := validateBrandingImage(image); err != nil {
		return brandingImage{}, err
	}
	return image, nil
}

func decodeBrandingDataURI(value string) (brandingImage, error) {
	comma := strings.IndexByte(value, ',')
	if comma < 0 {
		return brandingImage{}, fmt.Errorf("invalid branding image data URI")
	}
	header, payload := value[5:comma], value[comma+1:]
	fields := strings.Split(header, ";")
	contentType := strings.ToLower(strings.TrimSpace(fields[0]))
	if !brandingImageTypes[contentType] {
		return brandingImage{}, fmt.Errorf("branding image data URI type is not allowed")
	}
	var data []byte
	var err error
	if len(fields) > 1 && strings.EqualFold(strings.TrimSpace(fields[len(fields)-1]), "base64") {
		data, err = base64.StdEncoding.DecodeString(payload)
	} else {
		var decoded string
		decoded, err = url.PathUnescape(payload)
		data = []byte(decoded)
	}
	if err != nil || len(data) == 0 || len(data) > maxInlineBrandImage {
		return brandingImage{}, fmt.Errorf("branding image data URI is invalid or exceeds 256 KiB")
	}
	image := brandingImage{data: data, contentType: contentType}
	if err := validateBrandingImage(image); err != nil {
		return brandingImage{}, err
	}
	return image, nil
}

func (s *Server) cachedBrandingImage(name, source string) (brandingImage, error) {
	key := name + "\x00" + source
	s.brandingImageMu.Lock()
	cached := s.brandingImages[key]
	s.brandingImageMu.Unlock()
	if len(cached.data) > 0 {
		return cached, nil
	}
	cached, err := s.readBrandingImageCache(name, source)
	if err != nil {
		return brandingImage{}, err
	}
	s.brandingImageMu.Lock()
	if s.brandingImages == nil {
		s.brandingImages = make(map[string]brandingImage)
	}
	s.brandingImages[key] = cached
	s.brandingImageMu.Unlock()
	return cached, nil
}

// RefreshBrandingImages fetches configured remote images from the background
// scheduler, never from a visitor's page request.
func (s *Server) RefreshBrandingImages(ctx context.Context) error {
	var failures []error
	for _, name := range []string{"logo", "favicon", "og-image"} {
		source := s.imageSource(name)
		if !strings.HasPrefix(strings.ToLower(source), "https://") {
			continue
		}
		key := name + "\x00" + source
		if cached, err := s.cachedBrandingImage(name, source); err == nil && time.Since(cached.fetchedAt) < brandingCacheLifetime {
			continue
		}
		data, contentType, err := urlutil.FetchRemoteImage(ctx, source)
		var cached brandingImage
		if err == nil {
			cached = brandingImage{data: data, contentType: contentType, fetchedAt: time.Now()}
			err = validateBrandingImage(cached)
		}
		if err != nil {
			s.logBrandingImageFailure(map[string]string{"logo": "server.branding.logo", "favicon": "server.branding.favicon", "og-image": "server.seo.og_image"}[name], source)
			failures = append(failures, fmt.Errorf("refresh %s: remote image could not be fetched or validated", name))
			continue
		}
		if err := s.writeBrandingImageCache(name, source, cached); err != nil {
			slog.Warn("server.branding.cache_write_failed", "image", name, "error", err)
		}
		s.brandingImageMu.Lock()
		if s.brandingImages == nil {
			s.brandingImages = make(map[string]brandingImage)
		}
		for variantKey := range s.brandingVariants {
			if strings.HasPrefix(variantKey, name+":") {
				delete(s.brandingVariants, variantKey)
			}
		}
		s.brandingImages[key] = cached
		s.brandingImageMu.Unlock()
	}
	return errors.Join(failures...)
}

func (s *Server) brandingImageCachePath(name, source string) string {
	if s.cacheDir == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(name + "\x00" + source))
	return filepath.Join(s.cacheDir, "branding", hex.EncodeToString(digest[:])+".json")
}

func (s *Server) readBrandingImageCache(name, source string) (brandingImage, error) {
	path := s.brandingImageCachePath(name, source)
	if path == "" {
		return brandingImage{}, fmt.Errorf("remote branding image has not been fetched")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() <= 0 || info.Size() > 16<<20 {
		return brandingImage{}, fmt.Errorf("remote branding image cache is unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return brandingImage{}, err
	}
	var record brandingImageCacheRecord
	if err := json.Unmarshal(data, &record); err != nil || record.FetchedAt.IsZero() {
		return brandingImage{}, fmt.Errorf("remote branding image cache is invalid")
	}
	image := brandingImage{data: record.Data, contentType: record.ContentType, fetchedAt: record.FetchedAt}
	if len(image.data) == 0 || len(image.data) > 10<<20 || validateBrandingImage(image) != nil {
		return brandingImage{}, fmt.Errorf("remote branding image cache is invalid")
	}
	return image, nil
}

func (s *Server) writeBrandingImageCache(name, source string, image brandingImage) (retErr error) {
	path := s.brandingImageCachePath(name, source)
	if path == "" {
		return nil
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	record, err := json.Marshal(brandingImageCacheRecord{ContentType: image.contentType, FetchedAt: image.fetchedAt, Data: image.data})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".branding-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.Remove(temporary.Name()); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary branding image cache: %w", cleanupErr))
		}
	}()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(record); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

func (s *Server) logBrandingImageFailure(configKey, source string) {
	digest := sha256.Sum256([]byte(configKey + "\x00" + source))
	key := configKey + ":" + hex.EncodeToString(digest[:])
	s.brandingImageMu.Lock()
	defer s.brandingImageMu.Unlock()
	if _, logged := s.brandingImageWarnings[key]; logged {
		return
	}
	if s.brandingImageWarnings == nil {
		s.brandingImageWarnings = make(map[string]struct{})
	}
	s.brandingImageWarnings[key] = struct{}{}
	slog.Warn("branding.image_unavailable", "key", configKey)
}

func (s *Server) embeddedBrandingImage() (brandingImage, error) {
	data, err := staticFS.ReadFile("static/images/icon.svg")
	if err != nil {
		return brandingImage{}, err
	}
	return brandingImage{data: data, contentType: "image/svg+xml", fetchedAt: time.Now()}, nil
}
