package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/engine"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/store"
	"github.com/sagnik26/macdoc/internal/testutil/fstree"
)

func n(path string, bytes int64) model.Node {
	d := 0
	if path != "/" {
		d = strings.Count(path, "/")
	}
	return model.Node{Path: path, Kind: model.NodeDir, Bytes: bytes, Depth: d}
}

func TestLargestPrefersWhereSpaceIs(t *testing.T) {
	nodes := []model.Node{
		n("/", 100*gb),
		n("/Users", 90*gb),
		n("/Users/me", 90*gb),
		n("/Users/me/Library", 60*gb),
		n("/Users/me/Library/Developer", 40*gb),       // child Xcode holds >50%
		n("/Users/me/Library/Developer/Xcode", 35*gb), // engine.Children split evenly → qualifies
		n("/Users/me/Library/Developer/Xcode/DerivedData", 15*gb),
		n("/Users/me/Library/Developer/Xcode/iOS DeviceSupport", 12*gb),
		n("/Users/me/code", 20*gb), // no kept engine.Children → qualifies
		{Path: "/Users/me/big.dmg", Kind: model.NodeFile, Bytes: 5 * gb, Depth: 3},
		n("/Applications", 30*gb),  // top-level: never listed
		n("/Users/me/small", gb/2), // below minBytes
	}
	got := engine.Largest(nodes, 0, gb)
	want := []string{"/Users/me/code", "/Users/me/Library/Developer/Xcode/DerivedData", "/Users/me/Library/Developer/Xcode/iOS DeviceSupport", "/Users/me/big.dmg"}
	// Xcode (35) has DerivedData (15) < 17.5 → qualifies too; Library (60) has Developer (40) ≥ 30 → not.
	want = append([]string{"/Users/me/Library/Developer/Xcode"}, want...)
	if len(got) != len(want) {
		t.Fatalf("got %v", paths(got))
	}
	for i := range want {
		if got[i].Path != want[i] {
			t.Fatalf("got %v, want %v", paths(got), want)
		}
	}
	if l := engine.Largest(nodes, 2, gb); len(l) != 2 {
		t.Errorf("limit not applied: %v", paths(l))
	}
}

func TestChildren(t *testing.T) {
	nodes := []model.Node{n("/", 10), n("/a", 3), n("/b", 5), n("/a/x", 2)}
	got := engine.Children(nodes, "/")
	if len(got) != 2 || got[0].Path != "/b" || got[1].Path != "/a" {
		t.Errorf("got %v", paths(got))
	}
}

func paths(ns []model.Node) []string {
	var s []string
	for _, x := range ns {
		s = append(s, x.Path)
	}
	return s
}

func TestScanStoresResult(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	fstree.Build(t, root,
		fstree.File("a/big.bin", 3<<20),
		fstree.File("b/small.txt", 1024),
	)
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	sum, err := engine.Scan(context.Background(), engine.ScanOptions{Home: home, Root: root, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Info.Status != model.ScanComplete || sum.Info.TotalBytes < 3<<20 || sum.Info.TotalFiles != 2 {
		t.Fatalf("summary = %+v", sum.Info)
	}
	st, err := store.Open(store.DefaultPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	latest, err := st.LatestScan(model.ScanComplete)
	if err != nil || latest.ID != sum.Info.ID {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
	if _, err := engine.Scan(context.Background(), engine.ScanOptions{}); err == nil {
		t.Error("expected error without home")
	}
}

func TestScanNodesAdapter(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	fstree.Build(t, root, fstree.File("a/b/c.bin", 2<<20))
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	sum, err := engine.Scan(context.Background(), engine.ScanOptions{Home: home, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(store.DefaultPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ns, err := engine.ScanNodes{St: st, ScanID: sum.Info.ID}.Nodes(sum.Info.Root, 0)
	if err != nil || len(ns) == 0 {
		t.Fatalf("nodes = %v, %v", ns, err)
	}
}
