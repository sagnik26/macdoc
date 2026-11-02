package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The agent is written to the one LaunchAgents file, runs `macdoc scan
// --quiet`, and never replaces a file that isn't macdoc's.
func TestScheduleInstallWritesOnlyTheAgent(t *testing.T) {
	home := newEnv(t).home // a fixture home inside the sandbox
	bin := "/Users/me/bin/macdoc & co"
	logp := filepath.Join(home, "Library/Application Support/macdoc/scan.log")
	path, err := InstallSchedule(home, bin, logp)
	if err != nil {
		t.Fatal(err)
	}
	if path != ScheduleAgentPath(home) {
		t.Fatalf("wrote %s, want %s", path, ScheduleAgentPath(home))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"<string>" + ScheduleLabel + "</string>", "<string>scan</string>", "<string>--quiet</string>",
		"<integer>9</integer>", "<string>/Users/me/bin/macdoc &amp; co</string>", "<key>LowPriorityIO</key>"} {
		if !strings.Contains(s, want) {
			t.Errorf("agent misses %q", want)
		}
	}
	// A second install replaces macdoc's own file.
	if _, err := InstallSchedule(home, bin, logp); err != nil {
		t.Errorf("reinstall: %v", err)
	}
}

func TestScheduleNeverReplacesAForeignFile(t *testing.T) {
	home := newEnv(t).home // a fixture home inside the sandbox
	foreign := ScheduleAgentPath(home)
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("<plist>someone else's</plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSchedule(home, "/bin/macdoc", "/tmp/x.log"); err == nil || !strings.Contains(err.Error(), "isn't macdoc's") {
		t.Fatalf("install over a foreign file: err = %v", err)
	}
	if _, err := RemoveSchedule(home); err == nil || !strings.Contains(err.Error(), "isn't macdoc's") {
		t.Fatalf("remove of a foreign file: err = %v", err)
	}
	if b, _ := os.ReadFile(foreign); !strings.Contains(string(b), "someone else's") {
		t.Fatal("the foreign file was changed")
	}
}

func TestScheduleRemoveRemovesOnlyOurs(t *testing.T) {
	home := newEnv(t).home // a fixture home inside the sandbox
	if removed, err := RemoveSchedule(home); err != nil || removed {
		t.Fatalf("nothing installed: removed %v, err %v", removed, err)
	}
	if _, err := InstallSchedule(home, "/bin/macdoc", "/tmp/x.log"); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveSchedule(home); err != nil || !removed {
		t.Fatalf("remove: removed %v, err %v", removed, err)
	}
	if _, err := os.Stat(ScheduleAgentPath(home)); !os.IsNotExist(err) {
		t.Fatal("the agent is still there")
	}
}
