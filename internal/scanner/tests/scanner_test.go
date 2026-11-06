package tests

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/scanner"

	"golang.org/x/sys/unix"

	"github.com/sagnik26/macdoc/internal/model"
	ft "github.com/sagnik26/macdoc/internal/testutil/fstree"
)

const mb = 1 << 20

// alloc returns the allocated bytes of p (lstat, never followed).
func alloc(t *testing.T, p string) int64 {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Lstat(p, &st); err != nil {
		t.Fatal(err)
	}
	return st.Blocks * 512
}

// oracle walks root independently and returns allocated bytes and regular
// file count, hard links counted once, symlinks not followed.
func oracle(t *testing.T, root string) (bytes, files int64) {
	t.Helper()
	seen := map[uint64]bool{}
	err := filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		var st unix.Stat_t
		if err := unix.Lstat(p, &st); err != nil {
			return err
		}
		if st.Mode&unix.S_IFMT == unix.S_IFREG {
			if st.Nlink > 1 {
				if seen[st.Ino] {
					return nil
				}
				seen[st.Ino] = true
			}
			files++
		}
		bytes += st.Blocks * 512
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return bytes, files
}

func scan(t *testing.T, opts scanner.Options) scanner.Result {
	t.Helper()
	res, err := scanner.Scan(context.Background(), opts)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return res
}

func node(res scanner.Result, p string) (model.Node, bool) {
	for _, n := range res.Nodes {
		if n.Path == p {
			return n, true
		}
	}
	return model.Node{}, false
}

func paths(res scanner.Result) []string {
	var out []string
	for _, n := range res.Nodes {
		out = append(out, n.Path)
	}
	return out
}

