// Package engine ties configuration, the AbuseIPDB client, and the local cache
// into the single reputation-lookup flow shared by the CLI and (Phase 2) the
// MCP server, so their behaviour cannot diverge.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/nlink-jp/abuse-lookup/internal/abuseipdb"
	"github.com/nlink-jp/abuse-lookup/internal/cache"
	"github.com/nlink-jp/abuse-lookup/internal/config"
)

// Engine performs cached reputation lookups.
type Engine struct {
	cfg    *config.Config
	client abuseipdb.Client
	cache  *cache.Cache
}

// New builds an Engine from resolved config and an injected client.
func New(cfg *config.Config, client abuseipdb.Client) *Engine {
	return &Engine{
		cfg:    cfg,
		client: client,
		cache:  cache.New(cfg.CacheDir, cfg.CacheTTL),
	}
}

// Outcome is the result of a single Check plus its provenance.
type Outcome struct {
	Result    *abuseipdb.CheckResult
	FromCache bool
	FetchedAt time.Time
	RateLimit *abuseipdb.RateLimit // set only on a live fetch
}

// Check returns the reputation of ip. Unless refresh is set, a fresh cached
// result is returned without contacting the API; otherwise the API is queried
// and the result is cached. The IP is validated before any lookup.
func (e *Engine) Check(ctx context.Context, ip string, maxAgeDays int, verbose, refresh bool) (*Outcome, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil, fmt.Errorf("invalid IP address %q", ip)
	}
	canonical := addr.String()

	if !refresh {
		rec, ok, err := e.cache.Get(canonical, maxAgeDays, verbose)
		if err != nil {
			return nil, err
		}
		if ok {
			var res abuseipdb.CheckResult
			if err := json.Unmarshal(rec.Payload, &res); err == nil {
				return &Outcome{Result: &res, FromCache: true, FetchedAt: rec.FetchedAt}, nil
			}
			// Corrupt payload: fall through to a live fetch.
		}
	}

	res, rl, err := e.client.Check(ctx, canonical, maxAgeDays, verbose)
	if err != nil {
		return &Outcome{RateLimit: rl}, err
	}
	if payload, merr := json.Marshal(res); merr == nil {
		// A cache write failure must not fail the lookup itself.
		_ = e.cache.Put(canonical, maxAgeDays, verbose, payload)
	}
	return &Outcome{Result: res, FromCache: false, FetchedAt: time.Now(), RateLimit: rl}, nil
}

// ReportsOutcome is the result of a Reports lookup.
type ReportsOutcome struct {
	Page      *abuseipdb.ReportsPage
	RateLimit *abuseipdb.RateLimit
}

// Reports fetches one page of an IP's report history. Unlike Check, reports are
// not cached: they are a secondary, paginated detail fetch, and pagination is
// caller-driven (page / perPage).
func (e *Engine) Reports(ctx context.Context, ip string, maxAgeDays, page, perPage int) (*ReportsOutcome, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil, fmt.Errorf("invalid IP address %q", ip)
	}
	pageData, rl, err := e.client.Reports(ctx, addr.String(), maxAgeDays, page, perPage)
	if err != nil {
		return &ReportsOutcome{RateLimit: rl}, err
	}
	return &ReportsOutcome{Page: pageData, RateLimit: rl}, nil
}

// Config returns the resolved configuration.
func (e *Engine) Config() *config.Config { return e.cfg }

// Cache returns the underlying cache (for the cache subcommand).
func (e *Engine) Cache() *cache.Cache { return e.cache }

// HasKey reports whether an API key is configured.
func (e *Engine) HasKey() bool { return e.cfg.APIKey != "" }
