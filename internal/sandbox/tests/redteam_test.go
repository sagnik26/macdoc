package tests

// Red-team tests (task P0-R1). Failures starting with "BUG:" are confirmed
// findings (see .task/REPORT.md); the rest document attacks that held.

import (
	"github.com/sagnik26/macdoc/internal/sandbox"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RT-08: sandbox.Check trusts its caller to pass a canonical path and compares
// lexically. A non-canonical path through a symlink inside the sandbox, or a
// ".." after such a symlink, passes sandbox.Check while the kernel resolves it
// outside the sandbox. The sandbox exists to catch executor bugs, so it
// should not depend on the executor having canonicalized correctly.
func TestRedteamCheckNonCanonical(t *testing.T) {
	base, _ := setup(t)
	sb := mkdir(t, filepath.Join(base, "sb"))
	outside := mkdir(t, filepath.Join(base, "outside", "deep"))
	if err := os.WriteFile(filepath.Join(outside, "victim"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "outside", "sibling"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sb, "escape")); err != nil {
		t.Fatal(err)
	}
	t.Setenv(sandbox.EnvVar, sb)

	for _, tt := range []struct{ path, real string }{
		{sb + "/escape/victim", filepath.Join(outside, "victim")},
		{sb + "/escape/../sibling", filepath.Join(base, "outside", "sibling")},
	} {
		fi, err := os.Stat(tt.path)
		if err != nil {
			t.Fatal(err)
		}
		ri, _ := os.Stat(tt.real)
		if !os.SameFile(fi, ri) {
			t.Fatalf("precondition: %q is not %q", tt.path, tt.real)
		}
		if err := sandbox.Check(tt.path); err == nil {
			t.Errorf("BUG: sandbox.Check(%q) = nil; the kernel resolves it to %s, outside the sandbox", tt.path, tt.real)
		}
	}
}

// RT-09: sandbox.Root accepts a sandbox that is, or contains, a system root. Such a
// sandbox does not narrow anything (/Applications, /System, /Volumes). Only
// "/", $HOME and ancestors of $HOME are rejected today.
func TestRedteamRootSystemDirs(t *testing.T) {
	setup(t)
	for _, v := range []string{"/System", "/Applications", "/Library", "/Volumes", "/usr"} {
		if _, err := os.Stat(v); err != nil {
			continue
		}
		t.Setenv(sandbox.EnvVar, v)
		if root, _, err := sandbox.Root(); err == nil {
			t.Errorf("BUG: sandbox.Root() accepted %s=%s (root %q); a sandbox must not cover a system area", sandbox.EnvVar, v, root)
		}
	}
}

// --- Attacks that held -------------------------------------------------------

func TestRedteamRootForms(t *testing.T) {
	base, home := setup(t)
	sb := mkdir(t, filepath.Join(base, "sb"))
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home, filepath.Join(base, "homelink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(base, filepath.Join(sb, "up")); err != nil {
		t.Fatal(err)
	}

	ok := []string{sb + "/", sb + "//", sb + "/.", "/" + sb, strings.ToUpper(sb)}
	for _, v := range ok {
		t.Setenv(sandbox.EnvVar, v)
		root, active, err := sandbox.Root()
		if err != nil || !active || !strings.EqualFold(root, sb) {
			t.Errorf("Root() with %q = %q, %v, %v; want %q", v, root, active, err, sb)
		}
	}
	bad := []string{
		" ", "\t", "/", // (NUL cannot be put in the environment) "/.", "//", sb + "/..", sb + "/../sb",
		home, home + "/", strings.ToUpper(home), filepath.Dir(home), filepath.Join(base, "homelink"),
		sb + "/up",   // symlink to an ancestor of home
		file,         // not a directory
		sb + "/nope", // missing
		"~/sb", "sb",
	}
	for _, v := range bad {
		t.Setenv(sandbox.EnvVar, v)
		root, active, err := sandbox.Root()
		if err == nil {
			t.Errorf("Root() with %q accepted as %q", v, root)
		}
		if !active {
			t.Errorf("Root() with %q reported inactive alongside the error", v)
		}
		if err := sandbox.Check(filepath.Join(sb, "x")); err == nil {
			t.Errorf("Check with invalid %s=%q = nil; want fail closed", sandbox.EnvVar, v)
		}
	}
}

// A sandbox removed after it was set makes every sandbox.Check fail closed.
func TestRedteamRootRemovedLater(t *testing.T) {
	base, home := setup(t)
	sb := mkdir(t, filepath.Join(base, "sb"))
	t.Setenv(sandbox.EnvVar, sb)
	if err := sandbox.Check(sb + "/x"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(sb, sb+".gone"); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.Check(sb + "/x"); err == nil {
		t.Error("Check after the sandbox dir vanished = nil; want fail closed")
	}
	// Re-creating it as a symlink to home is rejected too.
	if err := os.Symlink(home, sb); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.Check(home + "/x"); err == nil {
		t.Error("sandbox swapped for a symlink to home: sandbox.Check = nil")
	}
}

// Without HOME the home checks cannot run, so sandbox.Root must fail closed.
func TestRedteamRootNoHome(t *testing.T) {
	base, _ := setup(t)
	sb := mkdir(t, filepath.Join(base, "sb"))
	t.Setenv(sandbox.EnvVar, sb)
	t.Setenv("HOME", "")
	if _, _, err := sandbox.Root(); err == nil {
		t.Error("Root() with HOME empty = nil error")
	}
	t.Setenv("HOME", "relative/home")
	if _, _, err := sandbox.Root(); err == nil {
		t.Error("Root() with relative HOME = nil error")
	}
}

func TestRedteamCheckPaths(t *testing.T) {
	base, _ := setup(t)
	sb := mkdir(t, filepath.Join(base, "sb"))
	t.Setenv(sandbox.EnvVar, sb)
	for _, tt := range []struct {
		path string
		ok   bool
	}{
		{sb + "/x/../../outside", false}, // lexically outside
		{sb + "/../sb/x", false},         // P0-W3 D8: non-canonical input is refused (was ok)
		{sb + "/.", false},               // equals root
		{sb + "//", false},
		{sb + "x", false}, // name-prefix sibling
		{strings.ToUpper(sb) + "/X", true},
		{"/" + sb + "/x", true},
		{"./" + sb + "/x", false},
	} {
		if err := sandbox.Check(tt.path); (err == nil) != tt.ok {
			t.Errorf("Check(%q) = %v; want ok=%v", tt.path, err, tt.ok)
		}
	}
}

// Spec ambiguity A1, resolved by P0-W3 D10 (this test used to assert the old
// "empty disables" behavior): a set-but-empty variable fails closed.
func TestRedteamEmptyDisables(t *testing.T) {
	base, _ := setup(t)
	t.Setenv(sandbox.EnvVar, "")
	if root, active, err := sandbox.Root(); root != "" || !active || err == nil {
		t.Errorf("Root() with empty var = %q, %v, %v; want \"\", true, error", root, active, err)
	}
	if err := sandbox.Check(filepath.Join(base, "x")); err == nil {
		t.Error("Check with empty var = nil; want fail closed")
	}
}
