package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/abuse-lookup/internal/abuseipdb"
	"github.com/nlink-jp/abuse-lookup/internal/config"
	"github.com/nlink-jp/abuse-lookup/internal/engine"
)

type mockClient struct {
	check   *abuseipdb.CheckResult
	reports *abuseipdb.ReportsPage
	checks  int
}

func (m *mockClient) Check(_ context.Context, ip string, _ int, _ bool) (*abuseipdb.CheckResult, *abuseipdb.RateLimit, error) {
	m.checks++
	r := *m.check
	r.IPAddress = ip
	return &r, &abuseipdb.RateLimit{Remaining: 900}, nil
}

func (m *mockClient) Reports(_ context.Context, _ string, _, _, _ int) (*abuseipdb.ReportsPage, *abuseipdb.RateLimit, error) {
	return m.reports, &abuseipdb.RateLimit{Remaining: 900}, nil
}

func makeReports(n int) []abuseipdb.Report {
	out := make([]abuseipdb.Report, n)
	for i := range out {
		out[i] = abuseipdb.Report{
			ReportedAt: "2026-07-10T00:00:00+00:00",
			Comment:    fmt.Sprintf("report %d", i),
			Categories: []int{18},
		}
	}
	return out
}

func newEngine(t *testing.T, mc *mockClient) *engine.Engine {
	t.Helper()
	cfg := &config.Config{
		BaseURL:   "https://example.test",
		CacheDir:  t.TempDir(),
		CacheTTL:  time.Hour,
		Workspace: filepath.Join(t.TempDir(), "ws"),
		APIKey:    "x",
	}
	return engine.New(cfg, mc)
}

type rawResp struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func drive(t *testing.T, e *engine.Engine, requests ...string) []rawResp {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	if err := Serve(context.Background(), e, "test-ver", in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resps []rawResp
	dec := json.NewDecoder(&out)
	for {
		var r rawResp
		if err := dec.Decode(&r); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode response: %v (buffer: %s)", err, out.String())
		}
		resps = append(resps, r)
	}
	return resps
}

func callText(t *testing.T, result json.RawMessage) (string, bool) {
	t.Helper()
	var tr toolResult
	if err := json.Unmarshal(result, &tr); err != nil {
		t.Fatalf("unmarshal toolResult: %v", err)
	}
	if len(tr.Content) == 0 {
		t.Fatal("empty content")
	}
	return tr.Content[0].Text, tr.IsError
}

func TestServeSequence(t *testing.T) {
	mc := &mockClient{
		check:   &abuseipdb.CheckResult{AbuseConfidenceScore: 100, CountryCode: "CN"},
		reports: &abuseipdb.ReportsPage{Total: 2, Page: 1, Count: 2, PerPage: 25, Results: makeReports(2)},
	}
	e := newEngine(t, mc)
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // no response expected
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"check_ip","arguments":{"ip":"8.8.8.8"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_reports","arguments":{"ip":"8.8.8.8"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"cache_status"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping"}`,
	)
	if len(resps) != 6 {
		t.Fatalf("got %d responses, want 6 (notification must be silent)", len(resps))
	}

	var initRes struct {
		ServerInfo   struct{ Name string } `json:"serverInfo"`
		Instructions string                `json:"instructions"`
	}
	json.Unmarshal(resps[0].Result, &initRes)
	if initRes.ServerInfo.Name != "abuse-lookup" {
		t.Errorf("serverInfo.name = %q", initRes.ServerInfo.Name)
	}
	if !strings.Contains(initRes.Instructions, "get_usage") {
		t.Errorf("instructions should mention get_usage: %q", initRes.Instructions)
	}

	var listRes struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	json.Unmarshal(resps[1].Result, &listRes)
	if len(listRes.Tools) != 4 {
		t.Errorf("tools = %d, want 4", len(listRes.Tools))
	}

	text, isErr := callText(t, resps[2].Result)
	if isErr || !strings.Contains(text, `"abuseConfidenceScore": 100`) || !strings.Contains(text, `"cached": false`) {
		t.Errorf("check_ip text = %s (isErr=%v)", text, isErr)
	}

	text, _ = callText(t, resps[3].Result)
	if !strings.Contains(text, `"total": 2`) || !strings.Contains(text, `"truncated": false`) {
		t.Errorf("get_reports text = %s", text)
	}

	text, _ = callText(t, resps[4].Result)
	if !strings.Contains(text, `"entries": 1`) {
		t.Errorf("cache_status text = %s", text)
	}
}

func TestCheckIPCacheAcrossCalls(t *testing.T) {
	mc := &mockClient{check: &abuseipdb.CheckResult{AbuseConfidenceScore: 50}}
	e := newEngine(t, mc)
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_ip","arguments":{"ip":"1.2.3.4"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"check_ip","arguments":{"ip":"1.2.3.4"}}}`,
	)
	t1, _ := callText(t, resps[0].Result)
	t2, _ := callText(t, resps[1].Result)
	if !strings.Contains(t1, `"cached": false`) {
		t.Errorf("first call should not be cached: %s", t1)
	}
	if !strings.Contains(t2, `"cached": true`) {
		t.Errorf("second call should be cached: %s", t2)
	}
	if mc.checks != 1 {
		t.Errorf("client Check called %d times, want 1", mc.checks)
	}
}

