package tests

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/model"
)

func itemOps() []model.Opportunity {
	op := func(id, title string, r model.Risk) model.Opportunity {
		return model.Opportunity{ID: id, Title: title, Risk: r, Method: model.MethodQuarantine, Bytes: model.Bytes{Value: 1 << 30, Source: model.SourceMeasured}}
	}
	return []model.Opportunity{op("O-safe01", "Xcode caches", model.RiskSafe), op("O-caut01", "Docker build cache", model.RiskCaution),
		op("O-risk01", "Android system image", model.RiskRisky)}
}

func pickKeys(t *testing.T, m cli.ItemPicker, ks ...string) cli.ItemPicker {
	t.Helper()
	for _, k := range ks {
		for _, msg := range keys(k) {
			next, _ := m.Update(msg)
			var ok bool
			if m, ok = next.(cli.ItemPicker); !ok {
				t.Fatalf("Update returned %T", next)
			}
		}
	}
	return m
}

func ids(ops []model.Opportunity) string {
	var s []string
	for _, op := range ops {
		s = append(s, op.ID)
	}
	return strings.Join(s, ",")
}

// The terminal list starts with nothing ticked; Enter with nothing ticked says so and
// does not finish; space ticks and moves down, a ticks all and then none.
func TestItemPickerStartsEmptyAndTicks(t *testing.T) {
	m := cli.NewItemPicker(cli.PlainLayoutTheme(), itemOps())
	mustContain(t, m.Screen(), "Choose what to delete", "nothing ticked yet", "Xcode caches", "RISKY", "Android system image")
	if len(m.Chosen()) != 0 {
		t.Fatalf("nothing is ticked at the start: %v", ids(m.Chosen()))
	}
	m = pickKeys(t, m, "enter")
	if m.Done() || !strings.Contains(m.Screen(), "Tick something first") {
		t.Fatalf("enter with nothing ticked should only say so: done=%v\n%s", m.Done(), m.Screen())
	}
	m = pickKeys(t, m, " ", "down", " ") // rows 1 and 3
	if got := ids(m.Chosen()); got != "O-safe01,O-risk01" {
		t.Fatalf("ticked = %s", got)
	}
	mustContain(t, m.Screen(), "2 ticked")
	m = pickKeys(t, m, "a")
	if len(m.Chosen()) != 3 {
		t.Errorf("a should tick all: %v", ids(m.Chosen()))
	}
	m = pickKeys(t, m, "a")
	if len(m.Chosen()) != 0 {
		t.Errorf("a again should untick all: %v", ids(m.Chosen()))
	}
}

// Enter submits what is ticked; esc cancels.
func TestItemPickerSubmitsOrCancels(t *testing.T) {
	m := pickKeys(t, cli.NewItemPicker(cli.PlainLayoutTheme(), itemOps()), "a", "enter")
	if !m.Done() || m.Canceled() || len(m.Chosen()) != 3 {
		t.Fatalf("enter should submit: done=%v canceled=%v chosen=%d", m.Done(), m.Canceled(), len(m.Chosen()))
	}
	m = pickKeys(t, cli.NewItemPicker(cli.PlainLayoutTheme(), itemOps()), "a", "esc")
	if !m.Done() || !m.Canceled() {
		t.Fatalf("esc should cancel: done=%v canceled=%v", m.Done(), m.Canceled())
	}
	next, _ := cli.NewItemPicker(cli.PlainLayoutTheme(), itemOps()).Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	if _, ok := next.(cli.ItemPicker); !ok {
		t.Fatalf("a resize returned %T", next)
	}
}

func planOf(risk model.Risk) model.Plan {
	return model.Plan{Origin: model.OriginCLI, Items: []model.PlanItem{
		{OpportunityID: "O-a", Title: "A", Risk: model.RiskSafe, Method: model.MethodQuarantine},
		{OpportunityID: "O-b", Title: "B", Risk: risk, Method: model.MethodQuarantine}}}
}

