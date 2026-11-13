package tests

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/probe"
)

// A reading with nothing wrong scores 100 and gives no reasons (an empty
// list, not null). A reading with no sections at all takes nothing off:
// what is not measured is not held against the Mac.
func TestHealthScoreCleanAndEmpty(t *testing.T) {
	clean := engine.StatusResult{
		Report: probe.Report{
			Disk:   probe.Disk{ContainerTotalBytes: 1000, ContainerFreeBytes: 500},
			Memory: probe.Memory{TotalBytes: 8 << 30, Pressure: probe.PressureNormal},
			Swap:   probe.Swap{UsedBytes: 1 << 30},
		},
		CPU:   &probe.CPU{UsagePercent: 20, Load1: 1, LogicalCores: 8},
		Power: &probe.Power{Present: true, HealthPercent: 95, CycleCount: 100},
	}
	for name, s := range map[string]engine.StatusResult{"clean": clean, "empty": {}} {
		score, reasons := engine.HealthScore(s)
		if score != 100 || reasons == nil || len(reasons) != 0 {
			t.Errorf("%s: score %d reasons %v; want 100 and an empty list", name, score, reasons)
		}
	}
}

// Each rule takes off its points, and the reasons say which and how many.
func TestHealthScoreRules(t *testing.T) {
	for _, c := range []struct {
		name  string
		res   engine.StatusResult
		score int
		why   string
	}{
		{"disk almost full", engine.StatusResult{Report: probe.Report{Disk: probe.Disk{ContainerTotalBytes: 1000, ContainerFreeBytes: 50}}}, 75, "disk is almost full"},
		{"disk getting full", engine.StatusResult{Report: probe.Report{Disk: probe.Disk{ContainerTotalBytes: 1000, ContainerFreeBytes: 150}}}, 90, "disk is getting full"},
		{"memory critical", engine.StatusResult{Report: probe.Report{Memory: probe.Memory{Pressure: probe.PressureCritical}}}, 75, "memory pressure is critical"},
		{"memory warn", engine.StatusResult{Report: probe.Report{Memory: probe.Memory{Pressure: probe.PressureWarn}}}, 90, "memory pressure is warn"},
		{"swap heavy", engine.StatusResult{Report: probe.Report{Memory: probe.Memory{TotalBytes: 8 << 30}, Swap: probe.Swap{UsedBytes: 5 << 30}}}, 90, "swap use"},
		{"cpu busy", engine.StatusResult{CPU: &probe.CPU{UsagePercent: 95}}, 90, "CPU is 95% busy"},
		{"load high", engine.StatusResult{CPU: &probe.CPU{Load1: 20, LogicalCores: 8}}, 90, "load is 2.5 per core"},
		{"battery worn", engine.StatusResult{Power: &probe.Power{Present: true, HealthPercent: 70, CycleCount: -1}}, 90, "battery health is 70%"},
		{"battery cycles", engine.StatusResult{Power: &probe.Power{Present: true, HealthPercent: 90, CycleCount: 1200}}, 95, "1200 charge cycles"},
	} {
		score, reasons := engine.HealthScore(c.res)
		if score != c.score || len(reasons) != 1 || !strings.Contains(reasons[0], c.why) {
			t.Errorf("%s: score %d reasons %v; want %d with %q", c.name, score, reasons, c.score, c.why)
		}
	}
}

// Boundaries: exactly 20% free, and a Mac with no battery, take nothing off.
func TestHealthScoreBoundariesAndMissingBattery(t *testing.T) {
	edge := engine.StatusResult{Report: probe.Report{Disk: probe.Disk{ContainerTotalBytes: 1000, ContainerFreeBytes: 200}}}
	if score, _ := engine.HealthScore(edge); score != 100 {
		t.Errorf("exactly 20%% free scored %d, want 100", score)
	}
	desktop := engine.StatusResult{Power: &probe.Power{Present: false, HealthPercent: 10, CycleCount: 5000}}
	if score, _ := engine.HealthScore(desktop); score != 100 {
		t.Errorf("a Mac with no battery scored %d, want 100", score)
	}
}

