package tests

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sagnik26/macdoc/internal/scanner"
	ft "github.com/sagnik26/macdoc/internal/testutil/fstree"
)

// These tests cover the range Measure returns for one folder (ADR 0005,
// stage 3). The scenarios mirror docs/experiments/S1-apfs-private-size/
// dirtest.py with real APFS clones. The true freed amount comes from how each
// tree is built (which files hold their own data), and every test requires
// lower <= truth <= upper. Free-space measurement on the shared Data volume
// is too noisy for 4 MB, so truth_test.go reads it on a throwaway volume when
// asked.

// measure runs Measure and fails on error.
func measure(t *testing.T, dir string, limit int) scanner.Bounds {
	t.Helper()
	flush(t)
	b, err := scanner.Measure(context.Background(), dir, limit)
	if err != nil {
		t.Fatalf("Measure(%s): %v", dir, err)
	}
	return b
}

// span requires lower <= truth <= upper with an upper that is known, and
// returns nothing: the three numbers are the whole claim.
func span(t *testing.T, b scanner.Bounds, truth int64) {
	t.Helper()
	if !b.Bounded {
		t.Fatalf("upper unknown (%+v); want a range", b)
	}
	if b.Lower > truth || truth > b.Upper {
		t.Errorf("truth %d outside [%d, %d]", truth, b.Lower, b.Upper)
	}
	if b.Lacking != 0 || b.Unread != 0 {
		t.Errorf("lacking %d unread %d; want 0", b.Lacking, b.Unread)
	}
}

// own is the folders' own allocation under dir, including dir itself: what a
// tree with no data in it holds.
func own(t *testing.T, dir string) int64 {
	t.Helper()
	var sum int64
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			sum += alloc(t, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

// overwrite replaces length bytes of the file at offset with new data, so
// the file stops sharing those blocks with its clone source.
func overwrite(t *testing.T, p string, offset, length int64) {
	t.Helper()
	cmd := exec.Command("dd", "if=/dev/urandom", "of="+p, "bs=1048576", "count="+strconv.FormatInt(length/mb, 10), "seek="+strconv.FormatInt(offset/mb, 10), "conv=notrunc")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dd: %v: %s", err, out)
	}
}

func TestBoundsUniqueFilesAreExact(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("d/sub/u.bin", 4*mb), ft.File("d/v.bin", 2*mb))
	b := measure(t, filepath.Join(root, "d"), 0)
	truth := own(t, filepath.Join(root, "d")) + alloc(t, filepath.Join(root, "d/sub/u.bin")) + alloc(t, filepath.Join(root, "d/v.bin"))
	span(t, b, truth)
	if b.Lower != truth || b.Upper != truth {
		t.Errorf("no sharing: [%d, %d]; want both %d", b.Lower, b.Upper, truth)
	}
	if b.Files != 2 {
		t.Errorf("files %d; want 2", b.Files)
	}
}

// A unique file plus a clone of a file outside: the clone frees nothing.
func TestBoundsCloneOfOutsideFileIsExact(t *testing.T) {
	outside := t.TempDir()
	ft.Build(t, outside, ft.File("x.bin", 4*mb))
	root := t.TempDir()
	ft.Build(t, root, ft.File("d/sub/u.bin", 4*mb))
	clone(t, filepath.Join(outside, "x.bin"), filepath.Join(root, "d", "c.bin"))
	d := filepath.Join(root, "d")
	b := measure(t, d, 0)
	truth := own(t, d) + alloc(t, filepath.Join(d, "sub/u.bin"))
	span(t, b, truth)
	if b.Lower != truth {
		t.Errorf("lower %d; want %d (the clone adds nothing)", b.Lower, truth)
	}
	// The group has a member outside, so none of its data frees.
	if b.Upper != truth {
		t.Errorf("upper %d; want %d (a member outside keeps the clone's data)", b.Upper, truth)
	}
}

