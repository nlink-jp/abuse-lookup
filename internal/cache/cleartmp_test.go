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
	n, err := s.Clear()
	if err != nil {
		t.Fatal(err)
	}
	// The count is of entries, as status counts them.
	if n != 0 {
		t.Errorf("Clear counted %d entries; the temp file is not one", n)
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Fatalf("the temp file is still there (%v)", err)
	}
}
