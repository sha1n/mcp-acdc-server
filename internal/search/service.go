package search

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

const (
	headingBoost = 2.5
	pathBoost    = 1.25
)

var (
	newMemoryIndex = bleve.NewMemOnly
	makeTempDir    = os.MkdirTemp
	removeAll      = os.RemoveAll
	newDiskIndex   = bleve.New
)

// SearchResult is a chunk returned by a search.
type SearchResult struct {
	ChunkID     string
	SourceID    string
	SourceURI   string
	ChunkURI    string
	SourceTitle string
	SourcePath  string
	HeadingPath []string
	StartLine   int
	EndLine     int
	Score       float64
	Snippet     string
	Content     string
}

// Searcher indexes and searches document chunks.
type Searcher interface {
	Search(query string, candidateLimit int) ([]SearchResult, error)
	Index(ctx context.Context, chunks <-chan domain.Chunk) error
	ReplaceSource(ctx context.Context, sourceID string, chunks []domain.Chunk) error
	DeleteSource(ctx context.Context, sourceID string) error
	Close()
}

// BatchIndexer is a subset of bleve.Index needed for batch indexing
type BatchIndexer interface {
	NewBatch() *bleve.Batch
	Batch(b *bleve.Batch) error
}

type searchIndex interface {
	BatchIndexer
	Search(request *bleve.SearchRequest) (*bleve.SearchResult, error)
	Close() error
	DocCount() (uint64, error)
}

// Service search service using Bleve
type Service struct {
	mu           sync.RWMutex
	settings     config.SearchSettings
	index        searchIndex
	indexDir     string
	sourceChunks map[string][]string
}

// Ensure Service implements Searcher
var _ Searcher = (*Service)(nil)

// NewService creates a new search service
func NewService(settings config.SearchSettings) *Service {
	return &Service{
		settings:     settings,
		sourceChunks: make(map[string][]string),
	}
}

// Index rebuilds the index from a stream of chunks.
func (s *Service) Index(ctx context.Context, chunks <-chan domain.Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	index, indexDir, err := s.newIndex()
	if err != nil {
		return err
	}
	newSourceChunks := make(map[string][]string)
	if err := batchIndex(ctx, index, chunks, newSourceChunks); err != nil {
		_ = index.Close()
		if indexDir != "" {
			_ = removeAll(indexDir)
		}
		return err
	}

	oldIndex, oldIndexDir := s.index, s.indexDir
	s.index = index
	s.indexDir = indexDir
	s.sourceChunks = newSourceChunks
	if oldIndex != nil {
		_ = oldIndex.Close()
	}
	if oldIndexDir != "" && oldIndexDir != indexDir {
		_ = removeAll(oldIndexDir)
	}
	return nil
}

func (s *Service) newIndex() (searchIndex, string, error) {
	indexMapping := buildMapping()
	if s.settings.InMemory {
		index, err := newMemoryIndex(indexMapping)
		if err != nil {
			return nil, "", fmt.Errorf("failed to create index: %w", err)
		}
		return index, "", nil
	}

	indexDir, err := makeTempDir("", "acdc_search_")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	if err := removeAll(indexDir); err != nil {
		return nil, "", fmt.Errorf("failed to remove temp dir: %w", err)
	}
	index, err := newDiskIndex(indexDir, indexMapping)
	if err != nil {
		_ = removeAll(indexDir)
		return nil, "", fmt.Errorf("failed to create index: %w", err)
	}
	return index, indexDir, nil
}

func (s *Service) batchIndex(ctx context.Context, index BatchIndexer, chunks <-chan domain.Chunk) error {
	return batchIndex(ctx, index, chunks, s.sourceChunks)
}

