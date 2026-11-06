package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"

	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/probe"
)

func probeFixture(t *testing.T, name string) platform.Result {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "probe", "tests", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return platform.Result{Stdout: b}
}

func slowRunner(t *testing.T, ps string) *platform.FakeRunner {
	return &platform.FakeRunner{Responses: map[string]platform.Result{
		"vm_stat":              probeFixture(t, "vm_stat.txt"),
		"sysctl -n hw.memsize": {Stdout: []byte("8589934592\n")},
		"memory_pressure -Q":   probeFixture(t, "memory_pressure_Q.txt"),
		"sysctl -n kern.memorystatus_vm_pressure_level": {Stdout: []byte("2\n")}, // warn
		"sysctl -n vm.swapusage":                        probeFixture(t, "swapusage.txt"),
		"ps -axo rss=,comm=":                            {Stdout: []byte(ps)},
	}}
}

func TestSlow(t *testing.T) {
	home := t.TempDir()
	ps := "3145728 /System/Library/Frameworks/Virtualization.framework/XPCServices/com.apple.Virtualization.VirtualMachine.xpc/Contents/MacOS/com.apple.Virtualization.VirtualMachine\n" +
		"2516582 /Users/me/Library/Android/sdk/emulator/qemu/darwin-aarch64/qemu-system-aarch64\n" +
		"204800 /usr/sbin/somedaemon\n"
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	r := slowRunner(t, ps)
	res := engine.Slow(context.Background(), engine.SlowOptions{Home: home, Runner: r, Now: now})

	if res.Level != engine.SeverityWarn || !strings.Contains(res.Verdict, "Memory is tight") {
		t.Errorf("verdict = %s %q", res.Level, res.Verdict)
	}
	if want := "Virtual machine 3.0 GB and Android Emulator 2.4 GB on 8.0 GB of RAM (67% together)."; res.Summary != want {
		t.Errorf("summary = %q, want %q", res.Summary, want)
	}
	if len(res.Groups) != 3 || !res.Groups[0].Dev || res.Groups[0].Explain == "" || res.Groups[2].Explain != "" {
		t.Errorf("groups = %+v", res.Groups)
	}
	// ps runs once: slow reuses the doctor's process list.
	n := 0
	for _, c := range r.Calls {
		if c.Name == "ps" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("ps ran %d times", n)
	}
	if res.Swap.Samples != 1 || res.Swap.Peak != res.Health.Report.Swap.UsedBytes {
		t.Errorf("swap history = %+v", res.Swap)
	}

	// A second check within five minutes isn't stored but is shown; a later
	// one is stored, and the history covers both.
	res = engine.Slow(context.Background(), engine.SlowOptions{Home: home, Runner: slowRunner(t, ps), Now: now.Add(time.Minute)})
	if res.Swap.Samples != 2 {
		t.Errorf("samples after a quick recheck = %d, want 2", res.Swap.Samples)
	}
	res = engine.Slow(context.Background(), engine.SlowOptions{Home: home, Runner: slowRunner(t, ps), Now: now.Add(time.Hour)})
	if res.Swap.Samples != 2 || !res.Swap.Since.Equal(now) || len(res.Warnings) != 0 {
		t.Errorf("history = %+v, warnings %v", res.Swap, res.Warnings)
	}
}

func TestSlowVerdict(t *testing.T) {
	disk := probe.Disk{ContainerTotalBytes: 245 * gb, ContainerFreeBytes: 60 * gb}
	low := probe.Disk{ContainerTotalBytes: 245 * gb, ContainerFreeBytes: 6 * gb}
	for _, c := range []struct {
		rep   probe.Report
		level engine.Severity
		want  string
	}{
		{probe.Report{Disk: low, Swap: probe.Swap{UsedBytes: 6 << 30}, Memory: probe.Memory{Pressure: probe.PressureNormal}}, engine.SeverityCritical, "Low disk (6.0 GB free) with 6.0 GB of swap"},
		{probe.Report{Disk: disk, Memory: probe.Memory{Pressure: probe.PressureCritical}}, engine.SeverityCritical, "short of memory"},
		{probe.Report{Disk: disk, Swap: probe.Swap{UsedBytes: 3 << 30}, Memory: probe.Memory{Pressure: probe.PressureNormal}}, engine.SeverityInfo, "3.0 GB of swap built up earlier"},
		{probe.Report{Disk: disk, Memory: probe.Memory{Pressure: probe.PressureNormal}}, engine.SeverityInfo, "isn't what's slowing"},
		{probe.Report{Disk: disk, Memory: probe.Memory{Pressure: probe.PressureUnknown}}, engine.SeverityInfo, "couldn't be read"},
	} {
		level, got := engine.SlowVerdict(c.rep)
		if level != c.level || !strings.Contains(got, c.want) {
			t.Errorf("verdict = %s %q, want %s %q", level, got, c.level, c.want)
		}
	}
}

func TestSlowSummary(t *testing.T) {
	ram := int64(16 << 30)
	rep := probe.Report{Hardware: probe.Hardware{MemoryBytes: ram}, TopProcesses: []probe.ProcessGroup{
		{Name: "A", RSSBytes: 4 << 30}, {Name: "B", RSSBytes: 2 << 30}, {Name: "C", RSSBytes: 1 << 30}, {Name: "D", RSSBytes: 1 << 30}}}
	if got, want := engine.SlowSummary(rep), "A 4.0 GB, B 2.0 GB and C 1.0 GB on 16.0 GB of RAM (43% together)."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	rep.TopProcesses = []probe.ProcessGroup{{Name: "small", RSSBytes: 100 << 20}}
	if got := engine.SlowSummary(rep); got != "" {
		t.Errorf("small users summarized: %q", got)
	}
	if got := engine.SlowSummary(probe.Report{}); got != "" {
		t.Errorf("unknown RAM summarized: %q", got)
	}
}
