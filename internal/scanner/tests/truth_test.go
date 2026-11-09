package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	ft "github.com/sagnik26/macdoc/internal/testutil/fstree"
)

// This test checks Measure against the kernel: it builds each tree on a
// throwaway APFS volume, reads the free space, deletes the tree and reads it
// again. It is skipped unless MACDOC_ORACLE_VOLUME names the mount point of
// that volume, made as in docs/experiments/S1-apfs-private-size/README.md:
//
//	hdiutil create -size 1g -type SPARSE -fs APFS -volname macdocoracle oracle
//	hdiutil attach -nobrowse -mountpoint "$PWD/mnt" oracle.sparseimage
//	touch "$PWD/mnt/.macdoc-oracle"
//	MACDOC_ORACLE_VOLUME="$PWD/mnt" go test ./internal/scanner/tests -run TestTruth -v
//
// The test deletes only trees it has just built under that mount point, and
// refuses any path that is not the mount point of a separate APFS volume
// holding the marker file you created.

const (
	// slack is the metadata noise a free-space reading may carry.
	slack = 256 << 10
	// marker is the file whose presence says the volume is meant to be deleted from.
	marker = ".macdoc-oracle"
)

// volume returns the checked mount point or skips the test.
func volume(t *testing.T) string {
	t.Helper()
	v := os.Getenv("MACDOC_ORACLE_VOLUME")
	if v == "" {
		t.Skip("set MACDOC_ORACLE_VOLUME to a throwaway APFS volume named macdocoracle to compare with the kernel")
	}
	v, err := filepath.EvalSymlinks(v)
	if err != nil {
		t.Fatal(err)
	}
	var here, up unix.Stat_t
	if err := unix.Stat(v, &here); err != nil {
		t.Fatal(err)
	}
	if err := unix.Stat(filepath.Dir(v), &up); err != nil {
		t.Fatal(err)
	}
	if here.Dev == up.Dev {
		t.Fatalf("%s is not a separate volume; refusing to delete anything there", v)
	}
	var fsys unix.Statfs_t
	if err := unix.Statfs(v, &fsys); err != nil {
		t.Fatal(err)
	}
	if got := unix.ByteSliceToString(fsys.Mntonname[:]); got != v || unix.ByteSliceToString(fsys.Fstypename[:]) != "apfs" {
		t.Fatalf("%s is not the mount point of an APFS volume (mounted on %q); refusing to delete anything there", v, got)
	}
	var mk unix.Stat_t
	if err := unix.Stat(v+"/"+marker, &mk); err != nil {
		t.Fatalf("%s has no %s file; refusing to delete anything there", v, marker)
	}
	return v
}

// free is the volume's free space after flushing, read twice as S1 does so
// that delayed releases have landed.
func free(t *testing.T, v string) int64 {
	t.Helper()
	flush(t)
	time.Sleep(1500 * time.Millisecond)
	flush(t)
	var st unix.Statfs_t
	if err := unix.Statfs(v, &st); err != nil {
		t.Fatal(err)
	}
	return int64(st.Bavail) * int64(st.Bsize) //nolint:gosec // G115: block counts fit in int64
}

func TestTruth(t *testing.T) {
	v := volume(t)
	// Files for scenarios to clone from; they stay on the volume throughout.
	outside := filepath.Join(v, "outside")
	if err := os.RemoveAll(outside); err != nil { // left by an earlier run
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	ft.Build(t, outside, ft.File("x.bin", 8*mb), ft.File("y.bin", 8*mb))
	cases := []struct {
		name  string
		build func(d string)
	}{
		{"unique", func(d string) { ft.Build(t, d, ft.File("sub/u.bin", 8*mb)) }},
		{"unique and clone of outside", func(d string) {
			ft.Build(t, d, ft.File("sub/u.bin", 8*mb))
			clone(t, filepath.Join(outside, "x.bin"), filepath.Join(d, "c.bin"))
		}},
		{"group inside", func(d string) {
			ft.Build(t, d, ft.File("g.bin", 8*mb))
			clone(t, filepath.Join(d, "g.bin"), filepath.Join(d, "g1.bin"))
			clone(t, filepath.Join(d, "g.bin"), filepath.Join(d, "g2.bin"))
		}},
		{"two clones of outside", func(d string) {
			clone(t, filepath.Join(outside, "y.bin"), filepath.Join(d, "a.bin"))
			clone(t, filepath.Join(outside, "y.bin"), filepath.Join(d, "b.bin"))
		}},
		{"mix", func(d string) {
			ft.Build(t, d, ft.File("u.bin", 4*mb), ft.File("g.bin", 4*mb))
			clone(t, filepath.Join(d, "g.bin"), filepath.Join(d, "g1.bin"))
			clone(t, filepath.Join(outside, "x.bin"), filepath.Join(d, "c.bin"))
		}},
		{"hardlinks inside", func(d string) {
			ft.Build(t, d, ft.File("h", 8*mb), ft.Hardlink("sub/l", "h"))
		}},
		{"hardlink to outside", func(d string) {
			ft.Build(t, d, ft.Dir("sub"))
			if err := os.Link(filepath.Join(outside, "y.bin"), filepath.Join(d, "l")); err != nil {
				t.Fatal(err)
			}
		}},
		{"rewritten clone beside its source", func(d string) {
			ft.Build(t, d, ft.File("a.bin", 8*mb))
			clone(t, filepath.Join(d, "a.bin"), filepath.Join(d, "b.bin"))
			overwrite(t, filepath.Join(d, "b.bin"), 2*mb, 2*mb)
		}},
		{"chain of rewritten clones", func(d string) {
			ft.Build(t, d, ft.File("a.bin", 8*mb))
			clone(t, filepath.Join(d, "a.bin"), filepath.Join(d, "b.bin"))
			overwrite(t, filepath.Join(d, "b.bin"), 1*mb, 2*mb)
			clone(t, filepath.Join(d, "b.bin"), filepath.Join(d, "c.bin"))
			overwrite(t, filepath.Join(d, "c.bin"), 4*mb, 2*mb)
			clone(t, filepath.Join(d, "a.bin"), filepath.Join(d, "d.bin"))
		}},
		{"rewritten clone of outside", func(d string) {
			clone(t, filepath.Join(outside, "x.bin"), filepath.Join(d, "c.bin"))
			overwrite(t, filepath.Join(d, "c.bin"), 0, 3*mb)
		}},
	}
	for i, c := range cases {
		d := filepath.Join(v, "case"+string(rune('a'+i)))
		c.build(d)
		b := measure(t, d, 0)
		before := free(t, v)
		if err := os.RemoveAll(d); err != nil {
			t.Fatal(err)
		}
		freed := free(t, v) - before
		t.Logf("%-36s freed %5.1f MB  lower %5.1f  upper %5.1f (bounded %v)", c.name,
			float64(freed)/mb, float64(b.Lower)/mb, float64(b.Upper)/mb, b.Bounded)
		if !b.Bounded {
			t.Errorf("%s: no upper bound", c.name)
			continue
		}
		if freed < b.Lower-slack || freed > b.Upper+slack {
			t.Errorf("%s: freed %d outside [%d, %d]", c.name, freed, b.Lower, b.Upper)
		}
	}
}
