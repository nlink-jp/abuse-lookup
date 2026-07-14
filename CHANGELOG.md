# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.0] - 2026-07-14

### Added

- Initial implementation.
- `check` — IP → AbuseIPDB reputation (abuse confidence score, country, usage
  type, ISP, domain, report counts, last-reported date, whitelist/Tor flags);
  multiple addresses and stdin input; `--max-age`, `--verbose`, `--refresh`;
  human block and JSON Lines output.
- `reports` — IP → paginated individual abuse reports (`--page`, `--per-page`,
  `--max-age`); human and JSON Lines output.
- `doctor` — report API-key configuration and cache health without spending
  quota.
- `cache` — show cache status; `--clear` to discard.
- `mcp` — local stdio MCP server exposing `get_usage`, `check_ip`,
  `get_reports`, and `cache_status`. `get_usage` returns an embedded operating
  manual (tools, caching model, workspace model, recovery table), and the server
  advertises it via the initialize `instructions` field.
- Local TTL cache keyed by `(IP, max-age, verbose)`: fresh results are served
  without spending API quota; per-IP JSON written atomically (temp + rename,
  `os.Root` containment) so a corrupt cache is never read. Configurable via
  `[cache] ttl_hours` (default 12h) and `--refresh`.
- File-mediated large `get_reports` pages: page metadata + a preview are returned
  inline, and the full page is written to a file in an agent-prepared
  `workspace_root` (with `os.Root` symlink containment) so a busy IP cannot flood
  the caller's context.
- Rate-limit handling: `X-RateLimit-Remaining` surfaced with a low-quota warning;
  `429` returned as a clear error with no automatic retry.
- Configuration via sectioned TOML (`~/.config/abuse-lookup/config.toml`) and
  `ABUSEIPDB_API_KEY` / `ABUSE_LOOKUP_KEY` environment variables; the API key is
  sent via the `Key` header and never appears in a URL, log, or error.
- Zero external dependencies (standard library only).
- AbuseIPDB attribution in `version`, `--help`, and the README.
