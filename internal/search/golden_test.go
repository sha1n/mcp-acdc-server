package search

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/stretchr/testify/require"
)

// Corpora under testdata. zeroConfigCorpus carries no frontmatter, reproducing
// what the server indexes in repository mode; curatedCorpus carries the
// frontmatter an authored catalog supplies.
const (
	zeroConfigCorpus = "testdata/corpus"
	curatedCorpus    = "testdata/curated"
)

// goldenCandidates is deliberately larger than any asserted rank, so a document
// that drops out of the top ranks is reported rather than silently truncated.
const goldenCandidates = 25

// goldenQuery pins one ranking judgement. Expectations address documents by
// source URI or by chunk URI; both forms are matched against every result.
//
// Assertions are on rank position only. Bleve scores move with library
// upgrades and with corpus size, so a score assertion pins the library rather
// than the ranking model.
type goldenQuery struct {
	name    string
	corpora []string
	query   string
	// rationale states why the pinned ranking is the right answer.
	rationale string
	// top, when set, must be the URI at rank 1.
	top string
	// within maps a URI to the rank it must reach or better.
	within map[string]int
	// ranks maps a URI to the exact rank it must occupy. Used to pin a measured
	// ranking that is not the desired one, so improving it fails the case
	// instead of passing silently.
	ranks map[string]int
	// absent lists URIs that must not appear among the candidates at all.
	absent []string
	// finding, when non-empty, marks a case whose expectations pin what the
	// current model *does* rather than what it should do. Retuning belongs to
	// the issue named here, not to this harness.
	finding string
}

// goldenService indexes the given corpora with the harness's default settings.
func goldenService(t *testing.T, corpora ...string) *Service {
	t.Helper()
	return goldenServiceWith(t, testSettings(), corpora...)
}

// corpusChunks discovers chunks through the production discovery and chunking
// path, so tests measure the scorer against the documents it actually sees
// rather than against hand-built chunks.
func corpusChunks(t *testing.T, corpora ...string) []domain.Chunk {
	t.Helper()

	index := &domain.IndexMetadata{Include: []string{"**/*.md"}}
	var chunks []domain.Chunk
	for _, corpus := range corpora {
		result, err := resources.Discover(context.Background(), content.NewContentProvider(corpus), index, "acdc")
		require.NoError(t, err, "discover %s", corpus)
		require.NotEmpty(t, result.Chunks, "corpus %s produced no chunks", corpus)
		chunks = append(chunks, result.Chunks...)
	}
	return chunks
}

func goldenServiceWith(t *testing.T, settings config.SearchSettings, corpora ...string) *Service {
	t.Helper()

	service := NewService(settings)
	t.Cleanup(service.Close)
	indexChunks(t, service, corpusChunks(t, corpora...))
	return service
}

// rankOf returns the best 1-based rank at which uri appears, or 0 when absent.
func rankOf(results []SearchResult, uri string) int {
	for rank, result := range results {
		if result.ChunkURI == uri || result.SourceURI == uri {
			return rank + 1
		}
	}
	return 0
}

// reportRanking logs the leading results for a query. Visible under
// `go test -run TestGolden -v`, this table is the measurement the harness
// exists to produce; the assertions are its regression gate.
func reportRanking(t *testing.T, query string, results []SearchResult) {
	t.Helper()
	t.Logf("query %q — %d candidates", query, len(results))
	for rank, result := range results {
		if rank == 5 {
			break
		}
		t.Logf("  %d · %.4f · %s · %s", rank+1, result.Score, result.ChunkURI, strings.Join(result.HeadingPath, " > "))
	}
}

