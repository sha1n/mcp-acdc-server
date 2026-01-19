package search

import (
	"fmt"
	"os"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
)

// SearchResult represents a search result
type SearchResult struct {
	URI     string
	Name    string
	Snippet string
}

// Document represents a document to index
type Document struct {
	URI     string `json:"uri"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Searcher interface in search package
type Searcher interface {
	Search(queryStr string, limit *int) ([]SearchResult, error)
	IndexDocuments(documents []Document) error
	Close()
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

// IndexDocuments indexes a list of documents
func (s *Service) IndexDocuments(documents []Document) error {
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

	// Batch index
	batch := index.NewBatch()
	for _, doc := range documents {
		if err := batch.Index(doc.URI, doc); err != nil {
			return fmt.Errorf("failed to add document to batch: %w", err)
		}
	}

	if err := index.Batch(batch); err != nil {
		return fmt.Errorf("failed to execute batch index: %w", err)
	}

	return nil
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
	nameMapping.Analyzer = "standard"

	// Content field: Indexed, Not Stored, Included in All
	contentMapping := bleve.NewTextFieldMapping()
	contentMapping.Store = true // DEBUG: Store content to ensure we can see it
	contentMapping.IncludeInAll = true
	contentMapping.Analyzer = "standard"

	docMapping := bleve.NewDocumentMapping()
	docMapping.AddFieldMappingsAt("uri", uriMapping)
	docMapping.AddFieldMappingsAt("name", nameMapping)
	docMapping.AddFieldMappingsAt("content", contentMapping)

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

	// Match query (searches across all fields included in 'all')
	// Python uses parse_query with default fields ["name", "content"]
	// Bleve's QueryStringQuery is similar
	var query query.Query
	if queryStr == "*" {
		query = bleve.NewMatchAllQuery()
	} else {
		query = bleve.NewQueryStringQuery(queryStr)
	}

	searchRequest := bleve.NewSearchRequest(query)
	searchRequest.Size = maxResults
	searchRequest.Fields = []string{"uri", "name", "content"} // Retrieve content too

	searchResult, err := s.index.Search(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	results := make([]SearchResult, 0, len(searchResult.Hits))
	for _, hit := range searchResult.Hits {
		uri, _ := hit.Fields["uri"].(string) // Relaxed check
		name, _ := hit.Fields["name"].(string)

		// Replicate Python snippet behavior
		snippet := fmt.Sprintf("%s (relevance: %.2f)", name, hit.Score)

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
