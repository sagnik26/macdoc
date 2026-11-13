package inventory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// userFolder reports whether a folder holds programs the user installed. The
// rule is ownership, not a list of names: a folder the user owns, or one the
// group can write (as Homebrew's and admin-installed folders are), counts.
// A root-owned folder the group can't write is part of macOS, so it doesn't.
func userFolder(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == os.Getuid() || fi.Mode().Perm()&0o020 != 0
}

func init() { Register(toolsSource{}) }

// toolsSource lists the programs in the folders on PATH. Whatever a shell can
// run is installed software, whichever installer put it there, so this needs
// no list of names. Only names and permission bits are read; nothing is run.
type toolsSource struct{}

// Name implements Source.
func (toolsSource) Name() string { return "tools" }

// Discover implements Source. A name found in an earlier folder on PATH is
// not listed again, because the earlier one is the one a shell runs.
func (toolsSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	seen := map[string]bool{}
	var out []Software
	for _, dir := range env.BinDirs {
		if !userFolder(dir) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			name := e.Name()
			if name == "" || strings.HasPrefix(name, ".") || seen[name] {
				continue
			}
			path := filepath.Join(dir, name)
			fi, err := os.Stat(path) // follows symlinks; a broken link is skipped
			if err != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
				continue
			}
			seen[name] = true
			out = append(out, Software{
				ID: "tool:" + name, Name: name, Kind: "tool", Source: "tools",
				Installed: fi.ModTime(), Evidence: path,
			})
		}
	}
	return out, nil
}