// A clone group wholly inside: the lower bound sees none of it, the upper
// bound counts one copy, which is what frees.
func TestBoundsCloneGroupInsideUpperIsExact(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("g/orig.bin", 4*mb))
	g := filepath.Join(root, "g")
	clone(t, filepath.Join(g, "orig.bin"), filepath.Join(g, "c1.bin"))
	clone(t, filepath.Join(g, "orig.bin"), filepath.Join(g, "c2.bin"))
	b := measure(t, g, 0)
	truth := own(t, g) + alloc(t, filepath.Join(g, "orig.bin"))
	span(t, b, truth)
	if b.Lower != own(t, g) {
		t.Errorf("lower %d; want only the folder's %d", b.Lower, own(t, g))
	}
	if b.Upper != truth {
		t.Errorf("upper %d; want one copy: %d", b.Upper, truth)
	}
}

// Two clones of one outside file: nothing frees, and both bounds say so.
func TestBoundsTwoClonesOfOutsideFile(t *testing.T) {
	outside := t.TempDir()
	ft.Build(t, outside, ft.File("y.bin", 4*mb))
	root := t.TempDir()
	ft.Build(t, root, ft.Dir("d"))
	d := filepath.Join(root, "d")
	clone(t, filepath.Join(outside, "y.bin"), filepath.Join(d, "a.bin"))
	clone(t, filepath.Join(outside, "y.bin"), filepath.Join(d, "b.bin"))
	b := measure(t, d, 0)
	truth := own(t, d)
	span(t, b, truth)
	if b.Lower != truth || b.Upper != truth {
		t.Errorf("[%d, %d]; want both %d", b.Lower, b.Upper, truth)
	}
}

// The mix from the experiment: a unique file, a group inside, a clone of an
// outside file. Only the clone's data stays.
func TestBoundsMixedTree(t *testing.T) {
	outside := t.TempDir()
	ft.Build(t, outside, ft.File("x.bin", 4*mb))
	root := t.TempDir()
	ft.Build(t, root, ft.File("d/u.bin", 2*mb), ft.File("d/g.bin", 2*mb))
	d := filepath.Join(root, "d")
	clone(t, filepath.Join(d, "g.bin"), filepath.Join(d, "g1.bin"))
	clone(t, filepath.Join(outside, "x.bin"), filepath.Join(d, "c.bin"))
	b := measure(t, d, 0)
	truth := own(t, d) + alloc(t, filepath.Join(d, "u.bin")) + alloc(t, filepath.Join(d, "g.bin"))
	span(t, b, truth)
	if b.Lower != own(t, d)+alloc(t, filepath.Join(d, "u.bin")) {
		t.Errorf("lower %d; want the folders and the unique file", b.Lower)
	}
	if b.Upper != truth {
		t.Errorf("upper %d; want %d", b.Upper, truth)
	}
}

// Files with several links, every link inside the folder: the inode frees
// once, which the lower bound cannot see and the upper bound counts once.
func TestBoundsHardlinksInside(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("h/orig", 2*mb), ft.Hardlink("h/sub/link", "h/orig"), ft.Hardlink("h/link2", "h/orig"))
	h := filepath.Join(root, "h")
	b := measure(t, h, 0)
	truth := own(t, h) + alloc(t, filepath.Join(h, "orig"))
	span(t, b, truth)
	if b.Lower != own(t, h) {
		t.Errorf("lower %d; want the folders' %d (3 links: nothing is certain)", b.Lower, own(t, h))
	}
	if b.Upper != truth {
		t.Errorf("upper %d; want the inode once: %d", b.Upper, truth)
	}
}

// One link outside the folder: nothing of the file frees. The upper bound
// overstates here, as documented; the lower bound is exact.
func TestBoundsHardlinkWithLinkOutside(t *testing.T) {
	outside := t.TempDir()
	ft.Build(t, outside, ft.File("keep.bin", 2*mb))
	root := t.TempDir()
	ft.Build(t, root, ft.Dir("d"))
	d := filepath.Join(root, "d")
	if err := os.Link(filepath.Join(outside, "keep.bin"), filepath.Join(d, "link.bin")); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	b := measure(t, d, 0)
	truth := own(t, d)
	span(t, b, truth)
	if b.Lower != truth {
		t.Errorf("lower %d; want %d", b.Lower, truth)
	}
	if b.Upper < b.Lower+2*mb {
		t.Errorf("upper %d; want it to count the linked file once", b.Upper)
	}
}

