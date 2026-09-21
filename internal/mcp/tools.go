package mcp

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/nlink-jp/abuse-lookup/internal/abuseipdb"
)

// usageMarkdown is the operating manual returned by the get_usage tool. Its
// coherence with the real tools/results is pinned by usage_test.go.
//
//go:embed usage.md
var usageMarkdown string

// Instructions is the initialize-time hint (surfaced via the MCP `instructions`
// field) that makes get_usage discoverable and steers clients away from common
// errors.
const Instructions = "abuse-lookup checks IP reputation via the AbuseIPDB API (online; an API key must be configured). " +
	"Results are cached locally with a TTL, so repeated check_ip calls do not re-spend the daily quota. " +
	"get_reports returns one page inline; walk large report sets with page / per_page. " +
	"The daily free quota is limited (1000 checks); a rate-limit error means wait for the daily reset. " +
	"Call get_usage for the full tool reference and error-recovery table."

// obj builds a tool's input schema. Every schema goes through here so that
// org ADR-021 §10's `additionalProperties: false` is set once instead of being
// remembered per tool — the next tool added gets the closed schema for free.
func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// decodeArgs decodes a tool's arguments strictly: an argument the tool does not
// declare is refused by name, and a malformed argument object is refused rather
// than read as an empty one. Every tool decodes through here.
//
// obj() above is only the declared half of org ADR-021 §4 — what a
// schema-checking client refuses before the call. This is the half that
// actually refuses, and it is needed because not every client checks the
// schema. The `_ = json.Unmarshal` this replaces discarded the decode error as
// well as the unknown field, so both defects were silent in the same way: a
// misspelt `refresh` returned a cached answer that read as a fresh one, and
// `{"ip": 123}` ran as if no address had been given.
func decodeArgs(raw json.RawMessage, into any) error {
	raw = bytes.TrimSpace(raw)
	// Omitted or null arguments mean the empty object, not an error: a tool
	// whose arguments are all optional is legitimately called with none.
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return errors.New("arguments: " + err.Error())
	}
	return nil
}

// toolsList returns the advertised tool set with JSON Schema for each input.
func (s *server) toolsList() any {
	strArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{
		"tools": []map[string]any{
			{
				"name":        "get_usage",
				"description": "Return this server's operating manual (markdown): the tools, the caching model, pagination, rate limits, and the error-recovery table. Call it once before first use.",
				"inputSchema": obj(map[string]any{}),
			},
			{
				"name": "check_ip",
				"description": "Check the AbuseIPDB reputation (abuse confidence score, country, ISP, usage type, report counts, last-reported date) of one or more IP addresses. " +
					"Results are served from a local TTL cache when fresh; pass refresh=true to force a live lookup.",
				"inputSchema": obj(map[string]any{
					"ip":      map[string]any{"type": "string", "description": "A single IPv4 or IPv6 address."},
					"ips":     strArray,
					"max_age": map[string]any{"type": "integer", "description": "Report look-back window in days (default 90)."},
					"verbose": map[string]any{"type": "boolean", "description": "Include recent report details in each result."},
					"refresh": map[string]any{"type": "boolean", "description": "Ignore the cache and re-fetch from the API."},
				}),
			},
			{
				"name": "get_reports",
				"description": "Fetch one page of the individual abuse reports for a single IP. " +
					"The page is always returned inline, together with its metadata (total, page, count, has_next_page). " +
					"Pagination is caller-driven via page / per_page: keep a page small enough for your context and walk large report sets with page.",
				"inputSchema": obj(map[string]any{
					"ip":       map[string]any{"type": "string", "description": "A single IPv4 or IPv6 address (required)."},
					"max_age":  map[string]any{"type": "integer", "description": "Report look-back window in days (default 90)."},
					"page":     map[string]any{"type": "integer", "description": "1-based page number (default 1)."},
					"per_page": map[string]any{"type": "integer", "description": "Reports per page (default 25)."},
				}),
			},
			{
				"name":        "cache_status",
				"description": "Report the local cache directory, TTL, and how many cached reputation entries exist (and how many are still fresh).",
				"inputSchema": obj(map[string]any{}),
			},
		},
	}
}

func (s *server) toolsCall(ctx context.Context, params json.RawMessage) (toolResult, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolResult{}, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	switch p.Name {
	case "get_usage":
		// No arguments — which still means "none", not "any".
		if err := decodeArgs(p.Arguments, &struct{}{}); err != nil {
			return textResult(true, err.Error()), nil
		}
		return textResult(false, usageMarkdown), nil
	case "check_ip":
		return s.toolCheckIP(ctx, p.Arguments), nil
	case "get_reports":
		return s.toolGetReports(ctx, p.Arguments), nil
	case "cache_status":
		if err := decodeArgs(p.Arguments, &struct{}{}); err != nil {
			return textResult(true, err.Error()), nil
		}
		return s.toolCacheStatus(), nil
	default:
		return toolResult{}, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}
}

