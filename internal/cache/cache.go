// Package cache stores AbuseIPDB reputation results on disk with a TTL so that
// repeated lookups of the same IP do not re-spend the free-tier daily budget.
//
// Each entry is one JSON file, written atomically (temp + rename) through
// os.Root containment so a corrupt or truncated write is never read back. The
// entry timestamp lives inside the record (not the file mtime), so freshness
// survives file copies and backups. The cache key is the tuple
// (IP, maxAgeDays, verbose): differing parameters yield different responses and
// must not alias.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Record is a cached reputation result plus the metadata needed to key and age
// it. Payload is the raw JSON of an abuseipdb.CheckResult, kept opaque here to
// avoid a dependency cycle.
type Record struct {
	IP         string          `json:"ip"`
	MaxAgeDays int             `json:"max_age_days"`
	Verbose    bool            `json:"verbose"`
	FetchedAt  time.Time       `json:"fetched_at"`
	Payload    json.RawMessage `json:"payload"`
}

// Cache is a TTL-bounded, file-backed store under a single directory.
type Cache struct {
	dir string
	ttl time.Duration
	now func() time.Time
}

// New returns a Cache rooted at dir with the given freshness TTL.
func New(dir string, ttl time.Duration) *Cache {
	return &Cache{dir: dir, ttl: ttl, now: time.Now}
}

// Dir reports the cache directory.
func (c *Cache) Dir() string { return c.dir }

// TTL reports the freshness window.
func (c *Cache) TTL() time.Duration { return c.ttl }

// Get returns the cached record for (ip, maxAgeDays, verbose) when it exists and
// is still within the TTL. A missing or stale entry returns (nil, false, nil); a
// stale file is left in place to be overwritten by the next Put.
func (c *Cache) Get(ip string, maxAgeDays int, verbose bool) (*Record, bool, error) {
	path := filepath.Join(c.dir, fileName(ip, maxAgeDays, verbose))
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read cache %s: %w", path, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		// A corrupt cache file is not fatal — treat it as a miss.
		return nil, false, nil
	}
	if c.now().Sub(rec.FetchedAt) > c.ttl {
		return nil, false, nil
	}
	return &rec, true, nil
}

// Put stores payload for (ip, maxAgeDays, verbose), stamping the fetch time.
func (c *Cache) Put(ip string, maxAgeDays int, verbose bool, payload []byte) error {
	rec := Record{
		IP:         ip,
		MaxAgeDays: maxAgeDays,
		Verbose:    verbose,
		FetchedAt:  c.now(),
		Payload:    json.RawMessage(payload),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return c.writeAtomic(fileName(ip, maxAgeDays, verbose), data)
}

// List returns every valid cached record, newest first.
func (c *Cache) List() ([]Record, error) {
	entries, err := os.ReadDir(c.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recs []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(c.dir, e.Name()))
		if err != nil {
			continue
		}
		var rec Record
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		recs = append(recs, rec)
	}
	sortByFetchedDesc(recs)
	return recs, nil
}

// Fresh reports whether a record is still within the TTL.
func (c *Cache) Fresh(r Record) bool {
	return c.now().Sub(r.FetchedAt) <= c.ttl
}

// Clear removes every cache file and returns the number removed.
func (c *Cache) Clear() (int, error) {
	entries, err := os.ReadDir(c.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if err := os.Remove(filepath.Join(c.dir, e.Name())); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// writeAtomic writes name under the cache dir via temp + rename, contained by
// os.Root so a symlink in the directory cannot redirect the write elsewhere.
func (c *Cache) writeAtomic(name string, data []byte) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir %s: %w", c.dir, err)
	}
	root, err := os.OpenRoot(c.dir)
	if err != nil {
		return fmt.Errorf("open cache dir %s: %w", c.dir, err)
	}
	defer root.Close()
	tmp := name + ".tmp"
	if err := root.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write cache %s: %w", name, err)
	}
	if err := root.Rename(tmp, name); err != nil {
		return fmt.Errorf("rename cache %s: %w", name, err)
	}
	return nil
}

// fileName maps a cache key to a filesystem-safe file name. Valid IP strings
// contain only [0-9a-fA-F.:]; ':' (IPv6) is replaced so the name is safe on all
// target filesystems, which is collision-free among valid IPs.
func fileName(ip string, maxAgeDays int, verbose bool) string {
	v := 0
	if verbose {
		v = 1
	}
	safe := strings.ReplaceAll(ip, ":", "-")
	return fmt.Sprintf("%s_a%d_v%d.json", safe, maxAgeDays, v)
}

func sortByFetchedDesc(recs []Record) {
	for i := 1; i < len(recs); i++ {
		for j := i; j > 0 && recs[j].FetchedAt.After(recs[j-1].FetchedAt); j-- {
			recs[j], recs[j-1] = recs[j-1], recs[j]
		}
	}
}
