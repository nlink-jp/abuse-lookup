# abuse-lookup — architecture

This document explains *why* abuse-lookup is built the way it is. For *what* the
tool does, see the [README](../../README.md); for the contributor map, see
[AGENTS.md](../../AGENTS.md).

## The core tension: online API, offline-style ergonomics

abuse-lookup is framed as the sibling of `asn-lookup`, but the two are
architecturally opposite. asn-lookup downloads a bulk database once and answers
every query offline. AbuseIPDB has no bulk download that fits few-IP
investigation, so **every live lookup is a network call against a rate-limited
API** (1000 checks/day on the free tier).

The shared goal — "let AI tools investigate IPs without excessive API calls" — is
therefore met differently here: not with a bulk DB, but with a **local TTL
cache**. During an investigation the same IP is often looked up repeatedly; a
12-hour cache turns those repeats into zero-cost reads. This single decision
shapes the rest of the design.

## One engine, two front-ends

`internal/engine` owns the whole lookup flow: validate the IP, consult the cache,
call the API on a miss, cache the result. The CLI (`internal/app`) and the MCP
server (`internal/mcp`) are thin shells over it. They cannot drift in caching
behaviour, IP validation, or error handling because there is only one
implementation of each.

## Testable without the network

The API client is an interface (`abuseipdb.Client`), not a concrete type, so the
engine and both front-ends are tested against a mock — no live key, no flakiness,
no quota spent in CI-style runs. The production `HTTPClient` funnels `/check` and
`/reports` through one `do()` helper, so 429 handling, rate-limit header parsing,
and error shaping are written once and shared.

## Caching that can't lie

Two failure modes matter for a cache: staleness and corruption.

- **Staleness** is judged by a timestamp stored *inside* the record
  (`FetchedAt`), not the file mtime. mtime is altered by copies and backups; an
  in-record timestamp is the truth regardless of how the file moved.
- **Corruption** is avoided by writing every entry atomically (temp file +
  rename) through `os.Root`. A reader either sees the previous complete file or
  the new complete file, never a half-written one; a malformed file is treated as
  a cache miss, not an error.

The cache key is the tuple `(IP, max-age, verbose)`. These parameters change the
API response, so collapsing them into one key would return wrong data. Reports
are deliberately *not* cached: they are a paginated detail fetch, and pagination
is caller-driven.

## Why the server has no filesystem

A single IP can have hundreds of abuse reports, and `get_reports` returns every
one of them inline. It used to do the opposite: a page above a threshold was
written to a caller-supplied `workspace_root` and only its path came back.

That was the wrong side of the wire, for two reasons. It made the server depend
on the client owning a filesystem it could name — a client speaking MCP over a
transport with no shared disk could not read its own result. And it put the
"too big for the model" judgement in the one process that cannot know the
model's context window; only the client knows that number.

So the split is: **bounding a response is the caller's job** (`per_page`), and
**spilling an oversized response is the client's job**. The server holds no
output directory, takes no path argument, and never writes a file. `total`,
`count` and `has_next_page` make the full set reachable by paging, which is the
property the file branch was really providing.

## Secret handling

The API key authenticates every request, but it is treated as a secret end to
end: sent only via the `Key` header (never a query parameter), never logged, and
never included in an error. It is intentionally not exposed as a command-line
flag, because a flag value lands in shell history and the process list — the key
comes from the environment or the config file only.

## Scope: read-only

AbuseIPDB also exposes write endpoints (`report`, `bulk-report`,
`clear-address`) that submit to the shared, public database. Those are an
outward-facing side effect and are out of scope: abuse-lookup only *reads*
reputation. This keeps the tool safe to expose to an AI over MCP without risking
an unintended contribution to the global dataset.
