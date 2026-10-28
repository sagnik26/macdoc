package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"macdoc/internal/engine"
	"macdoc/internal/probe"
)

func TestRenderSlow(t *testing.T) {
	now := time.Date(2026, 10, 28, 15, 0, 0, 0, time.Local)
	res := engine.SlowResult{
		Health:  sampleResult(),
		Level:   engine.SeverityWarn,
		Verdict: "Memory is tight.",
		Summary: "Docker 3.0 GB on 8.0 GB of RAM (37% together).",
		Notes:   []string{"Only 8.0 GB free on disk."},
		Groups: []engine.SlowGroup{
			{ProcessGroup: probe.ProcessGroup{Name: "Docker", RSSBytes: 3 << 30, Count: 4}, Dev: true, Explain: "The Linux VM."},
			{ProcessGroup: probe.ProcessGroup{Name: "Finder", RSSBytes: 100 << 20, Count: 1}},
		},
		Swap: engine.SwapHistory{Days: 7, Samples: 5, Peak: 6 << 30, PeakAt: now.Add(-24 * time.Hour), Low: 1 << 30, Critical: 2},
	}
	res.Health.Report.Memory.WiredBytes, res.Health.Report.Memory.CompressedBytes = 1<<30, 2<<30
	var buf bytes.Buffer
	if err := renderSlow(&buf, res, now); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	mustContain(t, out,
		"Is memory slowing it down?\n  ! Memory is tight.\n    Biggest users: Docker 3.0 GB on 8.0 GB of RAM (37% together).",
		"3.0 GB in use now · peak 6.0 GB yesterday 15:00 · lowest 1.0 GB (5 checks, last 7 days)",
		"Memory pressure was critical in 2 of those checks.",
		"1.0 GB used by macOS itself (wired) · 2.0 GB compressed · 43% free",
		"  Docker                      3.0 GB  4 processes\n    The Linux VM.\n",
		"  Finder                      100 MB\n",
		"Sizes are resident memory.",
		"Also\n  ! Only 8.0 GB free on disk.",
		"snapshots: tmutil: exit 1",
		"it never quits or changes apps")
	for _, l := range strings.Split(out, "\n") {
		if strings.HasSuffix(l, " ") {
			t.Errorf("trailing space: %q", l)
		}
	}

	// One sample: no history yet.
	res.Swap = engine.SwapHistory{Days: 7, Samples: 1}
	buf.Reset()
	_ = renderSlow(&buf, res, now)
	mustContain(t, buf.String(), "3.0 GB in use now. History builds up each time macdoc checks your Mac.")
}

func TestSlowCommand(t *testing.T) {
	cliFixture(t) // the fake runner fails every command: everything is unknown
	out, err := run(t, "slow")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Memory pressure couldn't be read", "Could not measure", "Nothing has been changed.")

	out, err = run(t, "slow", "--json", "--days", "30")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Command string            `json:"command"`
		Data    engine.SlowResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if env.Command != "doctor slow" || env.Data.Swap.Days != 30 {
		t.Errorf("envelope = %+v", env)
	}
	if _, err := run(t, "slow", "--days", "0"); err == nil {
		t.Error("--days 0 accepted")
	}
}
