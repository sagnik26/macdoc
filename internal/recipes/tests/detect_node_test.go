package tests

import (
	"context"
	"macdoc/internal/recipes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"macdoc/internal/model"
	"macdoc/internal/testutil/fstree"
)

// w2Project creates dir/package.json (unless pkg is false) and dir/<lock>
// (unless lock is ""), all with mtime.
func w2Project(dir string, pkg bool, lock string, mtime time.Time) []fstree.Entry {
	var es []fstree.Entry
	if pkg {
		es = append(es, fstree.File(dir+"/package.json", 10).WithModTime(mtime))
	}
	if lock != "" {
		es = append(es, fstree.File(dir+"/"+lock, 10).WithModTime(mtime))
	}
	return append(es, fstree.Dir(dir+"/node_modules"))
}

func TestNodeModulesStale(t *testing.T) {
	home := t.TempDir()
	oldT := time.Unix(w2DaysAgo(200), 0)
	old, recent := w2DaysAgo(200), w2DaysAgo(10)
	const big = 900 << 20

	// Real false positives from the developer's Mac. Each parent gets a
	// package.json and a lockfile so only the path rules reject them.
	tools := []string{
		".npm-global/lib",
		".nvm/versions/node/v20.20.2/lib",
		".local/lib",
		".local/share/mise/installs/node/lib",
		".cursor/extensions/ms-python.vscode-1.0/dist",
		".cursor/extensions/ms-python.vscode-1.0/out",
		"Library/Application Support/Accomplish/opencode",
		"Library/Caches/com.apple.python/Users/me/lib",
		".cursor/projects/p1/canvases",
		"code/tool/lib", // lib/node_modules outside a hidden tree
	}
	var entries []fstree.Entry
	var nodes fakeNodes
	for _, d := range tools {
		entries = append(entries, w2Project(d, true, "package-lock.json", oldT)...)
		nodes = append(nodes, model.Node{Path: home + "/" + d + "/node_modules", Bytes: big, MtimeMax: old})
	}
	nodes[len(tools)-2].Bytes = 127 << 10 // canvases: 127 KB noise

	type proj struct {
		dir   string
		pkg   bool
		lock  string
		bytes int64
		mtime int64
	}
	projects := []proj{
		{"code/web", true, "package-lock.json", big, old},          // positive
		{"code/yarnapp", true, "yarn.lock", big, old},              // positive
		{"code/pnpmapp", true, "pnpm-lock.yaml", big, old},         // positive
		{"code/bunapp", true, "bun.lock", big, old},                // positive
		{"code/bunb", true, "bun.lockb", big, old},                 // positive
		{"code/shrink", true, "npm-shrinkwrap.json", big, old},     // positive
		{"code/nolock", true, "", big, old},                        // no lockfile
		{"code/nopkg", false, "package-lock.json", big, old},       // no package.json
		{"code/small", true, "package-lock.json", 40_000_000, old}, // below 50 MB
		{"code/app", true, "package-lock.json", big, recent},       // recent node_modules
		{"code/nomtime", true, "package-lock.json", big, 0},        // unknown folder mtime, old files
	}
	for _, p := range projects {
		entries = append(entries, w2Project(p.dir, p.pkg, p.lock, oldT)...)
		nodes = append(nodes, model.Node{Path: home + "/" + p.dir + "/node_modules", Bytes: p.bytes, MtimeMax: p.mtime})
	}
	entries = append(entries,
		// Recently edited lockfile: the project isn't recipes.Stale.
		fstree.File("code/edited/package.json", 10).WithModTime(oldT),
		fstree.File("code/edited/yarn.lock", 10).WithModTime(time.Unix(recent, 0)),
		// Lockfile is a symlink or a directory, package.json is a directory.
		fstree.File("code/linked/package.json", 10).WithModTime(oldT),
		fstree.Symlink("code/linked/package-lock.json", "../web/package-lock.json"),
		fstree.File("code/lockdir/package.json", 10).WithModTime(oldT),
		fstree.Dir("code/lockdir/yarn.lock"),
		fstree.Dir("code/pkgdir/package.json"),
		fstree.File("code/pkgdir/yarn.lock", 10).WithModTime(oldT),
		fstree.File("code/legacy/package.json", 10).WithModTime(oldT),
		fstree.File("code/legacy/yarn.lock", 10).WithModTime(oldT),
	)
	nodes = append(nodes, fakeNodes{
		{Path: home + "/code/edited/node_modules", Bytes: big, MtimeMax: old},
		{Path: home + "/code/linked/node_modules", Bytes: big, MtimeMax: old},
		{Path: home + "/code/lockdir/node_modules", Bytes: big, MtimeMax: old},
		{Path: home + "/code/pkgdir/node_modules", Bytes: big, MtimeMax: old},
		{Path: home + "/code/web/node_modules/pkg/node_modules", Bytes: big, MtimeMax: old}, // nested
		{Path: home + "/code/legacy/node_modules_old", Bytes: big, MtimeMax: old},           // look-alike name
		{Path: home + "/node_modules", Bytes: big, MtimeMax: old},                           // too shallow
		{Path: home + "/code/x/node_modules.json", Kind: model.NodeFile, Bytes: big, MtimeMax: old},
	}...)
	fstree.Build(t, home, entries...)

	fs, err := w2Run(t, "node.node_modules_stale", recipes.Env{Home: home, Nodes: nodes})
	var want []string
	for _, d := range []string{"web", "yarnapp", "pnpmapp", "bunapp", "bunb", "shrink", "nomtime"} {
		want = append(want, home+"/code/"+d+"/node_modules")
	}
	w2WantTargets(t, fs, err, want...)
	for _, f := range fs {
		if f.LastUsed.Unix() != oldT.Unix() { // nomtime: from the files
			t.Errorf("%s: LastUsed %v, want %v", f.Target, f.LastUsed, oldT)
		}
		if f.Title != "node_modules in "+filepath.Base(filepath.Dir(f.Target)) {
			t.Errorf("title %q", f.Title)
		}
	}

	// LastUsed is the newest of the three mtimes: here the folder.
	nodes2 := fakeNodes{{Path: home + "/code/web/node_modules", Bytes: big, MtimeMax: w2DaysAgo(150)}}
	fs, err = w2Run(t, "node.node_modules_stale", recipes.Env{Home: home, Nodes: nodes2})
	if err != nil || len(fs) != 1 || fs[0].LastUsed.Unix() != w2DaysAgo(150) {
		t.Fatalf("got %+v, %v", fs, err)
	}

	r := w2Recipe(t, "node.node_modules_stale")
	if r.Params["min_bytes"] != "50000000" {
		t.Errorf("min_bytes = %q", r.Params["min_bytes"])
	}
	r.Params = map[string]string{"min_bytes": "x"}
	if _, err := recipes.DetectNodeModulesStale(t.Context(), recipes.Env{Home: home, Nodes: nodes}, r); err == nil {
		t.Error("bad min_bytes accepted")
	}
	fs, err = w2Run(t, "node.node_modules_stale", recipes.Env{})
	w2WantNone(t, fs, err)
	if _, err := w2Run(t, "node.node_modules_stale", recipes.Env{Nodes: w2ErrNodes{}}); err == nil {
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
	fs, err := w2Run(t, "nvm.old_versions", recipes.Env{Home: home, Nodes: nodes})
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
		if _, err := recipes.DetectNvmOldVersions(context.Background(), recipes.Env{Nodes: fakeNodes{}}, bad); err == nil {
			t.Errorf("missing %s accepted", key)
		}
	}
	fs, err := w2Run(t, "nvm.old_versions", recipes.Env{})
	w2WantNone(t, fs, err)
	if _, err := w2Run(t, "nvm.old_versions", recipes.Env{Nodes: w2ErrNodes{}}); err == nil {
		t.Error("Nodes error swallowed")
	}
	// An unreadable alias (a directory) is a real error.
	home, nodes := w2NvmHome(t, nil, "v20.20.2", "v24.22.0")
	if err := os.MkdirAll(filepath.Join(home, ".nvm/alias/default"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := w2Run(t, "nvm.old_versions", recipes.Env{Home: home, Nodes: nodes}); err == nil {
		t.Error("unreadable alias accepted")
	}
	// A zero-size old version is skipped.
	home, nodes = w2NvmHome(t, nil, "v20.20.2", "v24.22.0")
	nodes[1].Bytes = 0
	fs, err = w2Run(t, "nvm.old_versions", recipes.Env{Home: home, Nodes: nodes})
	w2WantNone(t, fs, err)
	if recipes.OrNone("") != "not set" || recipes.OrNone("v1.0.0") != "v1.0.0" {
		t.Error("orNone")
	}
}