// waitGoroutines polls until the goroutine count is back to at most want.
func waitGoroutines(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > want {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutine leak: %d > %d\n%s", runtime.NumGoroutine(), want, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSizes(t *testing.T) {
	root := t.TempDir()
	huge := t.TempDir()
	ft.Build(t, huge, ft.File("big", 4*mb))
	ft.Build(t, root,
		ft.Sparse("sparse", 512*mb),
		ft.File("orig", 64*1024),
		ft.Hardlink("d/link", "orig"),
		ft.Symlink("huge", huge),
		ft.File("d/small", 100),
	)
	res := scan(t, scanner.Options{Root: root, MapPrefix: root, KeepThreshold: mb})

	wantBytes, wantFiles := oracle(t, root)
	if res.TotalBytes != wantBytes || res.TotalFiles != wantFiles {
		t.Errorf("totals %d bytes / %d files; want %d / %d", res.TotalBytes, res.TotalFiles, wantBytes, wantFiles)
	}
	if wantFiles != 3 {
		t.Errorf("oracle files = %d; want 3 (sparse, orig once, small)", wantFiles)
	}
	if res.TotalBytes > mb {
		t.Errorf("total %d: sparse file or symlink target counted", res.TotalBytes)
	}
	orig := alloc(t, filepath.Join(root, "orig"))
	if orig < 64*1024 || res.TotalBytes < orig {
		t.Errorf("orig alloc %d, total %d", orig, res.TotalBytes)
	}
	if _, ok := node(res, "/sparse"); ok {
		t.Error("sparse file kept by logical size")
	}
	if res.HardlinkLimitHit || res.Incomplete || len(res.Unmeasured) != 0 {
		t.Errorf("unexpected flags: %+v", res)
	}
}

func TestHardlinkLimit(t *testing.T) {
	// Each inode has a link outside the scan root, so neither is ever fully
	// seen and both would stay tracked. With a limit of 1 the second one is
	// counted on every link.
	base := t.TempDir()
	ft.Build(t, base,
		ft.File("out/a", 64*1024), ft.Hardlink("scan/a1", "out/a"), ft.Hardlink("scan/a2", "out/a"),
		ft.File("out/b", 64*1024), ft.Hardlink("scan/b1", "out/b"), ft.Hardlink("scan/b2", "out/b"),
	)
	root := filepath.Join(base, "scan")
	blk := alloc(t, filepath.Join(base, "out/a"))
	dirOwn := alloc(t, root)

	res := scan(t, scanner.Options{Root: root})
	if res.HardlinkLimitHit || res.TotalBytes != dirOwn+2*blk || res.TotalFiles != 2 {
		t.Errorf("unlimited: hit=%v bytes=%d files=%d; want false %d 2", res.HardlinkLimitHit, res.TotalBytes, dirOwn+2*blk, res.TotalFiles)
	}
	res = scan(t, scanner.Options{Root: root, HardlinkLimit: 1})
	if !res.HardlinkLimitHit || res.TotalBytes != dirOwn+3*blk || res.TotalFiles != 3 {
		t.Errorf("limit 1: hit=%v bytes=%d files=%d; want true %d 3", res.HardlinkLimitHit, res.TotalBytes, dirOwn+3*blk, res.TotalFiles)
	}
}

func TestLinkSet(t *testing.T) {
	l := scanner.LinkSet{Limit: 1}
	steps := []struct {
		ino   uint64
		nlink uint16
		want  bool
	}{
		{1, 3, true},  // first link of 1: tracked
		{2, 2, true},  // set full: counted, limit hit
		{1, 3, false}, // second link of 1
		{1, 3, false}, // last link of 1: dropped from the set
		{3, 2, true},  // room again: tracked
		{3, 2, false},
	}
	for i, s := range steps {
		if got := l.Count(1, s.ino, s.nlink); got != s.want {
			t.Errorf("step %d: count(%d) = %v; want %v", i, s.ino, got, s.want)
		}
	}
	if !l.Hit || len(l.M) != 0 {
		t.Errorf("hit=%v tracked=%d; want true 0", l.Hit, len(l.M))
	}
}

func TestKeepRules(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root,
		ft.File("big/f", 2*mb),
		ft.File("big/tiny", 10),
		ft.File("small/x", 10),
		ft.File("other/x", 10),
		ft.File("op/big", 2*mb),
		ft.File("op/sub/big2", 2*mb),
		ft.File("skip/huge", 3*mb),
		ft.File("skipfile", 3*mb),
		ft.File("Deep/Case/skipme/f", 3*mb),
	)
	var keepCalls []string
	var mu sync.Mutex
	res := scan(t, scanner.Options{
		Root:          root,
		MapPrefix:     root,
		KeepThreshold: mb,
		Skip:          []string{root + "/SKIP", root + "/skipFILE", root + "/deep/case/SKIPME/"},
		Opaque:        []string{root + "/Op"},
		Keep: func(p string) bool {
			mu.Lock()
			keepCalls = append(keepCalls, p)
			mu.Unlock()
			return p == "/small"
		},
	})

	want := []string{"/", "/op", "/big", "/big/f", "/small"}
	if got := paths(res); !reflect.DeepEqual(got, want) {
		t.Errorf("nodes %v; want %v", got, want)
	}
	for _, p := range keepCalls {
		if strings.HasPrefix(p, "/op/") || strings.HasPrefix(p, root) {
			t.Errorf("Keep called with %q (opaque descendant or unmapped)", p)
		}
	}
	all, _ := oracle(t, root)
	skipped := alloc(t, filepath.Join(root, "skip")) + alloc(t, filepath.Join(root, "skip/huge")) +
		alloc(t, filepath.Join(root, "skipfile")) + alloc(t, filepath.Join(root, "Deep/Case/skipme")) +
		alloc(t, filepath.Join(root, "Deep/Case/skipme/f"))
	if res.TotalBytes != all-skipped {
		t.Errorf("total %d; want %d (all %d minus skipped %d)", res.TotalBytes, all-skipped, all, skipped)
	}
	op, _ := node(res, "/op")
	if op.Bytes < 4*mb || op.Files != 2 || op.Depth != 1 || op.Kind != model.NodeDir {
		t.Errorf("opaque node %+v; want >= 4 MB, 2 files, depth 1", op)
	}
	f, _ := node(res, "/big/f")
	if f.Kind != model.NodeFile || f.Files != 1 || f.Depth != 2 || f.Bytes < 2*mb {
		t.Errorf("file node %+v", f)
	}
	r, _ := node(res, "/")
	if r.Depth != 0 || r.Bytes != res.TotalBytes || r.Files != res.TotalFiles || res.Root != "/" {
		t.Errorf("root node %+v, result root %q", r, res.Root)
	}
}

