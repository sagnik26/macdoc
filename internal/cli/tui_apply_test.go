package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"macdoc/internal/engine"
	"macdoc/internal/planner"
)

// sealed is a dashboard showing a freshly sealed plan.
func sealed(t *testing.T) tuiModel {
	t.Helper()
	nowFunc = func() time.Time { return planNow }
	t.Cleanup(func() { nowFunc = time.Now })
	m := step(t, loaded(t, 100, 40), keys("2")...)
	m.deps.makePlan = func(*tuiDeps, planner.Selector, *engine.OpportunityResult) (engine.PlanResult, error) {
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
	m = next.(tuiModel)
	m = step(t, m, appliedMsg{id: "P-7F3K"})
	if m.overlay != overlayNone || m.plan != nil || !strings.Contains(m.flash, "Back from macdoc apply P-7F3K") {
		t.Errorf("after apply: overlay %d plan %v flash %q", m.overlay, m.plan, m.flash)
	}
	m = step(t, m, appliedMsg{id: "P-7F3K", err: errors.New("no tty")})
	if !strings.Contains(m.flash, "Couldn't hand the terminal") {
		t.Errorf("flash %q", m.flash)
	}
}

func TestTUIApplyOnlyPendingPlans(t *testing.T) {
	m := sealed(t)
	m.plan.Status = planner.StatusExpired
	next, cmd := m.Update(keys("a")[0])
	if cmd != nil || next.(tuiModel).overlay != overlayPlan {
		t.Error("a on an expired plan should do nothing")
	}
}

func TestTUIApplyCommand(t *testing.T) {
	m := loaded(t, 100, 40)
	run := func(line string) (tuiModel, tea.Cmd) {
		next, cmd := m.runCommand(line)
		return next.(tuiModel), cmd
	}
	if got, cmd := run("/apply"); cmd != nil || !strings.Contains(got.flash, "Usage: /apply") {
		t.Errorf("/apply with no plan: %q", got.flash)
	}
	if got, cmd := run("/apply ../etc"); cmd != nil || !strings.Contains(got.flash, "isn't a plan ID") {
		t.Errorf("/apply with a path: %q", got.flash)
	}
	if _, cmd := run("/apply p-7f3k"); cmd == nil {
		t.Error("/apply P-id should hand off")
	}
	mustContain(t, m.helpView(), "/apply [P-id]", "Nothing is removed until you confirm", "code typed")
}

// The handoff runs the real apply command on the terminal it's given, then
// waits for Enter. Without a real terminal, apply refuses.
func TestApplyHandoffRun(t *testing.T) {
	var out bytes.Buffer
	var gotID string
	h := &applyHandoff{home: "/nonexistent", id: "P-7F3K", run: func(cmd *cobra.Command, d applyDeps, id string) error {
		gotID = id
		if cmd.InOrStdin() == nil || d.home != "/nonexistent" {
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

	// With the real runApply, a non-terminal is refused before anything.
	out.Reset()
	h = &applyHandoff{home: t.TempDir(), id: "P-7F3K", run: runApply}
	h.SetStdin(strings.NewReader("7F3K\n\n"))
	h.SetStdout(&out)
	_ = h.Run()
	mustContain(t, out.String(), "needs you at a Terminal")
}
