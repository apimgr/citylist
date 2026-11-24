package securitydata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

const geoIPLatestURL = "https://github.com/sapics/ip-location-db/releases/download/latest/"
const geoIPChecksumURL = "https://github.com/sapics/ip-location-db/releases/download/checksum/"

type geoIPDatabase struct {
	file           string
	metadataPrefix string
}

var geoIPDatabases = []geoIPDatabase{
	{file: "iptoasn-asn.mmdb", metadataPrefix: "asn"},
	{file: "iptoasn-country.mmdb", metadataPrefix: "country"},
	{file: "dbip-city-ipv4.mmdb", metadataPrefix: "city"},
	{file: "dbip-city-ipv6.mmdb", metadataPrefix: "city"},
}

// UpdateGeoIP fetches and checksum-verifies all canonical ip-location-db MMDBs.
func UpdateGeoIP(ctx context.Context, directory string, client *http.Client) error {
	if client == nil {
		client = httpClient()
	} else {
		client = withHTTPSRedirects(client)
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0750); err != nil {
		return fmt.Errorf("create GeoIP parent directory: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(directory), ".geoip-stage-")
	if err != nil {
		return fmt.Errorf("create GeoIP staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.Chmod(stage, 0750); err != nil {
		return fmt.Errorf("secure GeoIP staging directory: %w", err)
	}
	for _, database := range geoIPDatabases {
		checksumBytes, err := fetch(ctx, client, geoIPChecksumURL+database.file+".sha256", 4096)
		if err != nil {
			return fmt.Errorf("fetch GeoIP checksum for %s: %w", database.file, err)
		}
		expected, err := parseSHA256(string(checksumBytes))
		if err != nil {
			return fmt.Errorf("parse GeoIP checksum for %s: %w", database.file, err)
		}
		if err := updateMMDB(ctx, client, stage, database, expected); err != nil {
			return err
		}
	}
	if err := writeLastUpdated(stage, time.Now()); err != nil {
		return fmt.Errorf("record GeoIP update time: %w", err)
	}
	if err := replaceDataDirectory(stage, directory); err != nil {
		return fmt.Errorf("publish GeoIP databases: %w", err)
	}
	return nil
}

func updateMMDB(ctx context.Context, client *http.Client, directory string, database geoIPDatabase, expected string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, geoIPLatestURL+database.file, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download GeoIP database %s: %w", database.file, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download GeoIP database %s: HTTP %d", database.file, response.StatusCode)
	}
	temporary, err := os.CreateTemp(directory, ".geoip-*")
	if err != nil {
		return fmt.Errorf("create temporary GeoIP database: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0640); err != nil {
		_ = temporary.Close()
		return err
	}
	digest := sha256.New()
	limited := io.LimitReader(response.Body, (512<<20)+1)
	written, copyErr := io.Copy(io.MultiWriter(temporary, digest), limited)
	if copyErr != nil {
		_ = temporary.Close()
		return fmt.Errorf("save GeoIP database %s: %w", database.file, copyErr)
	}
	if written <= 0 || written > 512<<20 {
		_ = temporary.Close()
		return fmt.Errorf("GeoIP database %s is empty or exceeds the size limit", database.file)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync GeoIP database %s: %w", database.file, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close GeoIP database %s: %w", database.file, err)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); !strings.EqualFold(got, expected) {
		return fmt.Errorf("GeoIP database %s checksum mismatch", database.file)
	}
	reader, err := maxminddb.Open(temporaryPath)
	if err != nil {
		return fmt.Errorf("validate GeoIP database %s: %w", database.file, err)
	}
	metadataType := strings.ToLower(reader.Metadata.DatabaseType)
	_ = reader.Close()
	if !strings.HasPrefix(metadataType, database.metadataPrefix+" ") {
		return fmt.Errorf("GeoIP database %s has unexpected database type %q", database.file, metadataType)
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, database.file)); err != nil {
		return fmt.Errorf("replace GeoIP database %s: %w", database.file, err)
	}
	return nil
}

func parseSHA256(text string) (string, error) {
	for _, field := range strings.Fields(text) {
		if len(field) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(field); err == nil {
			return strings.ToLower(field), nil
		}
	}
	return "", fmt.Errorf("SHA-256 digest is missing")
}
