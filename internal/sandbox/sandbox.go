// Package sandbox implements MACDOC_SANDBOX: when set, the executor must
// refuse to touch any path outside the sandbox directory. Used for all
// executor tests and for development (docs/DEVELOPMENT_PLAN.md Phase 0).
//
// CONTRACT (tech lead): the exported API below is fixed. Implementation is
// task P0-W2.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"

	"macdoc/internal/safety"
)

// EnvVar is the environment variable that enables sandbox mode.
const EnvVar = "MACDOC_SANDBOX"

// Root returns the canonical sandbox directory and true if sandbox mode is
// active. It returns an error if the variable is set but invalid (relative,
// missing, not a directory, or "/" or $HOME).
func Root() (string, bool, error) {
	v := os.Getenv(EnvVar)
	if v == "" {
		return "", false, nil
	}
	root, err := resolveRoot(v)
	if err != nil {
		return "", true, err
	}
	return root, true, nil
}

// Check returns nil if sandbox mode is inactive, or if path is inside the
// sandbox root. Otherwise it returns an error. path must be canonical.
func Check(path string) error {
	root, active, err := Root()
	if err != nil {
		// Fail closed: a set but invalid sandbox refuses every path.
		return fmt.Errorf("sandbox: refusing %q: %w", path, err)
	}
	if !active {
		return nil
	}
	if !filepath.IsAbs(path) || !safety.Within(root, path) || samePath(root, path) {
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
	c, err := safety.Canonicalize(v)
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
	fi, err := os.Stat(r) //nolint:gosec // G703: the env var is a path by design; Stat is read-only and the result is validated below.
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
	return r, nil
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
	c, err := safety.Canonicalize(h)
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
