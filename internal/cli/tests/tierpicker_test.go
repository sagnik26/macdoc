package tests

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/model"
)

// pickerRows is one row per risk type: two SAFE, one CAUTION, one RISKY and one
// advice-only item, or without RISKY when emptyRisky is set.
func pickerRows(emptyRisky bool) []cli.TierChoice {
	op := func(id string, r model.Risk, m model.Method) model.Opportunity {
		return model.Opportunity{ID: id, Risk: r, Method: m, Title: id, Bytes: model.Bytes{Value: 1 << 30, Source: model.SourceMeasured}}
	}
	ops := []model.Opportunity{
		op("O-safe01", model.RiskSafe, model.MethodQuarantine), op("O-safe02", model.RiskSafe, model.MethodQuarantine),
		op("O-caut01", model.RiskCaution, model.MethodQuarantine),
		op("O-advi01", model.RiskAdvise, model.MethodAdvise),
	}
	if !emptyRisky {
		ops = append(ops, op("O-risk01", model.RiskRisky, model.MethodQuarantine))
	}
	return cli.TierChoices(ops)
}

// pickerPress feeds keys to the picker and returns the model after them.
func pickerPress(t *testing.T, m cli.TierPicker, ks ...tea.Msg) cli.TierPicker {
	t.Helper()
	for _, k := range ks {
		next, _ := m.Update(k)
		var ok bool
		if m, ok = next.(cli.TierPicker); !ok {
			t.Fatalf("Update returned %T", next)
		}
	}
	return m
}

func ticked(m cli.TierPicker) string {
	var s []string
	for _, r := range m.Chosen() {
		s = append(s, string(r))
	}
	return strings.Join(s, ",")
}

// SAFE starts ticked, the others do not, and advice-only items have no row.
func TestTierPickerStartsWithSafeTicked(t *testing.T) {
	m := cli.NewTierPicker(cli.PlainTheme(), pickerRows(false))
	mustContain(t, m.Screen(), "[x] SAFE", "[ ] CAUTION", "[ ] RISKY", "2 items", "enter/space tick", "Clean 2 items")
	if got := ticked(m); got != "SAFE" {
		t.Errorf("ticked = %q, want SAFE", got)
	}
}

// Arrow keys move, space and Enter tick (several can be ticked), and only
// Enter on the separate Clean button submits what is ticked, in the order
// SAFE, CAUTION, RISKY.
func TestTierPickerTicksAndSubmits(t *testing.T) {
	m := cli.NewTierPicker(cli.PlainTheme(), pickerRows(false))
	m = pickerPress(t, m, keys("down")[0], keys(" ")[0], keys("down")[0], keys(" ")[0])
	if got := ticked(m); got != "SAFE,CAUTION,RISKY" {
		t.Fatalf("ticked = %q", got)
	}
	mustContain(t, m.Screen(), "[x] CAUTION", "[x] RISKY")
	m = pickerPress(t, m, keys(" ")[0]) // untick RISKY again
	if got := ticked(m); got != "SAFE,CAUTION" {
		t.Errorf("after unticking RISKY: %q", got)
	}
	// Enter on a type ticks it and never submits.
	m = pickerPress(t, m, keys("enter")[0])
	if m.Done() || ticked(m) != "SAFE,CAUTION,RISKY" {
		t.Fatalf("enter on a type should only tick it: done %v ticked %q", m.Done(), ticked(m))
	}
	m = pickerPress(t, m, keys("enter")[0]) // and unticks it again
	if m.Done() || ticked(m) != "SAFE,CAUTION" {
		t.Fatalf("enter again should untick: done %v ticked %q", m.Done(), ticked(m))
	}
	// Down to the Clean button; Enter there submits.
	m = pickerPress(t, m, keys("down")...)
	mustContain(t, m.Screen(), "▸ ", "Clean 3 items", "┏", "┗")
	m = pickerPress(t, m, keys("enter")[0])
	if !m.Done() || m.Canceled() || ticked(m) != "SAFE,CAUTION" {
		t.Errorf("enter on Clean: done %v canceled %v ticked %q", m.Done(), m.Canceled(), ticked(m))
	}
}

// ADVICE can never be ticked and says why. An empty RISKY row isn't shown at
// all (reclaim leaves installed apps to retire). "a"
// ticks every row that can be cleaned (never ADVICE) and a second "a" unticks.
func TestTierPickerAdviceAndEmptyRowsCannotBeTicked(t *testing.T) {
	rows := pickerRows(true) // TierChoices gives no ADVICE row; the picker still refuses one if it is given
	if strings.Contains(cli.NewTierPicker(cli.PlainTheme(), rows).Screen(), "ADVICE") {
		t.Error("an ADVICE row is shown")
	}
	rows = append(rows, cli.TierChoice{Risk: model.RiskAdvise, Note: "macdoc can't remove these",
		Items: []model.Opportunity{{ID: "O-advi01", Risk: model.RiskAdvise, Method: model.MethodAdvise, Title: "x"}}})
	m := cli.NewTierPicker(cli.PlainTheme(), rows)
	if strings.Contains(m.Screen(), "RISKY") {
		t.Error("an empty RISKY row is shown")
	}
	m = pickerPress(t, m, keys("down", "down")...) // CAUTION, ADVICE
	m = pickerPress(t, m, keys(" ")[0])
	mustContain(t, m.Screen(), "can't remove advice-only", "[-] ADVICE")
	if got := ticked(m); got != "SAFE" {
		t.Errorf("ADVICE was ticked: %q", got)
	}
	m = pickerPress(t, m, keys("a")[0])
	if got := ticked(m); got != "SAFE,CAUTION" {
		t.Errorf("a should tick SAFE and CAUTION only: %q", got)
	}
	m = pickerPress(t, m, keys("a")[0])
	if got := ticked(m); got != "" {
		t.Errorf("a again should untick them all: %q", got)
	}
}

// Enter with nothing ticked does not submit; Esc and Ctrl+C cancel.
func TestTierPickerNeedsAChoiceAndCancels(t *testing.T) {
	m := cli.NewTierPicker(cli.PlainTheme(), pickerRows(false))
	m = pickerPress(t, m, keys(" ")[0]) // untick SAFE
	mustContain(t, m.Screen(), "Clean: nothing ticked")
	m = pickerPress(t, m, keys("down", "down", "down", "down", "enter")...) // to the Clean button, then Enter
	if m.Done() {
		t.Error("Clean with nothing ticked should not submit")
	}
	mustContain(t, m.Screen(), "Tick at least one risk type")

	for _, k := range []tea.Msg{keys("esc")[0], tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}} {
		c := pickerPress(t, cli.NewTierPicker(cli.PlainTheme(), pickerRows(false)), k)
		if !c.Done() || !c.Canceled() {
			t.Errorf("%v should cancel", k)
		}
	}
}
