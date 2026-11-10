package search

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

// fieldLabels is search-internal: it is derived at index time and is not part
// of domain.Chunk.
const fieldLabels = "labels"

// indexedChunk is the document bleve indexes. The embedded Chunk's fields are
// promoted by bleve's mapper, so existing field paths are unchanged.
type indexedChunk struct {
	domain.Chunk
	Labels []string `json:"labels"`
}

func newIndexedChunk(chunk domain.Chunk) indexedChunk {
	return indexedChunk{Chunk: chunk, Labels: chunkLabels(chunk)}
}

func chunkLabels(chunk domain.Chunk) []string {
	labels := make([]string, 0, len(chunk.PathLabels)+len(chunk.Keywords))
	seen := make(map[string]struct{}, cap(labels))
	for _, group := range [][]string{chunk.PathLabels, chunk.Keywords} {
		for _, label := range group {
			name := normalizeLabel(label)
			if name == "" {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			labels = append(labels, name)
		}
	}
	return labels
}

func normalizeLabel(label string) string {
	return strings.ToLower(strings.TrimSpace(label))
}

func normalizeLabels(labels []string) []string {
	normalized := make([]string, 0, len(labels))
	for _, label := range labels {
		if name := normalizeLabel(label); name != "" {
			normalized = append(normalized, name)
		}
	}
	return normalized
}

// labelMapping indexes labels as whole lower-cased terms so filtering is exact
// and the field dictionary yields one entry per label.
func labelMapping() *mapping.FieldMapping {
	m := bleve.NewTextFieldMapping()
	m.Analyzer = keyword.Name
	m.Store = true
	m.IncludeInAll = false
	return m
}

// SearchLabeled finds chunks matching query that carry every given label.
// An empty query with labels matches all chunks carrying those labels; an
// empty query without labels matches nothing.
func (s *Service) SearchLabeled(queryStr string, labels []string, limit int) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	queryStr = strings.TrimSpace(queryStr)
	labels = normalizeLabels(labels)
	if s.index == nil || (queryStr == "" && len(labels) == 0) {
		return []SearchResult{}, nil
	}

	hits, err := s.execute(s.labeledQuery(queryStr, labels), s.resultLimit(limit))
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(hits))
	for _, hit := range hits {
		result := toSearchResult(hit)
		result.Labels = fieldStrings(hit.Fields, fieldLabels)
		if result.Labels == nil {
			result.Labels = []string{}
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Service) labeledQuery(queryStr string, labels []string) query.Query {
	if len(labels) == 0 {
		return s.textQuery(queryStr)
	}

	clauses := make([]query.Query, 0, len(labels)+1)
	if queryStr != "" {
		clauses = append(clauses, s.textQuery(queryStr))
	}
	for _, label := range labels {
		filter := bleve.NewTermQuery(label)
		filter.SetField(fieldLabels)
		// A label is a filter, not a relevance signal: with a text query
		// present, a zero boost keeps ranking identical to the unfiltered
		// search. Without one the default boost is kept, because an all-zero
		// query weight yields a NaN query norm.
		if queryStr != "" {
			filter.SetBoost(0)
		}
		clauses = append(clauses, filter)
	}
	return bleve.NewConjunctionQuery(clauses...)
}

// Labels lists labels whose name starts with prefix (case-insensitive), with
// the number of chunks carrying each, ordered by count descending then name.
// A non-positive limit returns all matches.
func (s *Service) Labels(prefix string, limit int) ([]LabelCount, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return []LabelCount{}, nil
	}
	idx, ok := s.index.(bleve.Index)
	if !ok {
		return nil, errors.New("label listing is not supported by this index")
	}

	labels, err := readLabelDictionary(idx, normalizeLabel(prefix))
	if err != nil {
		return nil, err
	}

	sort.Slice(labels, func(i, j int) bool {
		if labels[i].Count != labels[j].Count {
			return labels[i].Count > labels[j].Count
		}
		return labels[i].Name < labels[j].Name
	})
	if limit > 0 && len(labels) > limit {
		labels = labels[:limit]
	}
	return labels, nil
}

func readLabelDictionary(idx bleve.Index, prefix string) (labels []LabelCount, err error) {
	dict, err := idx.FieldDictPrefix(fieldLabels, []byte(prefix))
	if err != nil {
		return nil, fmt.Errorf("failed to read labels: %w", err)
	}
	defer func() {
		if closeErr := dict.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close label dictionary: %w", closeErr)
		}
	}()

	labels = []LabelCount{}
	for {
		entry, nextErr := dict.Next()
		if nextErr != nil {
			return nil, fmt.Errorf("failed to read labels: %w", nextErr)
		}
		if entry == nil {
			return labels, nil
		}
		labels = append(labels, LabelCount{Name: entry.Term, Count: int(entry.Count)})
	}
}
