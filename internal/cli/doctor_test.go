package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"macdoc/internal/engine"
	"macdoc/internal/model"
	"macdoc/internal/platform"
	"macdoc/internal/probe"
)

func sampleResult() engine.DoctorResult {
	rep := probe.Report{
		Hardware:     probe.Hardware{Model: "MacBookAir10,1", Chip: "Apple M1", MemoryBytes: 8 << 30, OSVersion: "26.6.2"},
		Memory:       probe.Memory{TotalBytes: 8 << 30, FreePercent: 43, Pressure: probe.PressureNormal},
		Swap:         probe.Swap{TotalBytes: 5 << 30, UsedBytes: 3 << 30},
		TopProcesses: []probe.ProcessGroup{{Name: "Cursor", RSSBytes: 1 << 30, Count: 12}, {Name: "Finder", RSSBytes: 50 << 20, Count: 1}},
		Disk: probe.Disk{ContainerTotalBytes: 245_000_000_000, ContainerFreeBytes: 8_000_000_000,
			PurgeableBytes: model.Bytes{Value: 2_900_000_000, Source: model.SourceMeasured}},
		Warnings: []string{"snapshots: tmutil: exit 1"},
	}
	return engine.DoctorResult{Report: rep, Findings: engine.Findings(rep)}
}

func TestRenderDoctor(t *testing.T) {
	var buf bytes.Buffer
	if err := renderDoctor(&buf, sampleResult()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"macdoc · MacBookAir10,1 (M1, 8.0 GB) · macOS 26.6.2",
		"237 GB / 245 GB used   8.0 GB free  (+2.9 GB purgeable)",
		"Memory     8.0 GB · pressure normal · 43% free · swap 3.0 GB used",
		"1. ‼ Low disk (8.0 GB free) while 3.0 GB of swap is in use.",
		"Cursor", "(12 processes)",
		"Could not measure", "snapshots: tmutil: exit 1",
		"Nothing has been changed.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "Finder                       50.0 MB (") {
		t.Error("single process should not show a count")
	}
}

func TestRenderDoctorUnmeasured(t *testing.T) {
	var buf bytes.Buffer
	if err := renderDoctor(&buf, engine.DoctorResult{Report: probe.Report{Memory: probe.Memory{FreePercent: -1}}}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Disk       —  unmeasured", "Memory     —", "Nothing urgent."} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "0 B free") {
		t.Error("unknown disk rendered as zero")
	}
}

func TestDoctorJSONAndRootDefault(t *testing.T) {
	old := runner
	runner = &platform.FakeRunner{} // every command errors -> warnings, never a failure
	t.Cleanup(func() { runner = old })

	for _, args := range [][]string{{"doctor", "--json"}, {"--json"}} {
		cmd := NewRootCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var env struct {
			SchemaVersion string              `json:"schema_version"`
			Command       string              `json:"command"`
			Data          engine.DoctorResult `json:"data"`
		}
		if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
			t.Fatalf("%v: bad JSON: %v\n%s", args, err, buf.String())
		}
		if env.SchemaVersion != model.SchemaVersion || env.Command != "doctor" {
			t.Errorf("%v: envelope = %+v", args, env)
		}
		if len(env.Data.Report.Warnings) == 0 {
			t.Errorf("%v: expected warnings when every probe fails", args)
		}
	}
}

func TestBarAndTruncate(t *testing.T) {
	if got := bar(50, 100, 10); got != "▕█████     ▏" {
		t.Errorf("bar = %q", got)
	}
	if got := bar(200, 100, 4); got != "▕████▏" {
		t.Errorf("overfull bar = %q", got)
	}
	if got := bar(1, 0, 3); got != "▕   ▏" {
		t.Errorf("zero-total bar = %q", got)
	}
	if got := truncate("abcdef", 4); got != "abc…" {
		t.Errorf("truncate = %q", got)
	}
}
