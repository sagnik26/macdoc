package tests

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sagnik26/macdoc/internal/recipes"

	"github.com/sagnik26/macdoc/internal/model"
)

func validRecipe() recipes.Recipe {
	return recipes.Recipe{
		ID: "test.cache", Title: "Test cache", Category: "app-caches", Toolchain: "system",
		Risk: model.RiskSafe, Method: model.MethodQuarantine, Detector: "children",
		Params: map[string]string{"dir": "~/Library/Caches/test"},
		Allow:  []string{"~/Library/Caches/test/*"}, KeepChildrenOf: []string{"~/Library/Caches/test"},
		Restore: model.Restore{Kind: model.RestoreRebuild}, Explain: "x",
		Preconditions: []string{"app_not_running:Test"},
	}
}

func TestValidateOK(t *testing.T) {
	if err := recipes.Validate(validRecipe()); err != nil {
		t.Fatal(err)
	}
	n := validRecipe()
	n.Method, n.Allow, n.Native = model.MethodNative, nil, &recipes.Native{Argv: []string{"xcrun", "simctl", "runtime", "delete", "{id}"}}
	if err := recipes.Validate(n); err != nil {
		t.Fatal(err)
	}
	a := validRecipe()
	a.Method, a.Risk, a.Allow = model.MethodAdvise, model.RiskAdvise, nil
	if err := recipes.Validate(a); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*recipes.Recipe){
		"no dot id":             func(r *recipes.Recipe) { r.ID = "cache" },
		"no explain":            func(r *recipes.Recipe) { r.Explain = "" },
		"category":              func(r *recipes.Recipe) { r.Category = "junk" },
		"toolchain":             func(r *recipes.Recipe) { r.Toolchain = "rust" },
		"blocked risk":          func(r *recipes.Recipe) { r.Risk = model.RiskBlocked },
		"detector":              func(r *recipes.Recipe) { r.Detector = "nope" },
		"restore":               func(r *recipes.Recipe) { r.Restore.Kind = "" },
		"stale":                 func(r *recipes.Recipe) { r.StaleDays = -1 },
		"quarantine no allow":   func(r *recipes.Recipe) { r.Allow = nil },
		"quarantine irreplace":  func(r *recipes.Recipe) { r.Restore.Kind = model.RestoreIrreplaceable },
		"quarantine+native":     func(r *recipes.Recipe) { r.Native = &recipes.Native{Argv: []string{"brew", "cleanup"}} },
		"allow outside home":    func(r *recipes.Recipe) { r.Allow = []string{"/opt/homebrew/*"} },
		"allow dotdot":          func(r *recipes.Recipe) { r.Allow = []string{"~/Library/../Documents/*"} },
		"allow too shallow":     func(r *recipes.Recipe) { r.Allow = []string{"~/*"} },
		"allow double-star":     func(r *recipes.Recipe) { r.Allow = []string{"~/**/node_modules"} },
		"allow trailing slash":  func(r *recipes.Recipe) { r.Allow = []string{"~/Library/Caches/"} },
		"allow bad glob":        func(r *recipes.Recipe) { r.Allow = []string{"~/Library/[/x"} },
		"keep children glob":    func(r *recipes.Recipe) { r.KeepChildrenOf = []string{"~/Library/*"} },
		"keep children shallow": func(r *recipes.Recipe) { r.KeepChildrenOf = []string{"~/Library"} },
		"keep name slash":       func(r *recipes.Recipe) { r.KeepNames = []string{"a/b"} },
		"precondition":          func(r *recipes.Recipe) { r.Preconditions = []string{"reboot"} },
		"precondition no arg":   func(r *recipes.Recipe) { r.Preconditions = []string{"app_not_running:"} },
		"bad method":            func(r *recipes.Recipe) { r.Method = "delete" },
		"advise wrong risk": func(r *recipes.Recipe) {
			r.Method, r.Allow = model.MethodAdvise, nil
		},
		"risk advise wrong method": func(r *recipes.Recipe) { r.Risk = model.RiskAdvise },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			r := validRecipe()
			mut(&r)
			if recipes.Validate(r) == nil {
				t.Fatalf("accepted: %+v", r)
			}
		})
	}
}

