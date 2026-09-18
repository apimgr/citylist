package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/apimgr/citylist/src/cities"
	"github.com/apimgr/citylist/src/config"
)

func testServer(t *testing.T) *Server {
	t.Helper()

	jsonData := []byte(`[
		{"id":1,"name":"London","country":"GB","coord":{"lon":-0.1257,"lat":51.5085}},
		{"id":2,"name":"Paris","country":"FR","coord":{"lon":2.3522,"lat":48.8566}},
		{"id":3,"name":"Berlin","country":"DE","coord":{"lon":13.405,"lat":52.52}}
	]`)

	citySvc, err := cities.NewService(jsonData)
	if err != nil {
		t.Fatalf("cities.NewService() error: %v", err)
	}

	cfg := config.DefaultConfig()
	return New(citySvc, cfg, "127.0.0.1", "8080", "1.0.0-test", "2026-01-01", "abc123")
}

func doRequest(t *testing.T, s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/healthz", "/health", "/status"} {
		rec := doRequest(t, s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
}

func TestHandleAPIInfo(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1 = %d, want 200", rec.Code)
	}

	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Error("expected Success=true")
	}
}

func TestHandleGetCities(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/cities?page=1&limit=2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/cities = %d, want 200", rec.Code)
	}

	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("Count = %d, want 2", resp.Count)
	}
}

func TestHandleSearchCities(t *testing.T) {
	s := testServer(t)

	t.Run("valid query", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/search?q=lon", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("query too short", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/search?q=l", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleGetCityByID(t *testing.T) {
	s := testServer(t)

	t.Run("found", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/1", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("not found", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/999", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/abc", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleGetCitiesByCountry(t *testing.T) {
	s := testServer(t)

	t.Run("valid code", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/country/GB", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("invalid code length", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/country/GBR", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleFindNearestGET(t *testing.T) {
	s := testServer(t)

	t.Run("valid coordinates", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/coordinates?latitude=51.5&longitude=-0.12", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("invalid latitude", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/coordinates?latitude=abc&longitude=0", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("invalid longitude", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/coordinates?latitude=0&longitude=abc", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("latitude out of range", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/coordinates?latitude=999&longitude=0", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("longitude out of range", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/coordinates?latitude=0&longitude=999", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleFindNearestPOST(t *testing.T) {
	s := testServer(t)

	t.Run("valid body", func(t *testing.T) {
		body := []byte(`{"latitude":51.5,"longitude":-0.12}`)
		rec := doRequest(t, s, http.MethodPost, "/api/v1/cities/coordinates", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodPost, "/api/v1/cities/coordinates", []byte(`not json`))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("out of range", func(t *testing.T) {
		body := []byte(`{"latitude":999,"longitude":0}`)
		rec := doRequest(t, s, http.MethodPost, "/api/v1/cities/coordinates", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleFindNearby(t *testing.T) {
	s := testServer(t)

	t.Run("defaults", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/nearby?lat=51.5&lon=-0.12", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("with radius and limit", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/nearby?lat=51.5&lon=-0.12&radius=500&limit=5", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("invalid radius falls back to default", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/nearby?lat=51.5&lon=-0.12&radius=-5", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("invalid limit falls back to default", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/nearby?lat=51.5&lon=-0.12&limit=-5", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("invalid coordinates", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/nearby?lat=abc&lon=0", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleStats(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/stats", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleStatsTxt(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/stats.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("expected non-empty body")
	}
}

func TestHandleCount(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/count", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleCountTxt(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/count.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleRawData(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/data", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleRandomCity(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/random", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleRandomCityTxt(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/random.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleHomePage(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleSearchPage(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/search", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleCoordinatesPage(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/coordinates", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleManifest(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/manifest.json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleServiceWorker(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/sw.js", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleRobots(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/robots.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleSecurityTxt(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/security.txt", "/.well-known/security.txt"} {
		rec := doRequest(t, s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
}

func TestHandleOpenAPI(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/openapi", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleOpenAPISpec(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/openapi.json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestCORSMiddleware(t *testing.T) {
	s := testServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/cities", nil)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("OPTIONS request status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("CORS header = %q, want *", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/healthz", nil)

	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", rec.Header().Get("X-Frame-Options"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", rec.Header().Get("X-Content-Type-Options"))
	}
}

func TestStaticFiles(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/static/nonexistent.css", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status for missing static file = %d, want 404", rec.Code)
	}
}
