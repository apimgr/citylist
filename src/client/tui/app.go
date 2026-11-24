package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	bubbletea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/apimgr/citylist/src/client/api"
	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/common/terminal"
	"github.com/apimgr/citylist/src/common/theme"
)

type command struct {
	name   string
	label  string
	fields []string
}

var commands = []command{
	{name: "city", label: "City by ID", fields: []string{"City ID"}},
	{name: "zipcode", label: "ZIP code", fields: []string{"ZIP code"}},
	{name: "search", label: "Search cities", fields: []string{"Query", "Country code (optional)"}},
	{name: "zip-search", label: "Search ZIP codes", fields: []string{"City or state", "State code (optional)"}},
	{name: "country", label: "Cities by country", fields: []string{"Country code"}},
	{name: "cities", label: "List cities", fields: []string{"Page (optional)", "Limit (optional)"}},
	{name: "stats", label: "Dataset statistics"},
	{name: "random", label: "Random city"},
	{name: "nearest", label: "Nearest city", fields: []string{"Latitude", "Longitude"}},
	{name: "nearby", label: "Nearby cities", fields: []string{"Latitude", "Longitude", "Radius in km"}},
	{name: "zip-state", label: "ZIP codes by state", fields: []string{"State code", "Limit (optional)"}},
	{name: "zip-nearby", label: "Nearby ZIP codes", fields: []string{"Latitude", "Longitude", "Radius in km"}},
}

type responseMsg struct {
	body []byte
	info string
	err  error
}

type viewMode uint8

const (
	setupMode viewMode = iota
	menuMode
	formMode
	resultMode
	helpMode
)

type model struct {
	serverURL  string
	token      string
	language   string
	theme      string
	version    string
	apiVersion string
	timeout    time.Duration
	retries    int
	retryDelay time.Duration
	mode       viewMode
	cursor     int
	form       int
	values     []string
	input      textinput.Model
	width      int
	height     int
	sizeMode   terminal.SizeMode
	filter     string
	filtering  bool
	result     []string
	resultPos  int
	status     string
	loading    bool
	saveServer func(string) error
	colors     bool
	mouse      bool
	unicode    bool
}

// Run starts the keyboard-driven interactive client.
func Run(input io.Reader, output io.Writer, serverURL, token, language, theme, version string, colors bool, apiVersion string, timeout time.Duration, retries int, retryDelay time.Duration, mouse, unicode bool, saveServer func(string) error) error {
	inputModel := textinput.New()
	inputModel.Placeholder = "Enter a value"
	inputModel.SetWidth(64)
	initial := model{
		serverURL:  serverURL,
		token:      token,
		language:   language,
		theme:      theme,
		version:    version,
		apiVersion: apiVersion,
		timeout:    timeout,
		retries:    retries,
		retryDelay: retryDelay,
		input:      inputModel,
		width:      80,
		height:     24,
		saveServer: saveServer,
		colors:     colors,
		mouse:      mouse,
		unicode:    unicode,
	}
	if strings.TrimSpace(serverURL) == "" {
		initial.mode = setupMode
		initial.input.Placeholder = localized(language, "tui_server_placeholder", "https://citylist.example.com")
		initial.input.Focus()
	}
	program := bubbletea.NewProgram(initial, bubbletea.WithInput(input), bubbletea.WithOutput(output))
	_, err := program.Run()
	return err
}

func (m model) Init() bubbletea.Cmd {
	return nil
}

