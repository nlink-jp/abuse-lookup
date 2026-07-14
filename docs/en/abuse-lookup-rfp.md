# RFP: abuse-lookup

> Generated: 2026-07-14
> Status: Draft

## 1. Problem Statement

In security investigation and incident response, one frequently needs to quickly
learn the **abuse history and reputation** of an IP address encountered in the
wild — abuse confidence score, report history, usage type, ISP, country.
AbuseIPDB is the go-to service for this, but opening its web UI for every lookup
is inefficient, and there is no way for AI tools (e.g. Claude Code) to
investigate IPs over MCP.

`abuse-lookup` is a **CLI plus local MCP server that wraps the AbuseIPDB API v2**,
letting you pull reputation information for a small number of IPs — exactly when
an investigation actually requires it — with a single command or MCP tool call.
Target users are the org operator and their AI tools (MCP clients). Because the
same IP may be looked up repeatedly in a short window, a TTL-bounded local cache
suppresses duplicate queries and avoids wasting the free-tier budget (1,000
checks/day). Used alongside the offline
[asn-lookup](../../../util-series/asn-lookup) (AS / country) — called separately
and cross-referenced — it enables multi-faceted IP investigation.

## 2. Functional Specification

### Commands / API Surface

Single binary + subcommands (following the util-series style; `mcp` subcommand
turns it into an MCP server).

| Command | Role |
|---|---|
| `abuse-lookup check <IP>...` | IP → reputation info. Multiple IPs; reads from stdin when args are omitted. `--max-age <days>` (report look-back window, default 90), `--verbose` (include recent report details), `--refresh` (ignore cache and re-fetch), `-j` / `--json` (JSONL) |
| `abuse-lookup reports <IP>` | Fetch paginated report details (**Phase 2**). Large results are file-mediated |
| `abuse-lookup cache` | Show cache entry count and freshness. `--clear` to discard |
| `abuse-lookup doctor` | Diagnose API-key config, cache state, API connectivity |
| `abuse-lookup mcp` | Start as a local MCP server (stdio) |
| `abuse-lookup version` | Version (`git describe`) |

**MCP tools** (`abuse-lookup mcp`):

| Tool | Role |
|---|---|
| `check_ip` | IP → reputation (score / country / ISP / usageType / total reports / last reported, etc.) |
| `get_reports` | IP → report details (**Phase 2**, large results file-mediated) |
| `cache_status` | Check cache entry count and freshness |
| `get_usage` | Embedded manual (asn-lookup convention; pointed to from `initialize` `instructions`) |

### Input / Output

- Default output is a human-oriented formatted table. `-j` / `--json` emits
  **JSONL** (one record per line, for pipes / machine processing). MCP always
  returns structured JSON.
- stdin support (`cat ips.txt | abuse-lookup check`) to honor the util pipe
  philosophy.
- Primary `check` fields: `ipAddress`, `abuseConfidenceScore`, `countryCode`,
  `usageType`, `isp`, `domain`, `totalReports`, `numDistinctUsers`,
  `lastReportedAt`, `isWhitelisted`, `isPublic`, `isTor`.
- **Rate limiting**: surface the response `X-RateLimit-Remaining` header and warn
  when approaching the limit. **429 (daily limit exceeded) is returned as a clear
  error** with no automatic retry (waiting does not help until the daily reset).
- **On cache hit**, no API call is made; the result indicates it came from cache
  (with fetch timestamp).

### Configuration

Secrets are never committed. Sectioned TOML (per asn-lookup / lite-series
conventions).

- API key: `ABUSEIPDB_API_KEY` env var > `[abuseipdb] key = "..."` in
  `~/.config/abuse-lookup/config.toml`
- Cache TTL: `[cache] ttl_hours = 12` (default 12 hours; `--refresh` overrides
  per call)
- Cache location: default `~/.local/share/abuse-lookup/cache/`

### External Dependencies

- The only runtime external service is the **AbuseIPDB API v2**
  (`https://api.abuseipdb.com/api/v2/check` etc.; API key sent on every request
  via the `Key:` header). Unlike asn-lookup, **network access is required at query
  time** (no offline mode).
- Go standard library only: `net/http` (API calls), `net/netip` (IP validation),
  `encoding/json` (responses / cache). **Zero external dependencies.**
- Cache: one JSON file per IP. **Atomic write (temp write → `rename`)** with
  `os.Root` containment so a corrupt cache is never read. Cache key is the tuple
  `IP + max-age + verbose` (differing params yield different responses).

## 3. Design Decisions

- **Go**: single-binary distribution, established macOS signing + notarization
  flow. `net/netip` for IP validation, `net/http` + `encoding/json` for the API —
  all standard library. **Zero external dependencies** (same stance as
  asn-lookup / nlk / mcp-guardian).
- **Shared engine**: config + HTTP client + cache bundled into an engine shared by
  the CLI and MCP so their behavior cannot diverge (asn-lookup pattern).
- **HTTP client behind an interface** for mocking in tests (testable without
  hitting the real API). Same design as asn-lookup's `Fetcher` interface.
- **API key is secret**: sent on every request via the `Key:` header but never
  logged or surfaced in errors (`Redact` equivalent).
- **Complements existing tools**: the online **reputation layer** paired with the
  offline asn-lookup (AS / country). Serves as IOC-enrichment infrastructure for
  cybersecurity-series (ai-ir2 / mail-analyzer / cti-graph / ioc-collector) —
  e.g. annotating collector-gathered IPs with an abuse score. Called separately;
  abuse-lookup does not depend on asn-lookup.