func TestCheckIPInvalid(t *testing.T) {
	mc := &mockClient{check: &abuseipdb.CheckResult{}}
	e := newEngine(t, mc)
	resps := drive(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_ip","arguments":{"ip":"not-an-ip"}}}`,
	)
	text, isErr := callText(t, resps[0].Result)
	if isErr {
		t.Fatalf("per-entry error should not mark whole result as error: %s", text)
	}
	if !strings.Contains(text, `"error"`) || !strings.Contains(text, "not-an-ip") {
		t.Errorf("expected per-entry error: %s", text)
	}
}

func TestGetReportsWritesFileWhenLarge(t *testing.T) {
	mc := &mockClient{reports: &abuseipdb.ReportsPage{
		Total: 100, Page: 1, Count: 30, PerPage: 30,
		NextPageURL: "/api/v2/reports?page=2", Results: makeReports(30),
	}}
	e := newEngine(t, mc)
	wsRoot := t.TempDir()
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_reports","arguments":{"ip":"1.2.3.4","per_page":30,"limit":10,"workspace_root":%q}}}`, wsRoot)
	resps := drive(t, e, req)
	text, isErr := callText(t, resps[0].Result)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, text)
	}
	e0 := entries[0]
	if e0["truncated"] != true || e0["has_next_page"] != true {
		t.Errorf("expected truncated + has_next_page: %v", e0)
	}
	rf, _ := e0["reports_file"].(string)
	if rf == "" || !strings.HasPrefix(rf, wsRoot) {
		t.Fatalf("reports_file %q not under workspace %q", rf, wsRoot)
	}
	data, err := os.ReadFile(rf)
	if err != nil {
		t.Fatalf("read reports_file: %v", err)
	}
	var written []abuseipdb.Report
	if err := json.Unmarshal(data, &written); err != nil || len(written) != 30 {
		t.Errorf("written file should hold 30 reports, got %d (err %v)", len(written), err)
	}
}

func TestGetReportsBadWorkspaceGivesNote(t *testing.T) {
	mc := &mockClient{reports: &abuseipdb.ReportsPage{
		Total: 100, Page: 1, Count: 30, PerPage: 30, Results: makeReports(30),
	}}
	e := newEngine(t, mc)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_reports","arguments":{"ip":"1.2.3.4","per_page":30,"limit":10,"workspace_root":"relative/not/absolute"}}}`
	resps := drive(t, e, req)
	text, _ := callText(t, resps[0].Result)
	var entries []map[string]any
	json.Unmarshal([]byte(text), &entries)
	e0 := entries[0]
	if _, ok := e0["reports_file"]; ok {
		t.Errorf("should not have written a file: %v", e0)
	}
	if note, _ := e0["note"].(string); !strings.Contains(note, "workspace_root") {
		t.Errorf("expected note about workspace_root, got %q", e0["note"])
	}
	if e0["total"].(float64) != 100 {
		t.Errorf("total = %v", e0["total"])
	}
}

func TestGetUsageManual(t *testing.T) {
	e := newEngine(t, &mockClient{check: &abuseipdb.CheckResult{}})
	resps := drive(t, e, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_usage"}}`)
	text, isErr := callText(t, resps[0].Result)
	if isErr || !strings.Contains(text, "Recovery table") || !strings.Contains(text, "workspace_root") {
		t.Errorf("get_usage manual incomplete: isErr=%v", isErr)
	}
}

func TestUnknownMethod(t *testing.T) {
	e := newEngine(t, &mockClient{check: &abuseipdb.CheckResult{}})
	resps := drive(t, e, `{"jsonrpc":"2.0","id":9,"method":"bogus/method"}`)
	if resps[0].Error == nil || resps[0].Error.Code != -32601 {
		t.Errorf("expected -32601 method not found, got %+v", resps[0].Error)
	}
}
