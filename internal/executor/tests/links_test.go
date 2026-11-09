package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/executor"
)

// The schedule commands may touch exactly one file, the agent path. If that
// path is a symlink, writing through it would change a different file, so the
// link is refused and nothing it points at is touched. The mutation-testing
// agent found that InstallSchedule followed a dangling link and created the
// target.

func TestScheduleRefusesALinkToNothing(t *testing.T) {
	home := newEnv(t).home
	agent := executor.ScheduleAgentPath(home)
	if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "Documents", "elsewhere.plist")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, agent); err != nil {
		t.Fatal(err)
	}

	_, err := executor.InstallSchedule(home, "/bin/macdoc", filepath.Join(home, "scan.log"))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("install over a dangling link: err = %v, want a refusal that names the link", err)
	}
	if _, serr := os.Lstat(outside); !os.IsNotExist(serr) {
		t.Errorf("the file the link pointed at was created: %v", serr)
	}
	if fi, lerr := os.Lstat(agent); lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link itself was changed: %v %v", fi, lerr)
	}
}

func TestScheduleRefusesALinkToAnotherAgent(t *testing.T) {
	home := newEnv(t).home
	agent := executor.ScheduleAgentPath(home)
	if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file that carries macdoc's label, so it looks like macdoc's own.
	target := filepath.Join(home, "dotfiles", "macdoc.plist")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "<plist><string>" + executor.ScheduleLabel + "</string> my own notes</plist>"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, agent); err != nil {
		t.Fatal(err)
	}

	if _, err := executor.InstallSchedule(home, "/bin/macdoc", filepath.Join(home, "scan.log")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("install over a link: err = %v", err)
	}
	if removed, err := executor.RemoveSchedule(home); err == nil || removed {
		t.Fatalf("remove of a link: removed=%v err=%v; want it left alone", removed, err)
	}
	if b, _ := os.ReadFile(target); string(b) != original {
		t.Errorf("the linked file was changed: %q", b)
	}
	if fi, err := os.Lstat(agent); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was removed or replaced: %v %v", fi, err)
	}
}
