package safety

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// resolveParents resolves symlinks in dir and every ancestor of it. It walks
// up to the deepest existing ancestor, resolves that with EvalSymlinks, then
// re-appends the Components that don't exist yet.
func resolveParents(dir string) (string, error) {
	var tail []string
	cur := dir
	for cur != "/" {
		_, err := os.Lstat(cur)
		if err == nil {
			break
		}
		if !missing(err) {
			return "", err
		}
		tail = append(tail, filepath.Base(cur))
		cur = filepath.Dir(cur)
	}
	resolved, err := filepath.EvalSymlinks(cur)
	if err != nil {
		return "", err
	}
	for i := len(tail) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, tail[i])
	}
	return resolved, nil
}

// HasDotDot reports whether the raw path has a ".." component. Such paths are
// rejected rather than cleaned: lexical cleaning disagrees with the kernel
// when ".." follows a symlink, and scanner paths never contain "..".
func HasDotDot(p string) bool {
	for _, c := range strings.Split(p, "/") {
		if c == ".." {
			return true
		}
	}
	return false
}

// missing reports whether err means "nothing exists at this path", including
// the case where an ancestor is a regular file.
func missing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// resolveFull canonicalizes p and, if it exists, also follows a symlink in
// its final component. Used only for policy roots so that a symlinked home or
// protected folder is protected at its real location too.
func resolveFull(p string) (string, error) {
	c, err := Canonicalize(p)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(c); err != nil {
		return c, nil
	}
	r, err := filepath.EvalSymlinks(c)
	if err != nil {
		return "", err
	}
	return norm.NFC.String(r), nil
}

// fold returns the comparison key for one path component: NFC, Unicode case
// folded, NFC again (folding can produce non-composed sequences).
func fold(s string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(s)))
}

// FoldName returns the comparison key for one name on disk: the same folding
// the protected-path rules use. Case-insensitive volumes (APFS by default)
// open ".GIT" as ".git", so checks on names inside a folder compare folded
// names too.
func FoldName(s string) string { return fold(s) }

// Components splits a cleaned absolute path into its non-empty Components.
func Components(p string) []string {
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

// foldedComponents cleans p and returns its folded Components.
func foldedComponents(p string) []string {
	cs := Components(filepath.Clean(p))
	for i, c := range cs {
		cs[i] = fold(c)
	}
	return cs
}

// hasPrefixComps reports whether path starts with all of root's Components.
// Both must already be folded.
func hasPrefixComps(root, path []string) bool {
	if len(path) < len(root) {
		return false
	}
	for i := range root {
		if root[i] != path[i] {
			return false
		}
	}
	return true
}
