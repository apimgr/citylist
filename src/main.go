package main

import (
	"context"
	"database/sql"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"citylist/src/auth"
	"citylist/src/database"
	"citylist/src/paths"
	"citylist/src/server"
	"citylist/src/utils"
)

//go:embed static/*
var staticFS embed.FS

//go:embed data/citylist.json
var citylistData []byte

var (
	Version   = "0.0.1"
	BuildDate = "unknown"
	Commit    = "unknown"
)

func main() {
	// Initialize random seed for port generation
	rand.Seed(time.Now().UnixNano())

	// CLI flags
	var (
		showHelp    = flag.Bool("help", false, "Show help message")
		showVersion = flag.Bool("version", false, "Show version information")
		showStatus  = flag.Bool("status", false, "Show server status")
		configDir   = flag.String("config", "", "Configuration directory")
		dataDir     = flag.String("data", "", "Data directory")
		logsDir     = flag.String("logs", "", "Logs directory")
		port        = flag.String("port", "", "HTTP port (default: random 64000-64999)")
		address     = flag.String("address", "::", "Listen address (default: :: for dual-stack IPv4+IPv6)")
		devMode     = flag.Bool("dev", false, "Run in development mode")
	)
	flag.Parse()

	// Handle --help
	if *showHelp {
		printHelp()
		os.Exit(0)
	}

	// Handle --version
	if *showVersion {
		fmt.Println(Version)
		return
	}

	// Get OS-specific default directories
	defaultConfigDir, defaultDataDir, defaultLogsDir := paths.GetDefaultDirs("citylist")

	// Apply directory overrides (priority: CLI flags > env vars > defaults)
	if *configDir == "" {
		*configDir = getEnv("CONFIG_DIR", defaultConfigDir)
	}
	if *dataDir == "" {
		*dataDir = getEnv("DATA_DIR", defaultDataDir)
	}
	if *logsDir == "" {
		*logsDir = getEnv("LOGS_DIR", defaultLogsDir)
	}

	// Create directories if they don't exist
	if err := paths.EnsureDirectories(*configDir, *dataDir, *logsDir); err != nil {
		log.Fatalf("Failed to create directories: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(*dataDir, "citylist.db")
	db, err := database.Initialize(dbPath, citylistData)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	log.Printf("📁 Config: %s", *configDir)
	log.Printf("📁 Data:   %s", *dataDir)
	log.Printf("📁 Logs:   %s", *logsDir)

	// Handle --status (before port generation for quick exit)
	if *showStatus {
		showServerStatus(db, *configDir, *dataDir, *logsDir)
		os.Exit(0)
	}

	// Get or generate port FIRST (needed for credential file)
	httpPort := getOrGeneratePort(db, *port)

	// Check if admin credentials exist, generate if needed
	// IMPORTANT: This must come AFTER port determination so credential file has correct URL
	var adminExists bool
	err = db.QueryRow("SELECT COUNT(*) FROM admin_credentials").Scan(&adminExists)
	if err != nil || !adminExists {
		log.Println("🔑 Generating admin credentials (first run)...")
		if err := setupAdminCredentials(db, *configDir, httpPort); err != nil {
			log.Fatalf("Failed to setup admin credentials: %v", err)
		}
	}

	log.Printf("🌐 Starting CityList API v%s", Version)
	log.Printf("🔧 Mode: %s", getMode(*devMode))

	// Create static filesystem (strip "static" prefix)
	staticSubFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("Failed to create static filesystem: %v", err)
	}

	// Create server
	srv := server.New(db, staticSubFS, citylistData, *address, httpPort, *devMode)

	// Start server in goroutine
	go func() {
		listenAddr := fmt.Sprintf("%s:%s", *address, httpPort)

		// Get the best URL to display to users (handles IPv6 brackets, never shows 0.0.0.0/localhost)
		baseURL := utils.FormatURL(*address, httpPort, "")
		docsURL := utils.FormatURL(*address, httpPort, "/docs")
		adminURL := utils.FormatURL(*address, httpPort, "/admin")

		log.Printf("🚀 Server listening on %s", baseURL)
		log.Printf("📖 API Docs: %s", docsURL)
		log.Printf("🔐 Admin Panel: %s", adminURL)

		if err := srv.Start(listenAddr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("⏸️  Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("✅ Server exited gracefully")
}

// printHelp displays help information
func printHelp() {
	fmt.Println("CityList API - Global Cities Database")
	fmt.Println()
	fmt.Println("Usage: citylist [OPTIONS]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --help              Show this help message")
	fmt.Println("  --version           Show version information")
	fmt.Println("  --status            Show server status")
	fmt.Println("  --config DIR        Configuration directory")
	fmt.Println("  --data DIR          Data directory")
	fmt.Println("  --logs DIR          Logs directory")
	fmt.Println("  --port PORT         HTTP port (default: random 64000-64999)")
	fmt.Println("  --address ADDR      Listen address (default: :: for dual-stack IPv4+IPv6)")
	fmt.Println("  --dev               Run in development mode")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  CONFIG_DIR          Override config directory")
	fmt.Println("  DATA_DIR            Override data directory")
	fmt.Println("  LOGS_DIR            Override logs directory")
	fmt.Println("  PORT                Override HTTP port")
	fmt.Println("  ADDRESS             Override listen address (default: ::)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  citylist                                     # Start with defaults")
	fmt.Println("  citylist --port 8080                         # Start on specific port")
	fmt.Println("  citylist --dev                               # Start in dev mode")
	fmt.Println("  citylist --config /etc/citylist              # Custom config dir")
	fmt.Println("  citylist --status                            # Show status")
}

// setupAdminCredentials generates and saves admin credentials
// port parameter is required for accurate URL generation in credential file
func setupAdminCredentials(db *sql.DB, configDir, port string) error {
	// Generate credentials
	creds, err := auth.GenerateAdminCredentials()
	if err != nil {
		return fmt.Errorf("failed to generate credentials: %w", err)
	}

	// Save to database
	_, err = db.Exec(`INSERT INTO admin_credentials (id, username, password_hash, token_hash)
		VALUES (1, ?, ?, ?)`,
		creds.Username, creds.PasswordHash, creds.TokenHash)
	if err != nil {
		return fmt.Errorf("failed to save credentials to database: %w", err)
	}

	// Save credentials file with port parameter for accurate URL
	if err := database.SaveCredentialsToFile(creds.Username, creds.Password, creds.Token, configDir, port); err != nil {
		log.Printf("Warning: Failed to save credentials file: %v", err)
	}

	// Display credentials
	fmt.Println()
	fmt.Println(strings.Repeat("=", 61))
	fmt.Println("🔐 Admin Credentials Generated")
	fmt.Println(strings.Repeat("=", 61))
	fmt.Printf("Username:  %s\n", creds.Username)
	fmt.Printf("Password:  %s\n", creds.Password)
	fmt.Printf("API Token: %s\n", creds.Token)
	fmt.Println(strings.Repeat("=", 61))
	fmt.Printf("Credentials saved to: %s/admin_credentials\n", configDir)
	fmt.Println("⚠️  Keep these credentials secure! They will not be shown again.")
	fmt.Println(strings.Repeat("=", 61))
	fmt.Println()

	return nil
}

// getOrGeneratePort gets port from CLI/env/database or generates random
func getOrGeneratePort(db *sql.DB, cliPort string) string {
	// Priority: CLI flag > env var > database > generate random
	if cliPort != "" {
		// Save to database for future use
		database.SetSetting(db, "server.http_port", cliPort, "number", "Server")
		return cliPort
	}

	// Check environment variable
	envPort := os.Getenv("PORT")
	if envPort != "" {
		database.SetSetting(db, "server.http_port", envPort, "number", "Server")
		return envPort
	}

	// Check database
	portStr := database.GetSetting(db, "server.http_port", "0")
	port, _ := strconv.Atoi(portStr)

	// Generate random port if 0 or not set
	if port == 0 {
		port = 64000 + rand.Intn(1000) // 64000-64999
		portStr = fmt.Sprintf("%d", port)
		database.SetSetting(db, "server.http_port", portStr, "number", "Server")
		log.Printf("🎲 Generated random port: %d", port)
	}

	return portStr
}

// showServerStatus displays current server status
func showServerStatus(db *sql.DB, configDir, dataDir, logsDir string) {
	fmt.Println("CityList API Server Status")
	fmt.Println(strings.Repeat("=", 51))
	fmt.Printf("Version:    %s\n", Version)
	fmt.Printf("Build Date: %s\n", BuildDate)
	fmt.Printf("Commit:     %s\n", Commit)
	fmt.Println()

	fmt.Println("Directories:")
	fmt.Printf("  Config: %s\n", configDir)
	fmt.Printf("  Data:   %s\n", dataDir)
	fmt.Printf("  Logs:   %s\n", logsDir)
	fmt.Println()

	// Database info
	var cityCount int
	db.QueryRow("SELECT COUNT(*) FROM cities").Scan(&cityCount)
	fmt.Printf("Database:\n")
	fmt.Printf("  Cities: %d\n", cityCount)
	fmt.Println()

	// Port
	port := database.GetSetting(db, "server.http_port", "not set")
	fmt.Printf("Configuration:\n")
	fmt.Printf("  HTTP Port: %s\n", port)
	fmt.Println()

	// Check if admin credentials exist
	var adminExists bool
	db.QueryRow("SELECT COUNT(*) FROM admin_credentials").Scan(&adminExists)
	fmt.Printf("Admin:\n")
	if adminExists {
		fmt.Println("  ✅ Admin credentials configured")
	} else {
		fmt.Println("  ❌ Admin credentials not set")
	}
	fmt.Println(strings.Repeat("=", 51))
}

// getMode returns mode string
func getMode(dev bool) string {
	if dev {
		return "Development"
	}
	return "Production"
}

// getEnv gets environment variable or returns default
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
