// Package safety decides whether a filesystem path may ever be a cleanup
// target. It is the first line of defense described in docs/SAFETY_MODEL.md
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
	RuleOutsideHome = "outside-home" // not within $HOME; CheckTarget never accepts such a path
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

	homeComps  []string        // folded components of home
	containers []protectedRoot // home/Library/Containers, lexical and resolved
	roots      []protectedRoot // system, home-relative and extra roots
}

// NewPolicy builds a policy for the given home directory. home and every
// extraProtected entry must be absolute; they are canonicalized (a trailing
// "/" is ignored). "~" prefixes in extraProtected expand to home.
//
// The resolved home must have at least 2 components and must not be within
// any system root, except strictly below /private/var/folders (temp dirs) or
// /Volumes (homes on external disks).
func NewPolicy(home string, extraProtected []string) (*Policy, error) {
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("safety: home %q is not absolute", home)
	}
	h, err := resolveFull(cleanRoot(home))
	if err != nil {
		return nil, fmt.Errorf("safety: home: %w", err)
	}
	hc := foldedComponents(h)
	if len(hc) < 2 {
		return nil, fmt.Errorf("safety: home %q must have at least 2 components", h)
	}
	if err := checkHomeLocation(h, hc); err != nil {
		return nil, err
	}

	p := &Policy{home: h, homeComps: hc}
	containers := filepath.Join(h, "Library", "Containers")
	p.containers = append(p.containers, newRoot(containers, false))
	rc, err := resolveFull(containers)
	if err != nil {
		return nil, fmt.Errorf("safety: home: %w", err)
	}
	if rc != containers {
		p.containers = append(p.containers, newRoot(rc, false))
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
		e = cleanRoot(homeRelative(h, e))
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

// checkHomeLocation rejects a resolved home inside a system root, apart from
// the homeAllowedUnder exceptions.
func checkHomeLocation(h string, hc []string) error {
	for _, a := range homeAllowedUnder {
		ac := foldedComponents(a)
		if len(hc) > len(ac) && hasPrefixComps(ac, hc) {
			return nil
		}
	}
	for _, r := range systemRoots {
		if hasPrefixComps(foldedComponents(r), hc) {
			return fmt.Errorf("safety: home %q is inside system path %s", h, r)
		}
	}
	return nil
}

// cleanRoot lexically cleans a policy root (home or extra protected path) so
// a trailing "/" is accepted there. Input with a ".." component is returned
// unchanged so that Canonicalize still rejects it.
func cleanRoot(s string) string {
	if hasDotDot(s) {
		return s
	}
	return filepath.Clean(s)
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
//   - rejects any ".." component (lexical cleaning disagrees with the kernel
//     when ".." follows a symlink)
//   - rejects a trailing "/" or a final "." component (other than "/"
//     itself): the kernel follows a final symlink for those, but the
//     canonical form would name the link
//   - cleans inner "." components and repeated "/" lexically
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
	case path != "/" && (strings.HasSuffix(path, "/") || strings.HasSuffix(path, "/.")):
		return "", &Violation{Path: path, Rule: RuleInvalid, Detail: "trailing slash or /."}
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
//   - it does not contain a named protected path (a home-relative or
//     extraProtected root, or the ~/Library/Containers/com.apple.* entries,
//     at their lexical or resolved location)
//   - it is within $HOME (RuleOutsideHome); every macdoc target outside home
//     is acted on only through native tool commands, never by path
//   - it is not $HOME, not a top-level home folder, and is at least 2
//     components below $HOME
//   - it matches at least one allow pattern
//
// Checks run in that order: canonicalize, protected, outside-home, depth,
// allow.
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
	if prot, root := p.isProtectedWith(c, p.currentContainers()); prot {
		detail := "inside protected path " + root
		if strings.HasPrefix(root, containsProtected) {
			detail = root
		}
		return &Violation{Path: c, Rule: RuleProtected, Detail: detail}
	}
	pc := foldedComponents(c)
	if !hasPrefixComps(p.homeComps, pc) {
		return &Violation{Path: c, Rule: RuleOutsideHome, Detail: "not within " + p.home}
	}
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
//
// IMPORTANT: path MUST be canonical, i.e. the result of Canonicalize. The
// check is purely lexical: non-canonical input (a path through a symlinked
// parent, or with "." or ".." components) can miss a protected root that the
// kernel would resolve it to. CheckTarget canonicalizes for you.
func (p *Policy) IsProtected(path string) (bool, string) {
	return p.isProtected(path)
}
