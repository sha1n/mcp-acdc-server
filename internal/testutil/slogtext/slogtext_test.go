package slogtext

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func textHandlerOutput(t *testing.T, log func(*slog.Logger)) []byte {
	t.Helper()
	var buf bytes.Buffer
	log(slog.New(slog.NewTextHandler(&buf, nil)))
	return buf.Bytes()
}

func TestParse_RoundTripsTextHandlerRecords(t *testing.T) {
	data := textHandlerOutput(t, func(logger *slog.Logger) {
		logger.Info("plain", "k", "v")
		logger.Error("needs quoting", "error", errors.New(`a "quoted" = value`), "n", 7)
	})

	records, err := Parse(data)

	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "INFO", records[0]["level"])
	assert.Equal(t, "plain", records[0]["msg"])
	assert.Equal(t, "v", records[0]["k"])
	assert.Equal(t, "ERROR", records[1]["level"])
	assert.Equal(t, "needs quoting", records[1]["msg"])
	assert.Equal(t, `a "quoted" = value`, records[1]["error"])
	assert.Equal(t, "7", records[1]["n"])
}

func TestParse_DecodesEveryTextHandlerQuotingForm(t *testing.T) {
	tests := []struct {
		name  string
		log   func(*slog.Logger)
		key   string
		value string
	}{
		{name: "quoted key with space", log: func(l *slog.Logger) { l.Info("m", "key with space", "v") }, key: "key with space", value: "v"},
		{name: "quoted key with equals and space", log: func(l *slog.Logger) { l.Info("m", "key=with space", "v") }, key: "key=with space", value: "v"},
		{name: "quoted key with equals", log: func(l *slog.Logger) { l.Info("m", "a=b", "v") }, key: "a=b", value: "v"},
		{name: "quoted key with quote", log: func(l *slog.Logger) { l.Info("m", `k"q`, "v") }, key: `k"q`, value: "v"},
		{name: "value with spaces", log: func(l *slog.Logger) { l.Info("m", "k", "one two  three") }, key: "k", value: "one two  three"},
		{name: "value with escaped quotes", log: func(l *slog.Logger) { l.Info("m", "k", `say "hi"`) }, key: "k", value: `say "hi"`},
		{name: "value with escaped newline", log: func(l *slog.Logger) { l.Info("m", "k", "line1\nline2") }, key: "k", value: "line1\nline2"},
		{name: "value with equals", log: func(l *slog.Logger) { l.Info("m", "k", "a=b") }, key: "k", value: "a=b"},
		{name: "value with backslash", log: func(l *slog.Logger) { l.Info("m", "k", `C:\dir`) }, key: "k", value: `C:\dir`},
		{name: "empty value", log: func(l *slog.Logger) { l.Info("m", "k", "") }, key: "k", value: ""},
		{name: "grouped key", log: func(l *slog.Logger) { l.Info("m", slog.Group("g", "k", "v")) }, key: "g.k", value: "v"},
		{name: "nested group from WithGroup", log: func(l *slog.Logger) { l.WithGroup("outer").Info("m", slog.Group("inner", "k", "v")) }, key: "outer.inner.k", value: "v"},
		{name: "grouped key with space", log: func(l *slog.Logger) { l.Info("m", slog.Group("my group", "k", "v w")) }, key: "my group.k", value: "v w"},
		{name: "message with spaces and equals", log: func(l *slog.Logger) { l.Info("a b=c") }, key: "msg", value: "a b=c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records, err := Parse(textHandlerOutput(t, tt.log))

			require.NoError(t, err)
			require.Len(t, records, 1)
			assert.Contains(t, records[0], tt.key)
			assert.Equal(t, tt.value, records[0][tt.key])
		})
	}
}

func TestParse_DecodesSeveralQuotedAttributesInOneRecord(t *testing.T) {
	data := textHandlerOutput(t, func(l *slog.Logger) {
		l.Info("first msg", "key=with space", "value one", "plain", "x", "", "empty key")
	})

	records, err := Parse(data)

	require.NoError(t, err)
	require.Len(t, records, 1)
	record := records[0]
	assert.Equal(t, "first msg", record["msg"])
	assert.Equal(t, "value one", record["key=with space"])
	assert.Equal(t, "x", record["plain"])
	assert.Equal(t, "empty key", record[""])
	assert.Len(t, record, 6)
}

func TestParse_RejectsDuplicateKeyInOneRecord(t *testing.T) {
	data := textHandlerOutput(t, func(l *slog.Logger) {
		l.Error("Identity self-check failed", "identity", "wrong", "identity", "expected")
	})

	records, err := Parse(data)

	require.Error(t, err)
	assert.Nil(t, records)
	assert.Contains(t, err.Error(), `"identity"`)
}

func TestParse_AllowsSameKeyInDifferentRecords(t *testing.T) {
	data := textHandlerOutput(t, func(l *slog.Logger) {
		l.Info("one", "k", "a")
		l.Info("two", "k", "b")
	})

	records, err := Parse(data)

	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "a", records[0]["k"])
	assert.Equal(t, "b", records[1]["k"])
}

// Malformed inputs are hand-written because slog.TextHandler cannot produce them.
func TestParse_RejectsMalformedLines(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "attribute without value", data: "level=INFO msg\n"},
		{name: "unterminated quoted value", data: "msg=\"open\n"},
		{name: "unterminated quoted key", data: "\"open=v\n"},
		{name: "missing separator after quoted value", data: "msg=\"one\"junk=value\n"},
		{name: "missing equals after quoted key", data: "\"k\" =v\n"},
		{name: "missing equals after quoted key at end", data: "\"k\"\n"},
		{name: "double separator", data: "a=b  c=d\n"},
		{name: "trailing separator", data: "a=b \n"},
		{name: "leading separator", data: " a=b\n"},
		{name: "quote inside unquoted key", data: "a\"b=c\n"},
		{name: "quote inside unquoted value", data: "a=b\"c\n"},
		{name: "equals inside unquoted value", data: "a==b\n"},
		{name: "space inside unquoted key", data: "a b=c\n"},
		{name: "empty unquoted key", data: "=v\n"},
		{name: "invalid escape in quoted value", data: "a=\"\\q\"\n"},
		{name: "duplicate key", data: "identity=wrong identity=expected\n"},
		{name: "malformed line after a valid one", data: "a=b\nmsg=\"one\"junk=value\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records, err := Parse([]byte(tt.data))

			assert.Error(t, err)
			assert.Nil(t, records)
		})
	}
}

func TestFilter_KeepsRecordsMatchingEveryPair(t *testing.T) {
	records := []Record{
		{"level": "ERROR", "msg": "a"},
		{"level": "INFO", "msg": "a"},
		{"level": "ERROR", "msg": "b"},
	}

	assert.Equal(t, []Record{records[0]}, Filter(records, Record{"level": "ERROR", "msg": "a"}))
}
