package cli

import (
	"crypto/sha256"
	"encoding/base32"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/safety"
)

// Deleting an installed program from the Retire tab (ADR 0017): pick it, read what
// goes, press Delete or Cancel. What goes is the program's own app bundle and the
// data folders the scan attributed to it. It runs through the same sealed plan,
// the same executor and the same audit log as every other removal, so each path is
// checked against the protected paths and re-checked just before it is moved. The
// person never sees a recipe; the plan carries one built-in entry for this run only
// (planner.RetireID), allowing exactly these paths.

// retireTarget is one path a deletion removes.
type retireTarget struct {
	path  string
	bytes model.Bytes
	title string
}

// retireTargets lists what deleting sw removes: its app bundle (for an app found in
// one of appDirs) and its data folders. A folder inside another listed folder is
// left out, since it goes with its parent. A path with a wildcard character is
// refused: it could widen the allow list.
func retireTargets(appDirs []string, sw engine.SoftwareEntry) []retireTarget {
	var out []retireTarget
	if sw.Kind == "app" {
		for _, dir := range appDirs {
			p := filepath.Join(dir, sw.Name+".app")
			if fi, err := os.Lstat(p); err == nil && fi.IsDir() {
				out = append(out, retireTarget{p, model.Bytes{Source: model.SourceUnknown}, sw.Name + " (app)"})
			}
		}
	}
	for _, f := range sw.Folders {
		out = append(out, retireTarget{f.Path, model.Bytes{Value: f.Bytes, Source: model.SourceMeasured}, sw.Name + " data"})
	}
	return withoutNested(out)
}

// withoutNested drops a target that is inside another one (it goes with its parent),
// one listed twice, and one whose path is not absolute or has a wildcard character.
func withoutNested(all []retireTarget) []retireTarget {
	var kept []retireTarget
	for i, t := range all {
		if strings.Contains(t.path, "*") || !filepath.IsAbs(t.path) {
			continue
		}
		inner := false
		for j, o := range all {
			if (i != j && o.path != t.path && safety.Within(o.path, t.path)) || (j < i && o.path == t.path) {
				inner = true
			}
		}
		if !inner {
			kept = append(kept, t)
		}
	}
	return kept
}

// retireOps turns the targets into the opportunities one deletion plans. Their IDs
// come from the paths, so they are stable within the run.
func retireOps(scanID string, targets []retireTarget) []model.Opportunity {
	ops := make([]model.Opportunity, 0, len(targets))
	for _, t := range targets {
		sum := sha256.Sum256([]byte(t.path))
		id := "O-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:5]))
		ops = append(ops, model.Opportunity{
			ID: id, ScanID: scanID, RecipeID: planner.RetireID, Title: t.title, Target: t.path, Bytes: t.bytes,
			Risk: model.RiskCaution, Method: model.MethodQuarantine, IdleDays: -1,
			Restore:  model.Restore{Kind: model.RestoreNone, Note: "Deleted for good."},
			Category: "app-caches", Toolchain: "system",
		})
	}
	return ops
}

// retireCatalog is base plus the one entry for this deletion (planner.RetireRecipe),
// which allows exactly the targets. It lives only for this deletion.
func retireCatalog(base *recipes.Catalog, targets []retireTarget) *recipes.Catalog {
	paths := make([]string, len(targets))
	for i, t := range targets {
		paths[i] = t.path
	}
	cat := &recipes.Catalog{}
	if base != nil {
		cat.Recipes = append(cat.Recipes, base.Recipes...)
	}
	cat.Recipes = append(cat.Recipes, planner.RetireRecipe(paths))
	return cat
}

// appDirs is where installed apps are looked for.
func (d *TuiDeps) appDirs() []string {
	if d.AppDirs != nil {
		return d.AppDirs
	}
	return []string{"/Applications", filepath.Join(d.Home, "Applications")}
}

// startRetire is Enter on a program in the Retire list: it seals a plan for the
// program's app bundle and data folders and opens the Delete / Cancel box.
func (m TuiModel) startRetire() (tea.Model, tea.Cmd) {
	if m.software == nil || m.Deps.MakePlan == nil || m.Planning {
		return m, nil
	}
	shown := m.shownSoftware(*m.software) // the real entries: the ones on screen are cleaned for the terminal
	if m.softwareCursor >= len(shown) {
		return m, nil
	}
	sw := shown[m.softwareCursor]
	targets := retireTargets(m.Deps.appDirs(), sw)
	if len(targets) == 0 {
		m.Flash = "There is nothing to delete for " + display(sw.Name) + ": no app or data folder was found."
		return m, nil
	}
	ops := retireOps(m.software.Scan.ID, targets)
	ids := make([]string, len(ops))
	for i, op := range ops {
		ids[i] = op.ID
	}
	pre := &engine.OpportunityResult{Scan: m.software.Scan, Opportunities: ops}
	deps := m.Deps
	deps.retire = retireCatalog(deps.cat, targets)
	m.Planning = true
	m.Flash = "Checking…"
	name, id := sw.Name, sw.ID
	return m, func() tea.Msg {
		res, err := deps.MakePlan(deps, planner.Selector{IDs: ids}, pre)
		return removeReadyMsg{Res: res, Err: err, Retire: name, RetireID: id}
	}
}

// catalog is the recipe catalog a plan or removal runs against: the base one, or the
// one holding a Retire deletion's own entry while that deletion is open.
func (d *TuiDeps) catalog() *recipes.Catalog {
	if d.retire != nil {
		return d.retire
	}
	return d.cat
}
