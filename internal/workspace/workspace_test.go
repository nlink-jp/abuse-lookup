package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureInAndWrite(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	ws, err := m.EnsureIn("", "job1")
	if err != nil {
		t.Fatalf("EnsureIn: %v", err)
	}
	path, err := ws.WriteFileAtomic("out.json", []byte("[]\n"))
	if err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	want := filepath.Join(root, "job1", "out.json")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "[]\n" {
		t.Errorf("content = %q", data)
	}
}

func TestWriteRejectsEscapes(t *testing.T) {
	ws := &Workspace{BaseDir: t.TempDir()}
	for _, rel := range []string{"../escape.txt", "/abs/escape.txt", ""} {
		if _, err := ws.WriteFileAtomic(rel, []byte("x")); err == nil {
			t.Errorf("WriteFileAtomic(%q): expected error", rel)
		}
	}
}

func TestSymlinkContainment(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	ws := &Workspace{BaseDir: root}
	// Writing through a symlinked component must be refused by os.Root.
	if _, err := ws.WriteFileAtomic("link/pwned.txt", []byte("x")); err == nil {
		t.Fatal("expected symlink traversal to be blocked")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); err == nil {
		t.Fatal("file escaped the workspace via symlink")
	}
}

func TestEnsureInValidation(t *testing.T) {
	m := NewManager(t.TempDir())
	if _, err := m.EnsureIn("relative/root", ""); err == nil {
		t.Error("expected error for relative workspace_root")
	}
	if _, err := m.EnsureIn("", "../evil"); err == nil {
		t.Error("expected error for traversal workspace_id")
	}
	if _, err := m.EnsureIn("", "a/b"); err == nil {
		t.Error("expected error for multi-segment workspace_id")
	}
}

func TestEnsureInNoDefault(t *testing.T) {
	m := NewManager("")
	if _, err := m.EnsureIn("", ""); err == nil || !strings.Contains(err.Error(), "workspace_root") {
		t.Errorf("expected 'pass workspace_root' error, got %v", err)
	}
}
