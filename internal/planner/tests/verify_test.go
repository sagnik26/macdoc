package tests

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/store"
)

// These tests pin each clause of Verify, the gate between planning and
// applying, and the boundaries of Build. Mutation testing
// showed that dropping any single clause left every test passing.

// reseal returns p with a fresh seal and a store row that agrees with it, the
// way someone who forged both the file and the row would present it.
func reseal(t *testing.T, p model.Plan, rec store.PlanRecord) (model.Plan, store.PlanRecord) {
	t.Helper()
	h, err := planner.Hash(p)
	if err != nil {
		t.Fatal(err)
	}
	p.SHA256 = h
	rec.SHA256 = h
	return p, rec
}

func TestVerifyChecksEveryTieBetweenFileAndStore(t *testing.T) {
	w := newWorld(t)
	st, _, p := sealed(t, w)
	rec, err := st.Plan(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Minute)
	if err := planner.Verify(p, rec, w.cat, w.pol, at); err != nil {
		t.Fatalf("untouched plan: %v", err)
	}

	tests := map[string]func(r *store.PlanRecord){
		"store row has another ID":      func(r *store.PlanRecord) { r.ID = "P-ZZZZ" },
		"store row has another seal":    func(r *store.PlanRecord) { r.SHA256 = strings.Repeat("0", 64) },
		"store row has another scan":    func(r *store.PlanRecord) { r.ScanID = "S-OTHER" },
		"store row expires differently": func(r *store.PlanRecord) { r.ExpiresAt = r.ExpiresAt.Add(time.Second) },
	}
	for name, edit := range tests {
		r := rec
		edit(&r)
		if err := planner.Verify(p, r, w.cat, w.pol, at); !errors.Is(err, planner.ErrTampered) {
			t.Errorf("%s: err = %v, want ErrTampered", name, err)
		}
	}

	// A plan from another schema version is refused even when it is sealed.
	old := p
	old.SchemaVersion = "0"
	old, oldRec := reseal(t, old, rec)
	if err := planner.Verify(old, oldRec, w.cat, w.pol, at); !errors.Is(err, planner.ErrTampered) {
		t.Errorf("other schema version: err = %v, want ErrTampered", err)
	}

	// An unsealed plan is refused.
	unsealed := p
	unsealed.SHA256 = ""
	if err := planner.Verify(unsealed, rec, w.cat, w.pol, at); !errors.Is(err, planner.ErrTampered) {
		t.Errorf("no seal: err = %v, want ErrTampered", err)
	}
}

func TestVerifyExpiryBoundary(t *testing.T) {
	w := newWorld(t)
	st, _, p := sealed(t, w)
	rec, _ := st.Plan(p.ID)
	if err := planner.Verify(p, rec, w.cat, w.pol, p.ExpiresAt.Add(-time.Nanosecond)); err != nil {
		t.Errorf("one nanosecond before expiry: %v", err)
	}
	if err := planner.Verify(p, rec, w.cat, w.pol, p.ExpiresAt); !errors.Is(err, planner.ErrExpired) {
		t.Errorf("at expiry: %v, want ErrExpired", err)
	}
}

