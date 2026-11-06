package tests

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/scanner"
	ft "github.com/sagnik26/macdoc/internal/testutil/fstree"
)

// withReader runs a scan with the getattrlistbulk reader on or off and puts
// the switch back afterwards.
func withReader(t *testing.T, bulk bool, opts scanner.Options) scanner.Result {
	t.Helper()
	orig := scanner.Bulk
	scanner.Bulk = bulk
	defer func() { scanner.Bulk = orig }()
	res := scan(t, opts)
	res.Duration = 0
	sort.Slice(res.Unmeasured, func(i, j int) bool { return res.Unmeasured[i].Path < res.Unmeasured[j].Path })
	return res
}

// requireSame scans opts with both readers (and several worker counts) and
// requires deeply equal results. It returns the fstatat result.
func requireSame(t *testing.T, opts scanner.Options) scanner.Result {
	t.Helper()
	var first scanner.Result
	for i, w := range []int{1, 4, 16} {
		opts.Workers = w
		stat := withReader(t, false, opts)
		bulk := withReader(t, true, opts)
		if !reflect.DeepEqual(stat, bulk) {
			t.Fatalf("workers=%d: readers differ\nfstatat: bytes=%d files=%d nodes=%d unmeasured=%+v\nbulk:    bytes=%d files=%d nodes=%d unmeasured=%+v\n%s",
				w, stat.TotalBytes, stat.TotalFiles, len(stat.Nodes), stat.Unmeasured,
				bulk.TotalBytes, bulk.TotalFiles, len(bulk.Nodes), bulk.Unmeasured, firstDiff(stat, bulk))
		}
		if i == 0 {
			first = stat
		}
	}
	return first
}

