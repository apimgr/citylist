package main

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/citylist/src/config"
)

type configuredLogs struct {
	mu        sync.Mutex
	dir       string
	settings  config.LogsConfig
	files     map[string]*logDestination
	level     slog.Level
	auditHash string
}

var activeLogs struct {
	sync.RWMutex
	value *configuredLogs
}

type logDestination struct {
	file                       *os.File
	path, rotate, keep, format string
	opened                     time.Time
	maxBytes                   int64
	compress                   bool
}

func configureLogs(dir, mode string, cfg config.LogsConfig) error {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	l := &configuredLogs{dir: dir, settings: cfg, files: make(map[string]*logDestination), level: slog.LevelInfo}
	level := strings.ToLower(cfg.Level)
	if level == "" {
		if mode == "development" || mode == "debug" {
			level = "debug"
		} else {
			level = "info"
		}
	}
	switch level {
	case "debug":
		l.level = slog.LevelDebug
	case "warn", "warning":
		l.level = slog.LevelWarn
	case "error":
		l.level = slog.LevelError
	}
	for _, kind := range []string{"server", "error", "app", "auth", "audit", "security", "debug"} {
		file, err := l.open(kind)
		if err != nil {
			l.close()
			return err
		}
		if file != nil {
			l.files[kind] = file
		}
	}
	if audit := l.files["audit"]; audit != nil {
		var err error
		l.auditHash, err = lastAuditHash(audit.path)
		if err != nil {
			l.close()
			return err
		}
	}
	slog.SetDefault(slog.New(&configuredLogHandler{logs: l}))
	activeLogs.Lock()
	previous := activeLogs.value
	activeLogs.value = l
	activeLogs.Unlock()
	if previous != nil {
		previous.close()
	}
	log.SetFlags(0)
	log.SetOutput(slogWriter{})
	return nil
}

func rotateConfiguredLogs() error {
	activeLogs.RLock()
	logs := activeLogs.value
	activeLogs.RUnlock()
	if logs == nil {
		return errors.New("application logs are not configured")
	}
	logs.mu.Lock()
	defer logs.mu.Unlock()
	for name, destination := range logs.files {
		if err := destination.write(nil); err != nil {
			return fmt.Errorf("rotate %s log: %w", name, err)
		}
	}
	return nil
}

type slogWriter struct{}

func (slogWriter) Write(p []byte) (int, error) {
	slog.Info(strings.TrimSpace(string(p)))
	return len(p), nil
}

func (l *configuredLogs) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, f := range l.files {
		_ = f.file.Close()
	}
	l.files = make(map[string]*logDestination)
}

func (l *configuredLogs) kind(name string) config.LogKindConfig {
	switch name {
	case "server":
		return l.settings.Server
	case "error":
		return l.settings.Error
	case "app":
		return l.settings.App
	case "auth":
		return l.settings.Auth
	case "audit":
		return l.settings.Audit.LogKindConfig
	case "security":
		return l.settings.Security
	case "debug":
		return l.settings.Debug
	}
	return config.LogKindConfig{}
}

