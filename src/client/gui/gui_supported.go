//go:build gui && (linux || darwin || windows)

package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/citylist/src/client/api"
	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/gogpu/gg"
	_ "github.com/gogpu/gg/gpu"
	"github.com/gogpu/gg/integration/ggcanvas"
	"github.com/gogpu/gogpu"
	"github.com/gogpu/ui/app"
	"github.com/gogpu/ui/core/button"
	"github.com/gogpu/ui/core/dropdown"
	"github.com/gogpu/ui/core/textfield"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/render"
	"github.com/gogpu/ui/state"
	guitheme "github.com/gogpu/ui/theme"
	"github.com/gogpu/ui/widget"
)

var guiCommands = []string{
	"city", "zipcode", "search", "zip-search", "country", "cities",
	"stats", "random", "nearest", "nearby", "zip-state", "zip-nearby",
}

var guiFields = [][]string{
	{"City ID"}, {"ZIP code"}, {"Query", "Country code (optional)"}, {"City or state", "State code (optional)"},
	{"Country code"}, {"Page (optional)", "Limit (optional)"}, nil, nil, {"Latitude", "Longitude"},
	{"Latitude", "Longitude", "Radius in km"}, {"State code", "Limit (optional)"}, {"Latitude", "Longitude", "Radius in km"},
}

type inputState struct {
	mu        sync.Mutex
	serverURL string
	command   int
	values    [3]string
}

// Available reports whether the native GUI is compiled for this platform.
func Available() bool { return true }

