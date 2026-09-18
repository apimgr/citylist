package cities

import (
	"testing"
)

func sampleJSON() []byte {
	return []byte(`[
		{"id":1,"name":"London","country":"gb","coord":{"lon":-0.1257,"lat":51.5085}},
		{"id":2,"name":"Paris","country":"FR","coord":{"lon":2.3522,"lat":48.8566}},
		{"id":3,"name":"Berlin","country":"DE","coord":{"lon":13.405,"lat":52.52}},
		{"id":4,"name":"Lyon","country":"fr","coord":{"lon":4.8357,"lat":45.764}},
		{"id":5,"name":"Manchester","country":"GB","coord":{"lon":-2.2426,"lat":53.4808}}
	]`)
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(sampleJSON())
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	return s
}

func TestNewService(t *testing.T) {
	t.Run("valid json", func(t *testing.T) {
		s := newTestService(t)
		if s.Count() != 5 {
			t.Errorf("Count() = %d, want 5", s.Count())
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := NewService([]byte(`not json`))
		if err == nil {
			t.Fatal("expected error for invalid JSON, got nil")
		}
	})

	t.Run("empty array", func(t *testing.T) {
		s, err := NewService([]byte(`[]`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s.Count() != 0 {
			t.Errorf("Count() = %d, want 0", s.Count())
		}
	})
}

func TestCount(t *testing.T) {
	s := newTestService(t)
	if got := s.Count(); got != 5 {
		t.Errorf("Count() = %d, want 5", got)
	}
}

func TestCountryCount(t *testing.T) {
	s := newTestService(t)
	// countries normalized to upper: GB, FR, DE = 3 unique
	if got := s.CountryCount(); got != 3 {
		t.Errorf("CountryCount() = %d, want 3", got)
	}
}

func TestGetAll(t *testing.T) {
	s := newTestService(t)

	tests := []struct {
		name      string
		page      int
		limit     int
		wantLen   int
		wantTotal int
	}{
		{"defaults invalid page/limit", 0, 0, 5, 5},
		{"page 1 limit 2", 1, 2, 2, 5},
		{"page 2 limit 2", 2, 2, 2, 5},
		{"page 3 limit 2", 3, 2, 1, 5},
		{"page beyond range", 10, 2, 0, 5},
		{"limit exceeds max", 1, 5000, 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, total := s.GetAll(tt.page, tt.limit)
			if len(got) != tt.wantLen {
				t.Errorf("GetAll(%d,%d) len = %d, want %d", tt.page, tt.limit, len(got), tt.wantLen)
			}
			if total != tt.wantTotal {
				t.Errorf("GetAll(%d,%d) total = %d, want %d", tt.page, tt.limit, total, tt.wantTotal)
			}
		})
	}
}

func TestGetByID(t *testing.T) {
	s := newTestService(t)

	city, ok := s.GetByID(2)
	if !ok {
		t.Fatal("expected to find city with ID 2")
	}
	if city.Name != "Paris" {
		t.Errorf("GetByID(2).Name = %q, want Paris", city.Name)
	}

	_, ok = s.GetByID(999)
	if ok {
		t.Error("expected GetByID(999) to return ok=false")
	}
}

func TestGetByCountry(t *testing.T) {
	s := newTestService(t)

	tests := []struct {
		name    string
		code    string
		limit   int
		wantLen int
	}{
		{"uppercase match", "FR", 10, 2},
		{"lowercase match normalizes", "fr", 10, 2},
		{"unknown country", "ZZ", 10, 0},
		{"limit applied", "GB", 1, 1},
		{"limit invalid defaults", "GB", 0, 2},
		{"limit exceeds max clamps", "GB", 5000, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.GetByCountry(tt.code, tt.limit)
			if len(got) != tt.wantLen {
				t.Errorf("GetByCountry(%q,%d) len = %d, want %d", tt.code, tt.limit, len(got), tt.wantLen)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	s := newTestService(t)

	// query "er" matches Berlin and Manchester (case-insensitive substring)
	tests := []struct {
		name    string
		query   string
		limit   int
		wantLen int
	}{
		{"short query below min length", "L", 10, 0},
		{"case insensitive substring", "lon", 10, 1},
		{"matches multiple", "er", 10, 2},
		{"no match", "xyz", 10, 0},
		{"limit invalid defaults", "er", 0, 2},
		{"limit exceeds max clamps", "er", 500, 2},
		{"limit truncates results", "er", 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Search(tt.query, tt.limit)
			if len(got) != tt.wantLen {
				t.Errorf("Search(%q,%d) len = %d, want %d", tt.query, tt.limit, len(got), tt.wantLen)
			}
		})
	}
}

func TestFindNearest(t *testing.T) {
	s := newTestService(t)

	city, ok := s.FindNearest(51.5, -0.12)
	if !ok {
		t.Fatal("expected to find a nearest city")
	}
	if city.Name != "London" {
		t.Errorf("FindNearest near London coords = %q, want London", city.Name)
	}
	if city.Distance < 0 {
		t.Errorf("Distance should be non-negative, got %f", city.Distance)
	}

	empty, err := NewService([]byte(`[]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, ok = empty.FindNearest(0, 0)
	if ok {
		t.Error("expected FindNearest on empty service to return ok=false")
	}
}

func TestFindNearby(t *testing.T) {
	s := newTestService(t)

	tests := []struct {
		name     string
		lat, lon float64
		radiusKm float64
		limit    int
		wantMin  int
	}{
		{"small radius near london", 51.5085, -0.1257, 1, 10, 1},
		{"large radius includes multiple", 51.5, 0, 1000, 10, 2},
		{"zero radius no match far away", 0, 0, 1, 10, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.FindNearby(tt.lat, tt.lon, tt.radiusKm, tt.limit)
			if len(got) < tt.wantMin {
				t.Errorf("FindNearby() len = %d, want at least %d", len(got), tt.wantMin)
			}
			for i := 1; i < len(got); i++ {
				if got[i-1].Distance > got[i].Distance {
					t.Errorf("results not sorted by distance: %v", got)
				}
			}
		})
	}

	t.Run("limit invalid defaults", func(t *testing.T) {
		got := s.FindNearby(51.5, 0, 100000, 0)
		if len(got) == 0 {
			t.Error("expected non-empty results with default limit")
		}
	})

	t.Run("limit exceeds max clamps", func(t *testing.T) {
		got := s.FindNearby(51.5, 0, 100000, 5000)
		if len(got) > 100 {
			t.Errorf("expected results clamped to 100, got %d", len(got))
		}
	})

	t.Run("limit truncates sorted results", func(t *testing.T) {
		got := s.FindNearby(51.5, 0, 100000, 1)
		if len(got) != 1 {
			t.Errorf("expected exactly 1 result, got %d", len(got))
		}
	})
}

func TestGetRaw(t *testing.T) {
	s := newTestService(t)
	got := s.GetRaw()
	if len(got) != 5 {
		t.Errorf("GetRaw() len = %d, want 5", len(got))
	}
}

func TestHaversine(t *testing.T) {
	if d := haversine(51.5, -0.1, 51.5, -0.1); d != 0 {
		t.Errorf("haversine same point = %f, want 0", d)
	}

	// London to Paris is approximately 343km
	d := haversine(51.5085, -0.1257, 48.8566, 2.3522)
	if d < 300 || d > 400 {
		t.Errorf("haversine London-Paris = %f, want ~343km", d)
	}
}

func TestMin(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{-1, 1, -1},
	}
	for _, tt := range tests {
		if got := min(tt.a, tt.b); got != tt.want {
			t.Errorf("min(%d,%d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