func (q goldenQuery) run(t *testing.T) {
	t.Helper()

	service := goldenService(t, q.corpora...)
	results, err := service.Search(q.query, goldenCandidates)
	require.NoError(t, err)
	reportRanking(t, q.query, results)

	if q.finding != "" {
		t.Logf("FINDING (%s): %s", q.finding, q.rationale)
	}

	if q.top != "" {
		require.NotEmpty(t, results, "no results for %q", q.query)
		require.Equal(t, 1, rankOf(results, q.top), "%s\nexpected %s at rank 1, got %s", q.rationale, q.top, results[0].ChunkURI)
	}
	for uri, limit := range q.within {
		rank := rankOf(results, uri)
		require.NotZero(t, rank, "%s\nexpected %s within rank %d, absent from %d candidates", q.rationale, uri, limit, len(results))
		require.LessOrEqual(t, rank, limit, "%s\nexpected %s within rank %d, got rank %d", q.rationale, uri, limit, rank)
	}
	for uri, expected := range q.ranks {
		require.Equal(t, expected, rankOf(results, uri), "%s\nexpected %s at rank %d", q.rationale, uri, expected)
	}
	for _, uri := range q.absent {
		require.Zero(t, rankOf(results, uri), "%s\nexpected %s to be absent", q.rationale, uri)
	}
}

func TestGolden_ZeroConfigRanking(t *testing.T) {
	corpus := []string{zeroConfigCorpus}
	queries := []goldenQuery{
		{
			name:    "dedicated page beats the README summary",
			corpora: corpus,
			query:   "authentication",
			rationale: "docs/authentication.md is the page about the topic and wins, as it should. " +
				"Measured: its six chunks then take ranks 1-6 outright, and the README summary — the only other document with a claim to the query — lands at 7. " +
				"Ranking is per chunk with no per-source diversification, so one document can occupy an entire result page and hide every alternative from a client that reads the top five.",
			top:     "acdc://docs/authentication",
			ranks:   map[string]int{"acdc://README": 7},
			finding: "no per-source diversification",
		},
		{
			name:      "defining section beats sibling sections of the same page",
			corpora:   corpus,
			query:     "bearer token",
			rationale: "The section that defines bearer tokens answers the query; rotation and revocation only use the term.",
			top:       "acdc://docs/authentication#bearer-tokens",
		},
		{
			name:      "nested heading retrieves the section, not the page",
			corpora:   corpus,
			query:     "readiness probe",
			rationale: "Chunk granularity is the point: the answer is the third-level Readiness section, not the whole Kubernetes page.",
			top:       "acdc://docs/deployment/kubernetes#readiness",
		},
		{
			name:    "path carries a signal the body does not",
			corpora: corpus,
			query:   "rate limiting",
			rationale: "docs/rate-limiting/quotas.md never writes 'rate' or 'limiting' in its body or headings, so only path_labels can retrieve it. " +
				"It must still beat the troubleshooting section that mentions an append rate in passing: path_labels (1.25) over content (1.0).",
			top:    "acdc://docs/rate-limiting/quotas",
			within: map[string]int{"acdc://docs/troubleshooting#queries-are-slow": 6},
		},
		{
			name:    "a heading beats repetition in a body",
			corpora: corpus,
			query:   "checksums",
			rationale: "The internals page has a Checksums heading and names the term once; the CLI reference names it three times under a heading about a subcommand. " +
				"heading_path (2.5) over content (1.0): what a section is titled says more than how often a term recurs inside one.",
			top:    "acdc://docs/internals/indexing#checksums",
			within: map[string]int{"acdc://docs/reference/cli#ledger-verify": 3},
		},
		{
			name:      "title outranks a heading on a recurring term",
			corpora:   corpus,
			query:     "configuration",
			rationale: "The term recurs in four documents. The page titled Configuration should win over the README section and the passing mentions.",
			top:       "acdc://docs/configuration",
			within:    map[string]int{"acdc://README": 6},
		},
		{
			name:      "symptom heading beats an explanatory mention",
			corpora:   corpus,
			query:     "401",
			rationale: "Troubleshooting has a heading for the symptom; authentication.md only mentions the status code while explaining validation.",
			top:       "acdc://docs/troubleshooting#requests-fail-with-401",
		},
		{
			name:      "an unrelated sibling page does not leak in",
			corpora:   corpus,
			query:     "helm chart",
			rationale: "Precision probe: the Docker page shares a parent directory with Kubernetes but says nothing about Helm.",
			top:       "acdc://docs/deployment/kubernetes#helm-chart",
			absent:    []string{"acdc://docs/deployment/docker"},
		},
		{
			name:    "path labels no longer admit an unrelated document",
			corpora: corpus,
			query:   "readiness",
			rationale: "README has no bearing on readiness probes, and the corpus contains one obviously correct answer. " +
				"Edit-distance matching is applied to content and headings but not to path_labels, so the label 'readme' — " +
				"which stems to within one edit of the query — no longer retrieves the file on a term it does not contain. " +
				"Both leading chunks now come from the page that answers the query; the README is demoted, not excluded.",
			top:   "acdc://docs/deployment/kubernetes#readiness",
			ranks: map[string]int{"acdc://README": 3},
		},
		{
			name:    "one term of a multi-word query is enough to retrieve",
			corpora: corpus,
			query:   "helm chart readiness",
			rationale: "No chunk carries all three terms. Every field clause is a MatchQuery left at its default OR operator, " +
				"so the Helm section leads on two terms and the Readiness section still reaches rank 2 on one. " +
				"Requiring every term instead returns nothing at all for this query: the operator applies within a single field, " +
				"and no field here holds the whole query. Measured and declined in #92.",
			top:    "acdc://docs/deployment/kubernetes#helm-chart",
			within: map[string]int{"acdc://docs/deployment/kubernetes#readiness": 2},
		},
	}

	for _, query := range queries {
		t.Run(query.name, query.run)
	}
}

