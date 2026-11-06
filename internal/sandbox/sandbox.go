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

// EnvVar is the environment variable that enables sandbox mode.
const EnvVar = "MACDOC_SANDBOX"

// Root returns the canonical sandbox directory and true if sandbox mode is
// active. Sandbox mode is inactive only when the variable is unset. It
// returns active and an error if the variable is set but invalid: empty or
// whitespace, relative, missing, not a directory, "/", $HOME or an ancestor
// of it, equal to or containing a system root (safety.SystemRoots), or
// within a system root other than strictly below /private/var/folders or
// /private/tmp (temp dirs).
func Root() (string, bool, error) {
	v, set := os.LookupEnv(EnvVar)
	if !set {
		return "", false, nil
	}
	if strings.TrimSpace(v) == "" {
		return "", true, fmt.Errorf("sandbox: %s is set but empty; unset it to disable sandbox mode", EnvVar)
	}
	root, err := resolveRoot(v)
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
	if !samePath(c, path) {
		return fmt.Errorf("sandbox: refusing non-canonical %q (canonical %q)", path, c)
	}
	if !safety.Within(root, c) || samePath(root, c) {
		return fmt.Errorf("sandbox: %q is not inside sandbox root %s", path, root)
	}
	return nil
}

// resolveRoot validates the env var value and returns the canonical,
// fully resolved sandbox directory.
func resolveRoot(v string) (string, error) {
	if !filepath.IsAbs(v) {
		return "", fmt.Errorf("sandbox: %s=%q must be an absolute path", EnvVar, v)
	}
	c, err := safety.Canonicalize(cleanRoot(v))
	if err != nil {
		return "", fmt.Errorf("sandbox: %s: %w", EnvVar, err)
	}
	// Canonicalize does not follow a final symlink. The executor compares
	// canonical targets against the root, so the root must be the real
	// directory, not a link to it.
	r, err := filepath.EvalSymlinks(c)
	if err != nil {
		return "", fmt.Errorf("sandbox: %s=%q: %w", EnvVar, v, err)
	}
	if r, err = safety.Canonicalize(r); err != nil {
		return "", fmt.Errorf("sandbox: %s: %w", EnvVar, err)
	}
	fi, err := os.Stat(r)
	if err != nil {
		return "", fmt.Errorf("sandbox: %s=%q: %w", EnvVar, v, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("sandbox: %s=%q is not a directory", EnvVar, v)
	}
	if r == "/" {
		return "", fmt.Errorf("sandbox: %s must not be the filesystem root", EnvVar)
	}
	home, err := canonicalHome()
	if err != nil {
		return "", err
	}
	if safety.Within(r, home) {
		if samePath(r, home) {
			return "", fmt.Errorf("sandbox: %s must not be the home directory %s", EnvVar, home)
		}
		return "", fmt.Errorf("sandbox: %s=%s contains the home directory %s; use a dedicated temporary directory", EnvVar, r, home)
	}
	if err := CheckSystemRoots(r); err != nil {
		return "", err
	}
	return r, nil
}

// tempRoots are the only system areas a sandbox may live in.
var tempRoots = []string{"/private/var/folders", "/private/tmp"}

// CheckSystemRoots rejects a resolved sandbox root that equals, contains or
// is within a system root, unless it is strictly below a temp dir.
func CheckSystemRoots(r string) error {
	for _, s := range safety.SystemRoots() {
		if safety.Within(r, s) {
			return fmt.Errorf("sandbox: %s=%s covers system path %s; use a dedicated temporary directory", EnvVar, r, s)
		}
	}
	for _, t := range tempRoots {
		if safety.Within(t, r) && !samePath(t, r) {
			return nil
		}
	}
	for _, s := range safety.SystemRoots() {
		if safety.Within(s, r) {
			return fmt.Errorf("sandbox: %s=%s is inside system path %s; use a dedicated temporary directory", EnvVar, r, s)
		}
	}
	return nil
}

// cleanRoot lexically cleans the env var value so a trailing "/" or "/." is
// accepted for the root. A value with a ".." component is returned unchanged
// so that safety.Canonicalize rejects it.
func cleanRoot(v string) string {
	for _, c := range strings.Split(v, "/") {
		if c == ".." {
			return v
		}
	}
	return filepath.Clean(v)
}

// canonicalHome returns the user's home directory, canonicalized and with
// any final symlink resolved, so it compares like the sandbox root.
func canonicalHome() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sandbox: cannot determine home directory: %w", err)
	}
	if !filepath.IsAbs(h) {
		return "", fmt.Errorf("sandbox: home directory %q is not absolute", h)
	}
	c, err := safety.Canonicalize(cleanRoot(h))
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

// samePath reports whether two canonical paths are equal under the same
// comparison rules as safety.Within.
func samePath(a, b string) bool {
	return safety.Within(a, b) && safety.Within(b, a)
}
