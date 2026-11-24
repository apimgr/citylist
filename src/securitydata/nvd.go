package securitydata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const nvdDefaultEndpoint = "https://services.nvd.nist.gov/rest/json/cves/2.0"

type nvdEnvelope struct {
	TotalResults    int               `json:"totalResults"`
	Vulnerabilities []json.RawMessage `json:"vulnerabilities"`
}

type nvdCVE struct {
	ID string `json:"id"`
}

// UpdateNVD downloads the initial NVD dataset or merges modified CVE records.
func UpdateNVD(ctx context.Context, directory, endpoint, apiKey string, client *http.Client, now time.Time) error {
	if endpoint == "" {
		endpoint = nvdDefaultEndpoint
	}
	if client == nil {
		client = httpClient()
	} else {
		client = withHTTPSRedirects(client)
	}
	if err := os.MkdirAll(directory, 0750); err != nil {
		return fmt.Errorf("create CVE directory: %w", err)
	}
	path := filepath.Join(directory, "nvd.json")
	records := map[string]json.RawMessage{}
	lastUpdate := time.Time{}
	if existing, err := os.ReadFile(path); err == nil {
		var current []json.RawMessage
		if err := json.Unmarshal(existing, &current); err != nil || len(current) == 0 {
			return fmt.Errorf("existing NVD dataset is invalid")
		}
		for _, raw := range current {
			var record nvdCVE
			if err := json.Unmarshal(raw, &record); err != nil || record.ID == "" {
				return fmt.Errorf("existing NVD dataset contains an invalid CVE record")
			}
			records[record.ID] = append(json.RawMessage(nil), raw...)
		}
		if marker, err := os.ReadFile(filepath.Join(directory, ".last_updated")); err == nil {
			lastUpdate, _ = time.Parse(time.RFC3339, strings.TrimSpace(string(marker)))
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read existing NVD dataset: %w", err)
	}

	end := now.UTC()
	if end.IsZero() {
		end = time.Now().UTC()
	}
	windows := [][2]time.Time{{}}
	if !lastUpdate.IsZero() {
		windows = nil
		start := lastUpdate.UTC()
		for start.Before(end) {
			windowEnd := start.Add(120 * 24 * time.Hour)
			if windowEnd.After(end) {
				windowEnd = end
			}
			windows = append(windows, [2]time.Time{start, windowEnd})
			start = windowEnd
		}
		if len(windows) == 0 {
			windows = append(windows, [2]time.Time{end.Add(-time.Minute), end})
		}
	}
	limiter := nvdRateLimiter{interval: 6 * time.Second}
	if apiKey != "" {
		limiter.interval = 600 * time.Millisecond
	}
	for _, window := range windows {
		startIndex := 0
		for {
			if err := limiter.wait(ctx); err != nil {
				return err
			}
			requestURL, err := nvdRequestURL(endpoint, startIndex, window, lastUpdate.IsZero())
			if err != nil {
				return err
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
			if err != nil {
				return err
			}
			request.Header.Set("Accept", "application/json")
			if apiKey != "" {
				request.Header.Set("apiKey", apiKey)
			}
			response, err := client.Do(request)
			if err != nil {
				return fmt.Errorf("request NVD CVEs: %w", err)
			}
			if response.StatusCode != http.StatusOK {
				status := response.StatusCode
				_ = response.Body.Close()
				return fmt.Errorf("NVD returned HTTP %d; retry on the next scheduled run", status)
			}
			var page nvdEnvelope
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&page)
			_ = response.Body.Close()
			if decodeErr != nil {
				return fmt.Errorf("decode NVD response: %w", decodeErr)
			}
			for _, item := range page.Vulnerabilities {
				var wrapper struct {
					CVE json.RawMessage `json:"cve"`
				}
				if err := json.Unmarshal(item, &wrapper); err != nil || len(wrapper.CVE) == 0 {
					return fmt.Errorf("NVD response contains an invalid CVE record")
				}
				var record nvdCVE
				if err := json.Unmarshal(wrapper.CVE, &record); err != nil || record.ID == "" {
					return fmt.Errorf("NVD response contains a CVE without an id")
				}
				records[record.ID] = append(json.RawMessage(nil), wrapper.CVE...)
			}
			startIndex += len(page.Vulnerabilities)
			if startIndex >= page.TotalResults || len(page.Vulnerabilities) == 0 {
				break
			}
		}
	}
	if len(records) == 0 {
		return fmt.Errorf("NVD update produced no valid records")
	}
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	output := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		output = append(output, records[id])
	}
	content, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("encode NVD dataset: %w", err)
	}
	if err := writeAtomically(directory, "nvd.json", content, 0640); err != nil {
		return fmt.Errorf("publish NVD dataset: %w", err)
	}
	if err := writeLastUpdated(directory, end); err != nil {
		return fmt.Errorf("record NVD update time: %w", err)
	}
	return nil
}

type nvdRateLimiter struct {
	interval time.Duration
	last     time.Time
}

func (limiter *nvdRateLimiter) wait(ctx context.Context) error {
	if !limiter.last.IsZero() {
		remaining := time.Until(limiter.last.Add(limiter.interval))
		if remaining > 0 {
			timer := time.NewTimer(remaining)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	limiter.last = time.Now()
	return nil
}

func nvdRequestURL(endpoint string, startIndex int, window [2]time.Time, full bool) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("NVD source must be an HTTPS URL")
	}
	query := parsed.Query()
	query.Set("resultsPerPage", "2000")
	query.Set("startIndex", fmt.Sprint(startIndex))
	if !full {
		const nvdTimeFormat = "2006-01-02T15:04:05.000"
		query.Set("lastModStartDate", window[0].UTC().Format(nvdTimeFormat))
		query.Set("lastModEndDate", window[1].UTC().Format(nvdTimeFormat))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
