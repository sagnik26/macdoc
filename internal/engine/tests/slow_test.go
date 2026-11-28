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

func slowRunner(t *testing.T, ps string) *platform.Fake {
	return &platform.Fake{Responses: map[string]platform.Result{
		"vm_stat":              probeFixture(t, "vm_stat.txt"),
		"sysctl -n hw.memsize": {Stdout: []byte("8589934592\n")},
		"memory_pressure -Q":   probeFixture(t, "memory_pressure_Q.txt"),
		"sysctl -n kern.memorystatus_vm_pressure_level": {Stdout: []byte("2\n")}, // warn
		"sysctl -n vm.swapusage":                        probeFixture(t, "swapusage.txt"),
		"ps -axo rss=,comm=":                            {Stdout: []byte(ps)},
		"ps -axo pid=,ppid=,stat=,pcpu=,comm=":          {Stdout: []byte("1 0 Ss 0.0 /sbin/launchd\n")},
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
	// slow reuses the doctor's memory list (one rss listing); the alerts need
	// process state and CPU, which is a second, different ps call.
	n, alerts := 0, 0
	for _, c := range r.Calls {
		switch {
		case c.Name == "ps" && len(c.Args) > 1 && c.Args[1] == "rss=,comm=":
			n++
		case c.Name == "ps":
			alerts++
		}
	}
	if n != 1 || alerts != 1 {
		t.Errorf("rss listings %d, alert listings %d; want 1 and 1", n, alerts)
	}
	if res.Alerts == nil {
		t.Error("slow should carry the process alerts")
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

// SlowChecks reports swap and free disk against their limits, and says
// "unknown" for a disk it could not read instead of showing zero.
func TestSlowChecks(t *testing.T) {
	rep := probe.Report{Swap: probe.Swap{UsedBytes: 3 << 30},
		Disk: probe.Disk{ContainerTotalBytes: 500e9, ContainerFreeBytes: 8e9}}
	got := engine.SlowChecks(rep)
	if len(got) != 2 || !got[0].Tripped || got[0].Flag != "high" || !got[1].Tripped || got[1].Flag != "low" {
		t.Fatalf("both should be tripped: %+v", got)
	}
	rep = probe.Report{Swap: probe.Swap{UsedBytes: 1 << 30}, Disk: probe.Disk{ContainerTotalBytes: 500e9, ContainerFreeBytes: 200e9}}
	if got := engine.SlowChecks(rep); got[0].Tripped || got[1].Tripped {
		t.Errorf("a quiet Mac trips nothing: %+v", got)
	}
	if got := engine.SlowChecks(probe.Report{}); got[1].Value != "unknown" || got[1].Tripped {
		t.Errorf("an unreadable disk is unknown, not zero or low: %+v", got[1])
	}
}

// Each check says which of the gauge's three stops its value is on: swap is
// comfortable below half its limit, rising up to it, high from it; free disk is
// plentiful from twice its limit, getting low down to it, low under it; an
// unreadable disk is on no stop.
func TestSlowCheckZones(t *testing.T) {
	zones := func(swap, free int64) (int, int) {
		got := engine.SlowChecks(probe.Report{Swap: probe.Swap{UsedBytes: swap},
			Disk: probe.Disk{ContainerTotalBytes: 500e9, ContainerFreeBytes: free}})
		return got[0].Zone, got[1].Zone
	}
	for _, c := range []struct {
		swap, free int64
		wantSwap   int
		wantDisk   int
	}{
		{0, 200e9, 0, 0},
		{(1 << 30) - 1, 20e9, 0, 0},
		{1 << 30, 19_999_999_999, 1, 1},
		{(2 << 30) - 1, 10e9, 1, 1},
		{2 << 30, 9_999_999_999, 2, 2},
		{8 << 30, 0, 2, 2},
	} {
		if sw, dk := zones(c.swap, c.free); sw != c.wantSwap || dk != c.wantDisk {
			t.Errorf("swap %d, free %d: zones %d, %d; want %d, %d", c.swap, c.free, sw, dk, c.wantSwap, c.wantDisk)
		}
	}
	if got := engine.SlowChecks(probe.Report{}); got[1].Zone != -1 {
		t.Errorf("an unreadable disk is on no stop: %+v", got[1])
	}
}
