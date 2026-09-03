package cities

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// City represents a city record
type City struct {
	ID      int     `json:"id"`
	Name    string  `json:"name"`
	Country string  `json:"country"`
	Coord   Coord   `json:"coord"`
}

// Coord represents geographic coordinates
type Coord struct {
	Lon float64 `json:"lon"`
	Lat float64 `json:"lat"`
}

// CityWithDistance includes distance from a point
type CityWithDistance struct {
	City
	Distance float64 `json:"distance"`
}

// Service provides city-related operations
type Service struct {
	cities    []City
	byID      map[int]*City
	byCountry map[string][]*City
}

// NewService creates a new cities service from JSON data
func NewService(jsonData []byte) (*Service, error) {
	var cities []City
	if err := json.Unmarshal(jsonData, &cities); err != nil {
		return nil, fmt.Errorf("failed to parse cities JSON: %w", err)
	}

	s := &Service{
		cities:    cities,
		byID:      make(map[int]*City),
		byCountry: make(map[string][]*City),
	}

	// Build indexes
	for i := range s.cities {
		city := &s.cities[i]
		s.byID[city.ID] = city
		countryCode := strings.ToUpper(city.Country)
		s.byCountry[countryCode] = append(s.byCountry[countryCode], city)
	}

	return s, nil
}

// Count returns the total number of cities
func (s *Service) Count() int {
	return len(s.cities)
}

// CountryCount returns the number of unique countries
func (s *Service) CountryCount() int {
	return len(s.byCountry)
}

// GetAll returns all cities (with pagination)
func (s *Service) GetAll(page, limit int) ([]City, int) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	total := len(s.cities)
	start := (page - 1) * limit
	if start >= total {
		return []City{}, total
	}

	end := start + limit
	if end > total {
		end = total
	}

	return s.cities[start:end], total
}

// GetByID returns a city by its ID
func (s *Service) GetByID(id int) (*City, bool) {
	city, ok := s.byID[id]
	return city, ok
}

// GetByCountry returns all cities in a country
func (s *Service) GetByCountry(countryCode string, limit int) []City {
	if limit < 1 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	cities := s.byCountry[strings.ToUpper(countryCode)]
	if cities == nil {
		return []City{}
	}

	result := make([]City, 0, min(len(cities), limit))
	for i := 0; i < len(cities) && i < limit; i++ {
		result = append(result, *cities[i])
	}
	return result
}

// Search searches cities by name (case-insensitive substring match)
func (s *Service) Search(query string, limit int) []City {
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	if len(query) < 2 {
		return []City{}
	}

	queryLower := strings.ToLower(query)
	result := make([]City, 0, limit)

	for i := range s.cities {
		if strings.Contains(strings.ToLower(s.cities[i].Name), queryLower) {
			result = append(result, s.cities[i])
			if len(result) >= limit {
				break
			}
		}
	}

	return result
}

// FindNearest finds the city closest to the given coordinates
func (s *Service) FindNearest(lat, lon float64) (*CityWithDistance, bool) {
	if len(s.cities) == 0 {
		return nil, false
	}

	var nearest *City
	minDist := math.MaxFloat64

	for i := range s.cities {
		dist := haversine(lat, lon, s.cities[i].Coord.Lat, s.cities[i].Coord.Lon)
		if dist < minDist {
			minDist = dist
			nearest = &s.cities[i]
		}
	}

	if nearest == nil {
		return nil, false
	}

	return &CityWithDistance{
		City:     *nearest,
		Distance: math.Round(minDist*100) / 100, // Round to 2 decimal places
	}, true
}

// FindNearby finds cities near the given coordinates within a radius
func (s *Service) FindNearby(lat, lon, radiusKm float64, limit int) []CityWithDistance {
	if limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	var results []CityWithDistance

	for i := range s.cities {
		dist := haversine(lat, lon, s.cities[i].Coord.Lat, s.cities[i].Coord.Lon)
		if dist <= radiusKm {
			results = append(results, CityWithDistance{
				City:     s.cities[i],
				Distance: math.Round(dist*100) / 100,
			})
		}
	}

	// Sort by distance
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	return results
}

// GetRaw returns all cities (for raw data endpoint)
func (s *Service) GetRaw() []City {
	return s.cities
}

// haversine calculates the great-circle distance between two points
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371.0 // km

	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	deltaLat := (lat2 - lat1) * math.Pi / 180
	deltaLon := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(deltaLon/2)*math.Sin(deltaLon/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadius * c
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
