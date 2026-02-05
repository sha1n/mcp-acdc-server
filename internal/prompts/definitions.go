package prompts

import (
	"text/template"
)

// PromptDefinition definition of an MCP prompt
type PromptDefinition struct {
	Name        string // Namespaced name: "source:promptname"
	Description string
	Arguments   []PromptArgument
	FilePath    string
	Template    *template.Template
	Source      string // Content location name
}

// PromptArgument definition of an MCP prompt argument
type PromptArgument struct {
	Name        string
	Description string
	Required    bool
}