func TestGolden_CuratedRanking(t *testing.T) {
	mixed := []string{zeroConfigCorpus, curatedCorpus}
	queries := []goldenQuery{
		{
			name:    "keywords retrieve a document whose body lacks the term",
			corpora: mixed,
			query:   "compaction",
			rationale: "Retention policy names compaction only in frontmatter keywords; the indexing page carries it as a heading and in its body. " +
				"The boost table says keywords (3.0) outranks heading_path (2.5), so the curated document was expected to win. " +
				"Measured: the indexing page wins outright and every retention chunk trails it. A larger boost does not decide a match — " +
				"the indexing chunk scores on two fields at once, and per-field IDF favours it because the term appears in one heading_path but in the keywords of all three retention chunks. " +
				"TestSearch_ChunkFieldBoosts proves the boosts are ordered; it holds all else equal, and on a real corpus all else is not equal.",
			top:     "acdc://docs/internals/indexing#segment-compaction",
			ranks:   map[string]int{"acdc://docs/retention": 2},
			finding: "#84 — boost order is not the effective ranking",
		},
		{
			name:    "a keyword beats a passing mention in a body",
			corpora: mixed,
			query:   "dashboards",
			rationale: "Observability declares dashboards as a keyword and never writes the word; troubleshooting mentions one dashboard in passing. " +
				"keywords (3.0) over content (1.0) is the curated catalog's whole value proposition, and this is the pair that isolates it.",
			top:    "acdc://docs/observability",
			within: map[string]int{"acdc://docs/troubleshooting#queries-are-slow": 4},
		},
		{
			name:      "a curated title still competes on its own terms",
			corpora:   mixed,
			query:     "metrics endpoint",
			rationale: "Frontmatter must not make a curated document win queries it has no claim to; here it has one, through both keywords and a heading.",
			top:       "acdc://docs/observability#metrics-endpoint",
		},
	}

	for _, query := range queries {
		t.Run(query.name, query.run)
	}
}

