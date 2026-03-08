package prompts

import (
	"bytes"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"text/template"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/content"
)

// PromptProvider provides access to prompts
type PromptProvider struct {
	definitions []PromptDefinition
	nameMap     map[string]PromptDefinition
	cp          *content.ContentProvider
}

// NewPromptProvider creates a new prompt provider
func NewPromptProvider(definitions []PromptDefinition, cp *content.ContentProvider) *PromptProvider {
	nameMap := make(map[string]PromptDefinition)
	for _, d := range definitions {
		nameMap[d.Name] = d
	}
	return &PromptProvider{
		definitions: definitions,
		nameMap:     nameMap,
		cp:          cp,
	}
}

// ListPrompts lists all available prompts
func (p *PromptProvider) ListPrompts() []mcp.Prompt {
	prompts := make([]mcp.Prompt, len(p.definitions))
	for i, d := range p.definitions {
		args := make([]*mcp.PromptArgument, len(d.Arguments))
		for j, a := range d.Arguments {
			args[j] = &mcp.PromptArgument{
				Name:        a.Name,
				Description: a.Description,
				Required:    a.Required,
			}
		}

		prompts[i] = mcp.Prompt{
			Name:        d.Name,
			Description: d.Description,
			Arguments:   args,
		}
	}
	return prompts
}

// GetPrompt renders a prompt by name with arguments
func (p *PromptProvider) GetPrompt(name string, arguments map[string]string) ([]*mcp.PromptMessage, error) {
	defn, ok := p.nameMap[name]
	if !ok {
		return nil, fmt.Errorf("unknown prompt: %s", name)
	}

	// Validate required arguments
	for _, arg := range defn.Arguments {
		if arg.Required {
			val, ok := arguments[arg.Name]
			if !ok || val == "" {
				return nil, fmt.Errorf("missing required argument: %s", arg.Name)
			}
		}
	}

	var buf bytes.Buffer
	if err := defn.Template.Execute(&buf, arguments); err != nil {
		return nil, fmt.Errorf("failed to execute prompt template: %w", err)
	}

	return []*mcp.PromptMessage{
		{
			Role: "user",
			Content: &mcp.TextContent{
				Text: buf.String(),
			},
		},
	}, nil
}

// DiscoverPrompts discovers prompts from markdown files
func DiscoverPrompts(cp *content.ContentProvider) ([]PromptDefinition, error) {
	var definitions []PromptDefinition
	promptsDir := cp.PromptsDir

	// Ensure directory exists, if not just return empty
	if _, err := os.Stat(promptsDir); err != nil {
		if os.IsNotExist(err) {
			slog.Debug("Prompts directory does not exist", "path", promptsDir)
			return nil, nil
		}
		slog.Error("Failed to access prompts directory", "path", promptsDir, "error", err)
		return nil, err
	}

	err := filepath.WalkDir(promptsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Error("Error walking prompts directory", "path", path, "error", err)
			return nil // continue walking
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}

		// Parse frontmatter
		md, err := cp.LoadMarkdownWithFrontmatter(path)
		if err != nil {
			slog.Warn("Skipping invalid prompt file", "file", d.Name(), "error", err)
			return nil
		}

		// Extract metadata
		name, _ := md.Metadata["name"].(string)
		description, _ := md.Metadata["description"].(string)

		if name == "" || description == "" {
			slog.Warn("Skipping prompt with missing metadata", "file", d.Name())
			return nil
		}

		// Extract arguments
		var arguments []PromptArgument
		if args, ok := md.Metadata["arguments"].([]interface{}); ok {
			for _, a := range args {
				if amap, ok := a.(map[string]interface{}); ok {
					argName, _ := amap["name"].(string)
					argDesc, _ := amap["description"].(string)
					argReq, ok := amap["required"].(bool)
					if !ok {
						argReq = true // default to required
					}
					if argName != "" {
						arguments = append(arguments, PromptArgument{
							Name:        argName,
							Description: argDesc,
							Required:    argReq,
						})
					}
				}
			}
		}

		// Parse and cache template
		tmpl, err := template.New(name).Option("missingkey=zero").Parse(md.Content)
		if err != nil {
			slog.Warn("Skipping prompt with invalid template", "file", d.Name(), "error", err)
			return nil
		}

		definitions = append(definitions, PromptDefinition{
			Name:        name,
			Description: description,
			Arguments:   arguments,
			FilePath:    path,
			Template:    tmpl,
		})

		slog.Info("Loaded prompt", "name", name)

		return nil
	})

	return definitions, err
}
