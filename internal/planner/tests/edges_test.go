package tests

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// Edge cases found by mutation testing: the text of a
// selector, ID normalization, the order of plan items, how a plan file is read
// back, and the status a verification result maps to.

func TestSelectorRequestText(t *testing.T) {
	for _, tt := range []struct {
		sel  planner.Selector
		want string
	}{
		{planner.Selector{IDs: []string{"O-aaaaaa", "O-bbbbbb"}}, "O-aaaaaa O-bbbbbb"},
		{planner.Selector{IDs: []string{"O-aaaaaa"}, Tier: model.RiskSafe}, "O-aaaaaa"},
		{planner.Selector{Tier: model.RiskSafe}, "--safe"},
		{planner.Selector{Tier: model.RiskCaution}, "--tier caution"},
		{planner.Selector{Tier: model.RiskRisky}, "--tier risky"},
		{planner.Selector{Recipe: "brew.cleanup"}, "--recipe brew.cleanup"},
	} {
		if got := tt.sel.Request(); got != tt.want {
			t.Errorf("Request(%+v) = %q, want %q", tt.sel, got, tt.want)
		}
	}
}

func TestNormalizeID(t *testing.T) {
	for _, tt := range []struct {
		in, want string
		ok       bool
	}{
		{"O-AB23CD", "O-ab23cd", true},
		{"o-ab23cd", "O-ab23cd", true},
		{"ab23cd", "O-ab23cd", true},
		{"  ab23cd\t", "O-ab23cd", true}, // surrounding whitespace is dropped
		{"ab23c", "", false},             // too short
		{"ab23cdſ", "", false},           // U+017F folds to s, so it must be refused first
		{"Kb23cd", "", false},            // the Kelvin sign folds to k
		{"ab23cé", "", false},
		{"", "", false},
	} {
		got, ok := planner.Normalize(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("NormalizeID(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
	// Parse trims before it looks for path characters, so a padded ID is one ID.
	if got, err := planner.Parse([]string{" ab23cd ", "AB23CD"}); err != nil || !slices.Equal(got, []string{"O-ab23cd"}) {
		t.Errorf("ParseIDs padded = %v, %v", got, err)
	}
}

// Items are grouped SAFE, CAUTION, RISKY; within a group the biggest comes
// first; equal sizes fall back to the ID.
func TestPlanItemOrder(t *testing.T) {
	w := newWorld(t)
	mb := func(n int64) model.Bytes { return model.Bytes{Value: n * 1_000_000, Source: model.SourceMeasured} }
	cmd := []string{"brew", "cleanup", "--prune=all"}
	w.cat.Recipes = append(w.cat.Recipes, w.cat.Recipes[0])
	w.cat.Recipes[len(w.cat.Recipes)-1].ID = "brew.risky"
	w.cat.Recipes[len(w.cat.Recipes)-1].Risk = model.RiskRisky
	w.ops = []model.Opportunity{
		{ID: "O-risky1", RecipeID: "brew.risky", Risk: model.RiskRisky, Method: model.MethodNative, Bytes: mb(900), Command: cmd},
		{ID: "O-cautn1", RecipeID: "ollama.model", Risk: model.RiskCaution, Method: model.MethodNative, Bytes: mb(50), Command: []string{"ollama", "rm", "m"}},
		{ID: "O-safeb2", RecipeID: "brew.cleanup", Risk: model.RiskSafe, Method: model.MethodNative, Bytes: mb(10), Command: cmd},
		{ID: "O-safea3", RecipeID: "brew.cleanup", Risk: model.RiskSafe, Method: model.MethodNative, Bytes: mb(10), Command: cmd},
		{ID: "O-safebig", RecipeID: "brew.cleanup", Risk: model.RiskSafe, Method: model.MethodNative, Bytes: mb(200), Command: cmd},
	}
	p, _, err := planner.Build(w.input(), planner.Selector{IDs: []string{"O-risky1", "O-cautn1", "O-safeb2", "O-safea3", "O-safebig"}})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(p.Items))
	for i, it := range p.Items {
		got[i] = it.OpportunityID
	}
	want := []string{"O-safebig", "O-safea3", "O-safeb2", "O-cautn1", "O-risky1"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestSelectorByTierAndRecipe(t *testing.T) {
	w := newWorld(t)
	byTier, _, err := planner.Build(w.input(), planner.Selector{Tier: model.RiskSafe})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(byTier); !slices.Equal(got, []string{"O-brewaa", "O-logsaa"}) {
		t.Errorf("--safe = %v", got)
	}
	byRecipe, _, err := planner.Build(w.input(), planner.Selector{Recipe: "nvm.old_versions"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(byRecipe); !slices.Equal(got, []string{"O-nodeaa"}) {
		t.Errorf("--recipe = %v", got)
	}
	// An empty selector selects nothing; it must not select everything.
	if _, _, err := planner.Build(w.input(), planner.Selector{}); !errors.Is(err, planner.ErrNothing) {
		t.Errorf("empty selector: %v, want ErrNothing", err)
	}
}

func TestStatusOfApplied(t *testing.T) {
	if got := planner.Classify(planner.ErrApplied); got != planner.Applied {
		t.Errorf("StatusOf(ErrApplied) = %q", got)
	}
	if got := planner.Classify(planner.ErrInvalid); got != planner.Invalid {
		t.Errorf("StatusOf(ErrInvalid) = %q", got)
	}
}

// Load must refuse what could be smuggled past the seal: unknown fields, an
// oversized file, a missing plan and anything that is not an ID.
func TestLoadRefusals(t *testing.T) {
	w := newWorld(t)
	_, dir, p := sealed(t, w)
	path := filepath.Join(dir, p.ID+".json")

	if _, err := planner.Load(dir, "P-ZZZZ"); !errors.Is(err, planner.ErrUnknown) {
		t.Errorf("missing plan: %v, want ErrUnknown", err)
	}
	if _, err := planner.Load(dir, "not-an-id"); err == nil || !strings.Contains(err.Error(), "isn't a plan ID") {
		t.Errorf("bad ID: %v", err)
	}

	// An extra field that does not change the content hash would otherwise
	// load as the original plan.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["extra"] = "ignored if unknown fields were allowed"
	edited, _ := json.Marshal(m)
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Load(dir, p.ID); !errors.Is(err, planner.ErrTampered) {
		t.Errorf("unknown field: %v, want ErrTampered", err)
	}

	// Size limit: 4 MiB. Just under it the file is read (and, being junk,
	// rejected as unreadable); just over it the file is refused before parsing.
	under := append([]byte(nil), make([]byte, 3<<20+1<<19)...)
	for i := range under {
		under[i] = ' '
	}
	if err := os.WriteFile(path, under, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Load(dir, p.ID); !errors.Is(err, planner.ErrTampered) {
		t.Errorf("3.5 MiB file: %v, want it read and rejected as ErrTampered", err)
	}
	over := make([]byte, 4<<20+1)
	for i := range over {
		over[i] = ' '
	}
	if err := os.WriteFile(path, over, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Load(dir, p.ID); err == nil || errors.Is(err, planner.ErrTampered) {
		t.Errorf("4 MiB + 1 byte file: %v, want a size refusal that is not ErrTampered", err)
	}
}