// TestGolden_KeywordsAreInertInZeroConfigMode pins the structural finding that
// motivates this harness: keywords carry the highest boost in the model (3.0,
// above heading_path at 2.5) and come only from YAML frontmatter, which
// repository documentation does not have. In the mode the server now defaults
// to, the strongest signal has no documents to match.
func TestGolden_KeywordsAreInertInZeroConfigMode(t *testing.T) {
	index := &domain.IndexMetadata{Include: []string{"**/*.md"}}
	result, err := resources.Discover(context.Background(), content.NewContentProvider(zeroConfigCorpus), index, "acdc")
	require.NoError(t, err)
	require.NotEmpty(t, result.Chunks)

	var withKeywords []string
	for _, chunk := range result.Chunks {
		if len(chunk.Keywords) > 0 {
			withKeywords = append(withKeywords, chunk.ChunkURI)
		}
	}
	require.Empty(t, withKeywords, "keywords boost %v has no reachable documents without frontmatter", testSettings().KeywordsBoost)

	curated, err := resources.Discover(context.Background(), content.NewContentProvider(curatedCorpus), index, "acdc")
	require.NoError(t, err)
	keywordChunks := 0
	for _, chunk := range curated.Chunks {
		if len(chunk.Keywords) > 0 {
			keywordChunks++
		}
	}
	require.NotZero(t, keywordChunks, "curated corpus must exercise the keywords field")
	t.Logf("keywords populated: 0/%d zero-config chunks, %d/%d curated chunks",
		len(result.Chunks), keywordChunks, len(curated.Chunks))
}

// TestGolden_TitleNeverFiresAloneInZeroConfigMode pins the second structural
// finding. Without frontmatter a source title is the document's first level-one
// heading, which the chunker also puts at the head of every heading path. So
// source_title (2.0) never matches a chunk that heading_path (2.5) has not
// already matched: two of the model's five fields carry no signal of their own
// in the mode the server now defaults to.
func TestGolden_TitleNeverFiresAloneInZeroConfigMode(t *testing.T) {
	index := &domain.IndexMetadata{Include: []string{"**/*.md"}}
	result, err := resources.Discover(context.Background(), content.NewContentProvider(zeroConfigCorpus), index, "acdc")
	require.NoError(t, err)
	require.NotEmpty(t, result.Chunks)

	for _, chunk := range result.Chunks {
		require.NotEmpty(t, chunk.HeadingPath, "chunk %s has no heading path", chunk.ChunkURI)
		require.Equal(t, chunk.SourceTitle, chunk.HeadingPath[0], "chunk %s: title is not the head of its heading path", chunk.ChunkURI)
	}
}

// TestGolden_ContentBoostCannotOutweighAHeadingMatch measures how much authority
// a boost constant actually carries, which is what #84 has to know before
// exposing any of them as configuration.
//
// The query isolates one field against another: the internals page carries
// "checksums" as a heading and never in its body, the CLI reference carries it
// three times in a body and in no heading. Raising the content boost narrows the
// gap and then stops — the two scores converge without crossing, so no value of
// content_boost makes the body match win. Bleve normalizes each clause of the
// disjunction against the query's own weights, which bounds what a single
// clause's boost can buy; field length normalization then keeps the short
// heading_path field ahead of a long body.
//
// A boost is therefore not a ranking override. Separately measured against the
// golden cases: content_boost must reach ~10x its default before any of them
// change at all, and settles by ~30x. Retuning the ranking model means changing
// analyzers, field norms, or query structure — the boosts alone cannot express
// every outcome an operator might configure them to want.
func TestGolden_ContentBoostCannotOutweighAHeadingMatch(t *testing.T) {
	const (
		query       = "checksums"
		headingOnly = "acdc://docs/internals/indexing#checksums"
		bodyOnly    = "acdc://docs/reference/cli#ledger-verify"
	)

	for _, boost := range []float64{1, 3, 1000} {
		t.Run(fmt.Sprintf("content_boost_%g", boost), func(t *testing.T) {
			settings := testSettings()
			settings.ContentBoost = boost
			service := goldenServiceWith(t, settings, zeroConfigCorpus)

			results, err := service.Search(query, goldenCandidates)
			require.NoError(t, err)
			reportRanking(t, query, results)
			require.Equal(t, 1, rankOf(results, headingOnly), "heading match must lead at content_boost %g", boost)
			require.Equal(t, 2, rankOf(results, bodyOnly), "body match must trail it at content_boost %g", boost)
		})
	}
}