func (l *configuredLogs) open(kind string) (*logDestination, error) {
	c := l.kind(kind)
	if c.Enabled != nil && !*c.Enabled {
		return nil, nil
	}
	name := c.Filename
	if name == "" {
		name = kind + ".log"
	}
	if filepath.Base(name) != name {
		return nil, fmt.Errorf("invalid %s log filename", kind)
	}
	path := filepath.Join(l.dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0640); err != nil {
		_ = f.Close()
		return nil, err
	}
	rotate := c.Rotate
	if rotate == "" {
		rotate = l.settings.Defaults.Rotate
	}
	if kind == "access" && c.Rotate == "" {
		rotate = "monthly"
	}
	if kind == "audit" && c.Rotate == "" {
		rotate = "daily"
	}
	keep := c.Keep
	if keep == "" {
		keep = l.settings.Defaults.Keep
	}
	if !validRotationPolicy(rotate) {
		_ = f.Close()
		return nil, fmt.Errorf("invalid %s log rotation policy %q", kind, rotate)
	}
	if !validRetentionPolicy(keep) {
		_ = f.Close()
		return nil, fmt.Errorf("invalid %s log retention policy %q", kind, keep)
	}
	var max int64
	for _, part := range strings.Split(strings.ToUpper(rotate), ",") {
		part = strings.TrimSpace(part)
		var n int64
		if _, scanErr := fmt.Sscanf(part, "%dMB", &n); scanErr == nil && n > 0 {
			max = n << 20
		}
		if _, scanErr := fmt.Sscanf(part, "%dGB", &n); scanErr == nil && n > 0 {
			max = n << 30
		}
	}
	format := c.Format
	if format == "" {
		switch kind {
		case "app":
			format = "logfmt"
		case "auth":
			format = "syslog"
		case "security":
			format = "fail2ban"
		default:
			format = "text"
		}
	}
	valid := map[string]map[string]bool{"server": {"text": true, "json": true}, "error": {"text": true, "json": true}, "app": {"logfmt": true, "json": true}, "auth": {"syslog": true, "json": true}, "audit": {"json": true}, "security": {"fail2ban": true, "syslog": true, "cef": true, "json": true, "text": true}, "debug": {"text": true, "json": true}}
	if !valid[kind][format] {
		_ = f.Close()
		return nil, fmt.Errorf("unsupported %s log format %q", kind, format)
	}
	if format == "custom" && strings.TrimSpace(l.settings.Defaults.Custom) == "" {
		_ = f.Close()
		return nil, errors.New("custom log format requires server.logs.defaults.custom")
	}
	compress := kind == "audit" && l.settings.Audit.Compress
	info, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return nil, statErr
	}
	ret := &logDestination{file: f, path: path, rotate: rotate, keep: keep, format: format, opened: info.ModTime(), maxBytes: max, compress: compress}
	if err = ret.pruneArchives(time.Now().UTC()); err != nil {
		_ = f.Close()
		return nil, err
	}
	return ret, nil
}

type configuredLogHandler struct {
	logs   *configuredLogs
	attrs  []slog.Attr
	groups []string
}

func (h *configuredLogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.logs.level
}
func (h *configuredLogHandler) WithAttrs(a []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr{}, h.attrs...), a...)
	return &c
}
func (h *configuredLogHandler) WithGroup(g string) slog.Handler {
	c := *h
	c.groups = append(append([]string{}, h.groups...), g)
	return &c
}
func (h *configuredLogHandler) Handle(_ context.Context, r slog.Record) error {
	data := map[string]any{"time": r.Time.UTC().Format(time.RFC3339Nano), "level": strings.ToUpper(r.Level.String()), "msg": r.Message}
	for _, a := range h.attrs {
		data[a.Key] = a.Value.Any()
	}
	r.Attrs(func(a slog.Attr) bool { data[a.Key] = a.Value.Any(); return true })
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	kind := "app"
	if strings.HasPrefix(r.Message, "security.") {
		kind = "security"
	} else if strings.HasPrefix(r.Message, "auth.") {
		kind = "auth"
	} else if r.Level >= slog.LevelError {
		kind = "error"
	} else if r.Level < slog.LevelInfo {
		kind = "debug"
	}
	h.logs.mu.Lock()
	defer h.logs.mu.Unlock()
	targets := []string{"server", kind}
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[target] {
			continue
		}
		seen[target] = true
		if f := h.logs.files[target]; f != nil {
			var line []byte
			var e error
			if f.format == "custom" {
				line, e = formatCustomLog(data, h.logs.settings.Defaults.Custom)
			} else if target == "security" {
				line, e = formatSecurityLog(data, f.format)
			} else {
				line, e = formatLog(data, f.format)
			}
			if e != nil {
				return e
			}
			if err = f.write(line); err != nil {
				return err
			}
		}
	}
	_, err = os.Stderr.Write(b)
	return err
}

