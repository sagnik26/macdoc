package tests

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/config"
)

// `config hide` takes a recipe ID or an O-… ID from the list, refuses
// anything that matches no recipe, hides it, and says so in the list's notes;
// `unhide` brings it back.
func TestConfigHideAndUnhide(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "reclaim")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "npm cache")

	if _, err := run(t, "config", "hide", "no.such_recipe"); err == nil || !strings.Contains(err.Error(), "matches no recipe") {
		t.Errorf("an unknown recipe was accepted: %v", err)
	}
	if _, err := run(t, "config", "hide", "O-zzzzzz"); err == nil || !strings.Contains(err.Error(), "isn't in the latest list") {
		t.Errorf("an unknown O-id was accepted: %v", err)
	}
	if out, err := run(t, "config", "hide", "npm.cache"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = run(t, "reclaim")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "npm cache") {
		t.Errorf("the hidden suggestion is still listed:\n%s", out)
	}
	mustContain(t, out, "hidden by your settings", "macdoc config unhide")
	out, _ = run(t, "config")
	mustContain(t, out, "npm.cache")

	if _, err := run(t, "config", "unhide", "npm.cache"); err != nil {
		t.Fatal(err)
	}
	out, _ = run(t, "reclaim")
	mustContain(t, out, "npm cache")
}

// x on the Suggestions tab hides the kind of the highlighted suggestion in the
// settings file and reads the list again; nothing else changes.
func TestTUIHideKeyHidesTheRecipe(t *testing.T) {
	home := t.TempDir()
	deps := &cli.TuiDeps{Home: home, Load: func(context.Context, *cli.TuiDeps, bool) (*cli.TuiData, error) { return tuiFixture(), nil }}
	m := cli.NewTUIModel(deps, cli.PlainLayoutTheme())
	m.InitCmd = m.StartLoad(false)
	m = step(t, m, tea.WindowSizeMsg{Width: 110, Height: 40}, cli.LoadedMsg{Data: tuiFixture()})
	m = step(t, m, keys("tab")...)
	want := m.Items()[m.Cursor].RecipeID

	m = step(t, m, keys("x")...)
	mustContain(t, m.Flash, "Hidden: "+want, "macdoc config unhide "+want)
	if !m.Loading {
		t.Error("the list should be read again after hiding")
	}
	c, err := config.Load(home)
	if err != nil || !c.Excludes(want) {
		t.Fatalf("settings after x: %v %v", c.ExcludedRecipes, err)
	}

	// On another tab x does nothing.
	m = step(t, loaded(t, 110, 40), keys("x")...)
	if m.Flash != "" && strings.Contains(m.Flash, "Hidden") {
		t.Errorf("x hid something on the Overview tab: %q", m.Flash)
	}
}