// TestGolden_HeadingBoostInfluenceIsBounded measures what the heading boost
// buys once it is configurable, over the same document pair as
// TestGolden_ContentBoostCannotOutweighAHeadingMatch: the internals page
// carries "checksums" as a heading and never in its body, the CLI reference
// carries it in a body and in no heading.
//
// The influence is not symmetric, and not for the reason it might seem.
// Raising heading_boost cannot change the ranking here because the
// heading-only document already leads at the default 2.5: ranks at 2.5 and
// at 1000 are identical because rank 1 has nowhere left to go, not because a
// large boost is inert. (Scores do keep moving — at 1000 the heading match's
// score is over 4000x the body match's, versus ~12x at 2.5 — the ranks just
// can't reflect it.) The measured bound on how far a boost can pull a
// *losing* clause toward overtaking a winner is established separately, by
// the sibling test TestGolden_ContentBoostCannotOutweighAHeadingMatch, which
// raises content_boost against this same heading match instead.
//
// The boost: 1000 row is not evidence of a bound; it pins that a very large
// boost does not destabilize the ordering (for example through score
// overflow), which is a fact worth guarding on its own even though the rank
// it asserts cannot change by construction.
//
// Lowering heading_boost to 0 does drop the heading_path clause from the
// query, but it does not remove the heading-only document's only route to
// the term: the chunker slices a section's Content starting at its own
// heading line, so the heading text is also present in the content field and
// keeps matching through the content_boost clause. At heading_boost 0 the
// heading-only document therefore still surfaces, just behind the body match
// instead of ahead of it — measured, not predicted. That asymmetry, plus the
// content-field duplication behind it, is the honest description of what an
// operator gets from --search-heading-boost at 0; docs/configuration.md
// should say so. Expectations here are measured, not desired; a change means
// the ranking model moved.
func TestGolden_HeadingBoostInfluenceIsBounded(t *testing.T) {
	const (
		query       = "checksums"
		headingOnly = "acdc://docs/internals/indexing#checksums"
		bodyOnly    = "acdc://docs/reference/cli#ledger-verify"
	)

	tests := []struct {
		boost       float64
		wantHeading int
		wantBody    int
	}{
		{boost: 0, wantHeading: 2, wantBody: 1},
		{boost: 0.1, wantHeading: 1, wantBody: 2},
		{boost: 2.5, wantHeading: 1, wantBody: 2},
		{boost: 1000, wantHeading: 1, wantBody: 2},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("heading_boost_%g", tt.boost), func(t *testing.T) {
			settings := testSettings()
			settings.HeadingBoost = tt.boost
			service := goldenServiceWith(t, settings, zeroConfigCorpus)

			results, err := service.Search(query, goldenCandidates)
			require.NoError(t, err)
			reportRanking(t, query, results)
			require.Equal(t, tt.wantHeading, rankOf(results, headingOnly), "heading-only document at heading_boost %g", tt.boost)
			require.Equal(t, tt.wantBody, rankOf(results, bodyOnly), "body-only document at heading_boost %g", tt.boost)
		})
	}
}

