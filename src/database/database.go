package database

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// City represents a city from the dataset
type City struct {
	ID      int     `json:"id"`
	Name    string  `json:"name"`
	Country string  `json:"country"`
	Lon     float64 `json:"lon"`
	Lat     float64 `json:"lat"`
}

// Initialize creates and initializes the database
func Initialize(dbPath string, citylistJSON []byte) (*sql.DB, error) {
	// Create data directory if it doesn't exist
	dbDir := filepath.Dir(dbPath)
	os.MkdirAll(dbDir, 0755)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// Set connection pool
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	// Create tables
	if err := createTables(db); err != nil {
		return nil, err
	}

	// Check if cities need to be loaded
	var count int
	db.QueryRow("SELECT COUNT(*) FROM cities").Scan(&count)
	if count == 0 {
		log.Println("Loading cities from embedded JSON...")
		if err := loadCitiesFromJSON(db, citylistJSON); err != nil {
			log.Printf("Warning: Failed to load cities: %v", err)
		} else {
			db.QueryRow("SELECT COUNT(*) FROM cities").Scan(&count)
			log.Printf("Loaded %d cities", count)
		}
	}

	return db, nil
}

func createTables(db *sql.DB) error {
	schema := `
	-- Cities table
	CREATE TABLE IF NOT EXISTS cities (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		country TEXT NOT NULL,
		lon REAL NOT NULL,
		lat REAL NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_cities_name ON cities(name);
	CREATE INDEX IF NOT EXISTS idx_cities_country ON cities(country);

	-- Admin credentials table
	CREATE TABLE IF NOT EXISTS admin_credentials (
		id INTEGER PRIMARY KEY,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		token_hash TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	-- Users table
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		username TEXT UNIQUE NOT NULL,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		display_name TEXT,
		avatar_url TEXT,
		bio TEXT,
		role TEXT NOT NULL CHECK (role IN ('administrator', 'user', 'guest')),
		status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'pending')),
		timezone TEXT DEFAULT 'UTC',
		language TEXT DEFAULT 'en',
		theme TEXT DEFAULT 'dark',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_login DATETIME,
		failed_login_attempts INTEGER DEFAULT 0,
		locked_until DATETIME,
		metadata TEXT
	);

	-- Sessions table
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		token TEXT UNIQUE NOT NULL,
		ip_address TEXT NOT NULL,
		user_agent TEXT,
		device_name TEXT,
		expires_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_activity DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		remember_me INTEGER DEFAULT 0,
		is_active INTEGER DEFAULT 1,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	);

	-- Tokens table
	CREATE TABLE IF NOT EXISTS tokens (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		name TEXT NOT NULL,
		token_hash TEXT UNIQUE NOT NULL,
		last_used DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		revoked_at DATETIME,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	);

	-- Settings table
	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		type TEXT NOT NULL CHECK (type IN ('string', 'number', 'boolean', 'json')),
		category TEXT NOT NULL,
		description TEXT,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_by TEXT
	);

	-- Audit log table
	CREATE TABLE IF NOT EXISTS audit_log (
		id TEXT PRIMARY KEY,
		user_id TEXT,
		action TEXT NOT NULL,
		resource TEXT NOT NULL,
		old_value TEXT,
		new_value TEXT,
		ip_address TEXT NOT NULL,
		user_agent TEXT,
		success INTEGER NOT NULL,
		error_message TEXT,
		timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	-- Insert default settings
	INSERT OR IGNORE INTO settings (key, value, type, category, description) VALUES
		('server.title', 'CityList API', 'string', 'Server', 'Application display name'),
		('server.tagline', 'Global Cities Database', 'string', 'Server', 'Application tagline'),
		('server.description', 'A comprehensive API for accessing global city information including coordinates and country data.', 'string', 'Server', 'Application description'),
		('server.http_port', '0', 'number', 'Server', 'HTTP port (0 = auto-generate random 64000-64999)'),
		('server.timezone', 'UTC', 'string', 'Server', 'Server timezone'),
		('security.session_timeout', '43200', 'number', 'Security', 'Session timeout in minutes (30 days)'),
		('security.max_login_attempts', '5', 'number', 'Security', 'Maximum login attempts'),
		('security.password_min_length', '8', 'number', 'Security', 'Minimum password length'),
		('robots.txt', 'User-agent: *\nDisallow: /admin/\nDisallow: /api/v1/admin/', 'string', 'Server', 'Robots.txt content'),
		('security.txt', 'Contact: security@example.com\nExpires: 2025-12-31T23:59:59Z', 'string', 'Server', 'Security.txt content');
	`

	_, err := db.Exec(schema)
	return err
}

func loadCitiesFromJSON(db *sql.DB, jsonData []byte) error {

	var cities []struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Country string `json:"country"`
		Coord   struct {
			Lon float64 `json:"lon"`
			Lat float64 `json:"lat"`
		} `json:"coord"`
	}

	if err := json.Unmarshal(jsonData, &cities); err != nil {
		return err
	}

	// Begin transaction
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Prepare statement
	stmt, err := tx.Prepare("INSERT INTO cities (id, name, country, lon, lat) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	// Insert cities
	for _, city := range cities {
		_, err := stmt.Exec(city.ID, city.Name, city.Country, city.Coord.Lon, city.Coord.Lat)
		if err != nil {
			log.Printf("Failed to insert city %s: %v", city.Name, err)
		}
	}

	return tx.Commit()
}

// GetSetting retrieves a setting value
func GetSetting(db *sql.DB, key, defaultValue string) string {
	var value string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		return defaultValue
	}
	return value
}

// SetSetting updates or inserts a setting
func SetSetting(db *sql.DB, key, value, typ, category string) error {
	_, err := db.Exec(`
		INSERT INTO settings (key, value, type, category, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = CURRENT_TIMESTAMP
	`, key, value, typ, category)
	return err
}
