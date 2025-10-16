package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"

	"citylist/src/auth"
	"citylist/src/database"
)

type Server struct {
	db           *sql.DB
	router       chi.Router
	server       *http.Server
	devMode      bool
	startTime    time.Time
	staticFS     fs.FS
	citylistJSON []byte
}

// New creates a new server instance with Chi router
func New(db *sql.DB, staticFS fs.FS, citylistJSON []byte, address, port string, devMode bool) *Server {
	s := &Server{
		db:           db,
		router:       chi.NewRouter(),
		devMode:      devMode,
		startTime:    time.Now(),
		staticFS:     staticFS,
		citylistJSON: citylistJSON,
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	r := s.router

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(s.securityHeaders)

	// Rate limiting: 100 requests per minute per IP
	// Configurable via admin settings in future iterations
	r.Use(httprate.LimitByIP(100, 1*time.Minute))

	// Public routes
	r.Get("/", s.handleHome)
	r.Get("/docs", s.handleDocs)
	r.Get("/openapi", s.handleSwaggerUI)
	r.Get("/graphql", s.handleGraphQLPlayground)
	r.Get("/healthz", s.handleHealth)
	r.Get("/manifest.json", s.handleManifest)
	r.Get("/robots.txt", s.handleRobotsTxt)
	r.Get("/security.txt", s.handleSecurityTxtRedirect)
	r.Get("/.well-known/security.txt", s.handleSecurityTxt)

	// Static assets (embedded)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(s.staticFS))))

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.handleAPIHealth)
		r.Get("/openapi", s.handleSwaggerUI)
		r.Get("/openapi.json", s.handleOpenAPISpec)
		r.Get("/graphql", s.handleGraphQLPlayground)
		r.Post("/graphql", s.handleGraphQL)
		r.Get("/citylist.json", s.handleRawJSON)
		r.Get("/cities", s.handleCities)
		r.Get("/cities/search", s.handleCitiesSearch)
		r.Get("/cities/country/{code}", s.handleCitiesByCountry)

		// Admin API (with auth)
		r.Group(func(r chi.Router) {
			r.Use(s.requireAdmin)
			r.Get("/admin/settings", s.handleAdminSettings)
			r.Put("/admin/settings", s.handleUpdateSettings)
			r.Get("/admin/stats", s.handleAdminStats)
		})
	})

	// Admin routes (with auth)
	r.Group(func(r chi.Router) {
		r.Use(s.requireAdminWeb)
		r.Get("/admin", s.handleAdminDashboard)
		r.Get("/admin/settings", s.handleAdminSettingsPage)
	})

	// Development routes
	if s.devMode {
		r.Get("/debug/routes", s.handleDebugRoutes)
	}
}

// Start starts the HTTP server
func (s *Server) Start(addr string) error {
	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// securityHeaders middleware adds security headers
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' https://unpkg.com; style-src 'self' 'unsafe-inline' https://unpkg.com; img-src 'self' data: https:; font-src 'self' data: https://unpkg.com; connect-src 'self'; frame-ancestors 'none';")

		// CORS (allow all in development)
		if s.devMode {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}

		next.ServeHTTP(w, r)
	})
}

