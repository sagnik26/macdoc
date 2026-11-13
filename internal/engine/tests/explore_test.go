package tests

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/store"
)

// exploreScan stores one complete scan with the given nodes and unreadable
// paths, in a fresh home, and returns the home and the store path.
func exploreScan(t *testing.T, nodes []model.Node, unmeasured []model.Unmeasured) (home, storePath string) {
	t.Helper()
	home = evaluatedTempDir(t)
	storePath = filepath.Join(home, "state.db")
	st, err := store.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	id, err := st.BeginScan("/", "test", now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddNodes(id, nodes); err != nil {
		t.Fatal(err)
	}
	if len(unmeasured) > 0 {
		if err := st.AddUnmeasured(id, unmeasured); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.FinishScan(id, model.ScanComplete, 0, 1, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	return home, storePath
}

func fileNode(path string, bytes int64) model.Node {
	return model.Node{Path: path, Kind: model.NodeFile, Bytes: bytes, Files: 1}
}

// exploreLibrary explores a folder with a directory, a file, a grandchild and
// room left over for a "smaller items" remainder.
func exploreLibrary(t *testing.T) engine.ExploreResult {
	t.Helper()
	nodes := []model.Node{
		dirNode("/Users/me/Library", 900),
		dirNode("/Users/me/Library/Caches", 500),
		fileNode("/Users/me/Library/big.bin", 300),
		dirNode("/Users/me/Library/Caches/deep", 200), // a grandchild: not an entry
		fileNode("/Users/me/Library/Caches/deep/f.db", 150),
	}
	home, sp := exploreScan(t, nodes, nil)
	res, err := engine.Explore(context.Background(), engine.ExploreOptions{Home: home, StorePath: sp, Path: "/Users/me/Library", Now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Positive: the folder lists its kept children biggest first, with a
// "smaller items" remainder so the entries add up to the folder.
func TestExploreListsChildrenAndRemainder(t *testing.T) {
	res := exploreLibrary(t)
	if res.Status != "complete" || res.Bytes == nil || *res.Bytes != 900 {
		t.Fatalf("status %q bytes %v; want complete 900", res.Status, res.Bytes)
	}
	var names []string
	var sum int64
	for _, e := range res.Entries {
		names = append(names, e.Name)
		sum += e.Bytes
	}
	if len(names) != 3 || names[0] != "Caches" || names[1] != "big.bin" || names[2] != "smaller items" {
		t.Errorf("entries = %v; want Caches, big.bin, smaller items", names)
	}
	if sum != 900 {
		t.Errorf("entries add up to %d; want the folder's 900", sum)
	}
	if res.Entries[0].Share < 0.55 || res.Entries[0].Share > 0.56 {
		t.Errorf("Caches share = %v; want about 0.556", res.Entries[0].Share)
	}
}

// The biggest files at any depth are listed, and the parent is the folder above.
func TestExploreLargestFilesAndParent(t *testing.T) {
	res := exploreLibrary(t)
	if len(res.LargestFiles) != 2 || res.LargestFiles[0].Name != "big.bin" || res.LargestFiles[1].Name != "f.db" {
		t.Errorf("largest files = %+v; want big.bin first, then f.db", res.LargestFiles)
	}
	if res.Parent != "/Users/me" {
		t.Errorf("parent = %q", res.Parent)
	}
}

// Look-alikes: a sibling whose name starts the same way is not a child.
func TestExploreIgnoresLookAlikeSiblings(t *testing.T) {
	nodes := []model.Node{
		dirNode("/Users/me/Library", 400),
		dirNode("/Users/me/Library2", 900),
		dirNode("/Users/me/Library/Caches", 300),
	}
	home, sp := exploreScan(t, nodes, nil)
	res, err := engine.Explore(context.Background(), engine.ExploreOptions{Home: home, StorePath: sp, Path: "/Users/me/Library"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Entries {
		if e.Name == "Library2" {
			t.Errorf("a look-alike sibling was listed: %+v", e)
		}
	}
}

// Precondition: a folder the scan kept no detail for is unavailable, with no
// size at all: unknown is never reported as zero.
func TestExploreUnavailableFolderHasNoSize(t *testing.T) {
	home, sp := exploreScan(t, []model.Node{dirNode("/Users/me/Library", 900)}, nil)
	res, err := engine.Explore(context.Background(), engine.ExploreOptions{Home: home, StorePath: sp, Path: "/Users/me/Tiny"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "unavailable" || res.Bytes != nil || res.Note == "" {
		t.Errorf("result = %+v; want unavailable, no bytes, with a note", res)
	}
}

// An unreadable path below makes the folder partial: its sizes are lower bounds.
func TestExplorePartialWhenAPathCouldNotBeRead(t *testing.T) {
	nodes := []model.Node{dirNode("/Users/me/Library", 900), dirNode("/Users/me/Library/Caches", 500)}
	home, sp := exploreScan(t, nodes, []model.Unmeasured{{Path: "/Users/me/Library/Mail", Reason: "permission denied"}})
	res, err := engine.Explore(context.Background(), engine.ExploreOptions{Home: home, StorePath: sp, Path: "/Users/me/Library"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "partial" || res.Note == "" {
		t.Errorf("status %q note %q; want partial with a note", res.Status, res.Note)
	}
	// A folder elsewhere is not affected by that unreadable path.
	other, err := engine.Explore(context.Background(), engine.ExploreOptions{Home: home, StorePath: sp, Path: "/Users/me/Library/Caches"})
	if err != nil {
		t.Fatal(err)
	}
	if other.Status != "complete" {
		t.Errorf("Caches status = %q; want complete", other.Status)
	}
}

// The path matches without regard to case, "~" is expanded against home, and
// a relative path is refused.
func TestExplorePathHandling(t *testing.T) {
	home := evaluatedTempDir(t)
	nodes := []model.Node{dirNode(home, 900), dirNode(home+"/Library", 500)}
	_, sp := exploreScan(t, nodes, nil)
	for _, p := range []string{"", "~", home} {
		res, err := engine.Explore(context.Background(), engine.ExploreOptions{Home: home, StorePath: sp, Path: p})
		if err != nil || res.Path != home {
			t.Errorf("%q: path %q, %v; want %q", p, res.Path, err, home)
		}
	}
	res, err := engine.Explore(context.Background(), engine.ExploreOptions{Home: home, StorePath: sp, Path: "~/library"})
	if err != nil || res.Status != "complete" {
		t.Errorf("~/library: %+v, %v; want the folder found without regard to case", res, err)
	}
	if _, err := engine.Explore(context.Background(), engine.ExploreOptions{Home: home, StorePath: sp, Path: "relative/dir"}); err == nil {
		t.Error("a relative path should be refused")
	}
}

func TestExploreNeedsAScan(t *testing.T) {
	_, err := engine.Explore(context.Background(), engine.ExploreOptions{Home: t.TempDir()})
	if !errors.Is(err, engine.ErrNoScan) {
		t.Errorf("err = %v, want ErrNoScan", err)
	}
}
