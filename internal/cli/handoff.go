package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/planner"
)

// AppliedMsg arrives when the dashboard gets the terminal back after apply.
type AppliedMsg struct {
	ID  string
	Err error
}

// ApplyHandoff runs `macdoc apply <id>` in this process on the real
// terminal while the dashboard is paused (tea.Exec). It is the same command
// with the same checks: a real terminal, the dry run again, per-item y/N
// where needed and the typed code. The dashboard itself never changes
// anything.
type ApplyHandoff struct {
	Home    string
	ID      string
	in      io.Reader
	out     io.Writer
	RunFunc func(cmd *cobra.Command, d ApplyDeps, id string) error // RunApply; tests replace it
}

// SetStdin implements tea.ExecCommand.
func (a *ApplyHandoff) SetStdin(r io.Reader) { a.in = r }

// SetStdout implements tea.ExecCommand.
func (a *ApplyHandoff) SetStdout(w io.Writer) { a.out = w }

// SetStderr implements tea.ExecCommand and ignores w.
func (a *ApplyHandoff) SetStderr(io.Writer) {}

// Run implements tea.ExecCommand: it runs `macdoc apply` on the real terminal.
func (a *ApplyHandoff) Run() error {
	if a.in == nil {
		a.in = os.Stdin
	}
	if a.out == nil {
		a.out = os.Stdout
	}
	cmd := &cobra.Command{}
	cmd.SetIn(a.in)
	cmd.SetOut(a.out)
	t := themeFor(a.out)
	_, _ = fmt.Fprintf(a.out, "\n %s\n\n", t.heading("macdoc apply "+a.ID))
	if err := a.RunFunc(cmd, ApplyDeps{Home: a.Home, Now: NowFunc}, a.ID); err != nil {
		_, _ = fmt.Fprintf(a.out, "\n %s %s\n", t.bad.Render("macdoc:"), err.Error())
	}
	_, _ = fmt.Fprint(a.out, "\n "+t.muted.Render("Press Enter to go back to the dashboard."))
	_, _ = bufio.NewReader(a.in).ReadString('\n')
	return nil
}

// startApply pauses the dashboard and hands the terminal to apply. id may
// be empty: then it's the plan sealed in this session.
func (m TuiModel) startApply(id string) (tea.Model, tea.Cmd) {
	id = strings.ToUpper(strings.TrimSpace(id))
	if id == "" && m.Plan != nil {
		id = m.Plan.Plan.ID
	}
	if id == "" {
		m.Flash = "Usage: /apply <P-id>, or seal a plan first (mark suggestions with space, then p)."
		return m, nil
	}
	if !planner.Valid(id) {
		m.Flash = fmt.Sprintf("%q isn't a plan ID (they look like P-7F3K).", id)
		return m, nil
	}
	run := m.Deps.apply
	if run == nil {
		run = RunApply
	}
	h := &ApplyHandoff{Home: m.Deps.Home, ID: id, RunFunc: run}
	return m, tea.Exec(h, func(err error) tea.Msg { return AppliedMsg{ID: id, Err: err} })
}

// applied is the dashboard coming back from apply.
func (m TuiModel) applied(msg AppliedMsg) (tea.Model, tea.Cmd) {
	from := m.Overlay
	m.Overlay, m.scroll = OverlayNone, 0
	if m.Plan != nil && m.Plan.Plan.ID == msg.ID {
		m.Plan = nil
	}
	if msg.Err != nil {
		m.Flash = "Couldn't hand the terminal to apply: " + msg.Err.Error()
		return m, nil
	}
	flash := "Back from macdoc apply " + msg.ID + ". What happened is in macdoc log."
	m.refreshClaudePlans()          // the plan just applied is no longer waiting
	if from == overlayMaintenance { // the refresh page again
		next, cmd := m.openMaintenance()
		if nm, ok := next.(TuiModel); ok && nm.Overlay == overlayMaintenance {
			nm.Flash = flash
			return nm, cmd
		}
	}
	m.Flash = flash + " Press r to rescan for fresh numbers."
	return m, nil
}
