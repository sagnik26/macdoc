package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/scanner"
	ft "github.com/sagnik26/macdoc/internal/testutil/fstree"
)

// These tests cover the private size a scan carries per node (ADR 0005,
// stage 1). They mirror docs/experiments/S1-apfs-private-size/dirtest.py
// with real APFS clones made by cp -c, and skip where the filesystem cannot
// clone. They never claim more than the lower bound the experiment proved.

// everything keeps every directory and file, so each one can be asserted on.
func everything(root string) scanner.Options {
	return scanner.Options{Root: root, MapPrefix: root, KeepThreshold: 1, Workers: 2}
}

// flush pushes delayed allocation to disk so st_blocks and the private size
// are settled before a scan reads them.
func flush(t *testing.T) {
	t.Helper()
	if err := unix.Sync(); err != nil {
		t.Fatal(err)
	}
}

// clone makes dst a copy-on-write clone of src, or skips the test when the
// filesystem has no clonefile(2).
func clone(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-c", src, dst).CombinedOutput(); err != nil {
		t.Skipf("no clone support here: cp -c: %v: %s", err, out)
	}
}

// need returns the node at p or fails.
func need(t *testing.T, res scanner.Result, p string) model.Node {
	t.Helper()
	n, ok := node(res, p)
	if !ok {
		t.Fatalf("node %s not kept; kept: %v", p, paths(res))
	}
	return n
}

// priv returns the private size of n or fails if it is unknown.
func priv(t *testing.T, n model.Node) int64 {
	t.Helper()
	if n.Private == nil {
		t.Fatalf("%s: private size unknown; want a value", n.Path)
	}
	return *n.Private
}

// checkPrivate requires that no node's private size exceeds its bytes (a
// lower bound never overstates) and logs how many are unknown and how much
// the root's private size differs from its bytes.
func checkPrivate(t *testing.T, res scanner.Result) {
	t.Helper()
	unknown := 0
	for _, n := range res.Nodes {
		switch {
		case n.Private == nil:
			unknown++
		case *n.Private < 0 || *n.Private > n.Bytes:
			t.Errorf("%s: private %d outside 0..bytes %d", n.Path, *n.Private, n.Bytes)
		}
	}
	if r, ok := node(res, "/"); ok && r.Private != nil {
		t.Logf("root: bytes %d private %d (%d less); %d of %d kept nodes unknown", r.Bytes, *r.Private, r.Bytes-*r.Private, unknown, len(res.Nodes))
	} else {
		t.Logf("root private unknown; %d of %d kept nodes unknown", unknown, len(res.Nodes))
	}
}

// folders is the own allocation of every folder in res, lstat'd under root:
// what a tree with no files in it would hold.
func folders(t *testing.T, root string, res scanner.Result) int64 {
	t.Helper()
	var sum int64
	for _, n := range res.Nodes {
		if n.Kind == model.NodeDir {
			sum += alloc(t, filepath.Join(root, n.Path))
		}
	}
	return sum
}

func TestPrivateUniqueFileEqualsItsSize(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("a/b/u.bin", 4*mb))
	flush(t)
	res := withReader(t, true, everything(root))
	f := need(t, res, "/a/b/u.bin")
	if got := priv(t, f); got != f.Bytes || got < 4*mb {
		t.Errorf("unique file: private %d bytes %d; want equal and at least 4 MB", got, f.Bytes)
	}
	// Directories are not clone-shared: a plain tree has private == bytes all the way up.
	for _, p := range []string{"/", "/a", "/a/b"} {
		n := need(t, res, p)
		if got := priv(t, n); got != n.Bytes {
			t.Errorf("%s: private %d, bytes %d; want equal on a tree with no clones", p, got, n.Bytes)
		}
	}
}

func TestPrivateCloneOfFileOutsideTreeIsZero(t *testing.T) {
	outside := t.TempDir()
	ft.Build(t, outside, ft.File("x.bin", 4*mb))
	root := t.TempDir()
	ft.Build(t, root, ft.Dir("d"))
	clone(t, filepath.Join(outside, "x.bin"), filepath.Join(root, "d", "c.bin"))
	flush(t)
	res := withReader(t, true, everything(root))
	c := need(t, res, "/d/c.bin")
	if c.Bytes < 4*mb || priv(t, c) != 0 {
		t.Errorf("clone of an outside file: bytes %d private %d; want bytes >= 4 MB and private 0", c.Bytes, priv(t, c))
	}
	// Deleting the tree frees only its folders: the data stays with the original.
	r := need(t, res, "/")
	if want := folders(t, root, res); priv(t, r) != want {
		t.Errorf("root private %d; want the folders' own %d", priv(t, r), want)
	}
	if r.Bytes-priv(t, r) < 4*mb {
		t.Errorf("root bytes %d private %d; the 4 MB clone should be the difference", r.Bytes, priv(t, r))
	}
}

