package main

import (
	"bufio"
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/apimgr/citylist/src/cities"
	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"github.com/apimgr/citylist/src/overlay/i2p"
	"github.com/apimgr/citylist/src/overlay/tor"
	"github.com/apimgr/citylist/src/paths"
	"github.com/apimgr/citylist/src/server"
	servicepkg "github.com/apimgr/citylist/src/service"
	"github.com/apimgr/citylist/src/updater"
	"github.com/apimgr/citylist/src/zipcodes"
	"golang.org/x/term"
)

//go:embed data/city.list.json data/US.zip
var cityData embed.FS

var (
	internalName             = "citylist"
	Version                  = "dev"
	CommitID                 = "unknown"
	BuildEpoch               = "0"
	OfficialSite             = ""
	Commit                   = "unknown"
	BuildDate                = "unknown"
	startedElevated          = elevatedAtStartup()
	scheduledUpdateRestarted atomic.Bool
)

const projectName = "citylist"

var serverOutputLanguage = "en"

func init() {
	if CommitID != "" && CommitID != "unknown" {
		Commit = CommitID
	}
	if epoch, err := strconv.ParseInt(BuildEpoch, 10, 64); err == nil && epoch > 0 {
		BuildDate = time.Unix(epoch, 0).UTC().Format(time.RFC3339)
	}
}

func main() {
	runServer(os.Args[1:])
}