func batchIndex(ctx context.Context, index BatchIndexer, chunks <-chan domain.Chunk, sourceChunks map[string][]string) error {
	batch := index.NewBatch()
	batchSize := 100
	count := 0
	pendingSources := make(map[string][]string)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				if count > 0 {
					if err := index.Batch(batch); err != nil {
						return fmt.Errorf("failed to execute final batch index: %w", err)
					}
					addSourceChunks(sourceChunks, pendingSources)
				}
				return nil
			}

			if err := batch.Index(chunk.ID, chunk); err != nil {
				return fmt.Errorf("failed to add chunk to batch: %w", err)
			}
			pendingSources[chunk.SourceID] = append(pendingSources[chunk.SourceID], chunk.ID)
			count++

			if count >= batchSize {
				if err := index.Batch(batch); err != nil {
					return fmt.Errorf("failed to execute batch index: %w", err)
				}
				addSourceChunks(sourceChunks, pendingSources)
				batch = index.NewBatch()
				count = 0
				pendingSources = make(map[string][]string)
			}
		}
	}
}

func addSourceChunks(destination, sourceChunks map[string][]string) {
	for sourceID, chunkIDs := range sourceChunks {
		destination[sourceID] = append(destination[sourceID], chunkIDs...)
	}
}

func buildMapping() mapping.IndexMapping {
	storedIdentifier := bleve.NewKeywordFieldMapping()
	storedIdentifier.Store = true
	storedIdentifier.IncludeInAll = false

	searchableText := bleve.NewTextFieldMapping()
	searchableText.Store = true
	searchableText.IncludeInAll = true
	searchableText.Analyzer = "en"

	lineNumber := bleve.NewNumericFieldMapping()
	lineNumber.Store = true
	lineNumber.IncludeInAll = false

	chunkMapping := bleve.NewDocumentMapping()
	for _, field := range []string{
		domain.FieldChunkID,
		domain.FieldSourceID,
		domain.FieldSourceURI,
		domain.FieldChunkURI,
		domain.FieldSourcePath,
	} {
		chunkMapping.AddFieldMappingsAt(field, storedIdentifier)
	}
	for _, field := range []string{
		domain.FieldSourceTitle,
		domain.FieldPathLabels,
		domain.FieldHeadingPath,
		domain.FieldContent,
		domain.FieldKeywords,
	} {
		chunkMapping.AddFieldMappingsAt(field, searchableText)
	}
	chunkMapping.AddFieldMappingsAt(domain.FieldStartLine, lineNumber)
	chunkMapping.AddFieldMappingsAt(domain.FieldEndLine, lineNumber)

	indexMapping := bleve.NewIndexMapping()
	indexMapping.DefaultMapping = chunkMapping
	return indexMapping
}

// Search finds chunks matching query.
func (s *Service) Search(queryStr string, candidateLimit int) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return []SearchResult{}, nil
	}

	maxResults := s.settings.MaxResults
	if candidateLimit > 0 {
		maxResults = candidateLimit
	}

	// Build query with keyword boosting
	// Use DisjunctionQuery to search multiple fields with different boosts
	var q query.Query
	if queryStr == "*" {
		q = bleve.NewMatchAllQuery()
	} else {
		q = bleve.NewDisjunctionQuery(
			fieldQuery(queryStr, domain.FieldKeywords, s.settings.KeywordsBoost),
			fieldQuery(queryStr, domain.FieldHeadingPath, headingBoost),
			fieldQuery(queryStr, domain.FieldSourceTitle, s.settings.NameBoost),
			fieldQuery(queryStr, domain.FieldPathLabels, pathBoost),
			fieldQuery(queryStr, domain.FieldContent, s.settings.ContentBoost),
		)
	}

	searchRequest := bleve.NewSearchRequest(q)
	searchRequest.Size = maxResults
	searchRequest.Fields = []string{"*"}
	searchRequest.Highlight = bleve.NewHighlight()
	searchRequest.SortBy([]string{"-_score", domain.FieldSourceURI, domain.FieldChunkID})

	searchResult, err := s.index.Search(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	results := make([]SearchResult, 0, len(searchResult.Hits))
	for _, hit := range searchResult.Hits {
		snippet := fieldString(hit.Fields, domain.FieldContent)
		if fragments, ok := hit.Fragments[domain.FieldContent]; ok && len(fragments) > 0 {
			snippet = fragments[0]
		}
		if snippet == "" {
			snippet = fieldString(hit.Fields, domain.FieldSourceTitle)
		}

		results = append(results, SearchResult{
			ChunkID:     fieldStringOr(hit.Fields, domain.FieldChunkID, hit.ID),
			SourceID:    fieldString(hit.Fields, domain.FieldSourceID),
			SourceURI:   fieldString(hit.Fields, domain.FieldSourceURI),
			ChunkURI:    fieldString(hit.Fields, domain.FieldChunkURI),
			SourceTitle: fieldString(hit.Fields, domain.FieldSourceTitle),
			SourcePath:  fieldString(hit.Fields, domain.FieldSourcePath),
			HeadingPath: fieldStrings(hit.Fields, domain.FieldHeadingPath),
			StartLine:   fieldInt(hit.Fields, domain.FieldStartLine),
			EndLine:     fieldInt(hit.Fields, domain.FieldEndLine),
			Score:       hit.Score,
			Snippet:     snippet,
			Content:     fieldString(hit.Fields, domain.FieldContent),
		})
	}

	return results, nil
}