// Run starts the native client window.
func Run(serverURL, token, language, version, themeMode, apiVersion string, timeout time.Duration, retries int, retryDelay time.Duration, saveServer func(string) error) error {
	stateData := &inputState{serverURL: serverURL}
	result := state.NewSignal(guiText(language, "gui_initial_result"))
	dropdownItems := make([]string, len(guiCommands))
	for index := range guiCommands {
		dropdownItems[index] = guiText(language, "gui_command_"+guiCommands[index])
	}
	selector := dropdown.New(dropdown.Items(dropdownItems...), dropdown.Placeholder(guiText(language, "gui_operation_placeholder")), dropdown.OnChange(func(index int, _ string) {
		if index < 0 || index >= len(guiCommands) {
			return
		}
		stateData.mu.Lock()
		stateData.command = index
		stateData.values = [3]string{}
		stateData.mu.Unlock()
	}))
	fields := make([]any, 0, 5)
	fields = append(fields, primitives.Text(guiText(language, "gui_title")).FontSize(24).Bold(), primitives.Text(guiText(language, "gui_description")))
	if serverURL != "" {
		fields = append(fields, primitives.Text(i18n.TranslateFormat(language, "cli.gui_server_configured", map[string]string{"server": serverURL})))
	}
	serverInfo := state.NewSignal("")
	serverInput := textfield.New(textfield.Placeholder(guiText(language, "gui_server_placeholder")), textfield.OnChange(func(value string) {
		stateData.mu.Lock()
		stateData.serverURL = strings.TrimSpace(value)
		stateData.mu.Unlock()
	}))
	fields = append(fields, serverInput, selector)
	fields = append(fields, button.New(button.TextOpt("Test Connection"), button.OnClick(func() {
		stateData.mu.Lock()
		server := strings.TrimSpace(stateData.serverURL)
		stateData.mu.Unlock()
		client, err := api.NewWithLanguage(server, language)
		if err != nil {
			serverInfo.Set(err.Error())
			return
		}
		client.SetUserAgent("citylist-cli/" + version)
		if err := client.SetAPIVersion(apiVersion); err != nil {
			serverInfo.Set(err.Error())
			return
		}
		if err := client.SetRequestPolicy(timeout, retries, retryDelay); err != nil {
			serverInfo.Set(err.Error())
			return
		}
		client.SetCredentials(token, language)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			body, err := client.Get(ctx, "/api/v1/server/healthz")
			if err != nil {
				serverInfo.Set(err.Error())
				return
			}
			tokenKind := ""
			if token != "" {
				authBody, err := client.Get(ctx, "/api/v1/server/auth/check")
				if err != nil {
					serverInfo.Set(err.Error())
					return
				}
				tokenKind = guiTokenKind(authBody)
			}
			info := guiHealthServerLabel(body, server)
			if saveServer != nil {
				if err := saveServer(server); err != nil {
					serverInfo.Set(i18n.TranslateFormat(language, "cli.error_config_save", map[string]string{"error": err.Error()}))
					return
				}
			}
			if tokenKind != "" {
				info += " (" + tokenKind + " token)"
			}
			serverInfo.Set("Connected to " + info)
		}()
	})))
	fields = append(fields, primitives.Text("").ContentSignal(serverInfo))
	for index := 0; index < 3; index++ {
		fieldIndex := index
		fields = append(fields, textfield.New(textfield.Placeholder(i18n.TranslateFormat(language, "cli.gui_input_placeholder", map[string]string{"number": fmt.Sprint(index + 1)})), textfield.OnChange(func(value string) {
			stateData.mu.Lock()
			stateData.values[fieldIndex] = value
			stateData.mu.Unlock()
		})))
	}
	uiTheme := guitheme.DefaultDark()
	if themeMode == "light" || themeMode == "auto" && !systemThemeIsDark() {
		uiTheme = guitheme.DefaultLight()
	}
	fields = append(fields, button.New(button.TextOpt(guiText(language, "gui_search")), button.OnClick(func() {
		stateData.mu.Lock()
		selected := stateData.command
		if selected < 0 || selected >= len(guiCommands) {
			stateData.mu.Unlock()
			result.Set(guiText(language, "gui_choose_valid_operation"))
			return
		}
		server := strings.TrimSpace(stateData.serverURL)
		fields := guiFields[selected]
		values := append([]string(nil), stateData.values[:len(fields)]...)
		stateData.mu.Unlock()
		for index, value := range values {
			values[index] = strings.TrimSpace(value)
			if values[index] == "" && !strings.HasSuffix(fields[index], "(optional)") {
				result.Set(i18n.TranslateFormat(language, "cli.gui_field_required", map[string]string{"field": guiFieldLabel(language, selected, index, fields[index])}))
				return
			}
		}
		for len(values) > 0 && values[len(values)-1] == "" {
			values = values[:len(values)-1]
		}
		if server == "" {
			result.Set(guiText(language, "gui_server_required"))
			return
		}
		go func() {
			endpoint, err := api.EndpointWithLanguage(guiCommands[selected], values, language)
			if err != nil {
				result.Set(err.Error())
				return
			}
			if saveServer != nil {
				if err := saveServer(server); err != nil {
					result.Set(i18n.TranslateFormat(language, "cli.error_config_save", map[string]string{"error": err.Error()}))
					return
				}
			}
			client, err := api.NewWithLanguage(server, language)
			if err != nil {
				result.Set(err.Error())
				return
			}
			client.SetUserAgent("citylist-cli/" + version)
			if err := client.SetAPIVersion(apiVersion); err != nil {
				result.Set(err.Error())
				return
			}
			if err := client.SetRequestPolicy(timeout, retries, retryDelay); err != nil {
				result.Set(err.Error())
				return
			}
			client.SetCredentials(token, language)
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			body, err := client.GetWithAccept(ctx, endpoint, "application/json")
			if err != nil {
				result.Set(err.Error())
				return
			}
			var value any
			if json.Unmarshal(body, &value) == nil {
				if formatted, marshalErr := json.MarshalIndent(value, "", "  "); marshalErr == nil {
					result.Set(string(formatted))
					return
				}
			}
			result.Set(string(body))
		}()
	})))
	fields = append(fields, primitives.Text("").ContentSignal(result))
	children := make([]widget.Widget, 0, len(fields))
	for _, field := range fields {
		child, ok := field.(widget.Widget)
		if ok {
			children = append(children, child)
		}
	}
	windowApp := gogpu.NewApp(gogpu.DefaultConfig().WithTitle(guiText(language, "gui_title")).WithSize(1000, 760))
	uiApp := app.New(
		app.WithWindowProvider(windowApp),
		app.WithPlatformProvider(windowApp),
		app.WithEventSource(windowApp.EventSource()),
		app.WithTheme(uiTheme),
	)
	uiApp.SetRoot(primitives.Box(children...).Padding(24).Gap(12))
	var canvas *ggcanvas.Canvas
	windowApp.OnDraw(func(dc *gogpu.Context) {
		w, h := dc.Width(), dc.Height()
		if w <= 0 || h <= 0 {
			return
		}
		if canvas == nil {
			provider := windowApp.GPUContextProvider()
			if provider == nil {
				return
			}
			var err error
			canvas, err = ggcanvas.New(provider, w, h)
			if err != nil {
				result.Set(i18n.TranslateFormat(language, "cli.gui_renderer_error", map[string]string{"error": err.Error()}))
				return
			}
		}
		uiApp.Frame()
		cw, ch := canvas.Size()
		if cw != w || ch != h {
			if err := canvas.Resize(w, h); err != nil {
				result.Set(i18n.TranslateFormat(language, "cli.gui_resize_error", map[string]string{"error": err.Error()}))
				return
			}
			cw, ch = w, h
		}
		target := dc.RenderTarget()
		surface := target.SurfaceView()
		sw, sh := target.SurfaceSize()
		if err := canvas.Draw(func(cc *gg.Context) {
			widgetCanvas := render.NewCanvas(cc, cw, ch)
			uiApp.Window().DrawTo(widgetCanvas)
		}); err != nil {
			result.Set(i18n.TranslateFormat(language, "cli.gui_render_error", map[string]string{"error": err.Error()}))
			return
		}
		if err := canvas.RenderDirect(surface, sw, sh); err != nil {
			result.Set(i18n.TranslateFormat(language, "cli.gui_render_error", map[string]string{"error": err.Error()}))
		}
	})
	windowApp.OnClose(func() { gg.CloseAccelerator() })
	return windowApp.Run()
}