func TestRootAlwaysKeptAndOrder(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("a/x", 10), ft.File("b/x", 10))
	res := scan(t, scanner.Options{Root: root, KeepThreshold: 1 << 40, Keep: func(string) bool { return true }})
	// a and b have equal sizes: ties are broken by path.
	want := []string{root, root + "/a", root + "/b"}
	if got := paths(res); !reflect.DeepEqual(got, want) {
		t.Errorf("nodes %v; want %v", got, want)
	}
	res = scan(t, scanner.Options{Root: root, KeepThreshold: 1 << 40})
	if got := paths(res); !reflect.DeepEqual(got, []string{root}) {
		t.Errorf("nodes %v; want only root", got)
	}
}

func TestOpaqueRoot(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("d/f", 2*mb))
	res := scan(t, scanner.Options{Root: root, KeepThreshold: mb, Opaque: []string{root}})
	if got := paths(res); !reflect.DeepEqual(got, []string{root}) {
		t.Errorf("nodes %v; want only root", got)
	}
}

func TestMtime(t *testing.T) {
	root := t.TempDir()
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC)
	ft.Build(t, root,
		ft.File("d/a", 10).WithModTime(old),
		ft.File("d/e/b", 10).WithModTime(newer),
		ft.Symlink("d/l", "x"),
		ft.Dir("d/e").WithModTime(old),
		ft.Dir("d").WithModTime(old),
	)
	// /d also holds a symlink whose own mtime is "now", which counts too.
	res := scan(t, scanner.Options{Root: root, MapPrefix: root, Keep: func(string) bool { return true }})
	e, _ := node(res, "/d/e")
	if e.MtimeMax != newer.Unix() {
		t.Errorf("/d/e mtime %d; want %d", e.MtimeMax, newer.Unix())
	}
	d, _ := node(res, "/d")
	if d.MtimeMax < newer.Unix() || d.Files != 2 || d.Depth != 1 {
		t.Errorf("/d %+v", d)
	}
}

func TestMapping(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root,
		ft.File("Users/x/big", 2*mb),
		ft.Dir("Users/x/locked").WithMode(0),
	)
	var keepIn []string
	var mu sync.Mutex
	res := scan(t, scanner.Options{
		Root:          root,
		MapPrefix:     root + "/",
		KeepThreshold: mb,
		Keep: func(p string) bool {
			mu.Lock()
			keepIn = append(keepIn, p)
			mu.Unlock()
			return false
		},
	})
	if res.Root != "/" {
		t.Errorf("root %q", res.Root)
	}
	want := []string{"/", "/Users", "/Users/x", "/Users/x/big"}
	if got := paths(res); !reflect.DeepEqual(got, want) {
		t.Errorf("nodes %v; want %v", got, want)
	}
	if len(res.Unmeasured) != 1 || res.Unmeasured[0].Path != "/Users/x/locked" {
		t.Errorf("unmeasured %+v", res.Unmeasured)
	}
	for _, p := range keepIn {
		if !strings.HasPrefix(p, "/Users") {
			t.Errorf("Keep got unmapped path %q", p)
		}
	}
	if len(keepIn) == 0 {
		t.Error("Keep never called")
	}
}

func TestMapPath(t *testing.T) {
	s := &scanner.Scanner{Prefix: "/System/Volumes/Data"}
	for in, want := range map[string]string{
		"/System/Volumes/Data":            "/",
		"/System/Volumes/Data/Users/x":    "/Users/x",
		"/System/Volumes/DataX/y":         "/System/Volumes/DataX/y",
		"/elsewhere":                      "/elsewhere",
		"/System/Volumes/Data/.fseventsd": "/.fseventsd",
	} {
		if got := s.MapPath(in); got != want {
			t.Errorf("MapPath(%q) = %q; want %q", in, got, want)
		}
	}
	for _, p := range []string{"", "/"} {
		s := &scanner.Scanner{Prefix: p}
		if got := s.MapPath("/a/b"); got != "/a/b" {
			t.Errorf("prefix %q: %q", p, got)
		}
	}
	if scanner.Join("/", "x") != "/x" || scanner.Join("/a", "x") != "/a/x" {
		t.Error("join")
	}
}

