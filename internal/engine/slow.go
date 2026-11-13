package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/probe"
	"github.com/sagnik26/macdoc/internal/store"
)

// SlowOptions configures Slow.
type SlowOptions struct {
	Home      string
	StorePath string // "" = store.DefaultPath(Home)
	Runner    platform.Runner
	Now       time.Time
	Days      int // swap history window; 0 = 7
}

// SlowGroup is one process group with what it is.
type SlowGroup struct {
	probe.ProcessGroup
	Dev     bool   `json:"dev"`               // a developer tool macdoc knows
	Explain string `json:"explain,omitempty"` // why it uses memory
}

// SwapHistory summarizes the stored samples.
type SwapHistory struct {
	Days     int       `json:"days"`
	Samples  int       `json:"samples"` // checks recorded in the window, this one included
	Since    time.Time `json:"since,omitempty"`
	Peak     int64     `json:"peak_bytes"`
	PeakAt   time.Time `json:"peak_at,omitempty"`
	Low      int64     `json:"low_bytes"`
	Critical int       `json:"critical_samples"` // samples with critical pressure
}

// SlowResult is the data behind `macdoc doctor slow`. It only explains; it
// never acts on a process.
type SlowResult struct {
	Health  DoctorResult `json:"health"`
	Level   Severity     `json:"level"` // critical, warn or info
	Verdict string       `json:"verdict"`
	Summary string       `json:"summary,omitempty"` // the biggest users against RAM size
	Notes   []string     `json:"notes,omitempty"`   // other things that slow a Mac down, e.g. low disk
	Groups  []SlowGroup  `json:"groups"`
	Tips    []SlowTip    `json:"tips,omitempty"` // with --tips: what to do about the big users
	Swap    SwapHistory  `json:"swap_history"`
	// Alerts lists zombie parents and busy processes; absent when ps failed.
	Alerts   *probe.ProcessAlerts `json:"alerts,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
}

// slowGroups is how many process groups slow lists.
const slowGroups = 10

// vitalsEvery is the shortest gap between two recorded samples.
const vitalsEvery = 5 * time.Minute

// groupInfo explains well-known groups. Keys are probe group names.
var groupInfo = map[string]struct {
	dev     bool
	explain string
}{
	"Docker":                 {true, "Docker Desktop's Linux VM keeps the memory it has used, up to the limit in Settings › Resources, even while containers are idle."},
	"Virtual machine":        {true, "A Linux VM (Docker Desktop, OrbStack, Colima or UTM). It keeps the memory it has used until it is stopped."},
	"Virtual machine (QEMU)": {true, "A QEMU virtual machine (Colima, Lima or UTM). It holds its whole memory setting while it runs."},
	"OrbStack":               {true, "OrbStack's Linux machines and containers."},
	"Android Emulator":       {true, "Each running emulator holds its RAM setting (often 2–4 GB) until it is closed."},
	"Android Studio":         {true, "The IDE is a Java process with its own memory limit; Gradle daemons run beside it."},
	"iOS Simulator":          {true, "Booted simulators keep running in the background until they are shut down, even with the Simulator window closed."},
	"Xcode":                  {true, "Indexing (SourceKit) and builds use a lot of memory in large projects."},
	"Node.js":                {true, "Dev servers, bundlers, language servers and tools like Claude Code. Each open project can add more."},
	"Ollama":                 {true, "A loaded model stays in memory for a few minutes after its last request; `ollama ps` shows it."},
	"java":                   {true, "Java processes: often Gradle daemons, which stay after a build (`./gradlew --stop` ends them), or a build server."},
	"Google Chrome":          {false, "Each tab, extension and helper is its own process."},
	"Microsoft Edge":         {false, "Each tab, extension and helper is its own process."},
	"Brave Browser":          {false, "Each tab, extension and helper is its own process."},
	"Arc":                    {false, "Each tab, extension and helper is its own process."},
	"Safari":                 {false, "Each tab is its own process."},
	"Firefox":                {false, "Each group of tabs is its own process."},
	"Cursor":                 {true, "Built on Chromium: each window and extension host is its own process."},
	"Code":                   {true, "VS Code is built on Chromium: each window and extension host is its own process."},
	"Visual Studio Code":     {true, "Built on Chromium: each window and extension host is its own process."},
	"Slack":                  {false, "Built on Chromium, like a browser."},
	"Discord":                {false, "Built on Chromium, like a browser."},
	"Microsoft Teams":        {false, "Built on a browser engine, like a browser."},
	"claude":                 {true, "Claude Code sessions."},
	"Claude":                 {false, "The Claude desktop app."},
	"WindowServer":           {false, "macOS draws the screen; it grows with displays, windows and their size."},
	"kernel_task":            {false, "macOS itself."},
	"mds_stores":             {false, "Spotlight's index. It is busy for a while after many files change, such as a big build or install."},
	"mdworker_shared":        {false, "Spotlight reading files to index them. It is busy for a while after many files change."},
}

// RSSNote is shown with every process list.
const RSSNote = "Sizes are resident memory. Memory shared between processes can count in more than one group, and memory already compressed or swapped out isn't included."

// Slow explains memory pressure, swap and the biggest memory users. It
// records a sample in macdoc's own store for the swap history; it changes
// nothing else.
func Slow(ctx context.Context, o SlowOptions) SlowResult {
	res := SlowFrom(doctor(ctx, o.Runner, slowGroups), o)
	r := o.Runner
	if r == nil {
		r = platform.Exec{}
	}
	if a, err := probe.CollectProcessAlerts(ctx, r); err != nil {
		res.Warnings = append(res.Warnings, "process alerts: "+err.Error())
	} else {
		res.Alerts = &a
	}
	return res
}

// SlowFrom is Slow from a health check already made (the dashboard has
// one), so nothing is probed twice.
func SlowFrom(h DoctorResult, o SlowOptions) SlowResult {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Days <= 0 {
		o.Days = 7
	}
	res := SlowResult{Health: h}
	rep := res.Health.Report
	ram := ramOf(rep)
	for i, g := range rep.TopProcesses {
		if i >= 3 && ram > 0 && g.RSSBytes*100 < ram { // under 1% of RAM
			break
		}
		info := groupInfo[g.Name]
		res.Groups = append(res.Groups, SlowGroup{ProcessGroup: g, Dev: info.dev, Explain: info.explain})
	}
	res.Level, res.Verdict = SlowVerdict(rep)
	res.Summary = SlowSummary(rep)
	if d := rep.Disk; d.ContainerTotalBytes > 0 && d.ContainerFreeBytes < lowFreeBytes && res.Level != SeverityCritical {
		res.Notes = append(res.Notes, fmt.Sprintf("Only %s free on disk. Swap and APFS need headroom; with under 10 GB free, macOS slows down and can hang under load.",
			DecimalBytes(d.ContainerFreeBytes)))
	}

	path := o.StorePath
	if path == "" {
		path = store.DefaultPath(o.Home)
	}
	samples, err := recordVitals(path, rep, o.Now, o.Now.AddDate(0, 0, -o.Days))
	if err != nil {
		res.Warnings = append(res.Warnings, "swap history: "+err.Error())
	}
	res.Swap = swapHistory(samples, o.Days)
	return res
}

// SlowVerdict is the one-line answer to "is memory why it's slow?".
func SlowVerdict(rep probe.Report) (Severity, string) {
	swap := rep.Swap.UsedBytes
	lowDisk := rep.Disk.ContainerTotalBytes > 0 && rep.Disk.ContainerFreeBytes < lowFreeBytes
	switch {
	case lowDisk && swap >= highSwapBytes:
		return SeverityCritical, fmt.Sprintf("Low disk (%s free) with %s of swap in use: macOS may hang if swap can't grow.",
			DecimalBytes(rep.Disk.ContainerFreeBytes), BinaryBytes(swap))
	case rep.Memory.Pressure == probe.PressureCritical:
		return SeverityCritical, "Your Mac is short of memory right now: it is compressing and swapping heavily."
	case rep.Memory.Pressure == probe.PressureWarn:
		return SeverityWarn, "Memory is tight: macOS is compressing memory and using swap, which slows apps down."
	case swap >= highSwapBytes && rep.Memory.Pressure == probe.PressureNormal:
		return SeverityInfo, fmt.Sprintf("Memory is fine now, but %s of swap built up earlier. It shrinks as apps quit or after a restart.", BinaryBytes(swap))
	case rep.Memory.Pressure == probe.PressureNormal:
		return SeverityInfo, "Memory isn't what's slowing your Mac down right now."
	}
	return SeverityInfo, "Memory pressure couldn't be read (see warnings)."
}

// SlowSummary names the biggest users against the size of RAM, e.g.
// "Docker 3.1 GB and Android Emulator 2.4 GB on 8.0 GB of RAM".
func SlowSummary(rep probe.Report) string {
	ram := ramOf(rep)
	if ram <= 0 {
		return ""
	}
	var parts []string
	var sum int64
	for _, g := range rep.TopProcesses {
		if len(parts) == 3 || g.RSSBytes*20 < ram { // under 5% of RAM
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s", g.Name, BinaryBytes(g.RSSBytes)))
		sum += g.RSSBytes
	}
	if len(parts) == 0 {
		return ""
	}
	list := parts[0]
	if len(parts) > 1 {
		list = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
	return fmt.Sprintf("%s on %s of RAM (%d%% together).", list, BinaryBytes(ram), sum*100/ram)
}

// ramOf is the size of RAM, 0 if unknown.
func ramOf(rep probe.Report) int64 {
	if rep.Hardware.MemoryBytes > 0 {
		return rep.Hardware.MemoryBytes
	}
	return rep.Memory.TotalBytes
}

// recordVitals stores this check's sample (at most one per vitalsEvery) and
// returns the samples since since, this one included.
func recordVitals(path string, rep probe.Report, now, since time.Time) ([]store.Vitals, error) {
	cur := vitalsOf(rep, now)
	st, err := store.Open(path)
	if err != nil {
		return []store.Vitals{cur}, err
	}
	defer func() { _ = st.Close() }()
	recent, err := st.VitalsSince(now.Add(-vitalsEvery))
	if err != nil {
		return []store.Vitals{cur}, err
	}
	if len(recent) == 0 && rep.Memory.TotalBytes > 0 {
		if err := st.AddVitals(cur); err != nil {
			return []store.Vitals{cur}, err
		}
	}
	all, err := st.VitalsSince(since)
	if err != nil {
		return []store.Vitals{cur}, err
	}
	if len(recent) > 0 || rep.Memory.TotalBytes <= 0 {
		all = append(all, cur) // shown, not stored
	}
	return all, nil
}

func vitalsOf(rep probe.Report, now time.Time) store.Vitals {
	v := store.Vitals{At: now.UTC(), MemTotal: rep.Memory.TotalBytes, SwapUsed: rep.Swap.UsedBytes,
		FreePercent: rep.Memory.FreePercent, Pressure: string(rep.Memory.Pressure), DiskFree: -1}
	if rep.Disk.ContainerTotalBytes > 0 {
		v.DiskFree = rep.Disk.ContainerFreeBytes
	}
	return v
}

func swapHistory(samples []store.Vitals, days int) SwapHistory {
	h := SwapHistory{Days: days, Samples: len(samples)}
	for i, v := range samples {
		if i == 0 {
			h.Since, h.Low = v.At, v.SwapUsed
		}
		if v.SwapUsed > h.Peak || h.PeakAt.IsZero() {
			h.Peak, h.PeakAt = v.SwapUsed, v.At
		}
		h.Low = min(h.Low, v.SwapUsed)
		if v.Pressure == string(probe.PressureCritical) {
			h.Critical++
		}
	}
	return h
}
