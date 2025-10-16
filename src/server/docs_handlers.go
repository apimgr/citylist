package server

import (
	"encoding/json"
	"html/template"
	"net/http"
)

// handleSwaggerUI serves the Swagger UI for API documentation with site theme
func (s *Server) handleSwaggerUI(w http.ResponseWriter, r *http.Request) {
	tmpl := `<!DOCTYPE html>
<html lang="en" data-theme="dark">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>API Documentation - CityList API</title>
    <link rel="stylesheet" href="/static/css/main.css">
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui.css">
    <style>
        body { margin: 0; padding: 0; display: flex; flex-direction: column; min-height: 100vh; }
        #swagger-container { flex: 1; }
        .swagger-ui { background: var(--bg-primary); }
        .swagger-ui .topbar { display: none; }
        .swagger-ui .info { color: var(--text-primary); }
        .swagger-ui .scheme-container { background: var(--bg-secondary); }
        .swagger-ui .opblock { background: var(--bg-secondary); border-color: var(--border-color); }
        .swagger-ui .opblock-tag { color: var(--text-primary); border-color: var(--border-color); }
        .swagger-ui .opblock-summary { background: var(--bg-tertiary); }
        .swagger-ui .opblock-description { color: var(--text-secondary); }
        .swagger-ui table thead tr td, .swagger-ui table thead tr th { color: var(--text-primary); border-color: var(--border-color); }
        .swagger-ui .parameter__name { color: var(--accent-primary); }
        .swagger-ui .response-col_status { color: var(--accent-success); }
        .swagger-ui input, .swagger-ui select, .swagger-ui textarea { background: var(--bg-tertiary); color: var(--text-primary); border-color: var(--border-color); }
        .swagger-ui .btn { background: var(--accent-primary); color: white; }
    </style>
</head>
<body>
    <header id="main-header">
        <div class="header-container">
            <div class="header-left">
                <button class="mobile-menu-toggle" onclick="toggleMobileMenu()">☰</button>
                <a class="logo" href="/">🌍 CityList API</a>
            </div>
            <nav id="main-nav" class="header-center">
                <a href="/">Home</a>
                <a href="#search">Search</a>
                <a href="/docs">Docs</a>
                <a href="/openapi" class="active">OpenAPI</a>
                <a href="/graphql">GraphQL</a>
            </nav>
            <div class="header-right">
                <div class="profile-dropdown">
                    <button class="profile-toggle" aria-label="Profile menu">
                        <span class="profile-avatar">G</span>
                        <span class="profile-name">Guest</span>
                        <span class="caret">▼</span>
                    </button>
                    <div class="profile-menu" id="profile-menu">
                        <a href="/healthz">Health Status</a>
                        <a href="#theme" id="theme-toggle">Toggle Theme</a>
                    </div>
                </div>
            </div>
        </div>
    </header>

    <div id="swagger-container">
        <div id="swagger-ui"></div>
    </div>

    <script src="/static/js/main.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui-standalone-preset.js"></script>
    <script>
        window.onload = function() {
            const ui = SwaggerUIBundle({
                url: "/api/v1/openapi.json",
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [
                    SwaggerUIBundle.presets.apis,
                    SwaggerUIStandalonePreset
                ],
                plugins: [
                    SwaggerUIBundle.plugins.DownloadUrl
                ],
                layout: "StandaloneLayout"
            });
            window.ui = ui;
        };
    </script>
</body>
</html>`

	t, err := template.New("swagger").Parse(tmpl)
	if err != nil {
		http.Error(w, "Failed to load template", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t.Execute(w, nil)
}

// handleOpenAPISpec serves the OpenAPI specification JSON
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	spec := map[string]interface{}{
		"openapi": "3.0.0",
		"info": map[string]interface{}{
			"title":       "CityList API",
			"description": "Global cities database API with search and country filtering",
			"version":     "1.0.0",
			"contact": map[string]string{
				"name": "CityList API",
				"url":  "https://github.com/apimgr/citylist",
			},
			"license": map[string]string{
				"name": "MIT",
				"url":  "https://opensource.org/licenses/MIT",
			},
		},
		"servers": []map[string]string{
			{"url": "/api/v1", "description": "API v1"},
		},
		"tags": []map[string]string{
			{"name": "cities", "description": "City data endpoints"},
			{"name": "health", "description": "Health check endpoints"},
			{"name": "admin", "description": "Admin endpoints (authentication required)"},
		},
		"paths": map[string]interface{}{
			"/health": map[string]interface{}{
				"get": map[string]interface{}{
					"tags":        []string{"health"},
					"summary":     "API health check",
					"description": "Simple health check endpoint for API status",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Successful response",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"success": map[string]string{"type": "boolean"},
											"data": map[string]interface{}{
												"type": "object",
												"properties": map[string]interface{}{
													"status":  map[string]string{"type": "string"},
													"version": map[string]string{"type": "string"},
												},
											},
											"timestamp": map[string]string{"type": "string"},
										},
									},
								},
							},
						},
					},
				},
			},
			"/cities": map[string]interface{}{
				"get": map[string]interface{}{
					"tags":        []string{"cities"},
					"summary":     "List all cities",
					"description": "Get a paginated list of all cities in the database",
					"parameters": []map[string]interface{}{
						{
							"name":        "limit",
							"in":          "query",
							"description": "Results per page (default: 100, max: 1000)",
							"required":    false,
							"schema":      map[string]string{"type": "integer", "default": "100"},
						},
						{
							"name":        "offset",
							"in":          "query",
							"description": "Pagination offset (default: 0)",
							"required":    false,
							"schema":      map[string]string{"type": "integer", "default": "0"},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Successful response",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"success": map[string]string{"type": "boolean"},
											"data": map[string]interface{}{
												"type": "object",
												"properties": map[string]interface{}{
													"cities": map[string]interface{}{
														"type": "array",
														"items": map[string]string{
															"$ref": "#/components/schemas/City",
														},
													},
													"total":  map[string]string{"type": "integer"},
													"limit":  map[string]string{"type": "integer"},
													"offset": map[string]string{"type": "integer"},
												},
											},
											"timestamp": map[string]string{"type": "string"},
										},
									},
								},
							},
						},
					},
				},
			},
			"/cities/search": map[string]interface{}{
				"get": map[string]interface{}{
					"tags":        []string{"cities"},
					"summary":     "Search cities",
					"description": "Search cities by name (case-insensitive, partial match)",
					"parameters": []map[string]interface{}{
						{
							"name":        "q",
							"in":          "query",
							"description": "Search query (city name)",
							"required":    true,
							"schema":      map[string]string{"type": "string"},
						},
						{
							"name":        "limit",
							"in":          "query",
							"description": "Maximum results (default: 50)",
							"required":    false,
							"schema":      map[string]string{"type": "integer", "default": "50"},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Successful response",
						},
						"400": map[string]interface{}{
							"description": "Missing query parameter",
						},
					},
				},
			},
			"/cities/country/{code}": map[string]interface{}{
				"get": map[string]interface{}{
					"tags":        []string{"cities"},
					"summary":     "Get cities by country",
					"description": "Get cities filtered by ISO 3166-1 alpha-2 country code",
					"parameters": []map[string]interface{}{
						{
							"name":        "code",
							"in":          "path",
							"description": "2-letter country code (e.g., US, GB, JP)",
							"required":    true,
							"schema":      map[string]string{"type": "string", "minLength": "2", "maxLength": "2"},
						},
						{
							"name":        "limit",
							"in":          "query",
							"description": "Maximum results (default: 100)",
							"required":    false,
							"schema":      map[string]string{"type": "integer", "default": "100"},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Successful response",
						},
						"400": map[string]interface{}{
							"description": "Invalid country code",
						},
					},
				},
			},
			"/citylist.json": map[string]interface{}{
				"get": map[string]interface{}{
					"tags":        []string{"cities"},
					"summary":     "Download raw citylist JSON",
					"description": "Download the complete citylist.json file",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Raw JSON file",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{},
							},
						},
					},
				},
			},
			"/admin/settings": map[string]interface{}{
				"get": map[string]interface{}{
					"tags":        []string{"admin"},
					"summary":     "Get all settings",
					"description": "Get all server settings (requires authentication)",
					"security": []map[string][]string{
						{"bearerAuth": {}},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Successful response",
						},
						"401": map[string]interface{}{
							"description": "Unauthorized",
						},
					},
				},
				"put": map[string]interface{}{
					"tags":        []string{"admin"},
					"summary":     "Update a setting",
					"description": "Update a server setting (requires authentication)",
					"security": []map[string][]string{
						{"bearerAuth": {}},
					},
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"key":   map[string]string{"type": "string"},
										"value": map[string]string{"type": "string"},
									},
									"required": []string{"key", "value"},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Setting updated",
						},
						"401": map[string]interface{}{
							"description": "Unauthorized",
						},
					},
				},
			},
			"/admin/stats": map[string]interface{}{
				"get": map[string]interface{}{
					"tags":        []string{"admin"},
					"summary":     "Get server statistics",
					"description": "Get server statistics including memory usage and uptime (requires authentication)",
					"security": []map[string][]string{
						{"bearerAuth": {}},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Successful response",
						},
						"401": map[string]interface{}{
							"description": "Unauthorized",
						},
					},
				},
			},
		},
		"components": map[string]interface{}{
			"securitySchemes": map[string]interface{}{
				"bearerAuth": map[string]string{
					"type":   "http",
					"scheme": "bearer",
				},
			},
			"schemas": map[string]interface{}{
				"City": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":      map[string]string{"type": "integer", "description": "City ID"},
						"name":    map[string]string{"type": "string", "description": "City name"},
						"country": map[string]string{"type": "string", "description": "ISO 3166-1 alpha-2 country code"},
						"lon":     map[string]string{"type": "number", "format": "double", "description": "Longitude"},
						"lat":     map[string]string{"type": "number", "format": "double", "description": "Latitude"},
					},
					"example": map[string]interface{}{
						"id":      2643743,
						"name":    "London",
						"country": "GB",
						"lon":     -0.1278,
						"lat":     51.5074,
					},
				},
			},
		},
	}

	s.respondJSON(w, http.StatusOK, spec)
}

