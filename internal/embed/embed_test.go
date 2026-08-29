package embed

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFake_SatisfiesTheEmbedderContract(t *testing.T) {
	TestEmbedderContract(t, func() Embedder { return NewFake(8) })
}

func TestFake_SatisfiesTheContractWithABoundedWindow(t *testing.T) {
	TestEmbedderContract(t, func() Embedder {
		fake := NewFake(8)
		fake.MaxTokens = 16
		return fake
	})
}

func TestFake_IsDeterministicAndTextSensitive(t *testing.T) {
	fake := NewFake(8)
	ctx := context.Background()

	first, err := fake.EmbedQuery(ctx, "alpha")
	require.NoError(t, err)
	again, err := fake.EmbedQuery(ctx, "alpha")
	require.NoError(t, err)
	other, err := fake.EmbedQuery(ctx, "beta")
	require.NoError(t, err)

	require.Equal(t, first, again)
	require.NotEqual(t, first, other)
}

func TestFake_VectorsAreUnitNorm(t *testing.T) {
	fake := NewFake(16)

	vector, err := fake.EmbedQuery(context.Background(), strings.Repeat("x", 100))
	require.NoError(t, err)

	var sum float64
	for _, value := range vector {
		sum += float64(value) * float64(value)
	}
	require.InDelta(t, 1.0, math.Sqrt(sum), 1e-6)
}

func TestFake_EmbedDocumentsPreservesInputOrderAndCounts(t *testing.T) {
	fake := NewFake(8)
	ctx := context.Background()

	vectors, err := fake.EmbedDocuments(ctx, []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Len(t, vectors, 3)
	require.Equal(t, 3, fake.DocumentsEmbedded())

	single, err := fake.EmbedQuery(ctx, "b")
	require.NoError(t, err)
	require.Equal(t, single, vectors[1])
	// EmbedQuery is not a document embed and must not move the counter.
	require.Equal(t, 3, fake.DocumentsEmbedded())

	fake.ResetCounts()
	require.Equal(t, 0, fake.DocumentsEmbedded())
}

func TestFake_ReportsWhichInputExceededTheWindow(t *testing.T) {
	fake := NewFake(8)
	fake.MaxTokens = 4
	ctx := context.Background()

	_, err := fake.EmbedDocuments(ctx, []string{"ok", "way too long for this window"})

	var tooLong *ErrInputTooLong
	require.ErrorAs(t, err, &tooLong)
	require.Equal(t, 1, tooLong.Index)
}

func TestFake_HonoursContextCancellation(t *testing.T) {
	fake := NewFake(8)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fake.EmbedDocuments(ctx, []string{"a"})
	require.ErrorIs(t, err, context.Canceled)

	_, err = fake.EmbedQuery(ctx, "a")
	require.ErrorIs(t, err, context.Canceled)
}

func TestErrNoBackend_IsMatchable(t *testing.T) {
	require.True(t, errors.Is(ErrNoBackend, ErrNoBackend))
	require.NotEmpty(t, ErrNoBackend.Error())
}

// The contract suite must actually fail an implementation that breaks an
// obligation, or it is decoration. These two drive it against deliberately
// broken embedders through a recorder that captures the failure instead of
// failing this test.

func TestEmbedderContract_RejectsNonUnitNormVectors(t *testing.T) {
	require.True(t, contractFails(func() Embedder {
		return &scaledFake{Fake: NewFake(8), scale: 3}
	}))
}

func TestEmbedderContract_RejectsWrongWidthVectors(t *testing.T) {
	require.True(t, contractFails(func() Embedder {
		return &truncatingFake{Fake: NewFake(8)}
	}))
}

func TestEmbedderContract_RejectsSilentTruncation(t *testing.T) {
	require.True(t, contractFails(func() Embedder {
		fake := NewFake(8)
		fake.MaxTokens = 16
		return &truncatingWindowFake{Fake: fake}
	}))
}

// errContractFailed unwinds a recorded Fatal, which must abort the suite the
// way testing's own Fatal does rather than letting it run on.
var errContractFailed = errors.New("contract failed")

type recordingTB struct {
	testing.TB
	failed bool
}

func (r *recordingTB) Helper()                           {}
func (r *recordingTB) Error(args ...any)                 { r.failed = true }
func (r *recordingTB) Errorf(format string, args ...any) { r.failed = true }
func (r *recordingTB) Fatal(args ...any)                 { r.failed = true; panic(errContractFailed) }
func (r *recordingTB) Fatalf(format string, args ...any) { r.failed = true; panic(errContractFailed) }
func (r *recordingTB) FailNow()                          { r.failed = true; panic(errContractFailed) }

func contractFails(newEmbedder func() Embedder) (failed bool) {
	recorder := &recordingTB{}
	defer func() {
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); !ok || !errors.Is(err, errContractFailed) {
				panic(recovered)
			}
		}
		failed = recorder.failed
	}()

	TestEmbedderContract(recorder, newEmbedder)
	return recorder.failed
}

