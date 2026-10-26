// Package safety decides whether a filesystem path may ever be a cleanup
// target. It is the first line of defence described in docs/SAFETY_MODEL.md
// §3 and is used by the planner and (again, at execution time) the executor.
//
// CONTRACT (tech lead): the exported API below is fixed. Implementation is
// task P0-W1 (docs/tasks/P0-W1.md).
package safety

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

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
	extraProtected []string // canonical extra protected paths

	homeComps       []string        // folded components of home
	containersComps []string        // folded components of home/Library/Containers
	roots           []protectedRoot // system, home-relative and extra roots
}

// NewPolicy builds a policy for the given home directory. home and every
// extraProtected entry must be absolute; they are canonicalized. "~" prefixes
// in extraProtected expand to home.
func NewPolicy(home string, extraProtected []string) (*Policy, error) {
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("safety: home %q is not absolute", home)
	}
	h, err := resolveFull(home)
	if err != nil {
		return nil, fmt.Errorf("safety: home: %w", err)
	}
	if len(components(h)) < 2 {
		return nil, fmt.Errorf("safety: home %q must have at least 2 components", h)
	}

	p := &Policy{
		home:            h,
		homeComps:       foldedComponents(h),
		containersComps: foldedComponents(filepath.Join(h, "Library", "Containers")),
	}
	for _, r := range systemRoots {
		p.roots = append(p.roots, newRoot(r, true))
	}
	for _, rel := range homeProtected {
		if err := p.addRoot(filepath.Join(h, rel)); err != nil {
			return nil, err
		}
	}
	for _, e := range extraProtected {
		e = homeRelative(h, e)
		if !filepath.IsAbs(e) {
			return nil, fmt.Errorf("safety: protected path %q is not absolute", e)
		}
		n := len(p.roots)
		if err := p.addRoot(e); err != nil {
			return nil, err
		}
		for _, r := range p.roots[n:] {
			p.extraProtected = append(p.extraProtected, r.path)
		}
	}
	return p, nil
}

// addRoot protects path both as canonicalized (the symlink itself, if it is
// one) and fully resolved (where a symlink points).
func (p *Policy) addRoot(path string) error {
	c, err := Canonicalize(path)
	if err != nil {
		return fmt.Errorf("safety: protected path: %w", err)
	}
	p.roots = append(p.roots, newRoot(c, false))
	r, err := resolveFull(c)
	if err != nil {
		return fmt.Errorf("safety: protected path: %w", err)
	}
	if r != c {
		p.roots = append(p.roots, newRoot(r, false))
	}
	return nil
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
	switch {
	case path == "":
		return "", &Violation{Path: path, Rule: RuleInvalid, Detail: "empty path"}
	case strings.ContainsRune(path, 0):
		return "", &Violation{Path: path, Rule: RuleInvalid, Detail: "path contains a NUL byte"}
	case !filepath.IsAbs(path):
		return "", &Violation{Path: path, Rule: RuleNotAbsolute, Detail: "path is relative"}
	case hasDotDot(path):
		return "", &Violation{Path: path, Rule: RuleInvalid, Detail: "path contains .."}
	}
	clean := filepath.Clean(path)
	if clean == "/" {
		return clean, nil
	}
	parent, err := resolveParents(filepath.Dir(clean))
	if err != nil {
		return "", &Violation{Path: path, Rule: RuleInvalid, Detail: "cannot resolve parent: " + err.Error()}
	}
	return norm.NFC.String(filepath.Join(parent, filepath.Base(clean))), nil
}

// Within reports whether path equals root or is a descendant of it. Both must
// already be canonical. The comparison is component-wise (so "/a/bc" is not
// within "/a/b"), Unicode NFC, and case-insensitive.
func Within(root, path string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false
	}
	return hasPrefixComps(foldedComponents(root), foldedComponents(path))
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
	c, err := Canonicalize(path)
	if err != nil {
		return err
	}
	if prot, root := p.isProtected(c); prot {
		return &Violation{Path: c, Rule: RuleProtected, Detail: "inside protected path " + root}
	}
	pc := foldedComponents(c)
	if !p.depthOK(pc) {
		return &Violation{Path: c, Rule: RuleTooShallow, Detail: "must be at least 2 components below " + p.home}
	}
	if !p.matchAllow(pc, allow) {
		return &Violation{Path: c, Rule: RuleNotAllowed, Detail: "matches no allow pattern"}
	}
	return nil
}

// IsProtected reports whether a canonical path is inside a protected area,
// and if so which protected root matched.
func (p *Policy) IsProtected(path string) (bool, string) {
	return p.isProtected(path)
}
