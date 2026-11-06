package tests

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// These tests cover ADR 0005 stage 2 through the real DerivedData recipe and
// a stored scan: a suggestion is sized by the node's private size, an unknown
// private size falls back to the allocated bytes, and a known 0 is a number.

const mb = 1_000_000

// derived stores a scan whose DerivedData holds three folders: one with files
// that share space with copies (private below bytes), one whose private size
// is unknown (a volume that cannot report it), one fully shared (known 0).
func derived(t *testing.T) (fixture, recipes.Recipe) {
	t.Helper()
	stubTools(t)
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := cat.Get("xcode.derived_data")
	if !ok {
		t.Fatal("no xcode.derived_data recipe")
	}
	f := newFixture(t, time.Hour)
	dd := f.p("Library/Developer/Xcode/DerivedData")
	old := f.now.Add(-100 * 24 * time.Hour).Unix()
	priv := func(n int64) *int64 { return &n }
	nodes := []model.Node{
		{Path: dd, Kind: model.NodeDir, Bytes: 12 * mb, Depth: 5},
		{Path: dd + "/Shared-a", Kind: model.NodeDir, Bytes: 5 * mb, Private: priv(2 * mb), MtimeMax: old, Depth: 6},
		{Path: dd + "/Plain-b", Kind: model.NodeDir, Bytes: 4 * mb, MtimeMax: old, Depth: 6},
		{Path: dd + "/Allshared-c", Kind: model.NodeDir, Bytes: 3 * mb, Private: priv(0), MtimeMax: old, Depth: 6},
	}
	for _, n := range nodes[1:] {
		if err := os.MkdirAll(n.Path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return withNodes(t, f, nodes...), r
}

func sizes(res engine.OpportunityResult) map[string]model.Bytes {
	out := map[string]model.Bytes{}
	for _, op := range res.Opportunities {
		out[op.Target[strings.LastIndex(op.Target, "/")+1:]] = op.Bytes
	}
	return out
}

// With the default threshold, a folder that would free under 1 MB is left out
// and counted in a note, like any other small suggestion; the sizes shown and
// the total are the lower bounds.
func TestSuggestionSizeIsTheLowerBound(t *testing.T) {
	f, r := derived(t)
	o := f.opts(r)
	o.MinBytes = 0 // the default: hide items that free under 1 MB
	res, err := engine.Opportunities(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	got := sizes(res)
	if len(got) != 2 || got["Shared-a"] != measured(2*mb) || got["Plain-b"] != measured(4*mb) {
		t.Fatalf("sizes = %+v; want Shared-a 2 MB (private) and Plain-b 4 MB (fallback to allocated)", got)
	}
	if res.Totals[model.RiskSafe] != 6*mb {
		t.Errorf("SAFE total %d; want the sum of the lower bounds, 6 MB", res.Totals[model.RiskSafe])
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "1 item(s) that would free under 1 MB not shown") {
		t.Errorf("warnings = %v; want the hidden fully shared folder counted", res.Warnings)
	}
}

// With nothing hidden, the fully shared folder is listed with a measured 0,
// last in the ranking, and the total does not change.
func TestKnownZeroIsListedAsZero(t *testing.T) {
	f, r := derived(t)
	res := f.run(t, r) // MinBytes -1: show everything
	got := sizes(res)
	if len(got) != 3 || got["Allshared-c"] != measured(0) {
		t.Fatalf("sizes = %+v; want Allshared-c as a measured 0", got)
	}
	if last := res.Opportunities[len(res.Opportunities)-1]; !strings.HasSuffix(last.Target, "Allshared-c") {
		t.Errorf("last suggestion is %s; want the one that frees nothing", last.Target)
	}
	if res.Totals[model.RiskSafe] != 6*mb {
		t.Errorf("SAFE total %d; a known 0 adds nothing", res.Totals[model.RiskSafe])
	}
}

// A plan is costed with the same lower bound.
func TestPlanIsCostedWithTheLowerBound(t *testing.T) {
	f, r := derived(t)
	o := engine.PlanOptions{OpportunityOptions: f.opts(r), Selector: planner.Selector{Tier: model.RiskSafe}}
	res, err := engine.CreatePlan(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"Shared-a": 2 * mb, "Plain-b": 4 * mb, "Allshared-c": 0}
	for _, it := range res.Plan.Items {
		name := it.Target[strings.LastIndex(it.Target, "/")+1:]
		if it.ExpectedBytes != want[name] || it.BytesSource != model.SourceMeasured {
			t.Errorf("%s: expected %d (%s); want %d measured", name, it.ExpectedBytes, it.BytesSource, want[name])
		}
	}
	if res.Plan.TotalBytes != 6*mb {
		t.Errorf("plan total %d; want 6 MB", res.Plan.TotalBytes)
	}
}