type scaledFake struct {
	*Fake
	scale float32
}

func (s *scaledFake) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vector, err := s.Fake.EmbedQuery(ctx, text)
	if err != nil {
		return nil, err
	}
	scale(vector, s.scale)
	return vector, nil
}

func (s *scaledFake) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := s.Fake.EmbedDocuments(ctx, texts)
	if err != nil {
		return nil, err
	}
	for _, vector := range vectors {
		scale(vector, s.scale)
	}
	return vectors, nil
}

func scale(vector []float32, factor float32) {
	for i := range vector {
		vector[i] *= factor
	}
}

type truncatingFake struct{ *Fake }

func (f *truncatingFake) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vector, err := f.Fake.EmbedQuery(ctx, text)
	if err != nil {
		return nil, err
	}
	return vector[:len(vector)-1], nil
}

// truncatingWindowFake accepts oversized input instead of reporting it, which
// is the failure obligation 3 exists to catch.
type truncatingWindowFake struct{ *Fake }

func (f *truncatingWindowFake) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	shortened := make([]string, len(texts))
	for i, text := range texts {
		runes := []rune(text)
		if len(runes) > f.MaxTokens {
			runes = runes[:f.MaxTokens]
		}
		shortened[i] = string(runes)
	}
	return f.Fake.EmbedDocuments(ctx, shortened)
}

func (f *truncatingWindowFake) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	runes := []rune(text)
	if len(runes) > f.MaxTokens {
		runes = runes[:f.MaxTokens]
	}
	return f.Fake.EmbedQuery(ctx, string(runes))
}

func TestEmbedderContract_RejectsNonFiniteVectors(t *testing.T) {
	require.True(t, contractFails(func() Embedder {
		return &nonFiniteFake{Fake: NewFake(8)}
	}))
}

func TestEmbedderContract_RejectsNonPositiveDimensions(t *testing.T) {
	// A Fake built with width 0 reports Dimensions: 0, which is the shape of an
	// adapter that failed to read its model metadata and defaulted the field.
	require.True(t, contractFails(func() Embedder { return NewFake(0) }))
}

func TestEmbedderContract_RejectsHappyPathErrors(t *testing.T) {
	require.True(t, contractFails(func() Embedder {
		return &failingDocumentsFake{Fake: NewFake(8)}
	}))
	require.True(t, contractFails(func() Embedder {
		return &failingQueryFake{Fake: NewFake(8)}
	}))
}

func TestEmbedderContract_RejectsWrongVectorCount(t *testing.T) {
	require.True(t, contractFails(func() Embedder {
		return &shortBatchFake{Fake: NewFake(8)}
	}))
}

func TestEmbedderContract_RejectsPlainOversizedInputErrors(t *testing.T) {
	require.True(t, contractFails(func() Embedder {
		fake := NewFake(8)
		fake.MaxTokens = 16
		return &plainOversizedErrorFake{Fake: fake}
	}))
}

func TestEmbedderContract_RejectsMisindexedOversizedInputErrors(t *testing.T) {
	require.True(t, contractFails(func() Embedder {
		fake := NewFake(8)
		fake.MaxTokens = 16
		return &misindexedOversizedErrorFake{Fake: fake}
	}))
}

func TestEmbedderContract_RejectsMisorderedVectors(t *testing.T) {
	require.True(t, contractFails(func() Embedder {
		return &reversingFake{Fake: NewFake(8)}
	}))
}

