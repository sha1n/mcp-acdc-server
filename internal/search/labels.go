package search

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

// labelsField holds each chunk's normalised labels as whole-value keyword
// terms, so a label filter is an exact match and never a token match.
const labelsField = "labels"

var browseSortOrder = []string{domain.FieldSourceURI, domain.FieldStartLine, domain.FieldChunkID}

// indexedChunk is the document bleve indexes for a chunk: the chunk's own
// fields plus its labels, which domain.Chunk derives rather than stores.
type indexedChunk struct {
	domain.Chunk
	Labels []string `json:"labels"`
}

func newIndexedChunk(chunk domain.Chunk) indexedChunk {
	return indexedChunk{Chunk: chunk, Labels: chunk.Labels()}
}

// LabelledResult is a search hit enriched for the web UI.
type LabelledResult struct {
	ChunkID     string
	SourceURI   string
	ChunkURI    string
	SourceTitle string
	SourcePath  string
	HeadingPath []string
	Labels      []string
	StartLine   int
	EndLine     int
	Score       float64
	Snippet     []SnippetSpan
	Content     string
}

// LabelCount is the number of indexed chunks carrying a label.
type LabelCount struct {
	Name  string
	Count int
}

// LabelledSearch runs a text query narrowed to chunks carrying every label.
// Without a query it browses the labelled chunks in document order, unscored.
func (s *Service) LabelledSearch(queryStr string, labels []string, limit int) ([]LabelledResult, error) {
	queryStr = strings.TrimSpace(queryStr)
	labels = domain.Labels(labels, nil)
	if queryStr == "" && len(labels) == 0 {
		return []LabelledResult{}, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return []LabelledResult{}, nil
	}

	request := s.labelledSearchRequest(queryStr, labels)
	request.Size = s.resultLimit(limit)
	searchResult, err := s.index.Search(request)
	if err != nil {
		return nil, fmt.Errorf("labelled search failed: %w", err)
	}

	scored := queryStr != ""
	results := make([]LabelledResult, 0, len(searchResult.Hits))
	for _, hit := range searchResult.Hits {
		results = append(results, labelledResult(hit, scored))
	}
	return results, nil
}

// labelledSearchRequest filters at boost 0 so that a text query scores and
// ranks exactly as Search does over the chunks that survive the filter.
func (s *Service) labelledSearchRequest(queryStr string, labels []string) *bleve.SearchRequest {
	filters := make([]query.Query, 0, len(labels)+1)
	for _, label := range labels {
		filter := bleve.NewTermQuery(label)
		filter.SetField(labelsField)
		filter.SetBoost(0)
		filters = append(filters, filter)
	}

	if queryStr == "" {
		request := bleve.NewSearchRequest(bleve.NewConjunctionQuery(filters...))
		request.Fields = []string{"*"}
		request.SortBy(browseSortOrder)
		return request
	}

	q := s.textQuery(queryStr)
	if len(filters) > 0 {
		q = bleve.NewConjunctionQuery(append([]query.Query{q}, filters...)...)
	}
	request := bleve.NewSearchRequest(q)
	request.Fields = []string{"*"}
	request.IncludeLocations = true
	request.SortBy(rankedSortOrder)
	return request
}

func labelledResult(hit *search.DocumentMatch, scored bool) LabelledResult {
	content := fieldString(hit.Fields, domain.FieldContent)
	title := fieldString(hit.Fields, domain.FieldSourceTitle)
	result := LabelledResult{
		ChunkID:     fieldStringOr(hit.Fields, domain.FieldChunkID, hit.ID),
		SourceURI:   fieldString(hit.Fields, domain.FieldSourceURI),
		ChunkURI:    fieldString(hit.Fields, domain.FieldChunkURI),
		SourceTitle: title,
		SourcePath:  fieldString(hit.Fields, domain.FieldSourcePath),
		HeadingPath: nonNilStrings(fieldStrings(hit.Fields, domain.FieldHeadingPath)),
		Labels:      nonNilStrings(fieldStrings(hit.Fields, labelsField)),
		StartLine:   fieldInt(hit.Fields, domain.FieldStartLine),
		EndLine:     fieldInt(hit.Fields, domain.FieldEndLine),
		Snippet:     snippetSpans(hit.Locations, content, title),
		Content:     content,
	}
	if scored {
		result.Score = hit.Score
	}
	return result
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// Labels lists indexed labels starting with prefix, most used first.
func (s *Service) Labels(prefix string, limit int) ([]LabelCount, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return []LabelCount{}, nil
	}

	var q query.Query = bleve.NewMatchAllQuery()
	if prefix != "" {
		prefixQuery := bleve.NewPrefixQuery(prefix)
		prefixQuery.SetField(labelsField)
		q = prefixQuery
	}
	request := bleve.NewSearchRequestOptions(q, 0, 0, false)
	// The facet must report every distinct label: truncating it would rank by
	// bleve's ordering before the prefix filter and limit below are applied.
	request.AddFacet(labelsField, bleve.NewFacetRequest(labelsField, math.MaxInt))

	searchResult, err := s.index.Search(request)
	if err != nil {
		return nil, fmt.Errorf("label listing failed: %w", err)
	}
	return labelCounts(searchResult.Facets[labelsField], prefix, limit), nil
}

// labelCounts keeps the facet terms starting with prefix. A prefix query
// selects chunks with at least one matching label, so the facet also counts
// those chunks' other labels.
func labelCounts(facet *search.FacetResult, prefix string, limit int) []LabelCount {
	counts := []LabelCount{}
	for _, term := range facet.Terms.Terms() {
		if strings.HasPrefix(term.Term, prefix) {
			counts = append(counts, LabelCount{Name: term.Term, Count: term.Count})
		}
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Count != counts[j].Count {
			return counts[i].Count > counts[j].Count
		}
		return counts[i].Name < counts[j].Name
	})
	if limit > 0 && len(counts) > limit {
		counts = counts[:limit]
	}
	return counts
}
