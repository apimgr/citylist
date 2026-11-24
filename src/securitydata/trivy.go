package securitydata

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const trivyLayerMediaType = "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"

type ociDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
}

type ociManifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	Layers        []ociDescriptor `json:"layers"`
}

type trivyMetadata struct {
	NextUpdate time.Time `json:"NextUpdate"`
}

// UpdateTrivy pulls the configured OCI database layer and atomically publishes its two data files.
func UpdateTrivy(ctx context.Context, directory, source string, client *http.Client, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	if client == nil {
		client = httpClient()
	} else {
		client = withHTTPSRedirects(client)
	}
	if strings.TrimSpace(source) == "" {
		source = "ghcr.io/aquasecurity/trivy-db:2"
	}
	registry, repository, reference, err := parseOCIReference(source)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(filepath.Join(directory, "metadata.json")); err == nil {
		var metadata trivyMetadata
		if json.Unmarshal(existing, &metadata) == nil && metadata.NextUpdate.After(now) {
			if databaseInfo, statErr := os.Stat(filepath.Join(directory, "trivy.db")); statErr == nil && databaseInfo.Mode().IsRegular() && databaseInfo.Size() > 0 {
				if marker, markerErr := os.ReadFile(filepath.Join(directory, ".last_updated")); markerErr == nil {
					if _, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(string(marker))); parseErr == nil {
						return nil
					}
				}
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0750); err != nil {
		return fmt.Errorf("create Trivy parent directory: %w", err)
	}
	tokenURL := "https://" + registry + "/token?service=" + url.QueryEscape(registry) + "&scope=" + url.QueryEscape("repository:"+repository+":pull")
	tokenBody, err := fetch(ctx, client, tokenURL, 1<<20)
	if err != nil {
		return fmt.Errorf("request Trivy registry token: %w", err)
	}
	var tokenResponse struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(tokenBody, &tokenResponse); err != nil {
		return fmt.Errorf("decode trivy registry token: %w", err)
	}
	token := tokenResponse.Token
	if token == "" {
		token = tokenResponse.AccessToken
	}
	if token == "" {
		return fmt.Errorf("trivy registry did not return a pull token")
	}
	manifestURL := "https://" + registry + "/v2/" + repository + "/manifests/" + url.PathEscape(reference)
	manifest, err := ociFetch(ctx, client, manifestURL, token, 4<<20, "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	if err != nil {
		return fmt.Errorf("fetch trivy OCI manifest: %w", err)
	}
	var image ociManifest
	if err := json.Unmarshal(manifest, &image); err != nil || image.SchemaVersion != 2 {
		return fmt.Errorf("trivy OCI manifest is invalid")
	}
	var layer string
	for _, descriptor := range image.Layers {
		if descriptor.MediaType == trivyLayerMediaType {
			layer = descriptor.Digest
			break
		}
	}
	if layer == "" || !strings.HasPrefix(layer, "sha256:") {
		return fmt.Errorf("trivy OCI manifest lacks the expected database layer")
	}
	blobURL := "https://" + registry + "/v2/" + repository + "/blobs/" + layer
	compressed, err := ociFetch(ctx, client, blobURL, token, 512<<20, "application/octet-stream")
	if err != nil {
		return fmt.Errorf("download trivy database layer: %w", err)
	}
	digest := sha256.Sum256(compressed)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), strings.TrimPrefix(layer, "sha256:")) {
		return fmt.Errorf("trivy database layer digest mismatch")
	}
	stage, err := os.MkdirTemp(filepath.Dir(directory), ".trivy-stage-")
	if err != nil {
		return fmt.Errorf("create trivy staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.Chmod(stage, 0750); err != nil {
		return fmt.Errorf("secure trivy staging directory: %w", err)
	}
	if err := extractTrivyLayer(compressed, stage); err != nil {
		return err
	}
	metadataBytes, err := os.ReadFile(filepath.Join(stage, "metadata.json"))
	if err != nil {
		return fmt.Errorf("trivy layer has no metadata.json: %w", err)
	}
	var metadata trivyMetadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil || metadata.NextUpdate.IsZero() {
		return fmt.Errorf("trivy metadata.json is invalid")
	}
	databaseInfo, err := os.Stat(filepath.Join(stage, "trivy.db"))
	if err != nil || !databaseInfo.Mode().IsRegular() || databaseInfo.Size() == 0 {
		return fmt.Errorf("trivy layer has no valid trivy.db")
	}
	if err := writeLastUpdated(stage, now); err != nil {
		return fmt.Errorf("record trivy update time: %w", err)
	}
	if err := replaceDataDirectory(stage, directory); err != nil {
		return fmt.Errorf("publish trivy dataset: %w", err)
	}
	return nil
}