func TestVerifyRefusesItemsThePlannerCouldNotHaveMade(t *testing.T) {
	w := newWorld(t)
	w.cat.Recipes = append(w.cat.Recipes, recipes.Recipe{ID: "x.bogus", Risk: model.RiskSafe, Method: "bogus"})
	st, _, p := sealed(t, w)
	rec, _ := st.Plan(p.ID)

	tests := []struct {
		name string
		edit func(q *model.Plan)
		want string
	}{
		{"no items", func(q *model.Plan) { q.Items = nil }, "no items"},
		{"an item twice", func(q *model.Plan) { q.Items = append(q.Items, q.Items[0]) }, "appears twice"},
		{"unknown recipe", func(q *model.Plan) { q.Items[0].RecipeID = "no.such.recipe" }, "unknown recipe"},
		{"method not the recipe's", func(q *model.Plan) { q.Items[0].Method = model.MethodQuarantine }, "wrong method"},
		{"advice-only risk", func(q *model.Plan) { q.Items[0].Risk = model.RiskAdvise }, "risk its recipe doesn't allow"},
		{"blocked risk", func(q *model.Plan) { q.Items[0].Risk = model.RiskBlocked }, "risk its recipe doesn't allow"},
		{"risk below the recipe's", func(q *model.Plan) { q.Items[2].Risk = model.RiskSafe }, "risk its recipe doesn't allow"},
		{"command the recipe doesn't allow", func(q *model.Plan) { q.Items[0].Argv = []string{"brew", "uninstall", "git"} }, "command its recipe doesn't allow"},
		{"a move with a command", func(q *model.Plan) { q.Items[1].Argv = []string{"rm", "-rf", "/"} }, "move with a command"},
		{"a protected target", func(q *model.Plan) { q.Items[1].Target = filepath.Join(w.home, "Documents") }, "safety rules refuse"},
		{"an unknown method", func(q *model.Plan) {
			q.Items[0].RecipeID, q.Items[0].Method, q.Items[0].Argv = "x.bogus", "bogus", nil
		}, "unknown method"},
	}
	for _, tt := range tests {
		q := p
		q.Items = append([]model.PlanItem(nil), p.Items...)
		tt.edit(&q)
		q, r := reseal(t, q, rec)
		err := planner.Verify(q, r, w.cat, w.pol, now.Add(time.Minute))
		if !errors.Is(err, planner.ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want ErrInvalid containing %q", tt.name, err, tt.want)
		}
	}
}

func TestBuildTimeBoundaries(t *testing.T) {
	if planner.TTL != 30*time.Minute || planner.Oldest != 6*time.Hour {
		t.Fatalf("DefaultTTL = %v, MaxScanAge = %v; want 30m and 6h", planner.TTL, planner.Oldest)
	}
	w := newWorld(t)
	sel := planner.Selector{IDs: []string{"O-brewaa"}}

	in := w.input()
	p, _, err := planner.Build(in, sel)
	if err != nil {
		t.Fatal(err)
	}
	if !p.CreatedAt.Equal(now) || !p.ExpiresAt.Equal(now.Add(planner.TTL)) {
		t.Errorf("default TTL: created %v expires %v", p.CreatedAt, p.ExpiresAt)
	}

	// A tiny but positive TTL is honored, not replaced by the default.
	in.TTL = time.Nanosecond
	if p, _, err = planner.Build(in, sel); err != nil || !p.ExpiresAt.Equal(now.Add(time.Nanosecond)) {
		t.Errorf("1ns TTL: expires %v, err %v", p.ExpiresAt, err)
	}

	// A scan exactly Oldest old is allowed; one nanosecond older is not.
	in = w.input()
	in.ScanFinished = now.Add(-planner.Oldest)
	if _, _, err := planner.Build(in, sel); err != nil {
		t.Errorf("scan exactly MaxScanAge old: %v", err)
	}
	in.ScanFinished = now.Add(-planner.Oldest - time.Nanosecond)
	if _, _, err := planner.Build(in, sel); !errors.Is(err, planner.ErrStaleScan) {
		t.Errorf("scan just over MaxScanAge old: %v, want ErrStaleScan", err)
	}
}

func TestBuildFingerprintsTheTarget(t *testing.T) {
	w := newWorld(t)
	p, _, err := planner.Build(w.input(), planner.Selector{IDs: []string{"O-logsaa"}})
	if err != nil {
		t.Fatal(err)
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(w.home, "Library/Logs/Zoom"), &st); err != nil {
		t.Fatal(err)
	}
	fp := p.Items[0].Fingerprint
	if fp.Dev != uint64(st.Dev) || fp.Ino != st.Ino || fp.AllocBytes != st.Blocks*512 || fp.MtimeNS != st.Mtimespec.Nano() { //nolint:gosec // G115: device numbers are non-negative
		t.Errorf("fingerprint %+v does not match stat %+v", fp, st)
	}
}

