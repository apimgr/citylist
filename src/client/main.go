package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/apimgr/citylist/src/client/api"
	"github.com/apimgr/citylist/src/client/gui"
	"github.com/apimgr/citylist/src/client/tui"
	"github.com/apimgr/citylist/src/common/display"
	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/common/theme"
	"github.com/apimgr/citylist/src/config"
	"gopkg.in/yaml.v3"
)

var (
	ProjectName  = "citylist"
	InternalName = "citylist"
	InternalOrg  = "apimgr"
	Version      = "dev"
	Commit       = "unknown"
	BuildDate    = "unknown"
)

var errTableRequiresJSON = errors.New("table output requires a JSON response")

type cliBooleanValue struct {
	value *bool
}

func (v cliBooleanValue) Set(value string) error {
	parsed, err := config.ParseBool(value, *v.value)
	if err != nil {
		return err
	}
	*v.value = parsed
	return nil
}

func (v cliBooleanValue) String() string {
	return strconv.FormatBool(*v.value)
}

func (cliBooleanValue) IsBoolFlag() bool {
	return true
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, output, errorOutput io.Writer) int {
	return runWithEnvironment(args, output, errorOutput, os.Stdin, os.LookupEnv)
}

func runWithEnvironment(args []string, output, errorOutput io.Writer, input io.Reader, lookupEnv func(string) (string, bool)) int {
	binaryName := filepath.Base(os.Args[0])
	helpLang := resolveLanguage(flagValue(args, "lang"), "auto", lookupEnv)
	if len(args) == 1 && args[0] == "help" {
		printHelp(output, binaryName, helpLang)
		return 0
	}
	if _, ok := requestedShellHelp(args); ok {
		printShellHelp(output, binaryName, helpLang)
		return 0
	}
	flags := flag.NewFlagSet(binaryName, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	serverFlag := flags.String("server", "", clientText(helpLang, "flag_server", "Server URL (default: config or CITYLIST_SERVER_PRIMARY)"))
	tokenFlag := flags.String("token", "", clientText(helpLang, "flag_token", "API token"))
	tokenFile := flags.String("token-file", "", clientText(helpLang, "flag_token_file", "Read API token from file"))
	configName := flags.String("config", "cli.yml", clientText(helpLang, "flag_config", "CLI configuration profile"))
	outputFormat := flags.String("output", "", clientText(helpLang, "flag_output", "Output format: json, table, yaml, csv, or plain"))
	flags.StringVar(outputFormat, "format", "", clientText(helpLang, "flag_output", "Output format: json, table, yaml, csv, or plain"))
	langFlag := flags.String("lang", "", clientText(helpLang, "flag_lang", "Language for output"))
	color := flags.String("color", "auto", clientText(helpLang, "flag_color", "Color output: auto, yes, or no"))
	debugValue, quietFlagValue, verboseFlagValue := false, false, false
	flags.Var(cliBooleanValue{value: &debugValue}, "debug", clientText(helpLang, "flag_debug", "Enable debug output"))
	flags.Var(cliBooleanValue{value: &quietFlagValue}, "quiet", clientText(helpLang, "flag_quiet", "Suppress non-essential output"))
	flags.Var(cliBooleanValue{value: &verboseFlagValue}, "verbose", clientText(helpLang, "flag_verbose", "Enable extra output"))
	pagerFlag := flags.String("pager", "", clientText(helpLang, "flag_pager", "Pager behavior: auto, always, or never"))
	flags.String("update", "", clientText(helpLang, "flag_update", "Check for or install a CLI update"))
	shellAction := flags.String("shell", "", clientText(helpLang, "flag_shell", "Print shell completions or initialization command"))
	showVersionValue := false
	flags.Var(cliBooleanValue{value: &showVersionValue}, "version", clientText(helpLang, "flag_version", "Print version information"))
	flags.Var(cliBooleanValue{value: &showVersionValue}, "v", clientText(helpLang, "flag_version", "Print version information"))
	flags.Usage = func() { printHelp(errorOutput, binaryName, helpLang) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintln(errorOutput, clientTextFormat(helpLang, "error_flags", "{error}", map[string]string{"error": err.Error()}))
		return 2
	}
	if showVersionValue {
		_, _ = fmt.Fprintln(output, clientTextFormat(helpLang, "version", "{binary} {version} ({commit})\nBuilt: {build_date}\nGo: {go_version}\nOS/Arch: {os}/{arch}", map[string]string{
			"binary": binaryName, "version": Version, "commit": Commit, "build_date": BuildDate,
			"go_version": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		}))
		return 0
	}
	if *shellAction == "help" {
		printShellHelp(output, binaryName, helpLang)
		return 0
	}
	if *shellAction != "" {
		return runShellAction(output, errorOutput, binaryName, *shellAction, flags.Args(), helpLang)
	}
	parsedArgs := flags.Args()
	if len(parsedArgs) == 2 && isCLICommand(parsedArgs[0]) && parsedArgs[1] == "help" {
		printHelp(output, binaryName, helpLang)
		return 0
	}
	if !validColor(*color) {
		_, _ = fmt.Fprintln(errorOutput, clientText(helpLang, "error_color", "color must be auto, yes, or no"))
		return 2
	}

	configPath, err := cliConfigPath(*configName)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, clientTextFormat(helpLang, "error_config_path", "{error}", map[string]string{"error": err.Error()}))
		return 2
	}
	cfg, err := loadCLIConfig(configPath)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, clientTextFormat(helpLang, "error_config_load", "{error}", map[string]string{"error": err.Error()}))
		return 1
	}
	requestTimeout, _ := time.ParseDuration(cfg.Server.Timeout)
	retryDelay, _ := time.ParseDuration(cfg.Server.RetryDelay)
	cacheTTL, _ := time.ParseDuration(cfg.Cache.TTL)
	cacheMaxBytes, sizeErr := parseSize(cfg.Cache.MaxSize)
	if sizeErr != nil {
		_, _ = fmt.Fprintln(errorOutput, "invalid cache.max_size")
		return 2
	}
	if err := api.SetDefaultResponseCache(cliCacheDirectory(), cfg.Cache.Enabled, cacheTTL, cacheMaxBytes); err != nil {
		_, _ = fmt.Fprintln(errorOutput, clientTextFormat(helpLang, "error_cache_config", "Could not configure response cache: {error}", map[string]string{"error": err.Error()}))
		return 2
	}
	if value, ok := lookupEnv("CITYLIST_SERVER_TIMEOUT"); ok && strings.TrimSpace(value) != "" {
		parsed, parseErr := parseDurationSetting(value)
		if parseErr != nil || parsed <= 0 {
			_, _ = fmt.Fprintln(errorOutput, "CITYLIST_SERVER_TIMEOUT must be a positive duration")
			return 2
		}
		requestTimeout = parsed
	}
	if value, ok := lookupEnv("CITYLIST_SERVER_RETRY"); ok && strings.TrimSpace(value) != "" {
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(value))
		if parseErr != nil || parsed < 0 || parsed > 10 {
			_, _ = fmt.Fprintln(errorOutput, "CITYLIST_SERVER_RETRY must be between 0 and 10")
			return 2
		}
		cfg.Server.Retry = parsed
	}
	if value, ok := lookupEnv("CITYLIST_SERVER_RETRY_DELAY"); ok && strings.TrimSpace(value) != "" {
		parsed, parseErr := parseDurationSetting(value)
		if parseErr != nil || parsed < 0 {
			_, _ = fmt.Fprintln(errorOutput, "CITYLIST_SERVER_RETRY_DELAY must be a non-negative duration")
			return 2
		}
		retryDelay = parsed
	}
	if value, ok := lookupEnv("CITYLIST_SERVER_API_VERSION"); ok && strings.TrimSpace(value) != "" {
		cfg.Server.APIVersion = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("CITYLIST_DEBUG"); ok && strings.TrimSpace(value) != "" {
		parsed, parseErr := config.ParseBool(value, false)
		if parseErr != nil {
			_, _ = fmt.Fprintln(errorOutput, "CITYLIST_DEBUG must be a supported boolean value")
			return 2
		}
		cfg.Debug = parsed
	}
	if debugValue {
		cfg.Debug = true
	}
	if value, ok := lookupEnv("CITYLIST_TUI_ENABLED"); ok && strings.TrimSpace(value) != "" {
		parsed, parseErr := config.ParseBool(value, false)
		if parseErr != nil {
			_, _ = fmt.Fprintln(errorOutput, "CITYLIST_TUI_ENABLED must be a supported boolean value")
			return 2
		}
		cfg.TUI.Enabled = parsed
	}
	if err := ensureCLIDirectories(configPath); err != nil {
		_, _ = fmt.Fprintln(errorOutput, clientTextFormat(helpLang, "error_runtime_dirs", "{error}", map[string]string{"error": err.Error()}))
		return 1
	}
	loggerConfig := cfg
	if cfg.Debug || debugValue || cfg.Output.Verbose || verboseFlagValue {
		loggerConfig.Logging.Level = "debug"
	}
	if err := configureCLILogger(loggerConfig); err != nil {
		_, _ = fmt.Fprintln(errorOutput, clientTextFormat(helpLang, "error_log_config", "Could not configure CLI logging: {error}", map[string]string{"error": err.Error()}))
		return 1
	}
	activeCLILogger.Log("info", "CLI started")
	serverURL := cfg.Server.Primary
	if env, ok := lookupEnv("CITYLIST_SERVER_PRIMARY"); ok && strings.TrimSpace(env) != "" {
		serverURL = strings.TrimSpace(env)
	}
	if strings.TrimSpace(*serverFlag) != "" {
		serverURL = strings.TrimSpace(*serverFlag)
		if validServerURL(serverURL) {
			if err := saveServerIfEmptyOrInvalid(configPath, &cfg, serverURL); err != nil {
				_, _ = fmt.Fprintln(errorOutput, clientTextFormat(helpLang, "error_config_save", "{error}", map[string]string{"error": err.Error()}))
				return 1
			}
		}
	}
	token := cfg.Auth.Token
	if !validCLIToken(token) {
		token = ""
	}
	if cfg.Auth.TokenFile != "" {
		fileToken, err := readCLITokenFile(cfg.Auth.TokenFile)
		if err != nil {
			_, _ = fmt.Fprintln(errorOutput, clientTextFormat(helpLang, "error_read_token_file", "Could not read token file: {error}", map[string]string{"error": err.Error()}))
			return 2
		}
		if !validCLIToken(fileToken) {
			_, _ = fmt.Fprintln(errorOutput, clientText(helpLang, "error_invalid_token_file", "token file does not contain a valid owner or operator token"))
			return 2
		}
		token = fileToken
	}
	if env, ok := lookupEnv("CITYLIST_TOKEN"); ok && env != "" {
		token = strings.TrimSpace(env)
		if !validCLIToken(token) {
			_, _ = fmt.Fprintln(errorOutput, clientText(helpLang, "error_invalid_token", "token must be an owner or operator token"))
			return 2
		}
	}
	if strings.TrimSpace(*tokenFlag) != "" {
		token = strings.TrimSpace(*tokenFlag)
		if !validCLIToken(token) {
			_, _ = fmt.Fprintln(errorOutput, clientText(helpLang, "error_invalid_token", "token must be an owner or operator token"))
			return 2
		}
		if !validCLIToken(cfg.Auth.Token) {
			cfg.Auth.Token = token
			if err := saveCLIConfig(configPath, cfg); err != nil {
				_, _ = fmt.Fprintln(errorOutput, clientTextFormat(helpLang, "error_config_save", "{error}", map[string]string{"error": err.Error()}))
				return 1
			}
		}
	}
	if *tokenFile != "" {
		fileToken, err := readCLITokenFile(*tokenFile)
		if err != nil {
			_, _ = fmt.Fprintln(errorOutput, clientTextFormat(helpLang, "error_read_token_file", "Could not read token file: {error}", map[string]string{"error": err.Error()}))
			return 2
		}
		token = fileToken
		if !validCLIToken(token) {
			_, _ = fmt.Fprintln(errorOutput, clientText(helpLang, "error_invalid_token_file", "token file does not contain a valid owner or operator token"))
			return 2
		}
	}
	if handled, code := maybeUpdateCLI(args, input, output, errorOutput, serverURL, token, Version, requestTimeout, cfg); handled {
		return code
	}
	langValue := *langFlag
	if langValue == "" {
		langValue, _ = lookupEnv("CITYLIST_LANG")
	}
	lang := resolveLanguage(langValue, cfg.Lang, lookupEnv)
	format := cfg.Output.Format
	if value, ok := lookupEnv("CITYLIST_FORMAT"); ok && strings.TrimSpace(value) != "" {
		format = strings.ToLower(strings.TrimSpace(value))
	}
	if value, ok := lookupEnv("CITYLIST_OUTPUT_FORMAT"); ok && strings.TrimSpace(value) != "" {
		format = strings.ToLower(strings.TrimSpace(value))
	}
	if strings.TrimSpace(*outputFormat) != "" {
		format = strings.ToLower(strings.TrimSpace(*outputFormat))
	}
	if !validFormat(format) {
		_, _ = fmt.Fprintln(errorOutput, clientText(lang, "error_format", "output must be json, table, or plain"))
		return 2
	}
	quiet := cfg.Output.Quiet
	verbose := cfg.Output.Verbose
	quietValue, quietSet := lookupEnv("CITYLIST_OUTPUT_QUIET")
	if !quietSet || strings.TrimSpace(quietValue) == "" {
		quietValue, quietSet = lookupEnv("CITYLIST_QUIET")
	}
	if quietSet && strings.TrimSpace(quietValue) != "" {
		quiet, err = config.ParseBool(quietValue, false)
		if err != nil {
			_, _ = fmt.Fprintln(errorOutput, "CITYLIST_QUIET must be a supported boolean value")
			return 2
		}
	}
	verboseValue, verboseSet := lookupEnv("CITYLIST_OUTPUT_VERBOSE")
	if !verboseSet || strings.TrimSpace(verboseValue) == "" {
		verboseValue, verboseSet = lookupEnv("CITYLIST_VERBOSE")
	}
	if verboseSet && strings.TrimSpace(verboseValue) != "" {
		verbose, err = config.ParseBool(verboseValue, false)
		if err != nil {
			_, _ = fmt.Fprintln(errorOutput, "CITYLIST_VERBOSE must be a supported boolean value")
			return 2
		}
	}
	if quietFlagValue {
		quiet = true
	}
	if verboseFlagValue {
		verbose = true
	}
	pager := cfg.Output.Pager
	if value, ok := lookupEnv("CITYLIST_OUTPUT_PAGER"); ok && strings.TrimSpace(value) != "" {
		pager = strings.ToLower(strings.TrimSpace(value))
	}
	if *pagerFlag != "" {
		pager = strings.ToLower(strings.TrimSpace(*pagerFlag))
	}
	if pager != "auto" && pager != "always" && pager != "never" {
		_, _ = fmt.Fprintln(errorOutput, "pager must be auto, always, or never")
		return 2
	}

	remaining := flags.Args()
	if len(remaining) == 0 {
		env := display.DetectDisplayEnv()
		mode := cfg.Display.Mode
		if mode == "" {
			mode = "auto"
		}
		if mode != "auto" && mode != "gui" && mode != "tui" {
			_, _ = fmt.Fprintln(errorOutput, clientText(lang, "error_display_mode", "display.mode must be auto, gui, or tui"))
			return 2
		}
		if mode == "auto" {
			switch env.Mode {
			case display.DisplayModeGUI:
				if gui.Available() {
					mode = "gui"
				} else if env.IsTerminal && isInteractive(input) && !env.IsDumbTerminal() {
					mode = "tui"
				} else {
					_, _ = fmt.Fprintln(errorOutput, clientText(lang, "error_interactive_required", "interactive mode requires a graphical display or terminal"))
					return 2
				}
			case display.DisplayModeTUI:
				if isInteractive(input) {
					mode = "tui"
				} else {
					_, _ = fmt.Fprintln(errorOutput, clientText(lang, "error_tui_required", "TUI mode requires an interactive terminal"))
					return 2
				}
			default:
				_, _ = fmt.Fprintln(errorOutput, clientText(lang, "error_interactive_required", "interactive mode requires a graphical display or terminal"))
				return 2
			}
		}
		if mode == "tui" && (!isInteractive(input) || !env.IsTerminal || env.IsDumbTerminal()) {
			_, _ = fmt.Fprintln(errorOutput, clientText(lang, "error_tui_required", "TUI mode requires an interactive terminal"))
			return 2
		}
		if mode == "tui" && !cfg.TUI.Enabled {
			_, _ = fmt.Fprintln(errorOutput, "TUI is disabled by config")
			return 2
		}
		if mode == "gui" && !env.HasDisplay {
			_, _ = fmt.Fprintln(errorOutput, clientText(lang, "error_gui_display", "GUI mode requires a local graphical display"))
			return 2
		}
		if mode == "gui" && !gui.Available() {
			_, _ = fmt.Fprintln(errorOutput, clientText(lang, "error_gui_unavailable", "native GUI is unavailable on this platform"))
			return 2
		}
		var forceColor *bool
		if *color != "auto" {
			value := *color == "yes"
			forceColor = &value
		} else if cfg.Output.Color != "auto" {
			value := cfg.Output.Color == "yes"
			forceColor = &value
		}
		colors := theme.ColorEnabled(forceColor)
		selectedTheme := cfg.Theme.Mode
		if (selectedTheme == "" || selectedTheme == "auto") && cfg.TUI.Theme != "" {
			selectedTheme = cfg.TUI.Theme
		}
		if selectedTheme == "" || selectedTheme == "auto" || selectedTheme == "system" {
			selectedTheme = theme.DetectTerminalTheme()
		}
		if selectedTheme != "dark" && selectedTheme != "light" {
			_, _ = fmt.Fprintln(errorOutput, clientText(lang, "error_theme_mode", "theme.mode must be auto, dark, or light"))
			return 2
		}
		saveServer := func(value string) error {
			return saveServerIfEmptyOrInvalid(configPath, &cfg, value)
		}
		if mode == "gui" {
			guiTheme := cfg.Theme.Mode
			if (guiTheme == "" || guiTheme == "auto") && cfg.TUI.Theme != "" {
				guiTheme = cfg.TUI.Theme
			}
			if guiTheme == "" {
				guiTheme = "auto"
			}
			if err := gui.Run(serverURL, token, lang, Version, guiTheme, cfg.Server.APIVersion, requestTimeout, cfg.Server.Retry, retryDelay, saveServer); err != nil {
				_, _ = fmt.Fprintln(errorOutput, clientTextFormat(lang, "error_gui_start", "Could not start the graphical client: {error}", map[string]string{"error": err.Error()}))
				return 1
			}
			return 0
		}
		if err := tui.Run(input, output, serverURL, token, lang, selectedTheme, Version, colors, cfg.Server.APIVersion, requestTimeout, cfg.Server.Retry, retryDelay, cfg.TUI.Mouse, cfg.TUI.Unicode, saveServer); err != nil {
			_, _ = fmt.Fprintln(errorOutput, clientTextFormat(lang, "error_tui_start", "Could not start the terminal client: {error}", map[string]string{"error": err.Error()}))
			return 1
		}
		return 0
	}
	if len(remaining) == 0 && !cfg.TUI.Enabled {
		_, _ = fmt.Fprintln(errorOutput, "TUI is disabled by config; provide a command")
		return 2
	}
	if !isCLICommand(remaining[0]) {
		remaining = append([]string{"search"}, remaining...)
	}
	endpoint, err := api.EndpointWithLanguage(remaining[0], remaining[1:], lang)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, clientTextFormat(lang, "error_command", "{error}", map[string]string{"error": err.Error()}))
		flags.Usage()
		return 2
	}
	if serverURL == "" {
		_, _ = fmt.Fprintln(errorOutput, clientText(lang, "error_server_required", "server URL is required; set --server or CITYLIST_SERVER_PRIMARY"))
		return 2
	}
	clientAPI, err := api.NewWithLanguage(serverURL, lang)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, err)
		return 2
	}
	if err := clientAPI.SetAPIVersion(cfg.Server.APIVersion); err != nil {
		_, _ = fmt.Fprintln(errorOutput, err)
		return 2
	}
	if err := clientAPI.SetRequestPolicy(requestTimeout, cfg.Server.Retry, retryDelay); err != nil {
		_, _ = fmt.Fprintln(errorOutput, err)
		return 2
	}
	clientAPI.SetUserAgent(ProjectName + "-cli/" + Version)
	clientAPI.SetCredentials(token, lang)
	if (verbose || cfg.Debug || cfg.Logging.Level == "debug") && !quiet {
		path := strings.SplitN(endpoint, "?", 2)[0]
		_, _ = fmt.Fprintf(errorOutput, "GET %s%s\n", strings.TrimRight(serverURL, "/"), path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	accept := "application/json"
	if format == "plain" {
		accept = "text/plain, application/json;q=0.8"
	}
	body, err := clientAPI.GetWithAccept(ctx, endpoint, accept)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, err)
		return 1
	}
	if err := writeResponseWithPager(output, errorOutput, body, format, pager); err != nil {
		_, _ = fmt.Fprintln(errorOutput, responseError(lang, err))
		return 1
	}
	return 0
}

