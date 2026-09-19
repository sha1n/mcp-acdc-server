// Package model2vectest builds throw-away model directories that the model2vec
// adapter can load.
//
// It exists because a _test.go file cannot be imported across packages, and
// both the adapter's own tests and the server wiring tests need the same
// fixture.
package model2vectest

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

const arithmeticTokenizer = `{
  "version": "1.0",
  "added_tokens": [{"id": 0, "content": "[UNK]", "special": true}],
  "normalizer": {"type": "BertNormalizer", "clean_text": true, "handle_chinese_chars": true, "strip_accents": null, "lowercase": true},
  "pre_tokenizer": {"type": "BertPreTokenizer"},
  "post_processor": null,
  "decoder": null,
  "model": {"type": "WordPiece", "unk_token": "[UNK]", "continuing_subword_prefix": "##", "max_input_chars_per_word": 100,
            "vocab": {"[UNK]": 0, "alpha": 1, "beta": 2}}
}`

// WriteArithmeticModel writes a three-token model whose vectors can be checked
// by hand, and returns its directory.
//
// Row 0 is the unknown token and must never be pooled. Rows 1 and 2 mean to
// (2, 0), which normalizes to (1, 0).
func WriteArithmeticModel(t testing.TB, normalize bool) string {
	t.Helper()
	dir := t.TempDir()

	WriteFile(t, filepath.Join(dir, "tokenizer.json"), arithmeticTokenizer)
	WriteFile(t, filepath.Join(dir, "config.json"),
		fmt.Sprintf(`{"model_type":"model2vec","hidden_dim":2,"normalize":%t}`, normalize))
	WriteFile(t, filepath.Join(dir, "model.safetensors"),
		safetensors(t, 3, 2, Float32Payload(9, 9, 3, 1, 1, -1)))

	return dir
}

// WriteFile writes content to path, failing the test rather than returning an
// error.
func WriteFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Float32Payload encodes values as the little-endian float32 block a
// safetensors tensor holds.
func Float32Payload(values ...float32) []byte {
	payload := make([]byte, 4*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint32(payload[4*i:], math.Float32bits(value))
	}
	return payload
}

// safetensors wraps payload in a single-tensor safetensors file named
// "embeddings".
func safetensors(t testing.TB, rows, cols int, payload []byte) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"embeddings": map[string]any{
		"dtype": "F32", "shape": []int{rows, cols}, "data_offsets": []int{0, len(payload)}}})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint64(len(header))); err != nil {
		t.Fatal(err)
	}
	buf.Write(header)
	buf.Write(payload)
	return buf.String()
}
