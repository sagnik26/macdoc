package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sagnik26/macdoc/internal/safety"
)

// A sweep may start at a folder directly in home, even a built-in protected
// one, because it removes one exact file name and nothing else (ADR 0014 §4).
func TestCheckSweepRoot(t *testing.T) {
	p := newTestPolicy(t, "~/Projects/secret")
	h := p.Home()
	ok := []string{"Documents", "Desktop", "Pictures", "Projects", "Code"}
	for _, n := range ok {
		if err := p.CheckSweepRoot(filepath.Join(h, n)); err != nil {
			t.Errorf("%s refused: %v", n, err)
		}
	}
	bad := map[string]string{
		"home itself":        h,
		"hidden folder":      filepath.Join(h, ".ssh"),
		"Library":            filepath.Join(h, "Library"),
		"library, any case":  filepath.Join(h, "LIBRARY"),
		"too deep":           filepath.Join(h, "Documents", "Taxes"),
		"outside home":       "/Library/Caches",
		"another user":       "/Users/someone-else",
		"relative":           "Documents",
		"dotdot":             h + "/Documents/../Documents",
		"protected by you":   filepath.Join(h, "Projects", "secret"),
		"inside your secret": filepath.Join(h, "Projects", "secret", "keys"),
	}
	for name, path := range bad {
		if err := p.CheckSweepRoot(path); err == nil {
			t.Errorf("%s: %q accepted", name, path)
		}
	}
}

// A folder you protected is skipped, along with everything below it.
func TestSweepSkips(t *testing.T) {
	p := newTestPolicy(t, "~/Projects/secret")
	h := p.Home()
	for path, want := range map[string]bool{
		filepath.Join(h, "Projects", "secret"):           true,
		filepath.Join(h, "Projects", "secret", "a", "b"): true,
		filepath.Join(h, "Projects"):                     false,
		filepath.Join(h, "Projects", "secret-old"):       false, // look-alike name
		filepath.Join(h, "Documents"):                    false,
	} {
		if got := p.SweepSkips(path); got != want {
			t.Errorf("SweepSkips(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestSweepSkipsDir(t *testing.T) {
	for name, want := range map[string]bool{
		".git": true, ".GIT": true, "node_modules": true, ".Trash": true,
		"Photos Library.photoslibrary": true, "Music.musiclibrary": true,
		"src": false, ".github": false, "node_modules_old": false, "photoslibrary": false,
	} {
		if got := safety.SweepSkipsDir(name); got != want {
			t.Errorf("SweepSkipsDir(%q) = %v, want %v", name, got, want)
		}
	}
	if safety.DSStoreName != ".DS_Store" {
		t.Errorf("DSStoreName = %q", safety.DSStoreName)
	}
}

// A symlink at the sweep root can't smuggle a path in: Canonicalize keeps it
// as the link, and the planner and executor refuse links separately.
func TestSweepRootSymlinkStaysTheLink(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	outside := t.TempDir()
	link := filepath.Join(h, "Link")
	mustSymlink(t, outside, link)
	if err := p.CheckSweepRoot(link); err != nil {
		t.Fatalf("the link's own path is a folder in home: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal(err)
	}
}
