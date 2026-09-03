package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/admin"
	"github.com/apimgr/citylist/src/cities"
	"github.com/apimgr/citylist/src/config"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed static/*
var staticFS embed.FS

//go:embed templates/*
var templateFS embed.FS

// Build info (set from main)
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Server represents the HTTP server
type Server struct {
	router       *chi.Mux
	citySvc      *cities.Service
	cfg          *config.Config
	address      string
	port         string
	version      string
	buildDate    string
	commit       string
	adminHandler *admin.Handler
}

// APIResponse is the standard JSON response format
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Count   int         `json:"count,omitempty"`
	Error   *APIError   `json:"error,omitempty"`
}

// APIError represents an error response
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// PaginatedResponse includes pagination info
type PaginatedResponse struct {
	Cities     []cities.City  `json:"cities"`
	Pagination PaginationInfo `json:"pagination"`
}

// PaginationInfo contains pagination metadata
type PaginationInfo struct {
	Page       int  `json:"page"`
	Limit      int  `json:"limit"`
	Total      int  `json:"total"`
	TotalPages int  `json:"total_pages"`
	HasNext    bool `json:"has_next"`
	HasPrev    bool `json:"has_prev"`
}

// New creates a new server instance
func New(citySvc *cities.Service, cfg *config.Config, address, port, version, buildDate, commit string) *Server {
	// Create admin handler
	adminHandler := admin.NewHandler(
		cfg.Server.Admin.Username,
		cfg.Server.Admin.Password,
		cfg.Server.Admin.APIToken,
		cfg.Server.Session.Timeout,
		false, // SSL enabled
		version,
		commit,
		buildDate,
	)

	s := &Server{
		router:       chi.NewRouter(),
		citySvc:      citySvc,
		cfg:          cfg,
		address:      address,
		port:         port,
		version:      version,
		buildDate:    buildDate,
		commit:       commit,
		adminHandler: adminHandler,
	}
	s.setupRoutes()
	return s
}

// Router returns the HTTP router
func (s *Server) Router() http.Handler {
	return s.router
}

// Run starts the HTTP server with graceful shutdown support
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.router,
	}

	errChan := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}

func (s *Server) setupRoutes() {
	r := s.router

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(s.corsMiddleware)
	r.Use(s.securityHeadersMiddleware)

	// Static files
	staticContent, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))

	// Health check endpoints
	r.Get("/healthz", s.handleHealthz)
	r.Get("/health", s.handleHealthz)
	r.Get("/status", s.handleHealthz)

	// Web routes
	r.Get("/", s.handleHome)
	r.Get("/search", s.handleSearchPage)
	r.Get("/coordinates", s.handleCoordinatesPage)

	// PWA routes
	r.Get("/manifest.json", s.handleManifest)
	r.Get("/sw.js", s.handleServiceWorker)
	r.Get("/robots.txt", s.handleRobots)
	r.Get("/security.txt", s.handleSecurityTxt)
	r.Get("/.well-known/security.txt", s.handleSecurityTxt)

	// OpenAPI/Swagger
	r.Get("/openapi", s.handleOpenAPI)
	r.Get("/api/v1/openapi.json", s.handleOpenAPISpec)

	// API routes
	r.Route("/api/v1", func(r chi.Router) {
		// Info
		r.Get("/", s.handleAPIInfo)

		// Cities
		r.Get("/cities", s.handleGetCities)
		r.Get("/cities/search", s.handleSearchCities)
		r.Get("/cities/{id}", s.handleGetCityByID)
		r.Get("/cities/country/{code}", s.handleGetCitiesByCountry)
		r.Get("/cities/coordinates", s.handleFindNearestGET)
		r.Post("/cities/coordinates", s.handleFindNearestPOST)
		r.Get("/cities/nearby", s.handleFindNearby)

		// Stats
		r.Get("/stats", s.handleStats)
		r.Get("/stats.txt", s.handleStatsTxt)
		r.Get("/count", s.handleCount)
		r.Get("/count.txt", s.handleCountTxt)
	})

	// Raw data endpoint
	r.Get("/api/data", s.handleRawData)

	// Shorthand routes
	r.Get("/random", s.handleRandomCity)
	r.Get("/random.txt", s.handleRandomCityTxt)

	// Admin routes (session auth for web, bearer token for API)
	s.adminHandler.RegisterRoutes(r)
}

// Middleware

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cors := s.cfg.WebSecurity.CORS
		if cors == "" {
			cors = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", cors)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// Response helpers

func (s *Server) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) respondSuccess(w http.ResponseWriter, data interface{}, count int) {
	s.respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    data,
		Count:   count,
	})
}

func (s *Server) respondError(w http.ResponseWriter, status int, code, message string) {
	s.respondJSON(w, status, APIResponse{
		Success: false,
		Error: &APIError{
			Code:    code,
			Message: message,
		},
	})
}

func (s *Server) respondText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(text))
}

// Handlers

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"version":   s.version,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleAPIInfo(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"name":        "Citylist API",
		"version":     s.version,
		"description": "World City Database API with 200,000+ cities",
		"endpoints": []string{
			"GET /api/v1/cities",
			"GET /api/v1/cities/search?q={query}",
			"GET /api/v1/cities/{id}",
			"GET /api/v1/cities/country/{code}",
			"GET /api/v1/cities/coordinates?latitude={lat}&longitude={lon}",
			"POST /api/v1/cities/coordinates",
			"GET /api/v1/stats",
			"GET /api/data",
		},
	}
	s.respondSuccess(w, info, 0)
}

func (s *Server) handleGetCities(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}

	cityList, total := s.citySvc.GetAll(page, limit)
	totalPages := (total + limit - 1) / limit

	response := PaginatedResponse{
		Cities: cityList,
		Pagination: PaginationInfo{
			Page:       page,
			Limit:      limit,
			Total:      total,
			TotalPages: totalPages,
			HasNext:    page < totalPages,
			HasPrev:    page > 1,
		},
	}

	s.respondSuccess(w, response, len(cityList))
}

func (s *Server) handleSearchCities(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if len(query) < 2 {
		s.respondError(w, http.StatusBadRequest, "INVALID_QUERY", "Search query must be at least 2 characters")
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 20
	}

	results := s.citySvc.Search(query, limit)
	s.respondSuccess(w, results, len(results))
}

func (s *Server) handleGetCityByID(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_ID", "City ID must be a number")
		return
	}

	city, found := s.citySvc.GetByID(id)
	if !found {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", "City not found")
		return
	}

	s.respondSuccess(w, city, 1)
}

func (s *Server) handleGetCitiesByCountry(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if len(code) != 2 {
		s.respondError(w, http.StatusBadRequest, "INVALID_CODE", "Country code must be 2 characters (ISO 3166-1 alpha-2)")
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 50
	}

	cityList := s.citySvc.GetByCountry(code, limit)
	s.respondSuccess(w, cityList, len(cityList))
}

func (s *Server) handleFindNearestGET(w http.ResponseWriter, r *http.Request) {
	latStr := r.URL.Query().Get("latitude")
	lonStr := r.URL.Query().Get("longitude")

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LATITUDE", "Invalid latitude value")
		return
	}

	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LONGITUDE", "Invalid longitude value")
		return
	}

	if lat < -90 || lat > 90 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", "Latitude must be between -90 and 90")
		return
	}
	if lon < -180 || lon > 180 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", "Longitude must be between -180 and 180")
		return
	}

	nearest, found := s.citySvc.FindNearest(lat, lon)
	if !found {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", "No cities found")
		return
	}

	result := map[string]interface{}{
		"city":     nearest.City,
		"distance": nearest.Distance,
		"coordinates": map[string]float64{
			"latitude":  lat,
			"longitude": lon,
		},
	}
	s.respondSuccess(w, result, 1)
}

func (s *Server) handleFindNearestPOST(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_JSON", "Invalid JSON body")
		return
	}

	if req.Latitude < -90 || req.Latitude > 90 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", "Latitude must be between -90 and 90")
		return
	}
	if req.Longitude < -180 || req.Longitude > 180 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", "Longitude must be between -180 and 180")
		return
	}

	nearest, found := s.citySvc.FindNearest(req.Latitude, req.Longitude)
	if !found {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", "No cities found")
		return
	}

	result := map[string]interface{}{
		"city":     nearest.City,
		"distance": nearest.Distance,
		"coordinates": map[string]float64{
			"latitude":  req.Latitude,
			"longitude": req.Longitude,
		},
	}
	s.respondSuccess(w, result, 1)
}

func (s *Server) handleFindNearby(w http.ResponseWriter, r *http.Request) {
	latStr := r.URL.Query().Get("lat")
	lonStr := r.URL.Query().Get("lon")
	radiusStr := r.URL.Query().Get("radius")
	limitStr := r.URL.Query().Get("limit")

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LATITUDE", "Invalid latitude value")
		return
	}

	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LONGITUDE", "Invalid longitude value")
		return
	}

	if lat < -90 || lat > 90 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", "Latitude must be between -90 and 90")
		return
	}
	if lon < -180 || lon > 180 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", "Longitude must be between -180 and 180")
		return
	}

	radius := 50.0 // default 50km
	if radiusStr != "" {
		radius, err = strconv.ParseFloat(radiusStr, 64)
		if err != nil || radius <= 0 {
			radius = 50.0
		}
	}

	limit := 10
	if limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit < 1 {
			limit = 10
		}
	}

	results := s.citySvc.FindNearby(lat, lon, radius, limit)
	s.respondJSON(w, http.StatusOK, results)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]interface{}{
		"total_cities":    s.citySvc.Count(),
		"total_countries": s.citySvc.CountryCount(),
		"version":         s.version,
		"data_source":     "GeoNames",
	}
	s.respondSuccess(w, stats, 0)
}

func (s *Server) handleStatsTxt(w http.ResponseWriter, r *http.Request) {
	text := fmt.Sprintf(`Citylist API Statistics
=======================
Total Cities: %d
Total Countries: %d
Version: %s
Data Source: GeoNames
`, s.citySvc.Count(), s.citySvc.CountryCount(), s.version)
	s.respondText(w, text)
}

func (s *Server) handleCount(w http.ResponseWriter, r *http.Request) {
	s.respondSuccess(w, map[string]int{"count": s.citySvc.Count()}, 0)
}

func (s *Server) handleCountTxt(w http.ResponseWriter, r *http.Request) {
	s.respondText(w, fmt.Sprintf("%d\n", s.citySvc.Count()))
}

func (s *Server) handleRawData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(s.citySvc.GetRaw())
}

func (s *Server) handleRandomCity(w http.ResponseWriter, r *http.Request) {
	// Get a random city by using current time as seed
	cityList, _ := s.citySvc.GetAll(1, s.citySvc.Count())
	if len(cityList) == 0 {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", "No cities available")
		return
	}

	index := int(time.Now().UnixNano()) % len(cityList)
	s.respondSuccess(w, cityList[index], 1)
}

func (s *Server) handleRandomCityTxt(w http.ResponseWriter, r *http.Request) {
	cityList, _ := s.citySvc.GetAll(1, s.citySvc.Count())
	if len(cityList) == 0 {
		s.respondText(w, "No cities available\n")
		return
	}

	index := int(time.Now().UnixNano()) % len(cityList)
	city := cityList[index]
	text := fmt.Sprintf(`City: %s
Country: %s
ID: %d
Latitude: %.6f
Longitude: %.6f
`, city.Name, city.Country, city.ID, city.Coord.Lat, city.Coord.Lon)
	s.respondText(w, text)
}

// Web handlers

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	tmpl, err := templateFS.ReadFile("templates/home.html")
	if err != nil {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	html := strings.ReplaceAll(string(tmpl), "{{.Version}}", s.version)
	html = strings.ReplaceAll(html, "{{.TotalCities}}", fmt.Sprintf("%d", s.citySvc.Count()))
	html = strings.ReplaceAll(html, "{{.TotalCountries}}", fmt.Sprintf("%d", s.citySvc.CountryCount()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

func (s *Server) handleSearchPage(w http.ResponseWriter, r *http.Request) {
	tmpl, err := templateFS.ReadFile("templates/search.html")
	if err != nil {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(tmpl)
}

func (s *Server) handleCoordinatesPage(w http.ResponseWriter, r *http.Request) {
	tmpl, err := templateFS.ReadFile("templates/coordinates.html")
	if err != nil {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(tmpl)
}

// PWA/Meta handlers

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	manifest := map[string]interface{}{
		"name":             "Citylist API",
		"short_name":       "Citylist",
		"description":      "World City Database API",
		"start_url":        "/",
		"display":          "standalone",
		"background_color": "#1a1a1a",
		"theme_color":      "#0066cc",
		"icons": []map[string]interface{}{
			{"src": "/static/images/icon-192.png", "sizes": "192x192", "type": "image/png"},
			{"src": "/static/images/icon-512.png", "sizes": "512x512", "type": "image/png"},
		},
	}
	s.respondJSON(w, http.StatusOK, manifest)
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	sw := `self.addEventListener('install', (event) => { self.skipWaiting(); });
self.addEventListener('fetch', (event) => { event.respondWith(fetch(event.request)); });`
	w.Header().Set("Content-Type", "application/javascript")
	w.Write([]byte(sw))
}

func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	var sb strings.Builder
	sb.WriteString("User-agent: *\n")
	for _, path := range s.cfg.WebRobots.Allow {
		sb.WriteString(fmt.Sprintf("Allow: %s\n", path))
	}
	for _, path := range s.cfg.WebRobots.Deny {
		sb.WriteString(fmt.Sprintf("Disallow: %s\n", path))
	}
	s.respondText(w, sb.String())
}

func (s *Server) handleSecurityTxt(w http.ResponseWriter, r *http.Request) {
	text := fmt.Sprintf(`Contact: mailto:%s
Preferred-Languages: en
Canonical: https://%s/.well-known/security.txt
`, s.cfg.WebSecurity.Admin, s.cfg.Server.FQDN)
	s.respondText(w, text)
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	tmpl, err := templateFS.ReadFile("templates/openapi.html")
	if err != nil {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(tmpl)
}

func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	spec := map[string]interface{}{
		"openapi": "3.0.0",
		"info": map[string]interface{}{
			"title":       "Citylist API",
			"description": "World City Database API with 200,000+ cities",
			"version":     s.version,
		},
		"servers": []map[string]string{
			{"url": fmt.Sprintf("http://%s:%s", s.address, s.port)},
		},
		"paths": map[string]interface{}{
			"/api/v1/cities": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "List all cities",
					"description": "Get paginated list of cities",
					"parameters": []map[string]interface{}{
						{"name": "page", "in": "query", "schema": map[string]string{"type": "integer"}},
						{"name": "limit", "in": "query", "schema": map[string]string{"type": "integer"}},
					},
				},
			},
			"/api/v1/cities/search": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Search cities",
					"description": "Search cities by name",
					"parameters": []map[string]interface{}{
						{"name": "q", "in": "query", "required": true, "schema": map[string]string{"type": "string"}},
						{"name": "limit", "in": "query", "schema": map[string]string{"type": "integer"}},
					},
				},
			},
			"/api/v1/cities/{id}": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Get city by ID",
					"description": "Get a single city by its GeoNames ID",
					"parameters": []map[string]interface{}{
						{"name": "id", "in": "path", "required": true, "schema": map[string]string{"type": "integer"}},
					},
				},
			},
		},
	}
	s.respondJSON(w, http.StatusOK, spec)
}
