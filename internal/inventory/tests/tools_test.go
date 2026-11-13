package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagnik26/macdoc/internal/inventory"
	"github.com/sagnik26/macdoc/internal/platform"
)

// writeTool makes an executable program in dir.
func writeTool(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// toolsOnly discovers the programs in dirs, with the other sources switched off.
func toolsOnly(t *testing.T, dirs ...string) map[string]inventory.Software {
	t.Helper()
	res := inventory.Discover(context.Background(), inventory.Env{
		BinDirs: dirs, Runner: &platform.Fake{}, HasTool: func(string) bool { return false },
		AppDirs: []string{},
	})
	got := map[string]inventory.Software{}
	for _, s := range res.Software {
		if s.Source == "tools" {
			got[s.ID] = s
		}
	}
	return got
}

// Positive: executables in a user folder are listed as tools.
func TestToolsListsExecutablesInUserFolders(t *testing.T) {
	dir := t.TempDir()
	writeTool(t, dir, "pip")
	writeTool(t, dir, "uv")
	got := toolsOnly(t, dir)
	for _, id := range []string{"tool:pip", "tool:uv"} {
		if s, ok := got[id]; !ok || s.Kind != "tool" || s.Installed.IsZero() {
			t.Errorf("%s = %+v, %v", id, s, ok)
		}
	}
}

// Look-alike and precondition failures: a file without the execute bit,
// a folder, a hidden file and a broken link are not programs.
func TestToolsSkipsNonPrograms(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes"), []byte("text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTool(t, dir, ".hidden")
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "broken")); err != nil {
		t.Fatal(err)
	}
	if got := toolsOnly(t, dir); len(got) != 0 {
		t.Errorf("non-programs listed: %+v", got)
	}
}

// A name in an earlier folder on PATH is the one a shell runs, so a later
// copy is not listed again.
func TestToolsFirstFolderWins(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeTool(t, first, "node")
	writeTool(t, second, "node")
	writeTool(t, second, "deno")
	got := toolsOnly(t, first, second)
	if len(got) != 2 {
		t.Fatalf("got %d tools, want node once and deno: %+v", len(got), got)
	}
	if got["tool:node"].Evidence != filepath.Join(first, "node") {
		t.Errorf("node evidence = %q; want the copy in the first folder", got["tool:node"].Evidence)
	}
}

// A symlink to a program counts; its target is the program.
func TestToolsFollowsSymlinks(t *testing.T) {
	target, dir := t.TempDir(), t.TempDir()
	writeTool(t, target, "realtool")
	if err := os.Symlink(filepath.Join(target, "realtool"), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, ok := toolsOnly(t, dir)["tool:linked"]; !ok {
		t.Error("a symlink to an executable should be listed")
	}
}