func runServer(args []string) {
	setInitialServerOutputLanguage(args)
	if len(args) > 0 && args[0] == "scheduler" {
		if err := runSchedulerCommand(args[1:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, serverOutputFormat("error_scheduler_command", "Scheduler command failed: {error}", map[string]string{"error": err.Error()}))
			os.Exit(2)
		}
		return
	}
	if _, ok := commandHelp(args, "shell"); ok {
		printShellHelp()
		return
	}
	if _, ok := commandHelp(args, "service"); ok {
		printServiceHelp()
		return
	}
	if _, ok := commandHelp(args, "update"); ok {
		printUpdateHelp()
		return
	}
	if maintenanceHelpRequested(args) {
		printMaintenanceHelp()
		return
	}

	// Get default directories
	configDir, dataDir, logsDir := paths.GetDefaultDirs(projectName)

	// Command-line flags
	port := flag.String("port", "", "Server port (overrides config)")
	address := flag.String("address", "", "Server address (overrides config)")
	configDirFlag := flag.String("config", "", "Configuration directory")
	dataDirFlag := flag.String("data", "", "Data directory")
	cacheDirFlag := flag.String("cache", "", "Cache directory")
	logsDirFlag := flag.String("log", "", "Logs directory")
	backupDirFlag := flag.String("backup", "", "Backup directory")
	includeSSL := flag.Bool("include-ssl", false, "Include SSL certificates in a maintenance backup")
	includeData := flag.Bool("include-data", false, "Include data files in a maintenance backup")
	pidFile := flag.String("pid", "", "PID file path")
	baseURL := flag.String("baseurl", "/", "URL path prefix")
	shellCmd := flag.String("shell", "", "Print shell completions or initialization command")
	languageFlag := flag.String("lang", "auto", "Language for output")
	daemon := flag.Bool("daemon", false, "Run in the background")
	version := flag.Bool("version", false, "Print version information")
	flag.BoolVar(version, "v", false, "Print version information")
	status := flag.Bool("status", false, "Check service status (for healthcheck)")
	help := flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")
	debug := flag.Bool("debug", false, "Enable debug logging")
	colorMode := flag.String("color", "auto", "Color output: auto, yes, or no")
	_ = debug

	// Service commands
	serviceCmd := flag.String("service", "", "Service commands: start, stop, restart, reload, status, --install, --uninstall, --disable, --help")

	// Maintenance commands
	maintenanceCmd := flag.String("maintenance", "", "Maintenance commands: backup, restore, update, mode, setup, pgp, secret, token, data, compliance")

	// Application mode
	modeFlag := flag.String("mode", "", "Application mode: production, development, debug")

	// Update commands
	updateCmd := flag.String("update", "", "Update commands: check, yes, branch {stable|beta|devel}")
	i2pFlag := flag.String("i2p", "", "Enable or disable the optional I2P eepsite")

	if err := flag.CommandLine.Parse(normalizeOptionalBoolFlag(args, "--i2p")); err != nil {
		fmt.Fprintln(os.Stderr, serverOutputFormat("error_flags", "Invalid command-line flags: {error}", map[string]string{"error": err.Error()}))
		os.Exit(2)
	}
	updateRequested := false
	i2pFlagSet := false
	baseURLFlagSet := false
	flag.Visit(func(item *flag.Flag) {
		if item.Name == "update" {
			updateRequested = true
		}
		if item.Name == "i2p" {
			i2pFlagSet = true
		}
		if item.Name == "baseurl" {
			baseURLFlagSet = true
		}
	})

	if *colorMode != "auto" && *colorMode != "yes" && *colorMode != "no" {
		fmt.Fprintln(os.Stderr, serverOutputText("error_color", "--color must be auto, yes, or no"))
		os.Exit(2)
	}
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled && *colorMode == "auto" {
		*colorMode = "no"
	}
	if *debug {
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC)
	}

	// Handle help flag
	if *help {
		printHelp()
		return
	}

	// Handle version flag
	if *version {
		commit := Commit
		if len(commit) > 7 {
			commit = commit[:7]
		}
		fmt.Println(serverOutputFormat("version", "{binary} {version} ({commit})\nBuilt: {build_date}\nGo: {go_version}\nOS/Arch: {os}/{arch}", map[string]string{
			"binary": filepath.Base(os.Args[0]), "version": Version, "commit": commit, "build_date": BuildDate,
			"go_version": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		}))
		return
	}
	if *shellCmd != "" {
		if err := runServerShell(os.Stdout, *shellCmd, flag.Args()); err != nil {
			fmt.Fprintln(os.Stderr, serverOutputFormat("error_shell", "Shell integration failed: {error}", map[string]string{"error": err.Error()}))
			os.Exit(2)
		}
		return
	}

	// Override directories from flags
	if *configDirFlag != "" {
		configDir = *configDirFlag
	}
	if *dataDirFlag != "" {
		dataDir = *dataDirFlag
	}
	if *logsDirFlag != "" {
		logsDir = *logsDirFlag
	}
	// Override from environment
	if envConfig := os.Getenv("CONFIG_DIR"); envConfig != "" && *configDirFlag == "" {
		configDir = envConfig
	}
	if envData := os.Getenv("DATA_DIR"); envData != "" && *dataDirFlag == "" {
		dataDir = envData
	}
	if envLogs := os.Getenv("LOGS_DIR"); envLogs != "" && *logsDirFlag == "" {
		logsDir = envLogs
	}
	cacheDir := paths.GetCacheDir(projectName)
	if *cacheDirFlag != "" {
		cacheDir = *cacheDirFlag
	}
	backupDir := paths.GetBackupDirFor(projectName, dataDir, *backupDirFlag)

	if *serviceCmd != "" {
		databaseDir := paths.GetDatabaseDir(dataDir)
		if *serviceCmd == "--install" {
			serviceConfig, err := config.LoadBasic(filepath.Join(configDir, "server.yml"))
			if err != nil {
				log.Fatal(serverOutputFormat("error_service_config", "Failed to read configuration for service installation: {error}", map[string]string{"error": err.Error()}))
			}
			if serviceConfig.Server.Database.Driver == "sqlite" {
				databasePath, err := resolveDatabasePath(serviceConfig.Server.Database.URL, dataDir)
				if err != nil {
					log.Fatal(serverOutputFormat("error_service_database_url", "Invalid database URL for service installation: {error}", map[string]string{"error": err.Error()}))
				}
				databaseDir = filepath.Dir(databasePath)
			}
		}
		options := servicepkg.Options{
			Name:        projectName,
			Org:         "apimgr",
			Language:    serverOutputLanguage,
			ConfigDir:   configDir,
			DataDir:     dataDir,
			DatabaseDir: databaseDir,
			CacheDir:    cacheDir,
			LogsDir:     logsDir,
			BackupDir:   backupDir,
			UserMode:    os.Geteuid() != 0 && runtime.GOOS != "windows",
		}
		if err := handleServiceCommand(*serviceCmd, options); err != nil {
			log.Fatal(serverOutputFormat("error_service_command", "Service command failed: {error}", map[string]string{"error": err.Error()}))
		}
		return
	}
	systemMode := runtime.GOOS != "windows" && startedElevated
	if systemMode && !*status && !updateRequested && *maintenanceCmd == "" && *modeFlag == "" {
		applied, applyErr := updater.ApplyStaged(dataDir, Version, mustBuildEpoch())
		if applyErr != nil {
			log.Print(serverOutputFormat("error_update_install", "Update installation failed: {error}", map[string]string{"error": applyErr.Error()}))
		} else if applied {
			if err := updater.Restart(); err != nil {
				log.Fatal(serverOutputFormat("error_update_restart", "Updated binary installed, but restart failed: {error}", map[string]string{"error": err.Error()}))
			}
		}
	}
	if systemMode && !*status && !updateRequested && *maintenanceCmd == "" && *modeFlag == "" {
		databaseDir := paths.GetDatabaseDir(dataDir)
		startupConfig, err := config.LoadBasic(filepath.Join(configDir, "server.yml"))
		if err != nil {
			log.Fatal(serverOutputFormat("error_read_listener_config", "Failed to read listener configuration: {error}", map[string]string{"error": err.Error()}))
		}
		if startupConfig.Server.Database.Driver == "sqlite" {
			databasePath, err := resolveDatabasePath(startupConfig.Server.Database.URL, dataDir)
			if err != nil {
				log.Fatal(serverOutputFormat("error_database_url", "Invalid database URL: {error}", map[string]string{"error": err.Error()}))
			}
			databaseDir = filepath.Dir(databasePath)
		}
		options := servicepkg.Options{Name: projectName, Org: "apimgr", Language: serverOutputLanguage, ConfigDir: configDir, DataDir: dataDir, CacheDir: cacheDir, LogsDir: logsDir, BackupDir: backupDir}
		if err := servicepkg.PrepareSystemRuntime(options, databaseDir); err != nil {
			log.Fatal(serverOutputFormat("error_service_runtime", "Failed to prepare service runtime: {error}", map[string]string{"error": err.Error()}))
		}
	}
	if !systemMode && !*status && !updateRequested && *maintenanceCmd == "" && *modeFlag == "" {
		for _, directory := range []string{configDir, dataDir, cacheDir, logsDir, backupDir, paths.GetDatabaseDir(dataDir)} {
			if err := paths.EnsurePrivateDir(directory); err != nil {
				log.Fatal(serverOutputFormat("error_user_runtime_directory", "Failed to prepare user runtime directory {path}: {error}", map[string]string{"path": directory, "error": err.Error()}))
			}
		}
	}
	if err := paths.EnforcePrivatePaths(configDir, dataDir); err != nil {
		log.Fatal(serverOutputFormat("error_private_paths", "Failed to secure private configuration paths: {error}", map[string]string{"error": err.Error()}))
	}

	configPath := filepath.Join(configDir, "server.yml")
	configMissing := false
	if _, err := os.Stat(configPath); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal(serverOutputFormat("error_inspect_config", "Failed to inspect configuration file: {error}", map[string]string{"error": err.Error()}))
		}
		configMissing = true
	}
	earlyConfig, err := config.LoadBasic(configPath)
	if err != nil {
		log.Fatal(serverOutputFormat("error_read_listener_config", "Failed to read listener configuration: {error}", map[string]string{"error": err.Error()}))
	}
	if configMissing {
		setServerOutputLanguage(*languageFlag, nil)
	} else {
		setServerOutputLanguage(*languageFlag, earlyConfig)
	}
	if updateRequested || *maintenanceCmd == "update" {
		command := *updateCmd
		updateArgs := flag.Args()
		if *maintenanceCmd == "update" {
			command = ""
			if len(updateArgs) > 0 {
				command, updateArgs = updateArgs[0], updateArgs[1:]
			}
		}
		if command == "" {
			command = "yes"
		}
		updateServiceOptions := servicepkg.Options{
			Name: projectName, Org: "apimgr", ConfigDir: configDir, DataDir: dataDir,
			CacheDir: cacheDir, LogsDir: logsDir, BackupDir: backupDir,
			Language: serverOutputLanguage,
			UserMode: os.Geteuid() != 0 && runtime.GOOS != "windows",
		}
		handleUpdateCommand(command, earlyConfig, configPath, updateArgs, updateServiceOptions)
		return
	}
	serverPort := earlyConfig.Server.Port
	defaultPortSelected := serverPort == "" && *port == "" && os.Getenv("PORT") == ""
	if *port != "" {
		serverPort = *port
	} else if envPort := os.Getenv("PORT"); envPort != "" {
		serverPort = envPort
	}
	serverAddress := earlyConfig.Server.Address
	if *address != "" {
		serverAddress = *address
	} else if envAddress := os.Getenv("LISTEN"); envAddress != "" {
		serverAddress = envAddress
	}
	if serverAddress == "" {
		serverAddress = "[::]"
	}
	if serverPort == "" {
		if isApplicationContainer() {
			serverPort = "80"
		} else {
			serverPort, err = selectDefaultPort(serverAddress)
			if err != nil {
				log.Fatal(serverOutputFormat("error_select_port", "Failed to select default listen port: {error}", map[string]string{"error": err.Error()}))
			}
		}
	}
	daemonize := (*daemon || earlyConfig.Server.Daemonize) && os.Getenv("_DAEMON_CHILD") != "1" && !isApplicationContainer() && !serviceManagedStart()
	if *status {
		checkPort := strings.Split(serverPort, ",")[0]
		if err := checkHealth(checkPort); err != nil {
			fmt.Fprintln(os.Stderr, serverOutputFormat("status_failed", "Health check failed: {error}", map[string]string{"error": err.Error()}))
			os.Exit(1)
		}
		fmt.Println(serverOutputText("status_ok", "OK"))
		os.Exit(0)
	}
	var privilegedListeners []net.Listener
	if systemMode {
		if daemonize {
			if err := startDaemon(logsDir); err != nil {
				log.Fatal(serverOutputFormat("error_start_daemon", "Failed to start daemon: {error}", map[string]string{"error": err.Error()}))
			}
			return
		}
		privilegedListeners, err = bindPrivilegedPorts(serverAddress, serverPort)
		if err != nil {
			log.Fatal(serverOutputFormat("error_bind_server_port", "Failed to bind server port: {error}", map[string]string{"error": err.Error()}))
		}
		if err := dropToServiceAccount(projectName, dataDir); err != nil {
			closeListeners(privilegedListeners)
			log.Fatal(serverOutputFormat("error_drop_privileges", "Failed to drop privileges: {error}", map[string]string{"error": err.Error()}))
		}
	} else {
		for _, directory := range []string{configDir, dataDir, cacheDir, logsDir, backupDir, paths.GetDatabaseDir(dataDir)} {
			if err := os.MkdirAll(directory, 0700); err != nil {
				log.Fatal(serverOutputFormat("error_create_private_directory", "Failed to create private directory {path}: {error}", map[string]string{"path": directory, "error": err.Error()}))
			}
			if err := os.Chmod(directory, 0700); err != nil {
				log.Fatal(serverOutputFormat("error_secure_private_directory", "Failed to secure private directory {path}: {error}", map[string]string{"path": directory, "error": err.Error()}))
			}
		}
		if daemonize {
			if err := startDaemon(logsDir); err != nil {
				log.Fatal(serverOutputFormat("error_start_daemon", "Failed to start daemon: {error}", map[string]string{"error": err.Error()}))
			}
			return
		}
	}
	privilegedListeners, err = bindRemainingPorts(serverAddress, serverPort, privilegedListeners)
	if err != nil {
		log.Fatal(serverOutputFormat("error_bind_server_ports", "Failed to bind server ports: {error}", map[string]string{"error": err.Error()}))
	}
	if !isApplicationContainer() {
		pidPath := *pidFile
		if pidPath == "" {
			if systemMode {
				pidPath = filepath.Join("/var/run", "apimgr", projectName, projectName+".pid")
			} else {
				pidPath = filepath.Join(dataDir, projectName+".pid")
			}
		}
		running, processID, pidErr := checkPIDFile(pidPath)
		if pidErr != nil {
			log.Fatal(serverOutputFormat("error_check_pid", "Failed to check PID file: {error}", map[string]string{"error": pidErr.Error()}))
		}
		if running {
			log.Fatal(serverOutputFormat("error_already_running", "Server is already running with PID {pid}", map[string]string{"pid": strconv.Itoa(processID)}))
		}
		pidMode := os.FileMode(0600)
		if systemMode {
			pidMode = 0644
		}
		if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), pidMode); err != nil {
			log.Fatal(serverOutputFormat("error_write_pid", "Failed to write PID file: {error}", map[string]string{"error": err.Error()}))
		}
		defer func() { _ = os.Remove(pidPath) }()
	}

	// Load the complete configuration only after any Unix privilege drop.
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(serverOutputFormat("error_load_config", "Failed to load configuration: {error}", map[string]string{"error": err.Error()}))
	}
	if strings.TrimSpace(cfg.Server.FQDN) == "" {
		cfg.Server.FQDN = server.GetFQDN(projectName)
	}
	if *port != "" || os.Getenv("PORT") != "" || cfg.Server.Port == "" {
		cfg.Server.Port = serverPort
	}
	if *address != "" || os.Getenv("BIND_ADDRESS") != "" || cfg.Server.Address == "" {
		cfg.Server.Address = serverAddress
	}
	cfg.Server.Logs.Audit.Keep = effectiveAuditRetention(cfg.Server.Compliance, cfg.Server.Logs.Audit.Keep)
	if err := configureLogs(logsDir, cfg.Server.Mode, cfg.Server.Logs); err != nil {
		log.Fatal(serverOutputFormat("error_configure_logging", "Failed to configure logging: {error}", map[string]string{"error": err.Error()}))
	}
	if err := server.ConfigureAccessLog(logsDir, cfg.Server.Logs); err != nil {
		log.Fatal(serverOutputFormat("error_configure_access_logging", "Failed to configure access logging: {error}", map[string]string{"error": err.Error()}))
	}
	i2pEnabled, err := config.ResolveI2PEnabled(cfg, *i2pFlag, i2pFlagSet, isApplicationContainer())
	if err != nil {
		log.Fatal(serverOutputFormat("error_i2p_setting", "Invalid I2P setting: {error}", map[string]string{"error": err.Error()}))
	}
	cfg.Server.I2P.Enabled = &i2pEnabled
	if configMissing || defaultPortSelected {
		cfg.Server.Port = serverPort
		if configMissing {
			cfg.Server.Address = serverAddress
		}
		if err := config.SaveNodeConfig(configPath, cfg); err != nil {
			log.Fatal(serverOutputFormat("error_save_listener_config", "Failed to save first-run listener configuration: {error}", map[string]string{"error": err.Error()}))
		}
	}
	if *languageFlag != "" && *languageFlag != "auto" {
		if i18n.IsSupported(*languageFlag) {
			cfg.Server.I18n.DefaultLanguage = strings.ToLower(*languageFlag)
		} else {
			cfg.Server.I18n.DefaultLanguage = "en"
		}
	}
	if cfg.InitialToken != "" {
		if _, err := fmt.Fprintln(os.Stdout, serverOutputFormat("startup_initial_token", "Initial operator token (save this value): {token}", map[string]string{"token": cfg.InitialToken})); err != nil {
			log.Print(serverOutputText("error_print_initial_token", "Unable to print initial operator token"))
		}
	}

	// Handle mode flag
	if *modeFlag != "" {
		setApplicationMode(*modeFlag, configPath)
		return
	}

	// Handle maintenance commands
	if *maintenanceCmd != "" {
		handleMaintenanceCommand(*maintenanceCmd, configDir, dataDir, logsDir, backupDir, configPath, cfg, *includeSSL, *includeData)
		return
	}
	if err := server.InitializeSMTP(cfg, configPath); err != nil {
		log.Print(serverOutputFormat("startup_smtp_failed", "SMTP initialization failed; email is disabled: {error}", map[string]string{"error": err.Error()}))
		cfg.Server.Notifications.Email.SMTP.Host = ""
	}

	if cfg.Server.Port != "" && *port == "" && os.Getenv("PORT") == "" {
		serverPort = cfg.Server.Port
	}

	databaseDir := paths.GetDatabaseDir(dataDir)
	if cfg.Server.Database.Driver == "sqlite" {
		dbPath, pathErr := resolveDatabasePath(cfg.Server.Database.URL, dataDir)
		if pathErr != nil {
			log.Fatal(serverOutputFormat("error_database_url", "Invalid database URL: {error}", map[string]string{"error": pathErr.Error()}))
		}
		databaseDir = filepath.Dir(dbPath)
	}
	if !systemMode && cfg.Server.Database.Driver == "sqlite" {
		if err := paths.EnsurePrivateDir(databaseDir); err != nil {
			log.Fatal(serverOutputFormat("error_database_directory", "Failed to secure database directory {path}: {error}", map[string]string{"path": databaseDir, "error": err.Error()}))
		}
	}
	store, err := openConfiguredDatabase(cfg, dataDir)
	if err != nil {
		log.Fatal(serverOutputFormat("error_open_database", "Failed to open database: {error}", map[string]string{"error": err.Error()}))
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		_ = store.StartCacheSync(context.Background())
		if !store.UsingOfflineCache() {
			_ = store.Close()
			log.Fatal(serverOutputFormat("error_initialize_database_schema", "Failed to initialize database schema: {error}", map[string]string{"error": err.Error()}))
		}
		log.Print("Remote database unavailable; starting with the existing read-only SQLite cache")
	} else if err := store.StartCacheSync(context.Background()); err != nil {
		_ = store.Close()
		log.Fatal(serverOutputFormat("error_initialize_database_schema", "Failed to initialize database schema: {error}", map[string]string{"error": err.Error()}))
	}
	if err := server.InitializeInstallationSecret(context.Background(), store); err != nil && !store.UsingOfflineCache() {
		_ = store.Close()
		log.Fatal(serverOutputFormat("error_initialize_installation_secret", "Failed to initialize installation secret: {error}", map[string]string{"error": err.Error()}))
	}
	rotationCtx, rotationCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	if err := server.ResumeSecretRotationsWithAudit(rotationCtx, configPath, configDir, cfg, store, func(event string) error {
		return writeAuditEvent(event, "warn", "success", "scheduler", "127.0.0.1", map[string]string{"type": "configuration", "id": event}, map[string]any{"reason": "completed interrupted secret rotation"})
	}); err != nil && !store.UsingOfflineCache() {
		rotationCancel()
		_ = store.Close()
		log.Fatal(serverOutputFormat("error_resume_secret_rotation", "Failed to resume secret rotation: {error}", map[string]string{"error": err.Error()}))
	}
	rotationCancel()

	// Load embedded city data
	data, err := cityData.ReadFile("data/city.list.json")
	if err != nil {
		log.Fatal(serverOutputFormat("error_city_data_read", "Failed to read embedded city data: {error}", map[string]string{"error": err.Error()}))
	}

	// Create cities service
	citiesService, err := cities.NewService(data)
	if err != nil {
		log.Fatal(serverOutputFormat("error_city_service_init", "Failed to initialize cities service: {error}", map[string]string{"error": err.Error()}))
	}

	log.Print(serverOutputFormat("startup_cities_loaded", "Loaded {cities} cities from {countries} countries", map[string]string{"cities": strconv.Itoa(citiesService.Count()), "countries": strconv.Itoa(citiesService.CountryCount())}))

	postalArchive, err := cityData.ReadFile("data/US.zip")
	if err != nil {
		log.Fatal(serverOutputFormat("error_zip_data_read", "Failed to read embedded US postal data: {error}", map[string]string{"error": err.Error()}))
	}
	postalService, err := zipcodes.NewService(postalArchive)
	if err != nil {
		log.Fatal(serverOutputFormat("error_zip_service_init", "Failed to initialize US postal service: {error}", map[string]string{"error": err.Error()}))
	}
	log.Print(serverOutputFormat("startup_zipcodes_loaded", "Loaded {count} US postal codes", map[string]string{"count": strconv.Itoa(postalService.Count())}))

	// Set build info for server
	server.Version = Version
	server.Commit = Commit
	server.BuildDate = BuildDate

	// Create and start server
	srv := server.NewWithDatabase(citiesService, postalService, cfg, serverAddress, serverPort, Version, BuildDate, Commit, store)
	if err := srv.MigrateOwnerTokenLast4(context.Background()); err != nil && !store.UsingOfflineCache() {
		_ = store.Close()
		log.Fatal(serverOutputFormat("error_migrate_owner_token_suffix", "Failed to migrate owner-token metadata: {error}", map[string]string{"error": err.Error()}))
	}
	srv.SetConfigDir(configDir)
	srv.SetDataDir(dataDir)
	effectiveBaseURL := cfg.Server.BaseURL
	if baseURLFlagSet {
		effectiveBaseURL = *baseURL
	}
	if err := srv.SetBaseURL(effectiveBaseURL); err != nil {
		_ = store.Close()
		log.Fatal(serverOutputFormat("error_base_url", "Invalid base URL: {error}", map[string]string{"error": err.Error()}))
	}

	// Handle graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.StartSecretCleanup(ctx, store)

	torOptions := tor.DefaultOptions(configDir, dataDir, logsDir)
	torOptions.TorBinary = cfg.Server.Tor.Binary
	torOptions.UseNetwork = cfg.Server.Tor.UseNetwork
	torOptions.MaxCircuits = cfg.Server.Tor.MaxCircuits
	torOptions.StartupTimeout = time.Duration(cfg.Server.Tor.StartupTimeout) * time.Second
	torOptions.CircuitTimeout = time.Duration(cfg.Server.Tor.CircuitTimeout) * time.Second
	torOptions.BootstrapTimeout = time.Duration(cfg.Server.Tor.BootstrapTimeout) * time.Second
	torOptions.SafeLogging = cfg.Server.Tor.SafeLogging
	torOptions.MaxStreamsPerCircuit = cfg.Server.Tor.MaxStreamsPerCircuit
	torOptions.CloseCircuitOnStreamLimit = cfg.Server.Tor.CloseCircuitOnStreamLimit
	torOptions.BandwidthRate = cfg.Server.Tor.BandwidthRate
	torOptions.BandwidthBurst = cfg.Server.Tor.BandwidthBurst
	torOptions.MaxMonthlyBandwidth = cfg.Server.Tor.MaxMonthlyBandwidth
	torOptions.NumIntroPoints = cfg.Server.Tor.NumIntroPoints
	torOptions.VirtualPort = cfg.Server.Tor.VirtualPort
	torManager, torErr := tor.Start(ctx, torOptions)
	if torErr != nil {
		log.Print(serverOutputFormat("startup_tor_unavailable", "Tor hidden service unavailable: {error}", map[string]string{"error": torErr.Error()}))
	}
	if torManager != nil {
		defer func() {
			if err := torManager.Close(); err != nil {
				log.Print(serverOutputFormat("shutdown_tor_failed", "Failed to stop Tor manager: {error}", map[string]string{"error": err.Error()}))
			}
		}()
	}

	i2pOptions := i2p.DefaultOptions()
	i2pOptions.Enabled = i2pEnabled
	i2pOptions.Binary = cfg.Server.I2P.Binary
	i2pOptions.SAMAddress = cfg.Server.I2P.SAMAddress
	i2pOptions.VirtualPort = cfg.Server.I2P.VirtualPort
	i2pOptions.InboundLength = cfg.Server.I2P.InboundLength
	i2pOptions.OutboundLength = cfg.Server.I2P.OutboundLength
	i2pOptions.InboundQuantity = cfg.Server.I2P.InboundQuantity
	i2pOptions.OutboundQuantity = cfg.Server.I2P.OutboundQuantity
	i2pOptions.SignatureType = cfg.Server.I2P.SignatureType
	i2pOptions.BootstrapTimeout = time.Duration(cfg.Server.I2P.BootstrapTimeout) * time.Second
	i2pOptions.ConfigDir = configDir
	i2pOptions.DataDir = dataDir
	i2pOptions.LogDir = logsDir
	i2pOptions.Handler = srv.Router()
	i2pManager, i2pErr := i2p.Start(ctx, i2pOptions)
	if i2pErr != nil {
		log.Print(serverOutputFormat("startup_i2p_unavailable", "I2P eepsite unavailable: {error}", map[string]string{"error": i2pErr.Error()}))
	}
	if i2pManager != nil {
		defer func() {
			if err := i2pManager.Close(); err != nil {
				log.Print(serverOutputFormat("shutdown_i2p_failed", "Failed to stop I2P manager: {error}", map[string]string{"error": err.Error()}))
			}
		}()
	}
	srv.SetOverlayManagers(torManager, i2pManager, torErr != nil, i2pEnabled && i2pErr != nil)
	operatorNotify := operatorEventNotifier(func(notifyContext context.Context, event string, details map[string]string) error {
		return server.NotifyOperator(notifyContext, cfg, event, details)
	})
	healthTasks := map[string]func() error{}
	if torManager != nil && torManager.Snapshot().Enabled {
		healthTasks["tor_health"] = func() error {
			if status := torManager.Snapshot().Status; status == "unavailable" {
				return errors.New("tor health check reports unavailable")
			}
			return nil
		}
	}
	if i2pEnabled {
		healthTasks["i2p_health"] = func() error {
			if i2pManager == nil {
				return errors.New("I2P health manager is unavailable")
			}
			if status := i2pManager.Snapshot().Status; status == "unavailable" {
				return errors.New("I2P health check reports unavailable")
			}
			return nil
		}
	}
	appScheduler, err := newApplicationScheduler(cfg, configDir, dataDir, logsDir, backupDir, store, ctx, healthTasks, operatorNotify)
	if err != nil {
		_ = store.Close()
		log.Fatal(serverOutputFormat("error_scheduler_init", "Failed to initialize scheduler: {error}", map[string]string{"error": err.Error()}))
	}
	appScheduler.Start()
	initializeSecurityData(appScheduler, cfg, dataDir)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		for {
			select {
			case <-scheduledUpdateRestart:
				scheduledUpdateRestarted.Store(true)
				log.Print("staged update requires graceful restart")
				cancel()
				return
			case sig := <-sigChan:
				switch sig {
				case syscall.SIGHUP:
					continue
				default:
					log.Print(serverOutputFormat("signal_shutdown", "Received signal {signal}, shutting down...", map[string]string{"signal": sig.String()}))
					cancel()
					return
				}
			}
		}
	}()

	watcher, err := config.NewConfigWatcher(configPath, cfg, func(_, next *config.Config, restartRequired []string) error {
		next.Server.Logs.Audit.Keep = effectiveAuditRetention(next.Server.Compliance, next.Server.Logs.Audit.Keep)
		if *port != "" || os.Getenv("PORT") != "" {
			next.Server.Port = serverPort
		}
		if *address != "" || os.Getenv("BIND_ADDRESS") != "" {
			next.Server.Address = serverAddress
		}
		if baseURLFlagSet {
			next.Server.BaseURL = *baseURL
		}
		if err := configureLogs(logsDir, next.Server.Mode, next.Server.Logs); err != nil {
			return fmt.Errorf("configure reloaded logs: %w", err)
		}
		if err := server.ConfigureAccessLog(logsDir, next.Server.Logs); err != nil {
			return fmt.Errorf("configure reloaded access log: %w", err)
		}
		return srv.ApplyConfig(next, restartRequired, next.Server.BaseURL)
	})
	if err != nil {
		log.Print(serverOutputFormat("error_config_watcher", "Unable to start configuration watcher: {error}", map[string]string{"error": err.Error()}))
	} else {
		go watcher.Run(ctx)
	}

	addr := net.JoinHostPort(strings.Trim(serverAddress, "[]"), strings.Split(serverPort, ",")[0])
	log.Print(serverOutputFormat("startup_listening", "Starting citylist server on {address}", map[string]string{"address": addr}))

	if torManager != nil && torManager.Listener() != nil {
		privilegedListeners = append(privilegedListeners, torManager.Listener())
	}
	if err := srv.RunListeners(ctx, addr, privilegedListeners); err != nil {
		appScheduler.Stop()
		_ = store.Close()
		log.Fatal(serverOutputFormat("error_server", "Server error: {error}", map[string]string{"error": err.Error()}))
	}
	appScheduler.Stop()
	if err := store.Close(); err != nil {
		log.Fatal(serverOutputFormat("error_close_database", "Failed to close database: {error}", map[string]string{"error": err.Error()}))
	}
	if scheduledUpdateRestarted.Load() {
		if torManager != nil {
			_ = torManager.Close()
		}
		if i2pManager != nil {
			_ = i2pManager.Close()
		}
		os.Exit(75)
	}
}

