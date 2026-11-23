package cli

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sagnik26/macdoc/internal/model"
)

// /reclaim in the dashboard asks nothing. Once its own scan has finished it
// marks every SAFE and CAUTION suggestion (the same two risk types as
// `macdoc reclaim --tier safe|caution --yes`, which only quarantine, so they
// can be restored), seals a plan and starts removing at once, with progress
// by category. RISKY items are never included. Esc stops the run between items.

// autoReclaim is the /reclaim command once its scan has loaded.
func (m TuiModel) autoReclaim() (tea.Model, tea.Cmd) {
	if m.Data == nil {
		m.Flash = "Scan first: /reclaim works from a fresh scan."
		return m, nil
	}
	rows := TierChoices(withoutInstalledApps(m.Items()))
	n := 0
	for _, r := range rows {
		if r.Risk == model.RiskSafe || r.Risk == model.RiskCaution {
			n += len(r.Items)
		}
	}
	if n == 0 {
		m.Flash = "Nothing safe for macdoc to clean on its own."
		return m, nil
	}
	m.reclaimAuto = true
	return m.markChosenAndRemove([]model.Risk{model.RiskSafe, model.RiskCaution})
}

// markChosenAndRemove marks the actionable suggestions of the chosen risk types
// (and only those) and seals a plan for them.
func (m TuiModel) markChosenAndRemove(tiers []model.Risk) (tea.Model, tea.Cmd) {
	if m.Marked == nil {
		m.Marked = map[string]bool{}
	}
	clear(m.Marked)
	first := -1
	for i, op := range m.Items() {
		if installedAppRecipes[op.RecipeID] {
			continue // installed apps are left to retire
		}
		if op.Method == model.MethodAdvise || !slices.Contains(tiers, op.Risk) {
			continue
		}
		m.Marked[op.ID] = true
		if first < 0 {
			first = i
		}
	}
	if first < 0 {
		m.reclaimAuto = false
		m.Flash = "Nothing in what you chose."
		return m, nil
	}
	m.Cursor = first // startRemove treats an advice row under the cursor as the whole request
	return m.startRemove()
}