// fakeLive answers the live probes; the health report's own commands are not
// answered, so the report carries warnings, which the status passes on.
func fakeLive(t *testing.T) *platform.Fake {
	t.Helper()
	netstat := "Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll\nen0 1500 <Link#14> a4:83:e7:00:00:01 10 0 1000 10 0 2000 0\n"
	return &platform.Fake{Responses: map[string]platform.Result{
		"top -l 2 -n 0 -s 1":                   {Stdout: []byte("CPU usage: 10.0% user, 5.0% sys, 85.0% idle\n")},
		"sysctl -n vm.loadavg":                 {Stdout: []byte("{ 1.0 1.0 1.0 }")},
		"sysctl -n hw.logicalcpu":              {Stdout: []byte("8")},
		"iostat -d -c 2 -w 1":                  {Stdout: []byte("KB/t tps MB/s\n 10.0 100 1.50\n")},
		"netstat -ib":                          {Stdout: []byte(netstat)},
		"pmset -g batt":                        {Stdout: []byte("Now drawing from 'AC Power'\n")},
		"ps -axo pid=,ppid=,stat=,pcpu=,comm=": {Stdout: []byte("1 0 Ss 0.0 /sbin/launchd\n20 1 Ss 0.0 /usr/bin/parent\n21 20 Z 0.0 (child)\n")},
	}}
}

// All sections come back when every probe works.
func TestStatusCollectsEverySection(t *testing.T) {
	res := engine.Status(context.Background(), engine.StatusOptions{Runner: fakeLive(t), Interval: 5 * time.Millisecond})
	if res.CPU == nil || res.CPU.UsagePercent != 15 || res.DiskIO == nil || res.DiskIO.MBPerSec != 1.5 ||
		res.Network == nil || res.Power == nil || res.Power.Present {
		t.Errorf("sections = cpu %+v diskio %+v network %+v power %+v", res.CPU, res.DiskIO, res.Network, res.Power)
	}
}

// One failing probe leaves only its own section out, with a warning; the rest
// are unaffected, and the section is absent rather than zero.
func TestStatusOneProbeFailingKeepsTheOthers(t *testing.T) {
	fake := fakeLive(t)
	fake.Errors = map[string]error{"top -l 2 -n 0 -s 1": os.ErrDeadlineExceeded}
	res := engine.Status(context.Background(), engine.StatusOptions{Runner: fake, Interval: 5 * time.Millisecond})
	if res.CPU != nil {
		t.Errorf("a failed CPU probe must leave the section absent: %+v", res.CPU)
	}
	if res.DiskIO == nil || res.Network == nil {
		t.Error("the other probes should still report")
	}
	found := false
	for _, w := range res.Warnings {
		if strings.HasPrefix(w, "cpu:") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v; want one starting with cpu:", res.Warnings)
	}
}

// Alerts come with the status reading, and a failed ps leaves them out with a
// warning instead of showing "all clear".
func TestStatusCarriesProcessAlerts(t *testing.T) {
	res := engine.Status(context.Background(), engine.StatusOptions{Runner: fakeLive(t), Interval: 5 * time.Millisecond})
	if res.Alerts == nil || len(res.Alerts.Zombies) != 1 || res.Alerts.Zombies[0].PID != 20 {
		t.Fatalf("alerts = %+v; want one zombie parent, pid 20", res.Alerts)
	}
	fake := fakeLive(t)
	fake.Errors = map[string]error{"ps -axo pid=,ppid=,stat=,pcpu=,comm=": os.ErrDeadlineExceeded}
	res = engine.Status(context.Background(), engine.StatusOptions{Runner: fake, Interval: 5 * time.Millisecond})
	if res.Alerts != nil {
		t.Errorf("a failed ps must leave alerts absent: %+v", res.Alerts)
	}
}
