package tests

import (
	"encoding/json"
	"github.com/sagnik26/macdoc/internal/cli"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/executor"
)

// scheduleHome points cli.UserHome at a fresh fixture home for one test.
func scheduleHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uh := cli.UserHome
	cli.UserHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { cli.UserHome = uh })
	return home
}

// enable writes the agent and prints the launchctl line to run; disable
// removes the file and prints the unload line. Neither runs launchctl.
func TestScheduleEnableDisable(t *testing.T) {
	home := scheduleHome(t)
	out, err := run(t, "schedule", "enable")
	if err != nil {
		t.Fatal(err)
	}
	path := executor.ScheduleAgentPath(home)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("enable wrote no agent: %v", err)
	}
	if !strings.Contains(out, "launchctl bootstrap gui/") || !strings.Contains(out, path) {
		t.Errorf("enable output misses the launchctl line: %q", out)
	}

	out, err = run(t, "schedule", "disable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("disable left the agent in place")
	}
	if !strings.Contains(out, "launchctl bootout gui/") {
		t.Errorf("disable output misses the bootout line: %q", out)
	}
	out, err = run(t, "schedule", "disable")
	if err != nil || !strings.Contains(out, "Nothing to remove") {
		t.Errorf("second disable: %q, %v", out, err)
	}
}

func TestScheduleJSON(t *testing.T) {
	scheduleHome(t)
	out, err := run(t, "schedule", "enable", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data cli.ScheduleResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if env.Data.Action != "enable" || !env.Data.Done || env.Data.Label != executor.ScheduleLabel {
		t.Errorf("schedule JSON = %+v", env.Data)
	}
	if _, err := run(t, "schedule", "disable"); err != nil {
		t.Fatal(err)
	}
}
