package model2vec

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/embed"
	"github.com/sha1n/mcp-acdc-server/internal/embed/model2vec/model2vectest"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestNew_EmbedsByMeanPooling(t *testing.T) {
	embedder, err := New(model2vectest.WriteArithmeticModel(t, false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := embedder.EmbedQuery(context.Background(), "alpha beta")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	want := []float32{2, 0}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNew_NormalizesWhenConfigured(t *testing.T) {
	embedder, err := New(model2vectest.WriteArithmeticModel(t, true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := embedder.EmbedQuery(context.Background(), "alpha beta")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if math.Abs(float64(got[0])-1) > 1e-6 || got[1] != 0 {
		t.Fatalf("got %v, want [1 0]", got)
	}
}

func TestNew_DropsUnknownTokens(t *testing.T) {
	embedder, err := New(model2vectest.WriteArithmeticModel(t, false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// "gamma" is out of vocabulary. Row 0 must not reach the mean.
	got, err := embedder.EmbedQuery(context.Background(), "alpha gamma")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if !slices.Equal(got, []float32{3, 1}) {
		t.Fatalf("got %v, want [3 1] — the unknown row leaked into the mean", got)
	}
}

func TestNew_ReturnsZeroVectorForUntokenizableText(t *testing.T) {
	embedder, err := New(model2vectest.WriteArithmeticModel(t, true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := embedder.EmbedQuery(context.Background(), "gamma")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if !slices.Equal(got, []float32{0, 0}) {
		t.Fatalf("got %v, want [0 0]", got)
	}
}

func TestNew_ReportsModelGeometry(t *testing.T) {
	embedder, err := New(model2vectest.WriteArithmeticModel(t, true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	info := embedder.Info()
	if info.Dimensions != 2 {
		t.Fatalf("Dimensions = %d, want 2", info.Dimensions)
	}
	if info.MaxTokens != 0 {
		t.Fatalf("MaxTokens = %d, want 0 — a static model has no window", info.MaxTokens)
	}
}

func TestNew_RejectsBrokenModelDirectories(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		if _, err := New(filepath.Join(t.TempDir(), "absent")); err == nil {
			t.Fatal("want an error naming the path")
		}
	})
	t.Run("hidden_dim disagrees with the matrix", func(t *testing.T) {
		dir := model2vectest.WriteArithmeticModel(t, true)
		write(t, filepath.Join(dir, "config.json"), `{"model_type":"model2vec","hidden_dim":384,"normalize":true}`)
		_, err := New(dir)
		if err == nil || !strings.Contains(err.Error(), "hidden_dim") {
			t.Fatalf("got %v, want a hidden_dim mismatch error", err)
		}
	})
	t.Run("token id beyond the matrix", func(t *testing.T) {
		dir := model2vectest.WriteArithmeticModel(t, true)
		write(t, filepath.Join(dir, "config.json"), `{"model_type":"model2vec","hidden_dim":2,"normalize":true}`)
		// Same vocabulary, one more token than the matrix has rows.
		write(t, filepath.Join(dir, "tokenizer.json"), strings.Replace(
			readFile(t, filepath.Join(dir, "tokenizer.json")),
			`"beta": 2`, `"beta": 2, "gamma": 3`, 1))
		_, err := New(dir)
		if err == nil || !strings.Contains(err.Error(), "vocabulary") {
			t.Fatalf("got %v, want a vocabulary/matrix size mismatch error", err)
		}
	})
}

// refusingTransport fails the test on any outbound request. go-huggingface's
// downloader builds its http.Client with a nil Transport, so every request it
// could make goes through http.DefaultTransport and lands here.
type refusingTransport struct{ t *testing.T }

func (r refusingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("the adapter attempted a network request to %s", req.URL)
	return nil, errors.New("network access is not allowed")
}

// TestNew_MakesNoNetworkRequest replaces a process-wide global, so nothing in
// this package may call t.Parallel.
func TestNew_MakesNoNetworkRequest(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = refusingTransport{t: t}
	t.Cleanup(func() { http.DefaultTransport = original })

	embedder, err := New(model2vectest.WriteArithmeticModel(t, true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := embedder.EmbedQuery(context.Background(), "alpha"); err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if _, err := embedder.EmbedDocuments(context.Background(), []string{"alpha", "beta"}); err != nil {
		t.Fatalf("EmbedDocuments: %v", err)
	}
}

func TestEmbedder_SatisfiesTheContract(t *testing.T) {
	dir := writeContractModel(t)
	embed.TestEmbedderContract(t, func() embed.Embedder {
		embedder, err := New(dir)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return embedder
	})
}

// contractVocabulary covers every word in embed.TestEmbedderContract's
// fixtures. The suite requires a unit-norm vector for each of them — only its
// deliberately unrepresentable fixtures may answer with zero — so a word
// missing here fails the contract for the wrong reason. If the suite gains a
// fixture, extend this list.
var contractVocabulary = []string{
	"alpha", "a", "longer", "passage", "of", "ordinary", "prose",
	"the", "treaty", "was", "signed", "in", "vienna", "that", "spring",
	"quicksort", "partitions", "an", "array", "around", "pivot",
	"chlorophyll", "absorbs", "light", "and", "blue", "red", "bands",
}

// writeContractModel builds a model whose rows are distinct enough for
// assertPreservesInputOrder to tell the three fixtures apart. Row i is a
// deterministic spread over eight dimensions; the values carry no meaning, only
// separation.
func writeContractModel(t *testing.T) string {
	t.Helper()
	const dimensions = 8

	vocab := map[string]int{"[UNK]": 0}
	for i, word := range contractVocabulary {
		vocab[word] = i + 1
	}
	rows := len(vocab)

	values := make([]float32, rows*dimensions)
	for id := range rows {
		for d := range dimensions {
			values[id*dimensions+d] = float32(math.Sin(float64(id+1) * float64(d+1)))
		}
	}

	dir := t.TempDir()
	vocabJSON, err := json.Marshal(vocab)
	if err != nil {
		t.Fatal(err)
	}
	tokenizer := fmt.Sprintf(`{
	  "version": "1.0",
	  "added_tokens": [{"id": 0, "content": "[UNK]", "special": true}],
	  "normalizer": {"type": "BertNormalizer", "clean_text": true, "handle_chinese_chars": true, "strip_accents": null, "lowercase": true},
	  "pre_tokenizer": {"type": "BertPreTokenizer"},
	  "post_processor": null,
	  "decoder": null,
	  "model": {"type": "WordPiece", "unk_token": "[UNK]", "continuing_subword_prefix": "##", "max_input_chars_per_word": 100,
	            "vocab": %s}
	}`, vocabJSON)
	write(t, filepath.Join(dir, "tokenizer.json"), tokenizer)
	write(t, filepath.Join(dir, "config.json"), fmt.Sprintf(`{"model_type":"model2vec","hidden_dim":%d,"normalize":true}`, dimensions))

	header, err := json.Marshal(map[string]any{"embeddings": map[string]any{
		"dtype": "F32", "shape": []int{rows, dimensions}, "data_offsets": []int{0, rows * dimensions * 4}}})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint64(len(header))); err != nil {
		t.Fatal(err)
	}
	buf.Write(header)
	buf.Write(float32Payload(values...))
	write(t, filepath.Join(dir, "model.safetensors"), buf.String())

	return dir
}
