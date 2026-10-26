package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"macdoc/internal/engine"
	"macdoc/internal/platform"
)

// runner is the command runner used by read-only commands; tests replace it.
var runner platform.Runner = platform.ExecRunner{}

func newDoctorCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "One-screen health summary: disk, memory, swap, snapshots (read-only)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res := engine.Doctor(context.Background(), runner)
			if g.json {
				return writeJSON(cmd.OutOrStdout(), "doctor", res)
			}
			return renderDoctor(cmd.OutOrStdout(), res)
		},
	}
}

func renderDoctor(w io.Writer, res engine.DoctorResult) error {
	var b strings.Builder
	rep := res.Report
	hw := rep.Hardware

	// Header.
	head := "macdoc"
	if hw.Model != "" {
		spec := strings.TrimPrefix(hw.Chip, "Apple ")
		if hw.MemoryBytes > 0 {
			spec = strings.TrimSpace(spec + ", " + engine.BinaryBytes(hw.MemoryBytes))
		}
		head += fmt.Sprintf(" · %s (%s)", hw.Model, strings.Trim(spec, ", "))
	}
	if hw.OSVersion != "" {
		head += " · macOS " + hw.OSVersion
	}
	fmt.Fprintf(&b, "%s\n\n", head)

	// Disk.
	d := rep.Disk
	if d.ContainerTotalBytes > 0 {
		used := d.ContainerTotalBytes - d.ContainerFreeBytes
		line := fmt.Sprintf("Disk       %s %s / %s used   %s free",
			bar(used, d.ContainerTotalBytes, 28), engine.DecimalBytes(used),
			engine.DecimalBytes(d.ContainerTotalBytes), engine.DecimalBytes(d.ContainerFreeBytes))
		if d.PurgeableBytes.Value > 0 {
			line += fmt.Sprintf("  (+%s purgeable)", engine.DecimalBytes(d.PurgeableBytes.Value))
		}
		fmt.Fprintln(&b, line)
	} else {
		fmt.Fprintln(&b, "Disk       —  unmeasured (see warnings)")
	}

	// Memory and swap.
	m := rep.Memory
	mem := "Memory     "
	if m.TotalBytes > 0 {
		mem += engine.BinaryBytes(m.TotalBytes)
	} else {
		mem += "—"
	}
	if m.Pressure != "" {
		mem += " · pressure " + string(m.Pressure)
	}
	if m.FreePercent >= 0 && m.TotalBytes > 0 {
		mem += fmt.Sprintf(" · %d%% free", m.FreePercent)
	}
	if rep.Swap.TotalBytes > 0 || rep.Swap.UsedBytes > 0 {
		mem += fmt.Sprintf(" · swap %s used", engine.BinaryBytes(rep.Swap.UsedBytes))
	}
	fmt.Fprintln(&b, mem)
	fmt.Fprintf(&b, "Snapshots  %d local Time Machine snapshot(s)\n\n", rep.Snapshots.Count)

	// Findings.
	fmt.Fprintln(&b, "What matters most")
	if len(res.Findings) == 0 {
		fmt.Fprintln(&b, "  Nothing urgent. Disk, memory, and swap look healthy.")
	}
	for i, f := range res.Findings {
		fmt.Fprintf(&b, "  %d. %s %s\n", i+1, severityMark(f.Severity), f.Title)
		if f.Detail != "" {
			fmt.Fprintf(&b, "       %s\n", f.Detail)
		}
	}

	// Top memory users.
	if len(rep.TopProcesses) > 0 {
		fmt.Fprintln(&b, "\nTop memory users")
		for _, p := range rep.TopProcesses {
			count := ""
			if p.Count > 1 {
				count = fmt.Sprintf(" (%d processes)", p.Count)
			}
			fmt.Fprintf(&b, "  %-28s %9s%s\n", truncate(p.Name, 28), engine.BinaryBytes(p.RSSBytes), count)
		}
	}

	if len(rep.Warnings) > 0 {
		fmt.Fprintln(&b, "\nCould not measure")
		for _, wn := range rep.Warnings {
			fmt.Fprintf(&b, "  — %s\n", wn)
		}
	}

	fmt.Fprintln(&b, "\nNothing has been changed. macdoc only reads unless you run `apply`.")
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
func bar(used, total int64, width int) string {
	if total <= 0 {
		return "▕" + strings.Repeat(" ", width) + "▏"
	}
	filled := int(float64(width) * float64(used) / float64(total))
	filled = max(0, min(width, filled))
	return "▕" + strings.Repeat("█", filled) + strings.Repeat(" ", width-filled) + "▏"
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