func fieldQuery(queryStr, field string, boost float64) *query.MatchQuery {
	fieldQuery := bleve.NewMatchQuery(queryStr)
	fieldQuery.SetField(field)
	fieldQuery.SetFuzziness(1)
	fieldQuery.SetBoost(boost)
	return fieldQuery
}

func fieldString(fields map[string]interface{}, field string) string {
	value, _ := fields[field].(string)
	return value
}

func fieldStringOr(fields map[string]interface{}, field, fallback string) string {
	if value := fieldString(fields, field); value != "" {
		return value
	}
	return fallback
}

func fieldStrings(fields map[string]interface{}, field string) []string {
	switch value := fields[field].(type) {
	case []string:
		return append([]string(nil), value...)
	case []interface{}:
		values := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				values = append(values, text)
			}
		}
		return values
	case string:
		return []string{value}
	default:
		return nil
	}
}

func fieldInt(fields map[string]interface{}, field string) int {
	switch value := fields[field].(type) {
	case float64:
		return int(value)
	case float32:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	default:
		return 0
	}
}

// ReplaceSource atomically removes a source's known chunks and indexes its replacements.
func (s *Service) ReplaceSource(ctx context.Context, sourceID string, chunks []domain.Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if s.index == nil {
		return fmt.Errorf("search index is not initialized")
	}

	batch := s.index.NewBatch()
	for _, chunkID := range s.sourceChunks[sourceID] {
		batch.Delete(chunkID)
	}
	for _, chunk := range chunks {
		if err := batch.Index(chunk.ID, chunk); err != nil {
			return fmt.Errorf("failed to add replacement chunk to batch: %w", err)
		}
	}
	if err := s.index.Batch(batch); err != nil {
		return fmt.Errorf("failed to replace source chunks: %w", err)
	}

	chunkIDs := make([]string, len(chunks))
	for i, chunk := range chunks {
		chunkIDs[i] = chunk.ID
	}
	s.sourceChunks[sourceID] = chunkIDs
	return nil
}

// DeleteSource atomically removes all known chunks for sourceID.
func (s *Service) DeleteSource(ctx context.Context, sourceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if s.index == nil {
		return nil
	}
	chunkIDs := s.sourceChunks[sourceID]
	if len(chunkIDs) == 0 {
		return nil
	}

	batch := s.index.NewBatch()
	for _, chunkID := range chunkIDs {
		batch.Delete(chunkID)
	}
	if err := s.index.Batch(batch); err != nil {
		return fmt.Errorf("failed to delete source chunks: %w", err)
	}
	delete(s.sourceChunks, sourceID)
	return nil
}

// Close cleans up resources
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.index != nil {
		_ = s.index.Close()
		s.index = nil
	}
	if s.indexDir != "" {
		_ = removeAll(s.indexDir)
		s.indexDir = ""
	}
}

// DocCount returns number of docs in index
func (s *Service) DocCount() (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return 0, nil
	}
	return s.index.DocCount()
}