func formatLog(fields map[string]any, format string) ([]byte, error) {
	if format == "json" {
		b, err := json.Marshal(fields)
		return append(b, '\n'), err
	}
	stamp := cleanLogText(fmt.Sprint(fields["time"]))
	level := cleanLogText(fmt.Sprint(fields["level"]))
	msg := cleanLogText(fmt.Sprint(fields["msg"]))
	if format == "custom" {
		return formatCustomLog(fields, "{datetime} [{level}] {msg}")
	}
	if format == "text" {
		return []byte(fmt.Sprintf("%s [%s] %s\n", stamp, level, msg)), nil
	}
	if format == "fail2ban" {
		return []byte(fmt.Sprintf("%s [security] %s\n", stamp, msg)), nil
	}
	if format == "syslog" {
		hostname, _ := os.Hostname()
		return []byte(fmt.Sprintf("%s %s citylist[%d]: %s\n", time.Now().Format("Jan _2 15:04:05"), cleanLogText(hostname), os.Getpid(), strings.ToLower(level)+": "+msg)), nil
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		value := fmt.Sprint(fields[key])
		if strings.ContainsAny(value, " =\t\n\"") {
			value = strconv.Quote(value)
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
	}
	b.WriteByte('\n')
	return []byte(b.String()), nil
}

func formatSecurityLog(fields map[string]any, format string) ([]byte, error) {
	switch format {
	case "cef":
		severity := 5
		if fmt.Sprint(fields["level"]) == "ERROR" {
			severity = 8
		} else if fmt.Sprint(fields["level"]) == "WARN" {
			severity = 6
		}
		escape := strings.NewReplacer("\\", "\\\\", "|", "\\|", "=", "\\=", "\n", " ")
		message := escape.Replace(cleanLogText(fmt.Sprint(fields["msg"])))
		keys := make([]string, 0, len(fields))
		for key := range fields {
			if key != "time" && key != "level" && key != "msg" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		var extensions strings.Builder
		for _, key := range keys {
			fmt.Fprintf(&extensions, " %s=%s", key, escape.Replace(cleanLogText(fmt.Sprint(fields[key]))))
		}
		line := fmt.Sprintf("CEF:0|Citylist|citylist|1|security|%s|%d|%s\n", message, severity, strings.TrimSpace(extensions.String()))
		return []byte(line), nil
	case "syslog":
		hostname, _ := os.Hostname()
		message := cleanLogText(fmt.Sprint(fields["msg"]))
		line := fmt.Sprintf("<%d>1 %s %s citylist - - - %s\n", securitySyslogPriority(fmt.Sprint(fields["level"])), fmt.Sprint(fields["time"]), cleanLogText(hostname), message)
		return []byte(line), nil
	case "fail2ban":
		line := fmt.Sprintf("%s [security] %s\n", cleanLogText(fmt.Sprint(fields["time"])), cleanLogText(fmt.Sprint(fields["msg"])))
		return []byte(line), nil
	default:
		return formatLog(fields, format)
	}
}

func securitySyslogPriority(level string) int {
	switch level {
	case "ERROR":
		return 131
	case "WARN":
		return 132
	default:
		return 134
	}
}

func (d *logDestination) write(line []byte) error {
	info, err := d.file.Stat()
	if err != nil {
		return err
	}
	rotate := d.maxBytes > 0 && info.Size()+int64(len(line)) > d.maxBytes
	if !rotate {
		rotate = d.timeRotationDue(time.Now().UTC())
	}
	if rotate {
		_ = d.file.Close()
		if d.keep == "none" || d.keep == "" {
			if err = os.Remove(d.path); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else {
			archive := d.path + "." + time.Now().UTC().Format("2006-01-02T150405.000000000Z")
			if err = os.Rename(d.path, archive); err != nil {
				return err
			}
			if d.compress {
				if err = compressLog(archive); err != nil {
					return err
				}
			}
			if err = d.pruneArchives(time.Now().UTC()); err != nil {
				return err
			}
		}
		d.file, err = os.OpenFile(d.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
		if err != nil {
			return err
		}
		d.opened = time.Now()
	}
	_, err = d.file.Write(line)
	return err
}

func (d *logDestination) timeRotationDue(now time.Time) bool {
	policy := strings.ToLower(d.rotate)
	for _, part := range strings.Split(policy, ",") {
		part = strings.TrimSpace(part)
		switch part {
		case "daily":
			if !now.Truncate(24 * time.Hour).Equal(d.opened.UTC().Truncate(24 * time.Hour)) {
				return true
			}
		case "weekly":
			oy, ow := d.opened.UTC().ISOWeek()
			ny, nw := now.ISOWeek()
			if oy != ny || ow != nw {
				return true
			}
		case "monthly":
			if now.Year() != d.opened.UTC().Year() || now.Month() != d.opened.UTC().Month() {
				return true
			}
		case "yearly":
			if now.Year() != d.opened.UTC().Year() {
				return true
			}
		}
	}
	return false
}

func compressLog(path string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(path+".gz", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	writer := gzip.NewWriter(out)
	_, copyErr := io.Copy(writer, in)
	closeErr := writer.Close()
	fileErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if fileErr != nil {
		return fileErr
	}
	return os.Remove(path)
}

func (d *logDestination) pruneArchives(now time.Time) error {
	matches, err := filepath.Glob(d.path + ".*")
	if err != nil {
		return err
	}
	sort.Strings(matches)
	if strings.EqualFold(strings.TrimSpace(d.keep), "none") {
		for _, path := range matches {
			if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}
	if strings.EqualFold(d.keep, "forever") {
		return nil
	}
	policy := strings.ToLower(strings.TrimSpace(d.keep))
	if i := strings.LastIndexByte(policy, ':'); i >= 0 {
		policy = policy[i+1:]
	}
	if count, e := strconv.Atoi(policy); e == nil {
		for len(matches) > count {
			if err = os.Remove(matches[0]); err != nil && !os.IsNotExist(err) {
				return err
			}
			matches = matches[1:]
		}
		return nil
	}
	if len(policy) < 2 {
		return nil
	}
	amount, e := strconv.Atoi(policy[:len(policy)-1])
	if e != nil || amount < 0 {
		return nil
	}
	var age time.Duration
	switch policy[len(policy)-1] {
	case 'd':
		age = time.Duration(amount) * 24 * time.Hour
	case 'w':
		age = time.Duration(amount) * 7 * 24 * time.Hour
	case 'm':
		age = time.Duration(amount) * 30 * 24 * time.Hour
	default:
		return nil
	}
	for _, path := range matches {
		info, e := os.Stat(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if now.Sub(info.ModTime()) > age {
			if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
	}
	return nil
}

func validRotationPolicy(policy string) bool {
	if strings.TrimSpace(policy) == "" {
		return false
	}
	for _, part := range strings.Split(strings.ToLower(policy), ",") {
		part = strings.TrimSpace(part)
		switch part {
		case "never", "daily", "weekly", "monthly", "yearly":
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.ToUpper(part), "%dMB", &n); err == nil && n > 0 {
			continue
		}
		if _, err := fmt.Sscanf(strings.ToUpper(part), "%dGB", &n); err == nil && n > 0 {
			continue
		}
		return false
	}
	return true
}

func validRetentionPolicy(policy string) bool {
	p := strings.ToLower(strings.TrimSpace(policy))
	if p == "none" || p == "forever" {
		return true
	}
	if strings.HasPrefix(p, "weekly:") || strings.HasPrefix(p, "monthly:") {
		p = p[strings.IndexByte(p, ':')+1:]
	}
	if len(p) < 1 {
		return false
	}
	if _, err := strconv.Atoi(p); err == nil {
		return true
	}
	if len(p) < 2 {
		return false
	}
	n, err := strconv.Atoi(p[:len(p)-1])
	if err != nil || n < 0 {
		return false
	}
	return strings.ContainsRune("dwm", rune(p[len(p)-1]))
}

func formatCustomLog(fields map[string]any, pattern string) ([]byte, error) {
	replacer := strings.NewReplacer("{time}", time.Now().Format("15:04:05"), "{date}", time.Now().Format("2006-01-02"), "{datetime}", fmt.Sprint(fields["time"]), "{level}", fmt.Sprint(fields["level"]), "{msg}", fmt.Sprint(fields["msg"]), "{remote_ip}", fmt.Sprint(fields["remote_ip"]), "{method}", fmt.Sprint(fields["method"]), "{path}", fmt.Sprint(fields["path"]), "{query}", fmt.Sprint(fields["query"]), "{status}", fmt.Sprint(fields["status"]), "{bytes}", fmt.Sprint(fields["bytes"]), "{latency}", fmt.Sprint(fields["latency"]), "{latency_ms}", fmt.Sprint(fields["latency_ms"]), "{user_agent}", fmt.Sprint(fields["user_agent"]), "{referer}", fmt.Sprint(fields["referer"]), "{request_id}", fmt.Sprint(fields["request_id"]), "{fqdn}", fmt.Sprint(fields["fqdn"]), "{protocol}", fmt.Sprint(fields["protocol"]), "{tls_version}", fmt.Sprint(fields["tls_version"]), "{country}", fmt.Sprint(fields["country"]), "{asn}", fmt.Sprint(fields["asn"]))
	return []byte(cleanLogText(replacer.Replace(pattern)) + "\n"), nil
}

func cleanLogText(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
}

func effectiveAuditRetention(compliance config.ComplianceConfig, configured string) string {
	requirements := []struct {
		enabled bool
		months  int
	}{{compliance.GDPR, 12}, {compliance.CCPA, 12}, {compliance.HIPAA, 72}, {compliance.SOC2, 12}, {compliance.PCIDSS, 12}, {compliance.ISO27001, 36}}
	required := 0
	for _, r := range requirements {
		if r.enabled && r.months > required {
			required = r.months
		}
	}
	if required == 0 {
		return configured
	}
	existing := retentionMonths(configured)
	if existing >= required {
		return configured
	}
	return strconv.Itoa(required) + "m"
}

func retentionMonths(policy string) int {
	p := strings.ToLower(strings.TrimSpace(policy))
	if p == "forever" {
		return int(^uint(0) >> 1)
	}
	if len(p) < 2 {
		return 0
	}
	n, err := strconv.Atoi(p[:len(p)-1])
	if err != nil {
		return 0
	}
	switch p[len(p)-1] {
	case 'm':
		return n
	case 'y':
		return n * 12
	case 'w':
		return n / 4
	case 'd':
		return n / 30
	}
	return 0
}

type auditRecord struct {
	ID       string            `json:"id"`
	Time     string            `json:"time"`
	Event    string            `json:"event"`
	Category string            `json:"category"`
	Severity string            `json:"severity"`
	Actor    map[string]string `json:"actor"`
	Target   map[string]string `json:"target,omitempty"`
	Details  map[string]any    `json:"details,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	Source   string            `json:"source"`
	Result   string            `json:"result"`
	Checksum string            `json:"checksum,omitempty"`
}

func writeAuditEvent(event, severity, result, source, ip string, target map[string]string, details map[string]any) error {
	activeLogs.RLock()
	l := activeLogs.value
	activeLogs.RUnlock()
	if l == nil {
		return nil
	}
	dest := l.files["audit"]
	if dest == nil {
		return nil
	}
	if l.settings.Audit.Enabled != nil && !*l.settings.Audit.Enabled {
		return nil
	}
	switch eventCategory(event) {
	case "security":
		if !l.settings.Audit.Events.Security {
			return nil
		}
	case "config":
		if !l.settings.Audit.Events.Configuration {
			return nil
		}
	case "backup":
		if !l.settings.Audit.Events.Backup {
			return nil
		}
	case "server":
		if !l.settings.Audit.Events.Server {
			return nil
		}
	}
	now := time.Now().UTC()
	id, err := newAuditULID(now)
	if err != nil {
		return err
	}
	record := auditRecord{ID: id, Time: now.Format("2006-01-02T15:04:05.000Z"), Event: event, Category: eventCategory(event), Severity: severity, Actor: map[string]string{"ip": ip, "identity": source}, Target: target, Details: redactAuditDetails(details), Source: source, Result: result}
	l.mu.Lock()
	defer l.mu.Unlock()
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(append([]byte(l.auditHash), payload...))
	record.Checksum = hex.EncodeToString(hash[:])
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if err = dest.write(line); err != nil {
		return err
	}
	l.auditHash = record.Checksum
	return nil
}

func lastAuditHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", nil
	}
	tail := []byte(lines[len(lines)-1])
	var last struct {
		Checksum string `json:"checksum"`
	}
	if err = json.Unmarshal(tail, &last); err != nil {
		return "", fmt.Errorf("read audit log integrity tail: %w", err)
	}
	if last.Checksum != "" {
		return last.Checksum, nil
	}
	hash := sha256.Sum256(tail)
	return hex.EncodeToString(hash[:]), nil
}

func eventCategory(event string) string {
	if i := strings.IndexByte(event, '.'); i > 0 {
		return event[:i]
	}
	return "system"
}

var auditEmailPattern = regexp.MustCompile(`(?i)([a-z0-9._%+-])[a-z0-9._%+-]*@([a-z0-9.-]+\.[a-z]{2,})`)

func redactAuditDetails(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "private_key") {
			out[key] = "[REDACTED]"
			continue
		}
		out[key] = maskAuditEmails(value)
	}
	return out
}
func maskAuditEmails(value any) any {
	switch v := value.(type) {
	case string:
		return auditEmailPattern.ReplaceAllString(v, "$1***@$2")
	case map[string]any:
		return redactAuditDetails(v)
	case []any:
		items := make([]any, len(v))
		for i, item := range v {
			items[i] = maskAuditEmails(item)
		}
		return items
	default:
		return value
	}
}

func newAuditULID(t time.Time) (string, error) {
	var raw [16]byte
	ms := uint64(t.UnixMilli())
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	if _, err := rand.Read(raw[6:]); err != nil {
		return "", err
	}
	encoded := base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding).EncodeToString(raw[:])
	return strings.Repeat("0", 26-len(encoded)) + encoded, nil
}