func TestDefaultsOnDataVolume(t *testing.T) {
	if _, err := os.Lstat(scanner.DataVolume); err != nil {
		t.Skip("no Data volume")
	}
	// Pre-canceled: only the root is lstat'ed, nothing is read.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := scanner.Scan(ctx, scanner.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Root != "/" || !res.Incomplete || len(res.Nodes) != 1 || res.Nodes[0].Path != "/" {
		t.Errorf("result %+v", res)
	}
}

func TestErrors(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root,
		ft.File("ok/f", 2*mb),
		ft.File("locked/secret", 2*mb),
		ft.Dir("locked").WithMode(0),
		ft.File("noexec/f", 10),
		ft.Dir("noexec").WithMode(0o400),
	)
	res := scan(t, scanner.Options{Root: root, MapPrefix: root, KeepThreshold: mb})
	want := map[string]bool{"/locked": true, "/noexec": true}
	if len(res.Unmeasured) != 2 {
		t.Fatalf("unmeasured %+v; want 2", res.Unmeasured)
	}
	for _, u := range res.Unmeasured {
		if !want[u.Path] || u.Reason != scanner.ReasonPermission {
			t.Errorf("unmeasured %+v", u)
		}
	}
	if _, ok := node(res, "/ok/f"); !ok {
		t.Error("rest of tree not counted")
	}
	if res.TotalFiles != 1 {
		t.Errorf("files %d; want 1", res.TotalFiles)
	}

	file := filepath.Join(root, "ok/f")
	link := filepath.Join(t.TempDir(), "l")
	ft.Build(t, filepath.Dir(link), ft.Symlink("l", root))
	for name, p := range map[string]string{"missing": filepath.Join(root, "nope"), "file": file, "symlink": link} {
		if _, err := scanner.Scan(context.Background(), scanner.Options{Root: p}); err == nil {
			t.Errorf("%s root: no error", name)
		}
	}
}

func TestFailReasons(t *testing.T) {
	s := &scanner.Scanner{}
	s.Fail("/a", &fs.PathError{Op: "open", Path: "/a", Err: syscall.EIO})
	s.Fail("/b", errors.New("boom"))
	s.Fail("/c", &fs.PathError{Op: "open", Path: "/c", Err: syscall.ENOENT})
	s.Fail("/d", syscall.ELOOP)
	s.Fail("/e", syscall.EPERM)
	want := []model.Unmeasured{
		{Path: "/a", Reason: syscall.EIO.Error()},
		{Path: "/b", Reason: "boom"},
		{Path: "/e", Reason: scanner.ReasonPermission},
	}
	if !reflect.DeepEqual(s.Unmeasured, want) {
		t.Errorf("unmeasured %+v; want %+v", s.Unmeasured, want)
	}
}

func TestStatErrorBecomesUnmeasured(t *testing.T) {
	root := t.TempDir()
	ft.Build(t, root, ft.File("bad", 10), ft.File("good", 10), ft.File("gone", 10))
	orig := scanner.Fstatat
	t.Cleanup(func() { scanner.Fstatat = orig })
	scanner.Fstatat = func(fd int, name string, st *unix.Stat_t, flags int) error {
		switch name {
		case "bad":
			return syscall.EIO
		case "gone":
			return syscall.ENOENT
		}
		return orig(fd, name, st, flags)
	}
	res := scan(t, scanner.Options{Root: root, MapPrefix: root})
	if len(res.Unmeasured) != 1 || res.Unmeasured[0].Path != "/bad" || res.Unmeasured[0].Reason != syscall.EIO.Error() {
		t.Errorf("unmeasured %+v", res.Unmeasured)
	}
	if res.TotalFiles != 1 {
		t.Errorf("files %d; want 1", res.TotalFiles)
	}
}

