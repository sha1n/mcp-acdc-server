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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.meta.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("McpMetadata.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