func TestPrivateCloneGroupInsideTreeIsALowerBound(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("g/orig.bin", 4*mb))
	clone(t, filepath.Join(root, "g", "orig.bin"), filepath.Join(root, "g", "c1.bin"))
	clone(t, filepath.Join(root, "g", "orig.bin"), filepath.Join(root, "g", "c2.bin"))
	flush(t)
	res := withReader(t, true, everything(root))
	for _, name := range []string{"orig.bin", "c1.bin", "c2.bin"} {
		n := need(t, res, "/g/"+name)
		if priv(t, n) != 0 {
			t.Errorf("%s: private %d; every member of a clone group reports 0", name, priv(t, n))
		}
	}
	// Deleting the whole group frees 4 MB, which this lower bound can't see.
	g := need(t, res, "/g")
	if priv(t, g) != g.Bytes-3*need(t, res, "/g/orig.bin").Bytes {
		t.Errorf("/g: private %d bytes %d; want only the folder's own blocks", priv(t, g), g.Bytes)
	}
}

func TestPrivateMixedTreeSumsUpTheTree(t *testing.T) {
	outside := t.TempDir()
	ft.Build(t, outside, ft.File("x.bin", 4*mb))
	root := t.TempDir()
	ft.Build(t, root, ft.File("a/b/unique.bin", 2*mb), ft.File("g/grp.bin", 2*mb))
	clone(t, filepath.Join(outside, "x.bin"), filepath.Join(root, "a", "out.bin"))
	clone(t, filepath.Join(root, "g", "grp.bin"), filepath.Join(root, "g", "grp2.bin"))
	flush(t)
	res := withReader(t, true, everything(root))

	shared := need(t, res, "/a/out.bin").Bytes + need(t, res, "/g/grp.bin").Bytes + need(t, res, "/g/grp2.bin").Bytes
	r := need(t, res, "/")
	if want := r.Bytes - shared; priv(t, r) != want {
		t.Errorf("root private %d; want bytes %d minus the shared files' %d = %d", priv(t, r), r.Bytes, shared, want)
	}
	a := need(t, res, "/a")
	if want := a.Bytes - need(t, res, "/a/out.bin").Bytes; priv(t, a) != want {
		t.Errorf("/a private %d; want %d (its unique file and folders)", priv(t, a), want)
	}
	for _, n := range res.Nodes {
		if p := priv(t, n); p < 0 || p > n.Bytes {
			t.Errorf("%s: private %d outside 0..bytes %d", n.Path, p, n.Bytes)
		}
	}
}

// A file with several links is counted once in bytes, and contributes a known
// 0 to the private sum: the kernel reports its full size for every link, so
// the tree cannot tell what a delete would free.
func TestPrivateHardlinkCountsOnceInBytesAndZeroInPrivate(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("h/orig", 1*mb), ft.Hardlink("h/sub/link", "h/orig"), ft.Hardlink("link2", "h/orig"))
	flush(t)
	res := withReader(t, true, everything(root))
	r := need(t, res, "/")
	if r.Files != 1 || r.Bytes < 1*mb {
		t.Errorf("root: files %d bytes %d; the inode counts once in bytes", r.Files, r.Bytes)
	}
	if want := folders(t, root, res); priv(t, r) != want {
		t.Errorf("root private %d; want the folders' own %d (the file has 3 links)", priv(t, r), want)
	}
}

// The other link lives outside the scanned tree: a delete of the tree frees
// nothing for the file, so a private size equal to its bytes would overstate.
func TestPrivateHardlinkWithLinkOutsideTreeIsKnownZero(t *testing.T) {
	outside := t.TempDir()
	ft.Build(t, outside, ft.File("keep.bin", 2*mb))
	root := t.TempDir()
	ft.Build(t, root, ft.Dir("d"))
	if err := os.Link(filepath.Join(outside, "keep.bin"), filepath.Join(root, "d", "link.bin")); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	flush(t)
	res := withReader(t, true, everything(root))
	l := need(t, res, "/d/link.bin")
	if l.Bytes < 2*mb || l.Private == nil || *l.Private != 0 {
		t.Errorf("link: bytes %d private %v; want bytes >= 2 MB and a known 0", l.Bytes, l.Private)
	}
	r := need(t, res, "/")
	if want := folders(t, root, res); priv(t, r) != want {
		t.Errorf("root private %d; want only the folders' own %d", priv(t, r), want)
	}
	if st := withReader(t, false, everything(root)); need(t, st, "/d/link.bin").Private != nil {
		t.Error("link read with fstatat: private known; an unreported size stays unknown")
	}
}