func TestCrossDevice(t *testing.T) {
	// A second device can't be mounted without root, so the stat hook
	// reports a different st_dev for "mnt", as a mount point would.
	root := t.TempDir()
	ft.Build(t, root, ft.File("mnt/f", 2*mb), ft.File("local/f", 2*mb))
	orig := scanner.Fstatat
	t.Cleanup(func() { scanner.Fstatat = orig })
	scanner.Fstatat = func(fd int, name string, st *unix.Stat_t, flags int) error {
		err := orig(fd, name, st, flags)
		if name == "mnt" {
			st.Dev++
		}
		return err
	}
	res := scan(t, scanner.Options{Root: root, MapPrefix: root, KeepThreshold: mb})
	if _, ok := node(res, "/mnt"); ok {
		t.Error("other device kept")
	}
	if _, ok := node(res, "/local/f"); !ok {
		t.Error("local not scanned")
	}
	if res.TotalFiles != 1 || res.TotalBytes >= 4*mb {
		t.Errorf("files %d bytes %d; other device counted", res.TotalFiles, res.TotalBytes)
	}
}

func TestWorkersIdentical(t *testing.T) {
	root := t.TempDir()
	var es []ft.Entry
	for i := range 30 {
		for j := range 8 {
			es = append(es, ft.File(fmt.Sprintf("d%02d/s%d/f", i, j), int64(1+i*j)*4096))
		}
		es = append(es, ft.File(fmt.Sprintf("d%02d/big", i), int64(i%3)*mb/2))
	}
	es = append(es, ft.File("h/orig", 8192), ft.Hardlink("h/link", "h/orig"))
	ft.Build(t, root, es...)
	opts := scanner.Options{Root: root, MapPrefix: root, KeepThreshold: 256 * 1024, Keep: func(p string) bool { return strings.HasSuffix(p, "s3") }}

	var first scanner.Result
	for _, w := range []int{1, 4, 16, 0, 99} {
		opts.Workers = w
		res := scan(t, opts)
		res.Duration = 0
		if w == 1 {
			first = res
			wantBytes, wantFiles := oracle(t, root)
			if res.TotalBytes != wantBytes || res.TotalFiles != wantFiles {
				t.Errorf("totals %d/%d; want %d/%d", res.TotalBytes, res.TotalFiles, wantBytes, wantFiles)
			}
			continue
		}
		if !reflect.DeepEqual(res, first) {
			t.Errorf("workers=%d differs:\n%+v\nvs\n%+v", w, res, first)
		}
	}
	if len(first.Nodes) < 30 {
		t.Errorf("only %d nodes", len(first.Nodes))
	}
}

