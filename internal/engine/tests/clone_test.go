package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/store"
	"github.com/sagnik26/macdoc/internal/testutil/fstree"
)

// cloned builds a home whose DerivedData holds one old project folder: a file
// of its own and a clone of a file that lives elsewhere. It returns the home,
// the scan root above it and the folder, and skips where the filesystem has
// no clonefile(2).
func cloned(t *testing.T) (home, root, dd string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(root, "home")
	dd = filepath.Join(home, "Library/Developer/Xcode/DerivedData/App-1")
	fstree.Build(t, home, fstree.File("ref/shared.bin", 4<<20), fstree.File("Library/Developer/Xcode/DerivedData/App-1/own.bin", 2<<20))
	if out, err := exec.Command("cp", "-c", filepath.Join(home, "ref/shared.bin"), filepath.Join(dd, "clone.bin")).CombinedOutput(); err != nil {
		t.Skipf("no clone support here: cp -c: %v: %s", err, out)
	}
	old := time.Now().Add(-100 * 24 * time.Hour)
	for _, p := range []string{filepath.Join(dd, "own.bin"), filepath.Join(dd, "clone.bin"), dd} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Sync(); err != nil {
		t.Fatal(err)
	}
	return home, root, dd
}

// scanned scans root and returns the stored node for path.
func scanned(t *testing.T, home, root, path string) model.Node {
	t.Helper()
	sum, err := engine.Scan(context.Background(), engine.ScanOptions{Home: home, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.DefaultPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ns, err := engine.NewScanNodes(st, sum.Info.ID).Nodes(path, 0)
	if err != nil || len(ns) == 0 || ns[0].Path != path {
		t.Fatalf("nodes = %v, %v", ns, err)
	}
	return ns[0]
}

// A real scan sees the folder's bytes include the clone, and the DerivedData
// suggestion is sized by what removing the folder frees.
func TestScanThenSuggestionOnRealClones(t *testing.T) {
	home, root, dd := cloned(t)
	node := scanned(t, home, root, dd)
	if node.Private == nil || *node.Private >= node.Bytes || node.Bytes-*node.Private < 4<<20 {
		t.Fatalf("node bytes %d private %v; want private known and at least 4 MB (the clone) below bytes", node.Bytes, node.Private)
	}

	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, _ := cat.Get("xcode.derived_data")
	stubTools(t)
	res, err := engine.Opportunities(context.Background(), engine.OpportunityOptions{Home: home, Catalog: &recipes.Catalog{Recipes: []recipes.Recipe{r}}, MinBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Opportunities) != 1 || res.Opportunities[0].Target != dd {
		t.Fatalf("opportunities = %v; warnings %v", ids(res.Opportunities), res.Warnings)
	}
	if got := res.Opportunities[0].Bytes; got != measured(*node.Private) || got.Value >= node.Bytes {
		t.Errorf("suggestion size %+v; want the node's private %d, below its bytes %d", got, *node.Private, node.Bytes)
	}
	if res.Totals[model.RiskSafe] != *node.Private {
		t.Errorf("SAFE total %d; want %d", res.Totals[model.RiskSafe], *node.Private)
	}
}