func firstDiff(a, b scanner.Result) string {
	m := map[string]string{}
	for _, n := range a.Nodes {
		m[n.Path] = fmt.Sprintf("%+v", n)
	}
	var out []string
	for _, n := range b.Nodes {
		if m[n.Path] != fmt.Sprintf("%+v", n) {
			out = append(out, fmt.Sprintf("fstatat %s\nbulk    %+v", m[n.Path], n))
		}
		delete(m, n.Path)
	}
	for p, v := range m {
		out = append(out, "only in fstatat: "+v+" "+p)
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return strings.Join(out, "\n")
}

// mixedTree is a fixture with every entry type and layout the scanner has a
// rule for.
func mixedTree(t *testing.T, root string) {
	t.Helper()
	outside := t.TempDir()
	ft.Build(t, outside, ft.File("target/big", 3*mb))
	es := []ft.Entry{
		// plain files of several sizes, including ones above the threshold
		ft.File("a/small", 100),
		ft.File("a/mid", 200*1024),
		ft.File("a/big", 2*mb),
		ft.Dir("empty"),
		ft.Dir("a/b/c/empty"),
		// sparse: huge logical size, almost nothing allocated
		ft.Sparse("sparse/hole", 400*mb),
		ft.File("sparse/part", 64*1024),
		// symlinks are never followed
		ft.Symlink("links/dangling", "/nonexistent/at/all"),
		ft.Symlink("links/up", ".."),
		ft.Symlink("links/self", "."),
		ft.Symlink("links/abs", outside+"/target"),
		ft.Symlink("links/rel", "../a/big"),
		ft.Symlink("links/long", strings.Repeat("x/", 200)+"end"),
		// hard links, two groups, one with three names across directories
		ft.File("h/orig", 32*1024),
		ft.Hardlink("h/link1", "h/orig"),
		ft.Hardlink("h/sub/link2", "h/orig"),
		ft.File("h/other", 8192),
		ft.Hardlink("h/other2", "h/other"),
		// names that are awkward for parsers and for case folding
		ft.File("uni/日本語 ディレクトリ/ファイル.txt", 128*1024),
		ft.File("uni/emoji 🎉/party 🥳.bin", 128*1024),
		ft.File("uni/café/nfc", 5000),
		ft.File("uni/café/nfd", 5000),
		ft.File("uni/with space/  leading and trailing  ", 4096),
		ft.File("uni/.hidden/.dotfile", 4096),
		ft.File("uni/tab\tname/new\nline", 4096),
		ft.File("uni/"+strings.Repeat("n", 250), 4096),
		ft.File("uni/"+strings.Repeat("é", 120), 4096),
		ft.File("keepme/x", 10),
		// watched names, matched case-insensitively
		ft.File("SkipMe/x", 1*mb),
		ft.File("Case/OpaqueDir/y", 2*mb),
		ft.File("Case/Opaque Ünï/z", 2*mb),
		ft.File("Case/other/w", 2*mb),
		ft.File("Case/SKIP-File", 1*mb),
	}
	// wide: more entries than one Readdirnames batch and more than one reply
	for i := range 1500 {
		es = append(es, ft.File(fmt.Sprintf("wide/f%04d", i), int64(1+i%7)*100))
	}
	// deep
	deep := "deep"
	for i := range 60 {
		deep += fmt.Sprintf("/d%02d", i)
		if i%10 == 9 {
			es = append(es, ft.File(deep+"/leaf", 70*1024))
		}
	}
	es = append(es, ft.File(deep+"/bottom", 1*mb))
	// unreadable
	es = append(es,
		ft.File("locked/secret", 2*mb), ft.Dir("locked").WithMode(0),
		ft.File("noexec/f", 10), ft.Dir("noexec").WithMode(0o400),
		ft.File("nolist/f", 10), ft.Dir("nolist").WithMode(0o100),
		ft.File("deep2/unreadable", 4096).WithMode(0),
	)
	ft.Build(t, root, es...)
}

func TestReadersAgreeOnMixedTree(t *testing.T) {
	root := t.TempDir()
	mixedTree(t, root)
	opts := scanner.Options{
		Root:          root,
		MapPrefix:     root,
		KeepThreshold: 64 * 1024,
		Keep:          func(p string) bool { return strings.Contains(p, "keep") || strings.HasSuffix(p, "/empty") },
		Skip:          []string{root + "/skipme", root + "/case/skip-file"},
		Opaque:        []string{root + "/CASE/opaquedir", root + "/case/OPAQUE ÜNÏ"},
	}
	res := requireSame(t, opts)

	// The comparison above is only meaningful if the tree exercised the
	// rules, so check a few things the fixture is meant to produce.
	if _, ok := node(res, "/a/big"); !ok {
		t.Error("big file not kept")
	}
	if _, ok := node(res, "/SkipMe"); ok {
		t.Error("skip path kept")
	}
	if n, ok := node(res, "/Case/OpaqueDir"); !ok || n.Bytes < 2*mb {
		t.Errorf("opaque dir %+v %v; want kept and measured", n, ok)
	}
	if _, ok := node(res, "/Case/OpaqueDir/y"); ok {
		t.Error("opaque child kept")
	}
	if _, ok := node(res, "/Case/other/w"); !ok {
		t.Error("sibling of an opaque dir not kept")
	}
	// Case folding of non-ASCII names must work the same in both readers.
	if n, ok := node(res, "/Case/Opaque Ünï"); !ok || n.Bytes < 2*mb {
		t.Errorf("non-ASCII opaque dir %+v %v; want kept and measured", n, ok)
	}
	if _, ok := node(res, "/Case/Opaque Ünï/z"); ok {
		t.Error("non-ASCII opaque child kept")
	}
	if _, ok := node(res, "/Case/SKIP-File"); ok {
		t.Error("skipped file kept")
	}
	if len(res.Unmeasured) == 0 {
		t.Error("no unmeasured entries; the unreadable cases did not trigger")
	}
}

func TestReadersAgreeOnSingleRules(t *testing.T) {
	// Each rule alone, so a disagreement names the rule.
	cases := map[string][]ft.Entry{
		"hardlinks":  {ft.File("o", 40960), ft.Hardlink("l1", "o"), ft.Hardlink("d/l2", "o"), ft.Hardlink("d/e/l3", "o")},
		"symlinks":   {ft.File("f", 100), ft.Symlink("s1", "f"), ft.Symlink("s2", "nowhere"), ft.Symlink("s3", ".")},
		"empty":      {ft.Dir("a"), ft.Dir("b/c"), ft.Dir("b/d")},
		"sparse":     {ft.Sparse("s", 1<<30), ft.Sparse("t", 5*mb)},
		"unicode":    {ft.File("é/ü/ñ", 4096), ft.File("日本/語", 8192), ft.File("🙂/🙃", 4096)},
		"wide":       wideEntries(1100),
		"unreadable": {ft.File("l/f", 5000), ft.Dir("l").WithMode(0), ft.File("ok/f", 5000)},
	}
	for name, es := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			ft.Build(t, root, es...)
			requireSame(t, scanner.Options{Root: root, MapPrefix: root, KeepThreshold: 4096})
		})
	}
}

func wideEntries(n int) []ft.Entry {
	es := make([]ft.Entry, 0, n)
	for i := range n {
		es = append(es, ft.File(fmt.Sprintf("w/%05d", i), 100))
	}
	return es
}

