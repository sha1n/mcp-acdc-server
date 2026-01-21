package app

import "github.com/spf13/pflag"

// RegisterFlags registers all CLI flags on the given FlagSet
func RegisterFlags(flags *pflag.FlagSet) {
	flags.StringP("content-dir", "c", "", "Path to content directory")
	flags.StringP("transport", "t", "", "Transport type: stdio or sse")
	flags.StringP("host", "H", "", "Host for SSE transport")
	flags.IntP("port", "p", 0, "Port for SSE transport")
	flags.IntP("search-max-results", "m", 0, "Maximum search results")
	flags.StringP("auth-type", "a", "", "Authentication type: none, basic, or apikey")
	flags.StringP("auth-basic-username", "u", "", "Basic auth username")
	flags.StringP("auth-basic-password", "P", "", "Basic auth password")
	flags.StringSliceP("auth-api-keys", "k", nil, "API keys (comma-separated)")
}