func selectDefaultPort(address string) (string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		number, err := cryptorand.Int(cryptorand.Reader, big.NewInt(1000))
		if err != nil {
			return "", fmt.Errorf("generate random port: %w", err)
		}
		port := strconv.Itoa(64000 + int(number.Int64()))
		listener, err := net.Listen("tcp", net.JoinHostPort(strings.Trim(address, "[]"), port))
		if err != nil {
			continue
		}
		if err := listener.Close(); err != nil {
			return "", fmt.Errorf("release selected port %s: %w", port, err)
		}
		return port, nil
	}
	return "", fmt.Errorf("no unused port found in 64000-64999")
}

func resolveDatabasePath(configuredURL, dataDir string) (string, error) {
	if configuredURL == "" {
		return filepath.Join(paths.GetDatabaseDir(dataDir), "server.db"), nil
	}
	configuredURL = strings.ReplaceAll(configuredURL, "{data_dir}", dataDir)
	if strings.HasPrefix(configuredURL, "file:") {
		parsed, err := url.Parse(configuredURL)
		if err != nil || parsed.Path == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("SQLite URL must contain only a file path")
		}
		return filepath.FromSlash(parsed.Path), nil
	}
	if !filepath.IsAbs(configuredURL) {
		configuredURL = filepath.Join(dataDir, configuredURL)
	}
	return filepath.Clean(configuredURL), nil
}

