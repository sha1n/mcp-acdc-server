package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

// logRecord is one captured slog record, flattened to its message and attrs.
type logRecord struct {
	Level   string
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
			record := logRecord{
				Level:   asString(raw["level"]),
				Message: asString(raw["msg"]),
				Attrs:   map[string]any{},
			}
			for key, value := range raw {
				switch key {
				case "time", "level", "msg":
					continue
				default:
					record.Attrs[key] = value
				}
			}
			records = append(records, record)
		}
		return records
	}
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

// findLog returns the first captured record with the given message.
func findLog(records []logRecord, message string) (logRecord, bool) {
	for _, record := range records {
		if record.Message == message {
			return record, true
		}
	}
	return logRecord{}, false
}

func requireLog(t *testing.T, records []logRecord, message string) logRecord {
	t.Helper()
	record, ok := findLog(records, message)
	if !ok {
		t.Fatalf("no log record with message %q; captured %v", message, messages(records))
	}
	return record
}

func messages(records []logRecord) []string {
	found := make([]string, 0, len(records))
	for _, record := range records {
		found = append(found, record.Message)
	}
	return found
}
