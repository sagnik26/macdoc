package fstree

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func stat(t *testing.T, p string) *syscall.Stat_t {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t)
}

func TestFileSizes(t *testing.T) {
	root := t.TempDir()
	Build(t, root,
		File("a/b/empty", 0),
		File("a/b/small", 100),
		File("big", 3<<20),
		Sparse("sparse", 256<<20),
	)
	for p, want := range map[string]int64{"a/b/empty": 0, "a/b/small": 100, "big": 3 << 20, "sparse": 256 << 20} {
		fi, err := os.Stat(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() != want || !fi.Mode().IsRegular() {
			t.Errorf("%s: size %d mode %v; want regular %d", p, fi.Size(), fi.Mode(), want)
		}
	}
	// st_blocks is in 512-byte units.
	if b := stat(t, filepath.Join(root, "big")).Blocks * 512; b < 3<<20 {
		t.Errorf("big: allocated %d bytes; want >= %d", b, 3<<20)
	}
	if b := stat(t, filepath.Join(root, "sparse")).Blocks * 512; b > 1<<20 {
		t.Errorf("sparse: allocated %d bytes; want ~0", b)
	}
	data, err := os.ReadFile(filepath.Join(root, "a/b/small"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\x00") != 0 {
		t.Error("allocated file contains zero bytes")
	}
}

func TestDirAndParents(t *testing.T) {
	root := t.TempDir()
	Build(t, root, Dir("x/y/z"), Dir("x/y/z"), File("x/f", 1))
	for _, p := range []string{"x", "x/y", "x/y/z"} {
		fi, err := os.Stat(filepath.Join(root, p))
		if err != nil || !fi.IsDir() {
			t.Errorf("%s: %v, %v", p, fi, err)
		}
	}
}

func TestSymlinkNotFollowed(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	Build(t, root,
		Symlink("links/out", outside),
		Symlink("links/rel", "../target"),
		Symlink("links/dangling", "/nonexistent/macdoc"),
		File("target", 10),
	)
	for name, want := range map[string]string{"out": outside, "rel": "../target", "dangling": "/nonexistent/macdoc"} {
		p := filepath.Join(root, "links", name)
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is not a symlink: %v", name, fi.Mode())
		}
		got, err := os.Readlink(p)
		if err != nil || got != want {
			t.Errorf("%s -> %q, %v; want %q", name, got, err, want)
		}
	}
	// Nothing was created at the far end of the link.
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Errorf("outside dir has %v, %v; want empty", entries, err)
	}
}

func TestHardlink(t *testing.T) {
	root := t.TempDir()
	Build(t, root, File("orig", 4096), Hardlink("dir/link", "orig"))
	a, b := stat(t, filepath.Join(root, "orig")), stat(t, filepath.Join(root, "dir/link"))
	if a.Nlink != 2 || b.Nlink != 2 {
		t.Errorf("nlink = %d, %d; want 2, 2", a.Nlink, b.Nlink)
	}
	if a.Ino != b.Ino {
		t.Errorf("inodes differ: %d vs %d", a.Ino, b.Ino)
	}
}

func TestModeAndCleanup(t *testing.T) {
	root := t.TempDir()
	t.Run("build", func(t *testing.T) {
		Build(t, root,
			Dir("locked").WithMode(0o000),
			File("locked/inner/secret", 10).WithMode(0o400),
			Dir("locked/inner").WithMode(0o000),
			File("ro", 10).WithMode(0o444),
			Entry{Path: "lit", Kind: KindFile, Size: 1, Mode: 0o600},
		)
		for p, want := range map[string]os.FileMode{"locked": 0, "ro": 0o444, "lit": 0o600} {
			fi, err := os.Lstat(filepath.Join(root, p))
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != want {
				t.Errorf("%s: mode %v; want %v", p, fi.Mode().Perm(), want)
			}
		}
		if _, err := os.ReadDir(filepath.Join(root, "locked")); err == nil {
			t.Error("locked dir is readable")
		}
	})
	// The subtest's cleanup restored the directories.
	for _, p := range []string{"locked", "locked/inner"} {
		fi, err := os.Stat(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != defaultDirMode {
			t.Errorf("%s: mode after cleanup %v; want %v", p, fi.Mode().Perm(), defaultDirMode)
		}
	}
	fi, err := os.Stat(filepath.Join(root, "locked/inner/secret"))
	if err != nil || fi.Mode().Perm() != 0o400 {
		t.Errorf("secret: %v, %v", fi, err)
	}
}

func TestModTime(t *testing.T) {
	root := t.TempDir()
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	Build(t, root,
		Dir("d").WithModTime(old),
		File("d/f", 10).WithModTime(old.Add(time.Hour)),
		File("d/g", 10),
	)
	for p, want := range map[string]time.Time{"d": old, "d/f": old.Add(time.Hour)} {
		fi, err := os.Stat(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		if !fi.ModTime().Equal(want) {
			t.Errorf("%s: mtime %v; want %v", p, fi.ModTime(), want)
		}
	}
}

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{KindDir: "dir", KindFile: "file", KindSymlink: "symlink", KindHardlink: "hardlink", Kind(42): "Kind(42)"} {
		if k.String() != want {
			t.Errorf("%d.String() = %q; want %q", int(k), k.String(), want)
		}
	}
}

// fakeTB records Fatalf and stops the calling goroutine like testing.T does.
type fakeTB struct {
	testing.TB
	msg      string
	failed   bool
	cleanups []func()
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed = true
	f.msg = fmt.Sprintf(format, args...)
	runtime.Goexit()
}
func (f *fakeTB) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }

