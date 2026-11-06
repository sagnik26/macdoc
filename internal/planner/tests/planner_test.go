package tests

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/planner"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/safety"
	"github.com/sagnik26/macdoc/internal/store"
)

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// world is a fake home with a catalog, a policy and ranked opportunities:
// two SAFE (one native, one quarantine), one CAUTION quarantine and one
// advice-only item.
type world struct {
	home string
	cat  *recipes.Catalog
	pol  *safety.Policy
	ops  []model.Opportunity
}

func newWorld(t *testing.T) world {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"Library/Logs/Zoom", "Library/Logs/Old", ".nvm/versions/node/v18.0.0"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := recipes.ToolInstalled
	recipes.ToolInstalled = func(string) bool { return true }
	t.Cleanup(func() { recipes.ToolInstalled = old })

	cat := &recipes.Catalog{Recipes: []recipes.Recipe{
		{ID: "brew.cleanup", Risk: model.RiskSafe, Method: model.MethodNative, Native: &recipes.Native{Argv: []string{"brew", "cleanup", "--prune=all"}}},
		{ID: "user.logs", Risk: model.RiskSafe, Method: model.MethodQuarantine, Allow: []string{"~/Library/Logs/*"}},
		{ID: "nvm.old_versions", Risk: model.RiskCaution, Method: model.MethodQuarantine, Allow: []string{"~/.nvm/versions/node/*"}, Preconditions: []string{"app_not_running:Xcode"}},
		{ID: "ollama.model", Risk: model.RiskCaution, Method: model.MethodNative, Native: &recipes.Native{Argv: []string{"ollama", "rm", "{id}"}}},
		{ID: "xcode.sim_runtimes_orphaned", Risk: model.RiskAdvise, Method: model.MethodAdvise},
	}}
	pol, err := safety.NewPolicy(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	mb := func(n int64) model.Bytes { return model.Bytes{Value: n * 1_000_000, Source: model.SourceMeasured} }
	ops := []model.Opportunity{
		{ID: "O-brewaa", RecipeID: "brew.cleanup", Title: "Homebrew old versions", Risk: model.RiskSafe, Method: model.MethodNative, Bytes: mb(400), Command: []string{"brew", "cleanup", "--prune=all"}},
		{ID: "O-logsaa", RecipeID: "user.logs", Title: "Old app logs · Zoom", Risk: model.RiskSafe, Method: model.MethodQuarantine, Bytes: mb(9), Target: filepath.Join(home, "Library/Logs/Zoom")},
		{ID: "O-nodeaa", RecipeID: "nvm.old_versions", Title: "Node.js v18.0.0", Risk: model.RiskCaution, Method: model.MethodQuarantine, Bytes: mb(160), Target: filepath.Join(home, ".nvm/versions/node/v18.0.0")},
		{ID: "O-modelb", RecipeID: "ollama.model", Title: "llama3:8b", Risk: model.RiskCaution, Method: model.MethodNative, Bytes: model.Bytes{Source: model.SourceUnknown}, Command: []string{"ollama", "rm", "llama3:8b"}},
		{ID: "O-simsaa", RecipeID: "xcode.sim_runtimes_orphaned", Title: "Leftover runtimes", Risk: model.RiskAdvise, Method: model.MethodAdvise, Bytes: mb(30000)},
	}
	return world{home: home, cat: cat, pol: pol, ops: ops}
}

func (w world) input() planner.Input {
	return planner.Input{ScanID: "S-TEST", ScanFinished: now.Add(-time.Hour), Opportunities: w.ops, Catalog: w.cat, Policy: w.pol, Now: now}
}

func ids(p model.Plan) []string {
	var out []string
	for _, it := range p.Items {
		out = append(out, it.OpportunityID)
	}
	return out
}

func refusedIDs(rs []planner.Refusal) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func TestParseIDs(t *testing.T) {
	got, err := planner.ParseIDs([]string{"O-AB23CD", "ab23cd", "o-ab23cd", "O-zz7qwe"})
	if err != nil || !slices.Equal(got, []string{"O-ab23cd", "O-zz7qwe"}) {
		t.Errorf("got %v, %v", got, err)
	}
	for _, bad := range []string{"/Users/me/Library", "~/Library/Caches", "../x", "Library/Logs", "O-ab12cd.json", "O-ab", "rm -rf", "O-ab12c1", "P-7F3K"} {
		if _, err := planner.ParseIDs([]string{bad}); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if _, err := planner.ParseIDs([]string{"~/x"}); err == nil || !strings.Contains(err.Error(), "never paths") {
		t.Errorf("a path must be refused as a path: %v", err)
	}
}

func TestBuildSelectors(t *testing.T) {
	w := newWorld(t)
	cases := []struct {
		sel          planner.Selector
		want, refuse []string
	}{
		{planner.Selector{Tier: model.RiskSafe}, []string{"O-brewaa", "O-logsaa"}, nil},
		{planner.Selector{Tier: model.RiskCaution}, []string{"O-nodeaa", "O-modelb"}, nil},
		{planner.Selector{Recipe: "user.logs"}, []string{"O-logsaa"}, nil},
		// IDs: sorted by tier then size; unknown and advice-only are refused.
		{planner.Selector{IDs: []string{"O-nodeaa", "O-nope22", "O-brewaa", "O-simsaa"}}, []string{"O-brewaa", "O-nodeaa"}, []string{"O-nope22", "O-simsaa"}},
		{planner.Selector{Recipe: "xcode.sim_runtimes_orphaned"}, nil, []string{"O-simsaa"}},
	}
	for _, c := range cases {
		p, refused, err := planner.Build(w.input(), c.sel)
		if len(c.want) == 0 {
			if !errors.Is(err, planner.ErrNothing) {
				t.Errorf("%s: err = %v, want planner.ErrNothing", c.sel.Request(), err)
			}
		} else if err != nil {
			t.Fatalf("%s: %v", c.sel.Request(), err)
		}
		if !slices.Equal(ids(p), c.want) || !slices.Equal(refusedIDs(refused), c.refuse) {
			t.Errorf("%s: items %v refused %v, want %v and %v", c.sel.Request(), ids(p), refusedIDs(refused), c.want, c.refuse)
		}
	}
}

func TestBuildItems(t *testing.T) {
	w := newWorld(t)
	p, _, err := planner.Build(w.input(), planner.Selector{IDs: []string{"O-brewaa", "O-nodeaa", "O-modelb"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "" || p.SHA256 != "" || p.ScanID != "S-TEST" || p.Origin != model.OriginCLI || !p.ExpiresAt.Equal(now.Add(planner.DefaultTTL)) {
		t.Errorf("plan header = %+v", p)
	}
	if p.TotalBytes != 560_000_000 {
		t.Errorf("total = %d (unknown sizes must not count)", p.TotalBytes)
	}
	brew, node, model3 := p.Items[0], p.Items[1], p.Items[2]
	if !slices.Equal(brew.Argv, []string{"brew", "cleanup", "--prune=all"}) || brew.Fingerprint != (model.Fingerprint{}) {
		t.Errorf("native item = %+v", brew)
	}
	if len(node.Argv) != 0 || node.Fingerprint.Ino == 0 || node.Fingerprint.MtimeNS == 0 || !slices.Equal(node.Preconditions, []string{"app_not_running:Xcode"}) {
		t.Errorf("quarantine item = %+v", node)
	}
	if model3.BytesSource != model.SourceUnknown || model3.ExpectedBytes != 0 {
		t.Errorf("unknown size = %+v", model3)
	}
}

// Targets are checked as they are now; anything changed is refused, never
// fixed up.
func TestBuildRechecksTargets(t *testing.T) {
	w := newWorld(t)
	logs := filepath.Join(w.home, "Library/Logs/Zoom")
	other := filepath.Join(w.home, "Library/Logs/Old")
	check := func(name, wantReason string, ops ...model.Opportunity) {
		t.Helper()
		in := w.input()
		in.Opportunities = ops
		_, refused, err := planner.Build(in, planner.Selector{IDs: []string{ops[0].ID}})
		if !errors.Is(err, planner.ErrNothing) || len(refused) != 1 || !strings.Contains(refused[0].Reason, wantReason) {
			t.Errorf("%s: err %v, refused %+v, want %q", name, err, refused, wantReason)
		}
	}
	op := w.ops[1]

	if err := os.Remove(logs); err != nil {
		t.Fatal(err)
	}
	check("gone", "no longer exists", op)
	if err := os.Symlink(other, logs); err != nil {
		t.Fatal(err)
	}
	check("symlink", "now a symlink", op)

	doc := op
	doc.Target = filepath.Join(w.home, "Documents")
	if err := os.MkdirAll(doc.Target, 0o755); err != nil {
		t.Fatal(err)
	}
	check("protected", "safety rules refuse", doc)

	bad := w.ops[0]
	bad.Command = []string{"brew", "cleanup", "--prune=all", "--force"}
	check("edited command", "doesn't match the recipe", bad)

	recipes.ToolInstalled = func(string) bool { return false }
	check("tool gone", "isn't installed", w.ops[0])

	unknown := w.ops[1]
	unknown.RecipeID = "made.up"
	check("unknown recipe", "recipe isn't in", unknown)
}

func TestBuildRefusesStaleScan(t *testing.T) {
	w := newWorld(t)
	in := w.input()
	in.ScanFinished = now.Add(-7 * time.Hour)
	if _, _, err := planner.Build(in, planner.Selector{Tier: model.RiskSafe}); !errors.Is(err, planner.ErrStaleScan) {
		t.Errorf("err = %v", err)
	}
}

// sealed builds, saves and loads a plan in a fresh store.
func sealed(t *testing.T, w world) (*store.Store, string, model.Plan) {
	t.Helper()
	st, err := store.Open(filepath.Join(w.home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p, _, err := planner.Build(w.input(), planner.Selector{IDs: []string{"O-brewaa", "O-logsaa", "O-nodeaa"}})
	if err != nil {
		t.Fatal(err)
	}
	dir := planner.Dir(filepath.Join(w.home, "state.db"))
	if err := planner.Save(st, dir, &p); err != nil {
		t.Fatal(err)
	}
	return st, dir, p
}

func verifyFile(t *testing.T, st *store.Store, dir, id string, w world, at time.Time) error {
	t.Helper()
	p, err := planner.Load(dir, id)
	if err != nil {
		return err
	}
	rec, err := st.Plan(id)
	if err != nil {
		return err
	}
	return planner.Verify(p, rec, w.cat, w.pol, at)
}

func TestSaveLoadVerify(t *testing.T) {
	w := newWorld(t)
	st, dir, p := sealed(t, w)
	if !planner.ValidID(p.ID) || len(p.SHA256) != 64 {
		t.Fatalf("id %q hash %q", p.ID, p.SHA256)
	}
	fi, err := os.Stat(filepath.Join(dir, p.ID+".json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("plan file: %v %v", fi, err)
	}
	if err := verifyFile(t, st, dir, p.ID, w, now.Add(time.Minute)); err != nil {
		t.Errorf("fresh plan: %v", err)
	}
	if err := verifyFile(t, st, dir, p.ID, w, now.Add(planner.DefaultTTL)); !errors.Is(err, planner.ErrExpired) {
		t.Errorf("after 30 min: %v, want planner.ErrExpired", err)
	}
	if planner.StatusOf(nil) != planner.StatusPending || planner.StatusOf(planner.ErrExpired) != planner.StatusExpired || planner.StatusOf(planner.ErrTampered) != planner.StatusInvalid {
		t.Error("StatusOf")
	}
	if _, err := planner.Load(dir, "../state"); err == nil {
		t.Error("Load must refuse an ID that isn't one")
	}
}

// Done-when: a plan file edited by hand is rejected.
func TestEditedPlanIsRejected(t *testing.T) {
	edits := map[string]func(m map[string]any){
		"command":        func(m map[string]any) { item(m, 0)["argv"] = []any{"brew", "cleanup", "--prune=all", "--force"} },
		"target":         func(m map[string]any) { item(m, 1)["target"] = "/Users/someone/Documents" },
		"risk":           func(m map[string]any) { item(m, 2)["risk"] = "SAFE" },
		"expiry":         func(m map[string]any) { m["expires_at"] = now.Add(24 * time.Hour).Format(time.RFC3339Nano) },
		"added item":     func(m map[string]any) { m["items"] = append(m["items"].([]any), item(m, 1)) },
		"unknown field":  func(m map[string]any) { m["force"] = true },
		"removed seal":   func(m map[string]any) { delete(m, "sha256") },
		"scan":           func(m map[string]any) { m["scan_id"] = "S-OTHER" },
		"expected bytes": func(m map[string]any) { item(m, 0)["expected_bytes"] = 1 },
	}
	for name, edit := range edits {
		w := newWorld(t)
		st, dir, p := sealed(t, w)
		path := filepath.Join(dir, p.ID+".json")
		var m map[string]any
		b, _ := os.ReadFile(path)
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		edit(m)
		b, _ = json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := verifyFile(t, st, dir, p.ID, w, now.Add(time.Minute)); !errors.Is(err, planner.ErrTampered) {
			t.Errorf("%s: err = %v, want planner.ErrTampered", name, err)
		}
	}
}

func item(m map[string]any, i int) map[string]any { return m["items"].([]any)[i].(map[string]any) }

// Re-sealing an edited file isn't enough: the store holds the original
// seal. And even a plan whose file and store row were both forged must
// contain only items the planner could have produced.
func TestForgedPlans(t *testing.T) {
	w := newWorld(t)
	st, dir, p := sealed(t, w)

	forged := p
	forged.Items = append([]model.PlanItem(nil), p.Items...)
	forged.Items[0].Argv = []string{"brew", "uninstall", "--force", "git"}
	forged.SHA256, _ = planner.Hash(forged)
	rec, _ := st.Plan(p.ID)
	if err := planner.Verify(forged, rec, w.cat, w.pol, now); !errors.Is(err, planner.ErrTampered) {
		t.Errorf("re-sealed file: %v, want planner.ErrTampered", err)
	}
	rec.SHA256 = forged.SHA256
	if err := planner.Verify(forged, rec, w.cat, w.pol, now); !errors.Is(err, planner.ErrInvalid) {
		t.Errorf("forged file and row, bad command: %v, want planner.ErrInvalid", err)
	}

	forged.Items = append([]model.PlanItem(nil), p.Items...)
	forged.Items[1].Target = filepath.Join(w.home, "Documents")
	forged.SHA256, _ = planner.Hash(forged)
	rec.SHA256 = forged.SHA256
	if err := planner.Verify(forged, rec, w.cat, w.pol, now); !errors.Is(err, planner.ErrInvalid) {
		t.Errorf("forged protected target: %v, want planner.ErrInvalid", err)
	}

	forged.Items = append([]model.PlanItem(nil), p.Items...)
	forged.Items[2].Risk = model.RiskSafe // below its recipe's CAUTION
	forged.SHA256, _ = planner.Hash(forged)
	rec.SHA256 = forged.SHA256
	if err := planner.Verify(forged, rec, w.cat, w.pol, now); !errors.Is(err, planner.ErrInvalid) {
		t.Errorf("forged lower risk: %v, want planner.ErrInvalid", err)
	}

	applied, _ := st.Plan(p.ID)
	applied.Status = store.PlanApplied
	if err := planner.Verify(p, applied, w.cat, w.pol, now); !errors.Is(err, planner.ErrApplied) {
		t.Errorf("applied: %v", err)
	}
	_ = dir
}

func TestLoadRejectsTrailingData(t *testing.T) {
	w := newWorld(t)
	_, dir, p := sealed(t, w)
	path := filepath.Join(dir, p.ID+".json")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"items":[]}`)
	_ = f.Close()
	if _, err := planner.Load(dir, p.ID); !errors.Is(err, planner.ErrTampered) {
		t.Errorf("err = %v", err)
	}
}

func TestWriteNewNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "P-AAAA.json")
	if err := planner.WriteNew(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := planner.WriteNew(path, []byte("two")); !errors.Is(err, os.ErrExist) {
		t.Errorf("second write: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "one" {
		t.Errorf("file = %q", b)
	}
}

func TestPlanIDs(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		id, err := planner.NewID()
		if err != nil || !planner.ValidID(id) || strings.ContainsAny(id[2:], "IO01") {
			t.Fatalf("id %q %v", id, err)
		}
		seen[id] = true
	}
	if len(seen) < 190 {
		t.Errorf("only %d distinct IDs in 200", len(seen))
	}
}

// Actions follow plan order, one per item, and planner.Perform keeps going after a
// failure.
func TestActionsAndPerform(t *testing.T) {
	w := newWorld(t)
	p, _, _ := planner.Build(w.input(), planner.Selector{IDs: []string{"O-nodeaa", "O-brewaa", "O-logsaa"}})
	rec := &recorder{failOn: "O-brewaa"}
	res := planner.Perform(p, rec)
	if len(res) != 3 || !errors.Is(res[0].Err, errBoom) || res[1].Err != nil || res[2].Err != nil {
		t.Errorf("results = %+v", res)
	}
	want := []string{"native O-brewaa brew cleanup --prune=all", "quarantine O-logsaa " + p.Items[1].Target, "quarantine O-nodeaa " + p.Items[2].Target}
	if !slices.Equal(rec.calls, want) {
		t.Errorf("calls = %q, want %q", rec.calls, want)
	}
}

var errBoom = errors.New("boom")

type recorder struct {
	calls  []string
	failOn string
}

func (r *recorder) RunNative(it model.PlanItem, argv []string) error {
	r.calls = append(r.calls, "native "+it.OpportunityID+" "+strings.Join(argv, " "))
	if it.OpportunityID == r.failOn {
		return errBoom
	}
	return nil
}

func (r *recorder) Quarantine(it model.PlanItem, target string) error {
	r.calls = append(r.calls, "quarantine "+it.OpportunityID+" "+target)
	return nil
}
