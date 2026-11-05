package tests

import (
	"bytes"
	"errors"
	"macdoc/internal/cli"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"macdoc/internal/engine"
	"macdoc/internal/planner"
)

// sealed is a dashboard showing a freshly sealed plan.
func sealed(t *testing.T) cli.TuiModel {
	t.Helper()
	cli.NowFunc = func() time.Time { return planNow }
	t.Cleanup(func() { cli.NowFunc = time.Now })
	m := step(t, loaded(t, 100, 40), keys("2")...)
	m.Deps.MakePlan = func(*cli.TuiDeps, planner.Selector, *engine.OpportunityResult) (engine.PlanResult, error) {
		return samplePlan(), nil
	}
	return runPlan(t, m)
}

func TestTUIApplyKeyHandsOff(t *testing.T) {
	m := sealed(t)
	mustContain(t, m.View(), "Press a to apply it", "a apply")
	next, cmd := m.Update(keys("a")[0])
	if cmd == nil {
		t.Fatal("a on a pending plan should hand the terminal to apply")
	}
	m = next.(cli.TuiModel)
	m = step(t, m, cli.AppliedMsg{Id: "P-7F3K"})
	if m.Overlay != cli.OverlayNone || m.Plan != nil || !strings.Contains(m.Flash, "Back from macdoc apply P-7F3K") {
		t.Errorf("after apply: overlay %d plan %v flash %q", m.Overlay, m.Plan, m.Flash)
	}
	m = step(t, m, cli.AppliedMsg{Id: "P-7F3K", Err: errors.New("no tty")})
	if !strings.Contains(m.Flash, "Couldn't hand the terminal") {
		t.Errorf("flash %q", m.Flash)
	}
}

func TestTUIApplyOnlyPendingPlans(t *testing.T) {
	m := sealed(t)
	m.Plan.Status = planner.StatusExpired
	next, cmd := m.Update(keys("a")[0])
	if cmd != nil || next.(cli.TuiModel).Overlay != cli.OverlayPlan {
		t.Error("a on an expired plan should do nothing")
	}
}

func TestTUIApplyCommand(t *testing.T) {
	m := loaded(t, 100, 40)
	run := func(line string) (cli.TuiModel, tea.Cmd) {
		next, cmd := m.RunCommand(line)
		return next.(cli.TuiModel), cmd
	}
	if got, cmd := run("/apply"); cmd != nil || !strings.Contains(got.Flash, "Usage: /apply") {
		t.Errorf("/apply with no plan: %q", got.Flash)
	}
	if got, cmd := run("/apply ../etc"); cmd != nil || !strings.Contains(got.Flash, "isn't a plan ID") {
		t.Errorf("/apply with a path: %q", got.Flash)
	}
	if _, cmd := run("/apply p-7f3k"); cmd == nil {
		t.Error("/apply P-id should hand off")
	}
	mustContain(t, m.HelpView(), "/apply [P-id]", "Nothing is removed until you confirm", "code typed")
}

// The handoff runs the real apply command on the terminal it's given, then
// waits for Enter. Without a real terminal, apply refuses.
func TestApplyHandoffRun(t *testing.T) {
	var out bytes.Buffer
	var gotID string
	h := &cli.ApplyHandoff{Home: "/nonexistent", Id: "P-7F3K", RunFunc: func(cmd *cobra.Command, d cli.ApplyDeps, id string) error {
		gotID = id
		if cmd.InOrStdin() == nil || d.Home != "/nonexistent" {
			t.Error("handoff didn't pass the terminal and home")
		}
		return errors.New("apply needs you at a Terminal")
	}}
	h.SetStdin(strings.NewReader("\n"))
	h.SetStdout(&out)
	if err := h.Run(); err != nil {
		t.Fatal(err)
	}
	if gotID != "P-7F3K" {
		t.Errorf("ran %q", gotID)
	}
	mustContain(t, out.String(), "macdoc apply P-7F3K", "macdoc: apply needs you at a Terminal", "Press Enter to go back")

	// With the real cli.RunApply, a non-terminal is refused before anything.
	out.Reset()
	h = &cli.ApplyHandoff{Home: t.TempDir(), Id: "P-7F3K", RunFunc: cli.RunApply}
	h.SetStdin(strings.NewReader("7F3K\n\n"))
	h.SetStdout(&out)
	_ = h.Run()
	mustContain(t, out.String(), "needs you at a Terminal")
}