// A file's allocation is non-zero, so this pins the blocks-to-bytes factor
// that the directory case above cannot (a directory allocates nothing here).
func TestFingerprintAllocatedBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, make([]byte, 20_000), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	fp := planner.Fingerprint(fi)
	if fp.AllocBytes == 0 || fp.AllocBytes != st.Blocks*512 {
		t.Errorf("AllocBytes = %d, want %d (st_blocks * 512)", fp.AllocBytes, st.Blocks*512)
	}
	if fp.Ino != st.Ino || fp.MtimeNS != fi.ModTime().UnixNano() {
		t.Errorf("fingerprint %+v does not match the file", fp)
	}
}

func TestBuildRefusals(t *testing.T) {
	w := newWorld(t)
	mb := model.Bytes{Value: 5_000_000, Source: model.SourceMeasured}
	w.ops = append(w.ops,
		model.Opportunity{ID: "O-advrsk", RecipeID: "brew.cleanup", Risk: model.RiskAdvise, Method: model.MethodNative, Bytes: mb, Command: []string{"brew", "cleanup", "--prune=all"}},
		model.Opportunity{ID: "O-advmet", RecipeID: "brew.cleanup", Risk: model.RiskSafe, Method: model.MethodAdvise, Bytes: mb},
		model.Opportunity{ID: "O-blocked", RecipeID: "brew.cleanup", Risk: model.RiskBlocked, Method: model.MethodNative, Bytes: mb},
		model.Opportunity{ID: "O-nosrc", RecipeID: "brew.cleanup", Risk: model.RiskSafe, Method: model.MethodNative, Bytes: model.Bytes{Value: 77}, Command: []string{"brew", "cleanup", "--prune=all"}},
		model.Opportunity{ID: "O-badrec", RecipeID: "not.a.recipe", Risk: model.RiskSafe, Method: model.MethodNative, Bytes: mb},
		model.Opportunity{ID: "O-badmet", RecipeID: "brew.cleanup", Risk: model.RiskSafe, Method: model.MethodQuarantine, Bytes: mb},
		model.Opportunity{ID: "O-gone", RecipeID: "user.logs", Risk: model.RiskSafe, Method: model.MethodQuarantine, Bytes: mb, Target: filepath.Join(w.home, "Library/Logs/Missing")},
	)
	ids := []string{"O-advrsk", "O-advmet", "O-blocked", "O-badrec", "O-badmet", "O-gone", "O-nope", "O-nosrc"}
	p, refused, err := planner.Build(w.input(), planner.Selector{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	wantReason := map[string]string{
		"O-advrsk": "advice only", "O-advmet": "advice only", "O-blocked": "blocked",
		"O-badrec": "recipe isn't in this version", "O-badmet": "method doesn't match",
		"O-gone": "no longer exists", "O-nope": planner.ReasonNotInScan,
	}
	got := map[string]string{}
	for _, r := range refused {
		got[r.ID] = r.Reason
	}
	for id, frag := range wantReason {
		if !strings.Contains(got[id], frag) {
			t.Errorf("%s: refusal %q, want it to contain %q", id, got[id], frag)
		}
	}
	// A size with no source is unknown, never shown as 77.
	if len(p.Items) != 1 || p.Items[0].OpportunityID != "O-nosrc" {
		t.Fatalf("items = %v, want only O-nosrc", p.Items)
	}
	if p.Items[0].BytesSource != model.SourceUnknown || p.Items[0].ExpectedBytes != 0 {
		t.Errorf("unsourced bytes became %v / %d, want unknown / 0", p.Items[0].BytesSource, p.Items[0].ExpectedBytes)
	}
}

func TestBuildRefusesAToolThatIsGone(t *testing.T) {
	w := newWorld(t)
	prev := recipes.ToolInstalled
	recipes.ToolInstalled = func(name string) bool { return name != "brew" }
	t.Cleanup(func() { recipes.ToolInstalled = prev })

	_, refused, err := planner.Build(w.input(), planner.Selector{IDs: []string{"O-brewaa", "O-logsaa"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(refused) != 1 || refused[0].ID != "O-brewaa" || !strings.Contains(refused[0].Reason, "brew isn't installed any more") {
		t.Errorf("refused = %+v, want O-brewaa because brew is gone", refused)
	}
}
