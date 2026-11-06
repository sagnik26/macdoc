package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/platform"
)

// Runner is the command Runner used by read-only commands; tests replace it.
var Runner platform.Runner = platform.ExecRunner{}

// newDoctorCmd builds `macdoc doctor`: the one command that shows everything,
// with sections underneath it for detail.
func newDoctorCmd(g *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Everything at once: health, where your space is, and what you can reclaim (read-only)",
		Long: `macdoc doctor shows one overview: disk and memory health, where your space
is, and what you could reclaim, ranked by safety. It scans only if there is
no scan yet. Use a section for more detail:

  macdoc doctor health          disk, memory, swap, top apps
  macdoc doctor slow            why it feels slow: memory, swap and what uses it
  macdoc doctor trend           what grew since an earlier scan
  macdoc doctor space           space by category, "System Data" decoded
  macdoc doctor wins            every suggestion with its ID
  macdoc doctor dev [toolchain] suggestions per developer toolchain
  macdoc doctor projects        projects: idle days, build folders, git state
  macdoc doctor explain <id>    one suggestion in detail
  macdoc doctor scan            rescan, then show the full report (every section)`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runOverview(cmd, g, overviewMode{})
		},
	}

	health := &cobra.Command{
		Use:   "health",
		Short: "Disk, memory, swap, snapshots and top memory users (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res := engine.Doctor(context.Background(), Runner)
			if g.json {
				return writeJSON(cmd.OutOrStdout(), "doctor health", res)
			}
			return RenderDoctor(cmd.OutOrStdout(), res)
		},
	}
	space := newStorageCmd(g)
	space.Use, space.Aliases = "space", []string{"storage"}
	cmd.AddCommand(health, newSlowCmd(g), newTrendCmd(g), space, newWinsCmd(g), newDevCmd(g), newProjectsCmd(g), newExplainCmd(g), newScanCmd(g))
	return cmd
}

// writeVitals writes the header and the disk, memory and snapshot lines.
func writeVitals(b *strings.Builder, t *theme, res engine.DoctorResult, extra string) {
	rep := res.Report
	hw := rep.Hardware

	machine := ""
	if hw.Model != "" {
		spec := strings.TrimPrefix(hw.Chip, "Apple ")
		if hw.MemoryBytes > 0 {
			spec = strings.TrimSpace(spec + ", " + engine.BinaryBytes(hw.MemoryBytes))
		}
		machine = fmt.Sprintf(" · %s (%s)", hw.Model, strings.Trim(spec, ", "))
	}
	if hw.OSVersion != "" {
		machine += " · macOS " + hw.OSVersion
	}
	switch {
	case t.on:
		b.WriteString(t.Card(t.brand.Render("◆ macdoc") + t.muted.Render(machine+extra)))
		b.WriteString("\n")
	default:
		fmt.Fprintf(b, "macdoc%s%s\n\n", machine, extra)
	}

	label := func(s string) string { return t.muted.Render(fmt.Sprintf("%-11s", s)) }
	d := rep.Disk
	if d.ContainerTotalBytes > 0 {
		used := d.ContainerTotalBytes - d.ContainerFreeBytes
		line := fmt.Sprintf("%s%s %s / %s used   %s free", label("Disk"),
			t.Meter(used, d.ContainerTotalBytes, 28), t.size.Render(engine.DecimalBytes(used)),
			engine.DecimalBytes(d.ContainerTotalBytes), t.usageStyle(used, d.ContainerTotalBytes).Render(engine.DecimalBytes(d.ContainerFreeBytes)))
		if d.PurgeableBytes.Value > 0 {
			line += t.muted.Render(fmt.Sprintf("  (+%s purgeable)", engine.DecimalBytes(d.PurgeableBytes.Value)))
		}
		fmt.Fprintln(b, line)
	} else {
		fmt.Fprintln(b, label("Disk")+t.muted.Render("—  unmeasured (see warnings)"))
	}

	m := rep.Memory
	mem := label("Memory")
	if m.TotalBytes > 0 {
		mem += engine.BinaryBytes(m.TotalBytes)
	} else {
		mem += "—"
	}
	if m.Pressure != "" {
		mem += " · pressure " + t.pressure(string(m.Pressure))
	}
	if m.FreePercent >= 0 && m.TotalBytes > 0 {
		mem += fmt.Sprintf(" · %d%% free", m.FreePercent)
	}
	if rep.Swap.TotalBytes > 0 || rep.Swap.UsedBytes > 0 {
		mem += fmt.Sprintf(" · swap %s used", engine.BinaryBytes(rep.Swap.UsedBytes))
	}
	fmt.Fprintln(b, mem)
	fmt.Fprintf(b, "%s%d local Time Machine snapshot(s)\n", label("Snapshots"), rep.Snapshots.Count)
}

// writeFindings writes "What matters most" (at most max findings; 0 = all).
func writeFindings(b *strings.Builder, t *theme, fs []engine.Finding, max int, details bool) {
	fmt.Fprintln(b, t.heading("What matters most"))
	if len(fs) == 0 {
		fmt.Fprintln(b, "  "+t.Ok.Render("Nothing urgent.")+" Disk, memory, and swap look healthy.")
	}
	for i, f := range fs {
		if max > 0 && i == max {
			fmt.Fprintln(b, t.muted.Render(fmt.Sprintf("  … %d more (macdoc doctor health)", len(fs)-max)))
			break
		}
		fmt.Fprintf(b, "  %s %s %s\n", t.muted.Render(fmt.Sprintf("%d.", i+1)), t.sev(f.Severity), t.text.Render(f.Title))
		if details && f.Detail != "" {
			fmt.Fprintln(b, t.muted.Render("       "+f.Detail))
		}
	}
}

// RenderDoctor renders `macdoc doctor health`.
func RenderDoctor(w io.Writer, res engine.DoctorResult) error {
	res = display(res) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	rep := res.Report
	writeVitals(&b, t, res, "")
	b.WriteString("\n")
	writeFindings(&b, t, res.Findings, 0, true)

	if len(rep.TopProcesses) > 0 {
		fmt.Fprintln(&b, "\n"+t.heading("Top memory users"))
		for _, p := range rep.TopProcesses {
			count := ""
			if p.Count > 1 {
				count = fmt.Sprintf(" (%d processes)", p.Count)
			}
			fmt.Fprintf(&b, "  %s %s%s\n", t.text.Render(fmt.Sprintf("%-28s", Truncate(p.Name, 28))),
				t.size.Render(fmt.Sprintf("%9s", engine.BinaryBytes(p.RSSBytes))), t.muted.Render(count))
		}
	}

	if len(rep.Warnings) > 0 {
		fmt.Fprintln(&b, "\n"+t.heading("Could not measure"))
		for _, wn := range rep.Warnings {
			fmt.Fprintln(&b, t.muted.Render("  — "+wn))
		}
	}

	fmt.Fprintln(&b, "\n"+t.done("Nothing has been changed. macdoc only reads unless you run `apply`."))
	_, err := io.WriteString(w, b.String())
	return err
}

func severityMark(s engine.Severity) string {
	switch s {
	case engine.SeverityCritical:
		return "‼"
	case engine.SeverityWarn:
		return "!"
	default:
		return "·"
	}
}

// bar renders a fixed-width usage bar.
func Bar(used, total int64, width int) string {
	if total <= 0 {
		return "▕" + strings.Repeat(" ", width) + "▏"
	}
	filled := int(float64(width) * float64(used) / float64(total))
	filled = max(0, min(width, filled))
	return "▕" + strings.Repeat("█", filled) + strings.Repeat(" ", width-filled) + "▏"
}

func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
