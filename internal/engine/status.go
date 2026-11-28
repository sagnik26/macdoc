package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/probe"
)

// statusInterval is how long the network rate is measured over by default.
const statusInterval = time.Second

// StatusOptions controls one live status reading.
type StatusOptions struct {
	Runner   platform.Runner // default platform.Exec{}
	Interval time.Duration   // how long traffic is measured over; default 1s
}

// StatusResult is the data behind `macdoc vitals`: what the Mac is
// doing right now. Each live section is a pointer that is absent when its
// probe failed, so an unknown value is never shown as zero.
type StatusResult struct {
	// Report is the hardware, memory, swap, disk and top-process snapshot
	// that `macdoc vitals` shows.
	Report  probe.Report        `json:"report"`
	CPU     *probe.CPU          `json:"cpu,omitempty"`
	DiskIO  *probe.DiskIO       `json:"disk_io,omitempty"`
	Network *probe.NetworkRates `json:"network,omitempty"`
	Power   *probe.Power        `json:"power,omitempty"`
	// Alerts lists zombie parents and busy processes; absent when ps failed.
	Alerts *probe.ProcessAlerts `json:"alerts,omitempty"`
	// Score is a 0 to 100 health score; Reasons lists what lowered it, each
	// with its points. It is a summary for reading, not a guarantee.
	Score    int      `json:"score"`
	Reasons  []string `json:"reasons"`
	Warnings []string `json:"warnings,omitempty"`
}

// Status reads the live state of the Mac. Every probe is read-only and runs
// at the same time, so a reading takes about as long as its slowest probe
// (a couple of seconds). A probe that fails leaves its section out and adds a
// warning; it never fails the whole reading.
func Status(ctx context.Context, o StatusOptions) StatusResult {
	if o.Runner == nil {
		o.Runner = platform.Exec{}
	}
	if o.Interval <= 0 {
		o.Interval = statusInterval
	}
	var (
		res StatusResult
		mu  sync.Mutex
		wg  sync.WaitGroup
	)
	warn := func(what string, err error) {
		mu.Lock()
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", what, err))
		mu.Unlock()
	}
	run := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}
	run(func() {
		d := Doctor(ctx, o.Runner)
		mu.Lock()
		res.Report = d.Report
		mu.Unlock()
	})
	run(func() {
		if c, err := probe.CollectCPU(ctx, o.Runner); err != nil {
			warn("cpu", err)
		} else {
			mu.Lock()
			res.CPU = &c
			mu.Unlock()
		}
	})
	run(func() {
		if d, err := probe.CollectDiskIO(ctx, o.Runner); err != nil {
			warn("disk traffic", err)
		} else {
			mu.Lock()
			res.DiskIO = &d
			mu.Unlock()
		}
	})
	run(func() {
		if n, err := probe.CollectNetwork(ctx, o.Runner, o.Interval); err != nil {
			warn("network", err)
		} else {
			mu.Lock()
			res.Network = &n
			mu.Unlock()
		}
	})
	run(func() {
		if p, err := probe.CollectPower(ctx, o.Runner); err != nil {
			warn("power", err)
		} else {
			mu.Lock()
			res.Power = &p
			mu.Unlock()
		}
	})
	run(func() {
		if a, err := probe.CollectProcessAlerts(ctx, o.Runner); err != nil {
			warn("process alerts", err)
		} else {
			mu.Lock()
			res.Alerts = &a
			mu.Unlock()
		}
	})
	wg.Wait()
	res.Warnings = append(res.Warnings, res.Report.Warnings...)
	res.Score, res.Reasons = HealthScore(res)
	return res
}

// deduction is points taken off the score, and why.
type deduction struct {
	points int
	why    string
}

// HealthScore turns a status reading into a 0 to 100 score and the reasons it
// is below 100. Only measured values count: a section that is absent takes
// nothing off. The rules are fixed so the same reading always scores the same.
func HealthScore(s StatusResult) (int, []string) {
	score := 100
	reasons := []string{}
	for _, rule := range []func(StatusResult) []deduction{diskRule, memoryRule, swapRule, cpuRule, batteryRule} {
		for _, d := range rule(s) {
			score -= d.points
			reasons = append(reasons, fmt.Sprintf("-%d %s", d.points, d.why))
		}
	}
	return max(0, score), reasons
}

func diskRule(s StatusResult) []deduction {
	d := s.Report.Disk
	if d.ContainerTotalBytes <= 0 {
		return nil
	}
	free := float64(d.ContainerFreeBytes) / float64(d.ContainerTotalBytes)
	switch {
	case free < 0.10:
		return []deduction{{25, fmt.Sprintf("disk is almost full (%.0f%% free)", free*100)}}
	case free < 0.20:
		return []deduction{{10, fmt.Sprintf("disk is getting full (%.0f%% free)", free*100)}}
	}
	return nil
}

func memoryRule(s StatusResult) []deduction {
	switch s.Report.Memory.Pressure {
	case probe.PressureCritical:
		return []deduction{{25, "memory pressure is critical"}}
	case probe.PressureWarn:
		return []deduction{{10, "memory pressure is warn"}}
	}
	return nil
}

func swapRule(s StatusResult) []deduction {
	total := s.Report.Memory.TotalBytes
	if total <= 0 {
		return nil
	}
	used := float64(s.Report.Swap.UsedBytes) / float64(total)
	switch {
	case used > 0.5:
		return []deduction{{10, fmt.Sprintf("swap use is %.0f%% of RAM", used*100)}}
	case used > 0.25:
		return []deduction{{5, fmt.Sprintf("swap use is %.0f%% of RAM", used*100)}}
	}
	return nil
}

func cpuRule(s StatusResult) []deduction {
	c := s.CPU
	if c == nil {
		return nil
	}
	var out []deduction
	switch {
	case c.UsagePercent > 90:
		out = append(out, deduction{10, fmt.Sprintf("CPU is %.0f%% busy", c.UsagePercent)})
	case c.UsagePercent > 75:
		out = append(out, deduction{5, fmt.Sprintf("CPU is %.0f%% busy", c.UsagePercent)})
	}
	if c.LogicalCores > 0 {
		switch per := c.Load1 / float64(c.LogicalCores); {
		case per > 2:
			out = append(out, deduction{10, fmt.Sprintf("load is %.1f per core", per)})
		case per > 1:
			out = append(out, deduction{5, fmt.Sprintf("load is %.1f per core", per)})
		}
	}
	return out
}

func batteryRule(s StatusResult) []deduction {
	p := s.Power
	if p == nil || !p.Present {
		return nil
	}
	var out []deduction
	if p.HealthPercent >= 0 && p.HealthPercent < 80 {
		out = append(out, deduction{10, fmt.Sprintf("battery health is %d%%", p.HealthPercent)})
	}
	if p.CycleCount > 1000 {
		out = append(out, deduction{5, fmt.Sprintf("battery has %d charge cycles", p.CycleCount)})
	}
	return out
}
