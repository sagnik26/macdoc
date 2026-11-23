package recipes

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/safety"
)

func init() { Register("ds_store", DetectDSStore) }

// SweepFile is one .DS_Store a sweep found.
type SweepFile struct {
	Path  string
	Bytes int64 // allocated on disk
}

// WalkDSStore calls fn for every regular file named exactly .DS_Store below
// root, and nothing else (ADR 0014 §4). It never follows a symlink, never
// enters a folder safety.SweepSkipsDir names, a folder skip reports, or a
// folder on another volume, and ignores what it can't read. The detector and
// the executor both use it, so the scan and the cleanup agree.
func WalkDSStore(ctx context.Context, root string, skip func(dir string) bool, fn func(SweepFile) error) error {
	var rootDev uint64
	if fi, err := os.Lstat(root); err == nil {
		rootDev, _ = devOf(fi)
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return enterDir(root, path, d, rootDev, skip)
		}
		if d.Name() != safety.DSStoreName || !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		return fn(SweepFile{Path: path, Bytes: allocated(fi)})
	})
}

// enterDir decides whether the walk goes into a folder.
func enterDir(root, path string, d fs.DirEntry, rootDev uint64, skip func(string) bool) error {
	if path == root {
		return nil
	}
	if safety.SweepSkipsDir(d.Name()) || (skip != nil && skip(path)) {
		return fs.SkipDir
	}
	fi, err := d.Info()
	if err != nil {
		return fs.SkipDir
	}
	if dev, ok := devOf(fi); !ok || dev != rootDev {
		return fs.SkipDir
	}
	return nil
}

func devOf(fi os.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:gosec // G115: device numbers are non-negative
}

func allocated(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return fi.Size()
}

// DetectDSStore emits one finding per folder directly in the home folder that
// holds .DS_Store files: "Finder view files in ~/Projects", with the count in
// the note. Hidden folders and Library are left out (safety.Policy.CheckSweepRoot
// refuses them too), and a folder with no such files gives nothing.
func DetectDSStore(ctx context.Context, env Env, _ Recipe) ([]Finding, error) {
	entries, err := os.ReadDir(env.Home)
	if err != nil {
		return nil, nil // an unreadable home gives no suggestion
	}
	var out []Finding
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || e.Type()&fs.ModeSymlink != 0 || strings.HasPrefix(name, ".") || strings.EqualFold(name, "Library") {
			continue
		}
		root := filepath.Join(env.Home, name)
		var n int
		var total int64
		if err := WalkDSStore(ctx, root, nil, func(f SweepFile) error {
			n++
			total += f.Bytes
			return nil
		}); err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		out = append(out, Finding{
			Target: root,
			Title:  fmt.Sprintf("Finder view files in ~/%s", name),
			Bytes:  model.Bytes{Value: total, Source: model.SourceMeasured},
			Note:   fmt.Sprintf("%d %s", n, plural(n, "file", "files")),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
