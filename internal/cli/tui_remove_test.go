package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"macdoc/internal/engine"
	"macdoc/internal/executor"
	"macdoc/internal/model"
	"macdoc/internal/planner"
)

// press sends one key and runs the command it returns, feeding the
// resulting message back in (one level, as the program would).
func press(t *testing.T, m tuiModel, k string) tuiModel {
	t.Helper()
	for _, msg := range keys(k) {
		next, cmd := m.Update(msg)
		m = next.(tuiModel)
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

func removeTUI(t *testing.T, items ...model.PlanItem) (tuiModel, *[]model.Plan) {
	t.Helper()
	m := step(t, loaded(t, 120, 40), keys("tab")...)
	m.deps.makePlan = func(_ *tuiDeps, sel planner.Selector, _ *engine.OpportunityResult) (engine.PlanResult, error) {
		res := samplePlan()
		res.Plan.Items = items
		return res, nil
	}
	var calls []model.Plan
	m.deps.remove = func(_ *tuiDeps, p model.Plan, onItem func(executor.ItemEvent)) (executor.Report, error) {
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
	ops := loaded(t, 120, 40).items()
	m, calls := removeTUI(t, model.PlanItem{OpportunityID: ops[0].ID, Title: "Homebrew old versions", ExpectedBytes: 400_000_000,
		BytesSource: model.SourceTool, Risk: model.RiskCaution, Method: model.MethodNative, Argv: []string{"brew", "cleanup"}})

	m = press(t, m, "enter")
	if m.overlay != overlayRemove || len(*calls) != 0 {
		t.Fatalf("first enter should only ask: overlay %d, calls %d", m.overlay, len(*calls))
	}
	mustContain(t, m.View(), "Remove 1 item and free the space?", "Homebrew old versions", "400 MB", "Enter remove", "esc cancel")

	m = press(t, m, "enter")
	if len(*calls) != 1 || m.overlay != overlayNone {
		t.Fatalf("second enter should remove: calls %d overlay %d", len(*calls), m.overlay)
	}
	for _, op := range m.items() {
		if op.ID == ops[0].ID {
			t.Error("a removed item is still listed")
		}
	}
	mustContain(t, m.flash, "Removed 1 of 1", "freed 400 MB")
}

func TestTUIRemoveCancel(t *testing.T) {
	ops := loaded(t, 120, 40).items()
	m, calls := removeTUI(t, model.PlanItem{OpportunityID: ops[0].ID, Title: "Logs", ExpectedBytes: 1, BytesSource: model.SourceMeasured,
		Risk: model.RiskSafe, Method: model.MethodQuarantine, Target: "/Users/me/Library/Logs/X"})
	m = press(t, m, "enter")
	m = press(t, m, "esc")
	if len(*calls) != 0 || m.overlay != overlayNone || !strings.Contains(m.flash, "Nothing was removed") {
		t.Fatalf("esc: calls %d overlay %d flash %q", len(*calls), m.overlay, m.flash)
	}
}

func TestTUIRemoveRiskyNeedsCode(t *testing.T) {
	ops := loaded(t, 120, 40).items()
	m, calls := removeTUI(t, model.PlanItem{OpportunityID: ops[0].ID, Title: "Old project node_modules", ExpectedBytes: 1,
		BytesSource: model.SourceMeasured, Risk: model.RiskRisky, Method: model.MethodQuarantine, Target: "/Users/me/p/node_modules"})
	m = press(t, m, "enter")
	mustContain(t, m.View(), "Type 7F3K and press Enter")

	m = press(t, m, "enter")
	if len(*calls) != 0 || m.overlay != overlayRemove {
		t.Fatal("enter without the code removed something")
	}
	for _, k := range []string{"7", "F", "3", "X"} {
		m = press(t, m, k)
	}
	m = press(t, m, "enter")
	if len(*calls) != 0 {
		t.Fatal("a wrong code removed something")
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	m = press(t, m, "k") // case doesn't matter
	m = press(t, m, "enter")
	if len(*calls) != 1 {
		t.Fatal("the right code should remove")
	}
}

func TestTUIRemoveAdviceExplains(t *testing.T) {
	m, calls := removeTUI(t)
	for i, op := range m.items() {
		if op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise {
			m.cursor = i
			m = press(t, m, "enter")
			if len(*calls) != 0 || m.overlay == overlayRemove || !m.detail || !strings.Contains(m.flash, "can't remove this one") {
				t.Fatalf("advice item: overlay %d flash %q", m.overlay, m.flash)
			}
			return
		}
	}
	t.Skip("no advice-only item in the fixture")
}
