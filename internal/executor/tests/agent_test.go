package tests

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"macdoc/internal/executor"
)

// outsideHome is a home folder outside the sandbox.
func outsideHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestScheduleInstallRefusesOutsideSandbox(t *testing.T) {
	home := outsideHome(t)
	path, err := executor.InstallSchedule(home, "/bin/macdoc", "/tmp/x.log")
	if err == nil || path != "" || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("install = %q, %v", path, err)
	}
	if _, err := os.Lstat(filepath.Join(home, "Library")); !os.IsNotExist(err) {
		t.Fatal("a refused install still created folders")
	}
}

func TestScheduleRemoveRefusesOutsideSandbox(t *testing.T) {
	home := outsideHome(t)
	agent := executor.ScheduleAgentPath(home)
	if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	// macdoc's own file, but outside the sandbox: it stays.
	body := "<plist><string>" + executor.ScheduleLabel + "</string></plist>"
	if err := os.WriteFile(agent, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := executor.RemoveSchedule(home)
	if err == nil || removed || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	if b, _ := os.ReadFile(agent); string(b) != body {
		t.Fatal("a refused remove changed the file")
	}
}

// Installing again replaces macdoc's own file with the new binary and says
// where it wrote.
func TestScheduleReinstallReplacesOurs(t *testing.T) {
	home := newEnv(t).home
	if _, err := executor.InstallSchedule(home, "/old/macdoc", "/tmp/x.log"); err != nil {
		t.Fatal(err)
	}
	path, err := executor.InstallSchedule(home, "/new/macdoc", "/tmp/x.log")
	if err != nil || path != executor.ScheduleAgentPath(home) {
		t.Fatalf("reinstall = %q, %v", path, err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "<string>/new/macdoc</string>") || strings.Contains(string(b), "/old/macdoc") {
		t.Fatalf("agent = %s", b)
	}
}

func TestScheduleInstallFailures(t *testing.T) {
	t.Run("LaunchAgents can't be made", func(t *testing.T) {
		home := newEnv(t).home
		if err := os.WriteFile(filepath.Join(home, "Library", "LaunchAgents"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if p, err := executor.InstallSchedule(home, "/bin/macdoc", "/tmp/x.log"); err == nil || p != "" {
			t.Fatalf("install = %q, %v", p, err)
		}
	})
	t.Run("the agent path is a folder", func(t *testing.T) {
		home := newEnv(t).home
		agent := executor.ScheduleAgentPath(home)
		if err := os.MkdirAll(filepath.Join(agent, "keep"), 0o755); err != nil {
			t.Fatal(err)
		}
		if p, err := executor.InstallSchedule(home, "/bin/macdoc", "/tmp/x.log"); err == nil || p != "" {
			t.Fatalf("install = %q, %v", p, err)
		}
		if _, err := os.Stat(filepath.Join(agent, "keep")); err != nil {
			t.Fatal("the folder at the agent path was disturbed")
		}
		if removed, err := executor.RemoveSchedule(home); err == nil || removed {
			t.Fatalf("remove of a folder = %v, %v", removed, err)
		}
		if _, err := os.Stat(filepath.Join(agent, "keep")); err != nil {
			t.Fatal("remove disturbed the folder at the agent path")
		}
	})
	t.Run("LaunchAgents is read-only", func(t *testing.T) {
		home := newEnv(t).home
		dir := filepath.Dir(executor.ScheduleAgentPath(home))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // G302: a read-only fixture folder.
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // G302: lets the cleanup remove it.
		if p, err := executor.InstallSchedule(home, "/bin/macdoc", "/tmp/x.log"); err == nil || p != "" {
			t.Fatalf("install = %q, %v", p, err)
		}
		if _, err := os.Lstat(executor.ScheduleAgentPath(home)); !os.IsNotExist(err) {
			t.Fatal("a file appeared despite the error")
		}
	})
	t.Run("remove can't delete", func(t *testing.T) {
		home := newEnv(t).home
		if _, err := executor.InstallSchedule(home, "/bin/macdoc", "/tmp/x.log"); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Dir(executor.ScheduleAgentPath(home))
		if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // G302: a read-only fixture folder.
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // G302: lets the cleanup remove it.
		if removed, err := executor.RemoveSchedule(home); err == nil || removed {
			t.Fatalf("remove = %v, %v", removed, err)
		}
	})
}

// A file at the agent path that can't be read can't be shown to be macdoc's,
// so it is neither replaced nor removed, and the error is the real one.
func TestScheduleUnreadableFileIsLeftAlone(t *testing.T) {
	home := newEnv(t).home
	agent := executor.ScheduleAgentPath(home)
	if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agent, []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(agent, 0o200); err != nil { // writable, but not readable
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(agent, 0o600) })
	if p, err := executor.InstallSchedule(home, "/bin/macdoc", "/tmp/x.log"); !errors.Is(err, fs.ErrPermission) || p != "" {
		t.Errorf("install = %q, %v; want a permission error", p, err)
	}
	if removed, err := executor.RemoveSchedule(home); !errors.Is(err, fs.ErrPermission) || removed {
		t.Errorf("remove = %v, %v; want a permission error", removed, err)
	}
	if err := os.Chmod(agent, 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(agent); string(b) != "someone else's" {
		t.Fatalf("the file was changed: %q", b)
	}
}

// A symlink at the agent path must not be followed to write or delete
// anything elsewhere.
func TestScheduleSymlinkedAgent(t *testing.T) {
	t.Run("link to a foreign file", func(t *testing.T) {
		home := newEnv(t).home
		agent := executor.ScheduleAgentPath(home)
		if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
			t.Fatal(err)
		}
		foreign := filepath.Join(home, "foreign.plist")
		if err := os.WriteFile(foreign, []byte("someone else's"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustSymlink(t, foreign, agent)
		if _, err := executor.InstallSchedule(home, "/bin/macdoc", "/tmp/x.log"); err == nil {
			t.Error("install followed a symlink to a foreign file")
		}
		if removed, err := executor.RemoveSchedule(home); err == nil || removed {
			t.Errorf("remove = %v, %v", removed, err)
		}
		if b, _ := os.ReadFile(foreign); string(b) != "someone else's" {
			t.Fatal("the file behind the link was changed")
		}
	})
}
