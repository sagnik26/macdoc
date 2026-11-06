package tests

import (
	"context"
	"github.com/sagnik26/macdoc/internal/recipes"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/projects"
)

// p4Project is an idle, clean React recipes.Native project with every artifact kind.
func p4Project() projects.Project {
	root := w2Home + "/code/app"
	art := func(rel string, mb int64) projects.Artifact {
		return projects.Artifact{Rel: rel, Path: root + "/" + rel, Bytes: mb * 1_000_000}
	}
	return projects.Project{
		Root: root, Name: "app", Kinds: []string{"react-native"},
		Artifacts: []projects.Artifact{
			art("node_modules", 500), art("ios/Pods", 300), art("ios/build", 200), art("android/app/build", 400),
			art("android/build", 5), art("android/.gradle", 20), art(".next", 100), art(".expo", 50),
		},
		LastActive: w2Now.Add(-120 * 24 * time.Hour), ActivitySource: projects.ActivityCommit,
		IdleDays: 120, Git: projects.GitClean,
	}
}

func p4Env(ps ...projects.Project) recipes.Env {
	return recipes.Env{Projects: func() (projects.Result, error) { return projects.Result{Projects: ps}, nil }}
}

func relTargets(fs []recipes.Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, strings.TrimPrefix(f.Target, w2Home+"/code/app/"))
	}
	return out
}

// Positive: an idle, clean project yields its artifacts at the recipe's risk.
func TestProjectArtifactRecipes(t *testing.T) {
	for id, want := range map[string]string{
		"rn.ios_pods_stale":     "ios/Pods",
		"rn.native_build_stale": "ios/build android/app/build android/.gradle", // android/build is under 10 MB
		"node.next_cache_stale": ".next",
	} {
		fs, err := w2Run(t, id, p4Env(p4Project()))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(relTargets(fs), " "); got != want {
			t.Errorf("%s: targets %q, want %q", id, got, want)
		}
		for _, f := range fs {
			if f.MinRisk != "" || !f.LastUsed.Equal(w2Now.Add(-120*24*time.Hour)) || f.Bytes.Source != model.SourceMeasured ||
				!strings.Contains(f.Note, "120 days ago (git commit); no uncommitted changes") || !strings.HasSuffix(f.Title, " in app") {
				t.Errorf("%s: finding %+v", id, f)
			}
		}
	}
}

// Look-alike negatives: protected projects, artifacts the recipe doesn't
// own, and no discovery result at all.
func TestProjectArtifactNegatives(t *testing.T) {
	p := p4Project()
	p.Protected = true
	for _, id := range []string{"rn.ios_pods_stale", "rn.native_build_stale", "node.next_cache_stale"} {
		if fs, _ := w2Run(t, id, p4Env(p)); len(fs) != 0 {
			t.Errorf("%s: protected project gave %v", id, relTargets(fs))
		}
		if fs, err := w2Run(t, id, recipes.Env{}); len(fs) != 0 || err != nil {
			t.Errorf("%s: no projects gave %v, %v", id, fs, err)
		}
	}
	// .expo and node_modules belong to no project_artifact recipe.
	for _, id := range []string{"rn.ios_pods_stale", "rn.native_build_stale", "node.next_cache_stale"} {
		fs, _ := w2Run(t, id, p4Env(p4Project()))
		for _, tg := range relTargets(fs) {
			if tg == ".expo" || tg == "node_modules" {
				t.Errorf("%s claimed %s", id, tg)
			}
		}
	}
}

// Precondition failures: uncommitted changes or an unconfirmed git state
// raise the risk; unknown activity leaves LastUsed zero (so classify skips
// the item for stale_days recipes).
func TestProjectArtifactGitAndIdle(t *testing.T) {
	cases := []struct {
		id     string
		git    projects.GitState
		risk   model.Risk
		reason string
	}{
		{"rn.ios_pods_stale", projects.GitDirty, model.RiskRisky, recipes.ReasonDirty},
		{"rn.ios_pods_stale", projects.GitUnknown, model.RiskRisky, recipes.ReasonUnknown},
		{"rn.ios_pods_stale", projects.GitNone, "", ""},
		{"rn.native_build_stale", projects.GitDirty, model.RiskCaution, recipes.ReasonDirty},
		{"node.next_cache_stale", projects.GitUnknown, model.RiskCaution, recipes.ReasonUnknown},
	}
	for _, c := range cases {
		p := p4Project()
		p.Git = c.git
		fs, err := w2Run(t, c.id, p4Env(p))
		if err != nil || len(fs) == 0 {
			t.Fatalf("%s: %v %v", c.id, fs, err)
		}
		for _, f := range fs {
			if f.MinRisk != c.risk || f.RiskReason != c.reason {
				t.Errorf("%s with git %s: min risk %q (%q), want %q (%q)", c.id, c.git, f.MinRisk, f.RiskReason, c.risk, c.reason)
			}
		}
	}

	p := p4Project()
	p.IdleDays, p.LastActive = -1, time.Time{}
	fs, _ := w2Run(t, "rn.ios_pods_stale", p4Env(p))
	if len(fs) != 1 || !fs[0].LastUsed.IsZero() || !strings.Contains(fs[0].Note, "last activity unknown") {
		t.Errorf("unknown activity: %+v", fs)
	}
}

