// Package app implements the abuse-lookup command-line interface: subcommand
// dispatch plus the check / doctor / cache commands. Core logic lives in the
// abuseipdb, cache, config, and engine packages; this package is the thin I/O
// shell around them. (The mcp subcommand arrives in Phase 2.)
package app

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nlink-jp/abuse-lookup/internal/abuseipdb"
	"github.com/nlink-jp/abuse-lookup/internal/config"
	"github.com/nlink-jp/abuse-lookup/internal/engine"
)

// Run dispatches a subcommand and returns a process exit code.
func Run(args []string, version string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "check":
		return cmdCheck(rest, os.Stdout, os.Stderr, os.Stdin)
	case "reports":
		return cmdReports(rest, os.Stdout, os.Stderr, os.Stdin)
	case "doctor":
		return cmdDoctor(rest, os.Stdout, os.Stderr)
	case "cache":
		return cmdCache(rest, os.Stdout, os.Stderr)
	case "mcp":
		return cmdMCP(rest, version, os.Stdin, os.Stdout, os.Stderr)
	case "version", "--version", "-v":
		fmt.Println("abuse-lookup " + version)
		fmt.Println("Data: AbuseIPDB (https://www.abuseipdb.com).")
		return 0
	case "help", "-h", "--help":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage(os.Stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `abuse-lookup — check IP address reputation against the AbuseIPDB API

Usage:
  abuse-lookup <command> [flags] [args]

Commands:
  check <IP>...   Look up the reputation of each IP (stdin if no args)
  reports <IP>... Fetch the individual abuse reports for each IP
  doctor          Check API-key configuration and cache state
  cache           Show cache status; --clear to discard
  mcp             Run as a local MCP server (stdio)
  version         Print the version

check flags:
  --max-age <days>   Report look-back window (default 90)
  --verbose          Include recent report details
  --refresh          Ignore the cache and re-fetch
  -j, --json         JSON Lines output

reports flags:
  --max-age <days>   Report look-back window (default 90)
  --page <n>         1-based page number (default 1)
  --per-page <n>     Reports per page (default 25)
  -j, --json         JSON Lines output (one report per line)

Common flags:
  -c, --config <path>   Config file (default ~/.config/abuse-lookup/config.toml)

Configuration:
  API key via ABUSEIPDB_API_KEY env var or [abuseipdb] key in the config file.

Data: AbuseIPDB (https://www.abuseipdb.com). Attribution required.
`)
}

// commonFlags are the config-resolution flags shared by every command. The API
// key is intentionally not a flag — it must not land in shell history or the
// process list; use the environment variable or the config file instead.
type commonFlags struct {
	config string
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.config, "config", "", "config file path")
	fs.StringVar(&c.config, "c", "", "config file path (shorthand)")
}

func (c *commonFlags) buildEngine() (*engine.Engine, error) {
	cfg, err := config.Load(c.config, "")
	if err != nil {
		return nil, err
	}
	client := abuseipdb.NewHTTPClient(cfg.BaseURL, cfg.APIKey)
	return engine.New(cfg, client), nil
}

// parseInterspersed parses fs while tolerating flags that appear after
// positional arguments (Go's flag package otherwise stops at the first
// non-flag). It returns the collected positional arguments. IP inputs never
// begin with '-', so there is no ambiguity.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positionals = append(positionals, args[0])
		args = args[1:]
	}
	return positionals, nil
}

// readInputs returns args verbatim, or whitespace-separated tokens read from
// stdin when args is empty. Blank lines and '#' comment lines are skipped.
func readInputs(args []string, stdin io.Reader) []string {
	if len(args) > 0 {
		return args
	}
	var out []string
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Fields(line)...)
	}
	return out
}
