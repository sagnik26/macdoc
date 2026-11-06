package tests

import (
	"bytes"
	"encoding/json"
	"github.com/sagnik26/macdoc/internal/cli"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/probe"
	"github.com/sagnik26/macdoc/internal/testutil/fstree"
)

func TestOverviewCommand(t *testing.T) {
	cliFixture(t) // fixture store already has a complete scan, so no scan runs
	for _, args := range [][]string{nil, {"doctor"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		mustContain(t, out, "scan 2h ago", "What matters most", "Where your space is (Data volume, 100 GB measured)",
			"Reclaimable", "Free & safe", "npm cache 3.0 GB", "Worth considering", "5 × Stale node_modules folder 15.0 GB",
			"Your call", "size unknown", "macdoc doctor scan (rescan + full report)", "Nothing has been changed.")
		if strings.Contains(out, "is old. Run") {
			t.Errorf("%v: 2h-old scan flagged stale", args)
		}
	}
}

func TestOverviewJSON(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "doctor", "--json")
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
	if env.Command != "doctor" || env.Data.Scan.ID == "" || env.Data.Scanned || len(env.Data.Opportunities.Opportunities) == 0 {
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

func TestDoctorSectionsExist(t *testing.T) {
	cmd := cli.NewRootCmd()
	doctor, _, err := cmd.Find([]string{"doctor"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"health", "space", "storage", "wins", "dev", "explain", "scan"} {
		if c, _, err := doctor.Find([]string{s}); err != nil || c == doctor {
			t.Errorf("section %q missing", s)
		}
	}
	for _, gone := range []string{"wins", "storage", "dev", "explain", "scan"} {
		if c, _, _ := cmd.Find([]string{gone}); c != cmd {
			t.Errorf("top-level %q should be gone (it lives under doctor)", gone)
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

	out, err := run(t, "doctor", "scan")
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

	out, err = run(t, "doctor", "scan", "--json", "--foreground", "--workers", "2")
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
	if env.Command != "doctor scan" || !env.Data.Overview.Scanned || env.Data.Overview.ScanSummary == nil ||
		len(env.Data.Overview.Opportunities.Opportunities) == 0 || len(env.Data.Space.Categories) == 0 || len(env.Data.Dev.Toolchains) == 0 {
		t.Errorf("full JSON = %+v", env.Data)
	}
	for _, gone := range [][]string{{"doctor", "--full"}, {"doctor", "--scan"}, {"--scan"}, {"--no-color"}, {"--verbose"}} {
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
