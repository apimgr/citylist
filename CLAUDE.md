# 🏙️ CityList API Server - Project Specification

**Project**: citylist
**Module**: citylist (local module)
**Language**: Go 1.23+
**Purpose**: Global cities database API with search and filtering capabilities
**Data**: 209,579 cities worldwide from SimpleMaps (embedded)

---

## 📖 Table of Contents

1. [Project Overview](#project-overview)
2. [Architecture](#architecture)
3. [Directory Layout](#directory-layout)
4. [Data Sources](#data-sources)
5. [Authentication](#authentication)
6. [Routes & Endpoints](#routes--endpoints)
7. [Configuration](#configuration)
8. [Build & Deployment](#build--deployment)
9. [Development](#development)
10. [Testing](#testing)
11. [Standards & Best Practices](#standards--best-practices)

---

## 🎯 Project Overview

### What This Is

A **public city information API** with a web frontend, built as a single self-contained Go binary.

- **Public API**: All city data is freely accessible (no authentication)
- **Admin Interface**: Server configuration protected by token/password authentication
- **Embedded Data**: citylist.json (~29MB) built into binary via go:embed
- **Fast Search**: SQLite database with indexed searches
- **Search Capabilities**: By name, country filtering
- **Web Frontend**: Dark-themed UI with embedded templates
- **Single Binary**: All assets embedded, no external dependencies

### Key Features

- Search cities by name with SQL LIKE queries
- Filter by country (ISO 3166-1 alpha-2 codes)
- Paginated results with limit/offset
- RESTful API endpoints
- Admin dashboard for server configuration
- Single binary deployment (~12MB static binary)
- OS-specific directory detection (Linux, macOS, Windows, BSD)
- Random port generation (64000-64999)
- Docker support with multi-arch builds (amd64, arm64)
- IPv6 dual-stack support

---

## 🏗️ Architecture

### System Design

```
┌─────────────────────────────────────────┐
│         Single Go Binary                │
│  ┌─────────────────────────────────┐   │
│  │  Embedded Assets (go:embed)     │   │
│  │  • citylist.json (29MB)         │   │
│  │  • Static files (CSS/JS)        │   │
│  │  • favicon.png, manifest.json   │   │
│  └─────────────────────────────────┘   │
│  ┌─────────────────────────────────┐   │
│  │  HTTP Server (Chi v5)           │   │
│  │  • Public routes (no auth)      │   │
│  │  • Admin routes (auth required) │   │
│  │  • API v1 endpoints             │   │
│  │  • IPv4/IPv6 dual-stack         │   │
│  └─────────────────────────────────┘   │
│  ┌─────────────────────────────────┐   │
│  │  SQLite Database (Pure Go)      │   │
│  │  • Cities data (209,579 records)│   │
│  │  • Admin credentials (hashed)   │   │
│  │  • Server settings              │   │
│  │  • User/session tables (future) │   │
│  └─────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

### Technology Stack

- **Language**: Go 1.23+
- **HTTP Router**: Chi v5 (github.com/go-chi/chi/v5)
- **Database**: SQLite (modernc.org/sqlite - pure Go, no CGO required)
- **Templates**: Go html/template (embedded in handlers)
- **Embedding**: Go embed.FS
- **Authentication**: SHA-256 hashing, Bearer tokens, Basic Auth

---

## 📁 Directory Layout

### OS-Specific Paths

The application uses the `paths` package to detect OS-specific directories:

```yaml
Linux/BSD (with root privileges):
  Config:  /etc/citylist/
  Data:    /var/lib/citylist/
  Logs:    /var/log/citylist/

Linux/BSD (without root):
  Config:  ~/.config/citylist/
  Data:    ~/.local/share/citylist/
  Logs:    ~/.local/state/citylist/

macOS (with privileges):
  Config:  /Library/Application Support/Citylist/
  Data:    /Library/Application Support/Citylist/data/
  Logs:    /Library/Logs/Citylist/

macOS (without privileges):
  Config:  ~/Library/Application Support/Citylist/
  Data:    ~/Library/Application Support/Citylist/data/
  Logs:    ~/Library/Logs/Citylist/

Windows:
  Config:  C:\ProgramData\Citylist\config\
  Data:    C:\ProgramData\Citylist\data\
  Logs:    C:\ProgramData\Citylist\logs\

Windows (user):
  Config:  %APPDATA%\Citylist\config\
  Data:    %APPDATA%\Citylist\data\
  Logs:    %APPDATA%\Citylist\logs\

Docker:
  Config:  /config
  Data:    /data
  Logs:    /logs
  DB:      /data/db/citylist.db
```

### Directory Contents

```yaml
Config Directory:
  - admin_credentials     # Generated on first run (0600 permissions)

Data Directory:
  - citylist.db          # SQLite database (contains cities + settings)

Logs Directory:
  - (Future: access.log, error.log, audit.log)
```

### Environment Variables & Flags

```yaml
Command-line Flags:
  --help              # Show help message
  --version           # Show version information (format: X.Y.Z, no "v" prefix)
  --status            # Show server status (health check)
  --config DIR        # Configuration directory
  --data DIR          # Data directory
  --logs DIR          # Logs directory
  --port PORT         # HTTP port (default: random 64000-64999)
  --address ADDR      # Listen address (default: ::, dual-stack IPv4+IPv6)
  --dev               # Run in development mode

Environment Variables (priority order):
  1. Command-line flags (highest priority)
  2. Environment variables
  3. OS-specific defaults (lowest priority)

  CONFIG_DIR          # Override config directory
  DATA_DIR            # Override data directory
  LOGS_DIR            # Override logs directory
  PORT                # Override HTTP port
  ADDRESS             # Override listen address (default: ::)

Docker Environment:
  PORT=80             # Internal port
  ADDRESS=::          # Dual-stack (IPv4+IPv6)
  CONFIG_DIR=/config
  DATA_DIR=/data
  LOGS_DIR=/logs
  DB_PATH=/data/db/citylist.db
```

### Project Source Layout

```
./
├── .github/
│   └── workflows/
│       ├── release.yml        # Binary builds and GitHub releases
│       └── docker.yml         # Docker multi-arch builds
├── .gitignore                 # Git ignore patterns
├── CLAUDE.md                  # This file (specification)
├── Dockerfile                 # Alpine-based multi-stage build
├── docker-compose.yml         # Production compose (172.17.0.1:64180:80)
├── docker-compose.test.yml    # Development compose (/tmp, 64181:80)
├── go.mod                     # Go module definition
├── go.sum                     # Go module checksums
├── Jenkinsfile                # CI/CD pipeline (jenkins.casjay.cc)
├── LICENSE.md                 # MIT License
├── Makefile                   # Build system (4 core targets + docker-dev)
├── README.md                  # User documentation (production-first)
├── release.txt                # Version tracking (X.Y.Z format, no "v")
├── binaries/                  # Built binaries (gitignored)
│   ├── citylist-linux-amd64
│   ├── citylist-linux-arm64
│   ├── citylist-windows-amd64.exe
│   ├── citylist-windows-arm64.exe
│   ├── citylist-darwin-amd64
│   ├── citylist-darwin-arm64
│   ├── citylist-freebsd-amd64
│   ├── citylist-freebsd-arm64
│   └── citylist               # Host platform binary
├── releases/                  # Release artifacts (gitignored)
│   ├── citylist-*             # Platform binaries
│   ├── citylist-X.Y.Z-src.tar.gz  # Source archive
│   └── citylist-X.Y.Z-src.zip     # Source archive (Windows)
├── rootfs/                    # Docker volumes (gitignored)
│   ├── config/citylist/       # Service config
│   ├── data/citylist/         # Service data
│   └── logs/citylist/         # Service logs
└── src/                       # Source code
    ├── main.go                # Entry point (embeds data/*.json)
    ├── auth/
    │   └── auth.go            # Admin credential generation
    ├── database/
    │   ├── database.go        # SQLite setup, schema, CRUD
    │   ├── credentials.go     # Credential management, URL display
    │   └── settings.go        # Settings management
    ├── paths/
    │   └── paths.go           # OS-specific path detection
    ├── server/
    │   ├── server.go          # Chi router setup, handlers
    │   ├── docs_handlers.go   # API documentation handler
    │   ├── static/            # Embedded static files
    │   │   ├── css/
    │   │   │   └── main.css   # Dark theme (~867 lines)
    │   │   ├── js/
    │   │   │   └── main.js    # Vanilla JS utilities (~130 lines)
    │   │   ├── favicon.png
    │   │   └── manifest.json
    │   └── templates/         # Embedded HTML templates
    │       ├── base.html      # Base template
    │       ├── home.html      # Homepage
    │       └── *.html         # Other pages
    ├── utils/
    │   └── network.go         # Network address detection
    └── data/
        └── citylist.json      # City data (JSON ONLY, no .go files)
```

**Important**: `src/data/` contains ONLY JSON files. No Go code. JSON is embedded from `main.go` using `//go:embed data/*.json`.

---

## 💾 Data Sources

### citylist.json

```yaml
Location: src/data/citylist.json
Size: ~29MB
Records: 209,579 worldwide cities
Source: SimpleMaps World Cities Database
Embedded: Yes (go:embed in main.go)
Loaded: On first run into SQLite database

Structure:
  [
    {
      "id": 707860,
      "name": "Hurzuf",
      "country": "UA",
      "coord": {
        "lon": 34.283333,
        "lat": 44.549999
      }
    }
  ]

Fields:
  - id: Unique city identifier (integer)
  - name: City name (string)
  - country: ISO 3166-1 alpha-2 country code (string)
  - coord.lon: Longitude (float)
  - coord.lat: Latitude (float)

Database Loading:
  - JSON parsed on first startup
  - Inserted into SQLite cities table
  - Indexes created on name and country
  - Subsequent startups use existing database
```

**Data Embedding Pattern**:
- JSON files stored in `src/data/` (JSON ONLY, no .go files)
- Embedded in `main.go` using `//go:embed data/citylist.json`
- Passed to services as `[]byte` parameter
- All data in single static binary

---

## 🔐 Authentication

### Overview

This project uses **admin-only authentication** - all city data is public, only server configuration requires authentication.

### Authentication Methods

```yaml
1. API Token (Bearer):
   Header: Authorization: Bearer <token>
   Use: Programmatic access to admin API
   Format: Random 32-character string
   Routes: /api/v1/admin/*

2. Basic Auth:
   Header: Authorization: Basic <base64(user:pass)>
   Use: Web UI access
   Browser: Prompts automatically
   Routes: /admin/*
```

### First Run Setup

```yaml
On first startup:
  1. Check if admin_credentials table has records

  2. If empty, generate:
     - Username: "administrator"
     - Password: Random 16-character alphanumeric
     - Token: Random 32-character alphanumeric

  3. Hash with SHA-256:
     - password_hash: SHA-256(password)
     - token_hash: SHA-256(token)

  4. Insert into database (id=1)

  5. Determine accessible URL (NEVER localhost/127.0.0.1/0.0.0.0):
     Priority: FQDN > hostname > public IP > fallback

  6. Write plaintext to {CONFIG_DIR}/admin_credentials (0600)
     With accessible URL (not localhost)

  7. Display credentials in console output
     ⚠️  Shown once - save securely!

Credential File Format:
  ==============================================================
  🔐 Admin Credentials Generated
  ==============================================================
  WEB UI LOGIN:
    URL:      http://server.example.com:64555/admin
    Username: administrator
    Password: <16-char-random>

  API ACCESS:
    URL:      http://server.example.com:64555/api/v1/admin
    Header:   Authorization: Bearer <32-char-token>

  CREDENTIALS:
    Username: administrator
    Password: <16-char-random>
    Token:    <32-char-random>

  Created: 2024-01-01 12:00:00
  ==============================================================
```

### URL Display Standards

**CRITICAL**: Never show `localhost`, `127.0.0.1`, or `0.0.0.0` to users.

**Priority Order**:
1. **FQDN** (if hostname resolves to public IP)
2. **Hostname** (if available and not "localhost")
3. **Public IP** (outbound IP from network detection)
4. **Fallback** (`<your-host>` placeholder)

**Implementation** (`src/database/credentials.go`):
```go
func getAccessibleURL(port string) string {
    // Try hostname resolution (FQDN)
    hostname, err := os.Hostname()
    if err == nil && hostname != "" && hostname != "localhost" {
        if addrs, err := net.LookupHost(hostname); err == nil && len(addrs) > 0 {
            // IPv6 addresses need brackets
            if strings.Contains(hostname, ":") {
                return fmt.Sprintf("http://[%s]:%s", hostname, port)
            }
            return fmt.Sprintf("http://%s:%s", hostname, port)
        }
    }

    // Try outbound IP
    if ip := getOutboundIP(); ip != "" {
        // IPv6 addresses need brackets
        if strings.Contains(ip, ":") {
            return fmt.Sprintf("http://[%s]:%s", ip, port)
        }
        return fmt.Sprintf("http://%s:%s", ip, port)
    }

    // Fallback to hostname
    if hostname != "" && hostname != "localhost" {
        return fmt.Sprintf("http://%s:%s", hostname, port)
    }

    // Last resort
    return fmt.Sprintf("http://<your-host>:%s", port)
}

func getOutboundIP() string {
    // Try IPv4 first
    conn, err := net.Dial("udp", "8.8.8.8:80")
    if err == nil {
        defer conn.Close()
        return conn.LocalAddr().(*net.UDPAddr).IP.String()
    }

    // Try IPv6
    conn, err = net.Dial("udp", "[2001:4860:4860::8888]:80")
    if err == nil {
        defer conn.Close()
        return conn.LocalAddr().(*net.UDPAddr).IP.String()
    }

    return ""
}
```

### Credential Storage

```sql
CREATE TABLE admin_credentials (
  id INTEGER PRIMARY KEY,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,      -- SHA-256 hashed
  token_hash TEXT NOT NULL,         -- SHA-256 hashed
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

---

## 🗺️ Routes & Endpoints

### Route Matching Philosophy

**Routes must mirror between web and API:**
- `/` ↔ `/api/v1`
- `/search` ↔ `/api/v1/search`
- `/docs` ↔ `/api/v1/docs`
- `/admin` ↔ `/api/v1/admin`

This makes the API predictable and consistent.

### Implemented Routes

```yaml
Public Routes (No Authentication):

Homepage:
  GET  /                      → Home page with search interface

Documentation:
  GET  /docs                  → API documentation page (HTML)

Health Checks:
  GET  /healthz               → Comprehensive health check (JSON)

Search:
  GET  /api/v1/cities/search  → Search cities (JSON)
    Query params:
      ?q=query               - Search term (city name)
      ?limit=50              - Results limit (default: 50)

Cities List:
  GET  /api/v1/cities         → List cities (paginated)
    Query params:
      ?limit=100             - Results limit (default: 100, max: 1000)
      ?offset=0              - Pagination offset

Cities by Country:
  GET  /api/v1/cities/country/{code}  → Cities by country code
    URL params:
      {code}                 - 2-letter ISO country code (e.g., US, GB)
    Query params:
      ?limit=100             - Results limit (default: 100)

API Health:
  GET  /api/v1/health         → Simple health check (JSON)

Static Assets:
  GET  /static/*              → Embedded CSS, JS, images
  GET  /manifest.json         → PWA manifest
  GET  /robots.txt            → Robots.txt (from settings)
  GET  /security.txt          → Redirect to /.well-known/security.txt
  GET  /.well-known/security.txt → Security.txt (RFC 9116)

Admin Routes (Authentication Required):

Dashboard:
  GET  /admin                 → Admin dashboard (Basic Auth)
  GET  /admin/settings        → Settings page (Basic Auth)

API Admin:
  GET  /api/v1/admin/settings → Get all settings (Bearer Token)
  PUT  /api/v1/admin/settings → Update settings (Bearer Token)
  GET  /api/v1/admin/stats    → Server statistics (Bearer Token)

Development Mode Only:
  GET  /debug/routes          → List all registered routes
```

### Response Format

```yaml
JSON Success:
  {
    "success": true,
    "data": { ... },
    "timestamp": "2024-01-01T12:00:00Z"
  }

JSON Error:
  {
    "success": false,
    "error": {
      "code": "ERROR_CODE",
      "message": "Human readable message"
    },
    "timestamp": "2024-01-01T12:00:00Z"
  }
```

---

## ⚙️ Configuration

### Database Schema

```sql
-- Cities table
CREATE TABLE cities (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  country TEXT NOT NULL,
  lon REAL NOT NULL,
  lat REAL NOT NULL
);
CREATE INDEX idx_cities_name ON cities(name);
CREATE INDEX idx_cities_country ON cities(country);

-- Admin credentials table
CREATE TABLE admin_credentials (
  id INTEGER PRIMARY KEY,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Users table (for future features)
CREATE TABLE users (
  id TEXT PRIMARY KEY,
  username TEXT UNIQUE NOT NULL,
  email TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('administrator', 'user', 'guest')),
  status TEXT NOT NULL DEFAULT 'active',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Sessions table (for future features)
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  token TEXT UNIQUE NOT NULL,
  expires_at DATETIME NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id) REFERENCES users(id)
);

-- Tokens table (for future API tokens)
CREATE TABLE tokens (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  token_hash TEXT UNIQUE NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id) REFERENCES users(id)
);

-- Settings table
CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('string', 'number', 'boolean', 'json')),
  category TEXT NOT NULL,
  description TEXT,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Audit log table
CREATE TABLE audit_log (
  id TEXT PRIMARY KEY,
  user_id TEXT,
  action TEXT NOT NULL,
  resource TEXT NOT NULL,
  ip_address TEXT NOT NULL,
  success INTEGER NOT NULL,
  timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Default Settings

```yaml
Server:
  server.title: "CityList API"
  server.tagline: "Global Cities Database"
  server.description: "A comprehensive API for accessing global city information..."
  server.http_port: "0" (0 = auto-generate random 64000-64999)
  server.timezone: "UTC"

Security:
  security.session_timeout: "43200" (30 days in minutes)
  security.max_login_attempts: "5"
  security.password_min_length: "8"

Content:
  robots.txt: "User-agent: *\nDisallow: /admin/\nDisallow: /api/v1/admin/"
  security.txt: "Contact: security@example.com\nExpires: 2025-12-31T23:59:59Z"
```

---

## 🔨 Build & Deployment

### Makefile Targets

```makefile
Core Targets (4 required):
  make build      # Build for all platforms + host binary
  make release    # Create GitHub release (auto-increment version)
  make docker     # Build and push multi-arch Docker image
  make test       # Run all tests with coverage

Additional Target:
  make docker-dev # Build local development image (citylist:dev)

Helper Targets:
  make clean      # Remove build artifacts
  make help       # Show help message

Build Process:
  - Uses Docker container (golang:alpine) for consistent builds
  - CGO_ENABLED=0 for static binaries
  - Builds for: Linux, Windows, macOS, FreeBSD (amd64, arm64)
  - Strips Linux binaries for smaller size
  - Version from release.txt (X.Y.Z format, no "v" prefix)
  - Creates ./binaries/ directory with all outputs
  - Creates ./releases/ directory with release artifacts

Binary Naming Scheme:
  {projectname}-{os}-{arch}

Examples:
  citylist-linux-amd64
  citylist-windows-arm64.exe
  citylist-darwin-arm64
  citylist                    # Host platform
```

### Version Format Standards

**IMPORTANT**: All version references use plain semantic versioning without "v" prefix.

```yaml
release.txt:          0.0.1
Git tags:             0.0.1
GitHub releases:      0.0.1
Docker tags:          ghcr.io/casapps/citylist:0.0.1
CLI --version output: 0.0.1
```

**Version Workflow**:
1. `make build` - Reads version from `release.txt`, does NOT modify it
2. Developer manually edits `release.txt` when ready for new version
3. `make release` - Creates GitHub release with current version
4. AFTER successful `gh release create`, auto-increments `release.txt`

### Docker

```yaml
Dockerfile:
  Base: golang:alpine (build) → alpine:latest (runtime)
  Build: CGO_ENABLED=0, static binary
  Runtime Tools: curl, bash, ca-certificates, tzdata
  Binary Location: /usr/local/bin/citylist
  User: 65534:65534 (nobody)
  Port: 80 (internal)
  Volumes: /config, /data, /logs
  Health Check: citylist --status

Dockerfile Standards:
  ✅ alpine:latest runtime (not scratch)
  ✅ Includes curl, bash for health checks
  ✅ All OCI metadata labels
  ✅ DB path: /data/db/citylist.db

docker-compose.yml (Production):
  ❌ NO version: field
  ❌ NO build: definition
  ✅ Uses pre-built image: ghcr.io/casapps/citylist:latest
  ✅ Network: citylist (external: false)
  ✅ Volumes: ./rootfs/{type}/citylist
  ✅ Port: 172.17.0.1:64180:80 (Docker bridge only)
  ✅ Persistent storage in ./rootfs

docker-compose.test.yml (Development):
  ❌ NO version: field
  ❌ NO build: definition
  ✅ Uses: citylist:dev (local image from make docker-dev)
  ✅ Network: citylist (same name as prod)
  ✅ Volumes: /tmp/citylist/rootfs/{type}/citylist
  ✅ Port: 64181:80 (different from prod)
  ✅ Ephemeral storage in /tmp
  ✅ restart: "no"

Docker Tags:
  Production: ghcr.io/casapps/citylist:latest
  Production: ghcr.io/casapps/citylist:X.Y.Z
  Development: citylist:dev (local only)

Building:
  make docker              # Multi-arch build (amd64, arm64), push to registry
  make docker-dev          # Local development build (not pushed)

Running Production:
  docker-compose up -d
  # Access: http://172.17.0.1:64180
  # Credentials: cat ./rootfs/config/citylist/admin_credentials

Running Development:
  docker-compose -f docker-compose.test.yml up -d
  # Access: http://localhost:64181
  # Cleanup: docker-compose -f docker-compose.test.yml down && sudo rm -rf /tmp/citylist/rootfs
```

### IPv6 Support

**REQUIRED**: Full dual-stack IPv4/IPv6 support.

```yaml
Default Behavior:
  - Listen on :: (dual-stack, includes IPv4)
  - Accept connections from both IPv4 and IPv6
  - Detect and display both protocol addresses

Address Configuration:
  --address ::              # Dual-stack (default, recommended)
  --address 0.0.0.0         # IPv4 only
  --address ::1             # IPv6 localhost
  ADDRESS=::                # Environment variable

URL Formatting:
  - IPv4: http://192.168.1.100:64555
  - IPv6: http://[2001:db8::1]:64555 (brackets required)
  - IPv6 localhost: http://[::1]:64555

Docker IPv6:
  networks:
    citylist:
      enable_ipv6: true
      ipam:
        config:
          - subnet: 172.18.0.0/16
          - subnet: 2001:db8:1::/64

Testing:
  curl http://[::1]:64555/healthz
  curl http://localhost:64555/healthz
```

### CI/CD

```yaml
GitHub Actions:
  Files:
    - .github/workflows/release.yml (Binary builds & releases)
    - .github/workflows/docker.yml (Docker multi-arch builds)

  Triggers:
    - Push to main/master
    - Monthly schedule (1st at 3 AM UTC)
    - Manual dispatch

  release.yml Jobs:
    1. test - Run make test
    2. build-and-release:
       - Read version from release.txt (does NOT modify)
       - Run make build (8 platforms)
       - Delete existing release if exists
       - Create new GitHub release with version from release.txt
       - Attach all binaries
       - Upload artifacts (90 day retention)

  docker.yml Jobs:
    1. build-and-push:
       - Read version from release.txt
       - Build multi-arch (amd64, arm64)
       - Push to ghcr.io/casapps/citylist
       - Tags: latest, X.Y.Z, branch-sha

Jenkins (Jenkinsfile):
  Server: jenkins.casjay.cc
  Agents: amd64, arm64

  Stages:
    1. Build (parallel: amd64, arm64)
       - Runs make build on each architecture
       - Stash binaries for later stages
    2. Test (parallel: amd64, arm64)
       - Runs make test on each architecture
    3. Docker Build (parallel: amd64, arm64)
       - Build platform-specific images
       - Tag as {VERSION}-{arch} and latest-{arch}
    4. Push Docker Images (main/master only)
       - Push individual arch images
       - Create multi-arch manifests:
         * ghcr.io/casapps/citylist:{VERSION}
         * ghcr.io/casapps/citylist:latest
       - Manifest combines amd64 and arm64 images
    5. GitHub Release (main/master only)
       - Unstash release artifacts
       - Run make release

  Multi-Arch Manifest Creation:
    docker manifest create ghcr.io/casapps/citylist:{VERSION} \
      ghcr.io/casapps/citylist:{VERSION}-amd64 \
      ghcr.io/casapps/citylist:{VERSION}-arm64
    docker manifest push ghcr.io/casapps/citylist:{VERSION}
```

---

## 🛠️ Development

### Development Mode

```yaml
Enable:
  --dev flag

Features:
  - Enhanced logging
  - Debug endpoints (/debug/routes)
  - CORS enabled for all origins

Debug Endpoints:
  GET /debug/routes          - List all registered routes (JSON)
```

### Local Development

```bash
# Clone repository
git clone https://github.com/casapps/citylist.git
cd citylist

# Build
make build

# Run with development mode
./binaries/citylist --dev --port 8080

# Server starts, displays:
# - Config/Data/Logs directories
# - Admin credentials (if first run)
# - Server URLs (NEVER shows localhost/127.0.0.1/0.0.0.0)

# Access:
http://your-hostname:8080        # Homepage
http://your-hostname:8080/docs   # API Documentation
http://your-hostname:8080/admin  # Admin Dashboard
```

---

## ✅ Testing

### Testing Environment Priority

**CRITICAL**: Testing workflow follows strict priority order.

**Building** (make build, cross-compilation):
- ✅ **Docker ONLY** - Always use Docker (golang:alpine builder)
- ❌ Never use Incus or Host OS for builds

**Testing/Debugging** (running services, integration tests):
1. **Incus** (preferred) - System containers, full OS environment
2. **Docker** (fallback) - If Incus unavailable
3. **Host OS** (last resort) - Only when containers unavailable

### Testing Requirements

```yaml
Temporary Files:
  ✅ ALWAYS use /tmp/{projectname}/ for all test data
  ❌ NEVER use production directories (/etc, /var/lib, /var/log)
  ✅ Cleanup after tests: rm -rf /tmp/citylist

Port Selection:
  ✅ ALWAYS random: $(shuf -i 64000-64999 -n 1)
  ❌ NEVER: 80, 443, 8080, 3000, 5000, or other common ports

Environment:
  ✅ Incus (preferred) - incus launch images:alpine/3.19 test
  ✅ Docker (fallback) - docker-compose -f docker-compose.test.yml up
  ❌ Host OS (last resort) - Only if containers unavailable
```

### Multi-Distro Testing (REQUIRED)

Test on multiple distributions to ensure binary compatibility:

```bash
# 1. Alpine (musl libc, no systemd)
incus launch images:alpine/3.19 test-alpine
incus file push ./binaries/citylist test-alpine/usr/local/bin/
incus exec test-alpine -- /usr/local/bin/citylist --version
incus delete -f test-alpine

# 2. Ubuntu (glibc, systemd)
incus launch images:ubuntu/24.04 test-ubuntu
incus file push ./binaries/citylist test-ubuntu/usr/local/bin/
incus exec test-ubuntu -- /usr/local/bin/citylist --version

# Test systemd integration
incus file push ./test-systemd.service test-ubuntu/etc/systemd/system/citylist.service
incus exec test-ubuntu -- systemctl daemon-reload
incus exec test-ubuntu -- systemctl start citylist
incus exec test-ubuntu -- systemctl status citylist
incus exec test-ubuntu -- journalctl -u citylist
incus exec test-ubuntu -- systemctl stop citylist
incus delete -f test-ubuntu

# 3. Debian (glibc, systemd)
incus launch images:debian/12 test-debian
incus file push ./binaries/citylist test-debian/usr/local/bin/
incus exec test-debian -- /usr/local/bin/citylist --version
incus delete -f test-debian
```

### Testing Commands

```bash
# Run all tests (in Docker)
make test

# Manual testing
go test -v -race -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Docker testing (recommended)
make docker-dev
docker-compose -f docker-compose.test.yml up -d

# Generate random test port
TESTPORT=$(shuf -i 64000-64999 -n 1)
echo "Testing on port: ${TESTPORT}"

# Test endpoints
curl http://localhost:${TESTPORT}/healthz
curl http://localhost:${TESTPORT}/api/v1/cities?limit=10

# Cleanup
docker-compose -f docker-compose.test.yml down
sudo rm -rf /tmp/citylist/rootfs
```

---

## 🔒 Security

### Best Practices

```yaml
Credentials:
  - Admin credentials auto-generated on first run
  - SHA-256 hashing for passwords and tokens
  - Credentials file: 0600 permissions (owner read/write only)
  - Change default credentials immediately after setup

Network:
  - Never displays 0.0.0.0, 127.0.0.1, or localhost to users
  - Smart address detection (FQDN > Hostname > Public IP > Fallback)
  - Random port generation (64000-64999) for security
  - Default bind to :: (dual-stack IPv4+IPv6)
  - IPv6 addresses properly formatted with brackets

Database:
  - SQLite with prepared statements (SQL injection protection)
  - Input validation on all endpoints
  - Admin routes protected by Bearer token or Basic Auth
```

---

## 📝 License

MIT License - See LICENSE.md

### Data Attribution

```yaml
City Data:
  Source: SimpleMaps World Cities Database
  License: Creative Commons Attribution 4.0
  Attribution: SimpleMaps.com
  URL: https://simplemaps.com/data/world-cities
```

---

## 🌐 Standards & Best Practices

### URL Display Standards

**NEVER** show `localhost`, `127.0.0.1`, or `0.0.0.0` to users.

**Priority Order**:
1. FQDN (if hostname resolves)
2. Hostname (if available and not "localhost")
3. Public IP (outbound IP detection)
4. Fallback (`<your-host>`)

**IPv6 Formatting**:
- IPv4: `http://192.168.1.100:64555`
- IPv6: `http://[2001:db8::1]:64555` (brackets required)

### Docker Standards

```yaml
Dockerfile:
  ✅ alpine:latest runtime (not scratch)
  ✅ Includes: curl, bash, ca-certificates, tzdata
  ✅ Binary location: /usr/local/bin/{projectname}
  ✅ DB location: /data/db/{projectname}.db

docker-compose.yml:
  ❌ NO version: field
  ❌ NO build: definition
  ✅ Pre-built images only
  ✅ Network: {projectname} (external: false)
  ✅ Volumes: ./rootfs/{type}/{servicename}
  ✅ Port: 172.17.0.1:64xxx:80

docker-compose.test.yml:
  ❌ NO version: field
  ❌ NO build: definition
  ✅ Image: {projectname}:dev
  ✅ Volumes: /tmp/{projectname}/rootfs/{type}/{servicename}
  ✅ Port: 64xxx:80 (different from prod)
  ✅ restart: "no"
```

### Makefile Standards

```makefile
4 Core Targets (REQUIRED):
  build      # Cross-platform builds
  release    # GitHub release
  docker     # Multi-arch registry push
  test       # Test suite

Additional Target:
  docker-dev # Local dev image (not pushed)
```

### Data Embedding Standards

```yaml
src/data/ Directory:
  ✅ Contains ONLY JSON files
  ❌ NO .go files
  ❌ NO code of any kind

Embedding Pattern:
  - main.go: //go:embed data/*.json
  - Pass to services as []byte parameter
  - Services parse JSON internally
```

### Testing Standards

```yaml
Environment Priority:
  Build: Docker ONLY
  Test:  Incus > Docker > Host OS

Temporary Files:
  ✅ ALWAYS: /tmp/{projectname}/
  ❌ NEVER: production directories

Port Selection:
  ✅ ALWAYS: $(shuf -i 64000-64999 -n 1)
  ❌ NEVER: 80, 443, 8080, 3000, 5000

Multi-Distro Testing:
  ✅ Alpine (musl libc)
  ✅ Ubuntu (systemd + glibc)
  ✅ Debian (systemd + glibc)
```

### Version Format Standards

```yaml
Format: X.Y.Z (no "v" prefix)
  release.txt:     1.0.0
  Git tags:        1.0.0
  GitHub releases: 1.0.0
  Docker tags:     ghcr.io/org/project:1.0.0
  CLI output:      1.0.0
```

### README Structure

```markdown
Order: About → Production → Docker → API Usage → Development

Sections:
  1. About (features, capabilities)
  2. Production Installation (binary, systemd, env vars)
  3. Docker Deployment (compose examples)
  4. API Usage (quick examples, admin panel)
  5. Development (requirements, build system, CI/CD)
  6. License & Credits
```

### Web UI Standards

```yaml
Technology:
  - Go html/template (server-side rendering)
  - Vanilla JavaScript (no frameworks)
  - CSS3 with custom properties (variables)
  - Dark theme by default (light theme available)

Components:
  - Buttons, cards, modals, toasts
  - Forms with validation
  - Responsive grid layouts
  - Mobile-friendly navigation

File Sizes:
  - CSS: ~867 lines / ~25KB
  - JavaScript: ~130 lines / ~4KB
  - Total: ~30KB (gzipped: ~10KB)

Embedding:
  - All static assets: //go:embed static/*
  - All templates: //go:embed templates/*
  - Single binary includes everything
```

### AI Assistant Guidelines

**CRITICAL RULES**:

✅ **ALWAYS**:
- Use `/tmp/` for all test data
- Use random ports (64000-64999)
- Test in Docker or Incus (never host OS)
- Use accessible URLs (never localhost/127.0.0.1/0.0.0.0)

❌ **NEVER**:
- Write to production directories (/etc, /var/lib, /var/log)
- Use common ports (80, 443, 8080, 3000, 5000)
- Test directly on host OS (unless explicitly requested)
- Run git commit, git push, git tag, or other VCS write operations
- Modify version control history

**Version Control**:
- ✅ CAN: `git status`, `git diff`, `git log` (read-only)
- ❌ NEVER: `git add`, `git commit`, `git push`, `git tag`, `git merge`
- Let users create commits manually
- Suggest commit messages but don't execute

---

**CityList API Server** - A focused, production-ready city information API built with Go. Single static binary, embedded data, multi-platform support, IPv6 ready, and Docker-ready deployment following strict SPEC.md standards.
