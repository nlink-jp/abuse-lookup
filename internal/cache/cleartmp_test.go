package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cache clear also removes the temp file a write left when the process ended
// part-way (a SIGTERM after the MCP server's grace, a crash): it held an
// answer, and Clear is the one way an operator deletes cached answers.
func TestClearRemovesALeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "1.1.1.1.json.tmp")
	if err := os.WriteFile(left, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir, time.Hour)
	if _, err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Fatalf("the temp file is still there (%v)", err)
	}
}