// buildWide creates top×sub directories with files files each.
func buildWide(t testing.TB, root string, top, sub, files int) {
	var wg sync.WaitGroup
	errs := make(chan error, top)
	for i := range top {
		wg.Go(func() {
			for j := range sub {
				d := filepath.Join(root, fmt.Sprintf("t%03d", i), fmt.Sprintf("s%03d", j))
				if err := os.MkdirAll(d, 0o755); err != nil {
					errs <- err
					return
				}
				for k := range files {
					if err := os.WriteFile(filepath.Join(d, fmt.Sprintf("f%02d", k)), []byte("macdoc fixture"), 0o644); err != nil {
						errs <- err
						return
					}
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestCancel(t *testing.T) {
	root := t.TempDir()
	buildWide(t, root, 40, 25, 4)
	full := scan(t, scanner.Options{Root: root})

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var canceledAt atomic.Int64
	var calls atomic.Int64
	res, err := scanner.Scan(ctx, scanner.Options{
		Root:    root,
		Workers: 4,
		Keep: func(string) bool {
			if calls.Add(1) == 20 {
				canceledAt.Store(time.Now().UnixNano())
				cancel()
			}
			return false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(time.Unix(0, canceledAt.Load()))
	if canceledAt.Load() == 0 {
		t.Fatal("never canceled")
	}
	if took > time.Second {
		t.Errorf("returned %v after cancel", took)
	}
	if !res.Incomplete {
		t.Error("not marked incomplete")
	}
	if res.TotalBytes <= 0 || res.TotalBytes >= full.TotalBytes || res.TotalFiles >= full.TotalFiles {
		t.Errorf("partial %d/%d vs full %d/%d", res.TotalBytes, res.TotalFiles, full.TotalBytes, full.TotalFiles)
	}
	if len(res.Nodes) == 0 || res.Nodes[0].Path != root {
		t.Errorf("root missing from partial nodes: %v", paths(res))
	}
	waitGoroutines(t, before)
}

func TestProgress(t *testing.T) {
	root := t.TempDir()
	buildWide(t, root, 10, 10, 3)
	before := runtime.NumGoroutine()
	var inflight, maxInflight, calls atomic.Int64
	var mu sync.Mutex
	var got []scanner.Progress
	start := time.Now()
	res := scan(t, scanner.Options{
		Root:          root,
		MapPrefix:     root,
		Workers:       4,
		ProgressEvery: 5 * time.Millisecond,
		Keep: func(string) bool {
			time.Sleep(2 * time.Millisecond) // slow the walk so ticks happen
			return false
		},
		Progress: func(p scanner.Progress) {
			n := inflight.Add(1)
			if n > maxInflight.Load() {
				maxInflight.Store(n)
			}
			calls.Add(1)
			mu.Lock()
			got = append(got, p)
			mu.Unlock()
			time.Sleep(time.Millisecond)
			inflight.Add(-1)
		},
	})
	elapsed := time.Since(start)
	waitGoroutines(t, before)
	n := calls.Load()
	if n == 0 {
		t.Fatal("Progress never called")
	}
	if maxInflight.Load() > 1 {
		t.Error("Progress called concurrently")
	}
	if limit := int64(elapsed/(5*time.Millisecond)) + 1; n > limit {
		t.Errorf("%d calls in %v; at most %d", n, elapsed, limit)
	}
	var last scanner.Progress
	for _, p := range got {
		if !strings.HasPrefix(p.CurrentDir, "/") || strings.HasPrefix(p.CurrentDir, root) {
			t.Errorf("CurrentDir %q not mapped", p.CurrentDir)
		}
		if p.Files < last.Files || p.Bytes < last.Bytes || p.Elapsed < last.Elapsed {
			t.Errorf("progress went backwards: %+v after %+v", p, last)
		}
		if p.Files > res.TotalFiles {
			t.Errorf("progress files %d > total %d", p.Files, res.TotalFiles)
		}
		last = p
	}
}

func TestSetBackgroundPriority(t *testing.T) {
	if err := scanner.SetBackgroundPriority(); err != nil {
		t.Fatal(err)
	}
}

// TestRealCaches is a read-only smoke test against the real
// ~/Library/Caches. Opt in with MACDOC_REAL_SCAN=1.
func TestRealCaches(t *testing.T) {
	if os.Getenv("MACDOC_REAL_SCAN") != "1" {
		t.Skip("set MACDOC_REAL_SCAN=1 to scan ~/Library/Caches")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	res := scan(t, scanner.Options{Root: filepath.Join(home, "Library/Caches")})
	t.Logf("%d bytes, %d files, %d nodes, %d unmeasured in %v", res.TotalBytes, res.TotalFiles, len(res.Nodes), len(res.Unmeasured), res.Duration)
	for i, n := range res.Nodes {
		if i == 10 {
			break
		}
		t.Logf("%12d %s", n.Bytes, n.Path)
	}
}

// BenchmarkScanSynthetic scans ~100k small files in ~10k directories (set
// MACDOC_BENCH_FULL=1 for ~200k files in ~20k directories; building that
// tree takes about 5 minutes) and reports peak heap in use, sampled while
// the scan runs. The tree is built once; only the scan is timed.
func BenchmarkScanSynthetic(b *testing.B) {
	top := 100
	if os.Getenv("MACDOC_BENCH_FULL") == "1" {
		top = 200
	}
	root := b.TempDir()
	buildWide(b, root, top, 100, 10)
	b.Run("scan", func(b *testing.B) {
		b.ReportAllocs()
		runtime.GC()
		var peak atomic.Uint64
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			var ms runtime.MemStats
			for {
				runtime.ReadMemStats(&ms)
				if ms.HeapInuse > peak.Load() {
					peak.Store(ms.HeapInuse)
				}
				select {
				case <-stop:
					return
				case <-time.After(2 * time.Millisecond):
				}
			}
		})
		var res scanner.Result
		b.ResetTimer()
		for b.Loop() {
			var err error
			res, err = scanner.Scan(context.Background(), scanner.Options{Root: root})
			if err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		close(stop)
		wg.Wait()
		if res.TotalFiles != int64(top*100*10) {
			b.Fatalf("files %d", res.TotalFiles)
		}
		b.ReportMetric(float64(peak.Load())/(1<<20), "peak-heap-MB")
	})
}
