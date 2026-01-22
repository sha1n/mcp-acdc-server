package domain

import "testing"

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
