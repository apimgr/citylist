package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/citylist/src/config"
)

var accessOutput struct {
	sync.Mutex
	file                               *os.File
	path, format, custom, rotate, keep string
	opened                             time.Time
	maxBytes                           int64
}

// ConfigureAccessLog opens the configured raw access log destination.
func ConfigureAccessLog(dir string, cfg config.LogsConfig) error {
	c := cfg.Access
	if c.Enabled != nil && !*c.Enabled {
		accessOutput.Lock()
		old := accessOutput.file
		accessOutput.file = nil
		accessOutput.Unlock()
		if old != nil {
			return old.Close()
		}
		return nil
	}
	name := c.Filename
	if name == "" {
		name = "access.log"
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid access log filename")
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	format := strings.ToLower(c.Format)
	if format == "" {
		format = "apache"
	}
	if format != "apache" && format != "nginx" && format != "json" && format != "custom" {
		_ = f.Close()
		return fmt.Errorf("unsupported access log format %q", format)
	}
	rotate := c.Rotate
	if rotate == "" {
		rotate = "monthly"
	}
	keep := c.Keep
	if keep == "" {
		keep = cfg.Defaults.Keep
	}
	if keep == "" {
		keep = "none"
	}
	maxBytes := parseLogSize(rotate)
	accessOutput.Lock()
	old := accessOutput.file
	accessOutput.file = f
	accessOutput.path = path
	accessOutput.format = format
	accessOutput.custom = cfg.Defaults.Custom
	accessOutput.rotate = rotate
	accessOutput.keep = keep
	accessOutput.opened = info.ModTime()
	accessOutput.maxBytes = maxBytes
	accessOutput.Unlock()
	if err = pruneAccessArchives(path, keep, time.Now().UTC()); err != nil {
		return err
	}
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func writeAccessLog(r *http.Request, status, size int, latency time.Duration) {
	accessOutput.Lock()
	defer accessOutput.Unlock()
	if accessOutput.file == nil {
		return
	}
	if err := rotateAccessLog(time.Now().UTC()); err != nil {
		return
	}
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	remote = sanitizeAccessField(remote)
	agent := sanitizeAccessField(r.UserAgent())
	referer := sanitizeAccessField(r.Referer())
	tm := time.Now().UTC()
	path := sanitizeAccessField(r.URL.EscapedPath())
	query := sanitizeAccessField(redactAccessQuery(r.URL.RawQuery))
	fields := map[string]any{"time": tm.Format(time.RFC3339Nano), "remote_ip": remote, "method": sanitizeAccessField(r.Method), "path": path, "query": query, "status": status, "bytes": size, "latency": latency.String(), "latency_ms": latency.Milliseconds(), "user_agent": agent, "referer": referer, "request_id": r.Header.Get("X-Request-ID"), "fqdn": sanitizeAccessField(r.Host), "protocol": sanitizeAccessField(r.Proto), "country": "", "asn": ""}
	if r.TLS != nil {
		fields["tls_version"] = fmt.Sprintf("0x%04x", r.TLS.Version)
	} else {
		fields["tls_version"] = ""
	}
	switch strings.ToLower(accessOutput.format) {
	case "json":
		_ = json.NewEncoder(accessOutput.file).Encode(map[string]any{"ip": remote, "time": tm.Format(time.RFC3339), "method": fields["method"], "path": path, "status": status, "size": size, "ua": agent})
	case "nginx":
		_, _ = fmt.Fprintf(accessOutput.file, "%s - - [%s] \"%s %s %s\" %d %d\n", remote, tm.Format("02/Jan/2006:15:04:05 -0700"), sanitizeAccessField(r.Method), sanitizeAccessField(r.URL.EscapedPath()), sanitizeAccessField(r.Proto), status, size)
	case "custom":
		line, err := formatCustomAccess(fields, accessOutput.custom)
		if err == nil {
			_, _ = accessOutput.file.Write(line)
		}
	default:
		_, _ = fmt.Fprintf(accessOutput.file, "%s - - [%s] \"%s %s %s\" %d %d \"%s\" \"%s\"\n", remote, tm.Format("02/Jan/2006:15:04:05 -0700"), sanitizeAccessField(r.Method), path, sanitizeAccessField(r.Proto), status, size, strings.ReplaceAll(referer, "\"", ""), strings.ReplaceAll(agent, "\"", ""))
	}
}

func formatCustomAccess(fields map[string]any, pattern string) ([]byte, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("access custom format requires server.logs.defaults.custom")
	}
	replacer := strings.NewReplacer("{time}", time.Now().Format("15:04:05"), "{date}", time.Now().Format("2006-01-02"), "{datetime}", fmt.Sprint(fields["time"]), "{remote_ip}", fmt.Sprint(fields["remote_ip"]), "{method}", fmt.Sprint(fields["method"]), "{path}", fmt.Sprint(fields["path"]), "{query}", fmt.Sprint(fields["query"]), "{status}", fmt.Sprint(fields["status"]), "{bytes}", fmt.Sprint(fields["bytes"]), "{latency}", fmt.Sprint(fields["latency"]), "{latency_ms}", fmt.Sprint(fields["latency_ms"]), "{user_agent}", fmt.Sprint(fields["user_agent"]), "{referer}", fmt.Sprint(fields["referer"]), "{request_id}", fmt.Sprint(fields["request_id"]), "{fqdn}", fmt.Sprint(fields["fqdn"]), "{protocol}", fmt.Sprint(fields["protocol"]), "{tls_version}", fmt.Sprint(fields["tls_version"]), "{country}", fmt.Sprint(fields["country"]), "{asn}", fmt.Sprint(fields["asn"]))
	return []byte(sanitizeAccessField(replacer.Replace(pattern)) + "\n"), nil
}

func parseLogSize(policy string) int64 {
	var max int64
	for _, part := range strings.Split(strings.ToUpper(policy), ",") {
		part = strings.TrimSpace(part)
		var n int64
		if _, err := fmt.Sscanf(part, "%dMB", &n); err == nil && n > 0 {
			max = n << 20
		}
		if _, err := fmt.Sscanf(part, "%dGB", &n); err == nil && n > 0 {
			max = n << 30
		}
	}
	return max
}

func rotateAccessLog(now time.Time) error {
	info, err := accessOutput.file.Stat()
	if err != nil {
		return err
	}
	rotate := accessOutput.maxBytes > 0 && info.Size() >= accessOutput.maxBytes
	opened := accessOutput.opened.UTC()
	for _, p := range strings.Split(strings.ToLower(accessOutput.rotate), ",") {
		switch strings.TrimSpace(p) {
		case "daily":
			rotate = rotate || now.YearDay() != opened.YearDay() || now.Year() != opened.Year()
		case "weekly":
			oy, ow := opened.ISOWeek()
			ny, nw := now.ISOWeek()
			rotate = rotate || oy != ny || ow != nw
		case "monthly":
			rotate = rotate || now.Year() != opened.Year() || now.Month() != opened.Month()
		case "yearly":
			rotate = rotate || now.Year() != opened.Year()
		}
	}
	if !rotate {
		return nil
	}
	_ = accessOutput.file.Close()
	if accessOutput.keep == "none" {
		_ = os.Remove(accessOutput.path)
	} else {
		arch := accessOutput.path + "." + now.Format("2006-01-02T150405Z")
		if err = os.Rename(accessOutput.path, arch); err != nil {
			return err
		}
		if err = pruneAccessArchives(accessOutput.path, accessOutput.keep, now); err != nil {
			return err
		}
	}
	accessOutput.file, err = os.OpenFile(accessOutput.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err == nil {
		accessOutput.opened = now
	}
	return err
}

func pruneAccessArchives(path, keep string, now time.Time) error {
	if strings.EqualFold(keep, "forever") {
		return nil
	}
	files, err := filepath.Glob(path + ".*")
	if err != nil {
		return err
	}
	sort.Strings(files)
	policy := strings.ToLower(strings.TrimSpace(keep))
	if index := strings.LastIndexByte(policy, ':'); index >= 0 {
		policy = policy[index+1:]
	}
	if n, e := strconv.Atoi(policy); e == nil {
		for len(files) > n {
			if e = os.Remove(files[0]); e != nil && !os.IsNotExist(e) {
				return e
			}
			files = files[1:]
		}
		return nil
	}
	if len(policy) < 2 {
		return nil
	}
	n, e := strconv.Atoi(policy[:len(policy)-1])
	if e != nil {
		return nil
	}
	var age time.Duration
	switch policy[len(policy)-1] {
	case 'd':
		age = time.Duration(n) * 24 * time.Hour
	case 'w':
		age = time.Duration(n) * 7 * 24 * time.Hour
	case 'm':
		age = time.Duration(n) * 30 * 24 * time.Hour
	default:
		return nil
	}
	for _, file := range files {
		info, e := os.Stat(file)
		if e != nil {
			return e
		}
		if now.Sub(info.ModTime()) > age {
			if e = os.Remove(file); e != nil {
				return e
			}
		}
	}
	return nil
}

func sanitizeAccessField(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
}

func redactAccessQuery(raw string) string {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "[redacted]"
	}
	for key := range values {
		switch strings.ToLower(key) {
		case "token", "session", "code", "key", "password", "secret", "auth", "pwd", "api_key", "apikey", "access_token", "refresh_token":
			for index := range values[key] {
				values[key][index] = "[redacted]"
			}
		}
	}
	return values.Encode()
}
