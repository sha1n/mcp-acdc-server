package app

import "github.com/spf13/pflag"

// RegisterFlags registers all CLI flags on the given FlagSet
func RegisterFlags(flags *pflag.FlagSet) {
	flags.StringP("content-dir", "c", "", "Path to content directory (default: ./content)")
	flags.StringP("transport", "t", "", "Transport type: stdio or sse (default: stdio)")
	flags.StringP("host", "H", "", "Host for SSE transport (default: 0.0.0.0)")
	flags.IntP("port", "p", 0, "Port for SSE transport (default: 8080)")
	flags.IntP("search-max-results", "m", 0, "Maximum search results (default: 10)")
	flags.Float64("search-keywords-boost", 0, "Boost for keywords matches (default: 3.0)")
	flags.Float64("search-name-boost", 0, "Boost for name matches (default: 2.0)")
	flags.Float64("search-content-boost", 0, "Boost for content matches (default: 1.0)")
	flags.StringP("uri-scheme", "s", "", "URI scheme for resources (default: acdc)")
	flags.StringP("auth-type", "a", "", "Authentication type: none, basic, or apikey (default: none)")
	flags.StringP("auth-basic-username", "u", "", "Basic auth username")
	flags.StringP("auth-basic-password", "P", "", "Basic auth password")
	flags.StringSliceP("auth-api-keys", "k", nil, "API keys (comma-separated)")
}
