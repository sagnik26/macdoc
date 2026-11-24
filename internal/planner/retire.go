package planner

import (
	"path/filepath"
	"strings"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/safety"
)

// RetireID is the one built-in recipe a Retire deletion runs under (ADR 0017).
const RetireID = "retire.program"

// RetireRecipe is the entry that lets one deletion of an installed program run
// through the normal sealed plan and the executor, without a catalog recipe naming
// it. It allows exactly the given absolute paths, plus an app bundle one level
// inside /Applications, and needs the app to be closed. It is not part of the
// catalog: the dashboard adds it for one deletion and drops it afterwards.
//
// The safety rules still decide first: a protected path, a path outside the home
// folder (other than that one app bundle) and one too shallow are refused whatever
// this entry allows. A path with a wildcard character, or one that is not absolute,
// is left out, because it could widen the allow list.
func RetireRecipe(paths []string) recipes.Recipe {
	allow := []string{safety.AppsPattern}
	for _, p := range paths {
		if filepath.IsAbs(p) && !strings.Contains(p, "*") {
			allow = append(allow, p)
		}
	}
	return recipes.Recipe{
		ID: RetireID, Title: "Delete an installed program", Category: "app-caches", Toolchain: "system",
		Risk: model.RiskCaution, Method: model.MethodQuarantine, Allow: allow,
		Preconditions: []string{"app_bundle_not_running"},
	}
}
