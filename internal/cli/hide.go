package cli

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sagnik26/macdoc/internal/config"
)

// hideCurrent is the x key on the Suggestions tab: it hides the kind of the
// highlighted suggestion (its recipe) in the settings file, the same as
// `macdoc config hide`, and reads the list again. Hiding only removes things
// from view; macdoc config unhide brings them back.
func (m TuiModel) hideCurrent() (tea.Model, tea.Cmd) {
	ops := m.Items()
	if m.Deps == nil || m.Cursor >= len(ops) {
		return m, nil
	}
	op := ops[m.Cursor]
	c, err := config.Load(m.Deps.Home)
	if err == nil {
		err = c.Exclude(op.RecipeID)
	}
	if err == nil {
		err = config.Save(m.Deps.Home, c)
	}
	if err != nil {
		m.Flash = "Couldn't hide it: " + err.Error()
		return m, nil
	}
	m.Flash = "Hidden: " + op.RecipeID + ". Show it again with: macdoc config unhide " + op.RecipeID
	if m.Marked != nil {
		delete(m.Marked, op.ID)
	}
	cmd := m.StartLoad(false)
	return m, cmd
}
