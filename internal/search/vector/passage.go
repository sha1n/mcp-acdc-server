package vector

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

const (
	// RunesPerToken converts a model's token window into runes without a
	// tokenizer, which only the adapter owns.
	//
	// The ratio is derived from English prose, which measures about 4, and is
	// deliberately not universally pessimistic: CJK runs near 1 token per rune
	// and dense code is denser still, so a ratio chosen to be safe everywhere
	// would overshoot by 3x or more and make the too-long error fire on every
	// passage of an entire corpus. It is a starting point; Halve is what makes
	// it self-correcting.
	RunesPerToken = 3

	// PrecisionWindow is the passage size in tokens, chosen for retrieval
	// precision rather than model capability. A static model has no window at
	// all, so this is what binds in the common case.
	PrecisionWindow = 200

	// MaxHalveDepth bounds the halve-and-retry recursion before a passage is
	// skipped and counted.
	MaxHalveDepth = 8

	// minWindowRunes keeps a pathologically small model window from producing
	// a body budget of zero. A model this narrow is not served by this design.
	minWindowRunes = 128

	overlapRatio      = 0.15
	prefixBudgetRatio = 0.25
	prefixSeparator   = " > "
	textSeparator     = "\n\n"
	keySeparator      = "\x00"
	ellipsis          = "…"
)

// Passage is one embeddable window of a chunk, paired with the cache key for
// the exact text that will be handed to the embedder.
type Passage struct {
	ChunkID string
	// Prefix is the source title and heading path, carried on every passage so
	// a window from deep inside a document still knows where it came from.
	Prefix string
	Body   string
	// Text is exactly what the embedder receives.
	Text string
	// Key is a model-salted hash of Text.
	Key string

	model Model
}

// Model identifies the embedder a passage is built for.
//
// ID is the configured model path and Dimensions is the width that path
// currently resolves to. Both are salt: the path alone is stable across a
// model swapped in place behind it, so width is what catches that swap.
type Model struct {
	ID         string
	Dimensions int
	// MaxTokens is the model's window. It is not salt — see PassageKey.
	MaxTokens int
}

// PassageKey hashes the exact embedder input, salted with the model.
//
// The salt is present from day one because without it a model swap, a
// dimension change or a changed prompt prefix would serve vectors from the
// wrong model. That cannot happen in a single process today, but it becomes
// live the moment this cache is ever persisted, and adding the salt later
// would be a silent-corruption migration.
//
// The window is deliberately left out: it changes how a chunk splits, so it
// already reaches the key through the text, and salting on it too would
// discard vectors whose embedder input never changed.
func PassageKey(model Model, text string) string {
	salt := model.ID + keySeparator + strconv.Itoa(model.Dimensions)
	sum := sha256.Sum256([]byte(salt + keySeparator + text))
	return hex.EncodeToString(sum[:])
}

// PrefixBudgetRunes is the longest prefix allowed for a given model window,
// exported so tests assert the cap rather than restate the arithmetic.
func PrefixBudgetRunes(maxTokens int) int {
	return int(float64(windowRunes(maxTokens)) * prefixBudgetRatio)
}

// BuildPassages splits a chunk into overlapping, context-prefixed windows.
//
// model.MaxTokens is the model's window; zero or less means unbounded, in
// which case PrecisionWindow binds. One rule serves both a static model with
// no window and a neural model whose window is narrower than the precision
// window.
func BuildPassages(chunk domain.Chunk, model Model) []Passage {
	window := windowRunes(model.MaxTokens)
	prefix := buildPrefix(chunk, int(float64(window)*prefixBudgetRatio))

	body := strings.TrimSpace(chunk.Content)
	if body == "" {
		if prefix == "" {
			return nil
		}
		return []Passage{newPassage(chunk.ID, model, prefix, "")}
	}

	budget := window - len([]rune(prefix)) - len([]rune(textSeparator))
	if budget < 1 {
		budget = 1
	}
	stride := budget - int(float64(budget)*overlapRatio)
	if stride < 1 {
		stride = 1
	}

	runes := []rune(body)
	passages := make([]Passage, 0, len(runes)/stride+1)
	for start := 0; start < len(runes); start += stride {
		end := start + budget
		if end > len(runes) {
			end = len(runes)
		}
		passages = append(passages, newPassage(chunk.ID, model, prefix, string(runes[start:end])))
		if end == len(runes) {
			break
		}
	}
	return passages
}

// Halve splits a passage's body in two, keeping the context prefix on both.
// It reports false for an indivisible body, which is the skip floor.
//
// This is what makes the rune-per-token estimate self-correcting: when the
// adapter's real tokenizer disagrees and reports the input too long, the
// caller halves and retries instead of dropping the text, and it works the
// same for CJK, for dense code and for any future model.
func (p Passage) Halve() (Passage, Passage, bool) {
	runes := []rune(p.Body)
	if len(runes) < 2 {
		return Passage{}, Passage{}, false
	}
	middle := len(runes) / 2
	first := newPassage(p.ChunkID, p.model, p.Prefix, string(runes[:middle]))
	second := newPassage(p.ChunkID, p.model, p.Prefix, string(runes[middle:]))
	return first, second, true
}

func newPassage(chunkID string, model Model, prefix, body string) Passage {
	text := prefix
	switch {
	case prefix == "":
		text = body
	case body != "":
		text = prefix + textSeparator + body
	}
	return Passage{
		ChunkID: chunkID,
		Prefix:  prefix,
		Body:    body,
		Text:    text,
		Key:     PassageKey(model, text),
		model:   model,
	}
}

func windowRunes(maxTokens int) int {
	tokens := PrecisionWindow
	if maxTokens > 0 && maxTokens < tokens {
		tokens = maxTokens
	}
	window := tokens * RunesPerToken
	if window < minWindowRunes {
		window = minWindowRunes
	}
	return window
}

// buildPrefix renders "source title > heading > heading", shortened to fit the
// budget by dropping the middle of the heading path first — the title and the
// deepest heading are the two most discriminating segments — and truncating
// only as a last resort.
func buildPrefix(chunk domain.Chunk, budget int) string {
	segments := make([]string, 0, len(chunk.HeadingPath)+1)
	if chunk.SourceTitle != "" {
		segments = append(segments, chunk.SourceTitle)
	}
	segments = append(segments, chunk.HeadingPath...)
	if len(segments) == 0 {
		return ""
	}

	full := strings.Join(segments, prefixSeparator)
	if len([]rune(full)) <= budget {
		return full
	}

	if len(segments) > 2 {
		shortened := strings.Join([]string{segments[0], ellipsis, segments[len(segments)-1]}, prefixSeparator)
		if len([]rune(shortened)) <= budget {
			return shortened
		}
		full = shortened
	}

	runes := []rune(full)
	if budget <= 0 {
		return ""
	}
	return string(runes[:budget])
}