// y approves every ticked item, RISKY ones included, each on its own; anything else
// cancels and approves nothing.
func TestConfirmDeleteAsksYesOrNo(t *testing.T) {
	for _, c := range []struct {
		answer string
		yes    bool
	}{{"y\n", true}, {"YES\n", true}, {"n\n", false}, {"\n", false}, {"sure\n", false}} {
		var out bytes.Buffer
		approved, ok, err := cli.ConfirmDelete(bufio.NewReader(strings.NewReader(c.answer)), &out, cli.PlainLayoutTheme(), planOf(model.RiskRisky))
		if err != nil || ok != c.yes || (len(approved) == 2) != c.yes {
			t.Fatalf("answer %q: ok=%v approved=%d err=%v", c.answer, ok, len(approved), err)
		}
		if c.yes {
			for id, a := range approved {
				if !a.Individually {
					t.Errorf("%s: a RISKY item ticked and confirmed is its own approval", id)
				}
			}
		}
		mustContain(t, out.String(), "Delete these 2 items for good? [y/N]")
	}
	if _, _, err := cli.ConfirmDelete(bufio.NewReader(strings.NewReader("")), &bytes.Buffer{}, cli.PlainLayoutTheme(), planOf(model.RiskSafe)); err == nil {
		t.Error("no answer at all should be an error")
	}
	claude := planOf(model.RiskSafe)
	claude.Origin = model.OriginClaude
	if _, ok, err := cli.ConfirmDelete(bufio.NewReader(strings.NewReader("y\n")), &bytes.Buffer{}, cli.PlainLayoutTheme(), claude); ok || err == nil {
		t.Errorf("a plan Claude proposed must never be deleted on a y here: ok=%v err=%v", ok, err)
	}
	admin := planOf(model.RiskSafe)
	admin.Items[1].Privileged = true
	if _, ok, err := cli.ConfirmDelete(bufio.NewReader(strings.NewReader("y\n")), &bytes.Buffer{}, cli.PlainLayoutTheme(), admin); ok || err == nil {
		t.Errorf("an administrator step keeps apply's own confirmation: ok=%v err=%v", ok, err)
	}
}

// `reclaim --choose` needs a terminal, and is refused with --yes and --dry-run.
func TestReclaimChooseGuards(t *testing.T) {
	reclaimFixture(t)
	termOn(t, true)
	for _, args := range [][]string{{"reclaim", "--choose", "--yes"}, {"reclaim", "--choose", "--dry-run"}} {
		if _, err := runWithInput(t, "1\ny\n", args...); err == nil || !strings.Contains(err.Error(), "--choose can't be used with") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	termOn(t, false)
	if _, err := runWithInput(t, "1\ny\n", "reclaim", "--choose"); err == nil || !strings.Contains(err.Error(), "needs you at a terminal") {
		t.Errorf("piped: err = %v", err)
	}
}

// With a (faked) terminal it lists the numbered items, deletes only the numbers typed
// after a y, and leaves everything else. Anything but a y, or an empty line, deletes nothing.
func TestReclaimChooseDeletesOnlyWhatIsTyped(t *testing.T) {
	home, safe, caution, risky := reclaimFixture(t)
	termOn(t, true)

	out, err := runWithInput(t, "\n", "reclaim", "--choose")
	if err != nil || !strings.Contains(out, "Canceled. Nothing has been changed.") {
		t.Fatalf("an empty line cancels: %v\n%s", err, out)
	}
	out, err = runWithInput(t, "1\nn\n", "reclaim", "--choose")
	if err != nil || !strings.Contains(out, "Canceled. Nothing has been changed.") {
		t.Fatalf("n cancels: %v\n%s", err, out)
	}
	if !present(safe) || !present(caution) || !present(risky) {
		t.Fatal("something was removed without a y")
	}

	out, err = runWithInput(t, "all\ny\n", "reclaim", "--choose")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "Choose what to delete", "Safe cache", "Caution cache", "Risky cache", "Delete these 3 items for good? [y/N]")
	if present(safe) || present(caution) || present(risky) {
		t.Errorf("all three should be gone: safe=%v caution=%v risky=%v", present(safe), present(caution), present(risky))
	}
	log, err := os.ReadFile(home + "/Library/Application Support/macdoc/audit.jsonl")
	if err != nil || !strings.Contains(string(log), "t.risky") {
		t.Errorf("the audit log doesn't record the RISKY deletion: %v", err)
	}
}