func (m model) Update(message bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	switch msg := message.(type) {
	case bubbletea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sizeMode = terminal.DetectTerminalSize(msg.Width, msg.Height).Mode
		if m.width < 20 {
			m.width = 20
		}
		if m.height < 5 {
			m.height = 5
		}
		m.input.SetWidth(m.width - 8)
		return m, nil
	case bubbletea.MouseWheelMsg:
		if !m.mouse {
			return m, nil
		}
		switch msg.Button {
		case bubbletea.MouseWheelUp:
			if m.mode == menuMode && m.cursor > 0 {
				m.cursor--
			} else if m.mode == resultMode && m.resultPos > 0 {
				m.resultPos--
			}
		case bubbletea.MouseWheelDown:
			if m.mode == menuMode && m.cursor < len(m.visibleCommands())-1 {
				m.cursor++
			} else if m.mode == resultMode && m.resultPos < max(0, len(m.result)-m.resultHeight()) {
				m.resultPos++
			}
		}
		return m, nil
	case responseMsg:
		m.loading = false
		if m.mode == setupMode {
			if msg.err != nil {
				m.status = msg.err.Error()
				return m, nil
			}
			if m.saveServer != nil {
				if err := m.saveServer(m.serverURL); err != nil {
					m.status = i18n.TranslateFormat(m.language, "cli.error_config_save", map[string]string{"error": err.Error()})
					return m, nil
				}
			}
			m.mode = menuMode
			m.status = i18n.TranslateFormat(m.language, "cli.tui_setup_connected", map[string]string{"info": msg.info})
			if m.status == "cli.tui_setup_connected" {
				m.status = "Connection successful: " + msg.info
			}
			return m, nil
		}
		if msg.err != nil {
			m.status = msg.err.Error()
			m.mode = resultMode
			return m, nil
		}
		var value any
		if err := json.Unmarshal(msg.body, &value); err == nil {
			formatted, err := json.MarshalIndent(value, "", "  ")
			if err == nil {
				m.result = strings.Split(string(formatted), "\n")
			}
		} else {
			m.result = strings.Split(string(msg.body), "\n")
		}
		m.mode = resultMode
		m.resultPos = 0
		m.status = ""
		return m, nil
	case mouseSelectMsg:
		if m.mode == menuMode {
			visible := m.visibleCommands()
			if msg.index >= 0 && msg.index < len(visible) {
				m.cursor = msg.index
				return m.updateMenu("enter")
			}
		}
		return m, nil
	case bubbletea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" || key == "q" && m.mode == menuMode && !m.filtering {
			return m, bubbletea.Quit
		}
		if m.mode == setupMode {
			if key == "enter" {
				serverURL := strings.TrimSpace(m.input.Value())
				if _, err := api.NewWithLanguage(serverURL, m.language); err != nil {
					m.status = localized(m.language, "error_invalid_server_url", "Enter a valid server URL")
					return m, nil
				}
				m.serverURL = serverURL
				m.loading = true
				m.status = localized(m.language, "tui_setup_testing", "Testing connection…")
				return m, m.testConnection(serverURL)
			}
			var cmd bubbletea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		if m.filtering {
			switch key {
			case "esc":
				m.filtering = false
				m.filter = ""
			case "enter":
				m.filtering = false
			default:
				if key == "backspace" {
					characters := []rune(m.filter)
					if len(characters) > 0 {
						m.filter = string(characters[:len(characters)-1])
					}
				} else if len([]rune(key)) == 1 {
					m.filter += key
				}
			}
			return m, nil
		}
		switch m.mode {
		case menuMode:
			return m.updateMenu(key)
		case formMode:
			if key == "esc" {
				m.mode = menuMode
				m.values = nil
				m.form = 0
				m.status = ""
				return m, nil
			}
			if key == "enter" {
				return m.submitField()
			}
			var cmd bubbletea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		case resultMode:
			switch key {
			case "esc", "enter", "q":
				m.mode = menuMode
				m.status = ""
			case "up", "k":
				if m.resultPos > 0 {
					m.resultPos--
				}
			case "down", "j":
				if m.resultPos < max(0, len(m.result)-m.resultHeight()) {
					m.resultPos++
				}
			case "pgup":
				m.resultPos = max(0, m.resultPos-m.resultHeight())
			case "pgdown":
				m.resultPos = min(max(0, len(m.result)-m.resultHeight()), m.resultPos+m.resultHeight())
			}
			return m, nil
		case helpMode:
			m.mode = menuMode
			return m, nil
		}
	}
	return m, nil
}

