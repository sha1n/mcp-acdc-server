package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

var (
	ErrUnknownResource = errors.New("unknown resource")
	ErrUnknownFragment = errors.New("unknown resource fragment")
)

// ContentTransformer transforms resource content before it is returned.
// It receives the raw content and the source containing the resource.
type ContentTransformer func(content string, def ResourceDefinition) string

// Option configures a ResourceProvider.
type Option func(*ResourceProvider)

// WithTransformer adds a content transformer to the provider.
// Multiple transformers are applied in the order they are added.
func WithTransformer(transformer ContentTransformer) Option {
	return func(provider *ResourceProvider) {
		provider.transformers = append(provider.transformers, transformer)
	}
}

// ResourceProvider is an immutable catalog of discovered sources and chunks.
type ResourceProvider struct {
	sources      []domain.SourceDocument
	chunks       []domain.Chunk
	sourceByURI  map[string]domain.SourceDocument
	chunkByURI   map[string]domain.Chunk
	sectionByURI map[string][]domain.Chunk
	transformers []ContentTransformer
}

// NewResourceProvider creates an immutable source and chunk catalog.
func NewResourceProvider(sources []domain.SourceDocument, chunks []domain.Chunk, opts ...Option) (*ResourceProvider, error) {
	provider := &ResourceProvider{
		sources:      make([]domain.SourceDocument, 0, len(sources)),
		chunks:       make([]domain.Chunk, 0, len(chunks)),
		sourceByURI:  make(map[string]domain.SourceDocument, len(sources)),
		chunkByURI:   make(map[string]domain.Chunk, len(chunks)),
		sectionByURI: make(map[string][]domain.Chunk),
	}

	for _, source := range sources {
		if _, exists := provider.sourceByURI[source.URI]; exists {
			return nil, fmt.Errorf("duplicate source URI: %s", source.URI)
		}
		copy := cloneSource(source)
		provider.sources = append(provider.sources, copy)
		provider.sourceByURI[copy.URI] = copy
	}

	chunkIDs := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		if _, exists := chunkIDs[chunk.ID]; exists {
			return nil, fmt.Errorf("duplicate chunk ID: %s", chunk.ID)
		}
		if _, exists := provider.chunkByURI[chunk.ChunkURI]; exists {
			return nil, fmt.Errorf("duplicate chunk URI: %s", chunk.ChunkURI)
		}
		chunkIDs[chunk.ID] = struct{}{}
		copy := cloneChunk(chunk)
		provider.chunks = append(provider.chunks, copy)
		provider.chunkByURI[copy.ChunkURI] = copy
		sectionURI := copy.SourceURI + "#" + copy.SectionFragment
		provider.sectionByURI[sectionURI] = append(provider.sectionByURI[sectionURI], copy)
	}

	for _, option := range opts {
		option(provider)
	}
	return provider, nil
}

// ListResources lists all available source resources.
func (p *ResourceProvider) ListResources() []mcp.Resource {
	resources := make([]mcp.Resource, len(p.sources))
	for i, source := range p.sources {
		resources[i] = mcp.Resource{
			URI:         source.URI,
			Name:        source.Name,
			Description: source.Description,
			MIMEType:    source.MIMEType,
		}
	}
	return resources
}

// ReadResource reads a source, exact chunk, or logical section URI.
func (p *ResourceProvider) ReadResource(uri string) (string, error) {
	if source, ok := p.sourceByURI[uri]; ok {
		return p.transform(source.Content, source), nil
	}
	if chunk, ok := p.chunkByURI[uri]; ok {
		return p.transform(chunk.Content, p.chunkSource(chunk)), nil
	}
	if chunks, ok := p.sectionByURI[uri]; ok {
		parts := make([]string, len(chunks))
		for i, chunk := range chunks {
			parts[i] = chunk.Content
		}
		return p.transform(strings.Join(parts, "\n\n"), p.chunkSource(chunks[0])), nil
	}

	base, _, hasFragment := strings.Cut(uri, "#")
	if _, exists := p.sourceByURI[base]; hasFragment && exists {
		return "", fmt.Errorf("%w: %s", ErrUnknownFragment, uri)
	}
	return "", fmt.Errorf("%w: %s", ErrUnknownResource, uri)
}

// StreamChunks streams transformed copies in deterministic discovery order.
func (p *ResourceProvider) StreamChunks(ctx context.Context, ch chan<- domain.Chunk) error {
	for _, catalogChunk := range p.chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := cloneChunk(catalogChunk)
		chunk.Content = p.transform(chunk.Content, p.chunkSource(chunk))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ch <- chunk:
		}
	}
	return nil
}

// StreamResources preserves the legacy source-document stream until indexing
// migrates to StreamChunks. New callers should use StreamChunks.
func (p *ResourceProvider) StreamResources(ctx context.Context, ch chan<- domain.Document) error {
	for _, source := range p.sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		document := domain.Document{
			URI:      source.URI,
			Name:     source.Name,
			Content:  p.transform(source.Content, source),
			Keywords: append([]string(nil), source.Keywords...),
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ch <- document:
		}
	}
	return nil
}

func (p *ResourceProvider) transform(body string, source domain.SourceDocument) string {
	for _, transformer := range p.transformers {
		body = transformer(body, source)
	}
	return body
}

func (p *ResourceProvider) chunkSource(chunk domain.Chunk) domain.SourceDocument {
	if source, ok := p.sourceByURI[chunk.SourceURI]; ok {
		return source
	}
	return domain.SourceDocument{ID: chunk.SourceID, URI: chunk.SourceURI}
}

func cloneSource(source domain.SourceDocument) domain.SourceDocument {
	source.Keywords = append([]string(nil), source.Keywords...)
	source.PathLabels = append([]string(nil), source.PathLabels...)
	return source
}

func cloneChunk(chunk domain.Chunk) domain.Chunk {
	chunk.PathLabels = append([]string(nil), chunk.PathLabels...)
	chunk.HeadingPath = append([]string(nil), chunk.HeadingPath...)
	chunk.Keywords = append([]string(nil), chunk.Keywords...)
	return chunk
}
