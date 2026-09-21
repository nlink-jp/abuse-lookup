package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nlink-jp/abuse-lookup/internal/abuseipdb"
)

// TestUnknownArgumentIsRefusedByName is the enforcing half of org ADR-021 §4:
// additionalProperties:false only tells a client what is allowed, and a client
// that does not check the schema sends the typo anyway. Every tool must refuse
// it, and the message must name the offending field — a caller that is told
// only "invalid arguments" has to re-read the schema to find its own typo.
//
// The misspellings below are the ones that would otherwise be dangerous: drop
// `refresh` and a cached answer reads as a fresh one; drop `per_page` and a
// page sized for someone else's context comes back.
func TestUnknownArgumentIsRefusedByName(t *testing.T) {
	cases := []struct {
		tool  string
		args  string
		field string
	}{
		{"check_ip", `{"ip":"8.8.8.8","refresh_":true}`, "refresh_"},
		{"check_ip", `{"ip":"8.8.8.8","maxage":30}`, "maxage"},
		{"get_reports", `{"ip":"8.8.8.8","perpage":5}`, "perpage"},
		{"cache_status", `{"verbose":true}`, "verbose"},
		{"get_usage", `{"topic":"caching"}`, "topic"},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.field, func(t *testing.T) {
			mc := &mockClient{
				check:   &abuseipdb.CheckResult{AbuseConfidenceScore: 100},
				reports: &abuseipdb.ReportsPage{Total: 1, Page: 1, Count: 1, PerPage: 25, Results: makeReports(1)},
			}
			e := newEngine(t, mc)
			req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tc.tool, tc.args)
			text, isErr := callText(t, drive(t, e, req)[0].Result)
			if !isErr {
				t.Fatalf("%s accepted unknown argument %q: %s", tc.tool, tc.field, text)
			}
			// Matching the decoder's own phrasing, not just the field name:
			// "provide 'hash…'" happens to contain "hash", so a bare substring
			// test passes for the wrong reason. The mutation check caught it.
			want := `unknown field "` + tc.field + `"`
			if !strings.Contains(text, want) {
				t.Errorf("%s: error does not name the offending argument: want %s, got %s", tc.tool, want, text)
			}
			// The call must not have run: an argument this server does not
			// understand means the caller asked for something else.
			if mc.checks != 0 {
				t.Errorf("%s: the API was called despite the rejected argument", tc.tool)
			}
		})
	}
}

// TestMalformedArgumentsAreRefused covers the other half of the discarded
// error: `_ = json.Unmarshal` left `a` at its zero value when the object did
// not decode, so a wrong-typed argument produced the same call as an absent
// one — and "provide 'ip'" is a misleading answer to a request that did
// provide it.
func TestMalformedArgumentsAreRefused(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
	}{
		{"wrong type", "check_ip", `{"ip":123}`},
		{"array for object", "check_ip", `["8.8.8.8"]`},
		{"string for object", "check_ip", `"8.8.8.8"`},
		{"wrong type", "get_reports", `{"ip":"8.8.8.8","page":"first"}`},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.name, func(t *testing.T) {
			mc := &mockClient{check: &abuseipdb.CheckResult{}}
			e := newEngine(t, mc)
			req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tc.tool, tc.args)
			text, isErr := callText(t, drive(t, e, req)[0].Result)
			if !isErr {
				t.Fatalf("%s accepted malformed arguments: %s", tc.tool, text)
			}
			if strings.Contains(text, "provide 'ip'") {
				t.Errorf("%s reported the argument as missing instead of malformed: %s", tc.tool, text)
			}
			if !strings.Contains(text, "arguments:") {
				t.Errorf("%s: error is not a decode error: %s", tc.tool, text)
			}
		})
	}
}

// TestOmittedArgumentsStillMeanNone pins the boundary of the change: strict
// decoding must not turn a legitimately argument-less call into an error.
func TestOmittedArgumentsStillMeanNone(t *testing.T) {
	for _, args := range []string{``, `,"arguments":{}`, `,"arguments":null`} {
		e := newEngine(t, &mockClient{check: &abuseipdb.CheckResult{}})
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cache_status"%s}}`, args)
		text, isErr := callText(t, drive(t, e, req)[0].Result)
		if isErr {
			t.Errorf("cache_status with arguments %q was refused: %s", args, text)
		}
	}
}
