# abuse-lookup

Check IP address reputation against the [AbuseIPDB](https://www.abuseipdb.com)
API, as a CLI **and** a local MCP server. Look up the abuse confidence score,
report history, usage type, and ISP of an IP — and cache the result locally with
a TTL so repeated lookups don't re-spend the free-tier daily budget.

The online, reputation-focused sibling of
[asn-lookup](https://github.com/nlink-jp/asn-lookup) (which answers AS/country
offline). Use them side by side to investigate an IP from multiple angles.

- **IP → reputation**: abuse confidence score, country, usage type, ISP, domain,
  total reports, distinct reporters, last-reported date, whitelist/Tor flags.
- **Report history**: paginated individual abuse reports for an IP.
- **CLI + MCP**: the same engine drives an interactive CLI and a stdio MCP server
  (`check_ip`, `get_reports`, `cache_status`, `get_usage`).
- **TTL cache**: results are cached per `(IP, max-age, verbose)`; a fresh cache
  hit costs no API quota.
- **Zero external dependencies**: standard library only (`net/http` + `net/netip`
  + `encoding/json`).

> **Data:** AbuseIPDB (<https://www.abuseipdb.com>). Attribution to AbuseIPDB is
> **required** when presenting results. This tool queries the API with your own
> key and keeps cached data local — it does not redistribute it.

## Install

```bash
make build          # → dist/abuse-lookup (signed on macOS)
```

Requires Go 1.25+. Cross-compile all platforms with `make build-all`.

## Quick start

1. Get a **free** API key at <https://www.abuseipdb.com/account/api> (the free
   tier allows 1,000 checks/day).
2. Configure it and look up an IP:

   ```bash
   export ABUSEIPDB_API_KEY=your_key_here
   abuse-lookup check 118.25.6.39
   # 118.25.6.39
   #   Abuse score:     100/100 (high risk)
   #   Country:         CN
   #   Usage type:      Data Center/Web Hosting/Transit
   #   ISP:             Tencent Cloud Computing
   #   Total reports:   … (from … distinct reporters)
   #   Last reported:   …
   ```

3. Fetch the individual reports:

   ```bash
   abuse-lookup reports 118.25.6.39
   ```

## Commands

| Command | Description |
|---------|-------------|
| `abuse-lookup check <IP>...` | Look up reputation per IP. Reads stdin when no args are given. |
| `abuse-lookup reports <IP>...` | Fetch one page of the abuse reports for each IP. |
| `abuse-lookup doctor` | Report API-key configuration and cache health (spends no quota). |
| `abuse-lookup cache` | Show cache status; `--clear` to discard. |
| `abuse-lookup mcp` | Run as a local MCP server over stdio. |
| `abuse-lookup version` | Print the version and data attribution. |

### check flags

- `--max-age <days>` — report look-back window (default 90).
- `--verbose` — include recent report details.
- `--refresh` — ignore the cache and re-fetch.
- `-j` / `--json` — **JSON Lines** (one object per IP) for pipelines.

### reports flags

- `--max-age <days>` — report look-back window (default 90).
- `--page <n>` / `--per-page <n>` — pagination (default page 1, 25 per page).
- `-j` / `--json` — JSON Lines (one report per line, tagged with `ipAddress`).

### Output formats

- Default: a human-readable block per IP.
- `-j` / `--json`: **JSON Lines** for pipelines.

```bash
cat ips.txt | abuse-lookup check --json | jq 'select(.abuseConfidenceScore >= 75) | .ipAddress'
```

Invalid IPs and per-IP API errors are reported per line and never abort the
batch. A rate-limit condition stops the batch (the daily quota won't recover
until the reset).

## MCP server

Register the server with an MCP client. For Claude Code:

```bash
claude mcp add abuse-lookup -- /path/to/abuse-lookup mcp
```

Or in a client config:

```json
{
  "mcpServers": {
    "abuse-lookup": { "command": "/path/to/abuse-lookup", "args": ["mcp"] }
  }
}
```

Tools (call `get_usage` first for the full manual and recovery table; the server
also advertises this via the MCP `instructions` field):

| Tool | Arguments | Purpose |
|------|-----------|---------|
| `get_usage` | — | Operating manual: tools, caching model, workspace model, recovery table |
| `check_ip` | `ip` (string) or `ips` (array), `max_age`, `verbose`, `refresh` | IP → reputation (cached) |
| `get_reports` | `ip`, `max_age`, `page`, `per_page`, `limit`, `workspace_root`, `workspace_id` | IP → paginated reports (large pages file-mediated) |
| `cache_status` | — | Cache directory, TTL, entry/fresh counts |

**Caching.** `check_ip` results are cached per `(IP, max_age, verbose)` for the
configured TTL (default 12h) and returned with `cached: true` without spending
quota; pass `refresh: true` to force a live lookup.

**Large report pages are file-mediated.** `get_reports` always returns page
metadata (`total`, `page`, `count`, `has_next_page`) plus an inline preview; when
a page exceeds `limit` (default 25) the full page is written to a file and only
its path is returned (`reports_file`, `truncated: true`) — so a busy IP never
floods the model's context. The output directory is agent-provided: create a
directory with your own file tools and pass it as `workspace_root` (essential in
sandboxed environments); omit it to use the server default. All writes are
confined to the workspace with `os.Root` (planted symlinks cannot escape).

## Configuration

Settings resolve in this order (later wins): config file → environment → flags.

**API key** (required):

- `ABUSEIPDB_API_KEY` (or `ABUSE_LOOKUP_KEY`) environment variable, or
- `[abuseipdb] key` in the config file.

The key is intentionally not a command-line flag — it must not land in shell
history or the process list. It is sent via the `Key` request header and never
appears in a URL, log, or error.

**Config file** — `~/.config/abuse-lookup/config.toml`
(honors `XDG_CONFIG_HOME`). See [`config.example.toml`](config.example.toml):

```toml
[abuseipdb]
key = "your_key_here"
# base_url = "https://api.abuseipdb.com/api/v2"

[cache]
# ttl_hours = 12
# dir = "~/.local/share/abuse-lookup/cache"

# [mcp]
# Default output directory for file-mediated get_reports results
# (ABUSE_LOOKUP_WORKSPACE). Callers may override per request with workspace_root.
# workspace = "~/.local/state/abuse-lookup/workspace"
```

**Cache location** — `~/.local/share/abuse-lookup/cache`
(honors `XDG_DATA_HOME`).

## Rate limits

The AbuseIPDB free tier allows **1,000 checks/day** (resetting daily). Each live
`check`/`reports` call spends one; cache hits spend nothing. When the remaining
quota runs low, a warning is printed to stderr; a `429` (quota exhausted) is
returned as a clear error with no automatic retry.

## How it works

`check` validates the IP with `net/netip`, consults the local TTL cache, and on
a miss queries `GET /check` with the `Key` header. Responses are written to a
per-IP JSON file atomically (temp + rename, `os.Root` containment) so a corrupt
cache is never read. `reports` queries `GET /reports` (paginated) and is not
cached. The same engine backs the CLI and the MCP server, so their behaviour
cannot diverge. See [docs/en/architecture.md](docs/en/architecture.md).

## Development

```bash
make test     # go test -race -cover ./...
make build    # → dist/abuse-lookup
make check    # lint + test + build-all
```

## License

Code: [MIT](LICENSE). Data: AbuseIPDB — attribution required; cached data is
local and not redistributed.
