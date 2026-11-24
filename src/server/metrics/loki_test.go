package metrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLogStoreBoundsAndRedactsCredentials(t *testing.T) {
	store := NewLogStore(2, time.Hour)
	store.RegisterSecret("mtr_private_value")
	for i := 0; i < 3; i++ {
		record := slog.NewRecord(time.Now(), slog.LevelInfo, "credential mtr_private_value", 0)
		record.AddAttrs(slog.String("api_token", "mtr_private_value"), slog.String("detail", "bearer mtr_private_value"))
		store.append(record, map[string]any{"nested": map[string]any{"password": "hidden"}})
	}

	entries := store.Entries(10, time.Hour)
	if len(entries) != 2 {
		t.Fatalf("Entries() returned %d records, want 2", len(entries))
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal entries: %v", err)
	}
	if strings.Contains(string(encoded), "mtr_private_value") || !strings.Contains(string(encoded), "xxxxx") {
		t.Fatalf("credentials were not sanitized: %s", encoded)
	}

	streams := store.LokiStreams(1, time.Hour)
	streamData, err := json.Marshal(streams)
	if err != nil {
		t.Fatalf("marshal streams: %v", err)
	}
	if !strings.Contains(string(streamData), `"streams"`) || strings.Contains(string(streamData), "mtr_private_value") {
		t.Fatalf("Loki stream is malformed or contains a credential: %s", streamData)
	}
}

func TestTeeHandlerPreservesLogDelivery(t *testing.T) {
	store := NewLogStore(5, time.Hour)
	base := slog.NewTextHandler(discardWriter{}, nil)
	handler := &teeHandler{next: base, store: store}
	if err := handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelWarn, "event", 0)); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
	if entries := store.Entries(5, time.Hour); len(entries) != 1 || entries[0]["message"] != "event" {
		t.Fatalf("tee handler stored entries = %#v", entries)
	}
}

type discardWriter struct{}

func (discardWriter) Write(value []byte) (int, error) { return len(value), nil }
