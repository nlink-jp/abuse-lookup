# CLAUDE.md — abuse-lookup

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Purpose

CLI + local MCP server that checks IP reputation against the AbuseIPDB API v2.
Reads IPs, returns the abuse confidence score / report history / usage type /
ISP, and caches results locally with a TTL so repeated lookups do not re-spend
the free-tier daily budget. The online, reputation-focused sibling of
asn-lookup (which answers AS/country offline).

## Build & test

```bash
make build       # Build → dist/abuse-lookup  (never `go build` directly)
make test        # Tests with race detector + coverage
go test ./...    # Same without Makefile
```

## Architecture

```
main.go                 CLI entry: main.version → app.Run
internal/abuseipdb/     AbuseIPDB API v2 client (Client interface + HTTPClient)
internal/cache/         TTL JSON cache; atomic write via os.Root
internal/config/        Sectioned-TOML subset + env/flag resolution
internal/engine/        Check (cached) / Reports (uncached) — shared by CLI & MCP
internal/workspace/     Agent-provided output dir + os.Root write containment
internal/app/           Dispatch + check/reports/doctor/cache/mcp + output
internal/mcp/           Zero-dep stdio JSON-RPC 2.0 server + tools
```

Core logic takes injected dependencies for testability (the HTTP client is an
interface, mocked in tests). **No external dependencies — standard library
only.** See [docs/en/architecture.md](docs/en/architecture.md) for the "why".

## Key conventions

- Online API, not an offline DB: every live lookup hits the network; a local TTL
  cache (default 12h) avoids re-spending the daily quota on repeats.
- Engine is shared by CLI and MCP so their behaviour cannot diverge.
- API key is secret: sent via the `Key` header; never in a URL, log, or error;
  never a CLI flag (would leak into shell history / the process list).
- Cache key is `(IP, max-age, verbose)`; freshness lives in the record
  (`FetchedAt`), not the file mtime; writes are atomic (temp + rename, `os.Root`).
- Reports are not cached; large `get_reports` pages are file-mediated to an
  agent-provided `workspace_root` (os.Root containment).
- Read-only scope: never calls AbuseIPDB's write endpoints (`report`,
  `bulk-report`, `clear-address`).
- Attribution: keep the AbuseIPDB credit in `version`, `--help`, and the READMEs;
  cached data is local and not redistributed.

## Communication Language

All communication between contributors and Claude Code is conducted in
**Japanese**.