func printHelp(output io.Writer, binaryName, lang string) {
	_, _ = fmt.Fprintln(output, clientTextFormat(lang, "help", "{binary} {version} - Citylist client\n\nUsage:\n  {binary} [flags] <command> [arguments]\n  {binary}                      Start the interactive client\n\nCommands:\n  city ID\n  zipcode ZIP\n  search QUERY [COUNTRY]\n  zip-search QUERY [STATE]\n  country CODE\n  cities [PAGE] [LIMIT]\n  stats\n  random\n  nearest LATITUDE LONGITUDE\n  nearby LATITUDE LONGITUDE RADIUS_KM\n  zip-state STATE [LIMIT]\n  zip-nearby LATITUDE LONGITUDE RADIUS_KM\n\nFlags:\n  -h, --help                 Show help\n  -v, --version              Show version\n      --server URL           Server URL\n      --token TOKEN          API token\n      --token-file FILE      Read API token from file\n      --config NAME          Configuration profile (default cli.yml)\n      --output FORMAT        json, table, or plain\n      --lang CODE            Output language\n      --color MODE           auto, yes, or no\n      --debug                Enable debug output\n      --shell ACTION [SHELL] Print shell completions or init command", map[string]string{
		"binary": binaryName, "version": Version,
	}))
}

