package tests

import (
	"github.com/sagnik26/macdoc/internal/executor"
	"os"
	"path/filepath"
	"testing"
)

// writeTool makes an executable file in dir with the given mode.
func writeTool(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // G302: a test fixture folder.
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// A tool that sits in a folder or is itself writable by others is refused,
// so a planted tool on PATH can't run as the user (P6-R1 notes).
func TestResolveToolRefusesWritableByOthers(t *testing.T) {
	base := t.TempDir()

	openDir := filepath.Join(base, "open")
	writeTool(t, openDir, "mytool", 0o755)
	if err := os.Chmod(openDir, 0o777); err != nil { //nolint:gosec // G302: the folder is the case under test.
		t.Fatal(err)
	}
	t.Setenv("PATH", openDir)
	if _, err := executor.ResolveTool("mytool"); err == nil {
		t.Error("a tool in a folder writable by others was accepted")
	}

	fileDir := filepath.Join(base, "fileopen")
	writeTool(t, fileDir, "mytool2", 0o777)
	t.Setenv("PATH", fileDir)
	if _, err := executor.ResolveTool("mytool2"); err == nil {
		t.Error("a tool file writable by others was accepted")
	}
}

func TestResolveToolAcceptsOwnTool(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bin")
	want := writeTool(t, dir, "mytool3", 0o755)
	t.Setenv("PATH", dir)
	got, err := executor.ResolveTool("mytool3")
	if err != nil {
		t.Fatalf("own tool refused: %v", err)
	}
	if got != want && filepath.Base(got) != "mytool3" {
		t.Errorf("resolved to %q", got)
	}
}

func TestResolveToolRejectsPaths(t *testing.T) {
	for _, name := range []string{"", "/bin/sh", "../sh", "a/b"} {
		if _, err := executor.ResolveTool(name); err == nil {
			t.Errorf("resolveTool(%q) accepted a path", name)
		}
	}
}
