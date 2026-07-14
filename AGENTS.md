# AGENTS.md — abuse-lookup

## What this is

A CLI + local MCP server that checks IP reputation against the **AbuseIPDB API
v2**. It looks up the abuse confidence score, report history, usage type, and ISP
of an IP, and caches results locally with a TTL so repeated lookups do not
re-spend the free-tier daily budget (1000 checks/day). It is the online,
reputation-focused sibling of `asn-lookup` (which answers AS/country offline).

## Build & test

```bash
make build      # → dist/abuse-lookup  (NEVER `go build` directly)
make test       # go test -race -cover ./...
make check      # lint + test + build-all
make build-all  # cross-compile linux/{amd64,arm64}, darwin/arm64, windows/amd64
```

Go 1.25+. **No external dependencies** — standard library only.

## Layout

```
main.go                 Entry point; sets main.version, calls app.Run.
internal/abuseipdb/     AbuseIPDB API v2 client: Client interface + HTTPClient.
  client.go             /check + /reports; shared do(); 429→ErrRateLimited, else *APIError.
internal/cache/         TTL JSON cache; atomic write via os.Root; key = IP+max-age+verbose.
internal/config/        Sectioned-TOML subset parser + env/flag resolution.
internal/engine/        Ties config+client+cache: Check (cached), Reports (uncached).
internal/workspace/     Agent-provided output dir + os.Root write containment.
internal/app/           CLI: dispatch, check/reports/doctor/cache/mcp, output.
internal/mcp/           Zero-dep stdio JSON-RPC 2.0 MCP server + tools.
  usage.md              Embedded get_usage manual (pinned by usage_test.go).
```

## Key design decisions

- **Online API, not an offline DB.** Unlike asn-lookup (bulk local DB), every
  live lookup hits the network. The "avoid excessive API calls" goal is met with
  a **local TTL cache** (default 12h) instead of a bulk download.
- **Engine is shared** by CLI and MCP so their behaviour cannot diverge.
- **HTTP client is an interface** (`abuseipdb.Client`) so the engine is tested
  without touching the network; `check`/`reports` share one `do()` helper.
- **Cache freshness lives in the record** (`FetchedAt`), not the file mtime, so
  it survives copies/backups. The cache key is `(IP, max-age, verbose)` — each
  combination is a different response and must not alias. Writes are atomic
  (temp + rename) through `os.Root` so a corrupt/truncated file is never read.
- **Reports are not cached.** `get_reports` / the `reports` command are a
  secondary, paginated detail fetch; pagination is caller-driven (`page`,
  `per_page`).
- **Large `get_reports` pages are file-mediated** (asn-lookup / voice-studio-mcp
  pattern). A busy IP can have hundreds of reports; the MCP tool returns metadata
  + a preview inline and writes the full page to an agent-provided
  `workspace_root` (default when omitted), returning the path. Writes go through
  `os.Root`. The workspace MUST be agent-specifiable — a hardcoded home-dir path
  breaks in sandboxes like Cowork.
- **Read-only scope.** The tool never calls AbuseIPDB's write endpoints
  (`report`, `bulk-report`, `clear-address`) — submitting to the shared database
  is out of scope.

## Gotchas

- **API key is a secret:** it is sent via the `Key` header, never placed in a
  URL, and never logged or surfaced in an error. It is deliberately **not** a
  CLI flag (would leak into shell history / the process list) — use
  `ABUSEIPDB_API_KEY` or `[abuseipdb] key`.
- **Rate limit:** free tier is 1000 checks/day. A `429` is a wrapped
  `abuseipdb.ErrRateLimited`; the CLI stops the batch (the quota won't recover
  until the daily reset — no auto-retry). `X-RateLimit-Remaining` drives a
  low-quota warning.
- **Flag order:** `check`/`reports` accept flags interspersed with positional
  IPs via `parseInterspersed` (Go's `flag` otherwise stops at the first
  non-flag). Keep using it if you add positional commands.
- **`mcp` starts without a key:** so `get_usage` / `cache_status` still work;
  API-backed tools return a clear error when no key is configured.
- **Workspace writes:** all `get_reports` file writes go through
  `workspace.WriteFileAtomic` (os.Root). Never write MCP outputs with plain
  `os.WriteFile` — that defeats symlink containment. The filename is
  server-generated (`reports-<ip>-p<page>.json`), so callers never control the
  leaf name.
- **get_usage manual:** `internal/mcp/usage.md` is embedded and returned by the
  `get_usage` tool; the initialize `instructions` field points clients to it.
  When you add/rename a tool or a result field, update usage.md — `usage_test.go`
  fails if the manual omits a tool name or a documented key term.
- **Attribution:** keep the AbuseIPDB credit in `version`, `--help`, and the
  READMEs. Cached data is local/private and is not redistributed.

## Data source

AbuseIPDB API v2: `https://api.abuseipdb.com/api/v2/check` and `/reports`
(free API key from <https://www.abuseipdb.com/account/api>). Free tier: 1000
checks/day. Key sent via the `Key` header.