func TestValidateArgv(t *testing.T) {
	bad := [][]string{
		{"rm", "-rf", "x"}, {"bash", "-c", "x"}, {"docker", "system", "prune"},
		{"docker", "volume", "prune", "--volumes"}, {"brew", "cleanup", "$(id)"},
		{"brew", "{id}", "{id}"}, {"brew", "pre{id}"}, {"curl", "x"}, {"brew", "a;b"},
		{"brew", "rm", "x"}, {"ollama", "run", "rm"}, {"docker", "rm", "{id}"},
	}
	for _, argv := range bad {
		r := validRecipe()
		r.Method, r.Allow, r.Native = model.MethodNative, nil, &recipes.Native{Argv: argv}
		if recipes.Validate(r) == nil {
			t.Errorf("accepted argv %q", argv)
		}
	}
	ok := validRecipe()
	ok.Method, ok.Allow, ok.Native = model.MethodNative, nil, &recipes.Native{Argv: []string{"ollama", "rm", "{id}"}}
	if err := recipes.Validate(ok); err != nil {
		t.Errorf("ollama rm rejected: %v", err)
	}
	r := validRecipe()
	r.Method, r.Allow = model.MethodNative, nil
	if recipes.Validate(r) == nil {
		t.Error("native without argv accepted")
	}
	r.Native = &recipes.Native{Argv: []string{"brew", "cleanup"}, DryRunArgv: []string{"sh", "-c", "x"}}
	if recipes.Validate(r) == nil {
		t.Error("bad dry-run argv accepted")
	}
}

