// Package sandbox implements MACDOC_SANDBOX: when set, the executor must
// refuse to touch any path outside the sandbox directory. Used for all
// executor tests and for development (docs/DEVELOPMENT_PLAN.md Phase 0).
//
// The sandbox fails closed: a sandbox that is set but unusable refuses every
// path, and never falls back to the real filesystem.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sagnik26/macdoc/internal/safety"
)

// Env is the environment variable that enables sandbox mode.
const Env = "MACDOC_SANDBOX"

// Root returns the canonical sandbox directory and true if sandbox mode is
// active. Sandbox mode is inactive only when the variable is unset. It
// returns active and an error if the variable is set but invalid: empty or
// whitespace, relative, missing, not a directory, "/", $HOME or an ancestor
// of it, equal to or containing a system root (safety.SystemRoots), or
// within a system root other than strictly below /private/var/folders or
// /private/tmp (temp dirs).
func Root() (string, bool, error) {
	v, set := os.LookupEnv(Env)
	if !set {
		return "", false, nil
	}
	if strings.TrimSpace(v) == "" {
		return "", true, fmt.Errorf("sandbox: %s is set but empty; unset it to disable sandbox mode", Env)
	}
	root, err := resolve(v)
	if err != nil {
		return "", true, err
	}
	return root, true, nil
}

// Check returns nil if sandbox mode is inactive, or if path is inside the
// sandbox root. Otherwise it returns an error. path must be canonical;
// non-canonical input (as judged by safety.Canonicalize) is refused.
func Check(path string) error {
	root, active, err := Root()
	if err != nil {
		// Fail closed: a set but invalid sandbox refuses every path.
		return fmt.Errorf("sandbox: refusing %q: %w", path, err)
	}
	if !active {
		return nil
	}
	c, err := safety.Canonicalize(path)
	if err != nil {
		return fmt.Errorf("sandbox: refusing %q: %w", path, err)
	}
	if !same(c, path) {
		return fmt.Errorf("sandbox: refusing non-canonical %q (canonical %q)", path, c)
	}
	if !safety.Within(root, c) || same(root, c) {
		return fmt.Errorf("sandbox: %q is not inside sandbox root %s", path, root)
	}
	return nil
}

// resolve validates the env var value and returns the canonical,
// fully resolved sandbox directory.
func resolve(v string) (string, error) {
	if !filepath.IsAbs(v) {
		return "", fmt.Errorf("sandbox: %s=%q must be an absolute path", Env, v)
	}
	c, err := safety.Canonicalize(clean(v))
	if err != nil {
		return "", fmt.Errorf("sandbox: %s: %w", Env, err)
	}
	// Canonicalize does not follow a final symlink. The executor compares
	// canonical targets against the root, so the root must be the real
	// directory, not a link to it.
	r, err := filepath.EvalSymlinks(c)
	if err != nil {
		return "", fmt.Errorf("sandbox: %s=%q: %w", Env, v, err)
	}
	if r, err = safety.Canonicalize(r); err != nil {
		return "", fmt.Errorf("sandbox: %s: %w", Env, err)
	}
	fi, err := os.Stat(r)
	if err != nil {
		return "", fmt.Errorf("sandbox: %s=%q: %w", Env, v, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("sandbox: %s=%q is not a directory", Env, v)
	}
	if r == "/" {
		return "", fmt.Errorf("sandbox: %s must not be the filesystem root", Env)
	}
	home, err := canonical()
	if err != nil {
		return "", err
	}
	if safety.Within(r, home) {
		if same(r, home) {
			return "", fmt.Errorf("sandbox: %s must not be the home directory %s", Env, home)
		}
		return "", fmt.Errorf("sandbox: %s=%s contains the home directory %s; use a dedicated temporary directory", Env, r, home)
	}
	if err := System(r); err != nil {
		return "", err
	}
	return r, nil
}

// temps are the only system areas a sandbox may live in.
var temps = []string{"/private/var/folders", "/private/tmp"}

// System rejects a resolved sandbox root that equals, contains or
// is within a system root, unless it is strictly below a temp dir.
func System(r string) error {
	for _, s := range safety.SystemRoots() {
		if safety.Within(r, s) {
			return fmt.Errorf("sandbox: %s=%s covers system path %s; use a dedicated temporary directory", Env, r, s)
		}
	}
	for _, t := range temps {
		if safety.Within(t, r) && !same(t, r) {
			return nil
		}
	}
	for _, s := range safety.SystemRoots() {
		if safety.Within(s, r) {
			return fmt.Errorf("sandbox: %s=%s is inside system path %s; use a dedicated temporary directory", Env, r, s)
		}
	}
	return nil
}

// clean lexically cleans the env var value so a trailing "/" or "/." is
// accepted for the root. A value with a ".." component is returned unchanged
// so that safety.Canonicalize rejects it.
func clean(v string) string {
	for _, c := range strings.Split(v, "/") {
		if c == ".." {
			return v
		}
	}
	return filepath.Clean(v)
}

// canonical returns the user's home directory, canonicalized and with
// any final symlink resolved, so it compares like the sandbox root.
func canonical() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sandbox: cannot determine home directory: %w", err)
	}
	if !filepath.IsAbs(h) {
		return "", fmt.Errorf("sandbox: home directory %q is not absolute", h)
	}
	c, err := safety.Canonicalize(clean(h))
	if err != nil {
		return "", fmt.Errorf("sandbox: home: %w", err)
	}
	if r, err := filepath.EvalSymlinks(c); err == nil {
		if rc, err := safety.Canonicalize(r); err == nil {
			return rc, nil
		}
	}
	// A missing home cannot be an ancestor-or-equal of an existing root
	// except lexically, which the caller still checks against c.
	return c, nil
}

// same reports whether two canonical paths are equal under the same
// comparison rules as safety.Within.
func same(a, b string) bool {
	return safety.Within(a, b) && safety.Within(b, a)
}
