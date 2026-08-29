package app

import "github.com/spf13/pflag"

// RegisterFlags registers all CLI flags on the given FlagSet
func RegisterFlags(flags *pflag.FlagSet) {
	flags.StringP("content-dir", "c", "", "Path to content directory (default: current working directory)")
	flags.StringP("transport", "t", "", "Transport type: stdio or sse (default: stdio)")
	flags.StringP("host", "H", "", "Host for SSE transport (default: 0.0.0.0)")
	flags.IntP("port", "p", 0, "Port for SSE transport (default: 8080)")
	flags.IntP("search-max-results", "m", 0, "Maximum search results (default: 10)")
	flags.Float64("search-keywords-boost", 0, "Boost for keywords matches (default: 3.0)")
	flags.Float64("search-heading-boost", 0, "Boost for heading path matches (default: 2.5)")
	flags.Float64("search-path-boost", 0, "Boost for path label matches (default: 1.25)")
	flags.Float64("search-title-boost", 0, "Boost for document title matches (default: 2.0)")
	flags.Float64("search-content-boost", 0, "Boost for content matches (default: 1.0)")
	flags.String("search-result-mode", "", "Search output mode: references or content (default: references)")
	flags.String("search-semantic-model", "", "Path to a semantic embedding model; empty disables semantic search (default: empty)")
	flags.Float64("search-semantic-floor", 0, "Minimum cosine similarity a semantic hit must clear; -1 disables the floor (default: 0.25)")
	// Unlike its neighbours this default is non-zero, so pflag prints its own
	// "(default true)" in --help; stating it in the usage text too would
	// duplicate it.
	flags.Bool("search-in-memory", true, "Hold the search index in memory instead of on disk")
	flags.StringP("uri-scheme", "s", "", "URI scheme for resources (default: acdc)")
	flags.Bool("cross-ref", false, "Transform relative markdown links to resource URIs (default: false)")
	flags.StringP("auth-type", "a", "", "Authentication type: none, basic, or apikey (default: none)")
	flags.StringP("auth-basic-username", "u", "", "Basic auth username")
	flags.StringP("auth-basic-password", "P", "", "Basic auth password")
	flags.StringSliceP("auth-api-keys", "k", nil, "API keys (comma-separated)")
}
