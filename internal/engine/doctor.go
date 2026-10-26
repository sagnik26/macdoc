package engine

import (
	"context"
	"fmt"
	"sort"

	"macdoc/internal/platform"
	"macdoc/internal/probe"
)

// Severity orders findings from most to least important.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarn     Severity = "warn"
	SeverityInfo     Severity = "info"
)

// Finding is one plain-language observation shown under "What matters most".
type Finding struct {
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail,omitempty"`
}

// DoctorResult is the data behind `macdoc doctor`.
type DoctorResult struct {
	Report   probe.Report `json:"report"`
	Findings []Finding    `json:"findings"`
}

// topProcessGroups is how many process groups doctor collects.
const topProcessGroups = 8

// Thresholds for findings.
const (
	lowFreeBytes      = 10_000_000_000 // 10 GB: APFS and swap start to struggle
	criticalFreeBytes = 5_000_000_000  // 5 GB: hangs and failed updates likely
	highSwapBytes     = 2 << 30        // 2 GiB of swap in use
	heavyGroupBytes   = 1536 << 20     // 1.5 GiB resident for one app group
)

// Doctor collects the system report. Individual probe failures become
// warnings; Doctor itself never fails.
func Doctor(ctx context.Context, r platform.Runner) DoctorResult {
	var rep probe.Report
	warn := func(section string, err error) {
		if err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: %v", section, err))
		}
	}
	var err error
	rep.Hardware, err = probe.CollectHardware(ctx, r)
	warn("hardware", err)
	rep.Memory, err = probe.CollectMemory(ctx, r)
	warn("memory", err)
	rep.Swap, err = probe.CollectSwap(ctx, r)
	warn("swap", err)
	rep.TopProcesses, err = probe.CollectProcesses(ctx, r, topProcessGroups)
	warn("processes", err)
	rep.Disk, err = probe.CollectDisk(ctx, r) // may return partial data with an error
	warn("disk", err)
	rep.Snapshots, err = probe.CollectSnapshots(ctx, r)
	warn("snapshots", err)

	return DoctorResult{Report: rep, Findings: Findings(rep)}
}

// Findings derives the "What matters most" list from a report, most severe
// first. Only facts present in the report are used; unknown sections are
// skipped rather than guessed.
func Findings(rep probe.Report) []Finding {
	var fs []Finding
	free := rep.Disk.ContainerFreeBytes
	haveDisk := rep.Disk.ContainerTotalBytes > 0
	swap := rep.Swap.UsedBytes

	if haveDisk {
		switch {
		case free < criticalFreeBytes:
			fs = append(fs, Finding{SeverityCritical,
				fmt.Sprintf("Only %s free on disk.", DecimalBytes(free)),
				"Below 5 GB, macOS updates fail and apps can hang. Run `macdoc wins` to see what can be reclaimed."})
		case free < lowFreeBytes:
			fs = append(fs, Finding{SeverityWarn,
				fmt.Sprintf("Free space is under 10 GB (%s).", DecimalBytes(free)),
				"APFS and swap need headroom; aim for 15–20 GB free."})
		}
	}

	if swap >= highSwapBytes {
		f := Finding{SeverityWarn,
			fmt.Sprintf("%s of swap in use.", BinaryBytes(swap)),
			"RAM is overcommitted; macOS is paging to disk, which slows everything down."}
		if rep.Memory.Pressure == probe.PressureNormal {
			f = Finding{SeverityInfo,
				fmt.Sprintf("%s of swap in use, but memory pressure is normal now.", BinaryBytes(swap)),
				"Swap built up earlier and macOS keeps it until apps quit or the Mac restarts. Watch it if heavy apps (Docker, emulators, local models) are running."}
		}
		if haveDisk && free < lowFreeBytes {
			f = Finding{SeverityCritical,
				fmt.Sprintf("Low disk (%s free) while %s of swap is in use.", DecimalBytes(free), BinaryBytes(swap)),
				"Swap lives on the same disk: if it can't grow, macOS may hang under load."}
		}
		fs = append(fs, f)
	}

	switch rep.Memory.Pressure {
	case probe.PressureCritical:
		fs = append(fs, Finding{SeverityCritical, "Memory pressure is critical.", "Quit heavy apps (see the top memory users below)."})
	case probe.PressureWarn:
		fs = append(fs, Finding{SeverityWarn, "Memory pressure is elevated.", "Quitting one heavy app usually helps."})
	}

	for _, g := range rep.TopProcesses {
		if g.RSSBytes >= heavyGroupBytes {
			fs = append(fs, Finding{SeverityInfo,
				fmt.Sprintf("%s is using %s of RAM.", g.Name, BinaryBytes(g.RSSBytes)),
				""})
		}
	}

	if rep.Snapshots.Count > 0 {
		fs = append(fs, Finding{SeverityInfo,
			fmt.Sprintf("%d local Time Machine snapshot(s) present.", rep.Snapshots.Count),
			"They can hold space that deleted files would otherwise free; macOS thins them when space runs low."})
	}

	if p := rep.Disk.PurgeableBytes; p.Value > 0 {
		fs = append(fs, Finding{SeverityInfo,
			fmt.Sprintf("%s is purgeable.", DecimalBytes(p.Value)),
			"macOS can reclaim it automatically (caches, iCloud files) when space is needed."})
	}

	rank := map[Severity]int{SeverityCritical: 0, SeverityWarn: 1, SeverityInfo: 2}
	sort.SliceStable(fs, func(i, j int) bool { return rank[fs[i].Severity] < rank[fs[j].Severity] })
	return fs
}

// DecimalBytes formats disk sizes the way Finder does (1 GB = 10^9 bytes).
func DecimalBytes(n int64) string { return formatBytes(n, 1000, []string{"B", "KB", "MB", "GB", "TB"}) }

// BinaryBytes formats RAM and swap sizes (1 GB = 2^30 bytes), matching how
// macOS reports memory.
func BinaryBytes(n int64) string { return formatBytes(n, 1024, []string{"B", "KB", "MB", "GB", "TB"}) }

func formatBytes(n int64, base float64, units []string) string {
	if n < 0 {
		return "-" + formatBytes(-n, base, units)
	}
	v := float64(n)
	i := 0
	for v >= base && i < len(units)-1 {
		v /= base
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[0])
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