func resolveDatabaseCachePath(configuredPath, dataDir string) (string, error) {
	configuredPath = strings.TrimSpace(strings.ReplaceAll(configuredPath, "${DATA_DIR}", dataDir))
	configuredPath = strings.ReplaceAll(configuredPath, "$DATA_DIR", dataDir)
	if configuredPath == "" {
		return "", errors.New("server.database.cache.path is required when cache is enabled")
	}
	if !filepath.IsAbs(configuredPath) {
		configuredPath = filepath.Join(dataDir, configuredPath)
	}
	return filepath.Clean(configuredPath), nil
}

func openConfiguredDatabase(cfg *config.Config, dataDir string) (*database.Store, error) {
	if cfg == nil {
		return nil, errors.New("database configuration is required")
	}
	dbConfig := cfg.Server.Database
	pool := database.PoolConfig{
		MaxOpen: dbConfig.Pool.MaxOpen, MaxIdle: dbConfig.Pool.MaxIdle,
		MaxLifetime: dbConfig.Pool.MaxLifetime, MaxIdleTime: dbConfig.Pool.MaxIdleTime,
	}
	switch config.NormalizeDatabaseDriver(dbConfig.Driver) {
	case "sqlite":
		path, err := resolveDatabasePath(dbConfig.URL, dataDir)
		if err != nil {
			return nil, err
		}
		return database.OpenSQLite(path, pool)
	case "libsql":
		store, err := database.OpenLibSQL(dbConfig.URL, dbConfig.Token, pool)
		if err != nil {
			return nil, err
		}
		if dbConfig.Cache.Enabled {
			cachePath, err := resolveDatabaseCachePath(dbConfig.Cache.Path, dataDir)
			if err != nil {
				_ = store.Close()
				return nil, err
			}
			syncEvery := 30 * time.Second
			if value := strings.TrimSpace(dbConfig.Cache.SyncInterval); value != "" {
				syncEvery, _ = time.ParseDuration(value)
			}
			maxAge := time.Hour
			if value := strings.TrimSpace(dbConfig.Cache.MaxAge); value != "" {
				maxAge, _ = time.ParseDuration(value)
			}
			if err := store.ConfigureLocalCache(cachePath, syncEvery, maxAge, dbConfig.Cache.OfflineMode); err != nil {
				_ = store.Close()
				return nil, err
			}
		}
		return store, nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", dbConfig.Driver)
	}
}

func setInitialServerOutputLanguage(args []string) {
	configDir, _, _ := paths.GetDefaultDirs(projectName)
	flagLang := languageArgument(args)
	configFlagSet := false
	for index, arg := range args {
		if arg == "--config" && index+1 < len(args) {
			configDir = args[index+1]
			configFlagSet = true
		} else if strings.HasPrefix(arg, "--config=") {
			configDir = strings.TrimPrefix(arg, "--config=")
			configFlagSet = true
		}
	}
	if !configFlagSet {
		if envConfig := os.Getenv("CONFIG_DIR"); envConfig != "" {
			configDir = envConfig
		}
	}
	var cfg *config.Config
	configPath := filepath.Join(configDir, "server.yml")
	if _, err := os.Stat(configPath); err == nil {
		cfg, _ = config.LoadBasic(configPath)
	}
	setServerOutputLanguage(flagLang, cfg)
}

func languageArgument(args []string) string {
	for index, arg := range args {
		if arg == "--lang" && index+1 < len(args) {
			return args[index+1]
		}
		if strings.HasPrefix(arg, "--lang=") {
			return strings.TrimPrefix(arg, "--lang=")
		}
	}
	return "auto"
}

func setServerOutputLanguage(flagLang string, cfg *config.Config) {
	lang := strings.ToLower(strings.TrimSpace(flagLang))
	if lang == "" || lang == "auto" {
		lang = ""
		if cfg != nil {
			lang = strings.ToLower(strings.TrimSpace(cfg.Server.I18n.DefaultLanguage))
		}
		if lang == "" || lang == "auto" {
			lang = os.Getenv("LC_ALL")
			if lang == "" {
				lang = os.Getenv("LANG")
			}
			lang = strings.ToLower(strings.TrimSpace(strings.SplitN(strings.SplitN(lang, ".", 2)[0], "_", 2)[0]))
		}
	}
	if !i18n.IsSupported(lang) {
		lang = "en"
	}
	serverOutputLanguage = lang
}

func serverOutputText(key, fallback string) string {
	value := i18n.Translate(serverOutputLanguage, "server_cli."+key)
	if value == "server_cli."+key {
		return fallback
	}
	return value
}

func serverOutputFormat(key, fallback string, values map[string]string) string {
	message := serverOutputText(key, fallback)
	for name, value := range values {
		message = strings.ReplaceAll(message, "{"+name+"}", value)
	}
	return message
}

func printHelp() {
	binaryName := filepath.Base(os.Args[0])
	message := serverOutputText("help", `{binary} {version} - Global city and US ZIP code lookup service

Usage:
  {binary} [flags]

Information:
  -h, --help                             Show help
  -v, --version                          Show version
      --status                           Show server status and health

Shell Integration:
      --shell completions [SHELL]        Print shell completions
      --shell init [SHELL]               Print shell init command
      --shell --help                     Show shell help

Server Configuration:
      --mode {production|development|debug}  Application mode
      --config DIR                       Config directory
      --data DIR                         Data directory
      --cache DIR                        Cache directory
      --log DIR                          Log directory
      --backup DIR                       Backup directory
      --pid FILE                         PID file path
      --address ADDR                     Listen address (default: [::])
      --port PORT                        Listen port (default: random 64000-64999, 80 in container)
      --i2p[=BOOL]                       Enable or disable the optional I2P eepsite
      --baseurl PATH                     URL path prefix (default: /)
      --daemon                           Run as daemon
      --debug                            Enable debug logging
      --color {auto|yes|no}              Color output (default: auto)
      --lang CODE                        Language for output (default: auto)

Service Management:
      --service CMD                      Service management (run --service --help for details)
      --maintenance CMD                  Maintenance operations (run --maintenance --help for details)
      --update [CMD]                     Check or install updates (run --update --help for details)

Run '{binary} <flag> --help' (for example '{binary} --service --help') for command details.`)
	message = strings.ReplaceAll(message, "{binary}", binaryName)
	message = strings.ReplaceAll(message, "{version}", Version)
	fmt.Println(message)
	fmt.Printf("\n%s:\n  %s\n", schedulerMessage("scheduler_heading", "Scheduler"), schedulerMessage("scheduler_help_short", "scheduler {list|show|run|enable|disable|history}"))
}

func maintenanceHelpRequested(args []string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] != "--maintenance" {
			continue
		}
		next := args[index+1]
		return next == "--help" || next == "-h" || next == "help"
	}
	return false
}

