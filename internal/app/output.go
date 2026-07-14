package app

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/nlink-jp/abuse-lookup/internal/abuseipdb"
	"github.com/nlink-jp/abuse-lookup/internal/engine"
)

// checkJSON flattens a CheckResult with lookup provenance for JSONL output. The
// embedded pointer's fields are promoted inline by encoding/json.
type checkJSON struct {
	*abuseipdb.CheckResult
	Cached    bool      `json:"cached"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// writeJSONLCheck emits one JSON object line for a successful lookup.
func writeJSONLCheck(w io.Writer, o *engine.Outcome) error {
	b, err := json.Marshal(checkJSON{
		CheckResult: o.Result,
		Cached:      o.FromCache,
		FetchedAt:   o.FetchedAt,
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// writeJSONLError emits one JSON object line describing a per-IP failure.
func writeJSONLError(w io.Writer, ip, msg string) {
	b, _ := json.Marshal(struct {
		IPAddress string `json:"ipAddress"`
		Error     string `json:"error"`
	}{ip, msg})
	fmt.Fprintln(w, string(b))
}

// writeHumanCheck prints a labeled block for a single lookup.
func writeHumanCheck(w io.Writer, o *engine.Outcome) {
	r := o.Result
	fmt.Fprintln(w, r.IPAddress)
	fmt.Fprintf(w, "  Abuse score:     %d/100 (%s)\n", r.AbuseConfidenceScore, riskLabel(r.AbuseConfidenceScore))
	if r.CountryCode != "" {
		fmt.Fprintf(w, "  Country:         %s\n", r.CountryCode)
	}
	if r.UsageType != "" {
		fmt.Fprintf(w, "  Usage type:      %s\n", r.UsageType)
	}
	if r.ISP != "" {
		fmt.Fprintf(w, "  ISP:             %s\n", r.ISP)
	}
	if r.Domain != "" {
		fmt.Fprintf(w, "  Domain:          %s\n", r.Domain)
	}
	fmt.Fprintf(w, "  Total reports:   %d (from %d distinct reporters)\n", r.TotalReports, r.NumDistinctUsers)
	if r.LastReportedAt != "" {
		fmt.Fprintf(w, "  Last reported:   %s\n", r.LastReportedAt)
	}
	if r.IsWhitelisted {
		fmt.Fprintln(w, "  Whitelisted:     true")
	}
	if r.IsTor {
		fmt.Fprintln(w, "  Tor exit node:   true")
	}
	if len(r.Reports) > 0 {
		fmt.Fprintln(w, "  Recent reports:")
		for i, rep := range r.Reports {
			if i >= 5 {
				fmt.Fprintf(w, "    … and %d more\n", len(r.Reports)-5)
				break
			}
			fmt.Fprintf(w, "    %s [%s] %s\n", shortDate(rep.ReportedAt), rep.ReporterCountryCode, truncate(rep.Comment, 70))
		}
	}
	if o.FromCache {
		fmt.Fprintf(w, "  [cached %s]\n", o.FetchedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintln(w)
}

// reportJSON flattens one report with the IP it belongs to for JSONL output.
type reportJSON struct {
	IPAddress string `json:"ipAddress"`
	abuseipdb.Report
}

// writeJSONLReports emits one JSON object line per report.
func writeJSONLReports(w io.Writer, ip string, page *abuseipdb.ReportsPage) {
	if page == nil {
		return
	}
	for _, rep := range page.Results {
		b, err := json.Marshal(reportJSON{IPAddress: ip, Report: rep})
		if err != nil {
			continue
		}
		fmt.Fprintln(w, string(b))
	}
}

// writeHumanReports prints a header plus one line per report.
func writeHumanReports(w io.Writer, ip string, page *abuseipdb.ReportsPage) {
	if page == nil {
		return
	}
	fmt.Fprintf(w, "%s — %d reports total (page %d, showing %d)\n",
		ip, page.Total, page.Page, page.Count)
	for _, rep := range page.Results {
		fmt.Fprintf(w, "  %s [%s] %s\n", shortDate(rep.ReportedAt), rep.ReporterCountryCode, truncate(rep.Comment, 80))
	}
	if page.NextPageURL != "" {
		fmt.Fprintf(w, "  (more: --page %d)\n", page.Page+1)
	}
	fmt.Fprintln(w)
}

// riskLabel gives a short severity word for an abuse confidence score.
func riskLabel(score int) string {
	switch {
	case score >= 75:
		return "high risk"
	case score >= 25:
		return "elevated"
	case score > 0:
		return "low"
	default:
		return "clean"
	}
}

func shortDate(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