// respondJSON is a helper to send JSON responses (referenced by handleOpenAPISpec)
func (s *Server) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// handleGraphQLPlayground serves the GraphQL Playground with site theme
func (s *Server) handleGraphQLPlayground(w http.ResponseWriter, r *http.Request) {
	tmpl := `<!DOCTYPE html>
<html lang="en" data-theme="dark">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>GraphQL Playground - CityList API</title>
    <link rel="stylesheet" href="/static/css/main.css">
    <style>
        body { margin: 0; padding: 0; display: flex; flex-direction: column; min-height: 100vh; }
        #graphql-container { flex: 1; display: flex; flex-direction: column; }
        #root { flex: 1; }
    </style>
</head>
<body>
    <header id="main-header">
        <div class="header-container">
            <div class="header-left">
                <button class="mobile-menu-toggle" onclick="toggleMobileMenu()">☰</button>
                <a class="logo" href="/">🌍 CityList API</a>
            </div>
            <nav id="main-nav" class="header-center">
                <a href="/">Home</a>
                <a href="#search">Search</a>
                <a href="/docs">Docs</a>
                <a href="/openapi">OpenAPI</a>
                <a href="/graphql" class="active">GraphQL</a>
            </nav>
            <div class="header-right">
                <div class="profile-dropdown">
                    <button class="profile-toggle" aria-label="Profile menu">
                        <span class="profile-avatar">G</span>
                        <span class="profile-name">Guest</span>
                        <span class="caret">▼</span>
                    </button>
                    <div class="profile-menu" id="profile-menu">
                        <a href="/healthz">Health Status</a>
                        <a href="#theme" id="theme-toggle">Toggle Theme</a>
                    </div>
                </div>
            </div>
        </div>
    </header>

    <div id="graphql-container">
        <div id="root"></div>
    </div>

    <link rel="stylesheet" href="https://unpkg.com/graphql-playground-react@1.7.28/build/static/css/index.css">
    <script src="/static/js/main.js"></script>
    <script src="https://unpkg.com/graphql-playground-react@1.7.28/build/static/js/middleware.js"></script>
    <script>
        window.addEventListener('load', function (event) {
            GraphQLPlayground.init(document.getElementById('root'), {
                endpoint: '/api/v1/graphql',
                settings: {
                    'editor.theme': 'dark',
                    'editor.cursorShape': 'line',
                    'theme': 'dark'
                },
                tabs: [
                    {
                        endpoint: '/api/v1/graphql',
                        query: '# Welcome to CityList GraphQL API\n# Press the Play button to run a query\n\nquery GetCity {\n  city(id: 2643743) {\n    id\n    name\n    country\n    coordinates {\n      lat\n      lon\n    }\n  }\n}\n\nquery SearchCities {\n  cities(search: "London", limit: 5) {\n    id\n    name\n    country\n  }\n}\n\nquery GetCitiesByCountry {\n  citiesByCountry(countryCode: "US", limit: 10) {\n    id\n    name\n    coordinates {\n      lat\n      lon\n    }\n  }\n}'
                    }
                ]
            });
        });
    </script>
</body>
</html>`

	t, err := template.New("graphql").Parse(tmpl)
	if err != nil {
		http.Error(w, "Failed to load template", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t.Execute(w, nil)
}

// handleGraphQL handles GraphQL queries
func (s *Server) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	// For now, return a simple message
	// Full GraphQL implementation would go here
	s.respondJSON(w, http.StatusOK, map[string]string{
		"message":    "GraphQL endpoint - Full implementation coming soon",
		"playground": "/graphql",
	})
}
