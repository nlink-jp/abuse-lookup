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
- Result: a JSON array with one object holding `input`, `total`, `page`, `count`,
  `per_page`, `has_next_page`, and `reports` — the whole page, always inline.
  Nothing is written to disk and no path is returned.

### `cache_status`
Reports `cache_dir`, `ttl`, `entries`, and `fresh` (count within the TTL). No
arguments.

## Arguments are strict

Every tool refuses an argument it does not declare, naming it:
`arguments: json: unknown field "maxage"`. A wrong-typed argument is refused the
same way. Nothing runs before the arguments decode, so a rejected call spends no
quota and reaches no network — fix the name or the type and call again. This is
the enforcing half of the closed schemas (org ADR-021 §4); a misspelt `refresh`
used to be dropped, which returned a cached answer that read as a fresh one.

## Caching model

`check_ip` results are cached per `(IP, max_age, verbose)` for the configured TTL
(default 12h). A cached result is returned with `cached:true` and does not spend
API quota. Use `refresh:true` to force a live lookup. `get_reports` is **not**
cached — it is a paginated detail fetch.

## Sizing a page

Every report on a page is returned inline; no file is written and no path comes
back. The page size is therefore yours to set: `per_page` bounds one response,
`has_next_page` says whether more exist, and `page` walks them.

If a page is larger than your context can hold, re-request with a smaller
`per_page`. `total` and `count` always reflect the real numbers, so nothing is
silently dropped.

## Recovery table

| Symptom (result text) | What it means | What to do |
|---|---|---|
| `arguments: json: unknown field "…"` | An argument name this tool does not declare — usually a typo | Fix the spelling and call again; the named field is the offending one. No quota was spent |
| `arguments: json: cannot unmarshal …` | An argument of the wrong JSON type | Check the argument's type in the tool list above and call again |
| `no AbuseIPDB API key configured` | No key is set | Ask the user to set `ABUSEIPDB_API_KEY` or `[abuseipdb] key` |
| `AbuseIPDB daily rate limit exceeded` | The 1000/day free quota is used up | Wait for the daily reset; do not retry immediately |
| `check_ip` → `{input, error:"invalid IP address …"}` | The input was not a valid IP | Fix the input |
| `get_reports` → the response is larger than you can hold | `per_page` was too large for your context | Re-request with a smaller `per_page`; walk the rest with `page` |
| `get_reports` → `has_next_page:true` | More reports exist beyond this page | Request `page+1`; `total` says how many there are in all |
| `check_ip` → `cached:true` | Result came from the local cache | Expected; pass `refresh:true` for a live value |

## Attribution

Data: AbuseIPDB (https://www.abuseipdb.com). Credit AbuseIPDB when you present
results derived from this API. Cached data is local/private and is not
redistributed.