func commandHelp(args []string, commands ...string) (string, bool) {
	for index := 0; index+1 < len(args); index++ {
		next := args[index+1]
		if next != "--help" && next != "-h" && next != "help" {
			continue
		}
		for _, command := range commands {
			if args[index] == "--"+command {
				return command, true
			}
		}
	}
	return "", false
}

func printMaintenanceHelp() {
	fmt.Println(serverOutputText("maintenance_help", `Maintenance commands:
  --maintenance backup [file]
  --maintenance restore <file>
  --maintenance update [check|yes|branch {stable|beta|devel}]
  --maintenance mode
  --maintenance setup
  --maintenance pgp {generate|rotate|publish|export|import|delete}
  --maintenance token {list|revoke <prefix> [--yes]}
  --maintenance secret rotate <name>
  --maintenance data {export|delete} --owner <prefix>
  --maintenance compliance report

PGP key export, import, secret rotation, and token revocation require operator authorization and typed confirmation.
Token revocation and data deletion accept --yes to skip the typed confirmation in scripts.`))
}

func checkHealth(port string) error {
	url := fmt.Sprintf("http://127.0.0.1:%s/server/healthz", port)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}
	return nil
}

func printServiceHelp() {
	configDir, dataDir, logsDir := paths.GetDefaultDirs(projectName)
	options := servicepkg.Options{
		Name: projectName, Org: "apimgr", ConfigDir: configDir, DataDir: dataDir,
		CacheDir: paths.GetCacheDir(projectName), LogsDir: logsDir,
		Language:  serverOutputLanguage,
		BackupDir: paths.GetBackupDirFor(projectName, dataDir, ""), UserMode: os.Geteuid() != 0 && runtime.GOOS != "windows",
	}
	fmt.Println(serverOutputText("service_help", `Service management commands:

start                                  - Start the service
stop                                   - Stop the service
restart                               - Restart the service
reload                                - Reload configuration without restart

--install                             - Install, enable, and start service
--disable                             - Stop and disable service (keeps data)
--uninstall                           - Stop, disable, and remove everything (keeps binary)

Current status:`))
	if err := servicepkg.PrintStatus(options, os.Stdout); err != nil {
		fmt.Printf("  %s\n", serverOutputFormat("service_status_unavailable", "Status unavailable: {error}", map[string]string{"error": err.Error()}))
	}
}

func printUpdateHelp() {
	fmt.Println(serverOutputText("update_help", `Update commands:
  --update check                         Check for updates
  --update yes                           Download and install an update
  --update branch {stable|beta|devel}    Change the update branch`))
}

func handleServiceCommand(cmd string, options servicepkg.Options) error {
	switch cmd {
	case "start":
		return servicepkg.StartWithOptions(options)
	case "stop":
		return servicepkg.StopWithOptions(options)
	case "restart":
		return servicepkg.RestartWithOptions(options)
	case "reload":
		return servicepkg.ReloadWithOptions(options)
	case "status":
		return servicepkg.PrintStatus(options, os.Stdout)
	case "--install":
		return servicepkg.InstallWithOptions(options)
	case "--uninstall":
		return servicepkg.UninstallWithOptions(options, os.Stdin, os.Stdout)
	case "--disable":
		return servicepkg.DisableWithOptions(options)
	case "--help":
		printServiceHelp()
		return nil
	default:
		return fmt.Errorf("unknown service command %q", cmd)
	}
}

func handleMaintenanceCommand(cmd, configDir, dataDir, logsDir, backupDir, configPath string, cfg *config.Config, includeSSL, includeData bool) {
	args := flag.Args()

	switch cmd {
	case "help", "--help", "-h":
		printMaintenanceHelp()
	case "backup":
		if len(args) > 0 && args[0] == "test" {
			backupFile := ""
			if len(args) > 1 {
				backupFile = args[1]
			}
			if len(args) > 2 {
				log.Fatal(serverOutputText("backup_test_usage", "Usage: citylist --maintenance backup test [backup-file]"))
			}
			if err := runBackupTestCommand(backupFile, backupDir, configDir, cfg); err != nil {
				log.Fatal(serverOutputFormat("error_backup_test", "Backup verification failed: {error}", map[string]string{"error": err.Error()}))
			}
			return
		}
		backupFile := ""
		if len(args) > 0 {
			backupFile = args[0]
		}
		if len(args) > 1 {
			log.Fatal(serverOutputText("backup_usage", "Usage: citylist --maintenance backup [file] [--include-ssl] [--include-data]"))
		}
		if err := runBackupCommand(configPath, configDir, dataDir, backupDir, backupFile, cfg, includeSSL, includeData); err != nil {
			log.Fatal(serverOutputFormat("error_backup", "Backup failed: {error}", map[string]string{"error": err.Error()}))
		}
	case "restore":
		if len(args) == 0 {
			fmt.Println(serverOutputText("restore_usage", "Usage: citylist --maintenance restore <backup-file>"))
			os.Exit(1)
		}
		if len(args) != 1 {
			log.Fatal(serverOutputText("restore_usage", "Usage: citylist --maintenance restore <backup-file>"))
		}
		if err := runRestoreCommand(args[0], configPath, configDir, dataDir, backupDir, cfg); err != nil {
			log.Fatal(serverOutputFormat("error_restore", "Restore failed: {error}", map[string]string{"error": err.Error()}))
		}
	case "update":
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Fatal(serverOutputFormat("error_load_config", "Failed to load configuration: {error}", map[string]string{"error": err.Error()}))
		}
		command := "yes"
		updateArgs := []string{}
		if len(args) > 0 {
			command = args[0]
			updateArgs = args[1:]
		}
		handleUpdateCommand(command, cfg, configPath, updateArgs)
	case "mode":
		showCurrentMode(configPath)
	case "setup":
		runInitialSetup(configPath)
	case "pgp":
		if err := maintenancePGP(args, configDir, dataDir, logsDir, configPath, cfg); err != nil {
			fmt.Fprintln(os.Stderr, serverOutputFormat("operator_error", "Operation failed: {error}", map[string]string{"error": err.Error()}))
			os.Exit(1)
		}
	case "token":
		if err := maintenanceTokenCommand(args, dataDir, cfg); err != nil {
			fmt.Fprintln(os.Stderr, serverOutputFormat("operator_error", "Operation failed: {error}", map[string]string{"error": err.Error()}))
			os.Exit(1)
		}
	case "secret":
		if err := maintenanceSecretCommand(args, configDir, dataDir, configPath, cfg); err != nil {
			fmt.Fprintln(os.Stderr, serverOutputFormat("operator_error", "Operation failed: {error}", map[string]string{"error": err.Error()}))
			os.Exit(1)
		}
	case "data":
		if err := maintenanceDataCommand(args, dataDir, cfg); err != nil {
			fmt.Fprintln(os.Stderr, serverOutputFormat("operator_error", "Operation failed: {error}", map[string]string{"error": err.Error()}))
			os.Exit(1)
		}
	case "compliance":
		if err := maintenanceComplianceReport(args, dataDir, cfg, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, serverOutputFormat("operator_error", "Operation failed: {error}", map[string]string{"error": err.Error()}))
			os.Exit(1)
		}
	default:
		fmt.Println(serverOutputFormat("maintenance_unknown_command", "Unknown maintenance command: {command}", map[string]string{"command": cmd}))
		fmt.Println(serverOutputText("maintenance_available_commands", "Available commands: backup, restore, update, mode, setup, pgp, secret, token, data, compliance"))
		os.Exit(1)
	}
}

var maintenancePGPAuditMu sync.Mutex

var maintenanceInput = bufio.NewReader(os.Stdin)

type maintenancePGPAuditEntry struct {
	Timestamp   string `json:"timestamp"`
	Event       string `json:"event"`
	Operator    string `json:"operator"`
	OperatorIP  string `json:"operator_ip"`
	Reason      string `json:"reason,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Success     bool   `json:"success"`
}

type pgpPublishState struct {
	Fingerprint   string `json:"fingerprint"`
	PublishedAt   string `json:"published_at,omitempty"`
	Attempts      int    `json:"attempts,omitempty"`
	NextAttemptAt string `json:"next_attempt_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	FinalFailure  bool   `json:"final_failure,omitempty"`
}

type pgpPublishError struct {
	err       error
	retryable bool
}

func (e pgpPublishError) Error() string { return e.err.Error() }
func (e pgpPublishError) Unwrap() error { return e.err }

type pgpPublishBatchError struct{ err error }

func (e pgpPublishBatchError) Error() string { return e.err.Error() }
func (e pgpPublishBatchError) Unwrap() error { return e.err }

type pgpPublishedServer struct {
	Keyserver   string `json:"keyserver"`
	Fingerprint string `json:"fingerprint"`
	PublishedAt string `json:"published_at"`
}

