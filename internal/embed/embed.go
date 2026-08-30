// Package embed defines the embedding seam: an interface, its obligations,
// and a fake. It imports nothing outside the standard library, which is what
// keeps the module's dependency tree unchanged until a real adapter lands.
package embed

import (
	"context"
	"errors"
	"fmt"
)

// ModelInfo describes the loaded model. Constant for an Embedder's lifetime.
type ModelInfo struct {
	// Dimensions is the width of every vector the Embedder returns.
	Dimensions int
	// MaxTokens is the longest input accepted without truncation. A value of
	// zero or less means the model has no window — static embedding models
	// have none, so nothing they are given is ever too long.
	MaxTokens int
}

// Embedder turns text into dense vectors.
//
// Implementations must be safe for concurrent use: indexing calls
// EmbedDocuments while EmbedQuery serves live requests.
//
// Every returned vector is Info().Dimensions wide and finite. Every vector is
// unit-norm, so callers may treat a dot product as cosine similarity, with one
// exception: a vector may be all-zero when the implementation cannot represent
// the text at all — a tokenizer that reduces it to no tokens. Callers must
// treat an all-zero vector as no embedding rather than as a position in the
// space: it scores zero against every query, so storing or ranking it would
// make it a match for everything at a similarity floor of zero or below.
type Embedder interface {
	Info() ModelInfo

	// EmbedDocuments embeds texts in corpus orientation, one vector per
	// input, in input order. It returns an error rather than truncating when
	// a text exceeds Info().MaxTokens.
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)

	// EmbedQuery embeds text in query orientation.
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}

// ErrInputTooLong reports which input exceeded the model window.
//
// The index matters: EmbedDocuments takes N texts and returns one error, so
// without it a caller could not tell which passage to re-split and would have
// to discard the whole batch or bisect blindly.
type ErrInputTooLong struct {
	Index int
}

func (e *ErrInputTooLong) Error() string {
	return fmt.Sprintf("embed: input at index %d exceeds the model's maximum token count", e.Index)
}

// ErrNoBackend reports that this build has no embedding adapter, so a
// configured semantic model cannot be loaded.
var ErrNoBackend = errors.New("embed: no embedding backend is available in this build")
