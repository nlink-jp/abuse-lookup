package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// clearEnv blanks the override env vars so the process environment cannot leak
// into a test. t.Setenv also restores them afterward.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ABUSEIPDB_API_KEY", "ABUSE_LOOKUP_KEY",
		"ABUSE_LOOKUP_BASE_URL", "ABUSE_LOOKUP_CACHE_DIR",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	missing := filepath.Join(t.TempDir(), "none.toml")
	cfg, err := Load(missing, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want default", cfg.BaseURL)
	}
	if cfg.CacheTTL != DefaultTTLHours*time.Hour {
		t.Errorf("CacheTTL = %v, want %d h", cfg.CacheTTL, DefaultTTLHours)
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", cfg.APIKey)
	}
}

func TestLoadFromFile(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `# sample
[abuseipdb]
key = "filekey"
base_url = "https://example.test/api"

[cache]
ttl_hours = 6
dir = "` + dir + `/cc"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIKey != "filekey" {
		t.Errorf("APIKey = %q", cfg.APIKey)
	}
	if cfg.BaseURL != "https://example.test/api" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.CacheTTL != 6*time.Hour {
		t.Errorf("CacheTTL = %v, want 6h", cfg.CacheTTL)
	}
	if cfg.CacheDir != dir+"/cc" {
		t.Errorf("CacheDir = %q", cfg.CacheDir)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[abuseipdb]\nkey = \"filekey\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABUSEIPDB_API_KEY", "envkey")
	cfg, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIKey != "envkey" {
		t.Errorf("APIKey = %q, want envkey (env wins over file)", cfg.APIKey)
	}
}

func TestKeyOverrideWins(t *testing.T) {
	clearEnv(t)
	t.Setenv("ABUSEIPDB_API_KEY", "envkey")
	cfg, err := Load(filepath.Join(t.TempDir(), "none.toml"), "flagkey")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIKey != "flagkey" {
		t.Errorf("APIKey = %q, want flagkey (override wins)", cfg.APIKey)
	}
}

func TestInvalidTTL(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[cache]\nttl_hours = \"nope\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Fatal("Load: expected error for non-numeric ttl_hours")
	}
}

func TestNegativeTTL(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[cache]\nttl_hours = -1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Fatal("Load: expected error for negative ttl_hours")
	}
}
