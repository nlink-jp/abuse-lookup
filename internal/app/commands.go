package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/nlink-jp/abuse-lookup/internal/abuseipdb"
	"github.com/nlink-jp/abuse-lookup/internal/engine"
	"github.com/nlink-jp/abuse-lookup/internal/mcp"
)

// rateLimitWarnThreshold: warn when fewer than this many daily checks remain.
const rateLimitWarnThreshold = 50

// cmdCheck looks up the reputation of one or more IPs.
func cmdCheck(args []string, out, errw io.Writer, stdin io.Reader) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(errw)
	var (
		common  commonFlags
		maxAge  int
		verbose bool
		refresh bool
		asJSON  bool
	)
	common.register(fs)
	fs.IntVar(&maxAge, "max-age", 90, "report look-back window in days")
	fs.BoolVar(&verbose, "verbose", false, "include recent report details")
	fs.BoolVar(&refresh, "refresh", false, "ignore the cache and re-fetch")
	fs.BoolVar(&asJSON, "json", false, "JSON Lines output")
	fs.BoolVar(&asJSON, "j", false, "JSON Lines output (shorthand)")

	positionals, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if maxAge < 1 {
		fmt.Fprintln(errw, "error: --max-age must be at least 1")
		return 2
	}

	ips := readInputs(positionals, stdin)
	if len(ips) == 0 {
		fmt.Fprintln(errw, "error: no IP addresses given (pass IPs as arguments or via stdin)")
		return 2
	}

	eng, err := common.buildEngine()
	if err != nil {
		fmt.Fprintf(errw, "error: %v\n", err)
		return 1
	}
	if !eng.HasKey() {
		fmt.Fprintln(errw, "error: no AbuseIPDB API key configured")
		fmt.Fprintln(errw, "set ABUSEIPDB_API_KEY or [abuseipdb] key in the config file; run 'abuse-lookup doctor'.")
		return 1
	}

	ctx := context.Background()
	exit := 0
	var lastLive *abuseipdb.RateLimit
	for _, ip := range ips {
		outcome, err := eng.Check(ctx, ip, maxAge, verbose, refresh)
		if err != nil {
			if errors.Is(err, abuseipdb.ErrRateLimited) {
				fmt.Fprintf(errw, "error: AbuseIPDB daily rate limit exceeded; try again after the daily reset (%s)\n", resetHint(outcome))
				return 1
			}
			if asJSON {
				writeJSONLError(out, ip, err.Error())
			} else {
				fmt.Fprintf(errw, "error: %s: %v\n", ip, err)
			}
			exit = 1
			continue
		}
		if outcome.RateLimit != nil {
			lastLive = outcome.RateLimit
		}
		if asJSON {
			if err := writeJSONLCheck(out, outcome); err != nil {
				fmt.Fprintf(errw, "error: %s: %v\n", ip, err)
				exit = 1
			}
		} else {
			writeHumanCheck(out, outcome)
		}
	}
	warnRateLimit(errw, lastLive)
	return exit
}

// cmdReports fetches one page of the abuse reports for one or more IPs.
func cmdReports(args []string, out, errw io.Writer, stdin io.Reader) int {
	fs := flag.NewFlagSet("reports", flag.ContinueOnError)
	fs.SetOutput(errw)
	var (
		common  commonFlags
		maxAge  int
		page    int
		perPage int
		asJSON  bool
	)
	common.register(fs)
	fs.IntVar(&maxAge, "max-age", 90, "report look-back window in days")
	fs.IntVar(&page, "page", 1, "1-based page number")
	fs.IntVar(&perPage, "per-page", 25, "reports per page")
	fs.BoolVar(&asJSON, "json", false, "JSON Lines output")
	fs.BoolVar(&asJSON, "j", false, "JSON Lines output (shorthand)")

	positionals, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if maxAge < 1 || page < 1 || perPage < 1 {
		fmt.Fprintln(errw, "error: --max-age, --page, and --per-page must be at least 1")
		return 2
	}
	ips := readInputs(positionals, stdin)
	if len(ips) == 0 {
		fmt.Fprintln(errw, "error: no IP addresses given (pass IPs as arguments or via stdin)")
		return 2
	}

	eng, err := common.buildEngine()
	if err != nil {
		fmt.Fprintf(errw, "error: %v\n", err)
		return 1
	}
	if !eng.HasKey() {
		fmt.Fprintln(errw, "error: no AbuseIPDB API key configured")
		fmt.Fprintln(errw, "set ABUSEIPDB_API_KEY or [abuseipdb] key in the config file; run 'abuse-lookup doctor'.")
		return 1
	}

	ctx := context.Background()
	exit := 0
	var lastLive *abuseipdb.RateLimit
	for _, ip := range ips {
		outcome, err := eng.Reports(ctx, ip, maxAge, page, perPage)
		if err != nil {
			if errors.Is(err, abuseipdb.ErrRateLimited) {
				fmt.Fprintf(errw, "error: AbuseIPDB daily rate limit exceeded; try again after the daily reset (%s)\n", resetHintReports(outcome))
				return 1
			}
			if asJSON {
				writeJSONLError(out, ip, err.Error())
			} else {
				fmt.Fprintf(errw, "error: %s: %v\n", ip, err)
			}
			exit = 1
			continue
		}
		if outcome.RateLimit != nil {
			lastLive = outcome.RateLimit
		}
		if asJSON {
			writeJSONLReports(out, ip, outcome.Page)
		} else {
			writeHumanReports(out, ip, outcome.Page)
		}
	}
	warnRateLimit(errw, lastLive)
	return exit
}