func maintenancePGP(args []string, configDir, dataDir, logsDir, configPath string, cfg *config.Config) error {
	if cfg == nil || len(args) == 0 {
		return errors.New(serverOutputText("pgp_usage", "Usage: citylist --maintenance pgp {generate|rotate|publish|export|import|delete}"))
	}
	action := args[0]
	if action == "--help" || action == "help" {
		fmt.Println(serverOutputText("pgp_help", "PGP maintenance: generate, rotate, publish, export public [path], export private <path>, import <file>, delete"))
		return nil
	}
	operator, err := authorizePGPMaintenance(action, args, cfg)
	if err != nil {
		return err
	}
	var confirmation, reason string
	if action == "delete" || action == "import" || action == "export" && len(args) > 1 && args[1] == "private" {
		if action == "delete" {
			fmt.Fprintln(os.Stderr, serverOutputText("pgp_delete_warning", "Warning: deleting the project PGP key makes retained and in-flight PGP-encrypted security reports unreadable."))
		}
		phrase := map[string]string{"delete": "DELETE PGP KEYPAIR", "import": "IMPORT PGP PRIVATE KEY", "export": "EXPORT PGP PRIVATE KEY"}[action]
		if err := requirePGPConfirmation(serverOutputFormat("pgp_confirm_prompt", "Type {phrase} to continue", map[string]string{"phrase": phrase}), phrase); err != nil {
			return err
		}
		confirmation = phrase
		if action == "export" {
			reason, err = promptPGPReason()
			if err != nil {
				return err
			}
		}
	}
	store, err := openConfiguredDatabase(cfg, dataDir)
	if err != nil {
		return errors.New(serverOutputFormat("pgp_database_open", "Could not open the application database: {error}", map[string]string{"error": err.Error()}))
	}
	defer func() { _ = store.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.EnsureSchema(ctx); err != nil {
		return errors.New(serverOutputFormat("pgp_schema_init", "Could not initialize the application schema: {error}", map[string]string{"error": err.Error()}))
	}
	if err := server.InitializeInstallationSecret(ctx, store); err != nil {
		return errors.New(serverOutputFormat("pgp_installation_secret", "Could not initialize the installation secret: {error}", map[string]string{"error": err.Error()}))
	}
	manager, err := server.NewPGPMaintenance(configDir, cfg, store)
	if err != nil {
		return err
	}
	if err := manager.CleanupExpiredOldProjectPGPKey(ctx); err != nil {
		return err
	}
	switch action {
	case "generate":
		key, err := manager.GenerateProjectPGPKeypair(ctx)
		if err != nil {
			return err
		}
		if err := savePGPMetadata(ctx, store, key, time.Time{}, time.Time{}, false); err != nil {
			return err
		}
		fmt.Println(serverOutputFormat("pgp_generated", "Generated project OpenPGP key {fingerprint} (expires {expires})", map[string]string{"fingerprint": key.Fingerprint, "expires": key.ExpiresAt.Format(time.RFC3339)}))
		if cfg.Web.Security.PublishPGPKey && len(cfg.Web.Security.Keyservers) > 0 {
			if err := publishPGPKey(ctx, configDir, logsDir, cfg, store, key); err != nil {
				fmt.Fprintln(os.Stderr, serverOutputFormat("pgp_generate_publish_warning", "Warning: key generated but publication failed: {error}", map[string]string{"error": err.Error()}))
			}
		}
	case "rotate":
		key, _, err := manager.RotateProjectPGPKeypair(ctx)
		if err != nil {
			return err
		}
		fmt.Println(serverOutputFormat("pgp_rotated", "Rotated project OpenPGP key to {fingerprint} (expires {expires})", map[string]string{"fingerprint": key.Fingerprint, "expires": key.ExpiresAt.Format(time.RFC3339)}))
		if cfg.Web.Security.PublishPGPKey && len(cfg.Web.Security.Keyservers) > 0 {
			if err := publishPGPKey(ctx, configDir, logsDir, cfg, store, key); err != nil {
				fmt.Fprintln(os.Stderr, serverOutputFormat("pgp_rotate_publish_warning", "Warning: key rotated but publication failed: {error}", map[string]string{"error": err.Error()}))
			}
		}
	case "publish":
		key, err := manager.LoadProjectPGPKeypair(ctx)
		if err != nil {
			return err
		}
		if err := publishPGPKey(ctx, configDir, logsDir, cfg, store, key); err != nil {
			return err
		}
		fmt.Println(serverOutputFormat("pgp_published", "Published project OpenPGP key {fingerprint}", map[string]string{"fingerprint": key.Fingerprint}))
	case "export":
		if len(args) < 2 {
			return errors.New(serverOutputText("pgp_export_usage", "Usage: citylist --maintenance pgp export {public [path]|private <path>}"))
		}
		switch args[1] {
		case "public":
			key, err := manager.LoadProjectPGPKeypair(ctx)
			if err != nil {
				return err
			}
			if len(args) < 3 || args[2] == "-" {
				_, err = os.Stdout.Write(key.PublicArmor)
				return err
			}
			if err := os.WriteFile(args[2], key.PublicArmor, 0644); err != nil {
				return errors.New(serverOutputFormat("pgp_export_public_error", "Could not export the public PGP key: {error}", map[string]string{"error": err.Error()}))
			}
			fmt.Println(serverOutputFormat("pgp_exported_public", "Exported public key to {path}", map[string]string{"path": args[2]}))
		case "private":
			if len(args) != 3 || args[2] == "" {
				return errors.New(serverOutputText("pgp_export_private_usage", "Usage: citylist --maintenance pgp export private <path>"))
			}
			if err := enforcePGPExportRateLimit(logsDir, operator); err != nil {
				return err
			}
			private, err := manager.ExportProjectPGPPrivate(ctx)
			if err != nil {
				return err
			}
			if err := writePrivatePGPExport(args[2], private); err != nil {
				return err
			}
			if err := writePGPAudit(logsDir, maintenancePGPAuditEntry{Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Event: "security.private_key_exported", Operator: operator, OperatorIP: maintenanceOperatorIP(), Reason: reason, Success: true}); err != nil {
				_ = os.Remove(args[2])
				return errors.New(serverOutputFormat("pgp_export_audit_error", "Could not record the private-key export audit event: {error}", map[string]string{"error": err.Error()}))
			}
			fmt.Println(serverOutputFormat("pgp_exported_private", "Exported private key to {path}", map[string]string{"path": args[2]}))
		default:
			return errors.New(serverOutputText("pgp_export_type_error", "PGP export type must be public or private"))
		}
	case "import":
		if len(args) != 2 || args[1] == "" {
			return errors.New(serverOutputText("pgp_import_usage", "Usage: citylist --maintenance pgp import <file>"))
		}
		input, err := readPGPImport(args[1])
		if err != nil {
			return err
		}
		key, err := manager.ImportProjectPGPPrivate(ctx, input, false)
		if errors.Is(err, server.ErrProjectPGPIdentityMismatch) {
			fmt.Fprintln(os.Stderr, serverOutputText("pgp_import_identity_warning", "Warning: the imported key identity differs from the configured project security contact."))
			if err := requirePGPConfirmation(serverOutputFormat("pgp_override_identity_prompt", "Type {phrase} to override", map[string]string{"phrase": "IMPORT KEY WITH DIFFERENT IDENTITY"}), "IMPORT KEY WITH DIFFERENT IDENTITY"); err != nil {
				return err
			}
			key, err = manager.ImportProjectPGPPrivate(ctx, input, true)
		}
		if err != nil {
			return err
		}
		if err := savePGPMetadata(ctx, store, key, time.Time{}, time.Time{}, false); err != nil {
			return err
		}
		if err := writePGPAudit(logsDir, maintenancePGPAuditEntry{Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Event: "security.private_key_imported", Operator: operator, OperatorIP: maintenanceOperatorIP(), Reason: confirmation, Fingerprint: key.Fingerprint, Success: true}); err != nil {
			return errors.New(serverOutputFormat("pgp_import_audit_error", "Could not record the private-key import audit event: {error}", map[string]string{"error": err.Error()}))
		}
		fmt.Println(serverOutputFormat("pgp_imported", "Imported project OpenPGP key {fingerprint}", map[string]string{"fingerprint": key.Fingerprint}))
	case "delete":
		key, err := manager.LoadProjectPGPKeypair(ctx)
		if err != nil {
			return err
		}
		previousPublish := cfg.Web.Security.PublishPGPKey
		cfg.Web.Security.PublishPGPKey = false
		if err := config.Save(configPath, cfg); err != nil {
			cfg.Web.Security.PublishPGPKey = previousPublish
			return errors.New(serverOutputFormat("pgp_disable_publishing_error", "Could not disable PGP publishing before deleting the key: {error}", map[string]string{"error": err.Error()}))
		}
		if err := manager.DeleteProjectPGPKeypair(ctx); err != nil {
			cfg.Web.Security.PublishPGPKey = previousPublish
			if restoreErr := config.Save(configPath, cfg); restoreErr != nil {
				return errors.Join(err, errors.New(serverOutputFormat("pgp_restore_publishing_error", "Could not restore PGP publishing configuration after key deletion: {error}", map[string]string{"error": restoreErr.Error()})))
			}
			return err
		}
		if err := writePGPAudit(logsDir, maintenancePGPAuditEntry{Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Event: "security.pgp_deleted", Operator: operator, OperatorIP: maintenanceOperatorIP(), Reason: confirmation, Fingerprint: key.Fingerprint, Success: true}); err != nil {
			return errors.New(serverOutputFormat("pgp_delete_audit_error", "Could not record the PGP deletion audit event: {error}", map[string]string{"error": err.Error()}))
		}
		fmt.Println(serverOutputText("pgp_deleted", "Deleted the project PGP keypair. PGP-encrypted reports can no longer be decrypted."))
	default:
		return errors.New(serverOutputText("pgp_unknown_action", "Unknown PGP action; use generate, rotate, publish, export, import, or delete"))
	}
	return nil
}

func maintenanceOperatorIP() string {
	if fields := strings.Fields(os.Getenv("SSH_CONNECTION")); len(fields) == 4 && net.ParseIP(fields[0]) != nil {
		return fields[0]
	}
	return "127.0.0.1"
}

func authorizePGPMaintenance(action string, args []string, cfg *config.Config) (string, error) {
	root := os.Geteuid() == 0
	privateExport := action == "export" && len(args) > 1 && args[1] == "private"
	if root && !privateExport {
		return "uid:0", nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New(serverOutputText("pgp_auth_terminal_required", "PGP maintenance authorization requires a terminal to enter the operator token"))
	}
	provided, err := readHiddenPGPInput(serverOutputText("pgp_operator_token_prompt", "Operator token: "))
	if err != nil {
		return "", err
	}
	if cfg.Server.Token == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(cfg.Server.Token)) != 1 {
		return "", errors.New(serverOutputText("pgp_operator_token_rejected", "Operator token was not accepted"))
	}
	hash := sha256.Sum256([]byte(provided))
	return "token:" + fmt.Sprintf("%x", hash[:]), nil
}

func readHiddenPGPInput(prompt string) (string, error) {
	_, _ = fmt.Fprint(os.Stderr, prompt)
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	_, _ = fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", errors.New(serverOutputFormat("pgp_secret_input_error", "Could not read secret input: {error}", map[string]string{"error": err.Error()}))
	}
	return string(bytes.TrimSpace(value)), nil
}

func requirePGPConfirmation(prompt, expected string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New(serverOutputText("pgp_confirmation_terminal_required", "Sensitive PGP action requires an interactive terminal confirmation"))
	}
	_, _ = fmt.Fprintln(os.Stderr, prompt)
	line, err := maintenanceInput.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.TrimSpace(line) != expected {
		return errors.New(serverOutputText("pgp_confirmation_mismatch", "PGP confirmation did not match"))
	}
	return nil
}

