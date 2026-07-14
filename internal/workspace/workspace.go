// Package workspace materializes an output directory for MCP file-mediated
// results and writes into it with kernel-enforced containment.
//
// The workspace root is either the server-configured default or an
// agent-prepared directory passed per call as workspace_root — the pattern
// used by voice-studio-mcp / asn-lookup. Because an agent-prepared root is
// agent-writable, every write goes through os.Root so a symlink planted in the
// workspace cannot redirect the write outside it (kernel-enforced; a lexical
// pre-check gives a friendly error first).
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Workspace is a materialized output directory.
type Workspace struct {
	BaseDir string
}

// Path joins parts under the base directory (for display / returning to the
// caller).
func (w *Workspace) Path(parts ...string) string {
	return filepath.Join(append([]string{w.BaseDir}, parts...)...)
}

// WriteFileAtomic writes a workspace-relative file (temp + rename) with symlink
// containment, and returns the absolute path written.
func (w *Workspace) WriteFileAtomic(rel string, data []byte) (string, error) {
	clean, err := resolveInside(rel)
	if err != nil {
		return "", err
	}
	r, err := os.OpenRoot(w.BaseDir)
	if err != nil {
		return "", fmt.Errorf("open workspace root: %w", err)
	}
	defer r.Close()
	if dir := filepath.Dir(clean); dir != "." {
		if err := r.MkdirAll(dir, 0o755); err != nil {
			return "", mapRootErr("mkdir", dir, err)
		}
	}
	tmp := clean + ".tmp"
	if err := r.WriteFile(tmp, data, 0o644); err != nil {
		return "", mapRootErr("write", tmp, err)
	}
	if err := r.Rename(tmp, clean); err != nil {
		return "", mapRootErr("rename", clean, err)
	}
	return w.Path(clean), nil
}

// resolveInside lexically rejects absolute paths and workspace escapes.
func resolveInside(rel string) (string, error) {
	if rel == "" {
		return "", errors.New("path must not be empty")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative to the workspace", rel)
	}
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace", rel)
	}
	return cleaned, nil
}

// mapRootErr rewrites os.Root escape errors into a clear message.
func mapRootErr(op, rel string, err error) error {
	if err == nil {
		return nil
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && strings.Contains(pe.Err.Error(), "escapes") {
		return fmt.Errorf("%s %q: path escapes the workspace root (symlink?)", op, rel)
	}
	return err
}

// Manager materializes workspaces under a default root, or under an
// agent-prepared root supplied per call.
type Manager struct {
	defaultRoot string
}

// NewManager returns a Manager with the given server-default root. An empty
// default means "no default" — callers must then supply workspace_root.
func NewManager(defaultRoot string) *Manager {
	if defaultRoot != "" {
		defaultRoot = filepath.Clean(defaultRoot)
	}
	return &Manager{defaultRoot: defaultRoot}
}

// EnsureIn creates and returns a workspace. When root is non-empty it must be
// an absolute path (an agent-prepared directory) and overrides the default.
// When id is non-empty it is a single-segment subdirectory beneath the root.
func (m *Manager) EnsureIn(root, id string) (*Workspace, error) {
	base := m.defaultRoot
	if strings.TrimSpace(root) != "" {
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("workspace_root %q must be an absolute path", root)
		}
		base = filepath.Clean(root)
	}
	if base == "" {
		return nil, errors.New("no workspace root configured; pass workspace_root")
	}
	if id != "" {
		if err := validateID(id); err != nil {
			return nil, err
		}
		base = filepath.Join(base, id)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace %q: %w", base, err)
	}
	return &Workspace{BaseDir: base}, nil
}

// validateID rejects ids that are not a single path segment.
func validateID(id string) error {
	if id != filepath.Base(id) || id == "." || id == ".." || strings.ContainsRune(id, filepath.Separator) {
		return fmt.Errorf("workspace_id %q must be a single path segment", id)
	}
	return nil
}
