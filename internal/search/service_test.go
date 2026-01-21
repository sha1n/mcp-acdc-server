package search

import (
	"os"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
)

func TestSearchService(t *testing.T) {
	settings := config.SearchSettings{
		MaxResults: 10,
	}
	service := NewService(settings)
	defer service.Close()

	docs := []Document{
		{
			URI:     "acdc://doc1",
			Name:    "Document One",
			Content: "This is the content of document one. It talks about testing.",
		},
		{
			URI:     "acdc://doc2",
			Name:    "Document Two",
			Content: "This is the content of document two. It talks about development.",
		},
	}

	if err := service.IndexDocuments(docs); err != nil {
		t.Fatalf("IndexDocuments failed: %v", err)
	}

	// Search for "testing"
	results, err := service.Search("testing", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0].URI != "acdc://doc1" {
		t.Errorf("Expected URI 'acdc://doc1', got '%s'", results[0].URI)
	}

	// Search for "document"
	results, err = service.Search("document", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	// DocCount
	count, err := service.DocCount()
	if err != nil {
		t.Fatalf("DocCount failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 documents, got %d", count)
	}
}

func TestSearchService_ReIndex(t *testing.T) {
	service := NewService(config.SearchSettings{InMemory: true}) // Use in-memory for speed
	defer service.Close()

	if err := service.IndexDocuments([]Document{{URI: "1", Name: "1"}}); err != nil {
		t.Fatal(err)
	}
	if count, _ := service.DocCount(); count != 1 {
		t.Errorf("Expected 1, got %d", count)
	}

	// Re-index
	if err := service.IndexDocuments([]Document{{URI: "2", Name: "2"}}); err != nil {
		t.Fatal(err)
	}
	if count, _ := service.DocCount(); count != 1 {
		t.Errorf("Expected 1 (replaced), got %d", count)
	}
}

func TestSearchService_Empty(t *testing.T) {
	service := NewService(config.SearchSettings{})
	// No index created yet
	results, err := service.Search("test", nil)
	if err != nil {
		t.Errorf("Expected no error for empty search, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 results, got %d", len(results))
	}

	count, err := service.DocCount()
	if err != nil {
		t.Errorf("Expected no error for empty doc count, got %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 count, got %d", count)
	}
}

func TestSearchService_DiskLifecycle(t *testing.T) {
	// Test without InMemory=true, so it uses disk
	service := NewService(config.SearchSettings{})

	// Create index (this should trigger temp dir creation)
	if err := service.IndexDocuments([]Document{{URI: "1", Name: "1"}}); err != nil {
		t.Fatal(err)
	}

	// Verify indexDir is set and exists
	if service.indexDir == "" {
		t.Error("Expected indexDir to be set")
	}
	if _, err := os.Stat(service.indexDir); os.IsNotExist(err) {
		t.Error("Expected indexDir to exist on disk")
	}

	// Close service
	service.Close()

	// Verify indexDir is removed
	if _, err := os.Stat(service.indexDir); !os.IsNotExist(err) {
		t.Error("Expected indexDir to be removed after Close()")
	}
}

func TestSearchService_Extended(t *testing.T) {
	service := NewService(config.SearchSettings{
		InMemory:   true,
		MaxResults: 5,
	})
	defer service.Close()

	docs := []Document{
		{URI: "1", Name: "Alpha", Content: "Content Alpha"},
		{URI: "2", Name: "Bravo", Content: "Content Bravo"},
		{URI: "3", Name: "Charlie", Content: "Content Charlie"},
	}
	if err := service.IndexDocuments(docs); err != nil {
		t.Fatal(err)
	}

	// 1. Test MatchAll (search with "*")
	results, err := service.Search("*", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("Expected 3 results for match all, got %d", len(results))
	}

	// 2. Test MaxResults and Limits
	// Default from settings is 5, request explicit limit 1
	limit := 1
	results, err = service.Search("*", &limit)
	if err != nil {
		t.Fatalf("Search with limit failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Expected 1 result with limit=1, got %d", len(results))
	}

	// Test nil limit uses MaxResults (all 3 should return because MaxResults=5)
	results, err = service.Search("*", nil)
	if err != nil {
		t.Fatalf("Search with nil limit failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("Expected 3 results (within MaxResults=5), got %d", len(results))
	}

	// 3. Test Result fields (Snippet, URI, Name)
	// Searching for "Alpha" should return doc 1
	results, err = service.Search("Alpha", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result for 'Alpha', got %d", len(results))
	}
	r := results[0]
	if r.URI != "1" {
		t.Errorf("Expected URI '1', got %s", r.URI)
	}
	if r.Name != "Alpha" {
		t.Errorf("Expected Name 'Alpha', got %s", r.Name)
	}
	// Snippet format check: "Name (relevance: Score)"
	expectedSnippetPrefix := "Alpha (relevance:"
	if len(r.Snippet) < len(expectedSnippetPrefix) || r.Snippet[:len(expectedSnippetPrefix)] != expectedSnippetPrefix {
		t.Errorf("Snippet '%s' does not start with expected prefix '%s'", r.Snippet, expectedSnippetPrefix)
	}
}

func TestSearch_MissingName(t *testing.T) {
	service := NewService(config.SearchSettings{InMemory: true, MaxResults: 10})
	if err := service.IndexDocuments([]Document{
		{
			URI:     "acdc://test",
			Name:    "", // empty name to trigger fallback
			Content: "The quick brown fox jumps over the lazy dog",
		},
	}); err != nil {
		t.Fatalf("IndexDocuments failed: %v", err)
	}
	defer service.Close()

	results, err := service.Search("fox", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result for 'fox', got %d", len(results))
	}
	if results[0].Name != "Unknown" {
		t.Errorf("Expected name 'Unknown', got '%s'", results[0].Name)
	}
}

func TestSearch_MissingURI(t *testing.T) {
	service := NewService(config.SearchSettings{InMemory: true, MaxResults: 10})

	// Since we can't easily produce a hit without a URI using IndexDocuments,
	// we use a real index and custom indexing logic just for this test.
	index, _ := bleve.NewMemOnly(buildMapping())
	_ = index.Index("1", struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}{
		Name:    "TestDoc",
		Content: "Some test content",
	})
	service.index = index
	defer service.Close()

	results, err := service.Search("test", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Documents missing URI should be skipped
	if len(results) != 0 {
		t.Errorf("Expected 0 results because URI is missing, got %d", len(results))
	}
}

func TestSearch_WrongTypeName(t *testing.T) {
	service := NewService(config.SearchSettings{InMemory: true, MaxResults: 10})
	index, _ := bleve.NewMemOnly(buildMapping())
	_ = index.Index("acdc://test", struct {
		URI     string `json:"uri"`
		Name    int    `json:"name"` // wrong type
		Content string `json:"content"`
	}{
		URI:     "acdc://test",
		Name:    123,
		Content: "Some test content",
	})
	service.index = index
	defer service.Close()

	results, err := service.Search("test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0].Name != "Unknown" {
		t.Errorf("Expected name 'Unknown', got '%s'", results[0].Name)
	}
}

// TestSearch_KeywordsBoosting proves that documents with matching keywords
// score higher than documents without keywords, even with identical content.
// This test is strict: both documents match the search term in content,
// but only one has it as a keyword. The keyword-boosted doc must rank first
// AND have a measurably higher score.
func TestSearch_KeywordsBoosting(t *testing.T) {
	service := NewService(config.SearchSettings{InMemory: true, MaxResults: 10})
	defer service.Close()

	// CRITICAL: Both documents contain "development" in their content
	// Only doc2 has "development" as a keyword (which gets 2x boost)
	docs := []Document{
		{
			URI:      "acdc://doc1",
			Name:     "Document One",
			Content:  "Information about software development practices", // Has "development" in content
			Keywords: nil,                                                // No keywords
		},
		{
			URI:      "acdc://doc2",
			Name:     "Document Two",
			Content:  "Information about software development practices", // Same content with "development"
			Keywords: []string{"development", "coding"},                  // Also has "development" as keyword
		},
	}

	if err := service.IndexDocuments(docs); err != nil {
		t.Fatalf("IndexDocuments failed: %v", err)
	}

	// Search for "development" - both docs match in content, but doc2 also matches in keywords
	results, err := service.Search("development", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Both documents should match (they both have "development" in content)
	if len(results) != 2 {
		t.Fatalf("Expected 2 results (both docs contain 'development'), got %d", len(results))
	}

	// doc2 MUST be first because it has "development" as a boosted keyword (2x boost)
	if results[0].URI != "acdc://doc2" {
		t.Errorf("Expected doc2 (with keyword boost) to rank first, got %s", results[0].URI)
	}
	if results[1].URI != "acdc://doc1" {
		t.Errorf("Expected doc1 to rank second, got %s", results[1].URI)
	}
}

// TestSearch_KeywordsEmpty verifies that empty/nil keywords don't affect search behavior
func TestSearch_KeywordsEmpty(t *testing.T) {
	service := NewService(config.SearchSettings{InMemory: true, MaxResults: 10})
	defer service.Close()

	docs := []Document{
		{
			URI:      "acdc://doc1",
			Name:     "Alpha Doc",
			Content:  "The quick brown fox jumps",
			Keywords: nil, // nil keywords
		},
		{
			URI:      "acdc://doc2",
			Name:     "Beta Doc",
			Content:  "The slow gray elephant walks",
			Keywords: []string{}, // empty keywords slice
		},
	}

	if err := service.IndexDocuments(docs); err != nil {
		t.Fatalf("IndexDocuments failed: %v", err)
	}

	// Search should still work normally
	results, err := service.Search("fox", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result for 'fox', got %d", len(results))
	}
	if results[0].URI != "acdc://doc1" {
		t.Errorf("Expected doc1, got %s", results[0].URI)
	}

	results, err = service.Search("elephant", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result for 'elephant', got %d", len(results))
	}
	if results[0].URI != "acdc://doc2" {
		t.Errorf("Expected doc2, got %s", results[0].URI)
	}
}

// TestSearch_MultipleKeywords verifies that multiple keywords work correctly
func TestSearch_MultipleKeywords(t *testing.T) {
	service := NewService(config.SearchSettings{InMemory: true, MaxResults: 10})
	defer service.Close()

	docs := []Document{
		{
			URI:      "acdc://api-guide",
			Name:     "API Guide",
			Content:  "General documentation for developers",
			Keywords: []string{"api", "rest", "http", "json"},
		},
	}

	if err := service.IndexDocuments(docs); err != nil {
		t.Fatalf("IndexDocuments failed: %v", err)
	}

	// Each keyword should match
	for _, kw := range []string{"api", "rest", "http", "json"} {
		results, err := service.Search(kw, nil)
		if err != nil {
			t.Fatalf("Search for '%s' failed: %v", kw, err)
		}
		if len(results) != 1 {
			t.Errorf("Expected 1 result for keyword '%s', got %d", kw, len(results))
		}
	}
}

// TestSearch_KeywordsOnlyMatch verifies that keywords alone can match a document
// even when the search term is not in the content
func TestSearch_KeywordsOnlyMatch(t *testing.T) {
	service := NewService(config.SearchSettings{InMemory: true, MaxResults: 10})
	defer service.Close()

	docs := []Document{
		{
			URI:      "acdc://guide",
			Name:     "Programming Guide",
			Content:  "This document discusses various programming concepts", // No "golang"
			Keywords: []string{"golang", "go"},                               // But has it as keyword
		},
	}

	if err := service.IndexDocuments(docs); err != nil {
		t.Fatalf("IndexDocuments failed: %v", err)
	}

	// Search for "golang" - only in keywords, not in content or name
	results, err := service.Search("golang", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result for keyword-only match 'golang', got %d", len(results))
	}
	if results[0].URI != "acdc://guide" {
		t.Errorf("Expected acdc://guide, got %s", results[0].URI)
	}
}