func promptPGPReason() (string, error) {
	_, _ = fmt.Fprint(os.Stderr, serverOutputText("pgp_private_key_export_reason_prompt", "Reason for exporting the private key: "))
	line, err := maintenanceInput.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	reason := strings.TrimSpace(line)
	if reason == "" || len(reason) > 2000 {
		return "", errors.New(serverOutputText("pgp_private_key_export_reason_required", "A reason of 1 to 2000 characters is required"))
	}
	return reason, nil
}

func readPGPImport(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 16<<10 {
		return nil, errors.New(serverOutputText("pgp_import_file_invalid", "PGP import path must be a regular file no larger than 16 KiB"))
	}
	return os.ReadFile(path)
}

func enforcePGPExportRateLimit(logsDir, operator string) error {
	maintenancePGPAuditMu.Lock()
	defer maintenancePGPAuditMu.Unlock()
	file, err := os.Open(filepath.Join(logsDir, "audit.log"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	cutoff := time.Now().UTC().Add(-time.Hour)
	for scanner.Scan() {
		var event maintenancePGPAuditEntry
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Event != "security.private_key_exported" || event.Operator != operator {
			continue
		}
		when, err := time.Parse(time.RFC3339Nano, event.Timestamp)
		if err == nil && when.After(cutoff) {
			return errors.New(serverOutputText("pgp_export_rate_limited", "Private-key export is limited to one per hour per operator"))
		}
	}
	return scanner.Err()
}

func writePrivatePGPExport(path string, data []byte) error {
	if len(data) == 0 {
		return errors.New(serverOutputText("pgp_private_key_empty", "Decrypted private key is empty"))
	}
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".citylist-pgp-export-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(temporary, path); err != nil {
		return errors.New(serverOutputFormat("pgp_private_key_write_error", "Could not write the private key without overwriting an existing path: {error}", map[string]string{"error": err.Error()}))
	}
	return nil
}

func writePGPAudit(logsDir string, entry maintenancePGPAuditEntry) error {
	maintenancePGPAuditMu.Lock()
	defer maintenancePGPAuditMu.Unlock()
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(logsDir, "audit.log")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if err := json.NewEncoder(file).Encode(entry); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func savePGPMetadata(ctx context.Context, store *database.Store, key *server.ProjectPGPKey, rotatedAt, oldExpires time.Time, revoked bool) error {
	if store == nil || key == nil {
		return errors.New(serverOutputText("pgp_metadata_missing", "PGP metadata requires a database and key"))
	}
	var rotatedValue, oldExpiryValue interface{}
	if !rotatedAt.IsZero() {
		rotatedValue = rotatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !oldExpires.IsZero() {
		oldExpiryValue = oldExpires.UTC().Format(time.RFC3339Nano)
	}
	return store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO project_pgp_key (id, fingerprint, created_at, expires_at, last_rotated_at, keyservers_published, revoked, old_key_expires_at) VALUES (1, ?, ?, ?, ?, '[]', ?, ?) ON CONFLICT(id) DO UPDATE SET fingerprint = excluded.fingerprint, created_at = excluded.created_at, expires_at = excluded.expires_at, last_rotated_at = excluded.last_rotated_at, keyservers_published = '[]', revoked = excluded.revoked, old_key_expires_at = excluded.old_key_expires_at`, key.Fingerprint, key.CreatedAt.UTC().Format(time.RFC3339Nano), key.ExpiresAt.UTC().Format(time.RFC3339Nano), rotatedValue, revoked, oldExpiryValue)
		return err
	})
}

func publishPGPKey(ctx context.Context, configDir, logsDir string, cfg *config.Config, store *database.Store, key *server.ProjectPGPKey) error {
	return publishPGPKeyWithSubmitter(ctx, configDir, logsDir, cfg, store, key, submitPGPKey)
}

func publishPGPKeyWithSubmitter(ctx context.Context, configDir, logsDir string, cfg *config.Config, store *database.Store, key *server.ProjectPGPKey, submit func(context.Context, string, []byte) error) error {
	if cfg == nil || key == nil || store == nil {
		return errors.New(serverOutputText("pgp_publish_dependencies_missing", "PGP publication requires configuration, database and key"))
	}
	if len(cfg.Web.Security.Keyservers) == 0 {
		return errors.New(serverOutputText("pgp_keyservers_missing", "No PGP keyservers are configured"))
	}
	securityDir := filepath.Join(configDir, "security")
	if err := paths.EnsurePrivateDir(securityDir); err != nil {
		return err
	}
	statePath := filepath.Join(securityDir, "keyservers.state")
	state := map[string]pgpPublishState{}
	if info, err := os.Lstat(statePath); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 1<<20 {
			return errors.New(serverOutputText("pgp_keyserver_state_invalid", "PGP keyserver state path is not a bounded regular file"))
		}
		data, err := os.ReadFile(statePath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &state); err != nil {
			return errors.New(serverOutputFormat("pgp_keyserver_state_parse_error", "Could not parse PGP keyserver state: {error}", map[string]string{"error": err.Error()}))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	published := make([]pgpPublishedServer, 0, len(state)+len(cfg.Web.Security.Keyservers))
	for endpoint, item := range state {
		if item.Fingerprint == key.Fingerprint && item.PublishedAt != "" {
			published = append(published, pgpPublishedServer{Keyserver: endpoint, Fingerprint: item.Fingerprint, PublishedAt: item.PublishedAt})
		}
	}
	var failures []error
	for _, configured := range cfg.Web.Security.Keyservers {
		configured = strings.TrimSpace(configured)
		if configured == "" {
			continue
		}
		previous := state[configured]
		if previous.Fingerprint == key.Fingerprint {
			if previous.PublishedAt != "" || previous.Attempts >= 8 || previous.FinalFailure {
				continue
			}
			if next, parseErr := time.Parse(time.RFC3339Nano, previous.NextAttemptAt); parseErr == nil && next.After(time.Now()) {
				continue
			}
		}
		endpoint, err := pgpKeyserverEndpoint(configured)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if err := submit(ctx, endpoint, key.PublicArmor); err != nil {
			failures = append(failures, errors.New(serverOutputFormat("pgp_keyserver_publish_error", "Could not publish to {server}: {error}", map[string]string{"server": configured, "error": err.Error()})))
			var publishErr pgpPublishError
			attempts := previous.Attempts + 1
			retry := errors.As(err, &publishErr) && publishErr.retryable && attempts < 8
			nextAttempt := ""
			if retry {
				delay := time.Minute * time.Duration(1<<min(attempts-1, 8))
				if delay > 6*time.Hour {
					delay = 6 * time.Hour
				}
				nextAttempt = time.Now().Add(delay).UTC().Format(time.RFC3339Nano)
			}
			state[configured] = pgpPublishState{Fingerprint: key.Fingerprint, Attempts: attempts, NextAttemptAt: nextAttempt, LastError: err.Error(), FinalFailure: !retry}
			_ = writePGPAudit(logsDir, maintenancePGPAuditEntry{Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Event: "security.pgp_keyserver_publish_failed", Operator: "maintenance", OperatorIP: maintenanceOperatorIP(), Fingerprint: key.Fingerprint, Success: false})
			continue
		}
		state[configured] = pgpPublishState{Fingerprint: key.Fingerprint, PublishedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		published = append(published, pgpPublishedServer{Keyserver: configured, Fingerprint: key.Fingerprint, PublishedAt: state[configured].PublishedAt})
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomicPrivateFile(statePath, append(data, '\n')); err != nil {
		return err
	}
	publishedJSON, err := json.Marshal(published)
	if err != nil {
		return err
	}
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE project_pgp_key SET keyservers_published = ? WHERE id = 1`, string(publishedJSON))
		return err
	}); err != nil {
		return err
	}
	if len(failures) > 0 {
		return pgpPublishBatchError{err: errors.Join(failures...)}
	}
	return nil
}

func retryScheduledPGPPublishing(ctx context.Context, configDir, logsDir string, cfg *config.Config, store *database.Store) error {
	if cfg == nil || store == nil || len(cfg.Web.Security.Keyservers) == 0 {
		return nil
	}
	manager, err := server.NewPGPMaintenance(configDir, cfg, store)
	if err != nil {
		return err
	}
	key, err := manager.LoadProjectPGPKeypair(ctx)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	err = publishPGPKey(ctx, configDir, logsDir, cfg, store, key)
	var publishFailures pgpPublishBatchError
	if errors.As(err, &publishFailures) {
		return nil
	}
	return err
}

func pgpKeyserverEndpoint(configured string) (string, error) {
	value := configured
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New(serverOutputFormat("pgp_keyserver_url_invalid", "Configured PGP keyserver {server} must be an HTTPS host or URL", map[string]string{"server": configured}))
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Path == "" || parsed.Path == "/" {
		switch host {
		case "keys.openpgp.org":
			parsed.Path = "/vks/v1/upload"
		case "keyserver.ubuntu.com":
			parsed.Path = "/pks/add"
		default:
			return "", errors.New(serverOutputFormat("pgp_keyserver_endpoint_missing", "PGP keyserver {server} needs an explicit HTTPS submission endpoint", map[string]string{"server": configured}))
		}
	}
	return parsed.String(), nil
}

func submitPGPKey(ctx context.Context, endpoint string, publicKey []byte) error {
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) == 0 || len(via) >= 2 || !strings.EqualFold(request.URL.Scheme, "https") || !strings.EqualFold(request.URL.Host, via[0].URL.Host) {
			return errors.New(serverOutputText("pgp_keyserver_redirect_unsafe", "Unsafe keyserver redirect"))
		}
		return nil
	}}
	defer client.CloseIdleConnections()
	return submitPGPKeyWithClient(ctx, endpoint, publicKey, client)
}