func runFake(fn func(tb testing.TB)) *fakeTB {
	f := &fakeTB{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(f)
	}()
	<-done
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
	return f
}

func TestBuildErrors(t *testing.T) {
	root := t.TempDir()
	Build(t, root, File("file", 1), File("exists", 1))
	cases := []struct {
		name  string
		root  string
		entry Entry
		want  string
	}{
		{"relative root", "rel", Dir("x"), "not absolute"},
		{"empty path", root, Dir(""), "stay inside"},
		{"absolute path", root, Dir("/etc/x"), "stay inside"},
		{"dotdot", root, File("../escape", 1), "stay inside"},
		{"dotdot inner", root, File("a/../../escape", 1), "stay inside"},
		{"unknown kind", root, Entry{Path: "u", Kind: Kind(9)}, "unknown kind"},
		{"negative size", root, File("neg", -1), "negative size"},
		{"existing file", root, File("exists", 1), "exists"},
		{"parent is file", root, File("file/child", 1), "not a directory"},
		{"dir over file", root, Dir("file"), "not a directory"},
		{"symlink exists", root, Symlink("exists", "x"), "exists"},
		{"symlink modtime", root, Symlink("l1", "x").WithModTime(time.Now()), "not supported"},
		{"symlink mode", root, Symlink("l2", "x").WithMode(0o700), "not supported"},
		{"hardlink missing", root, Hardlink("h1", "missing"), "no such file"},
		{"hardlink absolute", root, Hardlink("h2", "/etc/hosts"), "relative to the root"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := runFake(func(tb testing.TB) { Build(tb, c.root, c.entry) })
			if !f.failed || !strings.Contains(f.msg, c.want) {
				t.Errorf("failed=%v msg=%q; want failure mentioning %q", f.failed, f.msg, c.want)
			}
		})
	}
}

func TestDevHome(t *testing.T) {
	home := DevHome(t)
	if !filepath.IsAbs(home) {
		t.Fatalf("home %q not absolute", home)
	}
	if r, err := filepath.EvalSymlinks(home); err != nil || r != home {
		t.Errorf("home %q is not symlink-free (%q, %v)", home, r, err)
	}
	dirs := []string{
		"Library/Developer/Xcode/DerivedData/AppA-x",
		"Library/Developer/Xcode/DerivedData/AppB-y",
		"Library/Developer/Xcode/Archives/2025-01-01/App.xcarchive",
		"Library/Caches/com.example.app",
		"Library/Caches/CocoaPods",
		".gradle/caches",
		".android/avd/Pixel.avd",
		".npm/_cacache",
		"code/web/node_modules/x",
		"code/web/.git",
		"Documents",
		".ssh",
		"Library/Mobile Documents",
	}
	for _, d := range dirs {
		if fi, err := os.Stat(filepath.Join(home, d)); err != nil || !fi.IsDir() {
			t.Errorf("missing dir %s: %v", d, err)
		}
	}
	files := []string{
		"code/web/package.json",
		"code/web/node_modules/x/index.js",
		"code/web/.git/HEAD",
		"Documents/notes.txt",
		".ssh/id_ed25519",
		"Library/Mobile Documents/x",
	}
	for _, f := range files {
		if fi, err := os.Stat(filepath.Join(home, f)); err != nil || !fi.Mode().IsRegular() {
			t.Errorf("missing file %s: %v", f, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(home, ".ssh/id_ed25519")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("id_ed25519 mode: %v, %v", fi, err)
	}
}

func TestDevHomeTempDirError(t *testing.T) {
	f := runFake(func(tb testing.TB) { DevHome(&badTempDir{tb}) })
	if !f.failed {
		t.Error("DevHome with failing TempDir did not fail")
	}
}

type badTempDir struct{ testing.TB }

func (b *badTempDir) TempDir() string { return "/nonexistent/macdoc-fstree" }
