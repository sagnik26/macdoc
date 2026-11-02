package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"macdoc/internal/engine"
	"macdoc/internal/projects"
)

// minProjectBytes hides projects whose artifacts are too small to matter.
const minProjectBytes = 1_000_000

func newProjectsCmd(g *globalFlags) *cobra.Command {
	var idle int
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "Your projects: idle days, build artifacts and uncommitted changes (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if idle < 0 {
				return fmt.Errorf("--idle must be 0 or more")
			}
			home, err := userHome()
			if err != nil {
				return err
			}
			res, err := engine.Projects(context.Background(), engine.OpportunityOptions{Home: home, Runner: runner})
			if err != nil {
				return err
			}
			res = filterProjects(res, idle)
			if g.json {
				return writeJSONWarn(cmd.OutOrStdout(), "projects", res, res.Warnings)
			}
			return renderProjects(cmd.OutOrStdout(), res, home, idle)
		},
	}
	cmd.Flags().IntVar(&idle, "idle", 0, "only projects idle at least this many days")
	return cmd
}

// filterProjects drops projects with under 1 MB of artifacts and, with
// idle > 0, projects not known to be idle that long.
func filterProjects(res engine.ProjectsResult, idle int) engine.ProjectsResult {
	out := []projects.Project{}
	for _, p := range res.Projects {
		if p.ArtifactBytes < minProjectBytes || (idle > 0 && p.IdleDays < idle) {
			continue
		}
		out = append(out, p)
	}
	res.Projects = out
	return res
}

func renderProjects(w io.Writer, res engine.ProjectsResult, home string, idle int) error {
	res = display(res) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	b.WriteString(t.commandHeader("projects", res.Scan.ID, res.ScanAge))

	var total int64
	for _, p := range res.Projects {
		total += p.ArtifactBytes
	}
	b.WriteString(t.section(t.accent, "Projects", fmt.Sprintf("%s in %d", engine.DecimalBytes(total), len(res.Projects))))
	if len(res.Projects) == 0 {
		if idle > 0 {
			fmt.Fprintf(&b, "  No projects idle %d days or more with build artifacts.\n", idle)
		} else {
			b.WriteString("  No projects with build artifacts were found in the latest scan.\n")
		}
	}
	for _, p := range res.Projects {
		head := t.title.Render(tildePath(home, p.Root))
		if len(p.Kinds) > 0 {
			head += t.muted.Render(" · " + strings.Join(p.Kinds, ", "))
		}
		fmt.Fprintf(&b, "  %s\n", head)
		fmt.Fprintf(&b, "    %s · %s · %s\n", idleText(t, p), t.size.Render(engine.DecimalBytes(p.ArtifactBytes)), gitText(t, p))
		parts := make([]string, len(p.Artifacts))
		for i, a := range p.Artifacts {
			parts[i] = a.Rel + " " + engine.DecimalBytes(a.Bytes)
		}
		fmt.Fprintf(&b, "    %s\n", t.wrap(t.muted.Render(strings.Join(parts, " · ")), 4))
		if p.Protected {
			fmt.Fprintf(&b, "    %s\n", t.muted.Render("In a protected folder: listed only, macdoc never moves anything here."))
		}
	}
	b.WriteString("\n")

	var gone []projects.DerivedData
	for _, d := range res.DerivedData {
		if d.Missing {
			gone = append(gone, d)
		}
	}
	if len(gone) > 0 {
		var sum int64
		for _, d := range gone {
			sum += d.Bytes
		}
		b.WriteString(t.section(t.warn, "DerivedData of deleted projects", engine.DecimalBytes(sum)))
		for _, d := range gone {
			fmt.Fprintf(&b, "  %s  %s\n", t.size.Render(fmt.Sprintf("%9s", engine.DecimalBytes(d.Bytes))), tildePath(home, d.Workspace))
		}
		b.WriteString("\n")
	}

	writeNotes(&b, t, res.Warnings)
	b.WriteString(t.footer("macdoc doctor wins"))
	_, err := io.WriteString(w, b.String())
	return err
}

// idleText is "idle 143 days (git commit)" or "idle time unknown".
func idleText(t *theme, p projects.Project) string {
	if p.IdleDays < 0 {
		return t.muted.Render("idle time unknown")
	}
	s := fmt.Sprintf("idle %d days", p.IdleDays)
	if p.IdleDays == 1 {
		s = "idle 1 day"
	}
	st := t.text
	if p.IdleDays >= 90 {
		st = t.ok
	}
	return st.Render(s) + t.muted.Render(" ("+p.ActivitySource+")")
}

// gitText describes a project's git state.
func gitText(t *theme, p projects.Project) string {
	switch p.Git {
	case projects.GitClean:
		return t.ok.Render("git clean")
	case projects.GitDirty:
		return t.warn.Render("uncommitted changes")
	case projects.GitNone:
		return t.muted.Render("not in git")
	}
	return t.muted.Render("git not checked")
}

// tildePath shows p relative to home as "~/…".
func tildePath(home, p string) string {
	if rest, ok := strings.CutPrefix(p, strings.TrimSuffix(home, "/")+"/"); ok {
		return "~/" + rest
	}
	return p
}