func TestProjectArtifactParams(t *testing.T) {
	r := w2Recipe(t, "rn.ios_pods_stale")
	r.Params = map[string]string{}
	if _, err := recipes.DetectProjectArtifact(context.Background(), p4Env(p4Project()), r); err == nil {
		t.Error("missing params.artifacts must be an error")
	}
	r.Params = map[string]string{"artifacts": "ios/Pods", "dirty_risk": "SAFE"}
	if err := recipes.Validate(r); err == nil || !strings.Contains(err.Error(), "dirty_risk") {
		t.Errorf("dirty_risk SAFE must fail validation: %v", err)
	}
}

// recipes.WithProject: recent project activity keeps old dependencies from being
// called recipes.Stale; an idle project adds its note.
func TestWithProject(t *testing.T) {
	r := w2Recipe(t, "node.node_modules_stale")
	env := p4Env(p4Project())
	env.Now = w2Now
	f := recipes.Finding{LastUsed: w2Now.Add(-300 * 24 * time.Hour)}
	if !recipes.WithProject(env, &f, w2Home+"/code/app", r.StaleDays) || !f.LastUsed.Equal(w2Now.Add(-120*24*time.Hour)) || f.Note == "" {
		t.Errorf("idle project: %+v", f)
	}
	active := p4Project()
	active.IdleDays, active.LastActive = 10, w2Now.Add(-10*24*time.Hour)
	env = p4Env(active)
	env.Now = w2Now
	f = recipes.Finding{LastUsed: w2Now.Add(-300 * 24 * time.Hour)}
	if recipes.WithProject(env, &f, w2Home+"/code/app", r.StaleDays) {
		t.Error("a project committed to 10 days ago must not have recipes.Stale node_modules")
	}
	f = recipes.Finding{LastUsed: w2Now.Add(-300 * 24 * time.Hour)}
	if !recipes.WithProject(env, &f, w2Home+"/elsewhere", r.StaleDays) || f.Note != "" {
		t.Errorf("unknown project: %+v", f)
	}
}

func TestDerivedDataNotes(t *testing.T) {
	dd := w2Home + "/Library/Developer/Xcode/DerivedData"
	nodes := fakeNodes{
		{Path: dd, Kind: model.NodeDir, Bytes: 3},
		{Path: dd + "/App-abc", Kind: model.NodeDir, Bytes: 1, MtimeMax: w2DaysAgo(40)},
		{Path: dd + "/Gone-def", Kind: model.NodeDir, Bytes: 1, MtimeMax: w2DaysAgo(40)},
		{Path: dd + "/Odd-ghi", Kind: model.NodeDir, Bytes: 1, MtimeMax: w2DaysAgo(40)},
	}
	p := p4Project()
	env := recipes.Env{Nodes: nodes, Projects: func() (projects.Result, error) {
		return projects.Result{Projects: []projects.Project{p}, DerivedData: []projects.DerivedData{
			{Path: dd + "/App-abc", Workspace: p.Root + "/ios/App.xcworkspace", Project: p.Root},
			{Path: dd + "/Gone-def", Workspace: w2Home + "/old/Gone.xcodeproj", Missing: true},
		}}, nil
	}}
	fs, err := w2Run(t, "xcode.derived_data", env)
	if err != nil || len(fs) != 3 {
		t.Fatalf("%+v %v", fs, err)
	}
	notes := map[string]string{}
	for _, f := range fs {
		notes[f.Target[len(dd)+1:]] = f.Note
	}
	if !strings.HasPrefix(notes["App-abc"], "Built from App.xcworkspace. Project "+p.Root) ||
		notes["Gone-def"] != "Built from "+w2Home+"/old/Gone.xcodeproj, which no longer exists." || notes["Odd-ghi"] != "" {
		t.Errorf("notes = %q", notes)
	}
}