// TestGolden_FuzzinessPollutesThroughPathLabels measures the cost side of
// edit-distance matching, isolated to the field that causes it. The corpus has
// exactly one document about readiness probes. The README has no bearing on
// the query, but its path label "readme" stems to a term one edit from the
// query's "readi", so fuzzing path_labels retrieves it on a term it does not
// contain.
//
// Expectations are measured, not desired. A change means the ranking model
// moved.
func TestGolden_FuzzinessPollutesThroughPathLabels(t *testing.T) {
	const (
		query   = "readiness"
		correct = "acdc://docs/deployment/kubernetes#readiness"
		junk    = "acdc://README"
	)

	tests := []struct {
		name string
		// fuzziness resolves the edit distance per field, mirroring
		// Service.fuzziness.
		fuzziness func(string) int
		// wantJunk is the README's best rank, or 0 when it is absent.
		wantJunk int
	}{
		{name: "every field", fuzziness: func(string) int { return 1 }, wantJunk: 2},
		{name: "no field", fuzziness: func(string) int { return 0 }, wantJunk: 0},
		{name: "every field but path labels", fuzziness: fieldFuzziness, wantJunk: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := goldenService(t, zeroConfigCorpus)
			service.fuzziness = tt.fuzziness

			results, err := service.Search(query, goldenCandidates)
			require.NoError(t, err)
			reportRanking(t, query, results)

			require.Equal(t, 1, rankOf(results, correct), "the page that answers the query must lead")
			require.Equal(t, tt.wantJunk, rankOf(results, junk), "README rank with fuzziness %q", tt.name)
		})
	}
}

// TestGolden_FuzzinessRecallProbes measures the benefit side of edit-distance
// matching: what a mistyped query retrieves with it and without it.
//
// A probe is only meaningful if it stems to a term within edit distance 1 of
// its target — the analyzer runs before the fuzzy searcher, and stemming a
// misspelling is not predictable. Verify any new probe with an analyzer dump
// before adding it; a probe that misses measures nothing while still passing.
func TestGolden_FuzzinessRecallProbes(t *testing.T) {
	tests := []struct {
		name  string
		probe string
		// wantFuzzy is the chunk URI required at rank 1 with edit distance 1 on
		// every field, or "" when the probe must retrieve nothing at all.
		wantFuzzy string
		// wantExact is the same with edit-distance matching off everywhere.
		wantExact string
		// wantShipped is the same under the shipped per-field policy.
		wantShipped string
		rationale   string
	}{
		{
			name:        "a dropped character is rescued",
			probe:       "authentcation",
			wantFuzzy:   "acdc://docs/authentication#authentication",
			wantExact:   "",
			wantShipped: "acdc://docs/authentication#authentication",
			rationale:   "authentcation stems to authentc, one edit from authentication's authent.",
		},
		{
			name:        "a dropped character mid-word is rescued",
			probe:       "compation",
			wantFuzzy:   "acdc://docs/internals/indexing#segment-compaction",
			wantExact:   "",
			wantShipped: "acdc://docs/internals/indexing#segment-compaction",
			rationale:   "compation stems to compat, one edit from compaction's compact.",
		},
		{
			name:        "a dropped character in a heading term is rescued",
			probe:       "cheksums",
			wantFuzzy:   "acdc://docs/internals/indexing#checksums",
			wantExact:   "",
			wantShipped: "acdc://docs/internals/indexing#checksums",
			rationale:   "cheksums stems to cheksum, one edit from checksums' checksum.",
		},
		{
			name:        "the stemmer alone handles a plural near-miss",
			probe:       "kubernets",
			wantFuzzy:   "acdc://docs/deployment/kubernetes#running-ledger-on-kubernetes",
			wantExact:   "acdc://docs/deployment/kubernetes#running-ledger-on-kubernetes",
			wantShipped: "acdc://docs/deployment/kubernetes#running-ledger-on-kubernetes",
			rationale:   "Both kubernets and kubernetes stem to kubernet, so this costs no fuzziness. Control: without it the table reads as though fuzziness were the only thing rescuing near-misses.",
		},
		{
			name:        "a two-edit typo is beyond rescue",
			probe:       "deploymnet",
			wantFuzzy:   "",
			wantExact:   "",
			wantShipped: "",
			rationale:   "deploymnet does not stem at all, leaving it four edits from deployment's deploy. Control: edit distance 1 is a narrow safety net, not typo correction.",
		},
		{
			name:        "a British spelling is beyond rescue",
			probe:       "authorisation",
			wantFuzzy:   "",
			wantExact:   "",
			wantShipped: "",
			rationale:   "The stemmer takes authorization to author but authorisation only to authoris — two edits apart. Control: fuzziness is not a spelling-variant mechanism.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Log(tt.rationale)
			for _, setting := range []struct {
				label     string
				fuzziness func(string) int
				want      string
			}{
				{"fuzzy", func(string) int { return 1 }, tt.wantFuzzy},
				{"exact", func(string) int { return 0 }, tt.wantExact},
				{"shipped", fieldFuzziness, tt.wantShipped},
			} {
				t.Run(setting.label, func(t *testing.T) {
					service := goldenService(t, zeroConfigCorpus)
					service.fuzziness = setting.fuzziness

					results, err := service.Search(tt.probe, goldenCandidates)
					require.NoError(t, err)
					reportRanking(t, tt.probe, results)

					if setting.want == "" {
						require.Empty(t, results, "%s\n%q must retrieve nothing under the %s policy", tt.rationale, tt.probe, setting.label)
						return
					}
					require.Equal(t, 1, rankOf(results, setting.want), "%s\n%q under the %s policy", tt.rationale, tt.probe, setting.label)
				})
			}
		})
	}
}

