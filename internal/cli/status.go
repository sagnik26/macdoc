package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/probe"
)

// minWatchInterval is the shortest time between snapshots in --watch mode:
// one reading itself takes about a second.
const minWatchInterval = time.Second

// meterWidth is the width of the bars in the status view.
const meterWidth = 20

// newStatusCmd builds `macdoc vitals`: what the Mac is doing now.
// Read-only. With --watch it streams a snapshot every --interval until
// interrupted, as newline-delimited JSON with --json.
func newStatusCmd(g *GlobalFlags) *cobra.Command {
	var watch bool
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Live CPU, memory, disk, network and battery, with a health score (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if interval < minWatchInterval {
				return fmt.Errorf("--interval must be at least %s", minWatchInterval)
			}
			if !watch {
				errOut := cmd.ErrOrStderr()
				opts := engine.StatusOptions{Runner: Runner}
				tty := !g.json && !g.quiet && IsTerminal(errOut)
				if tty {
					opts.Progress = func(done, total int, probe string) {
						_, _ = fmt.Fprintf(statusOut(errOut), "\r\033[K%s", ProbeStatusLine(done, total, probe))
					}
				}
				res := engine.Status(context.Background(), opts)
				if tty {
					_, _ = fmt.Fprint(errOut, "\r\033[K")
				}
				return writeStatus(cmd.OutOrStdout(), g.json, res, false)
			}
			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, stop := signal.NotifyContext(parent, os.Interrupt)
			defer stop()
			return watchStatus(ctx, cmd.OutOrStdout(), g.json, interval)
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "stream a snapshot every --interval until interrupted")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "time between snapshots with --watch")
	return cmd
}

