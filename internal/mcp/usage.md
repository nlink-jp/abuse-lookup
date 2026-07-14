# abuse-lookup MCP — operating manual

This server checks IP reputation via the **AbuseIPDB API v2** (online). An API
key must be configured (`ABUSEIPDB_API_KEY` env var or `[abuseipdb] key` in the
config file). Results are cached locally with a TTL so repeated `check_ip` calls
do not re-spend the daily quota.

The free tier allows **1000 checks/day**; a rate-limit error means the daily
quota is exhausted — wait for the daily reset rather than retrying.

## Tools

### `get_usage`
Returns this manual. No arguments.

### `check_ip`
IP → reputation. Served from the local TTL cache when fresh.
- Arguments: `ip` (string) **or** `ips` (array of strings). At least one required.
  `max_age` (integer, default 90) sets the report look-back window; `verbose`
  (boolean) includes recent report details; `refresh` (boolean) ignores the cache
  and re-fetches.
- Result: a JSON array, one object per input, each with `input`, `cached`, and —
  when the lookup succeeded — `ipAddress`, `abuseConfidenceScore`, `countryCode`,
  `usageType`, `isp`, `domain`, `totalReports`, `numDistinctUsers`,
  `lastReportedAt`, `isWhitelisted`, `isTor`. A failed input comes back as
  `{input, error}`. A rate-limit condition returns a single error result.

### `get_reports`
One page of the individual abuse reports for a single IP.
- Arguments:
  - `ip` (string, **required**): a single IP address.
  - `max_age` (integer, default 90): report look-back window.
  - `page` (integer, default 1) / `per_page` (integer, default 25): pagination,
    caller-driven. Use `has_next_page` in the result to decide whether to request
    the next `page`.
  - `limit` (integer, default 25): max reports inlined before a file is written.
  - `workspace_root` (string): **absolute path** to a directory you prepared with
    your own file tools; the full page is written here when large. Omit to use the
    server default (may be unwritable in a sandbox — prefer passing this).
  - `workspace_id` (string): optional single-segment subdirectory under the root.
- Result: a JSON array with one object holding `input`, `total`, `page`, `count`,
  `per_page`, `has_next_page`, and:
  - small page → `reports` (the full inline list), `truncated:false`.
  - large page → `preview` (first `limit`), `truncated:true`, and `reports_file`
    (absolute path to the full page). **Read that file** for the complete page.

### `cache_status`
Reports `cache_dir`, `ttl`, `entries`, and `fresh` (count within the TTL). No
arguments.

## Caching model

`check_ip` results are cached per `(IP, max_age, verbose)` for the configured TTL
(default 12h). A cached result is returned with `cached:true` and does not spend
API quota. Use `refresh:true` to force a live lookup. `get_reports` is **not**
cached — it is a paginated detail fetch.

## Workspace model (why files, not bytes)

A single `get_reports` page can hold many verbose reports; returning them all
inline would flood your context, so large pages are written to a file and only
the path is returned. `total` and `count` always reflect the real numbers —
nothing is silently dropped.

The output directory is **caller-provided**: create a writable directory with
your own file tools and pass it as `workspace_root`. This is required in
sandboxed environments where the server cannot write under `$HOME`. Writes are
confined to the workspace (kernel-enforced via `os.Root`); the filename is
server-generated (`reports-<ip>-p<page>.json`), so you never control the leaf
name.

## Recovery table

| Symptom (result text) | What it means | What to do |
|---|---|---|
| `no AbuseIPDB API key configured` | No key is set | Ask the user to set `ABUSEIPDB_API_KEY` or `[abuseipdb] key` |
| `AbuseIPDB daily rate limit exceeded` | The 1000/day free quota is used up | Wait for the daily reset; do not retry immediately |
| `check_ip` → `{input, error:"invalid IP address …"}` | The input was not a valid IP | Fix the input |
| `get_reports` → `truncated:true`, `reports_file` set | The full page was written to a file | Read `reports_file` for the complete page |
| `get_reports` → `note` mentions `workspace_root` | The output file could not be written | Create a writable directory and pass its absolute path as `workspace_root` |
| `check_ip` → `cached:true` | Result came from the local cache | Expected; pass `refresh:true` for a live value |

## Attribution

Data: AbuseIPDB (https://www.abuseipdb.com). Credit AbuseIPDB when you present
results derived from this API. Cached data is local/private and is not
redistributed.