func (m model) testConnection(serverURL string) bubbletea.Cmd {
	return func() bubbletea.Msg {
		client, err := api.NewWithLanguage(serverURL, m.language)
		if err != nil {
			return responseMsg{err: err}
		}
		client.SetUserAgent("citylist-cli/" + m.version)
		if err := client.SetAPIVersion(m.apiVersion); err != nil {
			return responseMsg{err: err}
		}
		if err := client.SetRequestPolicy(m.timeout, m.retries, m.retryDelay); err != nil {
			return responseMsg{err: err}
		}
		client.SetCredentials(m.token, m.language)
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		defer cancel()
		body, err := client.Get(ctx, "/api/v1/server/healthz")
		if err != nil {
			return responseMsg{err: err}
		}
		info := healthServerLabel(body, serverURL)
		if err == nil && m.token != "" {
			body, err = client.Get(ctx, "/api/v1/server/auth/check")
		}
		return responseMsg{body: body, info: info, err: err}
	}
}

func healthServerLabel(body []byte, fallback string) string {
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

func (m model) updateMenu(key string) (bubbletea.Model, bubbletea.Cmd) {
	visible := m.visibleCommands()
	switch key {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(visible)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = max(0, len(visible)-1)
	case "/":
		m.filtering = true
		m.filter = ""
		m.cursor = 0
	case "?":
		m.mode = helpMode
	case "enter", "right", "l":
		if len(visible) == 0 {
			break
		}
		selected := visible[m.cursor]
		if len(selected.fields) == 0 {
			m.values = nil
			return m, m.fetch(selected)
		}
		m.mode = formMode
		m.form = 0
		m.values = make([]string, 0, len(selected.fields))
		m.input.Reset()
		m.input.Placeholder = localized(m.language, fieldTranslationKey(selected.fields[0]), selected.fields[0])
		return m, m.input.Focus()
	}
	return m, nil
}

func (m model) submitField() (bubbletea.Model, bubbletea.Cmd) {
	visible := m.visibleCommands()
	if len(visible) == 0 || m.cursor >= len(visible) {
		m.mode = menuMode
		return m, nil
	}
	selected := visible[m.cursor]
	value := strings.TrimSpace(m.input.Value())
	field := selected.fields[m.form]
	if value == "" && !strings.HasSuffix(field, "(optional)") {
		m.status = i18n.TranslateFormat(m.language, "cli.tui_field_required", map[string]string{"field": localized(m.language, fieldTranslationKey(field), field)})
		return m, nil
	}
	if value != "" {
		m.values = append(m.values, value)
	} else {
		m.values = append(m.values, "")
	}
	m.form++
	if m.form == len(selected.fields) {
		m.mode = resultMode
		m.loading = true
		m.status = localized(m.language, "tui_loading", "Loading…")
		return m, m.fetch(selected)
	}
	m.input.Reset()
	m.input.Placeholder = localized(m.language, fieldTranslationKey(selected.fields[m.form]), selected.fields[m.form])
	m.status = ""
	return m, m.input.Focus()
}

func (m model) fetch(selected command) bubbletea.Cmd {
	values := append([]string(nil), m.values...)
	for len(values) > 0 && values[len(values)-1] == "" {
		values = values[:len(values)-1]
	}
	endpoint, err := api.EndpointWithLanguage(selected.name, values, m.language)
	if err != nil {
		return func() bubbletea.Msg { return responseMsg{err: err} }
	}
	return func() bubbletea.Msg {
		client, err := api.NewWithLanguage(m.serverURL, m.language)
		if err != nil {
			return responseMsg{err: err}
		}
		client.SetUserAgent("citylist-cli/" + m.version)
		if err := client.SetAPIVersion(m.apiVersion); err != nil {
			return responseMsg{err: err}
		}
		if err := client.SetRequestPolicy(m.timeout, m.retries, m.retryDelay); err != nil {
			return responseMsg{err: err}
		}
		client.SetCredentials(m.token, m.language)
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		defer cancel()
		body, err := client.GetWithAccept(ctx, endpoint, "application/json")
		return responseMsg{body: body, err: err}
	}
}

func (m model) visibleCommands() []command {
	if m.filter == "" {
		return commands
	}
	filter := strings.ToLower(m.filter)
	visible := make([]command, 0, len(commands))
	for _, item := range commands {
		label := localized(m.language, commandTranslationKey(item.name), item.label)
		if strings.Contains(strings.ToLower(label), filter) || strings.Contains(item.name, filter) {
			visible = append(visible, item)
		}
	}
	return visible
}

func (m model) View() bubbletea.View {
	view := bubbletea.NewView(m.render())
	view.AltScreen = true
	if m.mouse {
		view.MouseMode = bubbletea.MouseModeCellMotion
		view.OnMouse = func(message bubbletea.MouseMsg) bubbletea.Cmd {
			click, ok := message.(bubbletea.MouseClickMsg)
			if !ok || click.Button != bubbletea.MouseLeft || m.mode != menuMode {
				return nil
			}
			firstRow := 2
			if m.sizeMode > terminal.SizeModeMicro {
				firstRow++
			}
			visible := m.visibleCommands()
			start := 0
			capacity := max(1, m.height-8)
			if m.cursor >= capacity {
				start = m.cursor - capacity + 1
			}
			end := min(len(visible), start+capacity)
			index := start + click.Y - firstRow
			if click.Y < firstRow || index < start || index >= end {
				return nil
			}
			return func() bubbletea.Msg { return mouseSelectMsg{index: index} }
		}
	}
	return view
}

func (m model) render() string {
	dark := m.theme != "light"
	header, active, muted, errorStyle := themeStyles(dark, m.colors)
	width := min(100, max(20, m.width-4))
	lines := []string{}
	if m.sizeMode > terminal.SizeModeMicro {
		lines = append(lines, header.Width(width).Render(localized(m.language, "tui_header", "CITYLIST  ·  Interactive Client")))
	}
	switch m.mode {
	case setupMode:
		lines = append(lines, "", localized(m.language, "tui_setup_connect", "Connect to a Citylist server"), localized(m.language, "prompt_server", "Server URL"), m.input.View())
		if m.status != "" {
			lines = append(lines, errorStyle.Render(m.status))
		}
		lines = append(lines, "", muted.Render(localized(m.language, "tui_setup_hint", "Enter save · Ctrl+C quit")))
	case menuMode:
		lines = append(lines, "", localized(m.language, "tui_choose_operation", "Choose an operation"))
		visible := m.visibleCommands()
		capacity := max(1, m.height-8)
		start := 0
		if m.cursor >= capacity {
			start = m.cursor - capacity + 1
		}
		end := min(len(visible), start+capacity)
		for index := start; index < end; index++ {
			prefix := "  "
			label := localized(m.language, commandTranslationKey(visible[index].name), visible[index].label)
			if index == m.cursor {
				prefix = "› "
				label = active.Render(label)
			}
			lines = append(lines, prefix+label)
		}
		if m.filtering {
			lines = append(lines, "", i18n.TranslateFormat(m.language, "cli.tui_filter_active", map[string]string{"filter": m.filter}))
		} else if m.filter != "" {
			lines = append(lines, "", i18n.TranslateFormat(m.language, "cli.tui_filter_hint", map[string]string{"filter": m.filter}))
		}
		if m.sizeMode <= terminal.SizeModeMinimal {
			lines = append(lines, "", muted.Render(localized(m.language, "tui_menu_hint_small", "↑/↓ move · Enter select · q quit")))
		} else {
			lines = append(lines, "", muted.Render(localized(m.language, "tui_menu_hint", "↑/↓ or j/k navigate · Enter select · / search · ? help · q quit")))
		}
	case formMode:
		visible := m.visibleCommands()
		if len(visible) == 0 || m.cursor >= len(visible) {
			return strings.Join(lines, "\n")
		}
		selected := visible[m.cursor]
		lines = append(lines, "", active.Render(localized(m.language, commandTranslationKey(selected.name), selected.label)), i18n.TranslateFormat(m.language, "cli.tui_field_position", map[string]string{"current": fmt.Sprint(m.form + 1), "total": fmt.Sprint(len(selected.fields)), "field": localized(m.language, fieldTranslationKey(selected.fields[m.form]), selected.fields[m.form])}), "", m.input.View())
		if m.status != "" {
			lines = append(lines, errorStyle.Render(m.status))
		}
		lines = append(lines, "", muted.Render(localized(m.language, "tui_form_hint", "Enter continue · Esc back")))
	case resultMode:
		if m.status != "" {
			lines = append(lines, "", errorStyle.Render(m.status))
		}
		capacity := m.resultHeight()
		for _, line := range m.result[m.resultPos:min(len(m.result), m.resultPos+capacity)] {
			lines = append(lines, truncate(line, width))
		}
		lines = append(lines, "", muted.Render(localized(m.language, "tui_result_hint", "↑/↓ or j/k scroll · Enter or Esc back")))
	case helpMode:
		lines = append(lines, "", active.Render(localized(m.language, "tui_help_title", "Keyboard help")), "", localized(m.language, "tui_help_up", "↑ / k       Move up"), localized(m.language, "tui_help_down", "↓ / j       Move down"), localized(m.language, "tui_help_select", "Enter       Select or submit"), localized(m.language, "tui_help_back", "Esc         Go back"), localized(m.language, "tui_help_filter", "/           Filter operations"), localized(m.language, "tui_help_show", "?           Show this help"), localized(m.language, "tui_help_quit", "q / Ctrl+C  Exit"), "", muted.Render(localized(m.language, "tui_help_readonly", "All actions are read-only requests to the configured Citylist server.")))
	}
	result := strings.Join(lines, "\n")
	if !m.unicode {
		result = strings.NewReplacer("·", "|", "›", ">", "↑", "^", "↓", "v", "…", "...", "—", "-", "→", "->").Replace(result)
		var ascii strings.Builder
		ascii.Grow(len(result))
		for _, character := range result {
			if character < 128 {
				ascii.WriteRune(character)
			} else {
				ascii.WriteByte('?')
			}
		}
		result = ascii.String()
	}
	return result
}

type mouseSelectMsg struct{ index int }

func (m model) resultHeight() int {
	return max(1, m.height-7)
}

func themeStyles(dark, colors bool) (lipgloss.Style, lipgloss.Style, lipgloss.Style, lipgloss.Style) {
	if !colors {
		return lipgloss.NewStyle().Bold(true), lipgloss.NewStyle().Bold(true), lipgloss.NewStyle(), lipgloss.NewStyle()
	}
	palette := theme.TerminalPaletteLight
	if dark {
		palette = theme.TerminalPaletteDark
	}
	styles := StylesFromTerminalPalette(palette)
	return styles.Title, styles.Selected, styles.Muted, styles.Error
}

func truncate(value string, width int) string {
	if len(value) <= width || width < 4 {
		return value
	}
	return value[:width-3] + "..."
}

func localized(language, key, fallback string) string {
	if key == "" {
		return fallback
	}
	value := i18n.Translate(language, "cli."+key)
	if value == "cli."+key {
		return fallback
	}
	return value
}

func commandTranslationKey(name string) string {
	return "gui_command_" + name
}

func fieldTranslationKey(field string) string {
	switch field {
	case "City ID":
		return "prompt_city_id"
	case "ZIP code":
		return "prompt_zipcode"
	case "Query":
		return "prompt_search_query"
	case "Country code":
		return "prompt_country"
	case "Country code (optional)":
		return "prompt_country_optional"
	case "City or state":
		return "prompt_zip_search"
	case "State code":
		return "prompt_state"
	case "State code (optional)":
		return "prompt_state_optional"
	case "Page (optional)":
		return "prompt_page_optional"
	case "Limit (optional)":
		return "prompt_limit_optional"
	case "Latitude":
		return "prompt_latitude"
	case "Longitude":
		return "prompt_longitude"
	case "Radius in km":
		return "prompt_radius"
	default:
		return ""
	}
}
