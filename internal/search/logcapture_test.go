package search

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

type logRecord struct {
	Message string
	Attrs   map[string]any
}

// captureLogs redirects the default logger for the duration of one test and
// returns a reader for the records written so far. The default logger is
// global, so this is safe only because no test in this package runs in
// parallel.
func captureLogs(t *testing.T, level slog.Level) func() []logRecord {
	t.Helper()

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return func() []logRecord {
		var records []logRecord
		decoder := json.NewDecoder(bytes.NewReader(buf.Bytes()))
		for decoder.More() {
			raw := map[string]any{}
			if err := decoder.Decode(&raw); err != nil {
				t.Fatalf("failed to decode a captured log record: %v", err)
			}
			message, _ := raw["msg"].(string)
			delete(raw, "time")
			delete(raw, "level")
			delete(raw, "msg")
			records = append(records, logRecord{Message: message, Attrs: raw})
		}
		return records
	}
}

func requireLog(t *testing.T, records []logRecord, message string) logRecord {
	t.Helper()
	found := make([]string, 0, len(records))
	for _, record := range records {
		if record.Message == message {
			return record
		}
		found = append(found, record.Message)
	}
	t.Fatalf("no log record with message %q; captured %v", message, found)
	return logRecord{}
}

func requireNoLog(t *testing.T, records []logRecord, message string) {
	t.Helper()
	for _, record := range records {
		if record.Message == message {
			t.Fatalf("unexpected log record with message %q", message)
		}
	}
}
