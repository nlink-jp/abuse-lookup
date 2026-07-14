package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const sampleBody = `{"data":{"ipAddress":"118.25.6.39","isPublic":true,"ipVersion":4,` +
	`"isWhitelisted":false,"abuseConfidenceScore":100,"countryCode":"CN",` +
	`"usageType":"Data Center/Web Hosting/Transit","isp":"Tencent","domain":"tencent.com",` +
	`"hostnames":[],"isTor":false,"totalReports":5,"numDistinctUsers":3,` +
	`"lastReportedAt":"2020-01-02T03:04:05+00:00"}}`

// clearEnv blanks the override env vars so the host environment cannot leak in.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ABUSEIPDB_API_KEY", "ABUSE_LOOKUP_KEY",
		"ABUSE_LOOKUP_BASE_URL", "ABUSE_LOOKUP_CACHE_DIR",
	} {
		t.Setenv(k, "")
	}
}

// testServer returns a stub AbuseIPDB server and a hit counter.
func testServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("X-RateLimit-Limit", "1000")
		w.Header().Set("X-RateLimit-Remaining", "990")
		w.Write([]byte(sampleBody))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// writeConfig writes a config.toml pointing at baseURL with the given key
// (omitted when empty) and a fresh cache dir, and returns its path.
func writeConfig(t *testing.T, baseURL, key string) string {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("[abuseipdb]\n")
	if key != "" {
		b.WriteString("key = \"" + key + "\"\n")
	}
	b.WriteString("base_url = \"" + baseURL + "\"\n\n")
	b.WriteString("[cache]\ndir = \"" + filepath.Join(dir, "cache") + "\"\nttl_hours = 12\n")
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCmdCheckHuman(t *testing.T) {
	clearEnv(t)
	srv, hits := testServer(t)
	cfg := writeConfig(t, srv.URL, "k")
	var out, errw bytes.Buffer

	code := cmdCheck([]string{"--config", cfg, "118.25.6.39"}, &out, &errw, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errw.String())
	}
	s := out.String()
	if !strings.Contains(s, "118.25.6.39") || !strings.Contains(s, "Abuse score:") {
		t.Errorf("human output missing fields:\n%s", s)
	}
	if !strings.Contains(s, "100/100") {
		t.Errorf("score not rendered:\n%s", s)
	}
	if *hits != 1 {
		t.Errorf("server hits = %d, want 1", *hits)
	}
}

func TestCmdCheckJSON(t *testing.T) {
	clearEnv(t)
	srv, _ := testServer(t)
	cfg := writeConfig(t, srv.URL, "k")
	var out, errw bytes.Buffer

	code := cmdCheck([]string{"--config", cfg, "-j", "118.25.6.39"}, &out, &errw, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errw.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatalf("JSONL parse: %v\n%s", err, out.String())
	}
	if m["abuseConfidenceScore"].(float64) != 100 {
		t.Errorf("score = %v", m["abuseConfidenceScore"])
	}
	if m["cached"].(bool) != false {
		t.Errorf("cached = %v, want false", m["cached"])
	}
	if m["ipAddress"] != "118.25.6.39" {
		t.Errorf("ipAddress = %v", m["ipAddress"])
	}
}

func TestCmdCheckCacheHit(t *testing.T) {
	clearEnv(t)
	srv, hits := testServer(t)
	cfg := writeConfig(t, srv.URL, "k")

	for range 2 {
		var out, errw bytes.Buffer
		if code := cmdCheck([]string{"--config", cfg, "118.25.6.39"}, &out, &errw, strings.NewReader("")); code != 0 {
			t.Fatalf("exit = %d, stderr=%s", code, errw.String())
		}
	}
	if *hits != 1 {
		t.Errorf("server hits = %d, want 1 (second lookup cached)", *hits)
	}

	// --refresh must bypass the cache.
	var out, errw bytes.Buffer
	if code := cmdCheck([]string{"--config", cfg, "--refresh", "118.25.6.39"}, &out, &errw, strings.NewReader("")); code != 0 {
		t.Fatalf("refresh exit = %d", code)
	}
	if *hits != 2 {
		t.Errorf("server hits = %d, want 2 after --refresh", *hits)
	}
}

func TestCmdCheckStdin(t *testing.T) {
	clearEnv(t)
	srv, hits := testServer(t)
	cfg := writeConfig(t, srv.URL, "k")
	var out, errw bytes.Buffer

	stdin := strings.NewReader("118.25.6.39\n# comment\n8.8.8.8\n")
	code := cmdCheck([]string{"--config", cfg}, &out, &errw, stdin)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errw.String())
	}
	if *hits != 2 {
		t.Errorf("server hits = %d, want 2 (two IPs from stdin)", *hits)
	}
}

func TestCmdCheckNoKey(t *testing.T) {
	clearEnv(t)
	srv, hits := testServer(t)
	cfg := writeConfig(t, srv.URL, "") // no key
	var out, errw bytes.Buffer

	code := cmdCheck([]string{"--config", cfg, "1.2.3.4"}, &out, &errw, strings.NewReader(""))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errw.String(), "API key") {
		t.Errorf("stderr should mention API key:\n%s", errw.String())
	}
	if *hits != 0 {
		t.Errorf("server hits = %d, want 0 (no key, no call)", *hits)
	}
}

