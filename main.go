// Command abuse-lookup checks the reputation of IP addresses against the
// AbuseIPDB API, as a CLI and (Phase 2) a local MCP server. Results are cached
// locally with a TTL to avoid re-spending the free-tier daily budget on
// repeated lookups.
package main

import (
	"os"

	"github.com/nlink-jp/abuse-lookup/internal/app"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(app.Run(os.Args[1:], version))
}