// watchStatus writes a snapshot, waits for the interval, and repeats until ctx
// ends. Ending is not an error.
func watchStatus(ctx context.Context, w io.Writer, asJSON bool, interval time.Duration) error {
	for {
		start := time.Now()
		res := engine.Status(ctx, engine.StatusOptions{Runner: Runner})
		if ctx.Err() != nil {
			return nil
		}
		if err := writeStatus(w, asJSON, res, true); err != nil {
			return err
		}
		wait := max(0, interval-time.Since(start))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// writeStatus writes one snapshot. In a stream (watch) the JSON is one compact
// line, so each line parses on its own.
func writeStatus(w io.Writer, asJSON bool, res engine.StatusResult, stream bool) error {
	if !asJSON {
		return RenderStatus(w, res)
	}
	if !stream {
		return writeJSONWarn(w, "vitals", res, res.Warnings)
	}
	return json.NewEncoder(w).Encode(model.Envelope{
		SchemaVersion: model.SchemaVersion,
		Command:       "vitals",
		GeneratedAt:   time.Now().UTC(),
		Data:          res,
		Warnings:      res.Warnings,
	})
}

// RenderStatus writes one status snapshot in the human format.
func RenderStatus(w io.Writer, res engine.StatusResult) error {
	res = display(res) // process names come from the machine
	t := themeFor(w)
	var b strings.Builder
	b.WriteString(statusBlocks(t, res, true))
	writeNotes(&b, t, res.Warnings)
	writeAlerts(&b, t, res.Alerts)
	b.WriteString(t.done("Nothing has been changed.") + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// statusBlocks is the status body, shared with the dashboard.
func statusBlocks(t *Theme, res engine.StatusResult, withProcesses bool) string {
	return statusBlocksLive(t, res, withProcesses, nil)
}

// statusBlocksLive is statusBlocks with, when h is not nil, an arrow after
// each number for the way it moved since the last reading.
func statusBlocksLive(t *Theme, res engine.StatusResult, withProcesses bool, h *vitalsHist) string {
	tail := func(s func(*vitalsHist) []float64, eps float64, upBad bool) string {
		if h == nil {
			return ""
		}
		return liveTail(t, s(h), eps, upBad)
	}
	var b strings.Builder
	hw := res.Report.Hardware
	title := fmt.Sprintf("Health %d / 100", res.Score)
	note := strings.TrimSpace(hw.Model + " " + strings.TrimPrefix(hw.Chip, "Apple "))
	if h != nil && len(h.score) > 1 {
		note += " " + tail(func(h *vitalsHist) []float64 { return h.score }, 0.5, false)
	}
	reasons := " Nothing is holding the score down.\n"
	if len(res.Reasons) > 0 {
		reasons = " " + strings.Join(res.Reasons, "\n ") + "\n"
	}
	b.WriteString(t.block(title, "· "+note, reasons))

	rows := nowRows(res, tail)
	b.WriteString("\n" + t.block("Now", "", " "+strings.Join(rows, "\n ")+"\n"))

	if withProcesses {
		b.WriteString(processBlock(t, res))
	}
	return b.String()
}

// nowRows are the lines of the "Now" block. tail adds the arrow and history
// bar after a number (nothing when the page is not live).
func nowRows(res engine.StatusResult, tail func(func(*vitalsHist) []float64, float64, bool) string) []string {
	var rows []string
	if c := res.CPU; c != nil {
		rows = append(rows, fmt.Sprintf("CPU      %s %5.1f%%%s   load %.2f / %.2f / %.2f (%d cores)",
			meter(c.UsagePercent/100), c.UsagePercent, tail(func(h *vitalsHist) []float64 { return h.cpu }, 1, true), c.Load1, c.Load5, c.Load15, c.LogicalCores))
	}
	if m := res.Report.Memory; m.TotalBytes > 0 && m.FreePercent >= 0 {
		used := 100 - m.FreePercent
		rows = append(rows, fmt.Sprintf("Memory   %s %5d%%%s   %s of %s, pressure %s, swap %s",
			meter(float64(used)/100), used, tail(func(h *vitalsHist) []float64 { return h.mem }, 0.5, true), engine.BinaryBytes(m.TotalBytes*int64(used)/100), engine.BinaryBytes(m.TotalBytes),
			m.Pressure, engine.BinaryBytes(res.Report.Swap.UsedBytes)))
	}
	if d := res.Report.Disk; d.ContainerTotalBytes > 0 {
		used := d.ContainerTotalBytes - d.ContainerFreeBytes
		line := fmt.Sprintf("Disk     %s %5.1f%%%s   %s free of %s",
			meter(float64(used)/float64(d.ContainerTotalBytes)), float64(used)*100/float64(d.ContainerTotalBytes),
			tail(func(h *vitalsHist) []float64 { return h.disk }, 0.05, true), engine.DecimalBytes(d.ContainerFreeBytes), engine.DecimalBytes(d.ContainerTotalBytes))
		if io := res.DiskIO; io != nil {
			line += fmt.Sprintf(", %.1f MB/s", io.MBPerSec)
		}
		rows = append(rows, line)
	}
	if n := res.Network; n != nil {
		rows = append(rows, fmt.Sprintf("Network  down %s/s%s   up %s/s%s", engine.DecimalBytes(int64(n.InBytesPerSec)),
			tail(func(h *vitalsHist) []float64 { return h.down }, 1024, false),
			engine.DecimalBytes(int64(n.OutBytesPerSec)), tail(func(h *vitalsHist) []float64 { return h.up }, 1024, false)))
	}
	if p := res.Power; p != nil && p.Present {
		line := powerLine(*p)
		line = strings.Replace(line, "%   ", "%"+tail(func(h *vitalsHist) []float64 { return h.power }, 0.5, false)+"   ", 1)
		rows = append(rows, line)
	}
	if len(rows) == 0 {
		rows = []string{"Nothing could be read. See the notes below."}
	}
	return rows
}

// processBlock is the table of the biggest memory users, or "" with none.
func processBlock(t *Theme, res engine.StatusResult) string {
	if len(res.Report.TopProcesses) == 0 {
		return ""
	}
	var prows [][]string
	for _, p := range res.Report.TopProcesses {
		prows = append(prows, []string{p.Name, engine.BinaryBytes(p.RSSBytes), fmt.Sprint(p.Count)})
	}
	return "\n" + t.block("Biggest memory users", "", t.Grid(
		[]Column{{Title: "Group", Flex: true}, {Title: "Memory", Right: true}, {Title: "Processes", Right: true}}, prows, 100,
		func(_, c int) *lipgloss.Style {
			if c == 1 {
				return ptr(t.size)
			}
			return ptr(t.text)
		}))
}

// powerLine describes the battery or power source in one line.
func powerLine(p probe.Power) string {
	src := "on AC power"
	if p.OnBattery {
		src = "on battery"
	}
	line := fmt.Sprintf("Power    %s %5d%%   %s, %s", meter(float64(p.Percent)/100), p.Percent, src, p.State)
	if p.Remaining != "" {
		line += ", " + p.Remaining + " left"
	}
	var extra []string
	if p.HealthPercent >= 0 {
		extra = append(extra, fmt.Sprintf("health %d%%", p.HealthPercent))
	}
	if p.CycleCount >= 0 {
		extra = append(extra, fmt.Sprintf("%d cycles", p.CycleCount))
	}
	if p.TemperatureC > 0 {
		extra = append(extra, fmt.Sprintf("%.0f °C", p.TemperatureC))
	}
	if len(extra) > 0 {
		line += " · " + strings.Join(extra, ", ")
	}
	return line
}

// meter draws a fraction (0 to 1) as a bar of meterWidth cells.
func meter(frac float64) string {
	n := int(frac*meterWidth + 0.5)
	n = max(0, min(meterWidth, n))
	return strings.Repeat("█", n) + strings.Repeat("░", meterWidth-n)
}

// statusEvery is how long the vitals page waits between live readings.
const statusEvery = 3 * time.Second

// StatusMsg carries one live reading for the vitals page.
type StatusMsg struct{ Res engine.StatusResult }

// statusTickMsg says it is time to take the next reading.
type statusTickMsg struct{}

// status reads the live state for the dashboard. Tests replace it through
// TuiDeps.Status.
func (d *TuiDeps) status(ctx context.Context) engine.StatusResult {
	if d.Status != nil {
		return d.Status(ctx, d)
	}
	return engine.Status(ctx, engine.StatusOptions{Runner: Runner,
		Progress: func(done, total int, probe string) { d.post(StatusProgressMsg{Done: done, Total: total, Probe: probe}) }})
}

// StatusProgressMsg says how many probes of the first vitals reading have
// answered, for the bar the page shows until the reading is in.
type StatusProgressMsg struct {
	Done, Total int
	Probe       string
}

// syncStatus starts a live reading while the vitals page is open and none is
// running. Each reading schedules the next one, so readings stop on their own
// when the page closes; nothing is read anywhere else. It needs no scan.
func (m TuiModel) syncStatus(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if m.Overlay != overlayVitals || m.statusBusy || m.statusWait || m.Deps == nil {
		return m, cmd
	}
	m.statusBusy = true
	deps := m.Deps
	read := func() tea.Msg { return StatusMsg{Res: deps.status(context.Background())} }
	return m, tea.Batch(cmd, read)
}

// openVitals is /vitals: a page of its own with the live readings. It works
// from the machine as it is now, so it needs no scan.
func (m TuiModel) openVitals() (tea.Model, tea.Cmd) {
	m.Overlay, m.scroll = overlayVitals, 0
	m.statusDone, m.statusTotal, m.statusProbe, m.statusOpened = 0, 0, "", time.Now()
	m.status, m.hist, m.vitalsWhy = nil, vitalsHist{}, false // start from a fresh reading and an empty history
	if m.clockOn {
		return m, nil
	}
	m.clockOn = true
	return m, statusClock()
}

// vitalsBody is the vitals page: the live readings, and a red alert only when
// memory (or a full disk with swap) is what is slowing the Mac down.
func (m TuiModel) vitalsBody() string {
	t := m.t
	if m.status == nil {
		head := "\n  " + m.spin.View() + " " + t.text.Render("Reading CPU, memory, disk, network and battery…") + "\n\n"
		if m.statusTotal > 0 {
			tail := fmt.Sprintf("%d of %d readings", m.statusDone, m.statusTotal)
			if m.statusProbe != "" {
				tail += " · " + m.statusProbe
			}
			return head + "  " + m.barView(float64(m.statusDone)/float64(m.statusTotal), true) + "\n\n  " + t.hint(tail) + "\n"
		}
		return head + "  " + m.barView(sweep(time.Since(m.statusOpened)), false) + "\n\n  " + t.hint("starting") + "\n"
	}
	res := display(*m.status)
	var b strings.Builder
	b.WriteString("\n" + m.liveHeader() + "\n")
	alert := false
	if lvl, verdict := engine.SlowVerdict(res.Report); lvl == engine.SeverityCritical || lvl == engine.SeverityWarn {
		alert = true
		b.WriteString(t.alertBox("Memory is slowing your Mac down", verdict))
		b.WriteString("\n")
	}
	if alert || m.vitalsWhy {
		b.WriteString(m.whyBox(res) + "\n")
	}
	b.WriteString(statusBlocksLive(t, res, true, &m.hist))
	if len(res.Warnings) > 0 {
		var rows [][]string
		for _, wn := range res.Warnings {
			rows = append(rows, []string{wn})
		}
		b.WriteString("\n" + t.block("Could not measure", "", t.Grid([]Column{{Title: "What and why", Wrap: true}}, rows, max(40, m.w-4),
			func(_, _ int) *lipgloss.Style { return ptr(t.muted) })))
	}
	return b.String()
}

// alertBox is a red box with a bold title: for things that need attention now.
func (t *Theme) alertBox(title, text string) string {
	body := t.bad.Bold(true).Render("▲ "+title) + "\n" + t.text.Render(text)
	if !t.on {
		return " ! " + title + "\n   " + text + "\n"
	}
	box := t.style().Border(lipgloss.RoundedBorder()).BorderForeground(t.c(colRed)).Padding(0, 1).Render(body)
	return indentLines(box, " ") + "\n"
}

// statusRead stores a reading and schedules the next one.
func (m TuiModel) statusRead(msg StatusMsg) (tea.Model, tea.Cmd) {
	res := msg.Res
	m.status, m.statusBusy, m.statusWait = &res, false, true // wait for the tick before the next reading
	m.statusAt, m.hist = NowFunc(), m.hist.add(res)
	return m, tea.Tick(statusEvery, func(time.Time) tea.Msg { return statusTickMsg{} })
}
