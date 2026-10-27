package recipes

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"macdoc/internal/model"
)

func validRecipe() Recipe {
	return Recipe{
		ID: "test.cache", Title: "Test cache", Category: "app-caches", Toolchain: "system",
		Risk: model.RiskSafe, Method: model.MethodQuarantine, Detector: "children",
		Params: map[string]string{"dir": "~/Library/Caches/test"},
		Allow:  []string{"~/Library/Caches/test/*"}, KeepChildrenOf: []string{"~/Library/Caches/test"},
		Restore: model.Restore{Kind: model.RestoreRebuild}, Explain: "x",
		Preconditions: []string{"app_not_running:Test"},
	}
}

func TestValidateOK(t *testing.T) {
	if err := Validate(validRecipe()); err != nil {
		t.Fatal(err)
	}
	n := validRecipe()
	n.Method, n.Allow, n.Native = model.MethodNative, nil, &Native{Argv: []string{"xcrun", "simctl", "runtime", "delete", "{id}"}}
	if err := Validate(n); err != nil {
		t.Fatal(err)
	}
	a := validRecipe()
	a.Method, a.Risk, a.Allow = model.MethodAdvise, model.RiskAdvise, nil
	if err := Validate(a); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Recipe){
		"no dot id":             func(r *Recipe) { r.ID = "cache" },
		"no explain":            func(r *Recipe) { r.Explain = "" },
		"category":              func(r *Recipe) { r.Category = "junk" },
		"toolchain":             func(r *Recipe) { r.Toolchain = "rust" },
		"blocked risk":          func(r *Recipe) { r.Risk = model.RiskBlocked },
		"detector":              func(r *Recipe) { r.Detector = "nope" },
		"restore":               func(r *Recipe) { r.Restore.Kind = "" },
		"stale":                 func(r *Recipe) { r.StaleDays = -1 },
		"quarantine no allow":   func(r *Recipe) { r.Allow = nil },
		"quarantine irreplace":  func(r *Recipe) { r.Restore.Kind = model.RestoreIrreplaceable },
		"quarantine+native":     func(r *Recipe) { r.Native = &Native{Argv: []string{"brew", "cleanup"}} },
		"allow outside home":    func(r *Recipe) { r.Allow = []string{"/opt/homebrew/*"} },
		"allow dotdot":          func(r *Recipe) { r.Allow = []string{"~/Library/../Documents/*"} },
		"allow too shallow":     func(r *Recipe) { r.Allow = []string{"~/*"} },
		"allow double-star":     func(r *Recipe) { r.Allow = []string{"~/**/node_modules"} },
		"allow trailing slash":  func(r *Recipe) { r.Allow = []string{"~/Library/Caches/"} },
		"allow bad glob":        func(r *Recipe) { r.Allow = []string{"~/Library/[/x"} },
		"keep children glob":    func(r *Recipe) { r.KeepChildrenOf = []string{"~/Library/*"} },
		"keep children shallow": func(r *Recipe) { r.KeepChildrenOf = []string{"~/Library"} },
		"keep name slash":       func(r *Recipe) { r.KeepNames = []string{"a/b"} },
		"precondition":          func(r *Recipe) { r.Preconditions = []string{"reboot"} },
		"precondition no arg":   func(r *Recipe) { r.Preconditions = []string{"app_not_running:"} },
		"bad method":            func(r *Recipe) { r.Method = "delete" },
		"advise wrong risk": func(r *Recipe) {
			r.Method, r.Allow = model.MethodAdvise, nil
		},
		"risk advise wrong method": func(r *Recipe) { r.Risk = model.RiskAdvise },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			r := validRecipe()
			mut(&r)
			if Validate(r) == nil {
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
		r.Method, r.Allow, r.Native = model.MethodNative, nil, &Native{Argv: argv}
		if Validate(r) == nil {
			t.Errorf("accepted argv %q", argv)
		}
	}
	ok := validRecipe()
	ok.Method, ok.Allow, ok.Native = model.MethodNative, nil, &Native{Argv: []string{"ollama", "rm", "{id}"}}
	if err := Validate(ok); err != nil {
		t.Errorf("ollama rm rejected: %v", err)
	}
	r := validRecipe()
	r.Method, r.Allow = model.MethodNative, nil
	if Validate(r) == nil {
		t.Error("native without argv accepted")
	}
	r.Native = &Native{Argv: []string{"brew", "cleanup"}, DryRunArgv: []string{"sh", "-c", "x"}}
	if Validate(r) == nil {
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
	c, err := LoadFS(fstest.MapFS{"c/a.yaml": {Data: []byte(good)}, "c/empty.yaml": {Data: nil}, "c/README.md": {Data: []byte("x")}}, "c")
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
		if _, err := LoadFS(fsys, "c"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEmbeddedCatalogLoads(t *testing.T) {
	c, err := Load()
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
	c := &Catalog{Recipes: []Recipe{{KeepChildrenOf: []string{"~/Library/Developer/Xcode/DerivedData"}, KeepNames: []string{"node_modules"}}}}
	keep := KeepFunc(c, "/Users/me")
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
	if KeepFunc(&Catalog{}, "/Users/me") != nil {
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
	env := Env{Home: home, Nodes: nodes, Now: time.Now()}
	r := Recipe{ID: "x.y", Params: map[string]string{"dir": "~/Library/Developer/Xcode/DerivedData", "min_bytes": "10"}}
	fs, err := detectChildren(context.Background(), env, r)
	if err != nil || len(fs) != 1 || fs[0].Target != dd+"/A-1" || fs[0].Bytes.Value != 200 || fs[0].LastUsed.IsZero() {
		t.Fatalf("children = %+v, %v", fs, err)
	}
	r.Params = map[string]string{"path": "~/Library/Developer/Xcode/DerivedData"}
	fs, err = detectPath(context.Background(), env, r)
	if err != nil || len(fs) != 1 || fs[0].Bytes.Value != 300 {
		t.Fatalf("path = %+v, %v", fs, err)
	}
	for _, d := range []Detector{detectChildren, detectPath} {
		if _, err := d(context.Background(), env, Recipe{ID: "x.y"}); err == nil {
			t.Error("missing param accepted")
		}
		if f, err := d(context.Background(), Env{Home: home}, Recipe{ID: "x.y", Params: map[string]string{"dir": "~/a/b", "path": "~/a/b"}}); err != nil || f != nil {
			t.Error("nil Nodes should give no findings")
		}
	}
	if _, err := detectChildren(context.Background(), env, Recipe{ID: "x.y", Params: map[string]string{"dir": "~/a/b", "min_bytes": "x"}}); err == nil {
		t.Error("bad min_bytes accepted")
	}
}

func TestRegistry(t *testing.T) {
	if _, ok := Lookup("children"); !ok {
		t.Fatal("children not registered")
	}
	names := DetectorNames()
	if len(names) < 2 {
		t.Fatalf("names = %v", names)
	}
	defer func() {
		if recover() == nil {
			t.Error("duplicate Register did not panic")
		}
	}()
	Register("children", detectChildren)
}

func TestExpandHome(t *testing.T) {
	if ExpandHome("/h", "~") != "/h" || ExpandHome("/h", "~/a") != "/h/a" || ExpandHome("/h", "/x") != "/x" {
		t.Error("ExpandHome")
	}
}
