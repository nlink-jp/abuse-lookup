// Package abuseipdb queries the AbuseIPDB API v2 for IP reputation.
//
// The API key authenticates to the user's own AbuseIPDB account. It is sent on
// every request via the "Key" header and is never placed in a URL, logged, or
// surfaced in an error — the request URL carries only the IP address and query
// parameters. The Client is an interface so the engine can be tested without
// touching the network.
package abuseipdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// CheckResult mirrors the "data" object of a /check response.
type CheckResult struct {
	IPAddress            string   `json:"ipAddress"`
	IsPublic             bool     `json:"isPublic"`
	IPVersion            int      `json:"ipVersion"`
	IsWhitelisted        bool     `json:"isWhitelisted"`
	AbuseConfidenceScore int      `json:"abuseConfidenceScore"`
	CountryCode          string   `json:"countryCode"`
	CountryName          string   `json:"countryName,omitempty"`
	UsageType            string   `json:"usageType"`
	ISP                  string   `json:"isp"`
	Domain               string   `json:"domain"`
	Hostnames            []string `json:"hostnames"`
	IsTor                bool     `json:"isTor"`
	TotalReports         int      `json:"totalReports"`
	NumDistinctUsers     int      `json:"numDistinctUsers"`
	LastReportedAt       string   `json:"lastReportedAt"`
	Reports              []Report `json:"reports,omitempty"` // populated only when verbose
}

// Report is one abuse report, returned only for verbose checks.
type Report struct {
	ReportedAt          string `json:"reportedAt"`
	Comment             string `json:"comment"`
	Categories          []int  `json:"categories"`
	ReporterID          int    `json:"reporterId"`
	ReporterCountryCode string `json:"reporterCountryCode"`
	ReporterCountryName string `json:"reporterCountryName"`
}

// RateLimit captures the per-response rate-limit headers. Fields are -1 when the
// corresponding header is absent or unparseable.
type RateLimit struct {
	Limit     int // X-RateLimit-Limit
	Remaining int // X-RateLimit-Remaining
	Reset     int // X-RateLimit-Reset (unix seconds), or Retry-After on 429
}

// ReportsPage is one page of the /reports response.
type ReportsPage struct {
	Total           int      `json:"total"`
	Page            int      `json:"page"`
	Count           int      `json:"count"`
	PerPage         int      `json:"perPage"`
	NextPageURL     string   `json:"nextPageUrl"`
	PreviousPageURL string   `json:"previousPageUrl"`
	Results         []Report `json:"results"`
}

// Client queries AbuseIPDB. Implementations must be safe for sequential use and
// honor ctx cancellation.
type Client interface {
	Check(ctx context.Context, ip string, maxAgeDays int, verbose bool) (*CheckResult, *RateLimit, error)
	Reports(ctx context.Context, ip string, maxAgeDays, page, perPage int) (*ReportsPage, *RateLimit, error)
}

// ErrRateLimited wraps a 429 response so callers can stop a batch early: once
// the daily quota is exhausted, further calls will fail the same way until the
// quota resets.
var ErrRateLimited = errors.New("abuseipdb: rate limit exceeded")

// APIError is a non-2xx response with the API-reported detail. The key is never
// included.
type APIError struct {
	StatusCode int
	Detail     string
}

func (e *APIError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("abuseipdb: HTTP %d: %s", e.StatusCode, e.Detail)
	}
	return fmt.Sprintf("abuseipdb: HTTP %d", e.StatusCode)
}

// HTTPClient is the production Client.
type HTTPClient struct {
	Client  *http.Client
	BaseURL string
	Key     string
}

// NewHTTPClient returns a Client with a sane default timeout.
func NewHTTPClient(baseURL, key string) *HTTPClient {
	return &HTTPClient{
		Client:  &http.Client{Timeout: 30 * time.Second},
		BaseURL: baseURL,
		Key:     key,
	}
}

// Check performs an authenticated GET /check. On a 429 it returns a wrapped
// ErrRateLimited; on other non-200 responses it returns an *APIError. The
// rate-limit headers are returned whenever present, even alongside an error.
func (c *HTTPClient) Check(ctx context.Context, ip string, maxAgeDays int, verbose bool) (*CheckResult, *RateLimit, error) {
	q := url.Values{}
	q.Set("ipAddress", ip)
	q.Set("maxAgeInDays", strconv.Itoa(maxAgeDays))
	if verbose {
		q.Set("verbose", "")
	}
	body, rl, err := c.do(ctx, "/check", q)
	if err != nil {
		return nil, rl, err
	}
	var payload struct {
		Data CheckResult `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, rl, fmt.Errorf("abuseipdb: decode response: %w", err)
	}
	return &payload.Data, rl, nil
}

// Reports fetches one page of the /reports history for an IP. page and perPage
// are omitted from the request when non-positive (the API applies its defaults).
// Error handling mirrors Check.
func (c *HTTPClient) Reports(ctx context.Context, ip string, maxAgeDays, page, perPage int) (*ReportsPage, *RateLimit, error) {
	q := url.Values{}
	q.Set("ipAddress", ip)
	q.Set("maxAgeInDays", strconv.Itoa(maxAgeDays))
	if page > 0 {
		q.Set("page", strconv.Itoa(page))
	}
	if perPage > 0 {
		q.Set("perPage", strconv.Itoa(perPage))
	}
	body, rl, err := c.do(ctx, "/reports", q)
	if err != nil {
		return nil, rl, err
	}
	var payload struct {
		Data ReportsPage `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, rl, fmt.Errorf("abuseipdb: decode reports: %w", err)
	}
	return &payload.Data, rl, nil
}

// do performs an authenticated GET against BaseURL+path with the given query,
// returning the response body and rate-limit headers. A 429 becomes a wrapped
// ErrRateLimited; any other non-200 becomes an *APIError. The API key is sent
// via the Key header and never appears in the URL.
func (c *HTTPClient) do(ctx context.Context, path string, q url.Values) ([]byte, *RateLimit, error) {
	if c.Key == "" {
		return nil, nil, errors.New("abuseipdb: no API key configured")
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	endpoint, err := url.Parse(c.BaseURL + path)
	if err != nil {
		return nil, nil, fmt.Errorf("abuseipdb: invalid base URL %q: %w", c.BaseURL, err)
	}
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Key", c.Key)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("abuseipdb: request failed: %w", err)
	}
	defer resp.Body.Close()

	rl := parseRateLimit(resp.Header)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, rl, fmt.Errorf("%w: %s", ErrRateLimited, firstErrorDetail(body))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, rl, &APIError{StatusCode: resp.StatusCode, Detail: firstErrorDetail(body)}
	}
	return body, rl, nil
}

// parseRateLimit extracts the rate-limit headers; missing values become -1.
func parseRateLimit(h http.Header) *RateLimit {
	rl := &RateLimit{Limit: -1, Remaining: -1, Reset: -1}
	if v := h.Get("X-RateLimit-Limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			rl.Limit = n
		}
	}
	if v := h.Get("X-RateLimit-Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			rl.Remaining = n
		}
	}
	if v := h.Get("X-RateLimit-Reset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			rl.Reset = n
		}
	} else if v := h.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			rl.Reset = n
		}
	}
	return rl
}

// firstErrorDetail pulls the first {"errors":[{"detail":...}]} message from an
// AbuseIPDB error body, falling back to a trimmed raw body.
func firstErrorDetail(body []byte) string {
	var e struct {
		Errors []struct {
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &e); err == nil && len(e.Errors) > 0 && e.Errors[0].Detail != "" {
		return e.Errors[0].Detail
	}
	s := string(body)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