// TestBulkAvoidsPerEntryStat proves the bulk reader does the work without
// fstatat: on an ordinary tree the hook is never called, and the old reader
// calls it once per entry.
func TestBulkAvoidsPerEntryStat(t *testing.T) {
	root := t.TempDir()
	var es []ft.Entry
	for i := range 20 {
		for j := range 25 {
			es = append(es, ft.File(fmt.Sprintf("d%02d/f%02d", i, j), 100))
		}
	}
	es = append(es, ft.Symlink("d00/link", "f00"), ft.Hardlink("d01/hl", "d01/f00"))
	ft.Build(t, root, es...)

	orig := scanner.Fstatat
	origBulk := scanner.Bulk
	t.Cleanup(func() { scanner.Fstatat = orig; scanner.Bulk = origBulk })
	var calls atomic.Int64
	scanner.Fstatat = func(fd int, name string, st *unix.Stat_t, flags int) error {
		calls.Add(1)
		return orig(fd, name, st, flags)
	}
	opts := scanner.Options{Root: root, MapPrefix: root}

	scanner.Bulk = true
	scan(t, opts)
	if got := calls.Load(); got != 0 {
		t.Errorf("bulk reader made %d fstatat calls on an ordinary tree; want 0", got)
	}
	calls.Store(0)
	scanner.Bulk = false
	scan(t, opts)
	if got, want := calls.Load(), int64(20*25+20+2); got != want {
		t.Errorf("fstatat reader made %d calls; want %d (one per entry)", got, want)
	}
}

// TestBulkSameWithHookInstalled checks that a transparent Fstatat hook does
// not change what the bulk reader reports.
func TestBulkSameWithHookInstalled(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("a/f", 5000), ft.File("b/g", 5000))
	orig := scanner.Fstatat
	t.Cleanup(func() { scanner.Fstatat = orig })
	scanner.Fstatat = func(fd int, name string, st *unix.Stat_t, flags int) error {
		return orig(fd, name, st, flags)
	}
	requireSame(t, scanner.Options{Root: root, MapPrefix: root, KeepThreshold: 4096})
}

// TestReadersAgreeOnRealTree compares the two readers over a real directory:
// MACDOC_SCAN_ROOT if set (for example a mounted disk image or a project
// checkout), else the read-only system frameworks. The tree must not change
// during the test.
//
// Hard links that cross directories are credited to whichever directory the
// walk reaches first, and the two readers list a directory in different
// orders, so per-directory sums can legitimately move between a link's
// parent directories. Totals, unreadable paths, kept files and the set of
// kept directories must still be identical.
func TestReadersAgreeOnRealTree(t *testing.T) {
	root := os.Getenv("MACDOC_SCAN_ROOT")
	if root == "" {
		root = "/System/Library/Frameworks"
	}
	if _, err := os.Stat(root); err != nil { //nolint:gosec // G703: the root comes from a developer's own environment variable
		t.Skipf("%s: %v", root, err)
	}
	if testing.Short() && os.Getenv("MACDOC_SCAN_ROOT") == "" {
		t.Skip("short mode")
	}
	opts := scanner.Options{Root: root, MapPrefix: root, KeepThreshold: 1 * mb, Workers: 1}
	stat := withReader(t, false, opts)
	bulk := withReader(t, true, opts)
	if stat.TotalBytes != bulk.TotalBytes || stat.TotalFiles != bulk.TotalFiles {
		t.Errorf("totals: fstatat %d bytes/%d files; bulk %d/%d", stat.TotalBytes, stat.TotalFiles, bulk.TotalBytes, bulk.TotalFiles)
	}
	if !reflect.DeepEqual(stat.Unmeasured, bulk.Unmeasured) {
		t.Errorf("unmeasured differ:\n%+v\n%+v", stat.Unmeasured, bulk.Unmeasured)
	}
	if !reflect.DeepEqual(kept(stat), kept(bulk)) {
		t.Errorf("kept nodes differ:\n%s", firstDiff(stat, bulk))
	}
	t.Logf("%s: %d bytes, %d files, %d kept nodes, %d unmeasured", root, bulk.TotalBytes, bulk.TotalFiles, len(bulk.Nodes), len(bulk.Unmeasured))
}

// kept lists kept files with their sizes and kept directories by path.
func kept(r scanner.Result) []string {
	var out []string
	for _, n := range r.Nodes {
		if n.Kind == model.NodeFile {
			out = append(out, fmt.Sprintf("f %d %d %s", n.Bytes, n.MtimeMax, n.Path))
		} else {
			out = append(out, "d "+n.Path)
		}
	}
	sort.Strings(out)
	return out
}
