package tests

import (
	"macdoc/internal/cli"
	"strings"
	"testing"

	"macdoc/internal/engine"
	"macdoc/internal/planner"
)

// The dashboard shows disk-sourced names cleaned, but plans from the
// engine's data as it was: a cleaned target ("x\x1b[0m" → "x") could name a
// different folder.
func TestTUIShowsCleanedPlansRaw(t *testing.T) {
	const hostile = "evil\x1b[2K\r SAFE fake\x1b[8m"
	data := tuiFixture()
	ops := data.Res.Opportunities.Opportunities
	ops[0].Title, ops[0].Target = hostile, "/Users/me/code/"+hostile
	m := step(t, newTestTUI(t, 120, 40), cli.LoadedMsg{Data: data})
	m = step(t, m, keys("2")...)

	view := m.View()
	if strings.Contains(view, "\x1b[2K") || strings.Contains(view, "\r") || strings.Contains(view, "\x1b[8m") {
		t.Errorf("the dashboard printed terminal escapes from a disk name: %q", view)
	}
	mustContain(t, view, "evil")

	var pre *engine.OpportunityResult
	m.Deps.MakePlan = func(_ *cli.TuiDeps, _ planner.Selector, p *engine.OpportunityResult) (engine.PlanResult, error) {
		pre = p
		return samplePlan(), nil
	}
	m = step(t, m, keys("p")...)
	_ = runPlan(t, m)
	if pre == nil || pre.Opportunities[0].Target != "/Users/me/code/"+hostile || pre.Opportunities[0].Title != hostile {
		t.Fatalf("the dashboard planned from cleaned data: %+v", pre)
	}
	// The message the dashboard keeps for itself is the display copy.
	if got := m.Data.Res.Opportunities.Opportunities[0].Target; strings.ContainsRune(got, 0x1b) {
		t.Errorf("display copy not cleaned: %q", got)
	}
}
