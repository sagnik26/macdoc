package tests

import (
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// press sends one key and runs the command it returns, feeding the
// resulting message back in (one level, as the program would).
func press(t *testing.T, m cli.TuiModel, k string) cli.TuiModel {
	t.Helper()
	for _, msg := range keys(k) {
		next, cmd := m.Update(msg)
		m = next.(cli.TuiModel)
		if cmd != nil {
			if out := cmd(); out != nil {
				if _, ok := out.(tea.BatchMsg); !ok {
					m = step(t, m, out)
				}
			}
		}
	}
	return m
}

func removeTUI(t *testing.T, items ...model.PlanItem) (cli.TuiModel, *[]model.Plan) {
	t.Helper()
	m := step(t, loaded(t, 120, 40), keys("tab")...)
	m.Deps.MakePlan = func(_ *cli.TuiDeps, sel planner.Selector, _ *engine.OpportunityResult) (engine.PlanResult, error) {
		res := samplePlan()
		res.Plan.Items = items
		return res, nil
	}
	var calls []model.Plan
	m.Deps.Remove = func(_ *cli.TuiDeps, p model.Plan, onItem func(executor.Event)) (executor.Report, error) {
		calls = append(calls, p)
		rep := executor.Report{PlanID: p.ID}
		for _, it := range p.Items {
			rep.Items = append(rep.Items, executor.ItemResult{Item: it, Outcome: executor.OutcomeDone, Deleted: true})
		}
		before, after := int64(1_000), int64(400_001_000)
		rep.FreeBefore, rep.FreeAfter = &before, &after
		return rep, nil
	}
	return m, &calls
}

func TestTUIRemoveEnterTwice(t *testing.T) {
	ops := loaded(t, 120, 40).Items()
	m, calls := removeTUI(t, model.PlanItem{OpportunityID: ops[0].ID, Title: "Homebrew old versions", ExpectedBytes: 400_000_000,
		BytesSource: model.SourceTool, Risk: model.RiskCaution, Method: model.MethodNative, Argv: []string{"brew", "cleanup"}})

	m = press(t, m, "enter")
	if m.Overlay != cli.OverlayRemove || len(*calls) != 0 {
		t.Fatalf("first enter should only ask: overlay %d, calls %d", m.Overlay, len(*calls))
	}
	mustContain(t, m.View(), "Remove 1 item and free the space?", "Homebrew old versions", "400 MB", "Enter remove", "esc cancel")

	m = press(t, m, "enter")
	if len(*calls) != 1 || m.Overlay != cli.OverlayNone {
		t.Fatalf("second enter should remove: calls %d overlay %d", len(*calls), m.Overlay)
	}
	for _, op := range m.Items() {
		if op.ID == ops[0].ID {
			t.Error("a removed item is still listed")
		}
	}
	mustContain(t, m.Flash, "Removed 1 of 1", "freed 400 MB")
}

func TestTUIRemoveCancel(t *testing.T) {
	ops := loaded(t, 120, 40).Items()
	m, calls := removeTUI(t, model.PlanItem{OpportunityID: ops[0].ID, Title: "Logs", ExpectedBytes: 1, BytesSource: model.SourceMeasured,
		Risk: model.RiskSafe, Method: model.MethodQuarantine, Target: "/Users/me/Library/Logs/X"})
	m = press(t, m, "enter")
	m = press(t, m, "esc")
	if len(*calls) != 0 || m.Overlay != cli.OverlayNone || !strings.Contains(m.Flash, "Nothing was removed") {
		t.Fatalf("esc: calls %d overlay %d flash %q", len(*calls), m.Overlay, m.Flash)
	}
}

func TestTUIRemoveRiskyNeedsCode(t *testing.T) {
	ops := loaded(t, 120, 40).Items()
	m, calls := removeTUI(t, model.PlanItem{OpportunityID: ops[0].ID, Title: "Old project node_modules", ExpectedBytes: 1,
		BytesSource: model.SourceMeasured, Risk: model.RiskRisky, Method: model.MethodQuarantine, Target: "/Users/me/p/node_modules"})
	m = press(t, m, "enter")
	mustContain(t, m.View(), "Type "+ops[0].ID+" and press Enter")

	m = press(t, m, "enter")
	if len(*calls) != 0 || m.Overlay != cli.OverlayRemove {
		t.Fatal("enter without the item's ID removed something")
	}
	for _, k := range []string{"X", "X", "X", "X", "X", "X", "X", "X"} {
		m = press(t, m, k)
	}
	m = press(t, m, "enter")
	if len(*calls) != 0 {
		t.Fatal("a wrong ID removed something")
	}
	for i := 0; i < 8; i++ {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	for _, r := range ops[0].ID {
		m = press(t, m, string(r))
	}
	m = press(t, m, "enter")
	if len(*calls) != 1 {
		t.Fatal("the right ID should remove")
	}
}

// Two RISKY items each need their own typed ID; the second one runs the
// removal, and neither ID alone is enough.
func TestTUIRemoveTwoRiskyEachNeedOwnID(t *testing.T) {
	m, calls := removeTUI(t,
		model.PlanItem{OpportunityID: "O-riska1", Title: "Old node_modules A", ExpectedBytes: 1,
			BytesSource: model.SourceMeasured, Risk: model.RiskRisky, Method: model.MethodQuarantine, Target: "/Users/me/p/a/node_modules"},
		model.PlanItem{OpportunityID: "O-riska2", Title: "Old node_modules B", ExpectedBytes: 1,
			BytesSource: model.SourceMeasured, Risk: model.RiskRisky, Method: model.MethodQuarantine, Target: "/Users/me/p/b/node_modules"})
	m = press(t, m, "enter")
	typeID := func(m cli.TuiModel, id string) cli.TuiModel {
		for _, r := range id {
			m = press(t, m, string(r))
		}
		return m
	}
	m = typeID(m, "O-riska1")
	m = press(t, m, "enter")
	if len(*calls) != 0 {
		t.Fatal("the first RISKY item's ID removed the plan")
	}
	mustContain(t, m.View(), "Type O-riska2 and press Enter (2 of 2 RISKY)")
	m = typeID(m, "O-riska1") // the first ID again is not the second item's ID
	m = press(t, m, "enter")
	if len(*calls) != 0 {
		t.Fatal("a repeated ID confirmed the second RISKY item")
	}
	for i := 0; i < 8; i++ {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m = typeID(m, "O-riska2")
	press(t, m, "enter")
	if len(*calls) != 1 {
		t.Fatal("confirming both RISKY items should remove")
	}
}

func TestTUIRemoveAdviceExplains(t *testing.T) {
	m, calls := removeTUI(t)
	for i, op := range m.Items() {
		if op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise {
			m.Cursor = i
			m = press(t, m, "enter")
			if len(*calls) != 0 || m.Overlay == cli.OverlayRemove || !m.Detail || !strings.Contains(m.Flash, "can't remove this one") {
				t.Fatalf("advice item: overlay %d flash %q", m.Overlay, m.Flash)
			}
			return
		}
	}
	t.Skip("no advice-only item in the fixture")
}

// The suggestions that were ticked stay ticked when the confirmation box is
// opened and canceled; only the items that were removed lose their tick.
func TestTUIRemoveCancelKeepsTheTicks(t *testing.T) {
	ops := loaded(t, 120, 40).Items()
	m, _ := removeTUI(t, model.PlanItem{OpportunityID: ops[0].ID, Title: "Logs", ExpectedBytes: 1, BytesSource: model.SourceMeasured,
		Risk: model.RiskSafe, Method: model.MethodQuarantine, Target: "/Users/me/Library/Logs/X"})
	m.Marked[ops[0].ID], m.Marked[ops[1].ID] = true, true

	m = press(t, m, "enter")
	m = press(t, m, "esc")
	if !m.Marked[ops[0].ID] || !m.Marked[ops[1].ID] {
		t.Fatalf("esc should keep the ticks: %v", m.Marked)
	}

	m = press(t, m, "enter")
	m = press(t, m, "enter")
	if m.Marked[ops[0].ID] || !m.Marked[ops[1].ID] {
		t.Errorf("only the removed item should lose its tick: %v", m.Marked)
	}
}
