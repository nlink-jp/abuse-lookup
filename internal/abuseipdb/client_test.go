package abuseipdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const okBody = `{"data":{"ipAddress":"118.25.6.39","isPublic":true,"ipVersion":4,` +
	`"isWhitelisted":false,"abuseConfidenceScore":100,"countryCode":"CN",` +
	`"usageType":"Data Center/Web Hosting/Transit","isp":"Tencent","domain":"tencent.com",` +
	`"hostnames":[],"isTor":false,"totalReports":5,"numDistinctUsers":3,` +
	`"lastReportedAt":"2020-01-02T03:04:05+00:00"}}`

func TestCheckSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/check" {
			t.Errorf("path = %q, want /check", r.URL.Path)
		}
		if got := r.URL.Query().Get("ipAddress"); got != "118.25.6.39" {
			t.Errorf("ipAddress = %q", got)
		}
		if got := r.URL.Query().Get("maxAgeInDays"); got != "90" {
			t.Errorf("maxAgeInDays = %q", got)
		}
		if r.URL.Query().Has("verbose") {
			t.Error("verbose should be absent")
		}
		if got := r.Header.Get("Key"); got != "secret" {
			t.Errorf("Key header = %q, want secret", got)
		}
		w.Header().Set("X-RateLimit-Limit", "1000")
		w.Header().Set("X-RateLimit-Remaining", "997")
		w.Write([]byte(okBody))
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "secret")
	res, rl, err := c.Check(context.Background(), "118.25.6.39", 90, false)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.AbuseConfidenceScore != 100 || res.CountryCode != "CN" {
		t.Errorf("unexpected result: %+v", res)
	}
	if res.TotalReports != 5 || res.NumDistinctUsers != 3 {
		t.Errorf("report counts: %+v", res)
	}
	if rl.Limit != 1000 || rl.Remaining != 997 {
		t.Errorf("rate limit = %+v", rl)
	}
}

func TestCheckVerbose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.Query().Has("verbose") {
			t.Error("verbose flag missing")
		}
		w.Write([]byte(okBody))
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "secret")
	if _, _, err := c.Check(context.Background(), "118.25.6.39", 30, true); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"errors":[{"detail":"Daily rate limit of 1000 requests exceeded","status":429}]}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "secret")
	_, rl, err := c.Check(context.Background(), "1.2.3.4", 90, false)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if rl == nil || rl.Reset != 3600 {
		t.Errorf("rate limit reset = %+v, want 3600", rl)
	}
}

func TestCheckAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"errors":[{"detail":"The ip address must be a valid IP address.","status":422}]}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "secret")
	_, _, err := c.Check(context.Background(), "1.2.3.4", 90, false)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != 422 {
		t.Errorf("StatusCode = %d, want 422", apiErr.StatusCode)
	}
	if apiErr.Detail == "" {
		t.Error("Detail should carry the API message")
	}
}

func TestCheckNoKey(t *testing.T) {
	c := NewHTTPClient("https://example.test", "")
	if _, _, err := c.Check(context.Background(), "1.2.3.4", 90, false); err == nil {
		t.Fatal("Check: expected error with no API key")
	}
}

func TestReports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reports" {
			t.Errorf("path = %q, want /reports", r.URL.Path)
		}
		if got := r.URL.Query().Get("page"); got != "2" {
			t.Errorf("page = %q, want 2", got)
		}
		if got := r.URL.Query().Get("perPage"); got != "10" {
			t.Errorf("perPage = %q, want 10", got)
		}
		w.Write([]byte(`{"data":{"total":42,"page":2,"count":1,"perPage":10,` +
			`"nextPageUrl":"/api/v2/reports?page=3","previousPageUrl":"/api/v2/reports?page=1",` +
			`"results":[{"reportedAt":"2020-01-02T03:04:05+00:00","comment":"SSH bruteforce",` +
			`"categories":[18,22],"reporterId":7,"reporterCountryCode":"US","reporterCountryName":"United States"}]}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "secret")
	page, _, err := c.Reports(context.Background(), "1.2.3.4", 90, 2, 10)
	if err != nil {
		t.Fatalf("Reports: %v", err)
	}
	if page.Total != 42 || page.Page != 2 || page.Count != 1 {
		t.Errorf("page meta = %+v", page)
	}
	if page.NextPageURL == "" || len(page.Results) != 1 {
		t.Errorf("page body = %+v", page)
	}
	rep := page.Results[0]
	if rep.Comment != "SSH bruteforce" || len(rep.Categories) != 2 || rep.ReporterCountryCode != "US" {
		t.Errorf("report = %+v", rep)
	}
}

func TestReportsRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"errors":[{"detail":"rate limit","status":429}]}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "secret")
	if _, _, err := c.Reports(context.Background(), "1.2.3.4", 90, 1, 25); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}