// Clones that were rewritten get their own clone ID, so groups cannot be
// summed by ID alone: a chain of rewritten clones shares blocks across IDs.
// The range must still hold the union of the blocks.
func TestBoundsChainOfRewrittenClones(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("c/a.bin", 4*mb))
	c := filepath.Join(root, "c")
	clone(t, filepath.Join(c, "a.bin"), filepath.Join(c, "b.bin"))
	overwrite(t, filepath.Join(c, "b.bin"), 1*mb, 1*mb)
	clone(t, filepath.Join(c, "b.bin"), filepath.Join(c, "c.bin"))
	overwrite(t, filepath.Join(c, "c.bin"), 2*mb, 1*mb)
	clone(t, filepath.Join(c, "a.bin"), filepath.Join(c, "d.bin"))
	b := measure(t, c, 0)
	// a's four blocks, b's new MB and c's new MB: nothing else exists.
	truth := own(t, c) + 6*mb
	span(t, b, truth)
	t.Logf("chain: lower %d truth %d upper %d (allocated %d)", b.Lower, truth, b.Upper, own(t, c)+4*4*mb)
}

func TestBoundsStopsAtCloneLimit(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("d/a.bin", 1*mb), ft.File("d/b.bin", 1*mb), ft.File("d/c.bin", 1*mb))
	d := filepath.Join(root, "d")
	for _, n := range []string{"a", "b", "c"} {
		clone(t, filepath.Join(d, n+".bin"), filepath.Join(d, n+"2.bin"))
	}
	b := measure(t, d, 2) // three groups, room for two
	if b.Bounded {
		t.Errorf("bounded with %+v; three groups cannot fit in two entries", b)
	}
	if b.Lower != own(t, d) {
		t.Errorf("lower %d; want %d (it needs no groups)", b.Lower, own(t, d))
	}
	if full := measure(t, d, 3); !full.Bounded || full.Upper != own(t, d)+3*alloc(t, filepath.Join(d, "a.bin")) {
		t.Errorf("limit 3: %+v; want a range up to the three copies", full)
	}
}

func TestBoundsIgnoresSymlinks(t *testing.T) {
	outside := t.TempDir()
	ft.Build(t, outside, ft.File("big/x.bin", 8*mb))
	root := t.TempDir()
	ft.Build(t, root, ft.File("d/u.bin", 1*mb), ft.Symlink("d/link", filepath.Join(outside, "big")))
	d := filepath.Join(root, "d")
	b := measure(t, d, 0)
	if b.Upper > b.Lower+alloc(t, filepath.Join(d, "link"))+mb/2 || b.Upper >= 8*mb {
		t.Errorf("[%d, %d]: followed a symlink to 8 MB outside", b.Lower, b.Upper)
	}
	// A symlink as the root is refused, not followed.
	if _, err := scanner.Measure(context.Background(), filepath.Join(d, "link"), 0); err == nil {
		t.Error("Measure of a symlink: no error")
	}
}

func TestBoundsCanceled(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("d/a.bin", 1*mb))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanner.Measure(ctx, filepath.Join(root, "d"), 0); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled", err)
	}
}

// An unreadable folder is missing from both bounds, so the upper bound must
// not be offered.
func TestBoundsUnreadableFolderHasNoUpper(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("d/a.bin", 1*mb), ft.File("d/locked/b.bin", 1*mb), ft.Dir("d/locked").WithMode(0))
	b := measure(t, filepath.Join(root, "d"), 0)
	if b.Bounded || b.Unread == 0 {
		t.Errorf("%+v; want upper unknown and the folder counted as unread", b)
	}
}
