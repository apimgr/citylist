package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/overlay/tor"
	"golang.org/x/term"
)

type torCLIEnvelope struct {
	OK      bool            `json:"ok"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

type torStatusResponse struct {
	Running      bool   `json:"running"`
	OnionAddress string `json:"onion_address"`
}

type healthCLIResponse struct {
	Mode     string `json:"mode"`
	Uptime   string `json:"uptime"`
	Status   string `json:"status"`
	Features struct {
		Tor struct {
			Running  bool   `json:"running"`
			Status   string `json:"status"`
			Hostname string `json:"hostname"`
		} `json:"tor"`
		I2P struct {
			Enabled  bool   `json:"enabled"`
			Running  bool   `json:"running"`
			Status   string `json:"status"`
			State    string `json:"state"`
			Hostname string `json:"hostname"`
			Provider string `json:"provider"`
		} `json:"i2p"`
	} `json:"features"`
}

func readHealth(port string) (healthCLIResponse, error) {
	var result healthCLIResponse
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+port+"/server/healthz", nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return result, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return result, errors.New(serverOutputFormat("tor_health_status", "Health check returned status {status}", map[string]string{"status": strconv.Itoa(resp.StatusCode)}))
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result)
	if err != nil {
		return result, errors.New(serverOutputFormat("tor_response_invalid", "Server returned an invalid Tor maintenance response: {error}", map[string]string{"error": err.Error()}))
	}
	if result.Mode == "" {
		return result, errors.New(torCLIText("health_details_missing", "Health response did not include server status details"))
	}
	return result, nil
}

func requestTorControl(port, method, path string, body any) (torCLIEnvelope, error) {
	var result torCLIEnvelope
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return result, err
		}
		requestBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, "http://127.0.0.1:"+port+path, requestBody)
	if err != nil {
		return result, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return result, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return result, errors.New(serverOutputFormat("tor_response_invalid", "Server returned an invalid Tor maintenance response: {error}", map[string]string{"error": err.Error()}))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		message := result.Message
		if message == "" {
			message = result.Error
		}
		if message == "" {
			message = resp.Status
		}
		return result, errors.New(message)
	}
	return result, nil
}

func maintenanceTor(args []string, configDir, dataDir, port string, cfg *config.Config) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return errors.New(torCLIText("usage", "Usage: citylist --maintenance tor {status|validate|restart|regenerate|vanity start|vanity stop|vanity apply [address]|import-keys <path>}"))
	}
	action := args[0]
	if action == "vanity" {
		if len(args) < 2 {
			return errors.New(torCLIText("vanity_usage", "Usage: citylist --maintenance tor vanity {start|stop|apply [address]}"))
		}
		action = "vanity-" + args[1]
		args = args[2:]
	} else {
		args = args[1:]
	}
	if err := authorizeTorMaintenance(cfg); err != nil {
		return err
	}

	method, path := http.MethodPost, ""
	var body any = map[string]any{}
	mutating := true
	switch action {
	case "status":
		method, path, mutating, body = http.MethodGet, "/server/tor/status", false, nil
	case "validate":
		path, mutating = "/server/tor/validate", false
	case "restart":
		path = "/server/tor/restart"
	case "regenerate":
		path = "/server/tor/regenerate"
		if err := confirmTorAction("REGENERATE TOR ADDRESS", args); err != nil {
			return err
		}
		body = map[string]any{"confirm": true}
	case "vanity-start":
		prefix, workers, err := parseTorVanityStart(args)
		if err != nil {
			return err
		}
		body = map[string]any{"prefix": prefix, "workers": workers}
		path = "/server/tor/vanity/start"
	case "vanity-stop":
		path = "/server/tor/vanity/stop"
	case "vanity-apply":
		address, remaining := firstNonFlag(args)
		if err := confirmTorAction("APPLY TOR VANITY ADDRESS", remaining); err != nil {
			return err
		}
		body = map[string]any{"address": address, "confirm": true}
		path = "/server/tor/vanity/apply"
	case "import-keys":
		pathArg, remaining := firstNonFlag(args)
		if !filepath.IsAbs(pathArg) {
			return errors.New(torCLIText("import_path_absolute", "import-keys requires an absolute key directory path"))
		}
		if err := confirmTorAction("IMPORT TOR KEYS", remaining); err != nil {
			return err
		}
		body = map[string]any{"path": pathArg, "confirm": true}
		path = "/server/tor/import-keys"
	default:
		return errors.New(serverOutputFormat("tor_unknown_command", "Unknown Tor maintenance command: {command}", map[string]string{"command": action}))
	}

	response, err := requestTorControl(port, method, path, body)
	if err != nil {
		if !mutating && (action == "status" || action == "validate") {
			return torMaintenanceFallback(action, configDir, dataDir, cfg)
		}
		if mutating {
			if !serverListenerReachable(port) {
				return errors.New(torCLIText("no_running_server", "Error: no running server detected — start the server first"))
			}
		}
		return err
	}
	var data map[string]any
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return errors.New(serverOutputFormat("tor_response_invalid", "Server returned an invalid Tor maintenance response: {error}", map[string]string{"error": err.Error()}))
	}
	encoded, _ := json.MarshalIndent(data, "", "  ")
	_, err = fmt.Fprintln(os.Stdout, string(encoded))
	return err
}

func serverListenerReachable(port string) bool {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 2*time.Second)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func authorizeTorMaintenance(cfg *config.Config) error {
	if os.Geteuid() == 0 {
		return nil
	}
	if cfg == nil || cfg.Server.Token == "" {
		return errors.New(torCLIText("authorization_required", "Tor maintenance requires server.token or root"))
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New(torCLIText("authorization_terminal", "Tor maintenance authorization requires a terminal to enter the operator token"))
	}
	provided, err := readHiddenPGPInput(serverOutputText("pgp_operator_token_prompt", "Operator token: "))
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(cfg.Server.Token)) != 1 {
		return errors.New(serverOutputText("pgp_operator_token_rejected", "Operator token was not accepted"))
	}
	return nil
}

func confirmTorAction(phrase string, args []string) error {
	for _, arg := range args {
		if arg == "--yes" {
			return nil
		}
	}
	prompt := serverOutputFormat("pgp_confirm_prompt", "Type {phrase} to continue", map[string]string{"phrase": phrase})
	return requirePGPConfirmation(prompt, phrase)
}

func firstNonFlag(args []string) (string, []string) {
	for index, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg, append(append([]string{}, args[:index]...), args[index+1:]...)
		}
	}
	return "", args
}

func parseTorVanityStart(args []string) (string, int, error) {
	prefix, workers := "", 0
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--prefix":
			if index+1 >= len(args) {
				return "", 0, errors.New(torCLIText("prefix_required", "--prefix requires a value"))
			}
			index++
			prefix = args[index]
		case "--workers":
			if index+1 >= len(args) {
				return "", 0, errors.New(torCLIText("workers_required", "--workers requires a value"))
			}
			index++
			value, err := strconv.Atoi(args[index])
			if err != nil {
				return "", 0, errors.New(torCLIText("workers_integer", "--workers must be an integer"))
			}
			workers = value
		default:
			if strings.HasPrefix(args[index], "-") {
				return "", 0, errors.New(serverOutputFormat("tor_vanity_unknown_option", "Unknown Tor vanity option: {option}", map[string]string{"option": args[index]}))
			}
			if prefix != "" {
				return "", 0, errors.New(torCLIText("one_vanity_prefix", "only one vanity prefix may be specified"))
			}
			prefix = args[index]
		}
	}
	if prefix == "" {
		return "", 0, errors.New(torCLIText("vanity_prefix_required", "vanity start requires --prefix PREFIX"))
	}
	return prefix, workers, nil
}

func torMaintenanceFallback(action, configDir, dataDir string, cfg *config.Config) error {
	if action == "status" {
		address, err := tor.ReadOnionAddress(dataDir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New(serverOutputFormat("tor_status_read_failed", "Could not read the Tor service status: {error}", map[string]string{"error": err.Error()}))
		}
		fmt.Printf("%s\n", torCLIText("disabled", "Tor Hidden Service: Disabled"))
		if address != "" {
			fmt.Printf("%s\n  %s: %s\n", torCLIText("stopped", "Tor Hidden Service: Stopped"), torCLIText("address", "Address"), address)
		}
		return nil
	}
	options := tor.DefaultOptions(configDir, dataDir, filepath.Join(dataDir, "logs"))
	if cfg != nil {
		options.TorBinary = cfg.Server.Tor.Binary
		options.MaxCircuits = cfg.Server.Tor.MaxCircuits
		if cfg.Server.Tor.StartupTimeout > 0 {
			options.StartupTimeout = time.Duration(cfg.Server.Tor.StartupTimeout) * time.Second
		}
		if cfg.Server.Tor.CircuitTimeout > 0 {
			options.CircuitTimeout = time.Duration(cfg.Server.Tor.CircuitTimeout) * time.Second
		}
		if cfg.Server.Tor.BootstrapTimeout > 0 {
			options.BootstrapTimeout = time.Duration(cfg.Server.Tor.BootstrapTimeout) * time.Second
		}
		options.SafeLogging = cfg.Server.Tor.SafeLogging
		options.MaxStreamsPerCircuit = cfg.Server.Tor.MaxStreamsPerCircuit
		options.CloseCircuitOnStreamLimit = cfg.Server.Tor.CloseCircuitOnStreamLimit
		options.BandwidthRate = cfg.Server.Tor.BandwidthRate
		options.BandwidthBurst = cfg.Server.Tor.BandwidthBurst
		options.MaxMonthlyBandwidth = cfg.Server.Tor.MaxMonthlyBandwidth
		options.NumIntroPoints = cfg.Server.Tor.NumIntroPoints
		options.VirtualPort = cfg.Server.Tor.VirtualPort
	}
	if err := tor.ValidateConfig(options); err != nil {
		return errors.New(serverOutputFormat("tor_validation_failed", "Tor configuration validation failed: {error}", map[string]string{"error": err.Error()}))
	}
	_, err := fmt.Fprintln(os.Stdout, torCLIText("valid", "Tor configuration is valid"))
	return err
}

func torCLIText(key, fallback string) string {
	return serverOutputText("tor_"+key, fallback)
}
