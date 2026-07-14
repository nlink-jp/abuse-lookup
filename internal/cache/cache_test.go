package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPutGetRoundtrip(t *testing.T) {
	c := New(t.TempDir(), time.Hour)
	if err := c.Put("1.2.3.4", 90, false, []byte(`{"score":100}`)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rec, ok, err := c.Get("1.2.3.4", 90, false)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if string(rec.Payload) != `{"score":100}` {
		t.Errorf("payload = %s", rec.Payload)
	}
	if rec.IP != "1.2.3.4" || rec.MaxAgeDays != 90 || rec.Verbose {
		t.Errorf("metadata = %+v", rec)
	}
}

func TestGetMiss(t *testing.T) {
	c := New(t.TempDir(), time.Hour)
	if _, ok, err := c.Get("9.9.9.9", 90, false); ok || err != nil {
		t.Fatalf("Get miss: ok=%v err=%v", ok, err)
	}
}

func TestTTLExpiry(t *testing.T) {
	c := New(t.TempDir(), time.Hour)
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return base }
	if err := c.Put("1.2.3.4", 90, false, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	// Within TTL.
	c.now = func() time.Time { return base.Add(30 * time.Minute) }
	if _, ok, _ := c.Get("1.2.3.4", 90, false); !ok {
		t.Error("entry should still be fresh at 30m")
	}
	// Past TTL.
	c.now = func() time.Time { return base.Add(2 * time.Hour) }
	if _, ok, _ := c.Get("1.2.3.4", 90, false); ok {
		t.Error("entry should be stale after 2h")
	}
}

func TestKeyDistinctByParams(t *testing.T) {
	c := New(t.TempDir(), time.Hour)
	if err := c.Put("1.2.3.4", 90, false, []byte(`"plain"`)); err != nil {
		t.Fatal(err)
	}
	if err := c.Put("1.2.3.4", 90, true, []byte(`"verbose"`)); err != nil {
		t.Fatal(err)
	}
	if err := c.Put("1.2.3.4", 30, false, []byte(`"shortwindow"`)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		maxAge  int
		verbose bool
		want    string
	}{
		{90, false, `"plain"`},
		{90, true, `"verbose"`},
		{30, false, `"shortwindow"`},
	} {
		rec, ok, _ := c.Get("1.2.3.4", tc.maxAge, tc.verbose)
		if !ok || string(rec.Payload) != tc.want {
			t.Errorf("Get(%d,%v) = %v / %s, want %s", tc.maxAge, tc.verbose, ok, payload(rec), tc.want)
		}
	}
}

func payload(r *Record) string {
	if r == nil {
		return "<nil>"
	}
	return string(r.Payload)
}

func TestCorruptFileTreatedAsMiss(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, time.Hour)
	// A garbage file with the exact name Put would use.
	bad := filepath.Join(dir, fileName("1.2.3.4", 90, false))
	if err := os.WriteFile(bad, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := c.Get("1.2.3.4", 90, false); ok || err != nil {
		t.Fatalf("corrupt Get: ok=%v err=%v (want miss, no error)", ok, err)
	}
	// List should skip it rather than fail.
	recs, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("List returned %d, want 0 (corrupt skipped)", len(recs))
	}
}

func TestListSortedAndClear(t *testing.T) {
	c := New(t.TempDir(), time.Hour)
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return base }
	c.Put("1.1.1.1", 90, false, []byte(`{}`))
	c.now = func() time.Time { return base.Add(time.Minute) }
	c.Put("2.2.2.2", 90, false, []byte(`{}`))

	recs, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("List len = %d, want 2", len(recs))
	}
	if recs[0].IP != "2.2.2.2" {
		t.Errorf("newest first: got %s", recs[0].IP)
	}

	n, err := c.Clear()
	if err != nil || n != 2 {
		t.Fatalf("Clear: n=%d err=%v", n, err)
	}
	recs, _ = c.List()
	if len(recs) != 0 {
		t.Errorf("after Clear, List len = %d", len(recs))
	}
}

func TestIPv6Key(t *testing.T) {
	c := New(t.TempDir(), time.Hour)
	if err := c.Put("2001:db8::1", 90, false, []byte(`{}`)); err != nil {
		t.Fatalf("Put IPv6: %v", err)
	}
	if _, ok, err := c.Get("2001:db8::1", 90, false); !ok || err != nil {
		t.Fatalf("Get IPv6: ok=%v err=%v", ok, err)
	}
}

func TestClearEmptyDir(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "does-not-exist"), time.Hour)
	if n, err := c.Clear(); n != 0 || err != nil {
		t.Fatalf("Clear missing dir: n=%d err=%v", n, err)
	}
}