func guiHealthServerLabel(body []byte, fallback string) string {
	var response struct {
		Project struct {
			Name string `json:"name"`
		} `json:"project"`
		Version string `json:"version"`
	}
	if json.Unmarshal(body, &response) != nil || response.Project.Name == "" {
		return fallback
	}
	if response.Version == "" {
		return response.Project.Name
	}
	return response.Project.Name + " " + response.Version
}

func guiTokenKind(body []byte) string {
	var response struct {
		Data map[string]string `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil {
		return ""
	}
	return response.Data["kind"]
}

func guiText(language, key string) string {
	return i18n.Translate(language, "cli."+key)
}

func guiFieldLabel(language string, command, fieldIndex int, fallback string) string {
	fieldKeys := [][]string{
		{"prompt_city_id"}, {"prompt_zipcode"}, {"prompt_search_query", "prompt_country_optional"},
		{"prompt_zip_search", "prompt_state_optional"}, {"prompt_country"}, {"prompt_page_optional", "prompt_limit_optional"},
		nil, nil, {"prompt_latitude", "prompt_longitude"}, {"prompt_latitude", "prompt_longitude", "prompt_radius"},
		{"prompt_state", "prompt_limit_optional"}, {"prompt_latitude", "prompt_longitude", "prompt_radius"},
	}
	if command >= 0 && command < len(fieldKeys) && fieldIndex >= 0 && fieldIndex < len(fieldKeys[command]) {
		return i18n.Translate(language, "cli."+fieldKeys[command][fieldIndex])
	}
	return fallback
}