func isCLICommand(command string) bool {
	return contains([]string{"city", "zipcode", "search", "zip-search", "country", "cities", "stats", "random", "nearest", "nearby", "zip-state", "zip-nearby"}, command)
}

func writeResponse(output io.Writer, body []byte, format string) error {
	if format == "plain" {
		var value any
		if json.Unmarshal(body, &value) != nil {
			_, err := fmt.Fprint(output, string(body))
			return err
		}
		return writePlain(output, value)
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		if format == "table" {
			return errTableRequiresJSON
		}
		_, writeErr := fmt.Fprint(output, string(body))
		return writeErr
	}
	if format == "yaml" {
		if envelope, ok := value.(map[string]any); ok {
			if data, exists := envelope["data"]; exists {
				value = data
			}
		}
		encoded, err := yaml.Marshal(value)
		if err != nil {
			return err
		}
		_, err = output.Write(encoded)
		return err
	}
	if format == "csv" {
		return writeCSV(output, value)
	}
	if format == "table" {
		return writeTable(output, value)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeCSV(output io.Writer, value any) error {
	if envelope, ok := value.(map[string]any); ok {
		if data, exists := envelope["data"]; exists {
			value = data
		}
	}
	var rows []any
	switch item := value.(type) {
	case []any:
		rows = item
	case map[string]any:
		rows = []any{item}
	default:
		return fmt.Errorf("CSV output requires an object or array of objects")
	}
	keysSet := make(map[string]struct{})
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("CSV output requires an array of objects")
		}
		for key := range row {
			keysSet[key] = struct{}{}
		}
	}
	keys := make([]string, 0, len(keysSet))
	for key := range keysSet {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	writer := csv.NewWriter(output)
	if len(keys) > 0 {
		if err := writer.Write(keys); err != nil {
			return err
		}
		for _, item := range rows {
			row := item.(map[string]any)
			record := make([]string, len(keys))
			for index, key := range keys {
				if row[key] == nil {
					continue
				}
				if nested, ok := row[key].(map[string]any); ok {
					encoded, err := json.Marshal(nested)
					if err != nil {
						return err
					}
					record[index] = string(encoded)
					continue
				}
				if nested, ok := row[key].([]any); ok {
					encoded, err := json.Marshal(nested)
					if err != nil {
						return err
					}
					record[index] = string(encoded)
					continue
				}
				record[index] = fmt.Sprint(row[key])
			}
			if err := writer.Write(record); err != nil {
				return err
			}
		}
	}
	writer.Flush()
	return writer.Error()
}

func writePlain(output io.Writer, value any) error {
	switch item := value.(type) {
	case map[string]any:
		if data, ok := item["data"]; ok {
			return writePlain(output, data)
		}
		for key, value := range item {
			if _, err := fmt.Fprintf(output, "%s: %v\n", key, value); err != nil {
				return err
			}
		}
		return nil
	case []any:
		for _, value := range item {
			if err := writePlain(output, value); err != nil {
				return err
			}
		}
		return nil
	default:
		_, err := fmt.Fprintln(output, value)
		return err
	}
}

func writeTable(output io.Writer, value any) error {
	if envelope, ok := value.(map[string]any); ok {
		if data, exists := envelope["data"]; exists {
			value = data
		}
	}
	rows, ok := value.([]any)
	if !ok {
		if object, isObject := value.(map[string]any); isObject {
			rows = []any{object}
		} else {
			return writePlain(output, value)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	first, ok := rows[0].(map[string]any)
	if !ok {
		return writePlain(output, rows)
	}
	keys := make([]string, 0, len(first))
	for key := range first {
		keys = append(keys, key)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	for i, key := range keys {
		if i > 0 {
			_, _ = io.WriteString(writer, "\t")
		}
		_, _ = io.WriteString(writer, key)
	}
	_, _ = io.WriteString(writer, "\n")
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for i, key := range keys {
			if i > 0 {
				_, _ = io.WriteString(writer, "\t")
			}
			_, _ = fmt.Fprint(writer, row[key])
		}
		_, _ = io.WriteString(writer, "\n")
	}
	return writer.Flush()
}

func validFormat(format string) bool {
	return format == "json" || format == "table" || format == "plain" || format == "yaml" || format == "csv"
}

func parseDurationSetting(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}
	return time.ParseDuration(value)
}

func flagValue(args []string, name string) string {
	prefix := "--" + name + "="
	for i, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
		if arg == "--"+name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func validColor(color string) bool {
	return color == "auto" || color == "yes" || color == "no"
}

func validServerURL(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	_, err := api.New(value)
	return err == nil
}

func validCLIToken(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 || (!strings.HasPrefix(value, "own_") && !strings.HasPrefix(value, "adm_")) {
		return false
	}
	for _, character := range value[4:] {
		if (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func readCLITokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func resolveLanguage(flagLang, configLang string, lookupEnv func(string) (string, bool)) string {
	for _, candidate := range []string{flagLang, configLang} {
		if candidate != "" && candidate != "auto" {
			if i18n.IsSupported(candidate) {
				return strings.ToLower(candidate)
			}
			return "en"
		}
	}
	for _, name := range []string{"LC_ALL", "LANG"} {
		if value, ok := lookupEnv(name); ok && value != "" {
			value = strings.ToLower(strings.Split(strings.Split(value, ".")[0], "_")[0])
			if i18n.IsSupported(value) {
				return value
			}
		}
	}
	return "en"
}

func isInteractive(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func responseError(lang string, err error) string {
	if errors.Is(err, errTableRequiresJSON) {
		return clientText(lang, "error_table_json", "table output requires a JSON response")
	}
	return clientTextFormat(lang, "error_output", "{error}", map[string]string{"error": err.Error()})
}