// checkEntry embeds the reputation result so its fields inline into the JSON;
// on error the embedded pointer is nil and only input/error appear.
type checkEntry struct {
	Input  string `json:"input"`
	Error  string `json:"error,omitempty"`
	Cached bool   `json:"cached"`
	*abuseipdb.CheckResult
}

func (s *server) toolCheckIP(ctx context.Context, args json.RawMessage) toolResult {
	var a struct {
		IP      string   `json:"ip"`
		IPs     []string `json:"ips"`
		MaxAge  *int     `json:"max_age"`
		Verbose bool     `json:"verbose"`
		Refresh bool     `json:"refresh"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return textResult(true, err.Error())
	}
	inputs := a.IPs
	if a.IP != "" {
		inputs = append([]string{a.IP}, inputs...)
	}
	if len(inputs) == 0 {
		return textResult(true, "provide 'ip' (string) or 'ips' (array of strings)")
	}
	maxAge := 90
	if a.MaxAge != nil && *a.MaxAge >= 1 {
		maxAge = *a.MaxAge
	}

	entries := make([]checkEntry, 0, len(inputs))
	for _, in := range inputs {
		outcome, err := s.e.Check(ctx, in, maxAge, a.Verbose, a.Refresh)
		if err != nil {
			if errors.Is(err, abuseipdb.ErrRateLimited) {
				return textResult(true, "AbuseIPDB daily rate limit exceeded; wait for the daily reset before retrying")
			}
			entries = append(entries, checkEntry{Input: in, Error: err.Error()})
			continue
		}
		entries = append(entries, checkEntry{Input: in, Cached: outcome.FromCache, CheckResult: outcome.Result})
	}
	return jsonResult(entries)
}

// reportsEntry is one page of reports, always inline. The caller bounds the
// size with per_page and walks the rest with page — the server never spills to
// a file, so it works against a client with no filesystem of its own.
type reportsEntry struct {
	Input       string             `json:"input"`
	Total       int                `json:"total"`
	Page        int                `json:"page"`
	Count       int                `json:"count"`
	PerPage     int                `json:"per_page"`
	HasNextPage bool               `json:"has_next_page"`
	Reports     []abuseipdb.Report `json:"reports"`
}

func (s *server) toolGetReports(ctx context.Context, args json.RawMessage) toolResult {
	var a struct {
		IP      string `json:"ip"`
		MaxAge  *int   `json:"max_age"`
		Page    *int   `json:"page"`
		PerPage *int   `json:"per_page"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return textResult(true, err.Error())
	}
	if a.IP == "" {
		return textResult(true, "provide 'ip' (a single IP address)")
	}
	maxAge := 90
	if a.MaxAge != nil && *a.MaxAge >= 1 {
		maxAge = *a.MaxAge
	}
	page, perPage := 1, 25
	if a.Page != nil && *a.Page >= 1 {
		page = *a.Page
	}
	if a.PerPage != nil && *a.PerPage >= 1 {
		perPage = *a.PerPage
	}
	outcome, err := s.e.Reports(ctx, a.IP, maxAge, page, perPage)
	if err != nil {
		if errors.Is(err, abuseipdb.ErrRateLimited) {
			return textResult(true, "AbuseIPDB daily rate limit exceeded; wait for the daily reset before retrying")
		}
		return textResult(true, err.Error())
	}
	pg := outcome.Page
	e := reportsEntry{
		Input: a.IP, Total: pg.Total, Page: pg.Page, Count: pg.Count,
		PerPage: pg.PerPage, HasNextPage: pg.NextPageURL != "",
	}
	e.Reports = pg.Results
	return jsonResult([]reportsEntry{e})
}

func (s *server) toolCacheStatus() toolResult {
	c := s.e.Cache()
	recs, err := c.List()
	if err != nil {
		return textResult(true, "cache unreadable: "+err.Error())
	}
	fresh := 0
	for _, r := range recs {
		if c.Fresh(r) {
			fresh++
		}
	}
	return jsonResult(map[string]any{
		"cache_dir": c.Dir(),
		"ttl":       c.TTL().String(),
		"entries":   len(recs),
		"fresh":     fresh,
	})
}

// jsonResult marshals v into a non-error text result.
func jsonResult(v any) toolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return textResult(true, "encode result: "+err.Error())
	}
	return textResult(false, string(b))
}
