package search

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

// SearchResult represents a search result
type SearchResult struct {
	URI     string
	Name    string
	Snippet string
}

// Searcher interface in search package
type Searcher interface {
	Search(queryStr string, limit *int) ([]SearchResult, error)
	Index(ctx context.Context, documents <-chan domain.Document) error
	Close()
}

// BatchIndexer is a subset of bleve.Index needed for batch indexing
type BatchIndexer interface {
	NewBatch() *bleve.Batch
	Batch(b *bleve.Batch) error
}

// Service search service using Bleve
type Service struct {
	settings config.SearchSettings
	index    bleve.Index
	indexDir string
}

// Ensure Service implements Searcher
var _ Searcher = (*Service)(nil)

// NewService creates a new search service
func NewService(settings config.SearchSettings) *Service {
	return &Service{
		settings: settings,
	}
}

// Index indexes a stream of documents
func (s *Service) Index(ctx context.Context, documents <-chan domain.Document) error {
	// Close existing index if any
	if s.index != nil {
		_ = s.index.Close()
		s.index = nil
	}
	if s.indexDir != "" {
		_ = os.RemoveAll(s.indexDir)
	}

	// Define mapping
	indexMapping := buildMapping()

	var index bleve.Index
	var err error

	if s.settings.InMemory {
		index, err = bleve.NewMemOnly(indexMapping)
	} else {
		// Create temp dir
		var mkErr error
		tempDir, mkErr := os.MkdirTemp("", "acdc_search_")
		if mkErr != nil {
			return fmt.Errorf("failed to create temp dir: %w", mkErr)
		}
		// bleve.New requires the directory to not exist
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			return fmt.Errorf("failed to remove temp dir: %w", rmErr)
		}
		s.indexDir = tempDir

		index, err = bleve.New(s.indexDir, indexMapping)
	}

	if err != nil {
		return fmt.Errorf("failed to create index: %w", err)
	}
	s.index = index

	return s.batchIndex(ctx, s.index, documents)
}

func (s *Service) batchIndex(ctx context.Context, index BatchIndexer, documents <-chan domain.Document) error {
	// Batch index
	batch := index.NewBatch()
	batchSize := 100 // configurable?
	count := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case doc, ok := <-documents:
			if !ok {
				// Channel closed, flush remaining
				if count > 0 {
					if err := index.Batch(batch); err != nil {
						return fmt.Errorf("failed to execute final batch index: %w", err)
					}
				}
				return nil
			}

			if err := batch.Index(doc.URI, doc); err != nil {
				return fmt.Errorf("failed to add document to batch: %w", err)
			}
			count++

			if count >= batchSize {
				if err := index.Batch(batch); err != nil {
					return fmt.Errorf("failed to execute batch index: %w", err)
				}
				batch = index.NewBatch()
				count = 0
			}
		}
	}
}

func buildMapping() mapping.IndexMapping {
	// URI field: Stored, Indexed
	uriMapping := bleve.NewTextFieldMapping()
	uriMapping.Store = true
	uriMapping.IncludeInAll = false

	// Name field: Stored, Indexed, Included in All (default)
	nameMapping := bleve.NewTextFieldMapping()
	nameMapping.Store = true
	nameMapping.IncludeInAll = true
	nameMapping.Analyzer = "en"

	// Content field: Indexed, Not Stored, Included in All
	contentMapping := bleve.NewTextFieldMapping()
	contentMapping.Store = true // DEBUG: Store content to ensure we can see it
	contentMapping.IncludeInAll = true
	contentMapping.Analyzer = "en"

	// Keywords field: Indexed, Not Stored, Included in All
	// Boosting is done at query-time via DisjunctionQuery
	keywordsMapping := bleve.NewTextFieldMapping()
	keywordsMapping.Store = false
	keywordsMapping.IncludeInAll = true
	keywordsMapping.Analyzer = "en"

	docMapping := bleve.NewDocumentMapping()
	docMapping.AddFieldMappingsAt(domain.FieldURI, uriMapping)
	docMapping.AddFieldMappingsAt(domain.FieldName, nameMapping)
	docMapping.AddFieldMappingsAt(domain.FieldContent, contentMapping)
	docMapping.AddFieldMappingsAt(domain.FieldKeywords, keywordsMapping)

	mapping := bleve.NewIndexMapping()
	mapping.DefaultMapping = docMapping
	return mapping
}

// Search searches for resources
func (s *Service) Search(queryStr string, limit *int) ([]SearchResult, error) {
	if s.index == nil {
		return []SearchResult{}, nil
	}

	maxResults := s.settings.MaxResults
	if limit != nil {
		maxResults = *limit
	}

	// Build query with keyword boosting
	// Use DisjunctionQuery to search multiple fields with different boosts
	var q query.Query
	if queryStr == "*" {
		q = bleve.NewMatchAllQuery()
	} else {
		// Create field-specific queries with boosting and fuzziness
		nameQuery := bleve.NewMatchQuery(queryStr)
		nameQuery.SetField(domain.FieldName)
		nameQuery.SetFuzziness(1)
		nameQuery.SetBoost(s.settings.NameBoost)

		contentQuery := bleve.NewMatchQuery(queryStr)
		contentQuery.SetField(domain.FieldContent)
		contentQuery.SetFuzziness(1)
		contentQuery.SetBoost(s.settings.ContentBoost)

		keywordsQuery := bleve.NewMatchQuery(queryStr)
		keywordsQuery.SetField(domain.FieldKeywords)
		keywordsQuery.SetFuzziness(1)
		keywordsQuery.SetBoost(s.settings.KeywordsBoost)

		// DisjunctionQuery combines results, boosted fields will score higher
		q = bleve.NewDisjunctionQuery(nameQuery, contentQuery, keywordsQuery)
	}

	searchRequest := bleve.NewSearchRequest(q)
	searchRequest.Size = maxResults
	searchRequest.Fields = []string{domain.FieldURI, domain.FieldName, domain.FieldContent}
	searchRequest.Highlight = bleve.NewHighlight()

	searchResult, err := s.index.Search(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	results := make([]SearchResult, 0, len(searchResult.Hits))
	for _, hit := range searchResult.Hits {
		uri, ok := hit.Fields[domain.FieldURI].(string)
		if !ok {
			slog.Warn("Search hit missing URI field", "id", hit.ID)
			continue
		}

		name, ok := hit.Fields[domain.FieldName].(string)
		if !ok || name == "" {
			name = "Unknown" // Fallback
		}

		// Improved snippet generation with highlighting
		snippet := fmt.Sprintf("%s (relevance: %.2f)", name, hit.Score)
		if fragments, ok := hit.Fragments[domain.FieldContent]; ok && len(fragments) > 0 {
			snippet = fmt.Sprintf("%s... (relevance: %.2f)", fragments[0], hit.Score)
		}

		results = append(results, SearchResult{
			URI:     uri,
			Name:    name,
			Snippet: snippet,
		})
	}

	return results, nil
}

// Close cleans up resources
func (s *Service) Close() {
	if s.index != nil {
		_ = s.index.Close()
	}
	if s.indexDir != "" {
		_ = os.RemoveAll(s.indexDir)
	}
}

// DocCount returns number of docs in index
func (s *Service) DocCount() (uint64, error) {
	if s.index == nil {
		return 0, nil
	}
	return s.index.DocCount()
}
