package domain

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMcpMetadata_Validate(t *testing.T) {
	tests := []struct {
		name    string
		meta    McpMetadata
		wantErr bool
	}{
		{
			name: "Valid",
			meta: McpMetadata{
				Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
				Tools:  []ToolMetadata{{Name: "t", Description: "d"}},
			},
			wantErr: false,
		},
		{
			name: "Missing Server Name",
			meta: McpMetadata{
				Server: ServerMetadata{Name: "", Version: "1", Instructions: "i"},
			},
			wantErr: true,
		},
		{
			name: "Missing Server Version",
			meta: McpMetadata{
				Server: ServerMetadata{Name: "s", Version: "", Instructions: "i"},
			},
			wantErr: true,
		},
		{
			name: "Missing Instructions",
			meta: McpMetadata{
				Server: ServerMetadata{Name: "s", Version: "1", Instructions: ""},
			},
			wantErr: true,
		},
		{
			name: "Missing Tool Name",
			meta: McpMetadata{
				Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
				Tools:  []ToolMetadata{{Name: "", Description: "d"}},
			},
			wantErr: true,
		},
		{
			name: "Missing Tool Description",
			meta: McpMetadata{
				Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
				Tools:  []ToolMetadata{{Name: "t", Description: ""}},
			},
			wantErr: true,
		},
		{
			name: "Duplicate Tool Name",
			meta: McpMetadata{
				Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
				Tools:  []ToolMetadata{{Name: "t", Description: "d"}, {Name: "t", Description: "d2"}},
			},
			wantErr: true,
		},
		{
			name: "Valid with no tools",
			meta: McpMetadata{
				Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.meta.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("McpMetadata.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMcpMetadata_ValidateIndex(t *testing.T) {
	tests := []struct {
		name    string
		index   *IndexMetadata
		wantErr string
	}{
		{name: "absent", index: nil},
		{name: "configured", index: &IndexMetadata{Include: []string{"docs/**/*.md"}}},
		{name: "empty include", index: &IndexMetadata{}, wantErr: "index.include requires at least one pattern"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := McpMetadata{
				Server: ServerMetadata{Name: "test", Version: "1", Instructions: "test"},
				Index:  tt.index,
			}
			err := metadata.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestGetToolMetadata(t *testing.T) {
	meta := McpMetadata{
		Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
		Tools: []ToolMetadata{
			{Name: "search", Description: "custom search"},
			{Name: "other", Description: "other tool"},
		},
	}

	t.Run("Override First", func(t *testing.T) {
		got := meta.GetToolMetadata("search")
		if got.Description != "custom search" {
			t.Errorf("expected custom search, got %s", got.Description)
		}
	})

	t.Run("Override Second", func(t *testing.T) {
		got := meta.GetToolMetadata("other")
		if got.Description != "other tool" {
			t.Errorf("expected other tool, got %s", got.Description)
		}
	})

	t.Run("Default (After loop)", func(t *testing.T) {
		got := meta.GetToolMetadata("read")
		expected := DefaultToolMetadata["read"].Description
		if got.Description != expected {
			t.Errorf("expected default read description, got %s", got.Description)
		}
	})

	t.Run("Unknown (After loop)", func(t *testing.T) {
		got := meta.GetToolMetadata("unknown")
		if got.Name != "" || got.Description != "" {
			t.Errorf("expected empty metadata for unknown tool, got %+v", got)
		}
	})

	t.Run("Empty Tools", func(t *testing.T) {
		emptyMeta := McpMetadata{}
		got := emptyMeta.GetToolMetadata("search")
		if got.Description != DefaultToolMetadata["search"].Description {
			t.Errorf("expected default search even with empty tools")
		}
	})
}

// exampleURIPattern matches single-quoted example URIs like 'acdc://guides/getting-started'
// or 'acdc://guides/getting-started#installation' as used in the default tool descriptions.
var exampleURIPattern = regexp.MustCompile(`'([a-zA-Z][a-zA-Z0-9+\-.]*://[^']*)'`)

func TestDefaultToolMetadata_ReadDescriptionExampleURIsHaveNoFileExtension(t *testing.T) {
	desc := DefaultToolMetadata["read"].Description

	matches := exampleURIPattern.FindAllStringSubmatch(desc, -1)
	require.NotEmpty(t, matches, "expected at least one example URI in the read tool description")

	for _, match := range matches {
		uri := match[1]
		require.False(t, strings.HasSuffix(uri, ".md"), "example URI %q should not carry a .md extension - resource URIs never include the file extension", uri)
		require.False(t, strings.HasSuffix(uri, ".markdown"), "example URI %q should not carry a .markdown extension - resource URIs never include the file extension", uri)
	}
}

func TestDefaultToolMetadata_ReadDescriptionMentionsFragmentForm(t *testing.T) {
	desc := DefaultToolMetadata["read"].Description

	require.Contains(t, desc, "#", "expected the read tool description to mention the '#fragment' URI form for reading a single chunk")
}

func TestDefaultToolMetadata_SearchDescriptionDoesNotClaimDescriptionsAreSearched(t *testing.T) {
	desc := DefaultToolMetadata["search"].Description

	require.NotContains(t, strings.ToLower(desc), "descriptions", "descriptions are not an indexed search field - the search default description should not claim otherwise")
}

func TestDefaultToolMetadata_SearchDescriptionMentionsChunkResults(t *testing.T) {
	desc := DefaultToolMetadata["search"].Description

	require.Contains(t, strings.ToLower(desc), "chunk", "search results are chunk citations - the default description should describe them as such")
}

func TestDefaultMetadata_PassesValidate(t *testing.T) {
	meta := DefaultMetadata("my-repo", "1.2.3")

	require.NoError(t, meta.Validate())
}

func TestDefaultMetadata_IndexIncludeOrder(t *testing.T) {
	meta := DefaultMetadata("my-repo", "1.2.3")

	require.NotNil(t, meta.Index)
	require.Equal(t, []string{"README.md", "docs/**/*.md"}, meta.Index.Include)
}

func TestDefaultMetadata_ToolsFallBackToDefaults(t *testing.T) {
	meta := DefaultMetadata("my-repo", "1.2.3")

	require.Empty(t, meta.Tools)
	require.Equal(t, DefaultToolMetadata["search"].Description, meta.GetToolMetadata("search").Description)
	require.Equal(t, DefaultToolMetadata["read"].Description, meta.GetToolMetadata("read").Description)
}

func TestDefaultMetadata_NameAndInstructionsContainRepoName(t *testing.T) {
	meta := DefaultMetadata("my-repo", "1.2.3")

	require.Equal(t, "my-repo Documentation", meta.Server.Name)
	require.Contains(t, meta.Server.Instructions, "my-repo")
}

func TestDefaultMetadata_MutatingIndexIncludeDoesNotAffectPackageDefaultOrSecondCall(t *testing.T) {
	originalDefault := append([]string(nil), DefaultIndexInclude...)

	first := DefaultMetadata("my-repo", "1.2.3")
	first.Index.Include[0] = "MUTATED.md"

	require.Equal(t, originalDefault, DefaultIndexInclude, "mutating the returned Index.Include must not corrupt the package default")

	second := DefaultMetadata("my-repo", "1.2.3")
	require.Equal(t, []string{"README.md", "docs/**/*.md"}, second.Index.Include, "mutating a previous result must not affect a later call")
}

func TestToolsMap(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		meta := McpMetadata{
			Tools: []ToolMetadata{
				{Name: "t1", Description: "d1"},
				{Name: "t2", Description: "d2"},
			},
		}
		m, err := meta.ToolsMap()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(m) != 2 {
			t.Errorf("expected 2 tools, got %d", len(m))
		}
	})

	t.Run("Empty", func(t *testing.T) {
		meta := McpMetadata{}
		m, err := meta.ToolsMap()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(m) != 0 {
			t.Errorf("expected 0 tools, got %d", len(m))
		}
	})

	t.Run("Duplicate", func(t *testing.T) {
		meta := McpMetadata{
			Tools: []ToolMetadata{
				{Name: "t", Description: "d1"},
				{Name: "t", Description: "d2"},
			},
		}
		_, err := meta.ToolsMap()
		if err == nil {
			t.Fatal("expected error for duplicate tool name")
		}
	})
}
