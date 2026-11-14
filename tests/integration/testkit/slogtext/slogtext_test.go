package slogtext

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_RoundTripsTextHandlerRecords(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logger.Info("plain", "k", "v")
	logger.Error("needs quoting", "error", errors.New(`a "quoted" = value`), "n", 7)

	records, err := Parse(buf.Bytes())

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

func TestParse_RejectsMalformedLines(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "attribute without value", data: "level=INFO msg\n"},
		{name: "unterminated quote", data: "msg=\"open\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.data))
			assert.Error(t, err)
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