func TestCmdCheckInvalidIP(t *testing.T) {
	clearEnv(t)
	srv, hits := testServer(t)
	cfg := writeConfig(t, srv.URL, "k")
	var out, errw bytes.Buffer

	code := cmdCheck([]string{"--config", cfg, "not-an-ip"}, &out, &errw, strings.NewReader(""))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errw.String(), "not-an-ip") {
		t.Errorf("stderr should name the bad IP:\n%s", errw.String())
	}
	if *hits != 0 {
		t.Errorf("server hits = %d, want 0 (invalid IP not sent)", *hits)
	}
}

func TestCmdDoctor(t *testing.T) {
	clearEnv(t)
	srv, _ := testServer(t)
	cfg := writeConfig(t, srv.URL, "k")
	var out, errw bytes.Buffer

	code := cmdDoctor([]string{"--config", cfg}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errw.String())
	}
	s := out.String()
	if !strings.Contains(s, "API key:     configured") || !strings.Contains(s, "Status:      ok") {
		t.Errorf("doctor output:\n%s", s)
	}
}

func TestCmdDoctorNoKey(t *testing.T) {
	clearEnv(t)
	cfg := writeConfig(t, "https://example.test", "")
	var out, errw bytes.Buffer
	if code := cmdDoctor([]string{"--config", cfg}, &out, &errw); code != 1 {
		t.Fatalf("exit = %d, want 1 (no key)", code)
	}
	if !strings.Contains(out.String(), "NOT configured") {
		t.Errorf("doctor should flag missing key:\n%s", out.String())
	}
}

func TestCmdCacheShowAndClear(t *testing.T) {
	clearEnv(t)
	srv, _ := testServer(t)
	cfg := writeConfig(t, srv.URL, "k")

	// Populate the cache with a lookup.
	var b1, e1 bytes.Buffer
	if code := cmdCheck([]string{"--config", cfg, "118.25.6.39"}, &b1, &e1, strings.NewReader("")); code != 0 {
		t.Fatalf("seed check exit = %d", code)
	}

	var out, errw bytes.Buffer
	if code := cmdCache([]string{"--config", cfg}, &out, &errw); code != 0 {
		t.Fatalf("cache show exit = %d", code)
	}
	if !strings.Contains(out.String(), "Entries:   1") || !strings.Contains(out.String(), "118.25.6.39") {
		t.Errorf("cache show:\n%s", out.String())
	}

	var cout, cerr bytes.Buffer
	if code := cmdCache([]string{"--config", cfg, "--clear"}, &cout, &cerr); code != 0 {
		t.Fatalf("cache clear exit = %d", code)
	}
	if !strings.Contains(cout.String(), "cleared 1") {
		t.Errorf("clear output: %s", cout.String())
	}
}

const reportsBody = `{"data":{"total":42,"page":1,"count":2,"perPage":25,` +
	`"nextPageUrl":"/api/v2/reports?page=2","previousPageUrl":null,"results":[` +
	`{"reportedAt":"2026-07-10T00:00:00+00:00","comment":"SSH bruteforce","categories":[18],"reporterId":1,"reporterCountryCode":"US","reporterCountryName":"United States"},` +
	`{"reportedAt":"2026-07-09T00:00:00+00:00","comment":"Port scan","categories":[14],"reporterId":2,"reporterCountryCode":"NL","reporterCountryName":"Netherlands"}]}}`

func TestCmdReportsHumanAndJSON(t *testing.T) {
	clearEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(reportsBody))
	}))
	t.Cleanup(srv.Close)
	cfg := writeConfig(t, srv.URL, "k")

	var out, errw bytes.Buffer
	if code := cmdReports([]string{"--config", cfg, "1.2.3.4"}, &out, &errw, strings.NewReader("")); code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errw.String())
	}
	if !strings.Contains(out.String(), "42 reports total") || !strings.Contains(out.String(), "SSH bruteforce") {
		t.Errorf("human reports:\n%s", out.String())
	}

	var jout, jerr bytes.Buffer
	if code := cmdReports([]string{"--config", cfg, "-j", "1.2.3.4"}, &jout, &jerr, strings.NewReader("")); code != 0 {
		t.Fatalf("json exit = %d", code)
	}
	lines := strings.Split(strings.TrimSpace(jout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSONL lines, got %d:\n%s", len(lines), jout.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("JSONL parse: %v", err)
	}
	if m["ipAddress"] != "1.2.3.4" || m["comment"] != "SSH bruteforce" {
		t.Errorf("report line = %v", m)
	}
}

func TestCmdMCPHandshake(t *testing.T) {
	clearEnv(t)
	cfg := writeConfig(t, "https://example.test", "k")
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	var out, errw bytes.Buffer
	if code := cmdMCP([]string{"--config", cfg}, "test-ver", in, &out, &errw); code != 0 {
		t.Fatalf("mcp exit = %d, stderr=%s", code, errw.String())
	}
	if !strings.Contains(out.String(), `"name":"abuse-lookup"`) {
		t.Errorf("initialize response missing serverInfo:\n%s", out.String())
	}
}

func TestReadInputs(t *testing.T) {
	got := readInputs(nil, strings.NewReader("1.1.1.1 2.2.2.2\n# skip\n\n3.3.3.3\n"))
	want := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("readInputs = %v, want %v", got, want)
	}
	// Args take precedence over stdin.
	got = readInputs([]string{"9.9.9.9"}, strings.NewReader("1.1.1.1"))
	if len(got) != 1 || got[0] != "9.9.9.9" {
		t.Errorf("args should win: %v", got)
	}
}
