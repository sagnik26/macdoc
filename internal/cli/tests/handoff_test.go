package tests

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/cli"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/store"
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
	m = step(t, m, cli.AppliedMsg{ID: "P-7F3K"})
	if m.Overlay != cli.OverlayNone || m.Plan != nil || !strings.Contains(m.Flash, "Back from macdoc apply P-7F3K") {
		t.Errorf("after apply: overlay %d plan %v flash %q", m.Overlay, m.Plan, m.Flash)
	}
	m = step(t, m, cli.AppliedMsg{ID: "P-7F3K", Err: errors.New("no tty")})
	if !strings.Contains(m.Flash, "Couldn't hand the terminal") {
		t.Errorf("flash %q", m.Flash)
	}
}

func TestTUIApplyOnlyPendingPlans(t *testing.T) {
	m := sealed(t)
	m.Plan.Status = planner.Expired
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
	m.Deps.Plans = func(*cli.TuiDeps) ([]store.PlanRecord, error) { return nil, nil }
	// With no plans waiting, /apply goes to the suggestions; nothing is handed off.
	if got, cmd := run("/apply"); cmd != nil || got.Overlay != cli.OverlayNone || got.Tab != cli.TabSuggestions || !strings.Contains(got.Flash, "Press enter on a suggestion") {
		t.Errorf("/apply with no plans: overlay %d tab %d flash %q", got.Overlay, got.Tab, got.Flash)
	}
	if got, cmd := run("/apply ../etc"); cmd != nil || !strings.Contains(got.Flash, "isn't a plan ID") {
		t.Errorf("/apply with a path: %q", got.Flash)
	}
	if _, cmd := run("/apply p-7f3k"); cmd == nil {
		t.Error("/apply P-id should hand off")
	}
	mustContain(t, m.HelpView(), "apply it here in the terminal", "Nothing is removed until you confirm", "code typed")
	if !strings.Contains(m.HelpView(), "/reclaim") || strings.Contains(m.HelpView(), "/apply") {
		t.Error("/reclaim is listed in the palette; /apply is typed-only")
	}
	if strings.Contains(m.HelpView(), "/quit") {
		t.Error("/quit is shown in the footer, not the palette")
	}
}

// There is no plans page. A plan Claude proposed and that can still be applied
// shows as one line on the Suggestions tab, and a hands it to apply. Plans the
// person made, expired ones and applied ones are not listed.
func TestTUIClaudePlanLine(t *testing.T) {
	now := time.Now()
	plans := []store.PlanRecord{
		{ID: "P-MINE", Origin: model.OriginCLI, CreatedAt: now, ExpiresAt: now.Add(25 * time.Minute), Items: 4, TotalBytes: 1e9, Status: store.PlanPending},
		{ID: "P-OLDC", Origin: model.OriginClaude, CreatedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(-150 * time.Minute), Items: 1, TotalBytes: 1e9, Status: store.PlanPending},
		{ID: "P-DONE", Origin: model.OriginClaude, CreatedAt: now, ExpiresAt: now.Add(20 * time.Minute), Items: 2, TotalBytes: 2e9, Status: store.PlanApplied},
		{ID: "P-7F3K", Origin: model.OriginClaude, CreatedAt: now.Add(-5 * time.Minute), ExpiresAt: now.Add(25 * time.Minute), Items: 3, TotalBytes: 12e9, Status: store.PlanPending},
	}
	m := loaded(t, 120, 40)
	m.Deps.Plans = func(*cli.TuiDeps) ([]store.PlanRecord, error) { return plans, nil }
	m = step(t, m, cli.LoadedMsg{Data: tuiFixture()})
	m = step(t, m, keys("tab")...)
	mustContain(t, m.View(), "Claude proposed a plan: 3 items, 12.0 GB", "apply it in Terminal")
	for _, gone := range []string{"P-MINE", "P-OLDC", "P-DONE", "Apply ◀"} {
		if strings.Contains(m.View(), gone) {
			t.Errorf("the suggestions tab should not list %q", gone)
		}
	}
	if _, cmd := m.Update(keys("a")[0]); cmd == nil {
		t.Error("a should hand Claude's plan to apply")
	}
	// Without a plan from Claude there is no line and a does nothing.
	none := loaded(t, 120, 40)
	none.Deps.Plans = func(*cli.TuiDeps) ([]store.PlanRecord, error) { return plans[:3], nil }
	none = step(t, none, cli.LoadedMsg{Data: tuiFixture()})
	none = step(t, none, keys("tab")...)
	if strings.Contains(none.View(), "Claude proposed") {
		t.Error("no line without a waiting plan from Claude")
	}
	if _, cmd := none.Update(keys("a")[0]); cmd != nil {
		t.Error("a with no plan from Claude should do nothing")
	}
}

// The handoff runs the real apply command on the terminal it's given, then
// waits for Enter. Without a real terminal, apply refuses.
func TestApplyHandoffRun(t *testing.T) {
	var out bytes.Buffer
	var gotID string
	h := &cli.ApplyHandoff{Home: "/nonexistent", ID: "P-7F3K", RunFunc: func(cmd *cobra.Command, d cli.ApplyDeps, id string) error {
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
	h = &cli.ApplyHandoff{Home: t.TempDir(), ID: "P-7F3K", RunFunc: cli.RunApply}
	h.SetStdin(strings.NewReader("7F3K\n\n"))
	h.SetStdout(&out)
	_ = h.Run()
	mustContain(t, out.String(), "needs you at a Terminal")
}
