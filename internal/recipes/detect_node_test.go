package recipes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeModulesStale(t *testing.T) {
	old, recent := w2DaysAgo(200), w2DaysAgo(10)
	nodes := fakeNodes{
		{Path: w2Home + "/code/web/node_modules", Bytes: 900 << 20, MtimeMax: old},
		{Path: w2Home + "/code/web/node_modules/pkg/node_modules", Bytes: 100 << 20, MtimeMax: old}, // nested
		{Path: w2Home + "/code/app/node_modules", Bytes: 500 << 20, MtimeMax: recent},               // recent
		{Path: w2Home + "/code/legacy/node_modules_old", Bytes: 500 << 20, MtimeMax: old},           // look-alike
		{Path: w2Home + "/code/nomtime/node_modules", Bytes: 500 << 20},                             // unknown mtime
		{Path: w2Home + "/code/empty/node_modules", MtimeMax: old},                                  // empty
		{Path: w2Home + "/node_modules", Bytes: 500 << 20, MtimeMax: old},                           // too shallow for the allow
		{Path: w2Home + "/code/x/node_modules.json", Kind: "file", Bytes: 500 << 20, MtimeMax: old},
	}
	fs, err := w2Run(t, "node.node_modules_stale", Env{Nodes: nodes})
	w2WantTargets(t, fs, err, w2Home+"/code/web/node_modules")
	if fs[0].Title != "node_modules in web" || fs[0].LastUsed.IsZero() {
		t.Errorf("finding %+v", fs[0])
	}
	fs, err = w2Run(t, "node.node_modules_stale", Env{})
	w2WantNone(t, fs, err)
	if _, err := w2Run(t, "node.node_modules_stale", Env{Nodes: w2ErrNodes{}}); err == nil {
		t.Error("Nodes error swallowed")
	}
}

// nvmHome builds a home with ~/.nvm/alias entries and returns it with scan
// nodes for the given installed versions.
func w2NvmHome(t *testing.T, aliases map[string]string, versions ...string) (string, fakeNodes) {
	t.Helper()
	home := t.TempDir()
	for name, val := range aliases {
		p := filepath.Join(home, ".nvm/alias", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(val+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(home, ".nvm/versions/node")
	nodes := fakeNodes{{Path: dir, Bytes: 1 << 30}}
	for _, v := range versions {
		nodes = append(nodes, fakeNodes{
			{Path: dir + "/" + v, Bytes: 100 << 20, MtimeMax: w2DaysAgo(50)},
			{Path: dir + "/" + v + "/lib", Bytes: 90 << 20},
		}...)
	}
	return home, nodes
}

func w2RunNvm(t *testing.T, aliases map[string]string, versions ...string) []string {
	t.Helper()
	home, nodes := w2NvmHome(t, aliases, versions...)
	fs, err := w2Run(t, "nvm.old_versions", Env{Home: home, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range fs {
		if !strings.HasPrefix(f.Target, home+"/.nvm/versions/node/") || f.Note == "" {
			t.Errorf("finding %+v", f)
		}
		names = append(names, filepath.Base(f.Target))
	}
	return names
}

func TestNvmOldVersions(t *testing.T) {
	// The real fixture: nvm_versions.txt lists v20.20.2 and v24.22.0.
	real := strings.Fields(string(w2Fixture(t, "nvm_versions.txt")))
	cases := []struct {
		name    string
		aliases map[string]string
		vers    []string
		want    string
	}{
		{"default newest", map[string]string{"default": "24"}, real, "v20.20.2"},
		{"default older (look-alike)", map[string]string{"default": "v20.20.2"}, real, ""},
		{"default major older", map[string]string{"default": "20"}, real, ""},
		{"default major.minor", map[string]string{"default": "v20.20"}, real, ""},
		{"no default alias", nil, real, "v20.20.2"},
		{"node alias", map[string]string{"default": "node"}, real, "v20.20.2"},
		{"stable alias", map[string]string{"default": "stable"}, real, "v20.20.2"},
		{"chained lts", map[string]string{"default": "lts/iron", "lts/iron": "v20.20.2"}, append([]string{"v18.1.0"}, real...), "v18.1.0"},
		{"newest per major", map[string]string{"default": "20"}, []string{"v20.1.0", "v20.20.2", "v24.22.0"}, "v20.1.0"},
		{"unresolvable alias", map[string]string{"default": "lts/*"}, real, ""},
		{"dangling chain", map[string]string{"default": "missing"}, real, ""},
		{"traversal", map[string]string{"default": "../../etc"}, real, ""},
		{"empty alias", map[string]string{"default": ""}, real, ""},
		{"loop", map[string]string{"default": "a", "a": "default"}, real, ""},
		{"default not installed", map[string]string{"default": "18"}, real, ""},
		{"single version", map[string]string{"default": "24"}, []string{"v24.22.0"}, ""},
		{"non-version dirs", nil, []string{"v24.22.0", "system", "v1.x.0", "v-1.0.0", "20.1.0"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(w2RunNvm(t, c.aliases, c.vers...), ",")
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestNvmEdgeCases(t *testing.T) {
	r := w2Recipe(t, "nvm.old_versions")
	for _, key := range []string{"dir", "alias"} {
		bad := r
		bad.Params = map[string]string{"dir": "~/a/b", "alias": "~/a/c"}
		delete(bad.Params, key)
		if _, err := detectNvmOldVersions(context.Background(), Env{Nodes: fakeNodes{}}, bad); err == nil {
			t.Errorf("missing %s accepted", key)
		}
	}
	fs, err := w2Run(t, "nvm.old_versions", Env{})
	w2WantNone(t, fs, err)
	if _, err := w2Run(t, "nvm.old_versions", Env{Nodes: w2ErrNodes{}}); err == nil {
		t.Error("Nodes error swallowed")
	}
	// An unreadable alias (a directory) is a real error.
	home, nodes := w2NvmHome(t, nil, "v20.20.2", "v24.22.0")
	if err := os.MkdirAll(filepath.Join(home, ".nvm/alias/default"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := w2Run(t, "nvm.old_versions", Env{Home: home, Nodes: nodes}); err == nil {
		t.Error("unreadable alias accepted")
	}
	// A zero-size old version is skipped.
	home, nodes = w2NvmHome(t, nil, "v20.20.2", "v24.22.0")
	nodes[1].Bytes = 0
	fs, err = w2Run(t, "nvm.old_versions", Env{Home: home, Nodes: nodes})
	w2WantNone(t, fs, err)
	if orNone("") != "not set" || orNone("v1.0.0") != "v1.0.0" {
		t.Error("orNone")
	}
}
