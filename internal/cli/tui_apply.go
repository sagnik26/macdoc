package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"macdoc/internal/planner"
)

// appliedMsg arrives when the dashboard gets the terminal back after apply.
type appliedMsg struct {
	id  string
	err error
}

// applyHandoff runs `macdoc apply <id>` in this process on the real
// terminal while the dashboard is paused (tea.Exec). It is the same command
// with the same checks: a real terminal, the dry run again, per-item y/N
// where needed and the typed code. The dashboard itself never changes
// anything.
type applyHandoff struct {
	home string
	id   string
	in   io.Reader
	out  io.Writer
	run  func(cmd *cobra.Command, d applyDeps, id string) error // runApply; tests replace it
}

func (a *applyHandoff) SetStdin(r io.Reader)  { a.in = r }
func (a *applyHandoff) SetStdout(w io.Writer) { a.out = w }
func (a *applyHandoff) SetStderr(io.Writer)   {}

func (a *applyHandoff) Run() error {
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
	_, _ = fmt.Fprintf(a.out, "\n %s\n\n", t.heading("macdoc apply "+a.id))
	if err := a.run(cmd, applyDeps{home: a.home, now: nowFunc}, a.id); err != nil {
		_, _ = fmt.Fprintf(a.out, "\n %s %s\n", t.bad.Render("macdoc:"), err.Error())
	}
	_, _ = fmt.Fprint(a.out, "\n "+t.muted.Render("Press Enter to go back to the dashboard."))
	_, _ = bufio.NewReader(a.in).ReadString('\n')
	return nil
}

// startApply pauses the dashboard and hands the terminal to apply. id may
// be empty: then it's the plan sealed in this session.
func (m tuiModel) startApply(id string) (tea.Model, tea.Cmd) {
	id = strings.ToUpper(strings.TrimSpace(id))
	if id == "" && m.plan != nil {
		id = m.plan.Plan.ID
	}
	if id == "" {
		m.flash = "Usage: /apply <P-id>, or seal a plan first (mark suggestions with space, then p)."
		return m, nil
	}
	if !planner.ValidID(id) {
		m.flash = fmt.Sprintf("%q isn't a plan ID (they look like P-7F3K).", id)
		return m, nil
	}
	run := m.deps.apply
	if run == nil {
		run = runApply
	}
	h := &applyHandoff{home: m.deps.home, id: id, run: run}
	return m, tea.Exec(h, func(err error) tea.Msg { return appliedMsg{id: id, err: err} })
}

// applied is the dashboard coming back from apply.
func (m tuiModel) applied(msg appliedMsg) (tea.Model, tea.Cmd) {
	m.overlay, m.scroll = overlayNone, 0
	if m.plan != nil && m.plan.Plan.ID == msg.id {
		m.plan = nil
	}
	if msg.err != nil {
		m.flash = "Couldn't hand the terminal to apply: " + msg.err.Error()
		return m, nil
	}
	m.flash = "Back from macdoc apply " + msg.id + ". What happened is in macdoc log. Press r to rescan for fresh numbers."
	return m, nil
}
