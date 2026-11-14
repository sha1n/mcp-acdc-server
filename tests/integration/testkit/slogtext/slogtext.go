// Package slogtext parses the output of slog's TextHandler so tests can assert
// on individual records rather than on substrings of a whole log.
package slogtext

import (
	"fmt"
	"strconv"
	"strings"
)

// Record maps each attribute key of one log line to its unquoted value.
type Record map[string]string

// Parse returns one Record per non-empty line of data.
func Parse(data []byte) ([]Record, error) {
	var records []Record
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			continue
		}
		record, err := parseLine(line)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// Filter returns the records whose attributes include every pair in match.
func Filter(records []Record, match Record) []Record {
	var matched []Record
	for _, record := range records {
		if record.has(match) {
			matched = append(matched, record)
		}
	}
	return matched
}

func (r Record) has(match Record) bool {
	for key, value := range match {
		if got, ok := r[key]; !ok || got != value {
			return false
		}
	}
	return true
}

func parseLine(line string) (Record, error) {
	record := Record{}
	for rest := line; rest != ""; {
		key, valueAndRest, ok := strings.Cut(rest, "=")
		if !ok {
			return nil, fmt.Errorf("attribute without a value in log line %q", line)
		}
		value, remainder, err := parseValue(valueAndRest)
		if err != nil {
			return nil, fmt.Errorf("log line %q: %w", line, err)
		}
		record[key] = value
		rest = strings.TrimPrefix(remainder, " ")
	}
	return record, nil
}

func parseValue(s string) (value, rest string, err error) {
	if !strings.HasPrefix(s, `"`) {
		value, rest, _ = strings.Cut(s, " ")
		return value, rest, nil
	}
	quoted, err := strconv.QuotedPrefix(s)
	if err != nil {
		return "", "", err
	}
	value, err = strconv.Unquote(quoted)
	return value, s[len(quoted):], err
}
