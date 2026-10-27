package recipes

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"macdoc/internal/model"
	"macdoc/internal/projects"
)

func init() {
	Register("project_artifact", detectProjectArtifact)
	Register("derived_data", detectDerivedData)
}

// Risk reasons for project state.
const (
	reasonDirty   = "the project has uncommitted changes"
	reasonUnknown = "macdoc couldn't confirm the project has no uncommitted changes"
)

// envProjects returns the shared project discovery result, or false if there
// is none.
func envProjects(env Env) (projects.Result, bool, error) {
	if env.Projects == nil {
		return projects.Result{}, false, nil
	}
	res, err := env.Projects()
	if err != nil {
		return projects.Result{}, false, err
	}
	return res, true, nil
}

// detectProjectArtifact emits project artifact folders whose path relative
// to the project root is in params["artifacts"] (comma-separated, e.g.
// "ios/Pods,Pods"), at least params["min_bytes"] (default 1 MB). Projects in
// protected folders are skipped. LastUsed is the project's last activity
// (git commit, source change or build), so an unknown activity time leaves
// the item out for recipes with stale_days. Unless git says the project is
// clean or it isn't in git, the risk is raised to params["dirty_risk"]
// (default CAUTION).
func detectProjectArtifact(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	want := map[string]bool{}
	for _, a := range strings.Split(r.Params["artifacts"], ",") {
		if a = strings.TrimSpace(a); a != "" {
			want[a] = true
		}
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("recipe %s: detector project_artifact needs params.artifacts", r.ID)
	}
	minBytes, err := paramInt(r, "min_bytes", 1_000_000)
	if err != nil {
		return nil, err
	}
	dirtyRisk := model.Risk(r.Params["dirty_risk"])
	if dirtyRisk == "" {
		dirtyRisk = model.RiskCaution
	}
	res, ok, err := envProjects(env)
	if !ok || err != nil {
		return nil, err
	}
	var out []Finding
	for _, p := range res.Projects {
		if p.Protected {
			continue
		}
		for _, a := range p.Artifacts {
			if !want[a.Rel] || a.Bytes < minBytes {
				continue
			}
			f := Finding{
				Target: a.Path,
				Title:  a.Rel + " in " + p.Name,
				Bytes:  model.Bytes{Value: a.Bytes, Source: model.SourceMeasured},
				Note:   projectNote(p),
			}
			if p.IdleDays >= 0 {
				f.LastUsed = p.LastActive
			}
			f.MinRisk, f.RiskReason = gitRaise(p, dirtyRisk)
			out = append(out, f)
		}
	}
	return out, nil
}

// gitRaise is the risk floor a project's git state calls for: none when git
// says it's clean or it isn't a git project, risk otherwise.
func gitRaise(p projects.Project, risk model.Risk) (model.Risk, string) {
	switch p.Git {
	case projects.GitClean, projects.GitNone:
		return "", ""
	case projects.GitDirty:
		return risk, reasonDirty
	}
	return risk, reasonUnknown
}

// projectNote describes a project's activity and git state in one line.
func projectNote(p projects.Project) string {
	var parts []string
	if p.IdleDays >= 0 {
		parts = append(parts, fmt.Sprintf("Project %s: last activity %d days ago (%s)", p.Root, p.IdleDays, p.ActivitySource))
	} else {
		parts = append(parts, "Project "+p.Root+": last activity unknown")
	}
	switch p.Git {
	case projects.GitClean:
		parts = append(parts, "no uncommitted changes")
	case projects.GitDirty:
		parts = append(parts, "has uncommitted changes")
	case projects.GitNone:
		parts = append(parts, "not in git")
	}
	return strings.Join(parts, "; ") + "."
}

// projectFor returns the project rooted at root, if discovery found it.
func projectFor(env Env, root string) (projects.Project, bool) {
	res, ok, err := envProjects(env)
	if !ok || err != nil {
		return projects.Project{}, false
	}
	return res.Find(root)
}

// withProject refines a node_modules or .venv finding with its project:
// the project's last activity counts as use (so a project committed to last
// week isn't stale even if its dependencies are old), and its git state is
// noted. It reports false if the project was active within days.
func withProject(env Env, f *Finding, root string, days int) bool {
	p, ok := projectFor(env, root)
	if !ok {
		return true
	}
	if p.IdleDays >= 0 && p.LastActive.After(f.LastUsed) {
		f.LastUsed = p.LastActive
		if !stale(env, f.LastUsed.Unix(), days) {
			return false
		}
	}
	f.Note = projectNote(p)
	return true
}

// detectDerivedData emits Xcode DerivedData folders like the children
// detector, noting the project each was built from, or that the project no
// longer exists.
func detectDerivedData(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	fs, err := detectChildren(ctx, env, r)
	if err != nil || len(fs) == 0 {
		return fs, err
	}
	res, ok, err := envProjects(env)
	if !ok || err != nil {
		return fs, err
	}
	byPath := map[string]projects.DerivedData{}
	for _, d := range res.DerivedData {
		byPath[d.Path] = d
	}
	for i := range fs {
		d, ok := byPath[fs[i].Target]
		switch {
		case !ok || d.Workspace == "":
		case d.Missing:
			fs[i].Note = "Built from " + d.Workspace + ", which no longer exists."
		default:
			fs[i].Note = "Built from " + d.Workspace + "."
			if p, ok := res.Find(d.Project); ok {
				fs[i].Note = "Built from " + filepath.Base(d.Workspace) + ". " + projectNote(p)
			}
		}
	}
	return fs, nil
}