func TestPrivateIsKnownOnAPlainMixedTree(t *testing.T) {
	root := t.TempDir()
	mixedTree(t, root)
	flush(t)
	opts := scanner.Options{Root: root, MapPrefix: root, KeepThreshold: 64 * 1024}
	res := withReader(t, true, opts)
	r := need(t, res, "/")
	// Files, sparse files, symlinks and hard links all report a private size.
	// Unreadable folders contribute their own allocation only.
	if r.Private == nil {
		t.Fatalf("root private unknown on a tree the bulk reader fully measured; unmeasured: %+v", res.Unmeasured)
	}
	// No clones, so the only gap is the two inodes that have several links,
	// each counted once in bytes and as a known 0 in private.
	linked := alloc(t, filepath.Join(root, "h", "orig")) + alloc(t, filepath.Join(root, "h", "other"))
	if want := r.Bytes - linked; *r.Private != want {
		t.Errorf("root private %d bytes %d; want bytes minus the hard-linked files' %d = %d", *r.Private, r.Bytes, linked, want)
	}
}

// Without the bulk reader nothing knows a private size, and the answer is
// unknown, not zero. Nothing else about the scan changes.
func TestPrivateUnknownWithoutBulkAndNothingElseMoves(t *testing.T) {
	root := t.TempDir()
	mixedTree(t, root)
	flush(t)
	opts := scanner.Options{Root: root, MapPrefix: root, KeepThreshold: 64 * 1024, Workers: 1}
	stat := withReader(t, false, opts)
	bulk := withReader(t, true, opts)
	if len(stat.Nodes) == 0 {
		t.Fatal("no nodes")
	}
	for _, n := range stat.Nodes {
		if n.Private != nil {
			t.Errorf("%s: fstatat reader invented private size %d", n.Path, *n.Private)
		}
	}
	for _, n := range bulk.Nodes {
		if n.Private == nil {
			t.Errorf("%s: bulk reader left private unknown", n.Path)
		}
	}
	if stat.TotalBytes != bulk.TotalBytes || stat.TotalFiles != bulk.TotalFiles || len(stat.Nodes) != len(bulk.Nodes) {
		t.Errorf("readers disagree: fstatat %d/%d/%d bytes/files/nodes, bulk %d/%d/%d",
			stat.TotalBytes, stat.TotalFiles, len(stat.Nodes), bulk.TotalBytes, bulk.TotalFiles, len(bulk.Nodes))
	}
	if !reflect.DeepEqual(noPrivate(stat), noPrivate(bulk)) {
		t.Errorf("results differ beyond private sizes:\n%s", firstDiff(noPrivate(stat), noPrivate(bulk)))
	}
}

// Unknown spreads upward from the file that was not measured, and stops at
// folders that hold no such file.
func TestPrivateUnknownSpreadsOnlyUpward(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("files/deep/f.bin", 4096), ft.Dir("onlydirs/a/b"))
	opts := everything(root)
	opts.Keep = func(string) bool { return true }
	res := withReader(t, false, opts)
	for _, p := range []string{"/", "/files", "/files/deep", "/files/deep/f.bin"} {
		if n := need(t, res, p); n.Private != nil {
			t.Errorf("%s: private %d; a file below was not measured, so it is unknown", p, *n.Private)
		}
	}
	for _, p := range []string{"/onlydirs", "/onlydirs/a", "/onlydirs/a/b"} {
		n := need(t, res, p)
		if got := priv(t, n); got != n.Bytes {
			t.Errorf("%s: private %d bytes %d; folders hold only their own blocks", p, got, n.Bytes)
		}
	}
}

// A file whose private size is 0 is a known zero; a missing one is not.
func TestPrivateZeroIsNotUnknown(t *testing.T) {
	outside := t.TempDir()
	ft.Build(t, outside, ft.File("x.bin", 1*mb))
	root := t.TempDir()
	clone(t, filepath.Join(outside, "x.bin"), filepath.Join(root, "c.bin"))
	flush(t)
	c := need(t, withReader(t, true, everything(root)), "/c.bin")
	if c.Private == nil || *c.Private != 0 {
		t.Errorf("clone: private %v; want a known 0", c.Private)
	}
	c = need(t, withReader(t, false, everything(root)), "/c.bin")
	if c.Private != nil {
		t.Errorf("clone read with fstatat: private %d; want unknown", *c.Private)
	}
}