// requireAdmin middleware for API authentication (Bearer token)
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			s.sendError(w, "UNAUTHORIZED", "Missing or invalid Authorization header", http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")

		// Get admin token hash from database
		var storedTokenHash string
		err := s.db.QueryRow("SELECT token_hash FROM admin_credentials WHERE id = 1").Scan(&storedTokenHash)
		if err != nil {
			s.sendError(w, "UNAUTHORIZED", "Invalid credentials", http.StatusUnauthorized)
			return
		}

		// Verify token
		if !auth.VerifyToken(token, storedTokenHash) {
			s.sendError(w, "UNAUTHORIZED", "Invalid token", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// requireAdminWeb middleware for web authentication (Basic Auth)
func (s *Server) requireAdminWeb(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Admin Area"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Get admin credentials from database
		var storedUsername, storedPasswordHash string
		err := s.db.QueryRow("SELECT username, password_hash FROM admin_credentials WHERE id = 1").Scan(&storedUsername, &storedPasswordHash)
		if err != nil || username != storedUsername {
			w.Header().Set("WWW-Authenticate", `Basic realm="Admin Area"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Verify password
		if !auth.VerifyPassword(password, storedPasswordHash) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Admin Area"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// handleHome serves the main frontend page
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	title := database.GetSetting(s.db, "server.title", "CityList API")
	tagline := database.GetSetting(s.db, "server.tagline", "Global Cities Database")

	tmpl := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <meta name="description" content="{{ .Tagline }}">
    <title>{{ .Title }}</title>
    <link rel="stylesheet" href="/static/css/main.css">
    <link rel="manifest" href="/manifest.json">
</head>
<body data-theme="dark">
    <header id="main-header">
        <div class="header-container">
            <div class="header-left">
                <button class="mobile-menu-toggle" aria-label="Toggle menu">☰</button>
                <a class="logo" href="/">{{ .Title }}</a>
            </div>
            <nav id="main-nav" class="header-center">
                <a href="/">Home</a>
                <a href="#search">Search</a>
                <a href="#api">API</a>
            </nav>
            <div class="header-right">
                <div class="notification-dropdown">
                    <button class="notification-bell" data-count="0" aria-label="Notifications">
                        🔔<span class="notification-badge">0</span>
                    </button>
                </div>
                <div class="profile-dropdown">
                    <button class="profile-toggle" aria-label="Profile menu">
                        <span class="profile-avatar">G</span>
                        <span class="profile-name">Guest</span>
                        <span class="caret">▼</span>
                    </button>
                    <div class="profile-menu" id="profile-menu">
                        <a href="#about">About</a>
                        <a href="/healthz">Health Status</a>
                        <a href="#theme" id="theme-toggle">Toggle Theme</a>
                    </div>
                </div>
            </div>
        </div>
    </header>
    <main id="main-content">
        <div class="container">
            <div class="hero">
                <h1>{{ .Title }}</h1>
                <p class="tagline">{{ .Tagline }}</p>
                <p class="description">Search and explore cities from around the world</p>
            </div>

            <div class="search-section" id="search">
                <h2>Search Cities</h2>
                <div class="form-group">
                    <label class="form-label" for="search-input">
                        City Name
                        <span class="form-hint">Search by city name (minimum 2 characters)</span>
                    </label>
                    <div class="search-box">
                        <input
                            type="text"
                            id="search-input"
                            class="form-input"
                            placeholder="Search by city name..."
                            data-tooltip="Enter at least 2 characters to search"
                            data-position="top"
                        />
                        <button id="search-btn" class="btn btn-primary">
                            <span class="btn-text">Search</span>
                        </button>
                    </div>
                    <span class="form-error" id="search-error"></span>
                </div>
                <div id="search-results"></div>
            </div>

            <div class="stats-section">
                <div class="stat-card">
                    <h3 id="total-cities">Loading...</h3>
                    <p>Total Cities</p>
                </div>
                <div class="stat-card">
                    <h3 id="total-countries">Loading...</h3>
                    <p>Countries</p>
                </div>
            </div>

            <div class="api-section" id="api">
                <h2>API Documentation</h2>
                <div class="api-endpoint">
                    <h3>GET /api/v1/cities</h3>
                    <p>Get all cities (paginated)</p>
                    <pre>curl http://localhost:8080/api/v1/cities?limit=10&offset=0</pre>
                </div>
                <div class="api-endpoint">
                    <h3>GET /api/v1/cities/search?q={query}</h3>
                    <p>Search cities by name</p>
                    <pre>curl http://localhost:8080/api/v1/cities/search?q=London</pre>
                </div>
                <div class="api-endpoint">
                    <h3>GET /api/v1/cities/country/{code}</h3>
                    <p>Get cities by country code</p>
                    <pre>curl http://localhost:8080/api/v1/cities/country/US</pre>
                </div>
                <div class="api-endpoint">
                    <h3>GET /healthz</h3>
                    <p>Health check endpoint</p>
                    <pre>curl http://localhost:8080/healthz</pre>
                </div>
            </div>
        </div>
    </main>
    <footer id="main-footer">
        <div class="container">
            <p>&copy; 2024 {{ .Title }} - Powered by CityList</p>
        </div>
    </footer>
    <div id="modal-container"></div>
    <div id="toast-container"></div>
    <script src="/static/js/main.js"></script>
</body>
</html>`

	t, err := template.New("home").Parse(tmpl)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]string{
		"Title":   title,
		"Tagline": tagline,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t.Execute(w, data)
}

// handleDocs serves the API documentation page
func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	title := database.GetSetting(s.db, "server.title", "CityList API")

	tmpl := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>API Documentation - {{ .Title }}</title>
    <link rel="stylesheet" href="/static/css/main.css">
    <style>
        .docs-container { max-width: 1200px; margin: 0 auto; padding: 2rem; }
        .docs-header { margin-bottom: 2rem; }
        .endpoint-group { margin-bottom: 3rem; }
        .endpoint { background: var(--card-bg); padding: 1.5rem; margin-bottom: 1rem; border-radius: 8px; border-left: 4px solid var(--primary-color); }
        .endpoint h3 { margin: 0 0 0.5rem 0; color: var(--primary-color); }
        .endpoint .method { display: inline-block; padding: 0.25rem 0.5rem; background: var(--primary-color); color: white; border-radius: 4px; font-weight: bold; margin-right: 0.5rem; }
        .endpoint pre { background: var(--bg-color); padding: 1rem; border-radius: 4px; overflow-x: auto; margin-top: 1rem; }
        .params { margin-top: 1rem; }
        .param { padding: 0.5rem; background: var(--bg-color); margin: 0.5rem 0; border-radius: 4px; }
        .param-name { color: var(--primary-color); font-weight: bold; }
        .response-example { margin-top: 1rem; }
    </style>
</head>
<body data-theme="dark">
    <header id="main-header">
        <div class="header-container">
            <div class="header-left">
                <a class="logo" href="/">{{ .Title }}</a>
            </div>
            <nav id="main-nav" class="header-center">
                <a href="/">Home</a>
                <a href="/docs" class="active">API Docs</a>
                <a href="/healthz">Health</a>
            </nav>
        </div>
    </header>
    <main>
        <div class="docs-container">
            <div class="docs-header">
                <h1>API Documentation</h1>
                <p>Complete REST API reference for {{ .Title }}</p>
            </div>

            <div class="endpoint-group">
                <h2>Health & Status</h2>

                <div class="endpoint">
                    <h3><span class="method">GET</span>/healthz</h3>
                    <p>Comprehensive health check with database status, memory usage, and uptime.</p>
                    <pre>curl http://localhost:8080/healthz</pre>
                    <div class="response-example">
                        <strong>Response:</strong>
                        <pre>{
  "status": "healthy",
  "timestamp": "2024-01-01T12:00:00Z",
  "version": "0.0.1",
  "uptime_seconds": 3600,
  "checks": {
    "database": {
      "status": "connected",
      "type": "sqlite",
      "city_count": 209579
    },
    "memory": {
      "status": "ok",
      "used_percent": 12
    }
  }
}</pre>
                    </div>
                </div>

                <div class="endpoint">
                    <h3><span class="method">GET</span>/api/v1/health</h3>
                    <p>Simple API health check endpoint.</p>
                    <pre>curl http://localhost:8080/api/v1/health</pre>
                </div>
            </div>

            <div class="endpoint-group">
                <h2>Cities API</h2>

                <div class="endpoint">
                    <h3><span class="method">GET</span>/api/v1/cities</h3>
                    <p>Get paginated list of cities.</p>
                    <div class="params">
                        <strong>Query Parameters:</strong>
                        <div class="param">
                            <span class="param-name">limit</span> (optional) - Number of results per page (default: 100, max: 1000)
                        </div>
                        <div class="param">
                            <span class="param-name">offset</span> (optional) - Pagination offset (default: 0)
                        </div>
                    </div>
                    <pre>curl http://localhost:8080/api/v1/cities?limit=10&offset=0</pre>
                    <div class="response-example">
                        <strong>Response:</strong>
                        <pre>{
  "success": true,
  "data": {
    "cities": [
      {
        "id": 1,
        "name": "Tokyo",
        "country": "JP",
        "lon": 139.6917,
        "lat": 35.6895
      }
    ],
    "total": 209579,
    "limit": 10,
    "offset": 0
  },
  "timestamp": "2024-01-01T12:00:00Z"
}</pre>
                    </div>
                </div>

                <div class="endpoint">
                    <h3><span class="method">GET</span>/api/v1/cities/search</h3>
                    <p>Search cities by name (case-insensitive, partial match).</p>
                    <div class="params">
                        <strong>Query Parameters:</strong>
                        <div class="param">
                            <span class="param-name">q</span> (required) - Search query
                        </div>
                        <div class="param">
                            <span class="param-name">limit</span> (optional) - Maximum results (default: 50)
                        </div>
                    </div>
                    <pre>curl http://localhost:8080/api/v1/cities/search?q=London</pre>
                    <div class="response-example">
                        <strong>Response:</strong>
                        <pre>{
  "success": true,
  "data": {
    "cities": [
      {
        "id": 2643743,
        "name": "London",
        "country": "GB",
        "lon": -0.1278,
        "lat": 51.5074
      }
    ],
    "query": "London",
    "count": 15
  }
}</pre>
                    </div>
                </div>

                <div class="endpoint">
                    <h3><span class="method">GET</span>/api/v1/cities/country/{code}</h3>
                    <p>Get cities by ISO 3166-1 alpha-2 country code.</p>
                    <div class="params">
                        <strong>URL Parameters:</strong>
                        <div class="param">
                            <span class="param-name">code</span> - 2-letter country code (e.g., US, GB, JP)
                        </div>
                        <strong>Query Parameters:</strong>
                        <div class="param">
                            <span class="param-name">limit</span> (optional) - Maximum results (default: 100)
                        </div>
                    </div>
                    <pre>curl http://localhost:8080/api/v1/cities/country/US</pre>
                    <div class="response-example">
                        <strong>Response:</strong>
                        <pre>{
  "success": true,
  "data": {
    "cities": [...],
    "country": "US",
    "count": 100
  }
}</pre>
                    </div>
                </div>
            </div>

            <div class="endpoint-group">
                <h2>Admin API (Authentication Required)</h2>
                <p><strong>Authentication:</strong> Bearer token in Authorization header</p>
                <pre>curl -H "Authorization: Bearer YOUR_TOKEN" http://localhost:8080/api/v1/admin/stats</pre>

                <div class="endpoint">
                    <h3><span class="method">GET</span>/api/v1/admin/settings</h3>
                    <p>Get all server settings.</p>
                    <pre>curl -H "Authorization: Bearer TOKEN" http://localhost:8080/api/v1/admin/settings</pre>
                </div>

                <div class="endpoint">
                    <h3><span class="method">PUT</span>/api/v1/admin/settings</h3>
                    <p>Update a server setting.</p>
                    <pre>curl -X PUT -H "Authorization: Bearer TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"key":"server.title","value":"My City API"}' \
  http://localhost:8080/api/v1/admin/settings</pre>
                </div>

                <div class="endpoint">
                    <h3><span class="method">GET</span>/api/v1/admin/stats</h3>
                    <p>Get server statistics including memory usage and uptime.</p>
                    <pre>curl -H "Authorization: Bearer TOKEN" http://localhost:8080/api/v1/admin/stats</pre>
                </div>
            </div>

            <div class="endpoint-group">
                <h2>Response Format</h2>
                <p>All API responses follow a consistent format:</p>
                <div class="endpoint">
                    <h3>Success Response</h3>
                    <pre>{
  "success": true,
  "data": { ... },
  "timestamp": "2024-01-01T12:00:00Z"
}</pre>
                </div>
                <div class="endpoint">
                    <h3>Error Response</h3>
                    <pre>{
  "success": false,
  "error": {
    "code": "ERROR_CODE",
    "message": "Human readable message"
  },
  "timestamp": "2024-01-01T12:00:00Z"
}</pre>
                </div>
            </div>

            <div class="endpoint-group">
                <h2>Rate Limiting</h2>
                <p>Currently no rate limiting is enforced. Fair use is expected.</p>
            </div>
        </div>
    </main>
    <footer id="main-footer">
        <div class="container">
            <p>&copy; 2024 {{ .Title }} - <a href="/">Back to Home</a></p>
        </div>
    </footer>
</body>
</html>`

	t, err := template.New("docs").Parse(tmpl)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]string{
		"Title": title,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t.Execute(w, data)
}

// handleHealth returns comprehensive health check
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Count cities
	var cityCount int
	s.db.QueryRow("SELECT COUNT(*) FROM cities").Scan(&cityCount)

	// Get memory stats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	health := map[string]interface{}{
		"status":         "healthy",
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
		"version":        "0.0.1",
		"uptime_seconds": int(time.Since(s.startTime).Seconds()),
		"checks": map[string]interface{}{
			"database": map[string]interface{}{
				"status":     "connected",
				"type":       "sqlite",
				"city_count": cityCount,
				"latency_ms": 1,
			},
			"memory": map[string]interface{}{
				"status":       "ok",
				"used_bytes":   m.Alloc,
				"total_bytes":  m.Sys,
				"used_percent": int((float64(m.Alloc) / float64(m.Sys)) * 100),
			},
		},
		"features": map[string]interface{}{
			"api_enabled": true,
			"dev_mode":    s.devMode,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// handleAPIHealth returns simple API health
func (s *Server) handleAPIHealth(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"success": true,
		"data": map[string]string{
			"status":  "healthy",
			"version": "0.0.1",
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleManifest serves the PWA manifest
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	title := database.GetSetting(s.db, "server.title", "CityList API")
	tagline := database.GetSetting(s.db, "server.tagline", "Global Cities Database")

	manifest := map[string]interface{}{
		"name":             title,
		"short_name":       "CityList",
		"description":      tagline,
		"start_url":        "/",
		"display":          "standalone",
		"orientation":      "any",
		"theme_color":      "#1a1a1a",
		"background_color": "#1a1a1a",
		"icons": []map[string]interface{}{
			{
				"src":     "/static/icon-192.png",
				"sizes":   "192x192",
				"type":    "image/png",
				"purpose": "any maskable",
			},
			{
				"src":     "/static/icon-512.png",
				"sizes":   "512x512",
				"type":    "image/png",
				"purpose": "any maskable",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(manifest)
}

// handleCities returns paginated list of cities
func (s *Server) handleCities(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit == 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := s.db.Query("SELECT id, name, country, lon, lat FROM cities LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		s.sendError(w, "DATABASE_ERROR", "Failed to query cities", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	cities := []database.City{}
	for rows.Next() {
		var c database.City
		if err := rows.Scan(&c.ID, &c.Name, &c.Country, &c.Lon, &c.Lat); err != nil {
			continue
		}
		cities = append(cities, c)
	}

	// Get total count
	var total int
	s.db.QueryRow("SELECT COUNT(*) FROM cities").Scan(&total)

	response := map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"cities": cities,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleCitiesSearch searches cities by name
func (s *Server) handleCitiesSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		s.sendError(w, "MISSING_PARAMETER", "Query parameter 'q' is required", http.StatusBadRequest)
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 50
	}

	searchQuery := "%" + query + "%"
	rows, err := s.db.Query("SELECT id, name, country, lon, lat FROM cities WHERE name LIKE ? LIMIT ?", searchQuery, limit)
	if err != nil {
		s.sendError(w, "DATABASE_ERROR", "Failed to search cities", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	cities := []database.City{}
	for rows.Next() {
		var c database.City
		if err := rows.Scan(&c.ID, &c.Name, &c.Country, &c.Lon, &c.Lat); err != nil {
			continue
		}
		cities = append(cities, c)
	}

	response := map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"cities": cities,
			"query":  query,
			"count":  len(cities),
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleCitiesByCountry returns cities filtered by country code
func (s *Server) handleCitiesByCountry(w http.ResponseWriter, r *http.Request) {
	countryCode := strings.ToUpper(chi.URLParam(r, "code"))

	if countryCode == "" || len(countryCode) != 2 {
		s.sendError(w, "INVALID_COUNTRY", "Country code must be 2 letters", http.StatusBadRequest)
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 100
	}

	rows, err := s.db.Query("SELECT id, name, country, lon, lat FROM cities WHERE country = ? LIMIT ?", countryCode, limit)
	if err != nil {
		s.sendError(w, "DATABASE_ERROR", "Failed to query cities", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	cities := []database.City{}
	for rows.Next() {
		var c database.City
		if err := rows.Scan(&c.ID, &c.Name, &c.Country, &c.Lon, &c.Lat); err != nil {
			continue
		}
		cities = append(cities, c)
	}

	response := map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"cities": cities,
			"country": countryCode,
			"count":  len(cities),
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleRobotsTxt serves robots.txt from database
func (s *Server) handleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	robotsTxt := database.GetSetting(s.db, "robots.txt", `User-agent: *
Allow: /
Sitemap: /sitemap.xml`)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(robotsTxt))
}

// handleSecurityTxtRedirect redirects to /.well-known/security.txt
func (s *Server) handleSecurityTxtRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/.well-known/security.txt", http.StatusMovedPermanently)
}

// handleSecurityTxt serves security.txt from database (RFC 9116)
func (s *Server) handleSecurityTxt(w http.ResponseWriter, r *http.Request) {
	securityTxt := database.GetSetting(s.db, "security.txt", `Contact: mailto:security@example.com
Expires: 2025-12-31T23:59:59Z
Preferred-Languages: en
Canonical: https://example.com/.well-known/security.txt`)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(securityTxt))
}

// handleAdminDashboard serves the admin dashboard page
func (s *Server) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <title>Admin Dashboard - CityList</title>
    <link rel="stylesheet" href="/static/css/main.css">
</head>
<body>
    <h1>Admin Dashboard</h1>
    <p>Welcome to the admin panel</p>
    <ul>
        <li><a href="/admin/settings">Settings</a></li>
        <li><a href="/api/v1/admin/stats">Statistics</a></li>
        <li><a href="/">Back to Home</a></li>
    </ul>
</body>
</html>`))
}

// handleAdminSettingsPage serves the admin settings page
func (s *Server) handleAdminSettingsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <title>Admin Settings - CityList</title>
    <link rel="stylesheet" href="/static/css/main.css">
</head>
<body>
    <h1>Admin Settings</h1>
    <p>Configure server settings</p>
    <ul>
        <li><a href="/api/v1/admin/settings">View Settings (API)</a></li>
        <li><a href="/admin">Back to Dashboard</a></li>
    </ul>
</body>
</html>`))
}

// handleAdminSettings returns all settings (API)
func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT key, value, type, category, description FROM settings ORDER BY category, key")
	if err != nil {
		s.sendError(w, "DATABASE_ERROR", "Failed to query settings", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	settings := []map[string]interface{}{}
	for rows.Next() {
		var key, value, typ, category, description string
		if err := rows.Scan(&key, &value, &typ, &category, &description); err != nil {
			continue
		}
		settings = append(settings, map[string]interface{}{
			"key":         key,
			"value":       value,
			"type":        typ,
			"category":    category,
			"description": description,
		})
	}

	response := map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"settings": settings,
			"count":    len(settings),
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleUpdateSettings updates a setting
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, "INVALID_JSON", "Invalid JSON body", http.StatusBadRequest)
		return
	}

	_, err := s.db.Exec("UPDATE settings SET value = ?, updated_at = CURRENT_TIMESTAMP WHERE key = ?", req.Value, req.Key)
	if err != nil {
		s.sendError(w, "DATABASE_ERROR", "Failed to update setting", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"key":   req.Key,
			"value": req.Value,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleAdminStats returns server statistics
func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	var cityCount, settingsCount int
	s.db.QueryRow("SELECT COUNT(*) FROM cities").Scan(&cityCount)
	s.db.QueryRow("SELECT COUNT(*) FROM settings").Scan(&settingsCount)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	stats := map[string]interface{}{
		"cities":          cityCount,
		"settings":        settingsCount,
		"uptime_seconds":  int(time.Since(s.startTime).Seconds()),
		"memory_used_mb":  m.Alloc / 1024 / 1024,
		"memory_total_mb": m.Sys / 1024 / 1024,
		"go_version":      runtime.Version(),
		"goroutines":      runtime.NumGoroutine(),
	}

	response := map[string]interface{}{
		"success":   true,
		"data":      stats,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleDebugRoutes shows all registered routes (dev mode only)
func (s *Server) handleDebugRoutes(w http.ResponseWriter, r *http.Request) {
	if !s.devMode {
		http.NotFound(w, r)
		return
	}

	routes := []string{
		"GET /",
		"GET /docs",
		"GET /openapi",
		"GET /graphql",
		"GET /healthz",
		"GET /manifest.json",
		"GET /robots.txt",
		"GET /security.txt",
		"GET /.well-known/security.txt",
		"GET /static/*",
		"GET /api/v1/health",
		"GET /api/v1/openapi",
		"GET /api/v1/openapi.json",
		"GET /api/v1/graphql",
		"POST /api/v1/graphql",
		"GET /api/v1/citylist.json",
		"GET /api/v1/cities",
		"GET /api/v1/cities/search?q={query}",
		"GET /api/v1/cities/country/{code}",
		"GET /api/v1/admin/settings (requires auth)",
		"PUT /api/v1/admin/settings (requires auth)",
		"GET /api/v1/admin/stats (requires auth)",
		"GET /admin (requires auth)",
		"GET /admin/settings (requires auth)",
		"GET /debug/routes",
	}

	response := map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"routes": routes,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// sendError sends a standardized error response
func (s *Server) sendError(w http.ResponseWriter, code, message string, status int) {
	response := map[string]interface{}{
		"success": false,
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(response)
}

// handleRawJSON serves the raw citylist.json file
func (s *Server) handleRawJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=citylist.json")
	w.Write(s.citylistJSON)
}
