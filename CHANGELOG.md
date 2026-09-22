# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

- **`make verify-release` now fails closed.** Its last block chained unzip, the
  packaged binary's `--version` and `spctl` with `&&` and ended the whole chain
  in `|| true`, so a zip that did not unpack or a binary that did not run exited
  0 and the upload proceeded. Each step is now judged on its own, the packaged
  binary's `--version` must contain the tag being released, and only the
  informational `spctl` line may be ignored. Matches the org template
  (CONVENTIONS.md §Code Signing → Verifying a release).
- **The Linux archives no longer carry macOS file metadata.** macOS `tar` wrote
  each bundled file's extended attributes (`com.apple.provenance`, and a Dropbox
  attribute where the tree is synced) into the `.tar.gz` twice: as AppleDouble
  `._` members, which GNU tar extracts as stray `._<name>` files beside the real
  ones, and as `LIBARCHIVE.xattr.*` / `SCHILY.xattr.*` pax headers, which it
  reports as unknown keywords. `make package` now archives with
  `COPYFILE_DISABLE=1 tar --no-xattrs`; each setting stops one of the two.
  Archives already published still carry them; the files themselves are
  unaffected.

### Internal

- `make verify-release` also judges each Linux archive: no AppleDouble or other
  macOS metadata members — listed with `--options 'tar:!mac-ext'`, because a
  plain macOS listing folds `._` members away — no extended attributes as pax
  headers, and exactly the canonical binary, `README.md` and `LICENSE`, compared
  in the C locale.

## [0.3.0] - 2026-09-21

### Changed

- **An MCP tool call carrying an argument the tool does not declare now fails
  instead of being quietly ignored.** This is a deliberate behaviour change,
  required by org ADR-021 §4. Until now a misspelt argument was dropped and the
  call ran without it: `refresh_` instead of `refresh` returned a cached
  answer that read as a freshly fetched one, and a misspelt `per_page` returned
  a page of whatever size the server chose. Every tool — including
  `get_usage` and `cache_status`, which take no arguments — now decodes with
  `DisallowUnknownFields` and refuses the call, naming the offending field:
  `arguments: json: unknown field "refresh_"`.

  A malformed argument object is refused for the same reason. The decode error
  used to be discarded along with the unknown field, so `{"ip": 123}` ran as if
  no address had been supplied and came back with "provide 'ip'" — an answer
  that contradicted the request. It now reports the type mismatch.

  Nothing runs before the arguments decode, so a rejected call spends no
  AbuseIPDB quota. Omitting `arguments`, or sending `{}` or `null`, still means
  "no arguments" and is not an error. There is no compatibility shim: an
  argument name this server does not declare has never meant anything, so the
  only fix is to correct it.

### Fixed

- **Every MCP tool input schema is closed.** The schemas omitted
  `additionalProperties: false`, so a mistyped argument read as a legitimate one
  to any client that validates against them. Schemas are now built through a
  single `obj()` helper that sets the flag, and an arch test fails if a tool's
  schema omits it — org ADR-021 §10 requires the test as well as the flag,
  because a rule stated only in prose is re-decided by whoever adds the next
  tool.

## [0.2.1] - 2026-09-21

### Fixed

- A number in the config file was accepted when it was not one. `NaN` passed the
  range check — it fails every comparison, so "reject what is below the floor"
  lets it through — and `Inf` or `1e300` overflowed the duration it became.
  Ranges are now stated from the inside, with a ceiling.

## [0.2.0] - 2026-08-31

### Changed

- **`get_reports` always returns the page inline.** The MCP tool no longer
  writes large pages to a file, and `workspace_root` / `workspace_id` / `limit`
  are gone from its schema along with the `reports_file` / `truncated` /
  `preview` / `note` result fields. The size of one response is bounded by
  `per_page`, and `page` walks the rest — `total` and `count` are unchanged, so
  nothing is dropped.

  Migration: replace a `workspace_root` call plus a file read with a smaller
  `per_page` and, when `has_next_page` is true, a request for `page+1`.

### Removed

- The `[mcp] workspace` config key and the `ABUSE_LOOKUP_WORKSPACE` environment
  variable. The server no longer has an output directory: it touches no
  filesystem, so it works unchanged against a client that has none.

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
