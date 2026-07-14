package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nlink-jp/abuse-lookup/internal/abuseipdb"
	"github.com/nlink-jp/abuse-lookup/internal/workspace"
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
	"Large get_reports results are file-mediated: pass a writable workspace_root and read the returned reports_file. " +
	"The daily free quota is limited (1000 checks); a rate-limit error means wait for the daily reset. " +
	"Call get_usage for the full tool reference and error-recovery table."

// defaultReportsPreview is how many reports are inlined before a file is written.
const defaultReportsPreview = 25

// toolsList returns the advertised tool set with JSON Schema for each input.
func (s *server) toolsList() any {
	strArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{
		"tools": []map[string]any{
			{
				"name":        "get_usage",
				"description": "Return this server's operating manual (markdown): the tools, the caching model, the workspace model for file-mediated results, rate limits, and the error-recovery table. Call it once before first use.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			},
			{
				"name": "check_ip",
				"description": "Check the AbuseIPDB reputation (abuse confidence score, country, ISP, usage type, report counts, last-reported date) of one or more IP addresses. " +
					"Results are served from a local TTL cache when fresh; pass refresh=true to force a live lookup.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ip":      map[string]any{"type": "string", "description": "A single IPv4 or IPv6 address."},
						"ips":     strArray,
						"max_age": map[string]any{"type": "integer", "description": "Report look-back window in days (default 90)."},
						"verbose": map[string]any{"type": "boolean", "description": "Include recent report details in each result."},
						"refresh": map[string]any{"type": "boolean", "description": "Ignore the cache and re-fetch from the API."},
					},
				},
			},
			{
				"name": "get_reports",
				"description": "Fetch one page of the individual abuse reports for a single IP. " +
					"Always returns page metadata (total, page, count, has_next_page). Small pages inline the reports; large pages are NOT inlined — the full page is written to a file in the workspace and its path is returned (truncated=true). " +
					"Pagination is caller-driven via page / per_page. To receive the file in a sandbox, pass a writable workspace_root.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ip":             map[string]any{"type": "string", "description": "A single IPv4 or IPv6 address (required)."},
						"max_age":        map[string]any{"type": "integer", "description": "Report look-back window in days (default 90)."},
						"page":           map[string]any{"type": "integer", "description": "1-based page number (default 1)."},
						"per_page":       map[string]any{"type": "integer", "description": "Reports per page (default 25)."},
						"limit":          map[string]any{"type": "integer", "description": "Max reports to inline before writing a file (default 25)."},
						"workspace_root": map[string]any{"type": "string", "description": "Absolute path to an agent-prepared directory for the output file; omit to use the server default."},
						"workspace_id":   map[string]any{"type": "string", "description": "Optional single-segment subdirectory under the workspace root."},
					},
				},
			},
			{
				"name":        "cache_status",
				"description": "Report the local cache directory, TTL, and how many cached reputation entries exist (and how many are still fresh).",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
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
		return textResult(false, usageMarkdown), nil
	case "check_ip":
		return s.toolCheckIP(ctx, p.Arguments), nil
	case "get_reports":
		return s.toolGetReports(ctx, p.Arguments), nil
	case "cache_status":
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
	_ = json.Unmarshal(args, &a)
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

// reportsEntry is the file-mediated reports result. Small pages inline Reports;
// large pages inline a Preview and write the full page to a file.
type reportsEntry struct {
	Input       string             `json:"input"`
	Total       int                `json:"total"`
	Page        int                `json:"page"`
	Count       int                `json:"count"`
	PerPage     int                `json:"per_page"`
	HasNextPage bool               `json:"has_next_page"`
	Truncated   bool               `json:"truncated"`
	Reports     []abuseipdb.Report `json:"reports,omitempty"`
	Preview     []abuseipdb.Report `json:"preview,omitempty"`
	ReportsFile string             `json:"reports_file,omitempty"`
	Note        string             `json:"note,omitempty"`
}

func (s *server) toolGetReports(ctx context.Context, args json.RawMessage) toolResult {
	var a struct {
		IP            string `json:"ip"`
		MaxAge        *int   `json:"max_age"`
		Page          *int   `json:"page"`
		PerPage       *int   `json:"per_page"`
		Limit         *int   `json:"limit"`
		WorkspaceRoot string `json:"workspace_root"`
		WorkspaceID   string `json:"workspace_id"`
	}
	_ = json.Unmarshal(args, &a)
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
	preview := defaultReportsPreview
	if a.Limit != nil && *a.Limit >= 0 {
		preview = *a.Limit
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
	if len(pg.Results) <= preview {
		e.Reports = pg.Results
	} else {
		e.Truncated = true
		e.Preview = pg.Results[:preview]
		ws, werr := s.ws.EnsureIn(a.WorkspaceRoot, a.WorkspaceID)
		if werr != nil {
			e.Note = "full page not written: " + werr.Error() + " — pass a writable 'workspace_root'"
		} else if path, err := writeReportsFile(ws, a.IP, page, pg.Results); err != nil {
			e.Note = "full page not written: " + err.Error()
		} else {
			e.ReportsFile = path
		}
	}
	return jsonResult([]reportsEntry{e})
}

// writeReportsFile writes the full page of reports as JSON into the workspace.
func writeReportsFile(ws *workspace.Workspace, ip string, page int, reports []abuseipdb.Report) (string, error) {
	b, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return "", err
	}
	safe := strings.NewReplacer(":", "-", "/", "-").Replace(ip)
	name := fmt.Sprintf("reports-%s-p%d.json", safe, page)
	return ws.WriteFileAtomic(name, b)
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
