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

// parseLine accepts exactly the grammar slog.TextHandler writes: attributes
// separated by a single space, each key=value, where a key or value is either
// bare (no space, quote, or '=') or a strconv.Quote literal, which is how the
// handler escapes anything else. A duplicate key is rejected rather than
// overwritten, so conflicting evidence in one record cannot go unnoticed.
func parseLine(line string) (Record, error) {
	record := Record{}
	for rest := line; ; {
		key, value, remainder, err := parseAttr(rest)
		if err != nil {
			return nil, fmt.Errorf("log line %q: %w", line, err)
		}
		if _, dup := record[key]; dup {
			return nil, fmt.Errorf("log line %q: duplicate key %q", line, key)
		}
		record[key] = value
		if remainder == "" {
			return record, nil
		}
		if remainder[0] != ' ' {
			return nil, fmt.Errorf("log line %q: missing separator before %q", line, remainder)
		}
		rest = remainder[1:]
	}
}

func parseAttr(s string) (key, value, rest string, err error) {
	key, rest, err = parseToken(s, '=')
	if err != nil {
		return "", "", "", fmt.Errorf("key: %w", err)
	}
	if !strings.HasPrefix(rest, "=") {
		return "", "", "", fmt.Errorf("key %q is not followed by '='", key)
	}
	value, rest, err = parseToken(rest[1:], ' ')
	if err != nil {
		return "", "", "", fmt.Errorf("value of key %q: %w", key, err)
	}
	return key, value, rest, nil
}

// parseToken leaves the terminator in rest so the caller validates what follows.
func parseToken(s string, terminator byte) (token, rest string, err error) {
	if strings.HasPrefix(s, `"`) {
		quoted, err := strconv.QuotedPrefix(s)
		if err != nil {
			return "", "", fmt.Errorf("malformed quoted token at %q: %w", s, err)
		}
		token, err = strconv.Unquote(quoted)
		return token, s[len(quoted):], err
	}
	end := strings.IndexAny(s, ` "=`)
	if end < 0 {
		end = len(s)
	} else if s[end] != terminator {
		return "", "", fmt.Errorf("unexpected %q in bare token at %q", s[end], s)
	}
	if end == 0 {
		return "", "", fmt.Errorf("empty bare token at %q", s)
	}
	return s[:end], s[end:], nil
}
