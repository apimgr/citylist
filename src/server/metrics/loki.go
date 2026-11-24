package metrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type logEntry struct {
	time    time.Time
	level   string
	message string
	attrs   map[string]any
}

// LogStore retains a bounded, sanitized stream of structured application logs.
type LogStore struct {
	mu         sync.RWMutex
	entries    []logEntry
	maxEntries int
	maxAge     time.Duration
	secrets    []string
}

// DefaultLogStore is the process-wide bounded log source for the Loki endpoint.
var DefaultLogStore = NewLogStore(1000, time.Hour)

// NewLogStore creates a log ring with bounded entry and age limits.
func NewLogStore(maxEntries int, maxAge time.Duration) *LogStore {
	store := &LogStore{}
	store.SetLimits(maxEntries, maxAge)
	return store
}

// SetLimits updates retention limits and removes entries outside the new bounds.
func (s *LogStore) SetLimits(maxEntries int, maxAge time.Duration) {
	if maxEntries < 1 {
		maxEntries = 1000
	}
	if maxEntries > 10000 {
		maxEntries = 10000
	}
	if maxAge <= 0 {
		maxAge = time.Hour
	}
	s.mu.Lock()
	s.maxEntries, s.maxAge = maxEntries, maxAge
	s.pruneLocked(time.Now())
	s.mu.Unlock()
}

// RegisterSecret adds a known credential to the sanitizer.
func (s *LogStore) RegisterSecret(secret string) {
	if secret == "" {
		return
	}
	s.mu.Lock()
	s.secrets = append(s.secrets, secret)
	s.mu.Unlock()
}

// Entries returns a bounded, newest-first-independent chronological snapshot.
func (s *LogStore) Entries(maxEntries int, maxAge time.Duration) []map[string]any {
	s.mu.Lock()
	s.pruneLocked(time.Now())
	entries := append([]logEntry(nil), s.entries...)
	secrets := append([]string(nil), s.secrets...)
	limit := s.maxEntries
	age := s.maxAge
	s.mu.Unlock()
	if maxEntries > 0 && maxEntries < limit {
		limit = maxEntries
	}
	if maxAge > 0 && maxAge < age {
		age = maxAge
	}
	cutoff := time.Now().Add(-age)
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	result := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if entry.time.Before(cutoff) {
			continue
		}
		attrs := make(map[string]any, len(entry.attrs))
		for key, value := range entry.attrs {
			attrs[key] = redactValue(key, value, secrets)
		}
		result = append(result, map[string]any{
			"timestamp":  entry.time.UTC().Format(time.RFC3339Nano),
			"level":      entry.level,
			"message":    redactText(entry.message, secrets),
			"attributes": attrs,
		})
	}
	return result
}

// LokiStreams returns a Loki push API stream payload from the recent log snapshot.
func (s *LogStore) LokiStreams(maxEntries int, maxAge time.Duration) map[string]any {
	entries := s.Entries(maxEntries, maxAge)
	values := make([][2]string, 0, len(entries))
	for _, entry := range entries {
		stamp, _ := time.Parse(time.RFC3339Nano, entry["timestamp"].(string))
		line, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		values = append(values, [2]string{strconvTimestamp(stamp), string(line)})
	}
	if len(values) == 0 {
		return map[string]any{"streams": []any{}}
	}
	return map[string]any{"streams": []any{map[string]any{
		"stream": map[string]string{"app": namespace, "source": "citylist"},
		"values": values,
	}}}
}

func strconvTimestamp(value time.Time) string { return strconv.FormatInt(value.UnixNano(), 10) }

func (s *LogStore) append(record slog.Record, attrs map[string]any) {
	level := strings.ToUpper(record.Level.String())
	entry := logEntry{time: record.Time, level: level, message: record.Message, attrs: attrs}
	if entry.time.IsZero() {
		entry.time = time.Now()
	}
	s.mu.Lock()
	entry.message = redactText(entry.message, s.secrets)
	for key, value := range entry.attrs {
		entry.attrs[key] = redactValue(key, value, s.secrets)
	}
	s.entries = append(s.entries, entry)
	s.pruneLocked(entry.time)
	s.mu.Unlock()
}

func (s *LogStore) pruneLocked(now time.Time) {
	cutoff := now.Add(-s.maxAge)
	first := 0
	for first < len(s.entries) && s.entries[first].time.Before(cutoff) {
		first++
	}
	if first > 0 {
		s.entries = append([]logEntry(nil), s.entries[first:]...)
	}
	if len(s.entries) > s.maxEntries {
		s.entries = append([]logEntry(nil), s.entries[len(s.entries)-s.maxEntries:]...)
	}
}

var sensitiveLogKey = regexp.MustCompile(`(?i)(token|password|secret|authorization|cookie|private.?key|credential)`)
var bearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/-]+=*`)
var inlineCredentialPattern = regexp.MustCompile(`(?i)((?:password|passwd|token|secret|api[_-]?key|access[_-]?key)=)[^&\s]+`)
var urlCredentialPattern = regexp.MustCompile(`(?i)(://)[^/@\s:]+:[^/@\s]+@`)

func redactValue(key string, value any, secrets []string) any {
	if sensitiveLogKey.MatchString(key) {
		return "xxxxx"
	}
	switch typed := value.(type) {
	case string:
		return redactText(typed, secrets)
	case map[string]any:
		copy := make(map[string]any, len(typed))
		for childKey, child := range typed {
			copy[childKey] = redactValue(childKey, child, secrets)
		}
		return copy
	case []any:
		copy := make([]any, len(typed))
		for index, child := range typed {
			copy[index] = redactValue(key, child, secrets)
		}
		return copy
	default:
		return value
	}
}

func redactText(value string, secrets []string) string {
	value = bearerPattern.ReplaceAllString(value, "${1}xxxxx")
	value = inlineCredentialPattern.ReplaceAllString(value, "${1}xxxxx")
	value = urlCredentialPattern.ReplaceAllString(value, "${1}xxxxx@")
	for _, secret := range secrets {
		value = strings.ReplaceAll(value, secret, "xxxxx")
	}
	return value
}

type teeHandler struct {
	next   slog.Handler
	store  *LogStore
	attrs  []slog.Attr
	groups []string
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *teeHandler) Handle(ctx context.Context, record slog.Record) error {
	attrs := make(map[string]any)
	for _, attr := range h.attrs {
		addLogAttr(attrs, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		addLogAttr(attrs, h.groups, attr)
		return true
	})
	h.store.append(record, attrs)
	return h.next.Handle(ctx, record)
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}

func addLogAttr(destination map[string]any, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindGroup {
		for _, child := range attr.Value.Group() {
			addLogAttr(destination, append(groups, attr.Key), child)
		}
		return
	}
	key := strings.Join(append(groups, attr.Key), ".")
	destination[key] = attr.Value.Any()
}

var installHandlerOnce sync.Once

// InstallLokiHandler tees structured process logs into the bounded Loki buffer.
func InstallLokiHandler() {
	installHandlerOnce.Do(func() {
		next := slog.Default().Handler()
		if reflect.TypeOf(next).String() == "*slog.defaultHandler" {
			next = slog.NewTextHandler(os.Stderr, nil)
		}
		slog.SetDefault(slog.New(&teeHandler{next: next, store: DefaultLogStore}))
	})
}
