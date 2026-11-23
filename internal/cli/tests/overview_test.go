package tests

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/probe"
	"github.com/sagnik26/macdoc/internal/testutil/fstree"
)

func TestOverviewCommand(t *testing.T) {
	cliFixture(t) // fixture store already has a complete scan, so no scan runs
	for _, args := range [][]string{nil} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		mustContain(t, out, "scan 2h ago", "What matters most", "Where your space is (Data volume, 100 GB measured)",
			"Reclaimable", "Free & safe", "npm cache 3.0 GB", "Worth considering", "5 × Stale node_modules folder 15.0 GB",
			"Your call", "size unknown", "macdoc scan (rescan + full report)", "Nothing has been changed.")
		if strings.Contains(out, "is old. Run") {
			t.Errorf("%v: 2h-old scan flagged stale", args)
		}
	}
}

func TestOverviewJSON(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Command string                `json:"command"`
		Data    engine.OverviewResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if env.Command != "overview" || env.Data.Scan.ID == "" || env.Data.Scanned || len(env.Data.Opportunities.Opportunities) == 0 {
		t.Errorf("overview JSON = %+v", env.Data)
	}
}

func TestRenderOverviewStaleAndEmpty(t *testing.T) {
	var buf bytes.Buffer
	res := engine.OverviewResult{
		Health:     engine.DoctorResult{Report: probe.Report{Memory: probe.Memory{FreePercent: -1}}},
		Scan:       model.ScanInfo{TotalBytes: 50_000_000_000},
		ScanAge:    "3d ago",
		ScanStale:  true,
		Scanned:    false,
		Warnings:   []string{"x: skipped"},
		DataVolume: model.Bytes{Value: 80_000_000_000, Source: model.SourceTool},
	}
	if err := cli.RenderOverview(&buf, res, nil); err != nil {
		t.Fatal(err)
	}
	mustContain(t, buf.String(), "scan 3d ago", "Nothing to reclaim", "30.0 GB not visible", "the scan is 3d old", "1 note(s)")

	buf.Reset()
	res.Scanned, res.ScanStale, res.TopLevel = true, false, []model.Node{{Path: "/Users", Bytes: 1}}
	if err := cli.RenderOverview(&buf, res, nil); err != nil {
		t.Fatal(err)
	}
	mustContain(t, buf.String(), "scanned just now", "/Users 1 B")
}

// The command set is reclaim, retire, explore, vitals and scan, with the helpers
// under "More"; the old doctor, plan, apply and refresh commands are gone.
func TestCommandSet(t *testing.T) {
	cmd := cli.NewRootCmd()
	main := map[string]bool{}
	more := map[string]bool{}
	for _, c := range cmd.Commands() {
		switch c.GroupID {
		case "main":
			main[c.Name()] = true
		case "more":
			more[c.Name()] = true
		}
	}
	for _, want := range []string{"reclaim", "retire", "explore", "vitals", "scan"} {
		if !main[want] {
			t.Errorf("headline command %q missing", want)
		}
	}
	if len(main) != 5 {
		t.Errorf("headline commands = %v; want exactly reclaim, retire, explore, vitals and scan", main)
	}
	for _, want := range []string{"quarantine", "log", "config", "mcp", "schedule", "version"} {
		if !more[want] {
			t.Errorf("%q missing from the More group", want)
		}
	}
	for _, gone := range []string{"doctor", "plan", "wins", "dev", "space", "health", "slow", "trend", "projects", "software", "status", "maintenance"} {
		if c, _, _ := cmd.Find([]string{gone}); c != cmd {
			t.Errorf("%q should be gone", gone)
		}
	}
}

func TestDoctorScanFullReport(t *testing.T) {
	cliFixture(t)
	root := t.TempDir()
	fstree.Build(t, root, fstree.File("Users/a/big.bin", 2<<20))
	old := cli.ScanRoot
	cli.ScanRoot = root
	t.Cleanup(func() { cli.ScanRoot = old })

	out, err := run(t, "scan")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "scanned just now", "Reclaimable", "══════ SCAN", "Scan S-", "══════ HEALTH", "Could not measure",
		"══════ SPACE", `"System Data", decoded`, "Large items with no recipe", "══════ ALL SUGGESTIONS", "O-", "══════ BY TOOLCHAIN")
	if strings.Count(out, "Stale node_modules folder ·") < 5 {
		t.Errorf("full report should list every item ungrouped:\n%s", out)
	}
	if strings.Contains(out, "--full") {
		t.Error("--full no longer exists")
	}

	out, err = run(t, "scan", "--json", "--foreground", "--workers", "2")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Command string         `json:"command"`
		Data    cli.FullResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if env.Command != "scan" || !env.Data.Overview.Scanned || env.Data.Overview.ScanSummary == nil ||
		len(env.Data.Overview.Opportunities.Opportunities) == 0 || len(env.Data.Space.Categories) == 0 || len(env.Data.Dev.Toolchains) == 0 {
		t.Errorf("full JSON = %+v", env.Data)
	}
	for _, gone := range [][]string{{"--scan"}, {"--no-color"}, {"--verbose"}} {
		if _, err := run(t, gone...); err == nil {
			t.Errorf("%v should be rejected (flag removed)", gone)
		}
	}
}

func TestRenderFullWithoutFreshScan(t *testing.T) {
	var buf bytes.Buffer
	res := engine.OverviewResult{Health: engine.DoctorResult{Report: probe.Report{Memory: probe.Memory{FreePercent: -1}}},
		Scan: model.ScanInfo{ID: "S-OLD"}, ScanAge: "2h ago"}
	if err := cli.RenderFull(&buf, res, engine.StorageResult{}, cli.DevResult{}, nil); err != nil {
		t.Fatal(err)
	}
	mustContain(t, buf.String(), "Using scan S-OLD from 2h ago.")
}

func TestOverviewReportsQuarantine(t *testing.T) {
	var buf bytes.Buffer
	res := engine.OverviewResult{Health: engine.DoctorResult{Report: probe.Report{Memory: probe.Memory{FreePercent: -1}}},
		Scan: model.ScanInfo{ID: "S-1"}, ScanAge: "1h ago",
		Quarantine: &engine.QuarantineNote{Entries: 2, HeldBytes: 12_300_000_000, ExpiredCount: 1, ExpiredBytes: 800_000_000}}
	if err := cli.RenderOverview(&buf, res, nil); err != nil {
		t.Fatal(err)
	}
	mustContain(t, buf.String(), "Quarantine 2 entries · 12.3 GB held, restorable (macdoc quarantine)")
	mustContain(t, buf.String(), "800 MB ready to purge: macdoc quarantine purge --expired")
	mustContain(t, buf.String(), "Nothing has been changed.")
}
