package securitydata

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const maxTextDownload = 64 << 20

func httpClient() *http.Client {
	return withHTTPSRedirects(&http.Client{Timeout: 2 * time.Minute})
}

func withHTTPSRedirects(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	copy := *client
	previousRedirect := copy.CheckRedirect
	copy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return fmt.Errorf("refusing non-HTTPS redirect")
		}
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		return nil
	}
	return &copy
}

func fetch(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	if client == nil {
		client = httpClient()
	} else {
		client = withHTTPSRedirects(client)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("refusing non-HTTPS data URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("data source returned HTTP %d", response.StatusCode)
	}
	if limit <= 0 {
		limit = maxTextDownload
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("data source response exceeds size limit")
	}
	return body, nil
}

func writeAtomically(directory, name string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(directory, 0750); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".update-*")
	if err != nil {
		return fmt.Errorf("create temporary data file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err = temporary.Chmod(mode); err == nil {
		_, err = temporary.Write(content)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write temporary data file: %w", err)
	}
	if err = os.Rename(temporaryPath, filepath.Join(directory, name)); err != nil {
		return fmt.Errorf("replace data file: %w", err)
	}
	return nil
}

func writeLastUpdated(directory string, updated time.Time) error {
	value := []byte(updated.UTC().Format(time.RFC3339) + "\n")
	return writeAtomically(directory, ".last_updated", value, 0640)
}
