package tests

// Tests for the P0-W3 tech-lead decisions (docs/tasks/P0-W3.md) that the
// P0-R1 red-team tests do not already cover.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/sandbox"
)

// D8: sandbox.Check refuses non-canonical input even when it would stay inside the
// sandbox.
func TestD8CheckRefusesNonCanonical(t *testing.T) {
	base, _ := setup(t)
	sb := mkdir(t, filepath.Join(base, "sb"))
	mkdir(t, filepath.Join(sb, "real"))
	if err := os.Symlink(filepath.Join(sb, "real"), filepath.Join(sb, "link")); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sandbox.Env, sb)
	for _, path := range []string{
		sb + "/link/x",  // symlinked parent, resolves inside
		sb + "/real/x/", // trailing slash
		sb + "/real/.",  // final "."
		sb + "/real/../real/x",
		sb + "/real/x\x00",
	} {
		if err := sandbox.Check(path); err == nil {
			t.Errorf("Check(%q) = nil; want non-canonical input refused", path)
		}
	}
	for _, path := range []string{sb + "/real/x", sb + "/link", sb + "/./real//x"} {
		if err := sandbox.Check(path); err != nil {
			t.Errorf("Check(%q) = %v; want nil", path, err)
		}
	}
	if err := sandbox.Check(sb + "/link/x"); err == nil || !strings.Contains(err.Error(), "non-canonical") {
		t.Errorf("Check(link/x) error = %v; want it to say non-canonical", err)
	}
}

// D9: a t.TempDir() sandbox is accepted.
func TestD9TempDirAccepted(t *testing.T) {
	setup(t)
	sb := t.TempDir() // /var/folders/..., resolves under /private/var/folders
	t.Setenv(sandbox.Env, sb)
	root, active, err := sandbox.Root()
	if err != nil || !active {
		t.Fatalf("Root() with TempDir %q = %q, %v, %v", sb, root, active, err)
	}
	if err := sandbox.Check(filepath.Join(root, "x")); err != nil {
		t.Errorf("Check inside TempDir sandbox: %v", err)
	}
}

// D9: a sandbox within a system root, or equal to a temp root, is rejected.
func TestD9SystemAreasRejected(t *testing.T) {
	// A (missing) home outside the candidates, so the system-root check is
	// what rejects them rather than the home-ancestor check.
	t.Setenv("HOME", "/nonexistent-macdoc-home/dev")
	for _, tt := range []struct{ val, want string }{
		{"/private", "covers system path"},
		{"/private/var", "inside system path"},
		{"/private/var/folders", "inside system path"},
		{"/private/tmp", "inside system path"},
		{"/Library/Caches", "inside system path"},
		{"/usr/share", "inside system path"},
	} {
		if _, err := os.Stat(tt.val); err != nil {
			continue
		}
		t.Setenv(sandbox.Env, tt.val)
		root, active, err := sandbox.Root()
		if err == nil || !active || root != "" {
			t.Errorf("Root() with %q = %q, %v, %v; want rejection", tt.val, root, active, err)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Root() with %q error %q does not mention %q", tt.val, err, tt.want)
		}
	}
}

// A sandbox outside every system root (e.g. a non-temp dir elsewhere) is
// still allowed; only home and its ancestors are excluded there.
func TestD9NonSystemRootAllowed(t *testing.T) {
	if err := sandbox.System("/nonexistent-macdoc/sb"); err != nil {
		t.Errorf("checkSystemRoots outside system roots: %v", err)
	}
	if err := sandbox.System("/private/tmp/sb"); err != nil {
		t.Errorf("checkSystemRoots below /private/tmp: %v", err)
	}
}
