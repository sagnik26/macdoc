// Package sandbox implements MACDOC_SANDBOX: when set, the executor must
// refuse to touch any path outside the sandbox directory. Used for all
// executor tests and for development (docs/DEVELOPMENT_PLAN.md Phase 0).
//
// CONTRACT (tech lead): the exported API below is fixed. Implementation is
// task P0-W2.
package sandbox

import "errors"

// EnvVar is the environment variable that enables sandbox mode.
const EnvVar = "MACDOC_SANDBOX"

// ErrNotImplemented is returned by stubs until P0-W2 lands.
var ErrNotImplemented = errors.New("sandbox: not implemented")

// Root returns the canonical sandbox directory and true if sandbox mode is
// active. It returns an error if the variable is set but invalid (relative,
// missing, not a directory, or "/" or $HOME).
func Root() (string, bool, error) {
	return "", false, ErrNotImplemented
}

// Check returns nil if sandbox mode is inactive, or if path is inside the
// sandbox root. Otherwise it returns an error. path must be canonical.
func Check(path string) error {
	return ErrNotImplemented
}
