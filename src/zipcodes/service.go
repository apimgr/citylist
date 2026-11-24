package zipcodes

import (
	"archive/zip"
	"bufio"
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Record is a public US postal-code record from the embedded GeoNames dataset.
type Record struct {
	ZIP   string  `json:"zip"`
	City  string  `json:"city"`
	State string  `json:"state"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
}

// NearbyRecord includes the great-circle distance from a query point in km.
type NearbyRecord struct {
	Record
	DistanceKM float64 `json:"distance_km"`
}

// Service indexes the immutable postal-code dataset for exact and geographic lookups.
type Service struct {
	records []Record
	byZIP   map[string]Record
	byState map[string][]Record
}

// NewService parses the GeoNames US postal-code ZIP archive.
func NewService(archive []byte) (*Service, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open GeoNames postal-code archive: %w", err)
	}
	var source *zip.File
	for _, file := range reader.File {
		if file.Name == "US.txt" {
			source = file
			break
		}
	}
	if source == nil {
		return nil, fmt.Errorf("GeoNames archive does not contain US.txt")
	}
	file, err := source.Open()
	if err != nil {
		return nil, fmt.Errorf("open GeoNames US.txt: %w", err)
	}
	defer func() { _ = file.Close() }()

	service := &Service{byZIP: make(map[string]Record), byState: make(map[string][]Record)}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 12 || fields[0] != "US" {
			return nil, fmt.Errorf("invalid GeoNames postal record on line %d", lineNumber)
		}
		zipCode := strings.TrimSpace(fields[1])
		state := strings.ToUpper(strings.TrimSpace(fields[4]))
		city := strings.TrimSpace(fields[2])
		// GeoNames includes military mail routing records (APO/FPO/DPO) without
		// geographic state codes. They are not geographic ZIP locations.
		if state == "" && (strings.HasPrefix(city, "APO ") || strings.HasPrefix(city, "FPO ") || strings.HasPrefix(city, "DPO ")) {
			continue
		}
		lat, latErr := strconv.ParseFloat(fields[9], 64)
		lon, lonErr := strconv.ParseFloat(fields[10], 64)
		if len(zipCode) != 5 || !digits(zipCode) || len(state) != 2 || city == "" || latErr != nil || lonErr != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
			return nil, fmt.Errorf("invalid GeoNames postal values on line %d", lineNumber)
		}
		record := Record{ZIP: zipCode, City: city, State: state, Lat: lat, Lon: lon}
		// GeoNames can list alternative place names for a postal code. Keep the first
		// source record as the stable display name while retaining one record per ZIP.
		if _, exists := service.byZIP[zipCode]; exists {
			continue
		}
		service.byZIP[zipCode] = record
		service.records = append(service.records, record)
		service.byState[state] = append(service.byState[state], record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read GeoNames US.txt: %w", err)
	}
	if len(service.records) == 0 {
		return nil, fmt.Errorf("GeoNames US.txt contains no usable postal records")
	}
	sort.Slice(service.records, func(i, j int) bool { return service.records[i].ZIP < service.records[j].ZIP })
	for state := range service.byState {
		sort.Slice(service.byState[state], func(i, j int) bool {
			return service.byState[state][i].ZIP < service.byState[state][j].ZIP
		})
	}
	return service, nil
}

func digits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

// Count returns the number of unique ZIP codes in the embedded source.
func (s *Service) Count() int { return len(s.records) }

// Lookup returns one postal record by its five-digit ZIP code.
func (s *Service) Lookup(zipCode string) (Record, bool) {
	record, ok := s.byZIP[strings.TrimSpace(zipCode)]
	return record, ok
}

// Search returns postal records whose city name contains query, optionally
// restricted to a two-letter state code.
func (s *Service) Search(query, state string, limit int) []Record {
	query = strings.ToLower(strings.TrimSpace(query))
	state = strings.ToUpper(strings.TrimSpace(state))
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if len(query) < 2 {
		return []Record{}
	}
	candidates := s.records
	if state != "" {
		candidates = s.byState[state]
	}
	results := make([]Record, 0, min(limit, len(candidates)))
	for _, record := range candidates {
		if strings.Contains(strings.ToLower(record.City), query) {
			results = append(results, record)
			if len(results) == limit {
				break
			}
		}
	}
	return results
}

// ByState returns ZIP records for a state, with bounded pagination.
func (s *Service) ByState(state string, page, limit int) ([]Record, int) {
	state = strings.ToUpper(strings.TrimSpace(state))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	records := s.byState[state]
	start := (page - 1) * limit
	if start >= len(records) {
		return []Record{}, len(records)
	}
	end := min(start+limit, len(records))
	return append([]Record(nil), records[start:end]...), len(records)
}

// Nearby returns postal records within radiusKM ordered by distance.
func (s *Service) Nearby(lat, lon, radiusKM float64, limit int) ([]NearbyRecord, error) {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 || math.IsNaN(lon) || math.IsInf(lon, 0) || lon < -180 || lon > 180 {
		return nil, fmt.Errorf("coordinates are out of range")
	}
	if math.IsNaN(radiusKM) || math.IsInf(radiusKM, 0) || radiusKM <= 0 || radiusKM > 20000 {
		return nil, fmt.Errorf("radius must be greater than 0 and at most 20000 km")
	}
	if limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	results := make([]NearbyRecord, 0)
	for _, record := range s.records {
		distance := haversine(lat, lon, record.Lat, record.Lon)
		if distance <= radiusKM {
			results = append(results, NearbyRecord{Record: record, DistanceKM: math.Round(distance*100) / 100})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].DistanceKM == results[j].DistanceKM {
			return results[i].ZIP < results[j].ZIP
		}
		return results[i].DistanceKM < results[j].DistanceKM
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371.0088
	lat1, lat2 = lat1*math.Pi/180, lat2*math.Pi/180
	deltaLat := (lat2 - lat1)
	deltaLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(deltaLon/2)*math.Sin(deltaLon/2)
	return earthRadius * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
