package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/probe"
)

// The text view shows the score, says nothing was changed, and carries the
// notes about probes that couldn't run (the test runner answers none of them).
func TestDoctorStatusText(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "vitals")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Health", "/ 100", "Now", "Nothing has been changed.")
}

// --json has the envelope, the score, and no section for a probe that failed.
func TestDoctorStatusJSON(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "vitals", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Command string `json:"command"`
		Data    struct {
			Score   int            `json:"score"`
			Reasons []string       `json:"reasons"`
			CPU     map[string]any `json:"cpu"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if env.Command != "vitals" || env.Data.Score < 0 || env.Data.Score > 100 || env.Data.Reasons == nil {
		t.Errorf("envelope = %+v", env)
	}
	if env.Data.CPU != nil {
		t.Errorf("the fake runner has no CPU output, so the section must be absent: %v", env.Data.CPU)
	}
}

// An interval shorter than a reading takes is refused.
func TestDoctorStatusRefusesAShortInterval(t *testing.T) {
	cliFixture(t)
	if _, err := run(t, "vitals", "--watch", "--interval", "100ms"); err == nil || !strings.Contains(err.Error(), "--interval") {
		t.Errorf("err = %v; want one naming --interval", err)
	}
}

// --watch --json streams one compact JSON snapshot per line, and stopping it
// (here by the context ending) is not an error.
func TestDoctorStatusWatchStreamsOneObjectPerLine(t *testing.T) {
	cliFixture(t)
	cmd := cli.NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"vitals", "--watch", "--interval", "1s", "--json"})
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("stopping a watch is not an error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("got %d lines, want at least 2 snapshots:\n%s", len(lines), buf.String())
	}
	for _, l := range lines {
		var env struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(l), &env); err != nil || env.Command != "vitals" {
			t.Errorf("line is not one compact snapshot: %q (%v)", l, err)
		}
	}
}

// Alerts are shown in plain words, a process name from the machine cannot
// inject terminal codes, and a quiet system has no alerts section at all.
func TestRenderStatusShowsProcessAlerts(t *testing.T) {
	res := engine.StatusResult{Alerts: &probe.ProcessAlerts{
		Zombies: []probe.ZombieParent{{PID: 20, Name: "Fo\x1bo", Zombies: 2, ParentOK: true}, {PID: 9999, Zombies: 1}},
		Busy:    []probe.BusyProcess{{PID: 300, Name: "yes", CPUPercent: 97.5}},
	}}
	var b bytes.Buffer
	if err := cli.RenderStatus(&b, res); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	mustContain(t, out, "Process alerts", "Foo (pid 20) has 2 finished child processes", "pid 9999, which is not in the process list", "yes (pid 300) is using 98% of a CPU core")
	if strings.Contains(out, "\x1b") {
		t.Errorf("a control character got through: %q", out)
	}
	b.Reset()
	if err := cli.RenderStatus(&b, engine.StatusResult{Alerts: &probe.ProcessAlerts{}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "Process alerts") {
		t.Error("a quiet system must show no alerts section")
	}
}