func parseOCIReference(source string) (registry, repository, reference string, err error) {
	if strings.Contains(source, "://") || strings.ContainsAny(source, "@?#") {
		return "", "", "", fmt.Errorf("trivy source must be an OCI image reference")
	}
	parts := strings.Split(source, "/")
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("trivy source must include a registry and repository")
	}
	registry = strings.ToLower(parts[0])
	if registry == "docker.io" || registry == "index.docker.io" {
		return "", "", "", fmt.Errorf("trivy source must use the configured HTTPS registry")
	}
	repositoryRef := strings.Join(parts[1:], "/")
	lastSlash := strings.LastIndexByte(repositoryRef, '/')
	colon := strings.LastIndexByte(repositoryRef, ':')
	if colon > lastSlash {
		repository, reference = repositoryRef[:colon], repositoryRef[colon+1:]
	} else {
		repository, reference = repositoryRef, "latest"
	}
	if repository == "" || reference == "" || strings.Contains(repository, "..") || strings.ContainsAny(repository, " \\;") || strings.ContainsAny(reference, " /\\") {
		return "", "", "", fmt.Errorf("trivy source image reference is invalid")
	}
	return registry, repository, reference, nil
}

func ociFetch(ctx context.Context, client *http.Client, rawURL, token string, limit int64, accept string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", accept)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("registry response exceeds size limit")
	}
	return data, nil
}

func extractTrivyLayer(compressed []byte, directory string) error {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("open Trivy gzip layer: %w", err)
	}
	defer func() { _ = reader.Close() }()
	limitedLayer := &io.LimitedReader{R: reader, N: (1 << 30) + 1}
	tarReader := tar.NewReader(limitedLayer)
	foundDB, foundMetadata := false, false
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read Trivy tar layer: %w", err)
		}
		name := filepath.Base(filepath.Clean(header.Name))
		if name != "trivy.db" && name != "metadata.json" {
			continue
		}
		maximumSize := int64(1 << 30)
		if name == "metadata.json" {
			maximumSize = 1 << 20
		}
		if header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > maximumSize {
			return fmt.Errorf("trivy layer contains an invalid %s entry", name)
		}
		mode := os.FileMode(0640)
		destination := filepath.Join(directory, name)
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return fmt.Errorf("create extracted trivy %s: %w", name, err)
		}
		_, copyErr := io.CopyN(output, tarReader, header.Size)
		closeErr := output.Close()
		if copyErr != nil {
			return fmt.Errorf("extract trivy %s: %w", name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close extracted trivy %s: %w", name, closeErr)
		}
		if name == "trivy.db" {
			foundDB = true
		} else {
			foundMetadata = true
		}
	}
	if limitedLayer.N == 0 {
		return fmt.Errorf("trivy layer exceeds the decompressed size limit")
	}
	if !foundDB || !foundMetadata {
		return fmt.Errorf("trivy layer is missing trivy.db or metadata.json")
	}
	return nil
}

func replaceDataDirectory(staging, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return os.Rename(staging, target)
	} else if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("existing data path is not a directory")
	}
	previous := target + ".previous"
	_ = os.RemoveAll(previous)
	if err := os.Rename(target, previous); err != nil {
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		_ = os.Rename(previous, target)
		return err
	}
	return os.RemoveAll(previous)
}
