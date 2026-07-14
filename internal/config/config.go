// Package config resolves abuse-lookup settings from a sectioned TOML file plus
// environment overrides. It parses only the small TOML subset the tool needs,
// keeping the binary free of external dependencies.
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the AbuseIPDB API v2 base endpoint.
	DefaultBaseURL = "https://api.abuseipdb.com/api/v2"
	// DefaultTTLHours is how long a cached result is reused before a re-fetch.
	DefaultTTLHours = 12
)

// Config holds resolved runtime settings.
type Config struct {
	APIKey    string        // AbuseIPDB API key (secret; never logged verbatim)
	BaseURL   string        // AbuseIPDB API base URL
	CacheDir  string        // directory holding cached reputation results
	CacheTTL  time.Duration // how long a cached result stays fresh
	Workspace string        // default MCP output directory for file-mediated results
}

// Load resolves configuration. If configPath is empty the default location
// (~/.config/abuse-lookup/config.toml) is used when present. Environment
// variables override file values, and any explicit non-empty override* argument
// wins over both.
func Load(configPath, keyOverride string) (*Config, error) {
	cfg := &Config{
		BaseURL:   DefaultBaseURL,
		CacheDir:  DefaultCacheDir(),
		CacheTTL:  DefaultTTLHours * time.Hour,
		Workspace: DefaultWorkspaceDir(),
	}

	if configPath == "" {
		configPath = DefaultConfigPath()
	}
	if configPath != "" {
		if f, err := os.Open(configPath); err == nil {
			defer f.Close()
			sections, perr := parseTOML(f)
			if perr != nil {
				return nil, fmt.Errorf("parse config %s: %w", configPath, perr)
			}
			if aerr := applySections(cfg, sections); aerr != nil {
				return nil, fmt.Errorf("config %s: %w", configPath, aerr)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("open config %s: %w", configPath, err)
		}
	}

	// Environment overrides.
	if v := firstEnv("ABUSEIPDB_API_KEY", "ABUSE_LOOKUP_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := firstEnv("ABUSE_LOOKUP_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := firstEnv("ABUSE_LOOKUP_CACHE_DIR"); v != "" {
		cfg.CacheDir = v
	}
	if v := firstEnv("ABUSE_LOOKUP_WORKSPACE"); v != "" {
		cfg.Workspace = v
	}

	// Explicit flag overrides win.
	if keyOverride != "" {
		cfg.APIKey = keyOverride
	}

	return cfg, nil
}

func applySections(cfg *Config, sections map[string]map[string]string) error {
	if a := sections["abuseipdb"]; a != nil {
		if v := a["key"]; v != "" {
			cfg.APIKey = v
		}
		if v := a["base_url"]; v != "" {
			cfg.BaseURL = v
		}
	}
	if c := sections["cache"]; c != nil {
		if v := c["dir"]; v != "" {
			cfg.CacheDir = expandHome(v)
		}
		if v := c["ttl_hours"]; v != "" {
			h, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("[cache] ttl_hours %q: %w", v, err)
			}
			if h < 0 {
				return fmt.Errorf("[cache] ttl_hours must not be negative")
			}
			cfg.CacheTTL = time.Duration(h * float64(time.Hour))
		}
	}
	if m := sections["mcp"]; m != nil {
		if v := m["workspace"]; v != "" {
			cfg.Workspace = expandHome(v)
		}
	}
	return nil
}

// DefaultConfigPath returns the default config file location, honoring
// XDG_CONFIG_HOME.
func DefaultConfigPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "abuse-lookup", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "abuse-lookup", "config.toml")
}

// DefaultCacheDir returns the default cache directory, honoring XDG_DATA_HOME.
func DefaultCacheDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "abuse-lookup", "cache")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "cache"
	}
	return filepath.Join(home, ".local", "share", "abuse-lookup", "cache")
}

// DefaultWorkspaceDir returns the default MCP output directory, honoring
// XDG_STATE_HOME (file-mediated results are reproducible, transient state).
func DefaultWorkspaceDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "abuse-lookup", "workspace")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "abuse-lookup", "workspace")
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// parseTOML parses the minimal subset abuse-lookup needs: [section] headers and
// key = value lines, where value is an optionally quoted string. Comments start
// with '#'. It intentionally does not support arrays, nested tables, or typed
// values.
func parseTOML(r io.Reader) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{}
	current := "" // top-level keys land in the "" section
	sections[current] = map[string]string{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if strings.HasPrefix(raw, "[") {
			end := strings.IndexByte(raw, ']')
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated section header", line)
			}
			current = strings.TrimSpace(raw[1:end])
			if _, ok := sections[current]; !ok {
				sections[current] = map[string]string{}
			}
			continue
		}
		eq := strings.IndexByte(raw, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", line)
		}
		key := strings.TrimSpace(raw[:eq])
		val := parseValue(strings.TrimSpace(raw[eq+1:]))
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", line)
		}
		sections[current][key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return sections, nil
}

// parseValue strips surrounding quotes, or trims a trailing inline comment from
// a bare value.
func parseValue(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		q := v[0]
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return v[1 : 1+end]
		}
	}
	if hash := strings.IndexByte(v, '#'); hash >= 0 {
		v = strings.TrimSpace(v[:hash])
	}
	return v
}