// fieldClauseCandidates counts what one field's clause retrieves on its own,
// built the way Service.Search builds it.
func fieldClauseCandidates(t *testing.T, service *Service, field, queryStr string, operator bleveQuery.MatchQueryOperator) int {
	t.Helper()

	clause := bleve.NewMatchQuery(queryStr)
	clause.SetField(field)
	clause.SetFuzziness(service.fuzziness(field))
	clause.SetOperator(operator)

	request := bleve.NewSearchRequest(clause)
	request.Size = 100

	result, err := service.index.Search(request)
	require.NoError(t, err)
	return len(result.Hits)
}

// TestGolden_RequiringEveryTermEmptiesTheShortFields measures why #92 declined
// MatchQueryOperatorAnd, and is the regression gate on that decision together
// with the golden case above.
//
// The operator applies inside a field clause, not across a document, so
// requiring every term requires them all in one field. heading_path,
// source_title and path_labels are short by construction — a file path is a
// handful of labels, a heading path a handful of words — and cannot hold a
// whole query. On a query every field retrieves under the shipped operator,
// requiring every term takes all four to zero and leaves a five-field ranking
// model with nothing to rank.
//
// Expectations are measured, not desired. A change means the retrieval model
// moved.
func TestGolden_RequiringEveryTermEmptiesTheShortFields(t *testing.T) {
	const probe = "401 unauthorized troubleshooting"

	service := goldenService(t, zeroConfigCorpus)

	tests := []struct {
		field string
		// wantAny is the candidate count under the shipped OR operator.
		wantAny int
		// wantAll is the count when every term is required instead.
		wantAll int
	}{
		{domain.FieldHeadingPath, 5, 0},
		{domain.FieldSourceTitle, 5, 0},
		{domain.FieldPathLabels, 5, 0},
		{domain.FieldContent, 6, 0},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			anyTerm := fieldClauseCandidates(t, service, tt.field, probe, bleveQuery.MatchQueryOperatorOr)
			everyTerm := fieldClauseCandidates(t, service, tt.field, probe, bleveQuery.MatchQueryOperatorAnd)
			t.Logf("%s — any term: %d candidates, every term: %d", tt.field, anyTerm, everyTerm)

			require.Equal(t, tt.wantAny, anyTerm, "%s under the shipped operator", tt.field)
			require.Equal(t, tt.wantAll, everyTerm, "%s when every term is required", tt.field)
		})
	}
}