func TestEmbedderContract_RejectsEmbeddersUnsafeForConcurrentUse(t *testing.T) {
	require.True(t, contractFails(func() Embedder {
		return &exclusiveFake{Fake: NewFake(8)}
	}))
}

// reversingFake returns well-formed vectors for the right texts in the wrong
// order. embedBatch attaches vectors[i] to queue[i], so an adapter shaped like
// this mis-attributes every vector in the corpus and nothing else catches it.
type reversingFake struct{ *Fake }

func (f *reversingFake) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := f.Fake.EmbedDocuments(ctx, texts)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(vectors)-1; i < j; i, j = i+1, j-1 {
		vectors[i], vectors[j] = vectors[j], vectors[i]
	}
	return vectors, nil
}

// exclusiveFake models an adapter that cannot serve concurrent callers — a
// session or interpreter that has to be held exclusively. Every call is held
// briefly so callers that are meant to overlap actually do; without the hold
// the fake could be served serially and prove nothing.
type exclusiveFake struct {
	*Fake
	mu       sync.Mutex
	inFlight int
}

var errNotConcurrencySafe = errors.New("embed: this adapter cannot serve concurrent callers")

func (f *exclusiveFake) hold() error {
	f.mu.Lock()
	f.inFlight++
	overlapped := f.inFlight > 1
	f.mu.Unlock()

	time.Sleep(5 * time.Millisecond)

	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()

	if overlapped {
		return errNotConcurrencySafe
	}
	return nil
}

func (f *exclusiveFake) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	if err := f.hold(); err != nil {
		return nil, err
	}
	return f.Fake.EmbedDocuments(ctx, texts)
}

func (f *exclusiveFake) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if err := f.hold(); err != nil {
		return nil, err
	}
	return f.Fake.EmbedQuery(ctx, text)
}

// nonFiniteFake poisons one component of every vector it returns. NaN is
// checked before the norm, so the norm assertion cannot be what catches this.
type nonFiniteFake struct{ *Fake }

func (f *nonFiniteFake) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vector, err := f.Fake.EmbedQuery(ctx, text)
	if err != nil {
		return nil, err
	}
	vector[0] = float32(math.NaN())
	return vector, nil
}

func (f *nonFiniteFake) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := f.Fake.EmbedDocuments(ctx, texts)
	if err != nil {
		return nil, err
	}
	for _, vector := range vectors {
		vector[0] = float32(math.NaN())
	}
	return vectors, nil
}

var errBackendUnavailable = errors.New("backend unavailable")

type failingDocumentsFake struct{ *Fake }

func (f *failingDocumentsFake) EmbedDocuments(context.Context, []string) ([][]float32, error) {
	return nil, errBackendUnavailable
}

type failingQueryFake struct{ *Fake }

func (f *failingQueryFake) EmbedQuery(context.Context, string) ([]float32, error) {
	return nil, errBackendUnavailable
}

// shortBatchFake drops a vector, breaking the one-vector-per-input obligation
// while every vector it does return is well formed.
type shortBatchFake struct{ *Fake }

func (f *shortBatchFake) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := f.Fake.EmbedDocuments(ctx, texts)
	if err != nil || len(vectors) == 0 {
		return vectors, err
	}
	return vectors[:len(vectors)-1], nil
}

// plainOversizedErrorFake reports oversized input without the typed error.
// HybridService.embedBatch halves and retries only on *ErrInputTooLong, so an
// adapter shaped like this would make it drop every oversized passage.
type plainOversizedErrorFake struct{ *Fake }

func (f *plainOversizedErrorFake) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := f.Fake.EmbedDocuments(ctx, texts)
	var tooLong *ErrInputTooLong
	if errors.As(err, &tooLong) {
		return nil, errors.New("embed: input too long")
	}
	return vectors, err
}

// misindexedOversizedErrorFake reports the right error against the wrong text.
// The index is what the caller re-splits, so a stale one sends it to a passage
// that was never too long.
type misindexedOversizedErrorFake struct{ *Fake }

func (f *misindexedOversizedErrorFake) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := f.Fake.EmbedDocuments(ctx, texts)
	var tooLong *ErrInputTooLong
	if errors.As(err, &tooLong) {
		return nil, &ErrInputTooLong{Index: tooLong.Index + 3}
	}
	return vectors, err
}