func submitPGPKeyWithClient(ctx context.Context, endpoint string, publicKey []byte, client *http.Client) error {
	isVKS := false
	if parsed, err := url.Parse(endpoint); err == nil {
		isVKS = strings.EqualFold(parsed.Hostname(), "keys.openpgp.org") || parsed.Path == "/vks/v1/upload"
	}
	var body string
	contentType := "application/x-www-form-urlencoded"
	if isVKS {
		encoded, err := json.Marshal(map[string]string{"keytext": string(publicKey)})
		if err != nil {
			return err
		}
		body = string(encoded)
		contentType = "application/json"
	} else {
		body = url.Values{"keytext": {string(publicKey)}}.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	response, err := client.Do(request)
	if err != nil {
		return pgpPublishError{err: err, retryable: true}
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return pgpPublishError{err: readErr, retryable: true}
	}
	if response.StatusCode != http.StatusOK {
		return pgpPublishError{err: errors.New(serverOutputFormat("pgp_keyserver_response", "Keyserver returned HTTP {status}", map[string]string{"status": strconv.Itoa(response.StatusCode)})), retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500}
	}
	if isVKS {
		var result struct {
			KeyFingerprint string `json:"key_fpr"`
			Status         string `json:"status"`
		}
		if err := json.Unmarshal(responseBody, &result); err != nil || strings.TrimSpace(result.KeyFingerprint) == "" || strings.TrimSpace(result.Status) == "" {
			return pgpPublishError{err: errors.New(serverOutputText("pgp_keyserver_response_invalid", "Keyserver returned an invalid VKS response"))}
		}
	}
	return nil
}

func writeAtomicPrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".pgp-state-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// setApplicationMode sets the application mode
func setApplicationMode(mode, configPath string) {
	if mode != "production" && mode != "development" && mode != "debug" {
		fmt.Println(serverOutputFormat("mode_invalid", "Invalid mode: {mode}", map[string]string{"mode": mode}))
		fmt.Println(serverOutputText("mode_valid", "Valid modes: production, development, debug"))
		os.Exit(1)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(serverOutputFormat("error_load_config", "Failed to load configuration: {error}", map[string]string{"error": err.Error()}))
	}

	cfg.Server.Mode = mode
	if err := config.Save(configPath, cfg); err != nil {
		log.Fatal(serverOutputFormat("error_save_config", "Failed to save configuration: {error}", map[string]string{"error": err.Error()}))
	}

	fmt.Println(serverOutputFormat("mode_set", "Application mode set to: {mode}", map[string]string{"mode": mode}))
}

// showCurrentMode displays the current application mode
func showCurrentMode(configPath string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(serverOutputFormat("error_load_config", "Failed to load configuration: {error}", map[string]string{"error": err.Error()}))
	}

	mode := cfg.Server.Mode
	if mode == "" {
		mode = "production"
	}
	fmt.Println(serverOutputFormat("mode_current", "Current mode: {mode}", map[string]string{"mode": mode}))
}

// runInitialSetup runs the initial setup wizard
func runInitialSetup(configPath string) {
	fmt.Println(serverOutputText("setup_title", "Citylist API Initial Setup"))

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(serverOutputFormat("error_load_config", "Failed to load configuration: {error}", map[string]string{"error": err.Error()}))
	}

	fmt.Println(serverOutputFormat("setup_configuration", "Configuration file: {path}", map[string]string{"path": configPath}))
	fmt.Println(serverOutputFormat("setup_port", "Current port: {port}", map[string]string{"port": cfg.Server.Port}))
	fmt.Println(serverOutputFormat("setup_mode", "Current mode: {mode}", map[string]string{"mode": cfg.Server.Mode}))
	fmt.Println(serverOutputText("setup_complete", "Setup complete. Edit the configuration file to customize settings."))
}

// handleUpdateCommand handles update-related commands
func handleUpdateCommand(cmd string, cfg *config.Config, configPath string, args []string, serviceOptions ...servicepkg.Options) {
	switch cmd {
	case "check":
		fmt.Println(serverOutputText("update_checking", "Checking for updates..."))
		fmt.Println(serverOutputFormat("update_current_version", "Current version: {version}", map[string]string{"version": Version}))
		fmt.Println(serverOutputFormat("update_branch", "Update branch: {branch}", map[string]string{"branch": cfg.Server.Update.Branch}))
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		release, err := updater.Check(ctx, nil, cfg.Server.Update.Branch, mustBuildEpoch())
		cancel()
		if err != nil {
			log.Fatal(serverOutputFormat("error_update_check", "Update check failed: {error}", map[string]string{"error": err.Error()}))
		}
		if release == nil || !updater.IsNewer(release.TagName, Version, release.PublishedAt.Unix(), mustBuildEpoch()) {
			fmt.Println(serverOutputText("update_none", "No updates available"))
			return
		}
		fmt.Println(serverOutputFormat("update_available", "Update available: {version} (published {date})", map[string]string{"version": release.TagName, "date": release.PublishedAt.UTC().Format(time.RFC3339)}))
	case "yes":
		fmt.Println(serverOutputText("update_checking", "Checking for updates..."))
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		release, err := updater.Check(ctx, nil, cfg.Server.Update.Branch, mustBuildEpoch())
		if err != nil {
			cancel()
			log.Fatal(serverOutputFormat("error_update_check", "Update check failed: {error}", map[string]string{"error": err.Error()}))
		}
		if release == nil || !updater.IsNewer(release.TagName, Version, release.PublishedAt.Unix(), mustBuildEpoch()) {
			cancel()
			fmt.Println(serverOutputText("update_none", "No updates available"))
			return
		}
		if err := manualUpdatePrivilegeError(filepath.Base(os.Args[0]), runtime.GOOS, startedElevated, serviceManagedStart()); err != nil {
			cancel()
			log.Fatal(serverOutputFormat("error_update_install", "Update installation failed: {error}", map[string]string{"error": err.Error()}))
		}
		fmt.Println(serverOutputFormat("update_installing", "Installing update {version}...", map[string]string{"version": release.TagName}))
		if err := updater.Install(ctx, nil, release, os.TempDir()); err != nil {
			cancel()
			log.Fatal(serverOutputFormat("error_update_install", "Update installation failed: {error}", map[string]string{"error": err.Error()}))
		}
		cancel()
		var restartErr error
		restartedByService := false
		if len(serviceOptions) > 0 {
			state, err := servicepkg.StateWithOptions(serviceOptions[0])
			if err != nil {
				restartErr = err
			} else if state.Installed {
				restartErr = servicepkg.RestartWithOptions(serviceOptions[0])
				restartedByService = true
			}
		}
		if restartErr == nil && !restartedByService {
			restartErr = updater.Restart()
		}
		if restartErr != nil {
			log.Fatal(serverOutputFormat("error_update_restart", "Updated binary installed, but restart failed: {error}", map[string]string{"error": restartErr.Error()}))
		}
	case "branch":
		if len(args) == 0 {
			fmt.Println(serverOutputFormat("update_branch_current", "Current update branch: {branch}", map[string]string{"branch": cfg.Server.Update.Branch}))
			fmt.Println(serverOutputText("update_branch_usage", "Usage: citylist --update branch {stable|beta|devel}"))
			return
		}
		branch := args[0]
		branch = strings.ToLower(strings.TrimSpace(branch))
		if branch == "daily" || branch == "rolling" || branch == "dev" || branch == "development" {
			branch = "devel"
		}
		if branch != "stable" && branch != "beta" && branch != "devel" {
			fmt.Println(serverOutputFormat("update_branch_invalid", "Invalid branch: {branch}", map[string]string{"branch": branch}))
			fmt.Println(serverOutputText("update_branch_valid", "Valid branches: stable, beta, devel"))
			os.Exit(1)
		}
		cfg.Server.Update.Branch = branch
		if err := config.Save(configPath, cfg); err != nil {
			log.Fatal(serverOutputFormat("error_update_save_branch", "Failed to save update branch: {error}", map[string]string{"error": err.Error()}))
		}
		fmt.Println(serverOutputFormat("update_branch_set", "Update branch set to: {branch}", map[string]string{"branch": branch}))
	default:
		fmt.Println(serverOutputFormat("update_unknown", "Unknown update command: {command}", map[string]string{"command": cmd}))
		fmt.Println(serverOutputText("update_commands", "Available commands: check, yes, branch"))
		os.Exit(1)
	}
}

func manualUpdatePrivilegeError(binaryName, goos string, elevated, serviceManaged bool) error {
	if elevated || !serviceManaged {
		return nil
	}
	command := binaryName + " --update yes"
	if goos != "windows" {
		command = "sudo " + command
	} else {
		command = "Run " + command + " from an elevated prompt"
	}
	return errors.New("an elevated process is required; " + command)
}

func mustBuildEpoch() int64 {
	epoch, err := strconv.ParseInt(BuildEpoch, 10, 64)
	if err != nil {
		return 0
	}
	return epoch
}

func runCommand(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Print(serverOutputFormat("error_command", "Command {command} failed: {error}", map[string]string{"command": name, "error": err.Error()}))
	}
}

func startDaemon(logsDir string) error {
	logFile, err := os.OpenFile(filepath.Join(logsDir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if os.Geteuid() == 0 && runtime.GOOS != "windows" {
		account, lookupErr := user.Lookup(projectName)
		if lookupErr != nil {
			_ = logFile.Close()
			return lookupErr
		}
		uid, uidErr := strconv.Atoi(account.Uid)
		gid, gidErr := strconv.Atoi(account.Gid)
		if uidErr != nil || gidErr != nil {
			_ = logFile.Close()
			return fmt.Errorf("invalid service account identity")
		}
		if err := logFile.Chown(uid, gid); err != nil {
			_ = logFile.Close()
			return err
		}
	}
	args := make([]string, 0, len(os.Args)-1)
	for _, arg := range os.Args[1:] {
		if arg != "--daemon" {
			args = append(args, arg)
		}
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "_DAEMON_CHILD=1")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	fmt.Println(serverOutputFormat("daemon_started", "Started citylist daemon with PID {pid}", map[string]string{"pid": strconv.Itoa(cmd.Process.Pid)}))
	return nil
}

func isApplicationContainer() bool {
	for _, file := range []string{"/.dockerenv", "/run/.containerenv", "/dev/lxc"} {
		if _, err := os.Stat(file); err == nil {
			return true
		}
	}
	for _, name := range []string{"container", "KUBERNETES_SERVICE_HOST"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

func normalizeOptionalBoolFlag(args []string, name string) []string {
	normalized := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument != name {
			normalized = append(normalized, argument)
			continue
		}
		if index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
			index++
			normalized = append(normalized, name+"="+args[index])
			continue
		}
		normalized = append(normalized, name+"=true")
	}
	return normalized
}

func serviceManagedStart() bool {
	if runtime.GOOS == "windows" && isWindowsService() {
		return true
	}
	if os.Getenv("INVOCATION_ID") != "" || os.Getenv("RC_SVCNAME") != "" {
		return true
	}
	if os.Getppid() == 1 {
		return true
	}
	return false
}
