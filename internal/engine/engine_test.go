package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nlink-jp/abuse-lookup/internal/abuseipdb"
	"github.com/nlink-jp/abuse-lookup/internal/config"
)

type mockClient struct {
	calls        int
	result       *abuseipdb.CheckResult
	rl           *abuseipdb.RateLimit
	err          error
	reportsPage  *abuseipdb.ReportsPage
	reportsCalls int
}

func (m *mockClient) Check(_ context.Context, ip string, _ int, _ bool) (*abuseipdb.CheckResult, *abuseipdb.RateLimit, error) {
	m.calls++
	if m.err != nil {
		return nil, m.rl, m.err
	}
	res := *m.result
	res.IPAddress = ip
	return &res, m.rl, nil
}

func (m *mockClient) Reports(_ context.Context, _ string, _, _, _ int) (*abuseipdb.ReportsPage, *abuseipdb.RateLimit, error) {
	m.reportsCalls++
	if m.err != nil {
		return nil, m.rl, m.err
	}
	return m.reportsPage, m.rl, nil
}

func newEngine(t *testing.T, client abuseipdb.Client) *Engine {
	t.Helper()
	cfg := &config.Config{
		BaseURL:  "https://example.test",
		CacheDir: t.TempDir(),
		CacheTTL: time.Hour,
		APIKey:   "x",
	}
	return New(cfg, client)
}

func TestCacheHitSkipsClient(t *testing.T) {
	mc := &mockClient{result: &abuseipdb.CheckResult{AbuseConfidenceScore: 42}, rl: &abuseipdb.RateLimit{Remaining: 900}}
	e := newEngine(t, mc)

	o1, err := e.Check(context.Background(), "1.2.3.4", 90, false, false)
	if err != nil {
		t.Fatalf("first Check: %v", err)
	}
	if o1.FromCache {
		t.Error("first lookup should not be cached")
	}
	if o1.Result.AbuseConfidenceScore != 42 {
		t.Errorf("score = %d", o1.Result.AbuseConfidenceScore)
	}

	o2, err := e.Check(context.Background(), "1.2.3.4", 90, false, false)
	if err != nil {
		t.Fatalf("second Check: %v", err)
	}
	if !o2.FromCache {
		t.Error("second lookup should be a cache hit")
	}
	if mc.calls != 1 {
		t.Errorf("client called %d times, want 1", mc.calls)
	}
}

func TestRefreshForcesFetch(t *testing.T) {
	mc := &mockClient{result: &abuseipdb.CheckResult{AbuseConfidenceScore: 1}}
	e := newEngine(t, mc)
	if _, err := e.Check(context.Background(), "1.2.3.4", 90, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Check(context.Background(), "1.2.3.4", 90, false, true); err != nil {
		t.Fatal(err)
	}
	if mc.calls != 2 {
		t.Errorf("client called %d times, want 2 (refresh bypasses cache)", mc.calls)
	}
}

func TestInvalidIP(t *testing.T) {
	mc := &mockClient{result: &abuseipdb.CheckResult{}}
	e := newEngine(t, mc)
	if _, err := e.Check(context.Background(), "not-an-ip", 90, false, false); err == nil {
		t.Fatal("expected error for invalid IP")
	}
	if mc.calls != 0 {
		t.Errorf("client called %d times for invalid IP, want 0", mc.calls)
	}
}

func TestRateLimitedPassthrough(t *testing.T) {
	mc := &mockClient{
		err: abuseipdb.ErrRateLimited,
		rl:  &abuseipdb.RateLimit{Remaining: 0, Reset: 3600},
	}
	e := newEngine(t, mc)
	o, err := e.Check(context.Background(), "1.2.3.4", 90, false, false)
	if !errors.Is(err, abuseipdb.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if o == nil || o.RateLimit == nil || o.RateLimit.Reset != 3600 {
		t.Errorf("rate limit not surfaced: %+v", o)
	}
}

func TestReports(t *testing.T) {
	mc := &mockClient{
		result: &abuseipdb.CheckResult{},
		reportsPage: &abuseipdb.ReportsPage{
			Total: 2, Page: 1, Count: 2, PerPage: 25,
			Results: []abuseipdb.Report{{Comment: "a"}, {Comment: "b"}},
		},
	}
	e := newEngine(t, mc)
	o, err := e.Reports(context.Background(), "1.2.3.4", 90, 1, 25)
	if err != nil {
		t.Fatalf("Reports: %v", err)
	}
	if o.Page == nil || o.Page.Total != 2 || len(o.Page.Results) != 2 {
		t.Errorf("reports page = %+v", o.Page)
	}
	if mc.reportsCalls != 1 {
		t.Errorf("reports calls = %d, want 1 (not cached)", mc.reportsCalls)
	}
	// Reports are not cached: a second call hits the client again.
	if _, err := e.Reports(context.Background(), "1.2.3.4", 90, 1, 25); err != nil {
		t.Fatal(err)
	}
	if mc.reportsCalls != 2 {
		t.Errorf("reports calls = %d, want 2 (reports uncached)", mc.reportsCalls)
	}
}

func TestReportsInvalidIP(t *testing.T) {
	mc := &mockClient{result: &abuseipdb.CheckResult{}}
	e := newEngine(t, mc)
	if _, err := e.Reports(context.Background(), "bad", 90, 1, 25); err == nil {
		t.Fatal("expected error for invalid IP")
	}
	if mc.reportsCalls != 0 {
		t.Errorf("reports calls = %d, want 0", mc.reportsCalls)
	}
}

func TestCanonicalizesIP(t *testing.T) {
	mc := &mockClient{result: &abuseipdb.CheckResult{}}
	e := newEngine(t, mc)
	// Leading zeros / uppercase canonicalize; a second call in canonical form
	// must hit the same cache entry.
	if _, err := e.Check(context.Background(), "2001:DB8::1", 90, false, false); err != nil {
		t.Fatal(err)
	}
	o, err := e.Check(context.Background(), "2001:db8::1", 90, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !o.FromCache {
		t.Error("canonical-equivalent IP should hit cache")
	}
	if mc.calls != 1 {
		t.Errorf("client called %d times, want 1", mc.calls)
	}
}
