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
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// withRetire adds the entry a Retire deletion runs under to e's catalog, allowing
// exactly the given paths (planner.RetireRecipe), and turns on delete-right-away as
// the dashboard does. Every folder is in the sandbox; nothing real is touched.
func withRetire(e *env, allow ...string) {
	cat := &recipes.Catalog{Recipes: append([]recipes.Recipe(nil), e.cfg.Catalog.Recipes...)}
	cat.Recipes = append(cat.Recipes, planner.RetireRecipe(allow))
	e.cfg.Catalog, e.cfg.DeleteNow = cat, true
}

// retireItem is a plan item for a Retire deletion of target: CAUTION, so one group
// approval covers it and no code is typed.
func retireItem(e *env, t *testing.T, id, target string) model.PlanItem {
	t.Helper()
	it := e.item(t, id, planner.RetireID, model.RiskCaution, target)
	it.Restore = model.Restore{Kind: model.RestoreNone}
	return it
}

// groupApproval approves every item as one Enter on the Delete box does.
func groupApproval(p model.Plan) executor.Approvals {
	m := executor.Approvals{}
	for _, it := range p.Items {
		m[it.OpportunityID] = executor.Approval{}
	}
	return m
}

// Deleting a program removes its app bundle and its data folder for good from one
// group approval (no typed code), and both are in the audit log.
func TestRetireDeletesTheAppAndItsDataFolder(t *testing.T) {
	e := newEnv(t)
	app := e.folder(t, "Applications/Mine.app", "Contents/MacOS/Mine")
	data := e.folder(t, "Library/Application Support/Mine", "prefs.json")
	withRetire(e, app, data)
	p := e.plan(t, retireItem(e, t, "O-appaaa", app), retireItem(e, t, "O-datbbb", data))
	rep, err := executor.Apply(context.Background(), e.cfg, p, groupApproval(p))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Count(executor.OutcomeDone) != 2 || exists(app) || exists(data) {
		t.Fatalf("both should be gone: %+v (app %v, data %v)", rep.Items, exists(app), exists(data))
	}
	for _, it := range rep.Items {
		if !it.Deleted {
			t.Errorf("%s was moved but not deleted for good", it.Item.Title)
		}
	}
	var recorded int
	for _, r := range e.events(t) {
		if r.Recipe == planner.RetireID && r.Event == "item_done" {
			recorded++
		}
	}
	if recorded != 2 {
		t.Errorf("the audit log should hold both deletions, got %d: %v", recorded, eventNames(e.events(t)))
	}
}

// An app that is open is not deleted: the item is skipped and nothing moves.
func TestRetireLeavesAnOpenApp(t *testing.T) {
	e := newEnv(t)
	app := e.folder(t, "Applications/Mine.app", "Contents/MacOS/Mine")
	withRetire(e, app)
	e.runner.Responses["ps -axww -o command="] = platform.Result{Stdout: []byte("/sbin/launchd\n" + app + "/Contents/MacOS/Mine\n")}
	p := e.plan(t, retireItem(e, t, "O-appaaa", app))
	rep := mustApply(t, e, p)
	if rep.Items[0].Outcome != executor.OutcomeSkipped || !exists(app) {
		t.Fatalf("an open app must stay: %+v", rep.Items[0])
	}
}

// The protected paths stay protected whatever the Retire entry allows: Documents,
// Mail, macdoc's own folder, the keys and the Desktop are refused, so the whole plan
// is.
func TestRetireNeverTouchesAProtectedPath(t *testing.T) {
	for _, rel := range []string{
		"Documents/Mine", "Library/Mail/Mine", "Library/Application Support/macdoc/Mine", ".ssh/Mine", "Desktop/Mine",
	} {
		e := newEnv(t)
		target := e.folder(t, rel, "keep.txt")
		withRetire(e, target)
		p := e.plan(t, retireItem(e, t, "O-protaa", target))
		_, err := executor.Apply(context.Background(), e.cfg, p, groupApproval(p))
		if !errors.Is(err, planner.ErrInvalid) || !exists(filepath.Join(target, "keep.txt")) {
			t.Errorf("%s: apply = %v, file kept = %v; want ErrInvalid and the file kept", rel, err, exists(filepath.Join(target, "keep.txt")))
		}
	}
}

// Only the paths named for this deletion are allowed: a sibling folder, and one a
// wildcard would match, are refused.
func TestRetireAllowsOnlyTheNamedPaths(t *testing.T) {
	e := newEnv(t)
	mine := e.folder(t, "Library/Application Support/Mine", "a")
	other := e.folder(t, "Library/Application Support/Other", "a")
	withRetire(e, mine, filepath.Join(e.home, "Library/Application Support/*")) // the wildcard one is dropped
	p := e.plan(t, retireItem(e, t, "O-otheraa", other))
	if _, err := executor.Apply(context.Background(), e.cfg, p, groupApproval(p)); !errors.Is(err, planner.ErrInvalid) || !exists(other) {
		t.Fatalf("a sibling must be refused: err %v, kept %v", err, exists(other))
	}
}

// A symlink where the program's folder was is not followed: it is not deleted, and
// what it pointed at is untouched.
func TestRetireDoesNotFollowASymlink(t *testing.T) {
	e := newEnv(t)
	real := e.folder(t, "Library/Application Support/Real", "keep.txt")
	link := filepath.Join(e.home, "Library", "Application Support", "Mine")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	withRetire(e, link)
	it := retireItem(e, t, "O-linkaa", real)
	it.Target = link
	p := e.plan(t, it)
	rep, err := executor.Apply(context.Background(), e.cfg, p, groupApproval(p))
	if err == nil && rep.Count(executor.OutcomeDone) != 0 {
		t.Fatalf("a symlink must not be deleted: %+v", rep.Items)
	}
	if !exists(filepath.Join(real, "keep.txt")) {
		t.Fatal("what the symlink pointed at was touched")
	}
}