- **Out of scope**:
  - `blacklist` (bulk malicious-IP download) — the goal is few-IP investigation,
    not bulk retrieval
  - `check-block` (CIDR checks, a paid feature)
  - **Reporting endpoints (`report` / `bulk-report` / `clear-address`)** — writes
    to a shared database, an outward-facing side-effecting operation. v1 is
    investigation (read-only) only. If ever added, CLI-only behind an explicit
    gate, never exposed to an AI over MCP
  - reverse DNS / PTR / whois / RDAP (asn-lookup's / another tool's domain)
  - Redistribution of the cache (i.e. AbuseIPDB data) — local/private only

## 4. Development Plan

### Phase 1: Core

- `net/netip`-based IP validation, AbuseIPDB `check` client with the `Key:` header
  (behind an interface), response struct mapping.
- TTL cache (per-IP JSON, atomic write, `os.Root` containment, key =
  `IP + max-age + verbose`).
- `check` (multiple IPs / stdin / `--max-age` / `--verbose` / `--refresh` / `-j`),
  `doctor`, `cache` subcommands.
- config (sectioned TOML: `[abuseipdb] key` / `[cache] ttl_hours`), env override,
  API-key redaction.
- Rate-limit header surfacing + explicit 429 error.
- Pure functions, injectable dependencies (HTTP behind an interface, mocked in
  tests). Testable without the real API. **Independently reviewable.**

### Phase 2: Features

- `mcp` subcommand (stdio MCP: `check_ip` / `cache_status` / `get_usage`). Port
  the data-toolbox-mcp skeleton.
- `reports` (paginated report details) via CLI + a `get_reports` MCP tool. Large
  results are **file-mediated** (agent-provided `workspace_root`, asn-lookup
  pattern).
- Finish JSONL output / stdin, freshness / cache warnings.
- **Independently reviewable.**

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md / AGENTS.md, LICENSE (MIT).
- **AbuseIPDB credit / terms attribution** in output and READMEs.
- Makefile (`make build` / `make build-all`), signing + notarization, GitHub
  release, umbrella submodule pointer update, org profile addition, `check-org.sh`.

## 5. Required API Scopes / Permissions

- A single **AbuseIPDB free-account API key** only.
- No OAuth / privileged IAM roles. The key is used solely for `check` / `reports`
  queries (reporting endpoints are never called).

## 6. Series Placement

Series: cybersecurity-series
Reason: abuse confidence score / report history is reputation / threat
intelligence itself, best grouped as **IOC-enrichment infrastructure** for
ai-ir2 / mail-analyzer / cti-graph / ioc-collector. Although it pairs with the
offline asn-lookup (util-series), abuse-lookup is a security-specific reputation
layer, so it belongs in cybersecurity-series. Confirmed by the user.

## 7. External Platform Constraints

- AbuseIPDB **free tier is 1,000 checks/day** (HTTP 429 on exceed, daily reset).
  Surface the `X-RateLimit-Remaining` header and warn near the limit; no automatic
  retry.
- Responses depend on the **AbuseIPDB API v2 schema** (`data.abuseConfidenceScore`
  etc.).
- Per the terms of service, the **AbuseIPDB credit** must appear in the READMEs.
  Retrieved data (the cache) is local/private only and is **not redistributed**.
- API key required. **Network required at query time** (no offline mode, in
  contrast to asn-lookup).

---

## Discussion Log

- **Inception**: conceived as a tool to pull reputation for a small number of
  encountered IPs from the CLI / MCP using the AbuseIPDB API. A sister product to
  asn-lookup (shared goal: "IP investigation from AI tools"), but structurally
  different in that it is an **online REST API with a per-request API key and rate
  limits**, not an offline DB.
- **Different over-call mitigation**: asn-lookup achieved this by holding a bulk
  DB; abuse-lookup instead uses a **TTL local cache** (real-time freshness
  required), suppressing duplicate queries and daily-budget waste.
- **Scope fixed (read-only investigation)**: `check` is the core; `blacklist` bulk
  download and `check-block` are out. **Reporting endpoints (`report` etc.) are a
  shared-DB write — an outward side-effecting operation — and are Out of scope.**
  v1 is investigation only.
- **`reports`**: `check --verbose` already returns recent reports, but the
  dedicated paginated fetch is placed in **Phase 2**, with large MCP results
  file-mediated.
- **Cache design**: TTL configurable (`[cache] ttl_hours`, default 12h),
  `--refresh` overrides per call. Stored as per-IP JSON with **atomic write (temp
  → rename)** so a corrupt cache is never read. Key is the tuple
  `IP + max-age + verbose`.
- **Rate-limit policy**: surface `X-RateLimit-Remaining` and warn near the limit;
  return 429 as a clear error. **No auto-retry**, since waiting does not help until
  the daily reset.
- **Zero external dependencies**: only `net/http` + `net/netip` + `encoding/json`.
  HTTP client behind an interface for mocking in tests (same as asn-lookup's
  `Fetcher`).
- **Series**: considered util-series (beside asn-lookup) vs cybersecurity-series
  (IOC-enrichment infra). Given the reputation / threat-intel character, fixed to
  **cybersecurity-series**.
- **Tool name**: chose `abuse-lookup` (`ip-reputation` / `abuseipdb-cli` were
  alternatives) — the `<thing>-lookup` pattern paired with asn-lookup.
