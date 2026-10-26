// Package safety decides whether a filesystem path may ever be a cleanup
// target. It is the first line of defence described in docs/SAFETY_MODEL.md
// §3 and is used by the planner and (again, at execution time) the executor.
//
// CONTRACT (tech lead): the exported API below is fixed. Implementation is
// task P0-W1 (docs/tasks/P0-W1.md).
package safety

import (
	"errors"
	"fmt"
)

// ErrNotImplemented is returned by stubs until P0-W1 lands.
var ErrNotImplemented = errors.New("safety: not implemented")

// Rule names reported in a Violation.
const (
	RuleProtected   = "protected"    // inside a protected path (§3)
	RuleTooShallow  = "too-shallow"  // $HOME itself or < 2 components below $HOME
	RuleNotAllowed  = "not-allowed"  // matches none of the recipe allow patterns
	RuleNotAbsolute = "not-absolute" // input path was relative
	RuleInvalid     = "invalid"      // empty, NUL bytes, unresolvable
	RuleOutsideHome = "outside-home" // reserved for callers that require $HOME
)

// Violation explains why a path was rejected.
type Violation struct {
	Path   string // canonical path if known, else the input
	Rule   string // one of the Rule* constants
	Detail string // human-readable reason, e.g. the protected root that matched
}

func (v *Violation) Error() string {
	return fmt.Sprintf("safety: %s rejected (%s): %s", v.Path, v.Rule, v.Detail)
}

// Policy holds the protected-path rules for one user.
type Policy struct {
	home           string   // canonical $HOME
	extraProtected []string //nolint:unused // used once P0-W1 lands
}

// NewPolicy builds a policy for the given home directory. home and every
// extraProtected entry must be absolute; they are canonicalized. "~" prefixes
// in extraProtected expand to home.
func NewPolicy(home string, extraProtected []string) (*Policy, error) {
	return nil, ErrNotImplemented
}

// Home returns the canonical home directory of the policy.
func (p *Policy) Home() string { return p.home }

// Canonicalize returns the canonical form of an absolute path:
//   - rejects relative paths, empty paths and paths containing NUL
//   - cleans "." and ".." lexically
//   - resolves symlinks in every existing ancestor (the final component is NOT
//     followed if it is itself a symlink; Lstat semantics)
//   - Unicode NFC-normalizes the result
//
// Case is preserved; comparisons elsewhere are case-insensitive because APFS
// volumes are case-insensitive by default.
func Canonicalize(path string) (string, error) {
	return "", ErrNotImplemented
}

// Within reports whether path equals root or is a descendant of it. Both must
// already be canonical. The comparison is component-wise (so "/a/bc" is not
// within "/a/b"), Unicode NFC, and case-insensitive.
func Within(root, path string) bool {
	return false
}

// CheckTarget canonicalizes path and returns nil only if it may be a cleanup
// target:
//   - it is not within any protected path (built-in list from
//     docs/SAFETY_MODEL.md §3 plus extraProtected)
//   - it is not $HOME, not a top-level home folder, and is at least 2
//     components below $HOME when under $HOME
//   - it matches at least one allow pattern
//
// Allow patterns are absolute or "~"-prefixed. "*" matches exactly one path
// component (no "/"); "**" matches zero or more components. Matching is
// case-insensitive and NFC-normalized. An empty allow list allows nothing.
//
// On rejection the error is a *Violation.
func (p *Policy) CheckTarget(path string, allow []string) error {
	return ErrNotImplemented
}

// IsProtected reports whether a canonical path is inside a protected area,
// and if so which protected root matched.
func (p *Policy) IsProtected(path string) (bool, string) {
	return false, ""
}
