// Package fstree builds fake filesystem trees (typically a fake $HOME) for
// tests. It only creates files under a root the test owns, usually from
// t.TempDir(); it never deletes anything.
package fstree

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Kind is the type of filesystem entry to create.
type Kind int

const (
	KindDir Kind = iota + 1
	KindFile
	KindSymlink
	KindHardlink
)

func (k Kind) String() string {
	switch k {
	case KindDir:
		return "dir"
	case KindFile:
		return "file"
	case KindSymlink:
		return "symlink"
	case KindHardlink:
		return "hardlink"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Entry describes one filesystem object relative to the build root.
type Entry struct {
	Path    string      // relative to root, must stay inside it
	Kind    Kind        // what to create
	Size    int64       // file size in bytes (KindFile)
	Sparse  bool        // KindFile: truncate to Size instead of writing bytes
	Target  string      // KindSymlink: link target (as-is); KindHardlink: existing path relative to root
	Mode    os.FileMode // permission bits; use WithMode (needed for 0o000). Default 0o755 dirs, 0o644 files
	ModTime time.Time   // zero means leave as created
	hasMode bool
}

// Dir creates a directory.
func Dir(p string) Entry { return Entry{Path: p, Kind: KindDir} }

// File creates a regular file with size bytes of real, allocated data.
func File(p string, size int64) Entry { return Entry{Path: p, Kind: KindFile, Size: size} }

// Sparse creates a regular file with the given logical size but (almost) no
// allocated blocks.
func Sparse(p string, logicalSize int64) Entry {
	return Entry{Path: p, Kind: KindFile, Size: logicalSize, Sparse: true}
}

// Symlink creates a symbolic link at p pointing to target. target is used
// verbatim, so it may be relative, absolute or dangling.
func Symlink(p, target string) Entry { return Entry{Path: p, Kind: KindSymlink, Target: target} }

// Hardlink creates p as a hard link to existing, which is relative to the
// root and must be created by an earlier entry.
func Hardlink(p, existing string) Entry { return Entry{Path: p, Kind: KindHardlink, Target: existing} }

// WithMode sets the permission bits, e.g. 0o000 for an unreadable directory.
// Modes are applied after every entry exists, so a locked directory can
// still have children. Directory modes are restored in t.Cleanup.
func (e Entry) WithMode(m os.FileMode) Entry {
	e.Mode = m.Perm()
	e.hasMode = true
	return e
}

// WithModTime sets the modification (and access) time. Not supported for
// symlinks.
func (e Entry) WithModTime(t time.Time) Entry {
	e.ModTime = t
	return e
}

const (
	DefaultDirMode  os.FileMode = 0o755
	defaultFileMode os.FileMode = 0o644
)

// Build creates entries under root in order. Parent directories are created
// automatically. Any error fails the test immediately.
func Build(t testing.TB, root string, entries ...Entry) {
	t.Helper()
	if !filepath.IsAbs(root) {
		t.Fatalf("fstree: root %q is not absolute", root)
		return
	}
	for _, e := range entries {
		if err := create(root, e); err != nil {
			t.Fatalf("fstree: %s %q: %v", e.Kind, e.Path, err)
			return
		}
	}
	// Times first (children are all created), then modes, so a locked
	// directory does not block anything.
	for _, e := range entries {
		if e.ModTime.IsZero() {
			continue
		}
		if err := os.Chtimes(filepath.Join(root, e.Path), e.ModTime, e.ModTime); err != nil {
			t.Fatalf("fstree: set mtime %q: %v", e.Path, err)
			return
		}
	}
	for _, e := range modeOrder(entries) {
		p := filepath.Join(root, e.Path)
		if err := os.Chmod(p, e.Mode); err != nil {
			t.Fatalf("fstree: chmod %q: %v", e.Path, err)
			return
		}
		if e.Kind == KindDir {
			t.Cleanup(func() {
				// Restore so t.TempDir's RemoveAll can descend into it.
				_ = os.Chmod(p, DefaultDirMode)
			})
		}
	}
}

// modeOrder returns the entries that set a mode: non-directories first, then
// directories deepest first, so no chmod is blocked by a locked parent.
func modeOrder(entries []Entry) []Entry {
	var files, dirs []Entry
	for _, e := range entries {
		switch {
		case !e.hasMode && e.Mode == 0:
		case e.Kind == KindDir:
			dirs = append(dirs, e)
		default:
			files = append(files, e)
		}
	}
	sort.SliceStable(dirs, func(i, j int) bool { return depth(dirs[i].Path) > depth(dirs[j].Path) })
	return append(files, dirs...)
}

func depth(p string) int { return strings.Count(filepath.Clean(p), string(filepath.Separator)) }

func create(root string, e Entry) error {
	if e.Path == "" || !filepath.IsLocal(e.Path) {
		return fmt.Errorf("path must be relative and stay inside the root")
	}
	if e.Kind == KindSymlink && !e.ModTime.IsZero() {
		return fmt.Errorf("WithModTime is not supported for symlinks")
	}
	if e.Kind == KindSymlink && (e.hasMode || e.Mode != 0) {
		return fmt.Errorf("WithMode is not supported for symlinks")
	}
	p := filepath.Join(root, e.Path)
	if e.Kind != KindDir {
		if err := os.MkdirAll(filepath.Dir(p), DefaultDirMode); err != nil {
			return err
		}
	}
	switch e.Kind {
	case KindDir:
		return os.MkdirAll(p, DefaultDirMode)
	case KindFile:
		return writeFile(p, e.Size, e.Sparse)
	case KindSymlink:
		return os.Symlink(e.Target, p)
	case KindHardlink:
		if !filepath.IsLocal(e.Target) {
			return fmt.Errorf("hardlink target %q must be relative to the root", e.Target)
		}
		return os.Link(filepath.Join(root, e.Target), p)
	}
	return fmt.Errorf("unknown kind")
}

func writeFile(p string, size int64, sparse bool) (err error) {
	if size < 0 {
		return fmt.Errorf("negative size %d", size)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, defaultFileMode)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	if sparse {
		return f.Truncate(size)
	}
	// Non-zero bytes so the data is really allocated.
	_, err = io.CopyN(f, repeatReader{}, size)
	return err
}

var chunk = bytes.Repeat([]byte("macdoc-fixture\n"), 4096)

// repeatReader yields fixture bytes forever.
type repeatReader struct{}

func (repeatReader) Read(b []byte) (int, error) {
	n := 0
	for n < len(b) {
		n += copy(b[n:], chunk)
	}
	return n, nil
}

// DevHome returns a fresh temporary home directory (canonical, with no
// symlinks in its path) populated with a small but representative developer
// layout: Xcode, CocoaPods, Gradle, Android, npm and a web project, plus
// user data that must never be touched (Documents, .ssh, iCloud).
func DevHome(t testing.TB) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("fstree: %v", err)
		return ""
	}
	home := filepath.Join(base, "Users", "dev")
	Build(t, home,
		File("Library/Developer/Xcode/DerivedData/AppA-x/Build/Intermediates.noindex/main.o", 8192),
		File("Library/Developer/Xcode/DerivedData/AppA-x/info.plist", 256),
		File("Library/Developer/Xcode/DerivedData/AppB-y/Build/Products/Debug/AppB", 4096),
		File("Library/Developer/Xcode/DerivedData/AppB-y/info.plist", 256),
		File("Library/Developer/Xcode/Archives/2025-01-01/App.xcarchive/Info.plist", 512),
		File("Library/Developer/Xcode/Archives/2025-01-01/App.xcarchive/Products/Applications/App.app/App", 4096),
		File("Library/Caches/com.example.app/Cache.db", 4096),
		File("Library/Caches/CocoaPods/Pods/Release/Alamofire/Alamofire.swift", 2048),
		File(".gradle/caches/modules-2/files-2.1/com.example/lib.jar", 4096),
		File(".android/avd/Pixel.avd/config.ini", 128),
		Sparse(".android/avd/Pixel.avd/userdata-qemu.img", 64<<20),
		File(".npm/_cacache/index-v5/00/aa", 512),
		File(".npm/_cacache/content-v2/sha512/00/bb", 4096),
		File("code/web/package.json", 64),
		File("code/web/node_modules/x/index.js", 1024),
		File("code/web/.git/HEAD", 23),
		File("Documents/notes.txt", 100),
		File(".ssh/id_ed25519", 411).WithMode(0o600),
		Dir(".ssh").WithMode(0o700),
		File("Library/Mobile Documents/x", 100),
	)
	return home
}
