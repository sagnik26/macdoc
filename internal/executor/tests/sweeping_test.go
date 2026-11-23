package tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// sweepItem is a .DS_Store sweep plan item for a folder.
func (e *env) sweepItem(t *testing.T, id, target string) model.PlanItem {
	t.Helper()
	r, ok := e.cfg.Catalog.Get("system.ds_store")
	if !ok {
		t.Fatal("no system.ds_store recipe")
	}
	return model.PlanItem{OpportunityID: id, RecipeID: r.ID, Title: r.Title, Target: target, ExpectedBytes: 12288,
		BytesSource: model.SourceMeasured, Risk: model.RiskCaution, Method: model.MethodSweep, Restore: r.Restore}
}

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A sweep removes exactly the files named .DS_Store, anywhere below the folder
// including built-in protected ones, and leaves every look-alike, folder and
// link alone. The audit record carries the count.
func TestSweepRemovesOnlyDSStore(t *testing.T) {
	e := newEnv(t)
	docs := filepath.Join(e.home, "Documents") // a protected folder: the sweep still works there
	gone := []string{".DS_Store", "a/b/c/.DS_Store"}
	kept := []string{".DS_Store.bak", "._.DS_Store", "DS_Store", "ds_store.txt", "a/notes.txt", "d/.DS_Store/inner.txt", // a folder named .DS_Store is not a file

		".git/.DS_Store", "node_modules/x/.DS_Store", "Photos.photoslibrary/.DS_Store", "Music.musiclibrary/.DS_Store"}
	for _, f := range append(append([]string{}, gone...), kept...) {
		write(t, filepath.Join(docs, f))
	}
	outside := linkedDSStore(t, e, docs)
	rep := mustApply(t, e, e.plan(t, e.sweepItem(t, "O-sweepa", docs)))
	res := rep.Items[0]
	if res.Outcome != executor.OutcomeDone || res.Files != 2 || !res.Deleted || res.MovedBytes == 0 {
		t.Fatalf("result = %+v", res)
	}
	for _, f := range gone {
		if exists(filepath.Join(docs, f)) {
			t.Errorf("%s is still there", f)
		}
	}
	for _, f := range kept {
		if !exists(filepath.Join(docs, f)) {
			t.Errorf("%s was removed", f)
		}
	}
	if !exists(filepath.Join(docs, "link", ".DS_Store")) || !exists(outside) {
		t.Error("a symlink named .DS_Store, or what it points to, was removed")
	}
	checkSweepAudit(t, e, 2)
}

// linkedDSStore puts a symlink named .DS_Store in docs/link, pointing at a file
// outside docs, and returns that file.
func linkedDSStore(t *testing.T, e *env, docs string) string {
	t.Helper()
	outside := filepath.Join(e.home, "outside.txt")
	write(t, outside)
	if err := os.MkdirAll(filepath.Join(docs, "link"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(docs, "link", ".DS_Store")); err != nil {
		t.Fatal(err)
	}
	return outside
}

// checkSweepAudit expects one sweep audit record that counts n files.
func checkSweepAudit(t *testing.T, e *env, n int) {
	t.Helper()
	for _, r := range e.events(t) {
		if r.Method == string(model.MethodSweep) && r.Event == "item_done" {
			if r.Items != n {
				t.Errorf("audit items = %d, want %d", r.Items, n)
			}
			return
		}
	}
	t.Errorf("no sweep audit record in %v", eventNames(e.events(t)))
}

// A sweep that finds nothing is skipped, not reported as done.
func TestSweepNothingLeft(t *testing.T) {
	e := newEnv(t)
	d := filepath.Join(e.home, "Projects")
	write(t, filepath.Join(d, "a.txt"))
	rep := mustApply(t, e, e.plan(t, e.sweepItem(t, "O-sweepb", d)))
	if rep.Items[0].Outcome != executor.OutcomeSkipped {
		t.Errorf("outcome = %s, want skipped", rep.Items[0].Outcome)
	}
}

// A folder swapped for a symlink between the plan and the apply is skipped and
// nothing behind the link is touched.
func TestSweepFolderBecameLink(t *testing.T) {
	e := newEnv(t)
	d := filepath.Join(e.home, "Projects")
	write(t, filepath.Join(d, ".DS_Store"))
	p := e.plan(t, e.sweepItem(t, "O-sweepc", d))
	elsewhere := filepath.Join(e.home, "Elsewhere")
	write(t, filepath.Join(elsewhere, ".DS_Store"))
	if err := os.RemoveAll(d); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, d); err != nil {
		t.Fatal(err)
	}
	rep := mustApply(t, e, p)
	if rep.Items[0].Outcome != executor.OutcomeSkipped || !exists(filepath.Join(elsewhere, ".DS_Store")) {
		t.Errorf("outcome %s, target file present %v", rep.Items[0].Outcome, exists(filepath.Join(elsewhere, ".DS_Store")))
	}
}

// Targets a sweep may never start at fail the plan's verification, so nothing
// is touched: home itself, Library, a hidden folder, a folder two deep.
func TestSweepRefusedRoots(t *testing.T) {
	for name, rel := range map[string]string{"home": "", "Library": "Library", "hidden": ".ssh", "deep": "Documents/Taxes", "outside": "../elsewhere"} {
		e := newEnv(t)
		target := filepath.Join(e.home, rel)
		write(t, filepath.Join(target, ".DS_Store"))
		_, err := executor.Apply(context.Background(), e.cfg, e.plan(t, e.sweepItem(t, "O-sweepd", target)), executor.Approvals{"O-sweepd": {Individually: true}})
		if !errors.Is(err, planner.ErrInvalid) {
			t.Errorf("%s: err = %v, want the plan refused", name, err)
		}
		if !exists(filepath.Join(target, ".DS_Store")) {
			t.Errorf("%s: the file was removed", name)
		}
		_ = os.RemoveAll(filepath.Join(e.home, "..", "elsewhere"))
	}
}

// A plan Claude proposed never sweeps.
func TestSweepNeverInClaudePlan(t *testing.T) {
	e := newEnv(t)
	d := filepath.Join(e.home, "Projects")
	write(t, filepath.Join(d, ".DS_Store"))
	p := model.Plan{SchemaVersion: model.SchemaVersion, CreatedAt: t0, ExpiresAt: t0.Add(planner.TTL), Origin: model.OriginClaude,
		ScanID: "S-1", Request: "test", Items: []model.PlanItem{e.sweepItem(t, "O-sweepe", d)}}
	if err := planner.Save(e.st, planner.Dir(e.storePath), &p); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Apply(context.Background(), e.cfg, p, all(p)); !errors.Is(err, planner.ErrInvalid) {
		t.Errorf("err = %v, want the plan refused", err)
	}
	if !exists(filepath.Join(d, ".DS_Store")) {
		t.Error("the file was removed")
	}
}

// Without approval nothing is removed.
func TestSweepNeedsApproval(t *testing.T) {
	e := newEnv(t)
	d := filepath.Join(e.home, "Projects")
	write(t, filepath.Join(d, ".DS_Store"))
	p := e.plan(t, e.sweepItem(t, "O-sweepf", d))
	rep, err := executor.Apply(context.Background(), e.cfg, p, executor.Approvals{"O-other": {}})
	if err != nil || rep.Items[0].Outcome != executor.OutcomeSkipped || !exists(filepath.Join(d, ".DS_Store")) {
		t.Errorf("rep %+v err %v", rep.Items, err)
	}
}