func TestLoadFS(t *testing.T) {
	good := `- id: test.cache
  title: Test cache
  category: app-caches
  toolchain: system
  risk: SAFE
  method: quarantine
  detector: children
  params: {dir: "~/Library/Caches/test"}
  allow: ["~/Library/Caches/test/*"]
  restore: {kind: rebuild}
  explain: x
`
	c, err := recipes.LoadFS(fstest.MapFS{"c/a.yaml": {Data: []byte(good)}, "c/empty.yaml": {Data: nil}, "c/README.md": {Data: []byte("x")}}, "c")
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := c.Get("test.cache"); !ok || r.Params["dir"] != "~/Library/Caches/test" {
		t.Fatalf("got %+v", c.Recipes)
	}
	if _, ok := c.Get("nope"); ok {
		t.Error("Get found a missing recipe")
	}
	for name, fsys := range map[string]fstest.MapFS{
		"unknown field": {"c/a.yaml": {Data: []byte(good + "  surprise: 1\n")}},
		"duplicate":     {"c/a.yaml": {Data: []byte(good)}, "c/b.yaml": {Data: []byte(good)}},
		"invalid":       {"c/a.yaml": {Data: []byte(strings.Replace(good, "SAFE", "MAYBE", 1))}},
		"bad yaml":      {"c/a.yaml": {Data: []byte("- id: [")}},
	} {
		if _, err := recipes.LoadFS(fsys, "c"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEmbeddedCatalogLoads(t *testing.T) {
	c, err := recipes.Load()
	if err != nil {
		t.Fatalf("embedded catalog invalid: %v", err)
	}
	for _, r := range c.Recipes {
		if strings.HasPrefix(r.ID, "test.") {
			t.Errorf("test recipe %s in catalog", r.ID)
		}
	}
}

func TestKeepFunc(t *testing.T) {
	c := &recipes.Catalog{Recipes: []recipes.Recipe{{KeepChildrenOf: []string{"~/Library/Developer/Xcode/DerivedData"}, KeepNames: []string{"node_modules"}}}}
	keep := recipes.KeepFunc(c, "/Users/me")
	for p, want := range map[string]bool{
		"/Users/me/Library/Developer/Xcode/DerivedData":           true,
		"/Users/me/Library/Developer/Xcode/DerivedData/App-abc":   true,
		"/users/ME/library/developer/xcode/deriveddata/App-abc":   true,
		"/Users/me/Library/Developer/Xcode/DerivedData/App/Build": false,
		"/Users/me/code/web/node_modules":                         true,
		"/Users/me/code/web/node_modules_old":                     false,
		"/Users/me/code":                                          false,
	} {
		if keep(p) != want {
			t.Errorf("keep(%q) = %v", p, !want)
		}
	}
	if recipes.KeepFunc(&recipes.Catalog{}, "/Users/me") != nil {
		t.Error("empty catalog should give nil Keep")
	}
}

type fakeNodes []model.Node

func (f fakeNodes) Nodes(prefix string, maxDepth int) ([]model.Node, error) {
	var out []model.Node
	pd := strings.Count(prefix, "/")
	for _, n := range f {
		if n.Path == prefix || strings.HasPrefix(n.Path, prefix+"/") {
			if maxDepth == 0 || strings.Count(n.Path, "/")-pd <= maxDepth {
				out = append(out, n)
			}
		}
	}
	return out, nil
}

func TestGenericDetectors(t *testing.T) {
	home := "/Users/me"
	dd := home + "/Library/Developer/Xcode/DerivedData"
	nodes := fakeNodes{
		{Path: dd, Bytes: 300}, {Path: dd + "/A-1", Bytes: 200, MtimeMax: 1_700_000_000},
		{Path: dd + "/B-2", Bytes: 5}, {Path: dd + "/A-1/Build", Bytes: 150},
	}
	env := recipes.Env{Home: home, Nodes: nodes, Now: time.Now()}
	r := recipes.Recipe{ID: "x.y", Params: map[string]string{"dir": "~/Library/Developer/Xcode/DerivedData", "min_bytes": "10"}}
	fs, err := recipes.DetectChildren(context.Background(), env, r)
	if err != nil || len(fs) != 1 || fs[0].Target != dd+"/A-1" || fs[0].Bytes.Value != 200 || fs[0].LastUsed.IsZero() {
		t.Fatalf("children = %+v, %v", fs, err)
	}
	r.Params = map[string]string{"path": "~/Library/Developer/Xcode/DerivedData"}
	fs, err = recipes.DetectPath(context.Background(), env, r)
	if err != nil || len(fs) != 1 || fs[0].Bytes.Value != 300 {
		t.Fatalf("path = %+v, %v", fs, err)
	}
	for _, d := range []recipes.Detector{recipes.DetectChildren, recipes.DetectPath} {
		if _, err := d(context.Background(), env, recipes.Recipe{ID: "x.y"}); err == nil {
			t.Error("missing param accepted")
		}
		if f, err := d(context.Background(), recipes.Env{Home: home}, recipes.Recipe{ID: "x.y", Params: map[string]string{"dir": "~/a/b", "path": "~/a/b"}}); err != nil || f != nil {
			t.Error("nil Nodes should give no findings")
		}
	}
	if _, err := recipes.DetectChildren(context.Background(), env, recipes.Recipe{ID: "x.y", Params: map[string]string{"dir": "~/a/b", "min_bytes": "x"}}); err == nil {
		t.Error("bad min_bytes accepted")
	}
}

func TestRegistry(t *testing.T) {
	if _, ok := recipes.Lookup("children"); !ok {
		t.Fatal("children not registered")
	}
	names := recipes.DetectorNames()
	if len(names) < 2 {
		t.Fatalf("names = %v", names)
	}
	defer func() {
		if recover() == nil {
			t.Error("duplicate recipes.Register did not panic")
		}
	}()
	recipes.Register("children", recipes.DetectChildren)
}

func TestExpandHome(t *testing.T) {
	if recipes.ExpandHome("/h", "~") != "/h" || recipes.ExpandHome("/h", "~/a") != "/h/a" || recipes.ExpandHome("/h", "/x") != "/x" {
		t.Error("ExpandHome")
	}
}
