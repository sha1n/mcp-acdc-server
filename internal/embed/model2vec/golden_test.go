package model2vec

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

type goldenFixture struct {
	Dimensions    int                  `json:"dimensions"`
	Rows          int                  `json:"rows"`
	UnkTokenID    int                  `json:"unk_token_id"`
	Normalize     bool                 `json:"normalize"`
	EmbeddingRows map[string][]float32 `json:"embedding_rows"`
	Cases         []struct {
		Text   string    `json:"text"`
		IDs    []int     `json:"ids"`
		Vector []float32 `json:"vector"`
	} `json:"cases"`
}

// buildGoldenModel writes a model directory whose matrix carries the real rows
// for every token the golden texts use and zeros everywhere else. Mean pooling
// reads only the rows of the tokens present, so the vectors this produces are
// the real model's vectors.
func buildGoldenModel(t *testing.T, fixture goldenFixture) string {
	t.Helper()
	dir := t.TempDir()

	source, err := os.ReadFile(filepath.Join("testdata", "tokenizer.json"))
	if err != nil {
		t.Fatalf("read tokenizer fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"model_type":"model2vec","hidden_dim":%d,"normalize":%t}`, fixture.Dimensions, fixture.Normalize)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	payload := make([]byte, fixture.Rows*fixture.Dimensions*4)
	for id, row := range fixture.EmbeddingRows {
		index, err := strconv.Atoi(id)
		if err != nil {
			t.Fatalf("bad row id %q: %v", id, err)
		}
		offset := index * fixture.Dimensions * 4
		for j, value := range row {
			binary.LittleEndian.PutUint32(payload[offset+j*4:], math.Float32bits(value))
		}
	}
	header, err := json.Marshal(map[string]any{"embeddings": map[string]any{
		"dtype": "F32", "shape": []int{fixture.Rows, fixture.Dimensions},
		"data_offsets": []int{0, len(payload)}}})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint64(len(header))); err != nil {
		t.Fatal(err)
	}
	buf.Write(header)
	buf.Write(payload)
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func loadGoldenFixture(t *testing.T) goldenFixture {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", "golden.json.gz"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = file.Close() }()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("gunzip fixture: %v", err)
	}
	defer func() { _ = reader.Close() }()
	var fixture goldenFixture
	if err := json.NewDecoder(reader).Decode(&fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return fixture
}

// TestGolden_TokenizerMatchesTheReference pins tokenizer output. It is separate
// from the vector test so a tokenizer regression reports as a tokenizer
// regression rather than as an arithmetic one.
func TestGolden_TokenizerMatchesTheReference(t *testing.T) {
	fixture := loadGoldenFixture(t)
	embedder, err := New(buildGoldenModel(t, fixture))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	adapter, ok := embedder.(*Embedder)
	if !ok {
		t.Fatalf("New returned %T, want *Embedder", embedder)
	}
	if adapter.unkID != fixture.UnkTokenID {
		t.Fatalf("unknown token id is %d, want %d", adapter.unkID, fixture.UnkTokenID)
	}
	for _, tc := range fixture.Cases {
		t.Run(fmt.Sprintf("%q", tc.Text), func(t *testing.T) {
			got := adapter.tokenize(tc.Text)
			if !slices.Equal(got, tc.IDs) {
				t.Fatalf("tokenize(%q) = %v, want %v", tc.Text, got, tc.IDs)
			}
		})
	}
}

func TestGolden_VectorsMatchTheReference(t *testing.T) {
	fixture := loadGoldenFixture(t)
	embedder, err := New(buildGoldenModel(t, fixture))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tc := range fixture.Cases {
		t.Run(fmt.Sprintf("%q", tc.Text), func(t *testing.T) {
			got, err := embedder.EmbedQuery(context.Background(), tc.Text)
			if err != nil {
				t.Fatalf("EmbedQuery: %v", err)
			}
			if len(got) != len(tc.Vector) {
				t.Fatalf("got %d dimensions, want %d", len(got), len(tc.Vector))
			}
			for i := range got {
				if math.Abs(float64(got[i]-tc.Vector[i])) > 1e-5 {
					t.Fatalf("dimension %d is %v, want %v", i, got[i], tc.Vector[i])
				}
			}
		})
	}
}

// TestGolden_CaseFoldingHolds is the cheap check that would have caught a
// missing BertNormalizer: the two spellings must embed identically.
func TestGolden_CaseFoldingHolds(t *testing.T) {
	fixture := loadGoldenFixture(t)
	embedder, err := New(buildGoldenModel(t, fixture))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lower, err := embedder.EmbedQuery(context.Background(), "hybrid semantic search")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	upper, err := embedder.EmbedQuery(context.Background(), "Hybrid Semantic Search")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if !slices.Equal(lower, upper) {
		t.Fatal("case folding is not being applied")
	}
}