// cmdMCP runs the local stdio MCP server. It starts even without an API key so
// get_usage / cache_status remain usable; API-backed tools report a clear error.
func cmdMCP(args []string, version string, in io.Reader, out, errw io.Writer) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(errw)
	var common commonFlags
	common.register(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	eng, err := common.buildEngine()
	if err != nil {
		fmt.Fprintf(errw, "error: %v\n", err)
		return 1
	}
	if err := mcp.Serve(context.Background(), eng, version, in, out); err != nil {
		fmt.Fprintf(errw, "mcp: %v\n", err)
		return 1
	}
	return 0
}

// cmdDoctor reports configuration and cache health without spending API quota.
func cmdDoctor(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(errw)
	var common commonFlags
	common.register(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	eng, err := common.buildEngine()
	if err != nil {
		fmt.Fprintf(errw, "error: %v\n", err)
		return 1
	}
	cfg := eng.Config()

	fmt.Fprintln(out, "abuse-lookup doctor")
	ok := true
	if eng.HasKey() {
		fmt.Fprintln(out, "  API key:     configured")
	} else {
		fmt.Fprintln(out, "  API key:     NOT configured")
		fmt.Fprintln(out, "               set ABUSEIPDB_API_KEY or [abuseipdb] key in the config file")
		ok = false
	}
	fmt.Fprintf(out, "  API base:    %s\n", cfg.BaseURL)
	fmt.Fprintf(out, "  Cache dir:   %s\n", cfg.CacheDir)
	fmt.Fprintf(out, "  Cache TTL:   %s\n", cfg.CacheTTL)

	c := eng.Cache()
	recs, err := c.List()
	if err != nil {
		fmt.Fprintf(out, "  Cache:       unreadable: %v\n", err)
		ok = false
	} else {
		fresh := 0
		for _, r := range recs {
			if c.Fresh(r) {
				fresh++
			}
		}
		fmt.Fprintf(out, "  Cache:       %d entries (%d fresh)\n", len(recs), fresh)
	}

	if ok {
		fmt.Fprintln(out, "  Status:      ok")
		return 0
	}
	fmt.Fprintln(out, "  Status:      needs attention")
	return 1
}

// cmdCache shows or clears the local cache.
func cmdCache(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("cache", flag.ContinueOnError)
	fs.SetOutput(errw)
	var (
		common commonFlags
		clear  bool
	)
	common.register(fs)
	fs.BoolVar(&clear, "clear", false, "remove all cached entries")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	eng, err := common.buildEngine()
	if err != nil {
		fmt.Fprintf(errw, "error: %v\n", err)
		return 1
	}
	c := eng.Cache()

	if clear {
		n, err := c.Clear()
		if err != nil {
			fmt.Fprintf(errw, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "cleared %d cache entr%s\n", n, plural(n))
		return 0
	}

	recs, err := c.List()
	if err != nil {
		fmt.Fprintf(errw, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Cache dir: %s\n", c.Dir())
	fmt.Fprintf(out, "TTL:       %s\n", c.TTL())
	fmt.Fprintf(out, "Entries:   %d\n", len(recs))
	if len(recs) > 0 {
		fmt.Fprintln(out)
		for _, r := range recs {
			score := "?"
			var res abuseipdb.CheckResult
			if json.Unmarshal(r.Payload, &res) == nil {
				score = fmt.Sprintf("%d", res.AbuseConfidenceScore)
			}
			state := "fresh"
			if !c.Fresh(r) {
				state = "stale"
			}
			fmt.Fprintf(out, "  %-39s  score=%-3s  %s  (%s)\n",
				r.IP, score, r.FetchedAt.UTC().Format(time.RFC3339), state)
		}
	}
	return 0
}

// warnRateLimit prints a stderr warning when the remaining daily quota is low.
func warnRateLimit(errw io.Writer, rl *abuseipdb.RateLimit) {
	if rl == nil || rl.Remaining < 0 {
		return
	}
	if rl.Remaining < rateLimitWarnThreshold {
		limit := ""
		if rl.Limit > 0 {
			limit = fmt.Sprintf(" of %d", rl.Limit)
		}
		fmt.Fprintf(errw, "warning: AbuseIPDB rate limit low: %d%s checks remaining today\n", rl.Remaining, limit)
	}
}

// resetHint renders the reset time from a rate-limited outcome, when known.
func resetHint(o *engine.Outcome) string {
	if o == nil || o.RateLimit == nil || o.RateLimit.Reset < 0 {
		return "daily reset"
	}
	return fmt.Sprintf("%ds", o.RateLimit.Reset)
}

// resetHintReports renders the reset time from a rate-limited reports outcome.
func resetHintReports(o *engine.ReportsOutcome) string {
	if o == nil || o.RateLimit == nil || o.RateLimit.Reset < 0 {
		return "daily reset"
	}
	return fmt.Sprintf("%ds", o.RateLimit.Reset)
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
