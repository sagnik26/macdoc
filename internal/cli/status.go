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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// newStatusCmd builds `macdoc doctor status`: what the Mac is doing now.
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
				return writeStatus(cmd.OutOrStdout(), g.json, engine.Status(context.Background(), engine.StatusOptions{Runner: Runner}), false)
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
		return writeJSONWarn(w, "doctor status", res, res.Warnings)
	}
	return json.NewEncoder(w).Encode(model.Envelope{
		SchemaVersion: model.SchemaVersion,
		Command:       "doctor status",
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
	var b strings.Builder
	hw := res.Report.Hardware
	title := fmt.Sprintf("Health %d / 100", res.Score)
	note := strings.TrimSpace(hw.Model + " " + strings.TrimPrefix(hw.Chip, "Apple "))
	reasons := " Nothing is holding the score down.\n"
	if len(res.Reasons) > 0 {
		reasons = " " + strings.Join(res.Reasons, "\n ") + "\n"
	}
	b.WriteString(t.block(title, "· "+note, reasons))

	var rows []string
	if c := res.CPU; c != nil {
		rows = append(rows, fmt.Sprintf("CPU      %s %5.1f%%   load %.2f / %.2f / %.2f (%d cores)",
			meter(c.UsagePercent/100), c.UsagePercent, c.Load1, c.Load5, c.Load15, c.LogicalCores))
	}
	if m := res.Report.Memory; m.TotalBytes > 0 && m.FreePercent >= 0 {
		used := 100 - m.FreePercent
		rows = append(rows, fmt.Sprintf("Memory   %s %5d%%   %s of %s, pressure %s, swap %s",
			meter(float64(used)/100), used, engine.BinaryBytes(m.TotalBytes*int64(used)/100), engine.BinaryBytes(m.TotalBytes),
			m.Pressure, engine.BinaryBytes(res.Report.Swap.UsedBytes)))
	}
	if d := res.Report.Disk; d.ContainerTotalBytes > 0 {
		used := d.ContainerTotalBytes - d.ContainerFreeBytes
		line := fmt.Sprintf("Disk     %s %5.1f%%   %s free of %s",
			meter(float64(used)/float64(d.ContainerTotalBytes)), float64(used)*100/float64(d.ContainerTotalBytes),
			engine.DecimalBytes(d.ContainerFreeBytes), engine.DecimalBytes(d.ContainerTotalBytes))
		if io := res.DiskIO; io != nil {
			line += fmt.Sprintf(", %.1f MB/s", io.MBPerSec)
		}
		rows = append(rows, line)
	}
	if n := res.Network; n != nil {
		rows = append(rows, fmt.Sprintf("Network  down %s/s   up %s/s", engine.DecimalBytes(int64(n.InBytesPerSec)), engine.DecimalBytes(int64(n.OutBytesPerSec))))
	}
	if p := res.Power; p != nil && p.Present {
		rows = append(rows, powerLine(*p))
	}
	if len(rows) == 0 {
		rows = []string{"Nothing could be read. See the notes below."}
	}
	b.WriteString("\n" + t.block("Now", "", " "+strings.Join(rows, "\n ")+"\n"))

	if withProcesses && len(res.Report.TopProcesses) > 0 {
		var prows [][]string
		for _, p := range res.Report.TopProcesses {
			prows = append(prows, []string{p.Name, engine.BinaryBytes(p.RSSBytes), fmt.Sprint(p.Count)})
		}
		b.WriteString("\n" + t.block("Biggest memory users", "", t.Grid(
			[]Column{{Title: "Group", Flex: true}, {Title: "Memory", Right: true}, {Title: "Processes", Right: true}}, prows, 100,
			func(_, c int) *lipgloss.Style {
				if c == 1 {
					return ptr(t.size)
				}
				return ptr(t.text)
			})))
	}
	return b.String()
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

// statusEvery is how long the Health tab waits between live readings.
const statusEvery = 3 * time.Second

// StatusMsg carries one live reading for the Health tab.
type StatusMsg struct{ Res engine.StatusResult }

// statusTickMsg says it is time to take the next reading.
type statusTickMsg struct{}

// status reads the live state for the dashboard. Tests replace it through
// TuiDeps.Status.
func (d *TuiDeps) status(ctx context.Context) engine.StatusResult {
	if d.Status != nil {
		return d.Status(ctx, d)
	}
	return engine.Status(ctx, engine.StatusOptions{Runner: Runner})
}

// syncStatus starts a live reading when the Health tab is showing and none is
// running. Each reading schedules the next one, so readings stop on their own
// when another tab is open; nothing is read anywhere else.
func (m TuiModel) syncStatus(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if m.Tab != tabHealth || m.Overlay != OverlayNone || m.Loading || m.Data == nil || m.statusBusy || m.statusWait || m.Deps == nil {
		return m, cmd
	}
	m.statusBusy = true
	deps := m.Deps
	read := func() tea.Msg { return StatusMsg{Res: deps.status(context.Background())} }
	return m, tea.Batch(cmd, read)
}

// statusRead stores a reading and schedules the next one.
func (m TuiModel) statusRead(msg StatusMsg) (tea.Model, tea.Cmd) {
	res := msg.Res
	m.status, m.statusBusy, m.statusWait = &res, false, true // wait for the tick before the next reading
	return m, tea.Tick(statusEvery, func(time.Time) tea.Msg { return statusTickMsg{} })
}
