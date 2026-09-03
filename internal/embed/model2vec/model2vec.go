// Package model2vec implements embed.Embedder over a model2vec/potion static
// embedding model.
//
// A static model has no neural forward pass: embedding a text is tokenizing it,
// gathering one row of a matrix per token, taking the mean and normalizing. That
// is why it costs milliseconds where a transformer costs minutes, and it is the
// whole reason this adapter exists.
package model2vec

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/gomlx/go-huggingface/tokenizers/api"
	"github.com/gomlx/go-huggingface/tokenizers/hftokenizer"

	"github.com/sha1n/mcp-acdc-server/internal/embed"
)

const (
	matrixFile      = "model.safetensors"
	tokenizerFile   = "tokenizer.json"
	configFile      = "config.json"
	embeddingTensor = "embeddings"

	// normalizeEpsilon matches the reference implementation, which adds it to
	// the norm rather than branching on zero.
	normalizeEpsilon = 1e-32
)

type modelConfig struct {
	HiddenDim int   `json:"hidden_dim"`
	Normalize *bool `json:"normalize"`
}

// Embedder is a loaded static embedding model.
type Embedder struct {
	// mu guards tokenize. hftokenizer.Tokenizer does not document itself as
	// safe for concurrent use, and the Embedder contract requires that
	// EmbedQuery may run while EmbedDocuments is indexing. Encoding costs tens
	// of microseconds, so serializing it is cheaper than parsing a second
	// 680 KB tokenizer per goroutine.
	mu        sync.Mutex
	tokenizer *hftokenizer.Tokenizer

	matrix    *matrix
	unkID     int // -1 when the tokenizer declares no unknown token
	normalize bool
}

var _ embed.Embedder = (*Embedder)(nil)

// New loads the model in dir, which must hold model.safetensors, tokenizer.json
// and config.json. It reads nothing else and reaches no network.
func New(dir string) (embed.Embedder, error) {
	settings, err := loadConfig(filepath.Join(dir, configFile))
	if err != nil {
		return nil, err
	}
	embeddings, err := readMatrix(filepath.Join(dir, matrixFile), embeddingTensor)
	if err != nil {
		return nil, err
	}
	if settings.HiddenDim > 0 && settings.HiddenDim != embeddings.cols {
		return nil, fmt.Errorf("model %q is inconsistent: config.json hidden_dim is %d but the embedding matrix is %d wide", dir, settings.HiddenDim, embeddings.cols)
	}
	tokenizer, err := loadTokenizer(filepath.Join(dir, tokenizerFile), embeddings.rows, dir)
	if err != nil {
		return nil, err
	}
	normalize := true
	if settings.Normalize != nil {
		normalize = *settings.Normalize
	}
	return &Embedder{
		tokenizer: tokenizer,
		matrix:    embeddings,
		unkID:     unknownTokenID(tokenizer),
		normalize: normalize,
	}, nil
}

func loadConfig(path string) (modelConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return modelConfig{}, fmt.Errorf("failed to read %q: %w", path, err)
	}
	var settings modelConfig
	if err := json.Unmarshal(raw, &settings); err != nil {
		return modelConfig{}, fmt.Errorf("failed to parse %q: %w", path, err)
	}
	return settings, nil
}

// loadTokenizer reads tokenizer.json and disables post-processing.
//
// Disabling it is not optional. potion's tokenizer.json carries a
// TemplateProcessing block inherited from its parent model, which wraps every
// input in [CLS] (id 2) and [SEP] (id 3). Left enabled, every vector would
// silently pool those two unrelated rows, and only the golden test notices.
func loadTokenizer(path string, rows int, dir string) (*hftokenizer.Tokenizer, error) {
	tokenizer, err := hftokenizer.NewFromFile(nil, path)
	if err != nil {
		return nil, fmt.Errorf("failed to load %q: %w", path, err)
	}
	if err := tokenizer.With(api.EncodeOptions{AddSpecialTokens: false}); err != nil {
		return nil, fmt.Errorf("failed to configure %q: %w", path, err)
	}
	for token, id := range tokenizer.GetVocab() {
		if id < 0 || id >= rows {
			return nil, fmt.Errorf("model %q is inconsistent: vocabulary token %q has id %d but the embedding matrix has %d rows", dir, token, id, rows)
		}
	}
	return tokenizer, nil
}

func unknownTokenID(tokenizer *hftokenizer.Tokenizer) int {
	id, err := tokenizer.SpecialTokenID(api.TokUnknown)
	if err != nil {
		return -1
	}
	return id
}

// Info reports the loaded geometry. MaxTokens is zero: a static model pools a
// bag of token vectors, so no input is ever too long for it.
func (e *Embedder) Info() embed.ModelInfo {
	return embed.ModelInfo{Dimensions: e.matrix.cols}
}

// EmbedDocuments embeds texts in corpus orientation, one vector per input, in
// input order.
func (e *Embedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		vectors[i] = e.embed(text)
	}
	return vectors, nil
}

// EmbedQuery embeds text in query orientation.
func (e *Embedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return e.embed(text), nil
}

// embed is the reference algorithm: tokenize, drop unknown ids, mean-pool the
// rows, normalize.
func (e *Embedder) embed(text string) []float32 {
	vector := make([]float32, e.matrix.cols)
	ids := e.tokenize(text)
	if len(ids) == 0 {
		return vector
	}
	for _, id := range ids {
		row := e.matrix.row(id)
		for j, value := range row {
			vector[j] += value
		}
	}
	count := float32(len(ids))
	for j := range vector {
		vector[j] /= count
	}
	if e.normalize {
		normalizeInPlace(vector)
	}
	return vector
}

func (e *Embedder) tokenize(text string) []int {
	e.mu.Lock()
	encoded := e.tokenizer.Encode(text)
	e.mu.Unlock()

	ids := encoded[:0:0]
	for _, id := range encoded {
		if id == e.unkID || id < 0 || id >= e.matrix.rows {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func normalizeInPlace(vector []float32) {
	var sum float64
	for _, value := range vector {
		sum += float64(value) * float64(value)
	}
	norm := float32(math.Sqrt(sum) + normalizeEpsilon)
	for j := range vector {
		vector[j] /= norm
	}
}
