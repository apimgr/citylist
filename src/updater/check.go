package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const releasesURL = "https://api.github.com/repos/apimgr/citylist/releases"

type Release struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Asset is one downloadable GitHub release file.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Check returns the newest release available on the selected cumulative channel.
func Check(ctx context.Context, client *http.Client, branch string, buildEpoch int64) (*Release, error) {
	return check(ctx, client, releasesURL, branch, buildEpoch)
}

// CheckEligible returns the newest newer release that has completed its configured defer window.
func CheckEligible(ctx context.Context, client *http.Client, branch, currentVersion string, buildEpoch int64, deferDays int, now time.Time) (*Release, error) {
	if deferDays < 0 || deferDays > 365 {
		return nil, fmt.Errorf("defer days must be between 0 and 365")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	branch = normalizeBranch(branch)
	if now.IsZero() {
		now = time.Now()
	}
	for page := 1; page <= 10; page++ {
		endpoint := fmt.Sprintf("%s?per_page=100&page=%d", releasesURL, page)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("request release list: %w", err)
		}
		if response.StatusCode != http.StatusOK {
			status := response.StatusCode
			_ = response.Body.Close()
			if status == http.StatusNotFound {
				return nil, nil
			}
			return nil, fmt.Errorf("release endpoint returned HTTP %d", status)
		}
		var releases []Release
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&releases)
		_ = response.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode release list: %w", decodeErr)
		}
		for index := range releases {
			release := releases[index]
			if !matchesBranch(release, branch) || !IsNewer(release.TagName, currentVersion, release.PublishedAt.Unix(), buildEpoch) {
				continue
			}
			if branch != "devel" && now.UTC().Sub(release.PublishedAt.UTC()) < time.Duration(deferDays)*24*time.Hour {
				continue
			}
			return &release, nil
		}
		if len(releases) < 100 {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("release list exceeds the 1000-release search limit")
}

func check(ctx context.Context, client *http.Client, endpoint, branch string, buildEpoch int64) (*Release, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	branch = normalizeBranch(branch)
	separator := "?"
	if strings.Contains(endpoint, "?") {
		separator = "&"
	}
	for page := 1; page <= 10; page++ {
		requestURL := fmt.Sprintf("%s%sper_page=100&page=%d", endpoint, separator, page)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("request release list: %w", err)
		}
		if response.StatusCode == http.StatusNotFound {
			_ = response.Body.Close()
			return nil, nil
		}
		if response.StatusCode != http.StatusOK {
			status := response.StatusCode
			_ = response.Body.Close()
			return nil, fmt.Errorf("release endpoint returned HTTP %d", status)
		}
		var releases []Release
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&releases)
		_ = response.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode release list: %w", decodeErr)
		}
		for index := range releases {
			release := &releases[index]
			if !matchesBranch(*release, branch) {
				continue
			}
			if release.TagName == "devel" && release.PublishedAt.Unix() <= buildEpoch {
				continue
			}
			return release, nil
		}
		if len(releases) < 100 {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("release list exceeds the 1000-release search limit")
}

func normalizeBranch(branch string) string {
	switch strings.ToLower(strings.TrimSpace(branch)) {
	case "beta":
		return "beta"
	case "devel", "daily", "rolling", "dev", "development":
		return "devel"
	default:
		return "stable"
	}
}

func matchesBranch(release Release, branch string) bool {
	if !release.Prerelease {
		return true
	}
	switch branch {
	case "beta":
		return strings.HasSuffix(release.TagName, "-beta")
	case "devel":
		return strings.HasSuffix(release.TagName, "-beta") || release.TagName == "devel"
	default:
		return false
	}
}

var versionParts = regexp.MustCompile(`^v?(\d+(?:\.\d+)*)(?:[-+].*)?$`)

// IsNewer compares a release tag to the running version, using build time for devel.
func IsNewer(candidate, current string, publishedAt, buildEpoch int64) bool {
	if candidate == "devel" {
		return publishedAt > buildEpoch
	}
	candidateNumbers, candidateOK := parseVersion(candidate)
	currentNumbers, currentOK := parseVersion(current)
	if !candidateOK {
		return false
	}
	if !currentOK {
		return true
	}
	length := len(candidateNumbers)
	if len(currentNumbers) > length {
		length = len(currentNumbers)
	}
	for index := 0; index < length; index++ {
		var candidatePart, currentPart int
		if index < len(candidateNumbers) {
			candidatePart = candidateNumbers[index]
		}
		if index < len(currentNumbers) {
			currentPart = currentNumbers[index]
		}
		if candidatePart != currentPart {
			return candidatePart > currentPart
		}
	}
	return !strings.Contains(candidate, "-") && strings.Contains(current, "-")
}

func parseVersion(value string) ([]int, bool) {
	matches := versionParts.FindStringSubmatch(value)
	if len(matches) != 2 {
		return nil, false
	}
	parts := strings.Split(matches[1], ".")
	numbers := make([]int, len(parts))
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		numbers[index] = number
	}
	return numbers, true
}
